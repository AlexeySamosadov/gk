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

// EpochMLNodeData stores ML node information indexed by [modelId][participantAddress]
// Used for tracking current, previous, and eligible ML nodes across epochs
type EpochMLNodeData struct {
	data map[string]map[string][]*types.MLNodeInfo
}

// NewEpochMLNodeData creates a new EpochMLNodeData instance
func NewEpochMLNodeData() *EpochMLNodeData {
	return &EpochMLNodeData{
		data: make(map[string]map[string][]*types.MLNodeInfo),
	}
}

// Set stores ML nodes for a specific model and participant
func (e *EpochMLNodeData) Set(modelId, participantAddr string, nodes []*types.MLNodeInfo) {
	if e.data[modelId] == nil {
		e.data[modelId] = make(map[string][]*types.MLNodeInfo)
	}
	e.data[modelId][participantAddr] = nodes
}

// GetForModel returns all participants' nodes for a given model
func (e *EpochMLNodeData) GetForModel(modelId string) map[string][]*types.MLNodeInfo {
	return e.data[modelId]
}

// GetForParticipant returns nodes for a specific participant and model
func (e *EpochMLNodeData) GetForParticipant(modelId, participantAddr string) []*types.MLNodeInfo {
	if e.data[modelId] == nil {
		return nil
	}
	return e.data[modelId][participantAddr]
}

// HasModel checks if the model exists in the data
func (e *EpochMLNodeData) HasModel(modelId string) bool {
	return e.data[modelId] != nil
}

// Models returns all model IDs
func (e *EpochMLNodeData) Models() []string {
	models := make([]string, 0, len(e.data))
	for modelId := range e.data {
		models = append(models, modelId)
	}
	return models
}

// PreviousEpochData stores ValidationWeight from previous epoch indexed by [modelId][participantAddress]
type PreviousEpochData struct {
	data map[string]map[string]*types.ValidationWeight
}

// NewPreviousEpochData creates a new PreviousEpochData instance
func NewPreviousEpochData() *PreviousEpochData {
	return &PreviousEpochData{
		data: make(map[string]map[string]*types.ValidationWeight),
	}
}

// Set stores ValidationWeight for a specific model and participant
func (p *PreviousEpochData) Set(modelId, participantAddr string, vw *types.ValidationWeight) {
	if p.data[modelId] == nil {
		p.data[modelId] = make(map[string]*types.ValidationWeight)
	}
	p.data[modelId][participantAddr] = vw
}

// GetForModel returns all participants' ValidationWeights for a given model
func (p *PreviousEpochData) GetForModel(modelId string) map[string]*types.ValidationWeight {
	return p.data[modelId]
}

// GetForParticipant returns ValidationWeight for a specific participant and model
func (p *PreviousEpochData) GetForParticipant(modelId, participantAddr string) *types.ValidationWeight {
	if p.data[modelId] == nil {
		return nil
	}
	return p.data[modelId][participantAddr]
}

// HasModel checks if the model exists in the data
func (p *PreviousEpochData) HasModel(modelId string) bool {
	return p.data[modelId] != nil
}

// Models returns all model IDs
func (p *PreviousEpochData) Models() []string {
	models := make([]string, 0, len(p.data))
	for modelId := range p.data {
		models = append(models, modelId)
	}
	return models
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
			// No hardware nodes - just set empty arrays
			ma.LogInfo("No hardware nodes found for participant, skipping model assignment.", types.EpochGroup, "flow_context", FlowContext, "step", "no_hardware_nodes", "participant_index", p.Index)
			p.Models = make([]string, 0)
			p.MlNodes = make([]*types.ModelMLNodes, 0)
			continue
		}

		// Get the original MLNodes from the first array (index 0) - populated by task 5.8
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

		// Track which MLNodes have been assigned
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
					continue // MLNode already assigned to another model
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

		// Add remaining unassigned MLNodes as overflow array (if any exist)
		var unassignedMLNodes []*types.MLNodeInfo
		for _, mlNode := range originalMLNodes {
			if !assignedMLNodes[mlNode.NodeId] {
				unassignedMLNodes = append(unassignedMLNodes, mlNode)
			}
		}
		ma.LogInfo("Unassigned MLNodes", types.EpochGroup, "flow_context", FlowContext, "step", "unassigned_nodes", "participant_index", p.Index, "unassigned_nodes", unassignedMLNodes)

		// Update participant with reorganized MLNode arrays and supported models
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

// allocateMLNodesForPoC orchestrates the allocation process for PoC slots across all participants
// It queries previous epoch data, filters eligible nodes, and applies allocation logic
func (ma *ModelAssigner) allocateMLNodesForPoC(ctx context.Context, upcomingEpoch types.Epoch, participants []*types.ActiveParticipant) {
	ma.LogInfo("Starting ML node allocation for PoC slots", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "start", "num_participants", len(participants))

	// Default allocation percentage: 50%
	allocationPercentage := types.DecimalFromFloat(50.0)

	// Phase 1: Build previous epoch data map
	previousEpochData := NewPreviousEpochData()

	// Collect unique model IDs
	uniqueModels := make(map[string]bool)
	for _, participant := range participants {
		for _, modelId := range participant.Models {
			uniqueModels[modelId] = true
		}
	}
	ma.LogInfo("Collected unique models", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "collect_unique_models", "num_unique_models", len(uniqueModels))

	// Sort model IDs for deterministic processing order
	var sortedModelIds []string
	for modelId := range uniqueModels {
		sortedModelIds = append(sortedModelIds, modelId)
	}
	slices.Sort(sortedModelIds)

	// Query previous epoch data for each model
	if upcomingEpoch.Index > 0 {
		for _, modelId := range sortedModelIds {
			previousEpochGroupData, found := ma.keeper.GetEpochGroupData(ctx, upcomingEpoch.Index-1, modelId)
			if found {
				for _, vw := range previousEpochGroupData.ValidationWeights {
					previousEpochData.Set(modelId, vw.MemberAddress, vw)
				}
				ma.LogInfo("Loaded previous epoch data for model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "load_prev_epoch_data", "model_id", modelId, "num_validation_weights", len(previousEpochGroupData.ValidationWeights))
			}
		}
	}

	// Phase 2: Build current epoch data map
	currentEpochData := NewEpochMLNodeData()
	for _, participant := range participants {
		for modelIdx, modelId := range participant.Models {
			if modelIdx >= len(participant.MlNodes) {
				ma.LogInfo("Model index out of bounds, skipping", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "model_index_oob", "participant_index", participant.Index, "model_id", modelId, "model_idx", modelIdx)
				continue
			}
			currentEpochData.Set(modelId, participant.Index, participant.MlNodes[modelIdx].MlNodes)
		}
	}
	ma.LogInfo("Built current epoch data map", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "build_current_epoch_data", "num_models", len(currentEpochData.Models()))

	// Phase 3: Filter eligible nodes
	eligibleNodesData := ma.filterEligibleMLNodes(upcomingEpoch, previousEpochData, currentEpochData)
	ma.LogInfo("Filtered eligible nodes for all models", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "filter_all_eligible", "num_models", len(eligibleNodesData.Models()))

	// Phase 4: Loop by model and allocate (using sorted model IDs for determinism)
	for _, modelId := range sortedModelIds {
		ma.LogInfo("Processing model for PoC allocation", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "model_loop_start", "model_id", modelId)
		ma.allocateMLNodePerPoCForModel(modelId, currentEpochData, eligibleNodesData, allocationPercentage)
	}

	ma.LogInfo("Finished ML node allocation for all participants", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "end")
}

// filterEligibleMLNodes filters which nodes are eligible for allocation across all models and participants
// Each node has 50% probability of being eligible (deterministic per epoch/participant/model)
func (ma *ModelAssigner) filterEligibleMLNodes(
	upcomingEpoch types.Epoch,
	previousEpochData *PreviousEpochData,
	currentEpochData *EpochMLNodeData,
) *EpochMLNodeData {
	ma.LogInfo("Starting eligible node filtering", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "filter_start")

	// Create filtered data
	eligibleNodesData := NewEpochMLNodeData()

	// Process each model
	for _, modelId := range currentEpochData.Models() {
		participantNodes := currentEpochData.GetForModel(modelId)

		// Get all participant addresses for this model and sort them for determinism
		var allParticipants []string
		for participantAddr := range participantNodes {
			allParticipants = append(allParticipants, participantAddr)
		}
		slices.Sort(allParticipants)

		// Create hash of all participants for reproducibility
		allParticipantsStr := fmt.Sprintf("%v", allParticipants)
		allParticipantsHash := sha256.Sum256([]byte(allParticipantsStr))
		allParticipantsHashStr := fmt.Sprintf("%x", allParticipantsHash[:8])

		ma.LogInfo("Generated participants hash for model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "participants_hash", "model_id", modelId, "num_participants", len(allParticipants), "participants_hash", allParticipantsHashStr)

		// Sort participant addresses for deterministic iteration
		var sortedParticipantAddrs []string
		for participantAddr := range participantNodes {
			sortedParticipantAddrs = append(sortedParticipantAddrs, participantAddr)
		}
		slices.Sort(sortedParticipantAddrs)

		// Process each participant for this model in sorted order
		for _, participantAddr := range sortedParticipantAddrs {
			currentNodes := participantNodes[participantAddr]
			// Get previous epoch ValidationWeight (if exists)
			previousValidationWeight := previousEpochData.GetForParticipant(modelId, participantAddr)

			// Handle first epoch and missing previous epoch data
			if previousValidationWeight == nil {
				// First epoch: all nodes are eligible (no history to filter on)
				if upcomingEpoch.Index == 0 {
					eligibleNodesData.Set(modelId, participantAddr, currentNodes)
					ma.LogInfo("First epoch: all nodes eligible", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "first_epoch_all_eligible", "participant_index", participantAddr, "model_id", modelId, "num_nodes", len(currentNodes))
					continue
				}
				// Later epochs: require previous participation
				eligibleNodesData.Set(modelId, participantAddr, []*types.MLNodeInfo{})
				ma.LogInfo("Participant had no nodes in previous epoch, skipping eligibility", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "no_previous_epoch", "participant_index", participantAddr, "model_id", modelId)
				continue
			}

			// Create deterministic random seed from epoch, all participants hash, specific participant, and model
			seed := fmt.Sprintf("filter_%d_%s_%s_%s", upcomingEpoch.Index, allParticipantsHashStr, participantAddr, modelId)
			hash := sha256.Sum256([]byte(seed))
			seedInt := int64(binary.BigEndian.Uint64(hash[:8]))
			rng := rand.New(rand.NewSource(seedInt))

			ma.LogInfo("Generated deterministic seed for eligibility filtering", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "generate_filter_seed", "participant_index", participantAddr, "model_id", modelId, "seed_string", seed, "seed_int", seedInt)

			// Filter nodes: each node has 50% probability of being eligible
			var eligibleNodes []*types.MLNodeInfo
			for _, node := range currentNodes {
				// Deterministic 50% selection using RNG
				if rng.Intn(2) == 0 {
					eligibleNodes = append(eligibleNodes, node)
					ma.LogInfo("Node marked as eligible", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "node_eligible", "participant_index", participantAddr, "model_id", modelId, "node_id", node.NodeId)
				} else {
					ma.LogInfo("Node marked as ineligible", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "node_ineligible", "participant_index", participantAddr, "model_id", modelId, "node_id", node.NodeId)
				}
			}

			eligibleNodesData.Set(modelId, participantAddr, eligibleNodes)
			ma.LogInfo("Filtered eligible nodes for participant-model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "participant_filter_complete", "participant_index", participantAddr, "model_id", modelId, "total_nodes", len(currentNodes), "eligible_nodes", len(eligibleNodes))
		}
	}

	ma.LogInfo("Finished eligible node filtering", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "filter_end", "num_models", len(eligibleNodesData.Models()))
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

	// Phase A: Calculate total weight from current epoch data
	var totalWeight int64
	currentModelNodes := currentEpochData.GetForModel(modelId)

	// Sort participant addresses for deterministic iteration
	var participantAddrs []string
	for participantAddr := range currentModelNodes {
		participantAddrs = append(participantAddrs, participantAddr)
	}
	slices.Sort(participantAddrs)

	for _, participantAddr := range participantAddrs {
		nodes := currentModelNodes[participantAddr]
		for _, node := range nodes {
			totalWeight += node.PocWeight
		}
	}

	percentageDecimal := percentage.ToDecimal()
	targetPoCWeightDecimal := percentageDecimal.Mul(decimal.NewFromInt(totalWeight)).Div(decimal.NewFromInt(100))
	targetPoCWeight := targetPoCWeightDecimal.IntPart()

	ma.LogInfo("Calculated target weight for model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "calculate_target_weight", "model_id", modelId, "total_weight", totalWeight, "percentage", percentageDecimal.String(), "target_weight", targetPoCWeight)

	// Phase B: Build sorted participant list
	type participantWeight struct {
		address     string
		totalWeight int64
	}

	var participantWeights []participantWeight
	eligibleModelNodes := eligibleNodesData.GetForModel(modelId)

	// Collect and sort participant addresses for determinism
	var eligibleParticipantAddrs []string
	for participantAddr := range eligibleModelNodes {
		eligibleParticipantAddrs = append(eligibleParticipantAddrs, participantAddr)
	}
	slices.Sort(eligibleParticipantAddrs)

	// Iterate in sorted order
	for _, participantAddr := range eligibleParticipantAddrs {
		nodes := eligibleModelNodes[participantAddr]
		var weight int64
		for _, node := range nodes {
			weight += node.PocWeight
		}
		participantWeights = append(participantWeights, participantWeight{
			address:     participantAddr,
			totalWeight: weight,
		})
	}

	// Sort by total weight ascending, then by participant address for determinism
	slices.SortFunc(participantWeights, func(a, b participantWeight) int {
		if a.totalWeight != b.totalWeight {
			if a.totalWeight < b.totalWeight {
				return -1
			}
			return 1
		}
		if a.address < b.address {
			return -1
		}
		if a.address > b.address {
			return 1
		}
		return 0
	})

	ma.LogInfo("Built sorted participant list", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "build_sorted_participants", "model_id", modelId, "num_participants", len(participantWeights))

	if len(participantWeights) == 0 {
		ma.LogInfo("No participants with eligible nodes for this model", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "no_participants", "model_id", modelId)
		return
	}

	// Phase C: Round-robin allocation
	var currentWeight int64
	currentParticipantIdx := 0
	allocatedInRound := false

	for currentWeight < targetPoCWeight {
		participant := participantWeights[currentParticipantIdx]
		nodes := eligibleNodesData.GetForParticipant(modelId, participant.address)

		nextMLNode := getSmallestMLNodeWithPOCSLotFalse(nodes)

		if nextMLNode == nil {
			// No eligible nodes for this participant, try next
			currentParticipantIdx = (currentParticipantIdx + 1) % len(participantWeights)

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

		ma.LogInfo("Allocated node to PoC slot", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "allocate_node", "model_id", modelId, "participant", participant.address, "node_id", nextMLNode.NodeId, "node_weight", nextMLNode.PocWeight, "current_weight", currentWeight, "target_weight", targetPoCWeight)

		currentParticipantIdx = (currentParticipantIdx + 1) % len(participantWeights)

		if currentParticipantIdx == 0 {
			allocatedInRound = false
		}
	}

	// Phase D: Log allocation summary per participant
	for _, pw := range participantWeights {
		nodes := eligibleNodesData.GetForParticipant(modelId, pw.address)
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

		ma.LogInfo("Participant allocation summary", types.EpochGroup, "flow_context", FlowContext, "sub_flow_context", SubFlowContext, "step", "participant_summary", "model_id", modelId, "participant", pw.address, "total_nodes", len(nodes), "allocated_nodes", allocatedCount, "allocated_weight", allocatedWeight, "allocated_node_ids", allocatedNodeIds)
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
