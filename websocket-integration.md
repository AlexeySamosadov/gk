# WebSocket Integration for Decentralized-API

## Goal

Add WebSocket support to the decentralized-api for receiving PoW batches from ML nodes, maintaining 100% backward compatibility with HTTP callbacks.

## Requirements

1. **If can connect to WebSocket** → WebSocket is used
2. **If not** → HTTP callback is used
3. **Even if WebSocket is used** → still accept requests from HTTP callback (dual mode)
4. **WebSocket connections** → should be alive and attempt to reconnect if interrupted (every 3-5 seconds)
5. **Simple, effective** → no complex abstractions

## Current Architecture

ML nodes already implement WebSocket support (see `/mlnode/packages/pow/docs/WEBSOCKET.md`):
- Endpoint: `/api/v1/pow/ws`
- ML node sends batches via WebSocket if connected
- Falls back to HTTP callback if WebSocket unavailable
- Expects ACK messages for delivered batches

## Design: Per-Node WebSocket Client

### Architecture Decision

**Each NodeWorker manages its own WebSocket connection.**

This follows the existing pattern where each NodeWorker already owns:
- MLNodeClient (HTTP client)
- Command queue
- Node state

Now it will also own:
- WebSocketClient (WebSocket connection)

### Why This Design?

1. **Encapsulation**: Each worker owns everything for its ML node
2. **No circular dependencies**: No centralized manager needed
3. **Lifecycle management**: Worker lifecycle = WebSocket lifecycle
4. **Simple**: One connection per worker, managed by that worker
5. **Consistent**: Follows existing NodeWorker pattern

### Structure

```
Broker
  └─ NodeWorkGroup
       └─ map[nodeID]*NodeWorker
            └─ NodeWorker {
                 mlClient     MLNodeClient
                 wsClient     *WebSocketClient  ← NEW
                 node         *NodeWithState
                 commandQueue chan Command
               }
```

## Implementation Plan

### 1. Create WebSocketClient (per-node)

**File**: `internal/poc/websocket_client.go`

```go
type WebSocketClient struct {
    nodeID      string
    nodeNum     uint64
    wsURL       string
    conn        *websocket.Conn
    recorder    CosmosMessageClient
    stopChan    chan struct{}
    handler     *BatchHandler
}

func NewWebSocketClient(nodeID string, nodeNum uint64, pocURL string, recorder CosmosMessageClient) *WebSocketClient

func (c *WebSocketClient) Start()  // Connect and start message loop
func (c *WebSocketClient) Stop()   // Close connection
func (c *WebSocketClient) run()    // Internal: connect, reconnect, handle messages
```

### 2. Create BatchHandler (replaces adapters)

**File**: `internal/poc/batch_handler.go`

Simple handler that:
- Takes nodeNum → nodeID mapping at creation
- Takes recorder interface directly
- No adapters needed

```go
type BatchHandler struct {
    nodeID   string
    nodeNum  uint64
    recorder CosmosMessageClient
}

func (h *BatchHandler) HandleGeneratedBatch(batch ProofBatch) error
func (h *BatchHandler) HandleValidatedBatch(batch ValidatedBatch) error
```

### 3. Integrate with NodeWorker

**File**: `broker/node_worker.go`

Add to NodeWorker:
```go
type NodeWorker struct {
    // ... existing fields ...
    wsClient *poc.WebSocketClient
}
```

Start WebSocket when PoC starts:
```go
// In StartPoCNodeCommand.Execute()
if result.Succeeded {
    worker.startWebSocket()
}
```

Stop WebSocket when PoC stops:
```go
// In StopNodeCommand.Execute()
worker.stopWebSocket()
```

### 4. HTTP Callbacks Remain Unchanged

**File**: `internal/server/mlnode/post_generated_batches_handler.go`

HTTP callback handlers continue to work as before, using the same BatchHandler logic.

Both WebSocket and HTTP can deliver batches simultaneously.

## Files to Create

1. `internal/poc/websocket_client.go` - Per-node WebSocket client
2. `internal/poc/batch_handler.go` - Batch processing logic (replaces batch_processor + adapters)
3. `internal/poc/websocket_client_test.go` - Unit tests
4. `internal/poc/batch_handler_test.go` - Unit tests

## Files to Modify

1. `broker/node_worker.go` - Add wsClient field, start/stop methods
2. `broker/node_worker_commands.go` - Call start/stop WebSocket on PoC lifecycle
3. `internal/server/mlnode/post_generated_batches_handler.go` - Use BatchHandler

## Files to Delete

1. `internal/poc/websocket_manager.go` - No centralized manager needed
2. `internal/poc/websocket_manager_test.go`
3. `internal/poc/batch_processor.go` - Replaced by simpler BatchHandler
4. `internal/poc/batch_processor_test.go`
5. `broker/broker_adapter.go` - No adapters needed
6. `broker/websocket_manager_interface.go`
7. `broker/mock_websocket_manager.go`
8. `cosmosclient/recorder_adapter.go`

## Key Design Principles

### No Circular Dependencies
- Broker knows about NodeWorker
- NodeWorker knows about WebSocketClient
- WebSocketClient knows nothing about Broker
- One-way dependencies only

### No Adapters
- WebSocketClient takes CosmosMessageClient directly
- BatchHandler takes nodeID and nodeNum at creation
- No BrokerAdapter, no RecorderAdapter needed

### Encapsulation
- Each NodeWorker owns its WebSocket connection
- Connection lifecycle tied to worker lifecycle
- No shared state between workers

### Reconnection Logic
- Each WebSocketClient manages its own reconnection
- Reconnect every 3-5 seconds with jitter
- Connection loop runs in goroutine until Stop() called

## Message Flow

### WebSocket Path
1. ML node sends batch via WebSocket to its specific connection
2. WebSocketClient receives message
3. BatchHandler processes batch (knows nodeID and nodeNum from construction)
4. Batch submitted to chain
5. WebSocketClient sends ACK back to ML node

### HTTP Callback Path (unchanged)
1. ML node sends batch via HTTP POST to callback URL
2. HTTP handler receives batch
3. BatchHandler processes batch (looks up nodeID by nodeNum from batch)
4. Batch submitted to chain
5. HTTP 200 response

## Backward Compatibility

- HTTP callback endpoints: **unchanged**
- ML node initialization: **unchanged** (still sends callback URL)
- Existing tests: **work as-is**
- Both paths can work simultaneously

## Further Steps

1. **Delete old implementation** (manager + adapters)
2. **Create WebSocketClient** (per-node, self-contained)
3. **Create BatchHandler** (simple, no adapters)
4. **Integrate with NodeWorker** (add field, start/stop methods)
5. **Update HTTP handlers** (use BatchHandler)
6. **Write tests** (per-node connection tests)
7. **Verify backward compatibility** (run existing tests)

## Success Criteria

- ✅ WebSocket connections established when PoC starts
- ✅ Automatic reconnection every 3-5 seconds on failure
- ✅ HTTP callbacks still work when WebSocket unavailable
- ✅ Both WebSocket and HTTP can deliver batches
- ✅ No circular dependencies
- ✅ No adapters
- ✅ All existing tests pass
- ✅ Clean, simple, encapsulated design

