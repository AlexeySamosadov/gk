package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/productscience/inference/x/inference/types"
)

// MigrateConfirmationWeights initializes ConfirmationWeight for existing EpochGroupData.
// This migration is needed for the v0.2.5 upgrade because ConfirmationWeight is a new field.
func (k Keeper) MigrateConfirmationWeights(ctx sdk.Context) error {
	k.Logger().Info("migration: initializing confirmation weights for existing epochs")

	allEpochGroupData := k.GetAllEpochGroupData(ctx)
	updatedCount := 0

	for _, epochGroupData := range allEpochGroupData {
		updated := false

		for i, vw := range epochGroupData.ValidationWeights {
			// If ConfirmationWeight is 0, initialize it from inference-serving nodes (POC_SLOT=false)
			if vw.ConfirmationWeight == 0 {
				confirmationWeight := calculateInferenceServingWeight(vw.MlNodes)
				epochGroupData.ValidationWeights[i].ConfirmationWeight = confirmationWeight
				updated = true

				k.Logger().Info("migration: initialized confirmation weight",
					"epoch", epochGroupData.EpochIndex,
					"model", epochGroupData.ModelId,
					"participant", vw.MemberAddress,
					"confirmationWeight", confirmationWeight)
			}
		}

		if updated {
			k.SetEpochGroupData(ctx, epochGroupData)
			updatedCount++
		}
	}

	k.Logger().Info("migration: finished initializing confirmation weights",
		"totalEpochGroupData", len(allEpochGroupData),
		"updated", updatedCount)

	return nil
}

// calculateInferenceServingWeight calculates the total weight of nodes serving inference (POC_SLOT=false).
// This matches the logic in epochgroup.calculateInferenceServingWeight.
func calculateInferenceServingWeight(mlNodes []*types.MLNodeInfo) int64 {
	totalWeight := int64(0)

	for _, node := range mlNodes {
		if node == nil {
			continue
		}

		// POC_SLOT is at index 1 (second timeslot)
		// false = serves inference, true = preserved for confirmation PoC
		if len(node.TimeslotAllocation) > 1 && !node.TimeslotAllocation[1] {
			totalWeight += node.PocWeight
		}
	}

	return totalWeight
}
