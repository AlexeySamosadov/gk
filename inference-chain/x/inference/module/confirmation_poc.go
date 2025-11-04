package inference

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/productscience/inference/x/inference/calculations"
	"github.com/productscience/inference/x/inference/types"
	"github.com/shopspring/decimal"
)

// handleConfirmationPoC manages confirmation PoC trigger decisions and phase transitions
func (am AppModule) handleConfirmationPoC(ctx context.Context, blockHeight int64) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// Get current parameters
	params, err := am.keeper.GetParamsSafe(ctx)
	if err != nil {
		return fmt.Errorf("failed to get params: %w", err)
	}

	confirmationParams := params.ConfirmationPocParams
	if confirmationParams == nil {
		// Confirmation PoC not configured, skip
		return nil
	}

	// Check if expected confirmations is 0 (feature disabled)
	if confirmationParams.ExpectedConfirmationsPerEpoch == 0 {
		return nil
	}

	epochParams := params.EpochParams
	if epochParams == nil {
		return fmt.Errorf("epoch params not found")
	}

	// Get current epoch context
	currentEpoch, found := am.keeper.GetEffectiveEpoch(ctx)
	if !found || currentEpoch == nil {
		// No epoch yet, skip
		return nil
	}

	epochContext, err := types.NewEpochContextFromEffectiveEpoch(*currentEpoch, *epochParams, blockHeight)
	if err != nil {
		return fmt.Errorf("failed to create epoch context: %w", err)
	}

	// Handle phase transitions for active event
	err = am.handleConfirmationPoCPhaseTransitions(ctx, blockHeight, epochContext, epochParams)
	if err != nil {
		am.LogError("Error handling confirmation PoC phase transitions", types.PoC, "error", err)
		// Continue to check for new triggers
	}

	// Check if we should trigger a new confirmation PoC event
	err = am.checkConfirmationPoCTrigger(ctx, blockHeight, epochContext, epochParams, confirmationParams, sdkCtx)
	if err != nil {
		return fmt.Errorf("failed to check confirmation PoC trigger: %w", err)
	}

	return nil
}

// checkConfirmationPoCTrigger checks if a confirmation PoC event should be triggered
func (am AppModule) checkConfirmationPoCTrigger(
	ctx context.Context,
	blockHeight int64,
	epochContext *types.EpochContext,
	epochParams *types.EpochParams,
	confirmationParams *types.ConfirmationPoCParams,
	sdkCtx sdk.Context,
) error {
	// Only trigger during inference phase
	currentPhase := epochContext.GetCurrentPhase(blockHeight)
	if currentPhase != types.InferencePhase {
		return nil
	}

	// Check if there's already an active event
	_, isActive, err := am.keeper.GetActiveConfirmationPoCEvent(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active confirmation PoC event: %w", err)
	}
	if isActive {
		// Already have an active event, don't trigger another
		return nil
	}

	// Calculate valid trigger window
	// [SetNewValidators(), NextPoCStart - InferenceValidationCutoff - ConfirmationWindowDuration]
	setNewValidatorsHeight := epochContext.SetNewValidators()
	nextEpochContext := epochContext.NextEpochContext()
	nextPoCStart := nextEpochContext.PocStartBlockHeight

	confirmationWindowDuration := epochParams.PocStageDuration + epochParams.PocValidationDuration
	triggerWindowEnd := nextPoCStart - epochParams.InferenceValidationCutoff - confirmationWindowDuration

	if blockHeight < setNewValidatorsHeight || blockHeight > triggerWindowEnd {
		// Outside valid trigger window
		return nil
	}

	triggerWindowLength := triggerWindowEnd - setNewValidatorsHeight + 1
	if triggerWindowLength <= 0 {
		// Invalid window
		return nil
	}

	// Calculate trigger probability using deterministicFloat pattern
	expectedConfirmations := decimal.NewFromInt(int64(confirmationParams.ExpectedConfirmationsPerEpoch))
	windowBlocks := decimal.NewFromInt(triggerWindowLength)
	triggerProbability := expectedConfirmations.Div(windowBlocks)

	// Use block hash at H-1 as randomness source
	prevBlockHash := sdkCtx.HeaderInfo().Hash
	if len(prevBlockHash) < 8 {
		return fmt.Errorf("block hash too short: %d bytes", len(prevBlockHash))
	}

	blockHashSeed := int64(binary.BigEndian.Uint64(prevBlockHash[:8]))
	randFloat := calculations.DeterministicFloat(blockHashSeed, fmt.Sprintf("confirmation_poc_trigger_%d", blockHeight))

	shouldTrigger := randFloat.LessThan(triggerProbability)

	if !shouldTrigger {
		return nil
	}

	// Trigger a new confirmation PoC event
	am.LogInfo("Triggering confirmation PoC event", types.PoC,
		"blockHeight", blockHeight,
		"epochIndex", epochContext.EpochIndex,
		"triggerProbability", triggerProbability.String(),
		"randomValue", randFloat.String())

	// Get next event sequence number for this epoch
	existingEvents, err := am.keeper.GetAllConfirmationPoCEventsForEpoch(ctx, epochContext.EpochIndex)
	if err != nil {
		return fmt.Errorf("failed to get existing events: %w", err)
	}
	eventSequence := uint64(len(existingEvents))

	// Calculate event heights
	generationStartHeight := blockHeight + epochParams.InferenceValidationCutoff
	generationEndHeight := generationStartHeight + epochParams.PocStageDuration - 1
	validationStartHeight := generationEndHeight + 1
	validationEndHeight := validationStartHeight + epochParams.PocValidationDuration - 1

	// Create new event in GRACE_PERIOD phase (poc_seed_block_hash will be set later)
	event := types.ConfirmationPoCEvent{
		EpochIndex:            epochContext.EpochIndex,
		EventSequence:         eventSequence,
		TriggerHeight:         blockHeight,
		GenerationStartHeight: generationStartHeight,
		GenerationEndHeight:   generationEndHeight,
		ValidationStartHeight: validationStartHeight,
		ValidationEndHeight:   validationEndHeight,
		Phase:                 types.ConfirmationPoCPhase_CONFIRMATION_POC_GRACE_PERIOD,
		PocSeedBlockHash:      "", // Will be set when transitioning to GENERATION phase
	}

	// Store the event
	err = am.keeper.SetConfirmationPoCEvent(ctx, event)
	if err != nil {
		return fmt.Errorf("failed to store confirmation PoC event: %w", err)
	}

	// Set as active event
	err = am.keeper.SetActiveConfirmationPoCEvent(ctx, event)
	if err != nil {
		return fmt.Errorf("failed to set active confirmation PoC event: %w", err)
	}

	am.LogInfo("Created confirmation PoC event", types.PoC,
		"epochIndex", event.EpochIndex,
		"eventSequence", event.EventSequence,
		"triggerHeight", event.TriggerHeight,
		"generationStartHeight", event.GenerationStartHeight,
		"validationEndHeight", event.ValidationEndHeight)

	return nil
}

// handleConfirmationPoCPhaseTransitions manages phase transitions for active confirmation PoC events
func (am AppModule) handleConfirmationPoCPhaseTransitions(
	ctx context.Context,
	blockHeight int64,
	epochContext *types.EpochContext,
	epochParams *types.EpochParams,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	activeEvent, isActive, err := am.keeper.GetActiveConfirmationPoCEvent(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active confirmation PoC event: %w", err)
	}
	if !isActive || activeEvent == nil {
		// No active event
		return nil
	}

	event := *activeEvent
	updated := false

	// GRACE_PERIOD -> GENERATION transition
	if event.Phase == types.ConfirmationPoCPhase_CONFIRMATION_POC_GRACE_PERIOD && blockHeight == event.GenerationStartHeight {
		// Capture block hash from (generation_start_height - 1)
		// At generation_start_height, HeaderInfo().Hash gives us the hash of the previous block
		prevBlockHash := sdkCtx.HeaderInfo().Hash
		event.PocSeedBlockHash = hex.EncodeToString(prevBlockHash)
		event.Phase = types.ConfirmationPoCPhase_CONFIRMATION_POC_GENERATION
		updated = true

		am.LogInfo("Confirmation PoC: GRACE_PERIOD -> GENERATION", types.PoC,
			"epochIndex", event.EpochIndex,
			"eventSequence", event.EventSequence,
			"generationStartHeight", event.GenerationStartHeight,
			"pocSeedBlockHash", event.PocSeedBlockHash[:16]+"...")
	}

	// GENERATION -> VALIDATION transition
	if event.Phase == types.ConfirmationPoCPhase_CONFIRMATION_POC_GENERATION && blockHeight == event.ValidationStartHeight {
		event.Phase = types.ConfirmationPoCPhase_CONFIRMATION_POC_VALIDATION
		updated = true

		am.LogInfo("Confirmation PoC: GENERATION -> VALIDATION", types.PoC,
			"epochIndex", event.EpochIndex,
			"eventSequence", event.EventSequence,
			"validationStartHeight", event.ValidationStartHeight)
	}

	// VALIDATION -> COMPLETED transition
	if event.Phase == types.ConfirmationPoCPhase_CONFIRMATION_POC_VALIDATION && blockHeight == event.ValidationEndHeight+1 {
		event.Phase = types.ConfirmationPoCPhase_CONFIRMATION_POC_COMPLETED
		updated = true

		// Clear active event
		err = am.keeper.ClearActiveConfirmationPoCEvent(ctx)
		if err != nil {
			return fmt.Errorf("failed to clear active confirmation PoC event: %w", err)
		}

		am.LogInfo("Confirmation PoC: VALIDATION -> COMPLETED", types.PoC,
			"epochIndex", event.EpochIndex,
			"eventSequence", event.EventSequence,
			"validationEndHeight", event.ValidationEndHeight)
	}

	// Update the event if phase changed
	if updated {
		// Update stored event
		err = am.keeper.SetConfirmationPoCEvent(ctx, event)
		if err != nil {
			return fmt.Errorf("failed to update confirmation PoC event: %w", err)
		}

		// Update active event if not completed
		if event.Phase != types.ConfirmationPoCPhase_CONFIRMATION_POC_COMPLETED {
			err = am.keeper.SetActiveConfirmationPoCEvent(ctx, event)
			if err != nil {
				return fmt.Errorf("failed to update active confirmation PoC event: %w", err)
			}
		}
	}

	return nil
}
