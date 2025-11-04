package inference

import (
	"context"

	"github.com/productscience/inference/x/inference/types"
)

// calculateConfirmationWeights calculates confirmation weights for all confirmation PoC events in an epoch
// Returns a map of participant_address -> minimum confirmation weight across all events
func (am AppModule) calculateConfirmationWeights(
	ctx context.Context,
	epochIndex uint64,
	currentValidatorWeights map[string]int64,
	participants map[string]types.Participant,
	seeds map[string]types.RandomSeed,
) map[string]int64 {
	// Get all confirmation events for this epoch
	events, err := am.keeper.GetAllConfirmationPoCEventsForEpoch(ctx, epochIndex)
	if err != nil {
		am.LogError("calculateConfirmationWeights: Error getting confirmation events", types.PoC,
			"epochIndex", epochIndex,
			"error", err)
		return nil
	}

	if len(events) == 0 {
		// No confirmation events for this epoch
		return nil
	}

	am.LogInfo("calculateConfirmationWeights: Processing confirmation events", types.PoC,
		"epochIndex", epochIndex,
		"numEvents", len(events))

	// Track minimum weight for each participant across all events
	minWeights := make(map[string]int64)

	// Process each confirmation event
	for _, event := range events {
		if event.Phase != types.ConfirmationPoCPhase_CONFIRMATION_POC_COMPLETED {
			// Skip events that aren't completed yet
			am.LogWarn("calculateConfirmationWeights: Skipping incomplete event", types.PoC,
				"epochIndex", event.EpochIndex,
				"eventSequence", event.EventSequence,
				"phase", event.Phase.String())
			continue
		}

		// Get batches for this confirmation event (using trigger_height as key)
		batches, err := am.keeper.GetPoCBatchesByStage(ctx, event.TriggerHeight)
		if err != nil {
			am.LogError("calculateConfirmationWeights: Error getting batches for event", types.PoC,
				"epochIndex", event.EpochIndex,
				"eventSequence", event.EventSequence,
				"triggerHeight", event.TriggerHeight,
				"error", err)
			continue
		}

		// Get validations for this confirmation event
		validations, err := am.keeper.GetPoCValidationByStage(ctx, event.TriggerHeight)
		if err != nil {
			am.LogError("calculateConfirmationWeights: Error getting validations for event", types.PoC,
				"epochIndex", event.EpochIndex,
				"eventSequence", event.EventSequence,
				"triggerHeight", event.TriggerHeight,
				"error", err)
			continue
		}

		// Calculate weights using existing WeightCalculator
		calculator := NewWeightCalculator(
			currentValidatorWeights,
			batches,
			validations,
			participants,
			seeds,
			event.TriggerHeight, // Use trigger_height for logging
			am,
		)

		eventParticipants := calculator.Calculate()

		am.LogInfo("calculateConfirmationWeights: Calculated weights for event", types.PoC,
			"epochIndex", event.EpochIndex,
			"eventSequence", event.EventSequence,
			"triggerHeight", event.TriggerHeight,
			"numParticipants", len(eventParticipants))

		// Update minimum weights for each participant
		for _, ap := range eventParticipants {
			participantWeight := ap.Weight

			// Take minimum across all events
			if existingMin, found := minWeights[ap.Index]; found {
				if participantWeight < existingMin {
					minWeights[ap.Index] = participantWeight
				}
			} else {
				minWeights[ap.Index] = participantWeight
			}
		}
	}

	am.LogInfo("calculateConfirmationWeights: Completed", types.PoC,
		"epochIndex", epochIndex,
		"numEvents", len(events),
		"numParticipants", len(minWeights))

	return minWeights
}
