# Phase 2: Transaction Batching

## Problem

After offchain payloads implementation, each inference still requires two on-chain transactions: `MsgStartInference` and `MsgFinishInference`. While payloads are offchain, transaction overhead remains:
- Block space consumed by transaction metadata
- Signature verification costs
- State update overhead per transaction

Current transaction size without payloads: ~2-5 KB per inference cycle (start + finish).

## Proposal

Batch multiple inferences in single transactions to reduce per-inference overhead.

**New Transaction Types:**
- `MsgStartInferenceBatched`: Batch 50-100 inference start requests
- `MsgFinishInferenceBatched`: Batch 50-100 inference finish requests

**Batching Strategy:**
- Each node sends batched transaction at most once per 5 blocks
- Batch size: 50-100 inferences per transaction
- Target batch transaction size: <100 KB
- Reduces transaction overhead proportionally to batch size

**Transaction Structure:**
```
message MsgStartInferenceBatched {
    string creator = 1;
    repeated InferenceStartMetadata inferences = 2;
}

message InferenceStartMetadata {
    string inference_id = 1;
    string model_id = 2;
    uint64 timestamp = 3;
    bytes prompt_hash = 4;
    uint32 input_token_count = 5;
}
```

Similar structure for `MsgFinishInferenceBatched` with response metadata.

**Impact:**
- 50x reduction in transaction count
- Proportional reduction in signature verification costs
- Lower block space consumption for same inference throughput
- Combined with offchain payloads: 3-5x throughput increase becomes 10-20x

## Implementation

**Chain Changes:**
- Add `MsgStartInferenceBatched` and `MsgFinishInferenceBatched` proto definitions
- Batch validation: verify all inferences in batch atomically
- State updates: process batched inferences efficiently

**API Node Changes:**
- Buffer inference requests for batching window (e.g., 5 blocks)
- Construct batched transactions when window closes or buffer full
- Handle partial batch failures gracefully

**Backward Compatibility:**
- Support both individual and batched transactions during migration
- Gradual rollout with feature flags
- Old transactions remain valid during transition period

**Testing:**
- Unit: Batch validation logic, buffer management
- Integration: Mixed batched/individual transactions
- Load: Verify throughput scaling matches projections

