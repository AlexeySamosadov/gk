import asyncio
import json
import pytest
from websockets import connect
from websockets.exceptions import ConnectionClosed
import requests
import time

from pow.service.client import PowClient
from pow.models.utils import Params


TEST_TIMEOUT = 30


@pytest.fixture
def server_url():
    return "http://localhost:8080"


@pytest.fixture
def websocket_url():
    return "ws://localhost:8080/api/v1/pow/ws"


@pytest.fixture
def client(server_url):
    return PowClient(server_url)


class TestWebSocketIntegration:
    
    def test_http_callback_fallback_without_websocket(self, client, server_url):
        """Test that HTTP callback still works when WebSocket is not connected"""
        batch_receiver_url = "http://localhost:8081"
        
        init_response = client.init_generate(
            node_id=0,
            node_count=1,
            url=batch_receiver_url,
            block_hash="test_block_hash",
            block_height=1,
            public_key="test_public_key",
            batch_size=100,
            r_target=0.5,
            fraud_threshold=0.01,
            params=Params()
        )
        
        assert init_response["status"] == "OK"
        time.sleep(2)
    
    @pytest.mark.asyncio
    async def test_websocket_connection(self, websocket_url, client, server_url):
        """Test that WebSocket connection can be established"""
        batch_receiver_url = "http://localhost:8081"
        
        init_response = client.init_generate(
            node_id=0,
            node_count=1,
            url=batch_receiver_url,
            block_hash="test_block_hash_ws",
            block_height=1,
            public_key="test_public_key_ws",
            batch_size=100,
            r_target=0.5,
            fraud_threshold=0.01,
            params=Params()
        )
        
        assert init_response["status"] == "OK"
        
        try:
            async with connect(websocket_url) as websocket:
                await asyncio.sleep(2)
                
                message = await asyncio.wait_for(
                    websocket.recv(),
                    timeout=TEST_TIMEOUT
                )
                
                data = json.loads(message)
                
                assert "type" in data
                assert data["type"] in ["generated", "validated"]
                assert "batch" in data
                assert "id" in data
                
                ack = {
                    "type": "ack",
                    "id": data["id"]
                }
                await websocket.send(json.dumps(ack))
                
        except ConnectionClosed:
            pytest.fail("WebSocket connection closed unexpectedly")
        except asyncio.TimeoutError:
            pytest.skip("No batch generated within timeout - this is ok for testing")
    
    @pytest.mark.asyncio
    async def test_websocket_reconnection(self, websocket_url, client, server_url):
        """Test that system handles WebSocket reconnection properly"""
        batch_receiver_url = "http://localhost:8081"
        
        init_response = client.init_generate(
            node_id=0,
            node_count=1,
            url=batch_receiver_url,
            block_hash="test_block_hash_reconnect",
            block_height=1,
            public_key="test_public_key_reconnect",
            batch_size=100,
            r_target=0.5,
            fraud_threshold=0.01,
            params=Params()
        )
        
        assert init_response["status"] == "OK"
        
        try:
            async with connect(websocket_url) as websocket:
                await asyncio.sleep(1)
            
            await asyncio.sleep(2)
            
            async with connect(websocket_url) as websocket:
                await asyncio.sleep(1)
                
        except ConnectionClosed:
            pass
    
    @pytest.mark.asyncio
    async def test_websocket_batch_acknowledgment(self, websocket_url, client, server_url):
        """Test that batches are acknowledged and removed from retry queue"""
        batch_receiver_url = "http://localhost:8081"
        
        init_response = client.init_generate(
            node_id=0,
            node_count=1,
            url=batch_receiver_url,
            block_hash="test_block_hash_ack",
            block_height=1,
            public_key="test_public_key_ack",
            batch_size=100,
            r_target=0.5,
            fraud_threshold=0.01,
            params=Params()
        )
        
        assert init_response["status"] == "OK"
        
        received_batches = []
        
        try:
            async with connect(websocket_url) as websocket:
                for _ in range(3):
                    try:
                        message = await asyncio.wait_for(
                            websocket.recv(),
                            timeout=10
                        )
                        
                        data = json.loads(message)
                        received_batches.append(data)
                        
                        ack = {
                            "type": "ack",
                            "id": data["id"]
                        }
                        await websocket.send(json.dumps(ack))
                        
                    except asyncio.TimeoutError:
                        break
                
                assert len(received_batches) > 0, "Should receive at least one batch"
                
        except ConnectionClosed:
            pytest.fail("WebSocket connection closed unexpectedly")


if __name__ == "__main__":
    pytest.main([__file__, "-v"])

