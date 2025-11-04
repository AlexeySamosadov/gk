package broker

import (
	"decentralized-api/cosmosclient"
	"decentralized-api/logging"
	"decentralized-api/mlnodeclient"

	"github.com/google/uuid"
	"github.com/productscience/inference/api/inference/inference"
	"github.com/productscience/inference/x/inference/types"
)

type BatchHandler struct {
	recorder cosmosclient.CosmosMessageClient
}

func NewBatchHandler(recorder cosmosclient.CosmosMessageClient) *BatchHandler {
	return &BatchHandler{
		recorder: recorder,
	}
}

func (h *BatchHandler) HandleGeneratedBatch(nodeID string, batch mlnodeclient.ProofBatch) error {
	logging.Debug("ProofBatch. Received", types.PoC, "body", batch)

	logging.Info("ProofBatch. Processing batch", types.PoC,
		"nodeId", nodeID,
		"nodeNum", batch.NodeNum)

	msg := &inference.MsgSubmitPocBatch{
		PocStageStartBlockHeight: batch.BlockHeight,
		Nonces:                   batch.Nonces,
		Dist:                     batch.Dist,
		BatchId:                  uuid.New().String(),
		NodeId:                   nodeID,
	}

	if err := h.recorder.SubmitPocBatch(msg); err != nil {
		logging.Error("ProofBatch. Failed to submit MsgSubmitPocBatch", types.PoC, "error", err)
		return err
	}

	return nil
}

func (h *BatchHandler) HandleValidatedBatch(batch mlnodeclient.ValidatedBatch) error {
	logging.Debug("ValidateReceivedBatches. ValidatedProofBatch received", types.PoC, "body", batch)

	address, err := cosmosclient.PubKeyToAddress(batch.PublicKey)
	if err != nil {
		logging.Error("ValidateReceivedBatches. Failed to convert public key to address", types.PoC,
			"publicKey", batch.PublicKey,
			"NInvalid", batch.NInvalid,
			"ProbabilityHonest", batch.ProbabilityHonest,
			"FraudDetected", batch.FraudDetected,
			"error", err)
		return err
	}

	logging.Info("ValidateReceivedBatches. ValidatedProofBatch received", types.PoC,
		"participant", address,
		"NInvalid", batch.NInvalid,
		"ProbabilityHonest", batch.ProbabilityHonest,
		"FraudDetected", batch.FraudDetected)

	msg := &inference.MsgSubmitPocValidation{
		ParticipantAddress:       address,
		PocStageStartBlockHeight: batch.BlockHeight,
		Nonces:                   batch.Nonces,
		Dist:                     batch.Dist,
		ReceivedDist:             batch.ReceivedDist,
		RTarget:                  batch.RTarget,
		FraudThreshold:           batch.FraudThreshold,
		NInvalid:                 batch.NInvalid,
		ProbabilityHonest:        batch.ProbabilityHonest,
		FraudDetected:            batch.FraudDetected,
	}

	emptyArrays(msg)

	if err := h.recorder.SubmitPoCValidation(msg); err != nil {
		logging.Error("ValidateReceivedBatches. Failed to submit MsgSubmitValidatedPocBatch", types.PoC,
			"participant", address,
			"error", err)
		return err
	}

	return nil
}

func emptyArrays(msg *inference.MsgSubmitPocValidation) {
	msg.Dist = make([]float64, 0)
	msg.ReceivedDist = make([]float64, 0)
	msg.Nonces = make([]int64, 0)
}

