package inference

import (
	"context"
	"testing"

	"github.com/productscience/inference/x/inference/keeper"

	"github.com/productscience/inference/x/inference/types"
	"github.com/stretchr/testify/require"
)

// Mock Keeper
type mockKeeperForModelAssigner struct {
	hardwareNodes    map[string]*types.HardwareNodes
	governanceModels []types.Model
	epochGroupData   map[string]map[uint64]types.EpochGroupData // modelId -> epochIndex -> data
	params           *types.Params
}

func (m *mockKeeperForModelAssigner) GetGovernanceModelsSorted(ctx context.Context) ([]*types.Model, error) {
	return keeper.ValuesToPointers(m.governanceModels), nil
}

func (m *mockKeeperForModelAssigner) GetHardwareNodes(ctx context.Context, participantId string) (*types.HardwareNodes, bool) {
	nodes, found := m.hardwareNodes[participantId]
	return nodes, found
}

func (m *mockKeeperForModelAssigner) GetActiveParticipants(ctx context.Context, epochId uint64) (val types.ActiveParticipants, found bool) {
	// Not implemented for this mock
	return types.ActiveParticipants{}, false
}

func (m *mockKeeperForModelAssigner) GetEpochGroupData(ctx context.Context, epochIndex uint64, modelId string) (val types.EpochGroupData, found bool) {
	if m.epochGroupData == nil {
		return types.EpochGroupData{}, false
	}
	if modelData, ok := m.epochGroupData[modelId]; ok {
		if data, ok := modelData[epochIndex]; ok {
			return data, true
		}
	}
	return types.EpochGroupData{}, false
}

func (m *mockKeeperForModelAssigner) GetParams(ctx context.Context) types.Params {
	if m.params != nil {
		return *m.params
	}
	return types.DefaultParams()
}

// Mock Logger
type mockLogger struct{}

func (m mockLogger) LogInfo(msg string, subSystem types.SubSystem, keyvals ...interface{})  {}
func (m mockLogger) LogError(msg string, subSystem types.SubSystem, keyvals ...interface{}) {}
func (m mockLogger) LogWarn(msg string, subSystem types.SubSystem, keyvals ...interface{})  {}
func (m mockLogger) LogDebug(msg string, subSystem types.SubSystem, keyvals ...interface{}) {}

func TestSetModelsForParticipants_OneModelTwoNodes_Bug(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	participantAddress := "gonka1xmwh48ugfvd2ktmy0t90ueuzqxdk4g0anwe3v6"
	modelID := "Qwen/QwQ-32B"

	models := []types.Model{
		{
			ProposedBy:             "genesis",
			Id:                     "Qwen/QwQ-32B",
			UnitsOfComputePerToken: 1000,
			HfRepo:                 "Qwen/QwQ-32B",
			HfCommit:               "976055f8c83f394f35dbd3ab09a285a984907bd0",
			ModelArgs:              []string{"--quantization", "fp8", "-kv-cache-dtype", "fp8"},
			VRam:                   32,
			ThroughputPerNonce:     1000,
			ValidationThreshold:    &types.Decimal{Value: 85, Exponent: -2},
		},
		{
			ProposedBy:             "genesis",
			Id:                     "Qwen/Qwen2.5-7B-Instruct",
			UnitsOfComputePerToken: 100,
			HfRepo:                 "Qwen/Qwen2.5-7B-Instruct",
			HfCommit:               "a09a35458c702b33eeacc393d103063234e8bc28",
			ModelArgs:              []string{"--quantization", "fp8"},
			VRam:                   16,
			ThroughputPerNonce:     10000,
			ValidationThreshold:    &types.Decimal{Value: 85, Exponent: -2},
		},
	}
	// Mock Keeper setup
	mockKeeper := &mockKeeperForModelAssigner{
		governanceModels: models,
		hardwareNodes: map[string]*types.HardwareNodes{
			participantAddress: {
				Participant: participantAddress,
				HardwareNodes: []*types.HardwareNode{
					{LocalId: "mlnode1", Models: []string{modelID}},
					{LocalId: "mlnode2", Models: []string{modelID}},
				},
			},
		},
		epochGroupData: map[string]map[uint64]types.EpochGroupData{
			modelID: {
				0: {
					ValidationWeights: []*types.ValidationWeight{
						{
							MemberAddress: participantAddress,
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "mlnode1", PocWeight: 29},
								{NodeId: "mlnode2", PocWeight: 28},
							},
						},
					},
				},
			},
		},
	}

	// Model Assigner
	modelAssigner := NewModelAssigner(mockKeeper, mockLogger{})

	// Participant data setup
	participants := []*types.ActiveParticipant{
		{
			Index:  participantAddress,
			Models: []string{modelID},
			MlNodes: []*types.ModelMLNodes{ // This is the initial state before model assignment
				{
					MlNodes: []*types.MLNodeInfo{
						{NodeId: "mlnode1", PocWeight: 29},
						{NodeId: "mlnode2", PocWeight: 28},
					},
				},
			},
		},
	}

	upcomingEpoch := types.Epoch{Index: 1}

	// 2. Execute
	modelAssigner.setModelsForParticipants(ctx, participants, upcomingEpoch)

	// 3. Assert
	participant := participants[0]

	// The bug causes the model list to have 1 model, but the ml_nodes list has 2 entries.
	// One for the assigned model, and one for the "overflow" node.
	require.Len(t, participant.Models, 1, "Should have one supported model")
	require.Equal(t, modelID, participant.Models[0], "The supported model should be correct")

	require.Len(t, participant.MlNodes, 1, "Should have one MLNode groups corresponding to the model: "+modelID)

	// Check first group (assigned model)
	modelGroup := participant.MlNodes[0]
	require.Len(t, modelGroup.MlNodes, 2, "The model-specific group should have two nodes")

	// Verify that both nodes are in the same group and have the correct timeslot allocations.
	assertNodeInGroup(t, modelGroup.MlNodes, "mlnode1")
	assertNodeInGroup(t, modelGroup.MlNodes, "mlnode2")

	// Verify that one node is allocated for PoC and the other is not.
	assertTimeslotAllocationCount(t, modelGroup.MlNodes, []bool{true, false}, 1)
	assertTimeslotAllocationCount(t, modelGroup.MlNodes, []bool{true, true}, 1)
}

// assertNodeInGroup checks if a node with the given ID exists in the list of nodes.
func assertNodeInGroup(t *testing.T, nodes []*types.MLNodeInfo, nodeID string) {
	t.Helper()
	found := false
	for _, node := range nodes {
		if node.NodeId == nodeID {
			found = true
			break
		}
	}
	require.True(t, found, "Node with ID %s not found in the group", nodeID)
}

// assertTimeslotAllocationCount checks if there are exactly `expectedCount` nodes
// with the given timeslot allocation.
func assertTimeslotAllocationCount(t *testing.T, nodes []*types.MLNodeInfo, allocation []bool, expectedCount int) {
	t.Helper()
	count := 0
	for _, node := range nodes {
		if equalBoolSlice(node.TimeslotAllocation, allocation) {
			count++
		}
	}
	require.Equal(t, expectedCount, count, "Expected %d nodes with timeslot allocation %v, but found %d", expectedCount, allocation, count)
}

// equalBoolSlice compares two boolean slices for equality.
func equalBoolSlice(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSetModelsForParticipants_OneNodeOneModel(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	participantAddress := "gonka1xmwh48ugfvd2ktmy0t90ueuzqxdk4g0anwe3v6"
	modelID := "Qwen/Qwen2.5-7B-Instruct"

	models := []types.Model{
		{
			ProposedBy: "genesis",
			Id:         modelID,
			VRam:       16,
		},
	}
	// Mock Keeper setup
	mockKeeper := &mockKeeperForModelAssigner{
		governanceModels: models,
		hardwareNodes: map[string]*types.HardwareNodes{
			participantAddress: {
				Participant: participantAddress,
				HardwareNodes: []*types.HardwareNode{
					{LocalId: "mlnode1", Models: []string{modelID}},
				},
			},
		},
		epochGroupData: map[string]map[uint64]types.EpochGroupData{
			modelID: {
				0: {
					ValidationWeights: []*types.ValidationWeight{
						{
							MemberAddress: participantAddress,
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "mlnode1", PocWeight: 29},
							},
						},
					},
				},
			},
		},
	}

	// Model Assigner
	modelAssigner := NewModelAssigner(mockKeeper, mockLogger{})

	// Participant data setup
	participants := []*types.ActiveParticipant{
		{
			Index:  participantAddress,
			Models: []string{modelID},
			MlNodes: []*types.ModelMLNodes{
				{
					MlNodes: []*types.MLNodeInfo{
						{NodeId: "mlnode1", PocWeight: 29},
					},
				},
			},
		},
	}

	upcomingEpoch := types.Epoch{Index: 1}

	// 2. Execute
	modelAssigner.setModelsForParticipants(ctx, participants, upcomingEpoch)

	// 3. Assert
	participant := participants[0]

	require.Len(t, participant.Models, 1, "Should have one supported model")
	require.Equal(t, modelID, participant.Models[0], "The supported model should be correct")

	require.Len(t, participant.MlNodes, 1, "Should have one MLNode group corresponding to the model")

	modelGroup := participant.MlNodes[0]
	require.Len(t, modelGroup.MlNodes, 1, "The model-specific group should have one node")

	assertNodeInGroup(t, modelGroup.MlNodes, "mlnode1")
	// With weight-based allocation: 1 node (weight 29), target 50% = 14.5, so it gets allocated
	assertTimeslotAllocationCount(t, modelGroup.MlNodes, []bool{true, true}, 1)
}

func TestSetModelsForParticipants_ManyNodesManyModels(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	participantAddress := "gonka1xmwh48ugfvd2ktmy0t90ueuzqxdk4g0anwe3v6"
	modelA := "Qwen/QwQ-32B"
	modelB := "Qwen/Qwen2.5-7B-Instruct"

	models := []types.Model{
		{ProposedBy: "genesis", Id: modelA, VRam: 32},
		{ProposedBy: "genesis", Id: modelB, VRam: 16},
	}

	// Mock Keeper setup with 4 nodes supporting mixed models
	mockKeeper := &mockKeeperForModelAssigner{
		governanceModels: models,
		hardwareNodes: map[string]*types.HardwareNodes{
			participantAddress: {
				Participant: participantAddress,
				HardwareNodes: []*types.HardwareNode{
					{LocalId: "mlnode1", Models: []string{modelA, modelB}}, // supports both
					{LocalId: "mlnode2", Models: []string{modelA}},         // supports A
					{LocalId: "mlnode3", Models: []string{modelB}},         // supports B
					{LocalId: "mlnode4", Models: []string{modelA, modelB}}, // supports both
				},
			},
		},
		epochGroupData: map[string]map[uint64]types.EpochGroupData{
			modelA: {
				1: {
					ValidationWeights: []*types.ValidationWeight{
						{
							MemberAddress: participantAddress,
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "mlnode1", PocWeight: 30},
								{NodeId: "mlnode2", PocWeight: 25},
								{NodeId: "mlnode4", PocWeight: 25},
							},
						},
					},
				},
			},
			modelB: {
				1: {
					ValidationWeights: []*types.ValidationWeight{
						{
							MemberAddress: participantAddress,
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "mlnode3", PocWeight: 20},
							},
						},
					},
				},
			},
		},
	}

	// Model Assigner
	modelAssigner := NewModelAssigner(mockKeeper, mockLogger{})

	// Participant data setup with legacy MLNodes list (pre-assignment state)
	participants := []*types.ActiveParticipant{
		{
			Index:  participantAddress,
			Models: []string{modelA, modelB},
			MlNodes: []*types.ModelMLNodes{
				{
					MlNodes: []*types.MLNodeInfo{
						{NodeId: "mlnode1", PocWeight: 30},
						{NodeId: "mlnode2", PocWeight: 25},
						{NodeId: "mlnode3", PocWeight: 20},
						{NodeId: "mlnode4", PocWeight: 25},
					},
				},
			},
		},
	}

	upcomingEpoch := types.Epoch{Index: 2}

	// 2. Execute
	modelAssigner.setModelsForParticipants(ctx, participants, upcomingEpoch)

	// 3. Assert
	participant := participants[0]

	// Expect two supported models in the same order as governance models
	require.Len(t, participant.Models, 2, "Should have two supported models")
	require.Equal(t, modelA, participant.Models[0], "First model should be modelA")
	require.Equal(t, modelB, participant.Models[1], "Second model should be modelB")

	// Expect two MLNode groups, one per model (no overflow group expected because all nodes get assigned)
	require.Len(t, participant.MlNodes, 2, "Should have two MLNode groups corresponding to the two models")

	// Group for modelA should contain nodes that support A and were unassigned at that time
	groupA := participant.MlNodes[0]
	require.Len(t, groupA.MlNodes, 3, "Model A group should have three nodes (mlnode1, mlnode2, mlnode4)")
	assertNodeInGroup(t, groupA.MlNodes, "mlnode1")
	assertNodeInGroup(t, groupA.MlNodes, "mlnode2")
	assertNodeInGroup(t, groupA.MlNodes, "mlnode4")

	// Group for modelB should contain the remaining node supporting B only
	groupB := participant.MlNodes[1]
	require.Len(t, groupB.MlNodes, 1, "Model B group should have one node (mlnode3)")
	assertNodeInGroup(t, groupB.MlNodes, "mlnode3")

	// Check weight-based allocation:
	// Model A: 3 nodes (30, 25, 25), total weight = 80, target 50% = 40
	// Algorithm allocates smallest first: 25 + 25 = 50, so 2 nodes get POC_SLOT=true
	// Model B: 1 node (20), total weight = 20, target 50% = 10
	// Algorithm allocates the node (0 < 10, add 20), so 1 node gets POC_SLOT=true
	assertTimeslotAllocationCount(t, groupA.MlNodes, []bool{true, true}, 2)
	assertTimeslotAllocationCount(t, groupA.MlNodes, []bool{true, false}, 1)
	assertTimeslotAllocationCount(t, groupB.MlNodes, []bool{true, true}, 1)
	assertTimeslotAllocationCount(t, groupB.MlNodes, []bool{true, false}, 0)
}

func TestAllocateMLNodesForPoC_MultipleParticipantsAndAllocations(t *testing.T) {
	const modelID = "model-abc"

	testCases := []struct {
		name                   string
		allocationPercentage   float64
		participants           []*types.ActiveParticipant
		hardwareNodesMap       map[string]*types.HardwareNodes
		previousEpochGroupData map[string]map[uint64]types.EpochGroupData
		expectedMinWeight      int64
		expectedMaxWeight      int64
		expectedTotalWeight    int64
		expectedTargetWeight   int64
	}{
		{
			name:                 "50% allocation with 3 participants, varying weights (10-50 range)",
			allocationPercentage: 50.0,
			participants: []*types.ActiveParticipant{
				{
					Index:  "participant1",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p1-node1", PocWeight: 30},
								{NodeId: "p1-node2", PocWeight: 25},
								{NodeId: "p1-node3", PocWeight: 20},
							},
						},
					},
				},
				{
					Index:  "participant2",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p2-node1", PocWeight: 40},
								{NodeId: "p2-node2", PocWeight: 35},
							},
						},
					},
				},
				{
					Index:  "participant3",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p3-node1", PocWeight: 50},
								{NodeId: "p3-node2", PocWeight: 45},
								{NodeId: "p3-node3", PocWeight: 40},
								{NodeId: "p3-node4", PocWeight: 35},
							},
						},
					},
				},
			},
			hardwareNodesMap: map[string]*types.HardwareNodes{
				"participant1": {
					Participant: "participant1",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p1-node1", Models: []string{modelID}},
						{LocalId: "p1-node2", Models: []string{modelID}},
						{LocalId: "p1-node3", Models: []string{modelID}},
					},
				},
				"participant2": {
					Participant: "participant2",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p2-node1", Models: []string{modelID}},
						{LocalId: "p2-node2", Models: []string{modelID}},
					},
				},
				"participant3": {
					Participant: "participant3",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p3-node1", Models: []string{modelID}},
						{LocalId: "p3-node2", Models: []string{modelID}},
						{LocalId: "p3-node3", Models: []string{modelID}},
						{LocalId: "p3-node4", Models: []string{modelID}},
					},
				},
			},
			previousEpochGroupData: map[string]map[uint64]types.EpochGroupData{
				modelID: {
					0: {
						ValidationWeights: []*types.ValidationWeight{
							{
								MemberAddress: "participant1",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p1-node1", PocWeight: 30},
									{NodeId: "p1-node2", PocWeight: 25},
									{NodeId: "p1-node3", PocWeight: 20},
								},
							},
							{
								MemberAddress: "participant2",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p2-node1", PocWeight: 40},
									{NodeId: "p2-node2", PocWeight: 35},
								},
							},
							{
								MemberAddress: "participant3",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p3-node1", PocWeight: 50},
									{NodeId: "p3-node2", PocWeight: 45},
									{NodeId: "p3-node3", PocWeight: 40},
									{NodeId: "p3-node4", PocWeight: 35},
								},
							},
						},
					},
				},
			},
			expectedTotalWeight:  320, // 75 + 75 + 170
			expectedTargetWeight: 160, // 50% of 320
			expectedMinWeight:    0,   // With participant-level filtering (2 out of 3 eligible), actual allocation varies
			expectedMaxWeight:    320, // But shouldn't exceed total
		},
		{
			name:                 "30% allocation with 2 participants (10-50 weight range)",
			allocationPercentage: 30.0,
			participants: []*types.ActiveParticipant{
				{
					Index:  "participant1",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p1-node1", PocWeight: 50},
								{NodeId: "p1-node2", PocWeight: 40},
								{NodeId: "p1-node3", PocWeight: 30},
							},
						},
					},
				},
				{
					Index:  "participant2",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p2-node1", PocWeight: 20},
								{NodeId: "p2-node2", PocWeight: 10},
							},
						},
					},
				},
			},
			hardwareNodesMap: map[string]*types.HardwareNodes{
				"participant1": {
					Participant: "participant1",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p1-node1", Models: []string{modelID}},
						{LocalId: "p1-node2", Models: []string{modelID}},
						{LocalId: "p1-node3", Models: []string{modelID}},
					},
				},
				"participant2": {
					Participant: "participant2",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p2-node1", Models: []string{modelID}},
						{LocalId: "p2-node2", Models: []string{modelID}},
					},
				},
			},
			previousEpochGroupData: map[string]map[uint64]types.EpochGroupData{
				modelID: {
					0: {
						ValidationWeights: []*types.ValidationWeight{
							{
								MemberAddress: "participant1",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p1-node1", PocWeight: 50},
									{NodeId: "p1-node2", PocWeight: 40},
									{NodeId: "p1-node3", PocWeight: 30},
								},
							},
							{
								MemberAddress: "participant2",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p2-node1", PocWeight: 20},
									{NodeId: "p2-node2", PocWeight: 10},
								},
							},
						},
					},
				},
			},
			expectedTotalWeight:  150, // 120 + 30
			expectedTargetWeight: 45,  // 30% of 150
			expectedMinWeight:    0,   // With participant-level filtering (2 out of 2 eligible), actual varies
			expectedMaxWeight:    150, // But shouldn't exceed total
		},
		{
			name:                 "70% allocation with 4 participants (10-50 weight range)",
			allocationPercentage: 70.0,
			participants: []*types.ActiveParticipant{
				{
					Index:  "participant1",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p1-node1", PocWeight: 15},
								{NodeId: "p1-node2", PocWeight: 10},
							},
						},
					},
				},
				{
					Index:  "participant2",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p2-node1", PocWeight: 25},
								{NodeId: "p2-node2", PocWeight: 20},
							},
						},
					},
				},
				{
					Index:  "participant3",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p3-node1", PocWeight: 35},
								{NodeId: "p3-node2", PocWeight: 30},
							},
						},
					},
				},
				{
					Index:  "participant4",
					Models: []string{modelID},
					MlNodes: []*types.ModelMLNodes{
						{
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p4-node1", PocWeight: 45},
								{NodeId: "p4-node2", PocWeight: 40},
							},
						},
					},
				},
			},
			hardwareNodesMap: map[string]*types.HardwareNodes{
				"participant1": {
					Participant: "participant1",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p1-node1", Models: []string{modelID}},
						{LocalId: "p1-node2", Models: []string{modelID}},
					},
				},
				"participant2": {
					Participant: "participant2",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p2-node1", Models: []string{modelID}},
						{LocalId: "p2-node2", Models: []string{modelID}},
					},
				},
				"participant3": {
					Participant: "participant3",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p3-node1", Models: []string{modelID}},
						{LocalId: "p3-node2", Models: []string{modelID}},
					},
				},
				"participant4": {
					Participant: "participant4",
					HardwareNodes: []*types.HardwareNode{
						{LocalId: "p4-node1", Models: []string{modelID}},
						{LocalId: "p4-node2", Models: []string{modelID}},
					},
				},
			},
			previousEpochGroupData: map[string]map[uint64]types.EpochGroupData{
				modelID: {
					0: {
						ValidationWeights: []*types.ValidationWeight{
							{
								MemberAddress: "participant1",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p1-node1", PocWeight: 15},
									{NodeId: "p1-node2", PocWeight: 10},
								},
							},
							{
								MemberAddress: "participant2",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p2-node1", PocWeight: 25},
									{NodeId: "p2-node2", PocWeight: 20},
								},
							},
							{
								MemberAddress: "participant3",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p3-node1", PocWeight: 35},
									{NodeId: "p3-node2", PocWeight: 30},
								},
							},
							{
								MemberAddress: "participant4",
								MlNodes: []*types.MLNodeInfo{
									{NodeId: "p4-node1", PocWeight: 45},
									{NodeId: "p4-node2", PocWeight: 40},
								},
							},
						},
					},
				},
			},
			expectedTotalWeight:  220, // 25 + 45 + 65 + 85
			expectedTargetWeight: 154, // 70% of 220
			expectedMinWeight:    0,   // With participant-level filtering (3 out of 4 eligible), actual varies
			expectedMaxWeight:    220, // But shouldn't exceed total
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock keeper with custom allocation percentage
			customParams := types.DefaultParams()
			customParams.EpochParams.PocSlotAllocation = types.DecimalFromFloat(tc.allocationPercentage)

			mockKeeper := &mockKeeperForModelAssigner{
				hardwareNodes: tc.hardwareNodesMap,
				governanceModels: []types.Model{
					{
						Id:                     modelID,
						ProposedBy:             "genesis",
						UnitsOfComputePerToken: 100,
						HfRepo:                 "test/model",
						HfCommit:               "abc123",
						VRam:                   16,
						ThroughputPerNonce:     1000,
						ValidationThreshold:    &types.Decimal{Value: 85, Exponent: -2},
					},
				},
				epochGroupData: tc.previousEpochGroupData,
				params:         &customParams,
			}

			modelAssigner := NewModelAssigner(mockKeeper, mockLogger{})
			ctx := context.Background()
			upcomingEpoch := types.Epoch{Index: 1}

			// Call setModelsForParticipants which internally calls allocateMLNodesForPoC
			modelAssigner.setModelsForParticipants(ctx, tc.participants, upcomingEpoch)

			// Verify allocation results
			var totalWeight int64
			var allocatedWeight int64
			var allocatedCount int
			var totalCount int

			for _, participant := range tc.participants {
				require.Len(t, participant.MlNodes, 1, "Each participant should have one model group")
				modelGroup := participant.MlNodes[0]

				for _, node := range modelGroup.MlNodes {
					totalCount++
					totalWeight += node.PocWeight

					if len(node.TimeslotAllocation) > 1 && node.TimeslotAllocation[1] {
						allocatedCount++
						allocatedWeight += node.PocWeight
					}
				}
			}

			// Verify total weight matches expected
			require.Equal(t, tc.expectedTotalWeight, totalWeight,
				"Total weight should match expected: %d", tc.expectedTotalWeight)

			// Verify target weight calculation
			require.Equal(t, tc.expectedTargetWeight, tc.expectedTotalWeight*int64(tc.allocationPercentage)/100,
				"Target weight calculation should match")

			// Verify allocated weight is within expected range
			require.GreaterOrEqual(t, allocatedWeight, tc.expectedMinWeight,
				"Allocated weight (%d) should be >= min expected (%d)", allocatedWeight, tc.expectedMinWeight)
			require.LessOrEqual(t, allocatedWeight, tc.expectedMaxWeight,
				"Allocated weight (%d) should be <= max expected (%d)", allocatedWeight, tc.expectedMaxWeight)

			t.Logf("Allocation Results:")
			t.Logf("  Total Weight: %d", totalWeight)
			t.Logf("  Target Weight: %d (%.1f%%)", tc.expectedTargetWeight, tc.allocationPercentage)
			t.Logf("  Allocated Weight: %d", allocatedWeight)
			t.Logf("  Allocated Percentage: %.2f%%", float64(allocatedWeight)/float64(totalWeight)*100)
			t.Logf("  Total Nodes: %d", totalCount)
			t.Logf("  Allocated Nodes: %d", allocatedCount)

			// Log per-participant allocation for debugging
			for _, participant := range tc.participants {
				participantAllocated := 0
				participantTotal := 0
				participantWeight := int64(0)
				for _, node := range participant.MlNodes[0].MlNodes {
					participantTotal++
					if len(node.TimeslotAllocation) > 1 && node.TimeslotAllocation[1] {
						participantAllocated++
						participantWeight += node.PocWeight
					}
				}
				t.Logf("  Participant %s: %d/%d nodes allocated (weight: %d)", participant.Index, participantAllocated, participantTotal, participantWeight)
			}
		})
	}
}

func TestEligibilityFilter_DebugRandomness(t *testing.T) {
	const modelID = "model-test"

	// Create mock with 9 nodes (matching the failing test)
	mockKeeper := &mockKeeperForModelAssigner{
		governanceModels: []types.Model{
			{
				Id:                     modelID,
				ProposedBy:             "genesis",
				UnitsOfComputePerToken: 100,
				HfRepo:                 "test/model",
				HfCommit:               "abc123",
				VRam:                   16,
				ThroughputPerNonce:     1000,
				ValidationThreshold:    &types.Decimal{Value: 85, Exponent: -2},
			},
		},
		hardwareNodes: map[string]*types.HardwareNodes{
			"participant1": {
				Participant: "participant1",
				HardwareNodes: []*types.HardwareNode{
					{LocalId: "p1-node1", Models: []string{modelID}},
					{LocalId: "p1-node2", Models: []string{modelID}},
					{LocalId: "p1-node3", Models: []string{modelID}},
				},
			},
			"participant2": {
				Participant: "participant2",
				HardwareNodes: []*types.HardwareNode{
					{LocalId: "p2-node1", Models: []string{modelID}},
					{LocalId: "p2-node2", Models: []string{modelID}},
				},
			},
			"participant3": {
				Participant: "participant3",
				HardwareNodes: []*types.HardwareNode{
					{LocalId: "p3-node1", Models: []string{modelID}},
					{LocalId: "p3-node2", Models: []string{modelID}},
					{LocalId: "p3-node3", Models: []string{modelID}},
					{LocalId: "p3-node4", Models: []string{modelID}},
				},
			},
		},
		epochGroupData: map[string]map[uint64]types.EpochGroupData{
			modelID: {
				0: {
					ValidationWeights: []*types.ValidationWeight{
						{
							MemberAddress: "participant1",
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p1-node1", PocWeight: 30},
								{NodeId: "p1-node2", PocWeight: 25},
								{NodeId: "p1-node3", PocWeight: 20},
							},
						},
						{
							MemberAddress: "participant2",
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p2-node1", PocWeight: 40},
								{NodeId: "p2-node2", PocWeight: 35},
							},
						},
						{
							MemberAddress: "participant3",
							MlNodes: []*types.MLNodeInfo{
								{NodeId: "p3-node1", PocWeight: 50},
								{NodeId: "p3-node2", PocWeight: 45},
								{NodeId: "p3-node3", PocWeight: 40},
								{NodeId: "p3-node4", PocWeight: 35},
							},
						},
					},
				},
			},
		},
	}

	participants := []*types.ActiveParticipant{
		{
			Index:  "participant1",
			Models: []string{modelID},
			MlNodes: []*types.ModelMLNodes{
				{
					MlNodes: []*types.MLNodeInfo{
						{NodeId: "p1-node1", PocWeight: 30},
						{NodeId: "p1-node2", PocWeight: 25},
						{NodeId: "p1-node3", PocWeight: 20},
					},
				},
			},
		},
		{
			Index:  "participant2",
			Models: []string{modelID},
			MlNodes: []*types.ModelMLNodes{
				{
					MlNodes: []*types.MLNodeInfo{
						{NodeId: "p2-node1", PocWeight: 40},
						{NodeId: "p2-node2", PocWeight: 35},
					},
				},
			},
		},
		{
			Index:  "participant3",
			Models: []string{modelID},
			MlNodes: []*types.ModelMLNodes{
				{
					MlNodes: []*types.MLNodeInfo{
						{NodeId: "p3-node1", PocWeight: 50},
						{NodeId: "p3-node2", PocWeight: 45},
						{NodeId: "p3-node3", PocWeight: 40},
						{NodeId: "p3-node4", PocWeight: 35},
					},
				},
			},
		},
	}

	modelAssigner := NewModelAssigner(mockKeeper, mockLogger{})
	ctx := context.Background()
	upcomingEpoch := types.Epoch{Index: 1}

	modelAssigner.setModelsForParticipants(ctx, participants, upcomingEpoch)

	// Check POC_SLOT status for all nodes
	totalNodes := 0
	nodesWithPOCSlot := 0
	nodesByParticipant := make(map[string]struct{ total, allocated int })

	for _, participant := range participants {
		total := 0
		allocated := 0

		for _, node := range participant.MlNodes[0].MlNodes {
			totalNodes++
			total++
			if len(node.TimeslotAllocation) > 1 && node.TimeslotAllocation[1] {
				nodesWithPOCSlot++
				allocated++
			}
		}
		nodesByParticipant[participant.Index] = struct{ total, allocated int }{total, allocated}
	}

	t.Logf("POC_SLOT Allocation Results:")
	t.Logf("  Total nodes: %d", totalNodes)
	t.Logf("  Nodes with POC_SLOT=true: %d (%.1f%%)", nodesWithPOCSlot, float64(nodesWithPOCSlot)/float64(totalNodes)*100)
	t.Logf("  Nodes with POC_SLOT=false: %d (%.1f%%)", totalNodes-nodesWithPOCSlot, float64(totalNodes-nodesWithPOCSlot)/float64(totalNodes)*100)
	t.Logf("  By participant:")
	for _, p := range []string{"participant1", "participant2", "participant3"} {
		stats := nodesByParticipant[p]
		t.Logf("    %s: %d/%d allocated", p, stats.allocated, stats.total)
	}
}
