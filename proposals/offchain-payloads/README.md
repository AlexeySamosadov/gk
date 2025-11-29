# Proposal: Offchain Payloads

## Goal / Problem

All inference prompts and response artifacts are currently stored on-chain. Validation requires access to inference artifacts: output tokens and top-k logprobs.

**Current bandwidth consumption:**
- Block size limit: 22MB
- Input tokens: ~0.0023 KB/token mean, ~0.0037 KB/token P90
- Output tokens: ~0.64 KB/token mean, ~0.71 KB/token P90 due to top-k=5 logprobs
- Typical payload: 1000 input + 150 output tokens = ~102 KB
- Current throughput: ~42 requests/second average, ~18 requests/second P90
- Bandwidth constrains inference throughput significantly below compute capacity

**Transaction structure:**
- `MsgStartInference`: stores full prompt payload
- `MsgFinishInference`: stores full response with tokens and logprobs
- `Inference` state: retains both payloads until epoch pruning

## Proposal

Move prompt and response payloads off-chain while preserving validation integrity through peer-to-peer retrieval with cryptographic verification.

**Storage:**
- Local storage on each node, organized by epoch for efficient pruning
- File-based initially, interface supports future PostgreSQL backend
- Only metadata stored on-chain: hashes, token counts, timestamps

**Validation flow:**
1. Executor stores payload locally, broadcasts `MsgFinishInference` with metadata and hash
2. Validator requests payload from executor's REST API endpoint
3. Validator verifies hash matches on-chain commitment before validating
4. On timeout or hash mismatch, validator initiates voting for invalidation

**Authentication protocol:**
- Validator request: `inferenceId` + `timestamp` signed by warm key, timestamp prevents replay attacks
- Model-based authorization: only participants serving same model can sign valid requests
- Executor response: `inferenceId` + `payload` signed by warm key
- Signature provides non-repudiable proof for invalidation without voting
- Uses existing SECP256K1 signature infrastructure

**Future optimization:** Transaction batching in Phase 2 to further reduce overhead by batching multiple inferences per transaction.

## Security Analysis

**1. Payload Withholding Attack:**
Executor commits hash but refuses to serve payload. After timeout, validator initiates voting for invalidation. Economic incentive ensures executor serves payload to receive payment. Residual risk: minor validator resource waste.

**2. Wrong Payload Attack:**
Executor serves payload mismatching committed hash. Validator detects hash mismatch, submits executor's signed payload as cryptographic proof for immediate banning without voting. Warm keys must remain stable during epoch for accountability.

**3. Hash Collision Attack:**
SHA256 collision resistance makes finding alternate payload with same hash cryptographically infeasible. Negligible risk.

**4. Replay Attack:**
Request signature includes timestamp. Executor rejects requests with timestamps >60s old using existing validation logic. Prevents unauthorized repeated access.

**Non-repudiation:** Executor's warm key signature on served payload provides cryptographic proof for fast invalidation without voting when executor serves wrong data.

## Implementation

**Affected Components:**
- `decentralized-api`: Storage layer, REST API endpoints, payload retrieval
- `inference-chain`: Transaction protos, signature verification
- `mlnode`: Payload serving interface
- `testermint`: Integration tests for retrieval scenarios

**Storage Interface:**
```
interface PayloadStorage {
    Store(inferenceId, epochId, payload)
    Retrieve(inferenceId) -> payload
    PruneEpoch(epochId)
}
```
File-based implementation: `storage/{epochId}/{inferenceId}.json`
Hash computation and verification in application layer.

**Implementation Phases:**
1. **Storage Layer** - Create storage module, file-based backend, unit tests
2. **Dual Write** - Store payloads on-chain and locally, verify consistency
3. **REST API Retrieval** - Implement serving endpoint and validator retrieval, fallback to on-chain
4. **Validation Migration** - REST API primary, on-chain fallback for old inferences only
5. **Chain Migration** - Remove payload fields from transactions, keep only metadata
6. **Cleanup** - Remove on-chain payload fields from state for new inferences

Feature flags control phase activation. Each phase independently testable with rollback capability.

**Testing Strategy:**
- Unit: Storage operations, hash verification, pruning
- Integration: REST API retrieval, signature validation, timeout handling, hash mismatches
- Testermint: Unavailability, wrong payloads, concurrent validation
- Load: Verify bandwidth no longer bottlenecks throughput