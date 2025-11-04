#!/usr/bin/env python3
"""
Manual test script for WebSocket integration.
This script simulates the API node connecting to the ML node via WebSocket.

Usage:
    python test_websocket_manual.py

This will:
1. Connect to the ML node WebSocket endpoint
2. Receive batches from the Sender
3. Send acknowledgments back
4. Log all activity
"""

import asyncio
import json
import sys
from websockets import connect
from websockets.exceptions import ConnectionClosed


MLNODE_WS_URL = "ws://localhost:8080/api/v1/pow/ws"


async def test_websocket_connection():
    """Connect to ML node and receive batches"""
    print(f"Connecting to {MLNODE_WS_URL}...")
    
    try:
        async with connect(MLNODE_WS_URL) as websocket:
            print("WebSocket connection established!")
            print("Waiting for batches from ML node Sender...\n")
            
            batch_count = 0
            
            while True:
                try:
                    message = await asyncio.wait_for(
                        websocket.recv(),
                        timeout=5.0
                    )
                    
                    data = json.loads(message)
                    batch_count += 1
                    
                    print(f"Received batch #{batch_count}")
                    print(f"  Type: {data.get('type')}")
                    print(f"  ID: {data.get('id')}")
                    print(f"  Batch keys: {list(data.get('batch', {}).keys())}")
                    
                    ack = {
                        "type": "ack",
                        "id": data.get("id")
                    }
                    
                    await websocket.send(json.dumps(ack))
                    print(f"  Sent acknowledgment for batch {data.get('id')}\n")
                    
                except asyncio.TimeoutError:
                    print("Waiting for batches... (timeout, will retry)")
                    
                except json.JSONDecodeError as e:
                    print(f"Error decoding JSON: {e}")
                    
    except ConnectionClosed as e:
        print(f"\nWebSocket connection closed: {e}")
        
    except Exception as e:
        print(f"\nError: {e}")
        import traceback
        traceback.print_exc()


async def test_connection_reconnect():
    """Test reconnection behavior"""
    print("\n--- Testing reconnection behavior ---\n")
    
    for attempt in range(3):
        print(f"Connection attempt #{attempt + 1}")
        
        try:
            async with connect(MLNODE_WS_URL) as websocket:
                print("Connected!")
                await asyncio.sleep(2)
                print("Closing connection...\n")
                
        except Exception as e:
            print(f"Error on attempt #{attempt + 1}: {e}\n")
            
        await asyncio.sleep(1)
    
    print("Reconnection test complete")


def main():
    """Main entry point"""
    print("=" * 60)
    print("ML Node WebSocket Test")
    print("=" * 60)
    print()
    
    if len(sys.argv) > 1 and sys.argv[1] == "reconnect":
        asyncio.run(test_connection_reconnect())
    else:
        print("Make sure:")
        print("1. ML node is running (e.g., on localhost:8080)")
        print("2. PoW has been initialized via /pow/init/generate")
        print()
        
        try:
            asyncio.run(test_websocket_connection())
        except KeyboardInterrupt:
            print("\n\nTest interrupted by user")


if __name__ == "__main__":
    main()

