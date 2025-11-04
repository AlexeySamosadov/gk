# Broker Integration for Confirmation PoC - Simplified

## Design Rationale

Three key realizations simplified this integration:

1. **POC_SLOT logic is universal** - Same node selection for regular and confirmation PoC. No separate helpers needed.
2. **Block hash lives in the event** - Chain populates `poc_seed_block_hash` at `generation_start_height`. No separate query needed for confirmation PoC.
3. **Commands already handle phases** - Extend existing commands' phase checks instead of creating new ones. Reconciliation self-corrects missed transitions.

Result: Reuse proven patterns. Minimal new code. Maximum reliability.

## Core Principle: Reuse Existing Patterns

**Reliability first**: Apply the same patterns already proven in regular PoC. The logic for node selection is identical - POC_SLOT determines behavior for BOTH regular PoC and confirmation PoC.

## Key Insight: POC_SLOT Logic is Universal

```
POC_SLOT=true  → Continue inference (regular PoC AND confirmation PoC)
POC_SLOT=false → Do PoC generation/validation (regular PoC AND confirmation PoC)
```

The ONLY difference is WHEN this happens:
- Regular PoC: During PoCGeneratePhase/PoCValidatePhase
- Confirmation PoC: During InferencePhase (when event is active)

## Simplified State Flow

```
Every reconciliation cycle:
  1. PhaseTracker updates from EpochInfo (includes confirmation event with poc_seed_block_hash)
  2. Commands check current phase + active confirmation event
  3. For each node:
     - ShouldContinueInference() determines behavior
     - Same logic for regular PoC and confirmation PoC
```

## Integration Requirements

During confirmation PoC, the API service must:

1. **Stop scheduling inference** to POC_SLOT=false nodes during:
   - Grace period (nodes finishing in-flight requests)
   - Generation phase (nodes generating PoC nonces)
   - Validation phase (validators verifying nonces)

2. **Continue serving inference** from POC_SLOT=true nodes (no interruption)

3. **Track confirmation PoC state** from chain via EpochInfo query

## Chain Integration Changes

### Proto Definitions

**Add to `QueryEpochInfoResponse` in `query.proto`**:

```protobuf
message QueryEpochInfoResponse {
  int64  block_height = 1;
  Params params       = 2 [(gogoproto.nullable) = false];
  Epoch  latest_epoch = 3 [(gogoproto.nullable) = false];
  
  // NEW: Include active confirmation PoC event (if any)
  ConfirmationPoCEvent active_confirmation_poc_event = 4;
  bool is_confirmation_poc_active = 5;
}
```

**Why**: `EpochInfo` is already queried every block. Adding confirmation PoC here means **zero additional queries**.

### Phase Tracker Changes

**Modify `phase_tracker.go`**:

```go
type ChainPhaseTracker struct {
	mu sync.RWMutex

	currentBlock       BlockInfo
	latestEpoch        *types.Epoch
	currentEpochParams *types.EpochParams
	currentIsSynced    bool
	
	// NEW: Track active confirmation PoC event
	activeConfirmationPoCEvent *types.ConfirmationPoCEvent
}

// Update caches the latest Epoch information from the network.
func (t *ChainPhaseTracker) Update(
	block BlockInfo, 
	epoch *types.Epoch, 
	params *types.EpochParams, 
	isSynced bool,
	confirmationPoCEvent *types.ConfirmationPoCEvent,  // NEW parameter
) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.currentBlock = block
	t.latestEpoch = epoch
	t.currentEpochParams = params
	t.currentIsSynced = isSynced
	t.activeConfirmationPoCEvent = confirmationPoCEvent  // NEW
}

type EpochState struct {
	LatestEpoch                types.EpochContext
	CurrentBlock               BlockInfo
	CurrentPhase               types.EpochPhase
	IsSynced                   bool
	ActiveConfirmationPoCEvent *types.ConfirmationPoCEvent  // NEW
}

func (t *ChainPhaseTracker) GetCurrentEpochState() *EpochState {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.latestEpoch == nil || t.currentEpochParams == nil {
		return nil
	}

	ec := types.NewEpochContext(*t.latestEpoch, *t.currentEpochParams)
	phase := ec.GetCurrentPhase(t.currentBlock.Height)

	return &EpochState{
		LatestEpoch:                ec,
		CurrentBlock:               t.currentBlock,
		CurrentPhase:               phase,
		IsSynced:                   t.currentIsSynced,
		ActiveConfirmationPoCEvent: t.activeConfirmationPoCEvent,  // NEW
	}
}
```

### Dispatcher Changes

**Modify `new_block_dispatcher.go`**:

```go
// NetworkInfo contains information queried from the network
type NetworkInfo struct {
	EpochParams                types.EpochParams
	IsSynced                   bool
	LatestEpoch                types.Epoch
	BlockHeight                int64
	ActiveConfirmationPoCEvent *types.ConfirmationPoCEvent  // NEW
}

// queryNetworkInfo queries the network for sync status and epoch parameters
func (d *OnNewBlockDispatcher) queryNetworkInfo(ctx context.Context) (NetworkInfo, error) {
	status, err := d.getStatusFunc()
	if err != nil {
		return NetworkInfo{}, err
	}
	isSynced := !status.SyncInfo.CatchingUp

	epochInfo, err := d.queryClient.EpochInfo(ctx, &types.QueryEpochInfoRequest{})
	if err != nil || epochInfo == nil {
		logging.Error("Failed to query epoch info", types.Stages, "error", err)
		return NetworkInfo{}, err
	}

	// NEW: Extract confirmation PoC event from response
	var confirmationEvent *types.ConfirmationPoCEvent
	if epochInfo.IsConfirmationPocActive {
		confirmationEvent = &epochInfo.ActiveConfirmationPocEvent
	}

	return NetworkInfo{
		EpochParams:                *epochInfo.Params.EpochParams,
		IsSynced:                   isSynced,
		LatestEpoch:                epochInfo.LatestEpoch,
		BlockHeight:                epochInfo.BlockHeight,
		ActiveConfirmationPoCEvent: confirmationEvent,  // NEW
	}, nil
}

// Update phase tracker call
d.phaseTracker.Update(
	blockInfo, 
	&networkInfo.LatestEpoch, 
	&networkInfo.EpochParams, 
	networkInfo.IsSynced,
	networkInfo.ActiveConfirmationPoCEvent,  // NEW
)
```

## Key Changes from Original Design

### 1. Remove Dedicated Confirmation Commands

**Remove**: `StartConfirmationPocCommand` - unnecessary.

**Use**: Existing commands work for BOTH regular and confirmation PoC by checking `epochState`.

### 2. Block Hash is in the Event (No Separate Query Needed!)

**Critical simplification**: The chain populates `poc_seed_block_hash` in the `ConfirmationPoCEvent` when transitioning to GENERATION phase. API service doesn't query it separately - it's already there!

**Chain-side flow** (confirmation PoC only):
1. Trigger decision at height H → create event with GRACE_PERIOD phase, `poc_seed_block_hash` is **empty**
2. During BeginBlock at `generation_start_height` → transition event to GENERATION phase, set `poc_seed_block_hash = GetBlockHash(generation_start_height - 1)`
3. API service receives updated event with **now-populated** hash at block `generation_start_height`

**API service flow**:
```go
if event := epochState.ActiveConfirmationPoCEvent; event != nil {
    if event.Phase == CONFIRMATION_POC_GENERATION || event.Phase == CONFIRMATION_POC_VALIDATION {
        // Use hash directly from event (already populated by chain)
        blockHash := event.PocSeedBlockHash
        keyHeight := event.TriggerHeight
        // Use for PoC generation
    }
}
```

**Timing is critical**:
- Hash is **NOT** set at trigger time (would allow precomputation)
- Hash is set **AT** `generation_start_height` by chain BeginBlock
- Hash value is **FROM** `generation_start_height - 1` (the previous block)
- API receives populated hash when it queries at `generation_start_height`

**Benefits**:
- ✅ No separate GetBlockHash query needed for confirmation PoC
- ✅ Security handled chain-side (hash set at exact right moment)
- ✅ Single source of truth (the event itself)
- ✅ Regular PoC unchanged (still queries as before)

### 3. Extend StartPocCommand to Handle Both Regular and Confirmation PoC

**Current**: `StartPocCommand` checks `CurrentPhase == PoCGeneratePhase`

**Extended**: Also trigger during InferencePhase if confirmation PoC is active:

```go
func (c StartPocCommand) Execute(b *Broker) {
    epochState := b.phaseTracker.GetCurrentEpochState()
    if epochState.IsNilOrNotSynced() {
        c.Response <- false
        return
    }
    
    // Check if we should run PoC (regular OR confirmation)
    shouldRunPoC := false
    
    // Regular PoC phases
    if epochState.CurrentPhase == types.PoCGeneratePhase {
        shouldRunPoC = true
    }
    
    // Confirmation PoC during inference
    if epochState.CurrentPhase == types.InferencePhase && epochState.ActiveConfirmationPoCEvent != nil {
        event := epochState.ActiveConfirmationPoCEvent
        currentHeight := epochState.CurrentBlock.Height
        
        // Check if we're in generation phase of confirmation PoC
        if currentHeight >= event.GenerationStartHeight && currentHeight <= event.GenerationEndHeight {
            shouldRunPoC = true
        }
    }
    
    if !shouldRunPoC {
        c.Response <- false
        return
    }
    
    // SAME LOGIC for both regular and confirmation PoC
    b.mu.Lock()
    for _, node := range b.nodes {
        if !node.State.ShouldBeOperational(epochState.LatestEpoch.EpochIndex, epochState.CurrentPhase) {
            node.State.IntendedStatus = types.HardwareNodeStatus_STOPPED
        } else if node.State.ShouldContinueInference() {  // POC_SLOT=true
            node.State.IntendedStatus = types.HardwareNodeStatus_INFERENCE
        } else {  // POC_SLOT=false
            node.State.IntendedStatus = types.HardwareNodeStatus_POC
            node.State.PocIntendedStatus = PocStatusGenerating
        }
    }
    b.mu.Unlock()
    
    c.Response <- true
}
```

**Key insight**: The node selection logic (`ShouldContinueInference()`) is IDENTICAL for both regular and confirmation PoC. Only the trigger condition differs.

### 4. Simplified prefetchPocParams - Use Event's Block Hash

```go
func (b *Broker) prefetchPocParams(epochState chainphase.EpochState, nodesToDispatch map[string]*NodeWithState, blockHeight int64) (*pocParams, error) {
    needsPocParams := false
    for _, node := range nodesToDispatch {
        if node.State.IntendedStatus == types.HardwareNodeStatus_POC {
            if node.State.PocIntendedStatus == PocStatusGenerating || node.State.PocIntendedStatus == PocStatusValidating {
                needsPocParams = true
                break
            }
        }
    }
    
    if !needsPocParams {
        return nil, nil
    }
    
    // CONFIRMATION PoC - use hash from event (populated by chain at generation_start_height)
    if epochState.CurrentPhase == types.InferencePhase && epochState.ActiveConfirmationPoCEvent != nil {
        event := epochState.ActiveConfirmationPoCEvent
        
        // Hash was set by chain at generation_start_height = GetBlockHash(generation_start_height - 1)
        // Trigger_height is used as storage key to distinguish from regular PoC
        return &pocParams{
            startPoCBlockHeight: event.TriggerHeight,      // Key for PoCBatches/PoCValidations storage
            startPoCBlockHash:   event.PocSeedBlockHash,   // From event - NO query needed!
        }, nil
    }
    
    // REGULAR PoC - query hash at poc_start_block_height as currently done
    return b.queryCurrentPoCParams(epochState.LatestEpoch.PocStartBlockHeight)
}
```

**Benefits**:
- ✅ No separate GetBlockHash query for confirmation PoC
- ✅ Hash and key both come from event
- ✅ Simpler, fewer moving parts

### 5. nodeAvailable Checks IntendedStatus (No Special Case Needed)

**Current behavior already correct**: `nodeAvailable()` checks `IntendedStatus`. If node is in PoC mode (regular OR confirmation), it returns false.

```go
func (b *Broker) nodeAvailable(node *NodeWithState, neededModel string, currentEpoch uint64, currentPhase types.EpochPhase) (bool, NodeNotAvailableReason) {
    // ... existing checks ...
    
    // This check works for BOTH regular PoC and confirmation PoC
    if node.State.IntendedStatus == types.HardwareNodeStatus_POC {
        return false, "Node is in PoC mode"
    }
    
    // ... rest of checks ...
}
```

**No changes needed** - existing logic already handles confirmation PoC correctly once `IntendedStatus` is set by commands.

### 6. Reconciliation Every Block in Inference

**Use Option A**: Simple, reliable, performant.

```go
var DefaultReconciliationConfig = MlNodeReconciliationConfig{
    Inference: &MlNodeStageReconciliationConfig{
        BlockInterval: 1,  // Every block for confirmation PoC responsiveness
        TimeInterval:  30 * time.Second,
    },
    PoC: &MlNodeStageReconciliationConfig{
        BlockInterval: 1,
        TimeInterval:  30 * time.Second,
    },
}
```

**Why**: Confirmation PoC transitions need to be fast. State updates already happen every block anyway.

### 7. Add handlePhaseTransitions Logic for Confirmation PoC

**Pattern**: Use same approach as regular PoC - check at specific heights and send commands.

```go
// In new_block_dispatcher.go handlePhaseTransitions
func (d *OnNewBlockDispatcher) handlePhaseTransitions(epochState chainphase.EpochState) {
    blockHeight := epochState.CurrentBlock.Height
    
    // ... existing regular PoC transition checks ...
    
    // NEW: Confirmation PoC transitions (only during inference phase)
    if epochState.CurrentPhase == types.InferencePhase && epochState.ActiveConfirmationPoCEvent != nil {
        event := epochState.ActiveConfirmationPoCEvent
        
        // Start generation
        if blockHeight == event.GenerationStartHeight {
            logging.Info("Confirmation PoC generation starting", types.PoC,
                "trigger_height", event.TriggerHeight,
                "block_hash", event.PocSeedBlockHash)  // Already set by chain!
            
            command := broker.NewStartPocCommand()
            if err := d.nodeBroker.QueueMessage(command); err != nil {
                logging.Error("Failed to send confirmation PoC start command", types.PoC, "error", err)
            }
        }
        
        // Start validation
        if blockHeight == event.ValidationStartHeight {
            logging.Info("Confirmation PoC validation starting", types.PoC,
                "trigger_height", event.TriggerHeight)
            
            command := broker.NewInitValidateCommand()
            if err := d.nodeBroker.QueueMessage(command); err != nil {
                logging.Error("Failed to send confirmation PoC validate command", types.PoC, "error", err)
            }
            
            // Start validation sampling
            go func() {
                d.nodePocOrchestrator.ValidateReceivedBatches(event.TriggerHeight)  // Use trigger_height as key
            }()
        }
        
        // End of event
        if blockHeight == event.ValidationEndHeight + 1 {
            logging.Info("Confirmation PoC completed", types.PoC,
                "trigger_height", event.TriggerHeight)
            
            command := broker.NewInferenceUpAllCommand()
            if err := d.nodeBroker.QueueMessage(command); err != nil {
                logging.Error("Failed to send inference up command", types.PoC, "error", err)
            }
        }
    }
}
```

**Pattern similarity**: This mirrors how regular PoC transitions work - check heights, send commands. Commands then update `IntendedStatus` for nodes, reconciliation applies changes.

### 8. No Need for ShouldParticipateInConfirmationPoC Helper

**Why**: The existing `ShouldContinueInference()` method already has the logic we need!

```go
// Already exists in broker.go:
func (s *NodeState) ShouldContinueInference() bool {
    for _, mlNodeInfo := range s.EpochMLNodes {
        if len(mlNodeInfo.TimeslotAllocation) > 1 && mlNodeInfo.TimeslotAllocation[1] {  // POC_SLOT=true
            return true
        }
    }
    return false
}
```

**Usage for both regular and confirmation PoC**:
- `ShouldContinueInference() == true` → node continues inference
- `ShouldContinueInference() == false` → node does PoC

Same method, same logic, works for BOTH cases. No new helper needed.

## Summary: What Was Simplified

### Removed
1. ❌ `StartConfirmationPocCommand` - use existing `StartPocCommand` instead
2. ❌ Separate block hash query for confirmation PoC - hash is in the event!
3. ❌ New helper methods - reuse `ShouldContinueInference()`
4. ❌ Complex state determination logic - commands already handle phase checks

### Key Simplifications
1. ✅ **POC_SLOT logic is universal** - same for regular and confirmation PoC
2. ✅ **Block hash in event** - chain sets it, API reads it, no separate query
3. ✅ **Reuse existing commands** - just extend their phase checks
4. ✅ **Same transition pattern** - check heights in handlePhaseTransitions, send commands
5. ✅ **Existing reconciliation works** - no changes needed to core reconciliation logic

### Reliability
1. ✅ **Same proven patterns**: Uses exact same flow as regular PoC
2. ✅ **Single source of truth**: Event from PhaseTracker contains everything
3. ✅ **Commands are idempotent**: Already handle repeated calls correctly
4. ✅ **Reconciliation self-corrects**: If command missed, next cycle applies state

## Complete Flow

```
Block N (trigger decision during InferencePhase):
  - Chain creates ConfirmationPoCEvent with GRACE_PERIOD phase
  - poc_seed_block_hash is empty
  - API receives event via EpochInfo query
  
Block N to N+grace_period:
  - Nodes continue inference
  - No special API action (grace period automatic)
  
Block N+grace_period (generation_start_height):
  - Chain BeginBlock: transitions event to GENERATION phase
  - Chain BeginBlock: sets poc_seed_block_hash = GetBlockHash(generation_start_height - 1)
  - API queries EpochInfo: receives updated event with NOW-POPULATED hash
  - handlePhaseTransitions: detects height match
  - Sends StartPocCommand
  - Command: sets IntendedStatus=POC for POC_SLOT=false nodes
  - Reconciliation: calls prefetchPocParams
  - prefetchPocParams: detects confirmation PoC, uses hash from event
  - Dispatches StartPoCNodeCommand with:
    - BlockHeight: event.TriggerHeight (for storage key in chain)
    - BlockHash: event.PocSeedBlockHash (from event - for PoC seed)
  
Note: Regular PoC still queries block hash separately as it currently does.
  
Block N+grace_period to N+...+generation_end:
  - Nodes generate PoC nonces
  - Submit MsgSubmitPoCBatch with trigger_height as key
  
Block N+...+validation_start:
  - handlePhaseTransitions detects height match
  - Sends InitValidateCommand
  - Starts ValidateReceivedBatches(trigger_height)
  - Validators sample and verify
  
Block N+...+validation_end:
  - Submit MsgSubmitPoCValidation with trigger_height
  
Block N+...+validation_end+1:
  - handlePhaseTransitions detects height match  
  - Sends InferenceUpAllCommand
  - Nodes return to inference
  - Chain marks event as COMPLETED
```

## Testing Checklist

- [ ] EpochInfo query returns nil when no confirmation PoC active
- [ ] EpochInfo query returns active event during grace period
- [ ] EpochInfo query returns event with populated hash during generation/validation
- [ ] Phase Tracker correctly stores and exposes confirmation event
- [ ] nodeAvailable rejects POC_SLOT=false nodes during grace/generation/validation
- [ ] nodeAvailable allows POC_SLOT=true nodes during confirmation PoC
- [ ] StartPocCommand executes for regular PoC
- [ ] StartPocCommand executes for confirmation PoC during generation window
- [ ] StartPocCommand does NOT execute during inference without confirmation event
- [ ] InitValidateCommand works for both regular and confirmation PoC
- [ ] prefetchPocParams returns event hash for confirmation PoC
- [ ] prefetchPocParams queries hash for regular PoC
- [ ] Nodes transition to PoC generation at correct height
- [ ] Block hash from event used correctly (not queried)
- [ ] Nodes return to inference after event completes
- [ ] Multiple confirmation events per epoch handled correctly (minimum weight)

## Testing Strategy

Test the extended commands with mock epoch states:

```go
func TestStartPocCommand_RegularPoC(t *testing.T) {
    // Regular PoC - should execute
    epochState := mockEpochState(phase: PoCGeneratePhase)
    cmd := StartPocCommand{Response: make(chan bool)}
    cmd.Execute(broker)
    assert.True(t, <-cmd.Response)
}

func TestStartPocCommand_ConfirmationPoC(t *testing.T) {
    // Confirmation PoC during inference - should execute
    epochState := mockEpochState(
        phase: InferencePhase,
        confirmationEvent: &ConfirmationPoCEvent{
            Phase: CONFIRMATION_POC_GENERATION,
            GenerationStartHeight: 1000,
            GenerationEndHeight: 1200,
            PocSeedBlockHash: "abc123...",
        },
        currentHeight: 1100,
    )
    cmd := StartPocCommand{Response: make(chan bool)}
    cmd.Execute(broker)
    assert.True(t, <-cmd.Response)
}

func TestStartPocCommand_NoTrigger(t *testing.T) {
    // Inference phase, no confirmation event - should NOT execute
    epochState := mockEpochState(phase: InferencePhase)
    cmd := StartPocCommand{Response: make(chan bool)}
    cmd.Execute(broker)
    assert.False(t, <-cmd.Response)
}

func TestPrefetchPocParams_ConfirmationPoC(t *testing.T) {
    // Confirmation PoC - should use hash from event
    epochState := mockEpochState(
        phase: InferencePhase,
        confirmationEvent: &ConfirmationPoCEvent{
            TriggerHeight: 1000,
            PocSeedBlockHash: "hash_from_event",
        },
    )
    params, err := broker.prefetchPocParams(epochState, nodes, 1100)
    assert.NoError(t, err)
    assert.Equal(t, 1000, params.startPoCBlockHeight) // trigger_height as key
    assert.Equal(t, "hash_from_event", params.startPoCBlockHash) // from event!
}

func TestPrefetchPocParams_RegularPoC(t *testing.T) {
    // Regular PoC - should query hash as usual
    epochState := mockEpochState(phase: PoCGeneratePhase)
    params, err := broker.prefetchPocParams(epochState, nodes, 5000)
    assert.NoError(t, err)
    // Verify hash was queried (not from event)
}
```

## Summary of Changes by Component

### Chain Integration
1. **Proto**: Add `active_confirmation_poc_event` and `is_confirmation_poc_active` to `QueryEpochInfoResponse`
2. **Zero cost**: Reuses existing `EpochInfo` query that runs every block

### Phase Tracker (phase_tracker.go)
1. Add `activeConfirmationPoCEvent` field to `ChainPhaseTracker`
2. Add parameter to `Update()` method
3. Add `ActiveConfirmationPoCEvent` to `EpochState` struct
4. Thread-safe access via existing mutex

### Dispatcher (new_block_dispatcher.go)
1. Add `ActiveConfirmationPoCEvent` to `NetworkInfo`
2. Extract event from `EpochInfo` query in `queryNetworkInfo()`
3. Pass event to `phaseTracker.Update()`
4. Add confirmation PoC transitions to `handlePhaseTransitions()`
5. Change `Inference.BlockInterval` from 5 to 1 for responsiveness

### Broker (broker.go)
1. No separate state storage - use `phaseTracker.GetCurrentEpochState()`
2. No changes to `nodeAvailable()` - existing logic works
3. No new helper methods - reuse `ShouldContinueInference()`

### Commands (state_commands.go)
1. Extend `StartPocCommand` to check for confirmation PoC during inference
2. Extend `InitValidateCommand` (similar pattern)
3. Reuse existing `InferenceUpAllCommand`

### PoC Parameters (broker.go)
1. Modify `prefetchPocParams()` to detect confirmation PoC
2. Use hash from event (not query) for confirmation PoC
3. Regular PoC unchanged

## Critical Security Points

1. **Block hash timing**: Hash is set by chain at `generation_start_height`, NOT at trigger time
   - Chain sets: `poc_seed_block_hash = GetBlockHash(generation_start_height - 1)`
   - Prevents precomputation during grace period
   - API receives already-populated hash

2. **Two heights for confirmation PoC**:
   - `poc_seed_block_hash` value: From block at `generation_start_height - 1`
   - `trigger_height`: Storage key in PoCBatches/PoCValidations collections

3. **Grace period**: API stops scheduling inference when event enters GRACE_PERIOD phase
   - Allows nodes to finish in-flight requests
   - Prepares for PoC generation

## Conclusion

**Significantly simpler than original design**:
- No new command types
- No separate block hash query (it's in the event!)
- No new helper methods (reuse existing)
- Same proven patterns as regular PoC

**Maximum reliability**:
- Reuses battle-tested code paths
- Commands already idempotent
- Reconciliation already self-correcting
- Single source of truth (event from chain)
- Zero additional queries

