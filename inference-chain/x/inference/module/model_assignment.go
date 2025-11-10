package inference

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"slices"

	"github.com/productscience/inference/x/inference/types"
	"github.com/shopspring/decimal"
)

const (
	FlowContext    = "model_assignment"
	SubFlowContext = "allocate_mlnodes_for_poc"
)

func sortedKeys[K ~string, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// EpochMLNodeData stores ML node information indexed by [modelId][participantAddress]
type EpochMLNodeData struct {
	data map[string]map[string][]*types.MLNodeInfo
}

func NewEpochMLNodeData() *EpochMLNodeData {
	return &EpochMLNodeData{
		data: make(map[string]map[string][]*types.MLNodeInfo),
	}
}

func (e *EpochMLNodeData) Set(modelId, participantAddr string, nodes []*types.MLNodeInfo) {
	if e.data[modelId] == nil {
		e.data[modelId] = make(map[string][]*types.MLNodeInfo)
	}
	e.data[modelId][participantAddr] = nodes
}

func (e *EpochMLNodeData) GetForModel(modelId string) map[string][]*types.MLNodeInfo {
	return e.data[modelId]
}

func (e *EpochMLNodeData) GetForParticipant(modelId, participantAddr string) []*types.MLNodeInfo {
	if e.data[modelId] == nil {
		return nil
	}
	return e.data[modelId][participantAddr]
}

func (e *EpochMLNodeData) HasModel(modelId string) bool {
	return e.data[modelId] != nil
}

func (e *EpochMLNodeData) Models() []string {
	return sortedKeys(e.data)
}

func (e *EpochMLNodeData) GetAllParticipantsHash() string {
	uniqueParticipants := make(map[string]bool)
	for _, modelData := range e.data {
		for participantAddr := range modelData {
			uniqueParticipants[participantAddr] = true
		}
	}

	sortedParticipants := sortedKeys(uniqueParticipants)

	allParticipantsStr := fmt.Sprintf("%v", sortedParticipants)
	allParticipantsHash := sha256.Sum256([]byte(allParticipantsStr))
	return fmt.Sprintf("%x", allParticipantsHash[:8])
}

func (e *EpochMLNodeData) GetTotalWeightForModel(modelId string) int64 {
	var total int64
	participantNodes := e.GetForModel(modelId)
	for _, nodes := range participantNodes {
		for _, node := range nodes {
			total += node.PocWeight
		}
	}
	return total
}

type ModelAssigner struct {
	types.InferenceLogger
	keeper KeeperForModelAssigner
}

func NewModelAssigner(keeper KeeperForModelAssigner, logger types.InferenceLogger) *ModelAssigner {
	return &ModelAssigner{
		keeper:          keeper,
		InferenceLogger: logger,
	}
}

type KeeperForModelAssigner interface {
	GetGovernanceModelsSorted(ctx context.Context) ([]*types.Model, error)
	GetHardwareNodes(ctx context.Context, participantId string) (*types.HardwareNodes, bool)
	GetActiveParticipants(ctx context.Context, epochId uint64) (val types.ActiveParticipants, found bool)
	GetEpochGroupData(ctx context.Context, epochIndex uint64, modelId string) (val types.EpochGroupData, found bool)
	GetParams(ctx context.Context) types.Params
}

func (ma *ModelAssigner) setModelsForParticipants(ctx context.Context, participants []*types.ActiveParticipant, upcomingEpoch types.Epoch) {
	// TODO: We may need to populate throughput in MLNodeInfo using the model's ThroughputPerNonce
	// This would ensure consistent throughput calculations based on governance model parameters
	// rather than relying on hardware node declarations alone.
	ma.LogInfo("Starting model and slot assignment for participants", types.EpochGroup, "flow_context", FlowContext, "step", "start", "num_participants", len(participants), "epoch_index", upcomingEpoch.Index)

	// Get governance models to iterate through
	governanceModels, err := ma.keeper.GetGovernanceModelsSorted(ctx)
	if err != nil {
		ma.LogError("setModelsForParticipants: Unable to get governance models", types.EpochGroup, "error", err.Error(), "flow_context", FlowContext)
		return
	}
	ma.LogInfo("Retrieved governance models", types.EpochGroup, "flow_context", FlowContext, "step", "get_governance_models", "num_models", len(governanceModels))

	for _, p := range participants {
		ma.LogInfo("Processing participant", types.EpochGroup, "flow_context", FlowContext, "step", "participant_loop_start", "participant_index", p.Index)
		hardwareNodes, found := ma.keeper.GetHardwareNodes(ctx, p.Index)
		if !found {
			ma.LogInfo("No hardware nodes found for participant, skipping model assignment.", types.EpochGroup, "flow_context", FlowContext, "step", "no_hardware_nodes", "participant_index", p.Index)
			p.Models = make([]string, 0)
			p.MlNodes = make([]*types.ModelMLNodes, 0)
			continue
		}

		var originalMLNodes []*types.MLNodeInfo
		if len(p.MlNodes) > 0 && p.MlNodes[0] != nil {
			originalMLNodes = p.MlNodes[0].MlNodes
		}
		ma.LogInfo("Original MLNodes", types.EpochGroup, "flow_context", FlowContext, "step", "pre_legacy_distribution", "participant_index", p.Index, "ml_nodes", originalMLNodes)

		// Set PRE_POC_SLOT to true and POC_SLOT to false for all MLNodes (default to mining PoC)
		for _, mlNode := range originalMLNodes {
			// Initialize timeslot allocation vector: [PRE_POC_SLOT=true, POC_SLOT=false]
			mlNode.TimeslotAllocation = []bool{true, false} // index 0=PRE_POC_SLOT, index 1=POC_SLOT
		}
		ma.LogInfo("Initialized all ML nodes to PRE_POC_SLOT=true, POC_SLOT=false", types.EpochGroup, "flow_context", FlowContext, "step", "init_slots", "participant_index", p.Index)

		assignedMLNodes := make(map[string]bool)
		var supportedModels []string
		var newMLNodeArrays []*types.ModelMLNodes

		supportedModelsByNode := supportedModelsByNode(hardwareNodes, governanceModels)
		for nodeId, supportedModels := range supportedModelsByNode {
			ma.LogInfo("Supported models by node", types.EpochGroup, "flow_context", FlowContext, "step", "supported_models_by_node", "node_id", nodeId, "supported_models", supportedModels)
		}

		// For each governance model, pick the available MLNodes that have the model as first supported model
		for _, model := range governanceModels {
			ma.LogInfo("Attempting to assign ML node for model", types.EpochGroup, "flow_context", FlowContext, "step", "model_assignment_loop", "participant_index", p.Index, "model_id", model.Id)
			var modelMLNodes []*types.MLNodeInfo

			for _, mlNode := range originalMLNodes {
				if assignedMLNodes[mlNode.NodeId] {
					ma.LogInfo("Skipping already assigned ML node", types.EpochGroup, "flow_context", FlowContext, "step", "node_already_assigned", "participant_index", p.Index, "model_id", model.Id, "node_id", mlNode.NodeId)
					continue
				}

				// Check if this MLNode supports the current governance model
				if slices.Contains(supportedModelsByNode[mlNode.NodeId], model.Id) {
					ma.LogInfo("Found supporting and unassigned ML node for model", types.EpochGroup, "flow_context", FlowContext, "step", "assign_node_to_model", "participant_index", p.Index, "model_id", model.Id, "node_id", mlNode.NodeId)
					// Add this MLNode to the current model's array
					modelMLNodes = append(modelMLNodes, mlNode)
					assignedMLNodes[mlNode.NodeId] = true
				}
			}

			// Only add the model and MLNode array if we found supporting MLNodes
			if len(modelMLNodes) > 0 {
				supportedModels = append(supportedModels, model.Id)
				newMLNodeArrays = append(newMLNodeArrays, &types.ModelMLNodes{MlNodes: modelMLNodes})
				ma.LogInfo("Assigned ML nodes to model", types.EpochGroup, "flow_context", FlowContext, "step", "model_assignment_complete", "participant_index", p.Index, "model_id", model.Id, "assigned_nodes", modelMLNodes)
			} else {
				ma.LogInfo("No available ML nodes support this model", types.EpochGroup, "flow_context", FlowContext, "step", "no_supporting_nodes", "participant_index", p.Index, "model_id", model.Id)
			}
		}

		var unassignedMLNodes []*types.MLNodeInfo
		for _, mlNode := range originalMLNodes {
			if !assignedMLNodes[mlNode.NodeId] {
				unassignedMLNodes = append(unassignedMLNodes, mlNode)
			}
		}
		ma.LogInfo("Unassigned MLNodes", types.EpochGroup, "flow_context", FlowContext, "step", "unassigned_nodes", "participant_index", p.Index, "unassigned_nodes", unassignedMLNodes)

		p.MlNodes = newMLNodeArrays
		p.Models = supportedModels
		p.Weight = RecalculateWeight(p)
		ma.LogInfo("Participant models and ML nodes updated", types.EpochGroup, "flow_context", FlowContext, "step", "participant_updated", "participant_index", p.Index, "supported_models", p.Models, "ml_nodes", p.MlNodes)
	}
	ma.LogInfo("Finished model assignment for all participants", types.EpochGroup, "flow_context", FlowContext, "step", "model_assignment_complete")

	// Allocate ML nodes for PoC after all participants have their models assigned
	ma.allocateMLNodesForPoC(ctx, upcomingEpoch, participants)
	ma.LogInfo("Finished PoC allocation for all participants", types.EpochGroup, "flow_context", FlowContext, "step", "end")
}

func (ma *ModelAssigner) allocateMLNodesForPoC(ctx context.Context, upcomingEpoch types.Epoch, participants []*types.ActiveParticipant) {
	ma.LogInfo("Starting ML node allocation for PoC slots", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "start", "num_participants", len(participants))

	allocationPercentage := ma.keeper.GetParams(ctx).EpochParams.PocSlotAllocation
	if allocationPercentage == nil || allocationPercentage.ToDecimal().IsZero() {
		ma.LogInfo("PocSlotAllocation is nil or 0, using default 50%", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "default_allocation")
		defaultAllocation := types.DecimalFromFloat(50.0)
		allocationPercentage = defaultAllocation
	}

	// Phase 1: Build previous epoch data map
	previousEpochData := NewEpochMLNodeData()

	uniqueModels := make(map[string]bool)
	for _, participant := range participants {
		for _, modelId := range participant.Models {
			uniqueModels[modelId] = true
		}
	}
	ma.LogDebug("Collected unique models", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "collect_unique_models", "num_unique_models", len(uniqueModels))

	sortedModelIds := sortedKeys(uniqueModels)
	if upcomingEpoch.Index > 0 {
		for _, modelId := range sortedModelIds {
			previousEpochGroupData, found := ma.keeper.GetEpochGroupData(ctx, upcomingEpoch.Index-1, modelId)
			if found {
				for _, vw := range previousEpochGroupData.ValidationWeights {
					// Extract and store MlNodes directly
					previousEpochData.Set(modelId, vw.MemberAddress, vw.MlNodes)
				}
				ma.LogInfo("Loaded previous epoch data for model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "load_prev_epoch_data", "model_id", modelId, "num_validation_weights", len(previousEpochGroupData.ValidationWeights))
			}
		}
	}

	currentEpochData := NewEpochMLNodeData()
	for _, participant := range participants {
		for modelIdx, modelId := range participant.Models {
			if modelIdx >= len(participant.MlNodes) {
				ma.LogWarn("Model index out of bounds, skipping", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "model_index_oob", "participant_index", participant.Index, "model_id", modelId, "model_idx", modelIdx)
				continue
			}
			currentEpochData.Set(modelId, participant.Index, participant.MlNodes[modelIdx].MlNodes)
		}
	}
	ma.LogInfo("Built current epoch data map", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "build_current_epoch_data", "num_models", len(currentEpochData.Models()))

	// Phase 3: Filter eligible nodes for all models
	eligibleNodesData := ma.filterEligibleMLNodes(upcomingEpoch, previousEpochData, currentEpochData)
	ma.LogInfo("Filtered eligible nodes for all models", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "filter_all_eligible", "num_models", len(eligibleNodesData.Models()))

	// Phase 4: Allocate ML nodes for PoC for each model
	for _, modelId := range sortedModelIds {
		ma.LogInfo("Processing model for PoC allocation", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "model_loop_start", "model_id", modelId)
		ma.allocateMLNodePerPoCForModel(modelId, currentEpochData, eligibleNodesData, allocationPercentage)
	}
}

// filterEligibleMLNodes filters which nodes are eligible for allocation across all models
// First filters out nodes with weight > 90th percentile, then selects N/2 + 1 participants deterministically per model
func (ma *ModelAssigner) filterEligibleMLNodes(
	upcomingEpoch types.Epoch,
	previousEpochData *EpochMLNodeData,
	currentEpochData *EpochMLNodeData,
) *EpochMLNodeData {
	allParticipantsHashStr := currentEpochData.GetAllParticipantsHash()
	eligibleNodesData := NewEpochMLNodeData()

	// Step 1: Collect all ML node weights across all models (deterministically)
	var allWeights []int64
	for _, modelId := range currentEpochData.Models() {
		participantNodes := currentEpochData.GetForModel(modelId)
		// Sort participant addresses for deterministic iteration
		sortedParticipantAddrs := sortedKeys(participantNodes)
		for _, participantAddr := range sortedParticipantAddrs {
			nodes := participantNodes[participantAddr]
			for _, node := range nodes {
				allWeights = append(allWeights, node.PocWeight)
			}
		}
	}

	// Step 2: Calculate outlier threshold using IQR method
	weightThreshold := calculateWeightThreshold(allWeights)
	ma.LogInfo("Calculated weight threshold for eligibility using IQR", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "calculate_weight_threshold", "threshold", weightThreshold, "total_nodes", len(allWeights))

	for _, modelId := range currentEpochData.Models() {
		participantNodes := currentEpochData.GetForModel(modelId)
		sortedParticipantAddrs := sortedKeys(participantNodes)

		// Filter participants with previous epoch data
		var participantsWithHistory []string
		for _, participantAddr := range sortedParticipantAddrs {
			previousValidationWeight := previousEpochData.GetForParticipant(modelId, participantAddr)

			// Handle first epoch and missing previous epoch data
			if previousValidationWeight == nil {
				currentNodes := participantNodes[participantAddr]
				// Filter out nodes above weight threshold
				filteredNodes := filterNodesByWeight(currentNodes, weightThreshold)
				// First epoch: all nodes (that pass weight filter) are eligible
				if upcomingEpoch.Index == 0 {
					eligibleNodesData.Set(modelId, participantAddr, filteredNodes)
					ma.LogInfo("First epoch: nodes eligible after weight filter", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "first_epoch_all_eligible", "participant_index", participantAddr, "model_id", modelId, "original_nodes", len(currentNodes), "filtered_nodes", len(filteredNodes))
					continue
				}
				// Later epochs: require previous participation
				eligibleNodesData.Set(modelId, participantAddr, []*types.MLNodeInfo{})
				ma.LogInfo("Participant had no nodes in previous epoch, skipping eligibility", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "no_previous_epoch", "participant_index", participantAddr, "model_id", modelId)
				continue
			}

			participantsWithHistory = append(participantsWithHistory, participantAddr)
		}

		// Shuffle and select N/2 + 1 participants to be eligible
		if len(participantsWithHistory) > 0 && upcomingEpoch.Index > 0 {
			// Create deterministic seed for shuffling participants for this model
			seed := fmt.Sprintf("filter_%d_%s_%s", upcomingEpoch.Index, allParticipantsHashStr, modelId)
			hash := sha256.Sum256([]byte(seed))
			seedInt := int64(binary.BigEndian.Uint64(hash[:8]))
			rng := rand.New(rand.NewSource(seedInt))

			ma.LogInfo("Generated deterministic seed for participant selection", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "generate_filter_seed", "model_id", modelId, "seed_string", seed, "seed_int", seedInt)

			shuffledParticipants := make([]string, len(participantsWithHistory))
			copy(shuffledParticipants, participantsWithHistory)
			rng.Shuffle(len(shuffledParticipants), func(i, j int) {
				shuffledParticipants[i], shuffledParticipants[j] = shuffledParticipants[j], shuffledParticipants[i]
			})

			numEligible := min(len(sortedParticipantAddrs)/2+1, len(shuffledParticipants))
			eligibleParticipants := make(map[string]bool)
			for i := 0; i < numEligible && i < len(shuffledParticipants); i++ {
				eligibleParticipants[shuffledParticipants[i]] = true
			}

			ma.LogInfo("Selected eligible participants", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "select_eligible_participants", "model_id", modelId, "total_participants", len(participantsWithHistory), "eligible_participants", numEligible)

			for _, participantAddr := range participantsWithHistory {
				currentNodes := participantNodes[participantAddr]
				if eligibleParticipants[participantAddr] {
					// Filter out nodes above weight threshold
					filteredNodes := filterNodesByWeight(currentNodes, weightThreshold)
					eligibleNodesData.Set(modelId, participantAddr, filteredNodes)
					ma.LogInfo("Participant eligible: nodes included after weight filter", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "participant_eligible", "participant_index", participantAddr, "model_id", modelId, "original_nodes", len(currentNodes), "filtered_nodes", len(filteredNodes))
				} else {
					eligibleNodesData.Set(modelId, participantAddr, []*types.MLNodeInfo{})
					ma.LogInfo("Participant ineligible: no nodes included", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "participant_ineligible", "participant_index", participantAddr, "model_id", modelId)
				}
			}
		}
	}

	return eligibleNodesData
}

// allocateMLNodePerPoCForModel applies weight-based allocation logic across all participants for a specific model
func (ma *ModelAssigner) allocateMLNodePerPoCForModel(
	modelId string,
	currentEpochData *EpochMLNodeData,
	eligibleNodesData *EpochMLNodeData,
	percentage *types.Decimal,
) {
	ma.LogInfo("Starting allocation for model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "model_allocation_start", "model_id", modelId)

	totalWeight := currentEpochData.GetTotalWeightForModel(modelId)

	percentageDecimal := percentage.ToDecimal()
	targetPoCWeightDecimal := percentageDecimal.Mul(decimal.NewFromInt(totalWeight)).Div(decimal.NewFromInt(100))
	targetPoCWeight := targetPoCWeightDecimal.IntPart()

	ma.LogInfo("Calculated target weight for model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "calculate_target_weight", "model_id", modelId, "total_weight", totalWeight, "percentage", percentageDecimal.String(), "target_weight", targetPoCWeight)

	// Phase B: Get eligible participants (sorted alphabetically for determinism)
	eligibleModelNodes := eligibleNodesData.GetForModel(modelId)
	eligibleParticipantAddrs := sortedKeys(eligibleModelNodes)

	ma.LogInfo("Built participant list", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "build_participants", "model_id", modelId, "num_participants", len(eligibleParticipantAddrs))

	if len(eligibleParticipantAddrs) == 0 {
		ma.LogInfo("No participants with eligible nodes for this model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "no_participants", "model_id", modelId)
		return
	}

	// Phase C: Round-robin allocation
	var currentWeight int64
	currentParticipantIdx := 0
	allocatedInRound := false

	for currentWeight < targetPoCWeight {
		participantAddr := eligibleParticipantAddrs[currentParticipantIdx]
		nodes := eligibleNodesData.GetForParticipant(modelId, participantAddr)

		nextMLNode := getSmallestMLNodeWithPOCSLotFalse(nodes)

		if nextMLNode == nil {
			// No eligible nodes for this participant, try next
			currentParticipantIdx = (currentParticipantIdx + 1) % len(eligibleParticipantAddrs)

			// Check if we completed full round without allocation
			if currentParticipantIdx == 0 {
				if !allocatedInRound {
					ma.LogInfo("Completed full round without allocation, exiting", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "exit_no_nodes", "model_id", modelId, "current_weight", currentWeight, "target_weight", targetPoCWeight)
					break
				}
				allocatedInRound = false
			}
			continue
		}

		// Allocate this node
		nextMLNode.TimeslotAllocation[1] = true
		currentWeight += nextMLNode.PocWeight
		allocatedInRound = true

		ma.LogInfo("Allocated node to PoC slot", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "allocate_node", "model_id", modelId, "participant", participantAddr, "node_id", nextMLNode.NodeId, "node_weight", nextMLNode.PocWeight, "current_weight", currentWeight, "target_weight", targetPoCWeight)

		currentParticipantIdx = (currentParticipantIdx + 1) % len(eligibleParticipantAddrs)

		if currentParticipantIdx == 0 {
			allocatedInRound = false
		}
	}

	// Phase D: Log allocation summary per participant
	for _, participantAddr := range eligibleParticipantAddrs {
		nodes := eligibleNodesData.GetForParticipant(modelId, participantAddr)
		var allocatedCount int
		var allocatedWeight int64
		var allocatedNodeIds []string

		for _, node := range nodes {
			if len(node.TimeslotAllocation) > 1 && node.TimeslotAllocation[1] {
				allocatedCount++
				allocatedWeight += node.PocWeight
				allocatedNodeIds = append(allocatedNodeIds, node.NodeId)
			}
		}

		ma.LogInfo("Participant allocation summary", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "participant_summary", "model_id", modelId, "participant", participantAddr, "total_nodes", len(nodes), "allocated_nodes", allocatedCount, "allocated_weight", allocatedWeight, "allocated_node_ids", allocatedNodeIds)
	}

	ma.LogInfo("Finished allocation for model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "model_allocation_end", "model_id", modelId, "achieved_weight", currentWeight, "target_weight", targetPoCWeight, "total_weight", totalWeight)
}

// getSmallestMLNodeWithPOCSLotFalse returns the smallest node (by PocWeight) that has POC_SLOT=false
// Returns nil if no such node exists
func getSmallestMLNodeWithPOCSLotFalse(nodes []*types.MLNodeInfo) *types.MLNodeInfo {
	var smallest *types.MLNodeInfo
	for _, node := range nodes {
		if len(node.TimeslotAllocation) > 1 && !node.TimeslotAllocation[1] {
			if smallest == nil || node.PocWeight < smallest.PocWeight {
				smallest = node
			}
		}
	}
	return smallest
}

// calculateWeightThreshold calculates outlier threshold using IQR (Interquartile Range) method
// Returns Q3 + 1.5*IQR, filtering nodes with anomalously high weights
// Uses pure integer arithmetic for blockchain determinism
func calculateWeightThreshold(weights []int64) int64 {
	if len(weights) == 0 {
		return 0
	}

	if len(weights) == 1 {
		return weights[0]
	}

	// Sort weights for quartile calculation
	sortedWeights := make([]int64, len(weights))
	copy(sortedWeights, weights)
	slices.Sort(sortedWeights)

	n := len(sortedWeights)

	// Calculate Q1 (25th percentile) and Q3 (75th percentile)
	// Using integer division - deterministic across all nodes
	q1Index := n / 4
	q3Index := (n * 3) / 4

	// Ensure indices are within bounds
	if q3Index >= n {
		q3Index = n - 1
	}

	q1 := sortedWeights[q1Index]
	q3 := sortedWeights[q3Index]

	// Calculate IQR (Interquartile Range)
	iqr := q3 - q1

	// Threshold = Q3 + 1.5*IQR (standard outlier detection)
	// Using integer math: 1.5*IQR = IQR + IQR/2
	threshold := q3 + iqr + (iqr / 2)

	return threshold
}

// filterNodesByWeight filters out nodes with weight greater than threshold
func filterNodesByWeight(nodes []*types.MLNodeInfo, threshold int64) []*types.MLNodeInfo {
	filtered := make([]*types.MLNodeInfo, 0, len(nodes))
	for _, node := range nodes {
		if node.PocWeight <= threshold {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

// Helper function to create a map of modelId to supported models
func supportedModelsByNode(hardwareNodes *types.HardwareNodes, governanceModels []*types.Model) map[string][]string {
	governanceModelsMap := make(map[string]bool)
	for _, model := range governanceModels {
		governanceModelsMap[model.Id] = true
	}

	supportedModelsByNode := make(map[string][]string)
	for _, node := range hardwareNodes.HardwareNodes {
		// keep only the models that are in the governanceModelsMap
		supportedModels := make([]string, 0)
		for _, model := range node.Models {
			if governanceModelsMap[model] {
				supportedModels = append(supportedModels, model)
			}
		}
		supportedModelsByNode[node.LocalId] = supportedModels
	}

	return supportedModelsByNode
}
