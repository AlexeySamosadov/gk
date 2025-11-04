package epochgroup

import (
	"testing"

	"github.com/productscience/inference/x/inference/types"
	"github.com/stretchr/testify/require"
)

func TestCalculateInferenceServingWeight_POCSlotTrue(t *testing.T) {
	// Nodes with POC_SLOT=true (index 1 = true) should be EXCLUDED
	mlNodes := []*types.ModelMLNodes{
		{
			MlNodes: []*types.MLNodeInfo{
				{
					NodeId:             "node1",
					PocWeight:          100,
					TimeslotAllocation: []bool{true, true}, // POC_SLOT=true (continues inference)
				},
				{
					NodeId:             "node2",
					PocWeight:          200,
					TimeslotAllocation: []bool{true, true}, // POC_SLOT=true
				},
			},
		},
	}

	weight := calculateInferenceServingWeight(mlNodes)
	
	// Should be 0 since all nodes have POC_SLOT=true
	require.Equal(t, int64(0), weight)
}

func TestCalculateInferenceServingWeight_POCSlotFalse(t *testing.T) {
	// Nodes with POC_SLOT=false (index 1 = false) should be INCLUDED
	mlNodes := []*types.ModelMLNodes{
		{
			MlNodes: []*types.MLNodeInfo{
				{
					NodeId:             "node1",
					PocWeight:          100,
					TimeslotAllocation: []bool{true, false}, // POC_SLOT=false (serves inference)
				},
				{
					NodeId:             "node2",
					PocWeight:          200,
					TimeslotAllocation: []bool{false, false}, // POC_SLOT=false
				},
			},
		},
	}

	weight := calculateInferenceServingWeight(mlNodes)
	
	// Should be sum of all weights since all have POC_SLOT=false
	require.Equal(t, int64(300), weight)
}

func TestCalculateInferenceServingWeight_Mixed(t *testing.T) {
	// Mixed nodes - some with POC_SLOT=true, some with POC_SLOT=false
	mlNodes := []*types.ModelMLNodes{
		{
			MlNodes: []*types.MLNodeInfo{
				{
					NodeId:             "node1",
					PocWeight:          100,
					TimeslotAllocation: []bool{true, false}, // POC_SLOT=false - INCLUDE
				},
				{
					NodeId:             "node2",
					PocWeight:          200,
					TimeslotAllocation: []bool{true, true}, // POC_SLOT=true - EXCLUDE
				},
				{
					NodeId:             "node3",
					PocWeight:          300,
					TimeslotAllocation: []bool{false, false}, // POC_SLOT=false - INCLUDE
				},
				{
					NodeId:             "node4",
					PocWeight:          400,
					TimeslotAllocation: []bool{false, true}, // POC_SLOT=true - EXCLUDE
				},
			},
		},
	}

	weight := calculateInferenceServingWeight(mlNodes)
	
	// Should be 100 + 300 = 400 (only POC_SLOT=false nodes)
	require.Equal(t, int64(400), weight)
}

func TestCalculateInferenceServingWeight_EmptySlots(t *testing.T) {
	// Nodes with empty or short TimeslotAllocation arrays
	mlNodes := []*types.ModelMLNodes{
		{
			MlNodes: []*types.MLNodeInfo{
				{
					NodeId:             "node1",
					PocWeight:          100,
					TimeslotAllocation: []bool{}, // Empty - should be excluded
				},
				{
					NodeId:             "node2",
					PocWeight:          200,
					TimeslotAllocation: []bool{true}, // Only 1 slot - should be excluded
				},
				{
					NodeId:             "node3",
					PocWeight:          300,
					TimeslotAllocation: []bool{true, false}, // Has index 1 = false - INCLUDE
				},
			},
		},
	}

	weight := calculateInferenceServingWeight(mlNodes)
	
	// Should be 300 (only node3 has valid POC_SLOT at index 1)
	require.Equal(t, int64(300), weight)
}

func TestCalculateInferenceServingWeight_NilNodes(t *testing.T) {
	// Test handling of nil nodes
	mlNodes := []*types.ModelMLNodes{
		nil, // Nil model nodes
		{
			MlNodes: []*types.MLNodeInfo{
				nil, // Nil node
				{
					NodeId:             "node1",
					PocWeight:          100,
					TimeslotAllocation: []bool{true, false},
				},
			},
		},
	}

	weight := calculateInferenceServingWeight(mlNodes)
	
	// Should handle nils gracefully and count only valid node
	require.Equal(t, int64(100), weight)
}

func TestCalculateInferenceServingWeight_MultipleModelArrays(t *testing.T) {
	// Multiple model arrays (though typically there's only one)
	mlNodes := []*types.ModelMLNodes{
		{
			MlNodes: []*types.MLNodeInfo{
				{
					NodeId:             "node1",
					PocWeight:          100,
					TimeslotAllocation: []bool{true, false},
				},
			},
		},
		{
			MlNodes: []*types.MLNodeInfo{
				{
					NodeId:             "node2",
					PocWeight:          200,
					TimeslotAllocation: []bool{false, false},
				},
			},
		},
	}

	weight := calculateInferenceServingWeight(mlNodes)
	
	// Should sum across all model arrays
	require.Equal(t, int64(300), weight)
}
