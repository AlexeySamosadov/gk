package validation

import (
	"context"
	"decentralized-api/cosmosclient"
	"decentralized-api/logging"
	"decentralized-api/payloadstorage"
	apiutils "decentralized-api/utils"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/productscience/inference/cmd/inferenced/cmd"
	"github.com/productscience/inference/x/inference/calculations"
	"github.com/productscience/inference/x/inference/types"
)

// HTTP client with timeout for payload retrieval
var payloadRetrievalClient = apiutils.NewHttpClient(30 * time.Second)

// PayloadResponse matches the executor endpoint response
type PayloadResponse struct {
	InferenceId       string `json:"inference_id"`
	PromptPayload     string `json:"prompt_payload"`
	ResponsePayload   string `json:"response_payload"`
	ExecutorSignature string `json:"executor_signature"`
}

// RetrievePayloadsFromExecutor makes a single REST call to executor.
// Returns payloads or error. No retry logic - handled by caller.
func RetrievePayloadsFromExecutor(
	ctx context.Context,
	inferenceId string,
	executorAddress string,
	epochId uint64,
	recorder cosmosclient.CosmosMessageClient,
) (promptPayload, responsePayload string, err error) {
	// 1. Get executor's InferenceUrl from chain
	queryClient := recorder.NewInferenceQueryClient()
	participantResp, err := queryClient.Participant(ctx, &types.QueryGetParticipantRequest{
		Index: executorAddress,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get executor participant: %w", err)
	}

	executorUrl := participantResp.Participant.InferenceUrl
	if executorUrl == "" {
		return "", "", fmt.Errorf("executor has no inference URL")
	}

	// 2. Build request using url.JoinPath (handles trailing slashes safely)
	// URL-encode inferenceId since it's base64 and may contain '/' characters
	requestUrl, err := url.JoinPath(executorUrl, "v1/inference", url.PathEscape(inferenceId), "payloads")
	if err != nil {
		return "", "", fmt.Errorf("failed to build request URL: %w", err)
	}

	// 3. Create signed request
	timestamp := time.Now().UnixNano()
	validatorAddress := recorder.GetAccountAddress()

	signature, err := signPayloadRequest(inferenceId, timestamp, validatorAddress, recorder)
	if err != nil {
		return "", "", fmt.Errorf("failed to sign request: %w", err)
	}

	// 4. Make HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set(apiutils.XValidatorAddressHeader, validatorAddress)
	req.Header.Set(apiutils.XTimestampHeader, strconv.FormatInt(timestamp, 10))
	req.Header.Set(apiutils.XEpochIdHeader, strconv.FormatUint(epochId, 10))
	req.Header.Set(apiutils.AuthorizationHeader, signature)

	resp, err := payloadRetrievalClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", "", fmt.Errorf("payload not found on executor")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("executor returned status %d: %s", resp.StatusCode, string(body))
	}

	// 5. Parse response
	var payloadResp PayloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&payloadResp); err != nil {
		return "", "", fmt.Errorf("failed to decode response: %w", err)
	}

	// 6. Verify hashes match on-chain commitment
	inference, err := queryClient.Inference(ctx, &types.QueryGetInferenceRequest{Index: inferenceId})
	if err != nil {
		return "", "", fmt.Errorf("failed to get inference from chain: %w", err)
	}

	actualPromptHash, err := payloadstorage.ComputePromptHash(payloadResp.PromptPayload)
	if err != nil {
		return "", "", fmt.Errorf("failed to compute prompt hash: %w", err)
	}
	if inference.Inference.PromptHash != "" && actualPromptHash != inference.Inference.PromptHash {
		return "", "", fmt.Errorf("prompt hash mismatch: expected %s, got %s",
			inference.Inference.PromptHash, actualPromptHash)
	}

	actualResponseHash, err := payloadstorage.ComputeResponseHash(payloadResp.ResponsePayload)
	if err != nil {
		return "", "", fmt.Errorf("failed to compute response hash: %w", err)
	}
	if inference.Inference.ResponseHash != "" && actualResponseHash != inference.Inference.ResponseHash {
		return "", "", fmt.Errorf("response hash mismatch: expected %s, got %s",
			inference.Inference.ResponseHash, actualResponseHash)
	}

	// 7. Verify executor signature for non-repudiation
	// Get executor pubkeys (granter + grantees/warm keys)
	grantees, err := queryClient.GranteesByMessageType(ctx, &types.QueryGranteesByMessageTypeRequest{
		GranterAddress: executorAddress,
		MessageTypeUrl: "/inference.inference.MsgStartInference",
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get executor grantees: %w", err)
	}
	executorPubkeys := make([]string, 0, len(grantees.Grantees)+1)
	for _, g := range grantees.Grantees {
		executorPubkeys = append(executorPubkeys, g.PubKey)
	}
	// Get executor's own pubkey
	executorParticipant, err := queryClient.InferenceParticipant(ctx, &types.QueryInferenceParticipantRequest{
		Address: executorAddress,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to get executor pubkey: %w", err)
	}
	executorPubkeys = append(executorPubkeys, executorParticipant.Pubkey)

	if err := verifyExecutorPayloadSignature(
		inferenceId,
		payloadResp.PromptPayload,
		payloadResp.ResponsePayload,
		payloadResp.ExecutorSignature,
		executorAddress,
		executorPubkeys,
	); err != nil {
		return "", "", fmt.Errorf("executor signature verification failed: %w", err)
	}

	logging.Debug("Successfully retrieved and verified payloads from executor", types.Validation,
		"inferenceId", inferenceId, "executorAddress", executorAddress)

	return payloadResp.PromptPayload, payloadResp.ResponsePayload, nil
}

// DEPRECATED: retrievePayloadsFromChain queries chain for payload fields.
// Only used for inferences created before offchain payload upgrade.
// Will be removed in Phase 6 when payload fields are eliminated from chain.
func retrievePayloadsFromChain(
	ctx context.Context,
	inferenceId string,
	recorder cosmosclient.CosmosMessageClient,
) (promptPayload, responsePayload string, err error) {
	logging.Warn("Using DEPRECATED chain payload retrieval", types.Validation,
		"inferenceId", inferenceId)

	queryClient := recorder.NewInferenceQueryClient()
	response, err := queryClient.Inference(ctx, &types.QueryGetInferenceRequest{Index: inferenceId})
	if err != nil {
		return "", "", fmt.Errorf("failed to query inference: %w", err)
	}

	return response.Inference.PromptPayload, response.Inference.ResponsePayload, nil
}

// signPayloadRequest signs the payload retrieval request with validator's key
// Validator signs: inferenceId + timestamp + validatorAddress
func signPayloadRequest(
	inferenceId string,
	timestamp int64,
	validatorAddress string,
	recorder cosmosclient.CosmosMessageClient,
) (string, error) {
	components := calculations.SignatureComponents{
		Payload:         inferenceId,
		Timestamp:       timestamp,
		TransferAddress: validatorAddress,
		ExecutorAddress: "",
	}

	signerAddressStr := recorder.GetSignerAddress()
	signerAddress, err := sdk.AccAddressFromBech32(signerAddressStr)
	if err != nil {
		return "", err
	}
	accountSigner := &cmd.AccountSigner{
		Addr:    signerAddress,
		Keyring: recorder.GetKeyring(),
	}

	return calculations.Sign(accountSigner, components, calculations.Developer)
}

// verifyExecutorPayloadSignature verifies the executor's signature on the payload response.
// This provides non-repudiation: if executor serves wrong payload, validator has cryptographic proof.
// Executor signs: inferenceId + promptHash + responseHash (with timestamp=0)
func verifyExecutorPayloadSignature(
	inferenceId string,
	promptPayload string,
	responsePayload string,
	signature string,
	executorAddress string,
	executorPubkeys []string,
) error {
	if signature == "" {
		return fmt.Errorf("executor signature is empty")
	}

	promptHash := apiutils.GenerateSHA256Hash(promptPayload)
	responseHash := apiutils.GenerateSHA256Hash(responsePayload)
	payload := inferenceId + promptHash + responseHash

	components := calculations.SignatureComponents{
		Payload:         payload,
		Timestamp:       0, // Executor uses timestamp=0 for non-repudiation signatures
		TransferAddress: executorAddress,
		ExecutorAddress: "",
	}

	return calculations.ValidateSignatureWithGrantees(components, calculations.Developer, executorPubkeys, signature)
}
