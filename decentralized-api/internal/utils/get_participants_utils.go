package utils

import (
	"context"
	cosmos_client "decentralized-api/cosmosclient"
	"decentralized-api/logging"
	"decentralized-api/merkleproof"
	"encoding/hex"
	"errors"
	rpcclient "github.com/cometbft/cometbft/rpc/client/http"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/gonka-ai/gonka-utils/go/contracts"
	externalutils "github.com/gonka-ai/gonka-utils/go/utils"
	"github.com/productscience/common"
	"github.com/productscience/inference/x/inference/types"
	"net/url"
	"strconv"
)

func QueryActiveParticipants(rpcClient *rpcclient.HTTP, queryClient types.QueryClient) externalutils.GetParticipantsFn {
	return func(ctx context.Context, epochParam string) (*contracts.ActiveParticipantWithProof, error) {
		if epochParam == "" {
			return nil, errors.New("invalid epoch id")
		}

		var epochId uint64
		if epochParam == "current" {
			currEpoch, err := queryClient.GetCurrentEpoch(ctx, &types.QueryGetCurrentEpochRequest{})
			if err != nil {
				logging.Error("Failed to get current epoch", types.Participants, "error", err)
				return nil, err
			}
			logging.Info("Current epoch resolved.", types.Participants, "epoch", currEpoch.Epoch)
			epochId = currEpoch.Epoch
		} else {
			epoch, err := strconv.ParseUint(epochParam, 10, 64)
			if err != nil {
				return nil, errors.New("invalid epoch id")
			}
			epochId = epoch
		}

		dataKey := types.ActiveParticipantsFullKey(epochId)
		result, err := cosmos_client.QueryByKey(rpcClient, "inference", dataKey)
		if err != nil {
			logging.Error("Failed to query active participants. Req 1", types.Participants, "error", err)
			return nil, err
		}

		logging.Info("[PARTICIPANTS-DEBUG] Raw active participants query result", types.Participants,
			"epoch", epochId,
			"value_bytes", len(result.Response.Value))

		interfaceRegistry := codectypes.NewInterfaceRegistry()
		types.RegisterInterfaces(interfaceRegistry)
		cdc := codec.NewProtoCodec(interfaceRegistry)

		var activeParticipants types.ActiveParticipants
		if err := cdc.Unmarshal(result.Response.Value, &activeParticipants); err != nil {
			logging.Error("Failed to unmarshal active participants. Req 1", types.Participants, "error", err)
			return nil, err
		}

		logging.Info("[PARTICIPANTS-DEBUG] Unmarshalled ActiveParticipants", types.Participants,
			"epoch", epochId,
			"created_at_block_height", activeParticipants.CreatedAtBlockHeight,
			"effective_block_height", activeParticipants.EffectiveBlockHeight)

		blockProof, err := queryClient.GetBlockProofByHeight(ctx, &types.QueryBlockProofRequest{ProofHeight: activeParticipants.CreatedAtBlockHeight})
		if err != nil {
			logging.Error("Failed to get block proof by height", types.Participants, "error", err)
			return nil, err
		}

		if result.Response.ProofOps != nil {
			hash, _ := hex.DecodeString(blockProof.Proof.AppHashHex)
			verifyProof(epochId, result, hash)
		}

		proofResp, err := queryClient.GetParticipantsProofByHeight(ctx, &types.QueryGetParticipantsProofRequest{ProofHeight: activeParticipants.CreatedAtBlockHeight})
		if err != nil {
			return nil, err
		}

		activeParticipantsBytes := hex.EncodeToString(result.Response.Value)
		addresses := make([]string, len(activeParticipants.Participants))
		for i, participant := range activeParticipants.Participants {
			addresses[i], err = common.ConsensusKeyToConsensusAddress(participant.ValidatorKey)
			if err != nil {
				logging.Error("Failed to convert public key to address", types.Participants, "error", err)
			}
		}

		finalParticipants := contracts.ActiveParticipants{
			Participants:         make([]*contracts.ActiveParticipant, len(activeParticipants.Participants)),
			EpochGroupId:         activeParticipants.EpochGroupId,
			PocStartBlockHeight:  activeParticipants.PocStartBlockHeight,
			EffectiveBlockHeight: activeParticipants.EffectiveBlockHeight,
			CreatedAtBlockHeight: activeParticipants.CreatedAtBlockHeight,
			EpochId:              activeParticipants.EpochId,
		}

		for i, participant := range activeParticipants.Participants {
			addresses[i], err = common.ConsensusKeyToConsensusAddress(participant.ValidatorKey)

			mlNodes := make([]*contracts.ModelMLNodes, len(participant.MlNodes))
			for i, node := range participant.MlNodes {
				mlNodesData := make([]*contracts.MLNodeInfo, len(node.MlNodes))

				for j, data := range node.MlNodes {
					mlNodesData[j] = &contracts.MLNodeInfo{
						NodeId:             data.NodeId,
						Throughput:         data.Throughput,
						PocWeight:          data.PocWeight,
						TimeslotAllocation: data.TimeslotAllocation,
					}
				}
				mlNodes[i] = &contracts.ModelMLNodes{
					MlNodes: mlNodesData,
				}
			}

			var seed *contracts.RandomSeed
			if participant.Seed != nil {
				seed = &contracts.RandomSeed{
					Participant: participant.Seed.Participant,
					EpochIndex:  participant.Seed.EpochIndex,
					Signature:   participant.Seed.Signature,
				}
			}
			finalParticipants.Participants[i] = &contracts.ActiveParticipant{
				Index:        participant.Index,
				ValidatorKey: participant.ValidatorKey,
				Weight:       participant.Weight,
				InferenceUrl: participant.InferenceUrl,
				Models:       participant.Models,
				Seed:         seed,
				MlNodes:      mlNodes,
			}
			if err != nil {
				logging.Error("Failed to convert public key to address", types.Participants, "error", err)
			}
		}

		block := common.ToContractsBlockProof(blockProof.Proof)
		validators := common.ToContractsValidatorsProof(proofResp.ValidatorsProof)
		proofOps := common.ToCryptoProofOps(proofResp.MerkleProof)
		return &contracts.ActiveParticipantWithProof{
			ActiveParticipants:      finalParticipants,
			Addresses:               addresses,
			ActiveParticipantsBytes: activeParticipantsBytes,
			ProofOps:                proofOps,
			BlockProof:              block,
			ValidatorsProof:         validators,
			ChainId:                 "gonka-mainnet",
		}, nil
	}
}

func GetParticipantsMerkleProof(rpcClient *rpcclient.HTTP, epochId uint64, height int64) (*types.ProofOps, error) {
	dataKey := types.ActiveParticipantsFullKey(epochId)

	result, err := cosmos_client.QueryByKeyWithOptions(rpcClient, "inference", dataKey, height, true)
	if err != nil {
		logging.Error("Failed to query active participant. Req 2", types.Participants, "error", err)
		return nil, err
	}

	if result.Response.ProofOps == nil {
		return nil, errors.New("empty result")
	}
	proofOps := &types.ProofOps{
		Ops: make([]types.ProofOp, 0),
	}
	for _, op := range result.Response.ProofOps.Ops {
		proofOps.Ops = append(proofOps.Ops, types.ProofOp(op))
	}
	proofOps.Epoch = epochId
	return proofOps, nil
}

func verifyProof(epoch uint64, result *coretypes.ResultABCIQuery, appHash []byte) {
	dataKey := types.ActiveParticipantsFullKey(epoch)
	// Build the key path used by proof verification. We percent-encode the raw
	// binary key so the path is a valid UTF-8/URL string.
	verKey := "/inference/" + url.PathEscape(string(dataKey))
	// verKey2 := string(result.Response.Key)
	logging.Info("Attempting verification", types.Participants, "verKey", verKey)
	err := merkleproof.VerifyUsingProofRt(result.Response.ProofOps, appHash, verKey, result.Response.Value)
	if err != nil {
		logging.Error("VerifyUsingProofRt failed", types.Participants, "error", err)
	}

	err = merkleproof.VerifyUsingMerkleProof(result.Response.ProofOps, appHash, "inference", string(dataKey), result.Response.Value)
	if err != nil {
		logging.Error("VerifyUsingMerkleProof failed", types.Participants, "error", err)
	}
}
