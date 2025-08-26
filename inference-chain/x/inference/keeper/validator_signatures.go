package keeper

import (
	"context"
	"github.com/productscience/inference/x/inference/types"
)

func (k Keeper) SetValidatorsSignatures(ctx context.Context, signatures types.ValidatorsProof) error {
	h := uint64(signatures.BlockHeight)

	exists, err := k.ValidatorsProofs.Has(ctx, h)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return k.ValidatorsProofs.Set(ctx, h, signatures)
}

func (k Keeper) GetValidatorsSignatures(ctx context.Context, height int64) (types.ValidatorsProof, bool) {
	v, err := k.ValidatorsProofs.Get(ctx, uint64(height))
	if err != nil {
		return types.ValidatorsProof{}, false
	}
	return v, true
}
