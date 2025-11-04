# WebSocket Integration for PoW Sender

## Motivation

### Core Philosophy

This implementation follows Gonka's core principles:
- **Crazy Simple**: Minimal code, maximum clarity
- **Minimalistic**: Single responsibility, no boilerplate, no unnecessary abstraction
- **Standard**: Follow established patterns and project structures
- **Clean**: Pure functionality, no comments explaining obvious code
- **Modern**: Use contemporary tooling and best practices
- **Backward Compatible**: Changes must be 100% backward compatible

### The Problem

In the original design, when the API node initiates PoW on an ML node, it must provide a callback URL:

```python
class PowInitRequestUrl(PowInitRequest):
    url: str  # Callback URL for sending batches
```

The flow:
1. API node calls `/pow/init` or `/pow/init/generate` with callback URL
2. ML node starts the Sender process with this URL
3. API node calls `/pow/phase/generate` or `/pow/phase/validate` to switch phases
4. Sender process generates or validates batches
5. Sender sends batches to callback URL via HTTP POST

Problems:
1. **URL Management**: API node must expose and manage callback endpoints
2. **Network Configuration**: Callback URLs require routing, firewall rules, reverse proxy setup
3. **Reliability**: If callback URL becomes unreachable, batches accumulate in retry queue
4. **Latency**: HTTP request/response overhead for each batch
5. **Connection State**: No way to know if API node is actively listening
6. **Scalability**: Each batch requires a new HTTP connection

### How Endpoints Trigger the Sender

The Sender process runs continuously and responds to phase changes:

```python
# Endpoints in routes.py
@router.post("/pow/phase/generate")
async def start_generate(request: Request):
    manager.pow_controller.start_generate()
    
@router.post("/pow/phase/validate")
async def start_validate(request: Request):
    manager.pow_controller.start_validate()

# Sender process loop in sender.py
def run(self):
    while not self.stop_event.is_set():
        if self.phase.value == Phase.GENERATE:
            generated = self._get_generated()
            if len(generated) > 0:
                self.generated_not_sent.append(generated)
            self._send_generated()
        
        elif self.phase.value == Phase.VALIDATE:
            self.validated_not_sent.extend(self._get_validated())
            self._send_validated()
        
        time.sleep(5)
```

Flow:
1. Controller generates batches and puts them in queues
2. Sender retrieves batches from queues based on current phase
3. Sender sends batches to callback URL
4. Batches remain in retry queue until successfully acknowledged
5. Loop repeats every 5 seconds

## Proposal

### Solution

WebSocket provides a persistent bidirectional connection that solves the callback URL problems.

**Logic:**
- If WebSocket connection is alive: use WebSocket
- If WebSocket connection is not alive but callback URL is set: use HTTP callback
- API node will reinitiate WebSocket if interrupted, ensuring reliability

**Flow:**
1. API node initiates PoW via `/pow/init` with callback URL (backward compatible)
2. API node connects to ML node WebSocket at `/api/v1/pow/ws`
3. Sender process checks WebSocket connection status before sending
4. If WebSocket connected: send batch via WebSocket, wait for acknowledgment
5. If WebSocket not connected: fall back to HTTP POST callback
6. Batch removed from retry queue only after confirmation (WebSocket ACK or HTTP 200)

**Benefits:**
1. **Simpler Architecture**: No callback URL management needed when using WebSocket
2. **Real-time Communication**: Instant batch delivery with acknowledgment
3. **Reliable**: Automatic fallback to HTTP ensures delivery
4. **Decoupled**: WebSocket and HTTP paths are independent
5. **Backward Compatible**: Existing HTTP-only flows work unchanged
6. **Connection Awareness**: Sender knows if API node is actively listening

### Design Principles

**Separation of Concerns:**

**Sender Process** (`sender.py`):
- Accepts optional WebSocket queues and connection state
- Tries WebSocket first with timeout
- Falls back to HTTP on failure
- Remains fully functional without WebSocket (backward compatible)

**WebSocket Endpoint** (`routes.py`):
- Separate endpoint at `/pow/ws`
- Enforces single connection with lock protection
- Runs two async tasks: send batches, receive acknowledgments
- Handles disconnection gracefully

**PowManager** (`manager.py`):
- Creates WebSocket infrastructure (queues, shared state, lock)
- Passes to Sender during initialization
- Cleans up on stop

**Key Principle: Decoupling**

WebSocket functionality is completely optional and decoupled from core PoW logic. If WebSocket infrastructure is not initialized, everything works exactly as before using HTTP callbacks.

## Implementation

### Components

**PowManager** creates WebSocket infrastructure:
```python
def init(self, init_request: PowInitRequest):
    # ... existing controller initialization ...
    
    self.websocket_out_queue = Queue()
    self.websocket_ack_queue = Queue()
    self.websocket_connected = Value('i', 0)
    self.websocket_lock = Lock()
    
    self.pow_sender = Sender(
        url=init_request.url,
        generation_queue=self.pow_controller.generated_batch_queue,
        validation_queue=self.pow_controller.validated_batch_queue,
        phase=self.pow_controller.phase,
        r_target=self.pow_controller.r_target,
        fraud_threshold=init_request.fraud_threshold,
        websocket_out_queue=self.websocket_out_queue,
        websocket_ack_queue=self.websocket_ack_queue,
        websocket_connected=self.websocket_connected,
    )
```

**Sender** tries WebSocket first, falls back to HTTP:
```python
def _send_generated(self):
    if not self.generated_not_sent:
        return

    failed_batches = []

    for batch in self.generated_not_sent:
        sent = self._try_send_via_websocket("generated", batch.__dict__)
        
        if not sent:
            try:
                response = requests.post(f"{self.url}/generated", json=batch.__dict__)
                response.raise_for_status()
            except RequestException as e:
                failed_batches.append(batch)

    self.generated_not_sent = failed_batches

def _try_send_via_websocket(self, batch_type: str, batch: dict, timeout: float = 3.0) -> bool:
    if not self.websocket_connected or self.websocket_connected.value == 0:
        return False
    
    batch_id = str(uuid.uuid4())
    message = {"type": batch_type, "batch": batch, "id": batch_id}
    
    try:
        self.websocket_out_queue.put_nowait(message)
    except queue.Full:
        return False
    
    # Wait for acknowledgment with timeout
    start_time = time.time()
    while time.time() - start_time < timeout:
        try:
            ack = self.websocket_ack_queue.get(timeout=0.1)
            if ack.get("id") == batch_id:
                return True
        except queue.Empty:
            pass
    
    return False
```

**WebSocket Endpoint** at `/pow/ws`:
```python
@router.websocket("/pow/ws")
async def websocket_endpoint(websocket: WebSocket, request: Request):
    manager: PowManager = request.app.state.pow_manager
    
    # Validate PoW is running and enforce single connection
    if not manager.is_running():
        await websocket.close(code=1008, reason="PoW is not running")
        return
    
    with manager.websocket_lock:
        if manager.websocket_connected.value == 1:
            await websocket.close(code=1008, reason="Another client is already connected")
            return
        manager.websocket_connected.value = 1
    
    await websocket.accept()
    
    try:
        # Task 1: Send batches from queue to WebSocket
        async def send_batches():
            while manager.is_running():
                try:
                    message = manager.websocket_out_queue.get(timeout=0.1)
                    await asyncio.wait_for(websocket.send_json(message), timeout=5.0)
                except queue.Empty:
                    await asyncio.sleep(0.1)
        
        # Task 2: Receive acknowledgments from WebSocket to queue
        async def receive_acks():
            while manager.is_running():
                data = await websocket.receive_json()
                if data.get("type") == "ack":
                    data["timestamp"] = time.time()
                    manager.websocket_ack_queue.put_nowait(data)
        
        # Run both tasks, exit on first exception
        send_task = asyncio.create_task(send_batches())
        receive_task = asyncio.create_task(receive_acks())
        
        await asyncio.wait([send_task, receive_task], return_when=asyncio.FIRST_EXCEPTION)
    
    finally:
        with manager.websocket_lock:
            manager.websocket_connected.value = 0
```

### Message Protocol

**ML Node → API Node** (Batch):
```json
{
  "type": "generated",
  "batch": {
    "public_key": "...",
    "block_hash": "...",
    "block_height": 123,
    "nonces": [...],
    "dist": [...],
    "node_id": 0
  },
  "id": "uuid-v4"
}
```

Or `"type": "validated"` for validated batches.

**API Node → ML Node** (Acknowledgment):
```json
{
  "type": "ack",
  "id": "uuid-v4"
}
```

### Fallback Behavior

1. **WebSocket Connected**: Sends batch via WebSocket, waits for ack (3s timeout)
2. **WebSocket Not Connected**: Sends batch via HTTP POST to callback URL
3. **WebSocket Timeout**: Falls back to HTTP POST for that batch
4. **Queue Full**: Falls back to HTTP
5. **Send Timeout**: Client too slow (5s), disconnects

### Connection Management

- **Single Connection**: Lock enforces only one client at a time
- **Atomic State**: Connection state protected by lock
- **Graceful Cleanup**: Lock used during disconnect

### Backward Compatibility

- **No API Changes**: `PowInitRequestUrl` unchanged
- **HTTP Still Works**: If WebSocket not used, HTTP callbacks work as before
- **Graceful Degradation**: WebSocket failure → automatic HTTP fallback
- **Existing Tests Pass**: All integration tests continue to work unchanged

### API Node Integration Example

```python
import asyncio
import json
from websockets import connect

async def connect_to_mlnode(mlnode_url):
    ws_url = mlnode_url.replace("http://", "ws://").replace("https://", "wss://")
    ws_url = f"{ws_url}/api/v1/pow/ws"
    
    async with connect(ws_url) as websocket:
        while True:
            message = await websocket.recv()
            data = json.loads(message)
            
            await process_batch(data["batch"])
            
            ack = {"type": "ack", "id": data["id"]}
            await websocket.send(json.dumps(ack))
```
