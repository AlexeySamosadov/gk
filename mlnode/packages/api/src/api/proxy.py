import asyncio
import os
import json
import threading
from typing import Dict, List, Optional, Set

import uvicorn
import httpx
from fastapi import FastAPI, Request, Response
from fastapi.responses import StreamingResponse
from starlette.middleware.base import BaseHTTPMiddleware

from common.logger import create_logger

logger = create_logger(__name__)

VLLM_HOST = "127.0.0.1"
PROXY_PROVIDER = os.getenv("PROXY_PROVIDER", "novita").lower()
PROXY_API_KEY = os.getenv("PROXY_API_KEY") or os.getenv("NOVITA_API_KEY")
MAX_LOGPROBS_RETRIES = int(os.getenv("MAX_LOGPROBS_RETRIES", "20"))

PROXY_MODEL_MAPPING = {}
mapping_env = os.getenv("PROXY_MODEL_MAPPING")
if mapping_env:
    for m in mapping_env.split(","):
        k, v = m.split("=")
        PROXY_MODEL_MAPPING[k.strip()] = v.strip()

PROXY_URLS = {"novita": "https://api.novita.ai/openai"}
LIMITS = httpx.Limits(max_connections=20_000, max_keepalive_connections=5_000)

vllm_backend_ports: List[int] = []
vllm_healthy: Dict[int, bool] = {}
vllm_counts: Dict[int, int] = {}
vllm_pick_lock = asyncio.Lock()
vllm_client: Optional[httpx.AsyncClient] = None
shutdown_event = asyncio.Event()
compatibility_server_task = None
compatibility_server = None
health_check_task = None

_tokenizer = None
_tokenizer_lock = threading.Lock()

def get_tokenizer():
    global _tokenizer
    if _tokenizer is not None:
        return _tokenizer
    with _tokenizer_lock:
        if _tokenizer is None:
            try:
                from transformers import AutoTokenizer
                logger.info("Loading Qwen3 tokenizer...")
                _tokenizer = AutoTokenizer.from_pretrained("Qwen/Qwen3-32B", trust_remote_code=True)
                logger.info("Tokenizer loaded")
            except Exception as e:
                logger.error(f"Failed to load tokenizer: {e}")
        return _tokenizer

def convert_logprobs_to_gonka_format(data: dict) -> dict:
    """Convert Novita logprobs to Gonka format (token IDs + masked top_logprobs)."""
    tokenizer = get_tokenizer()
    if not tokenizer:
        return data
    try:
        for choice in data.get("choices", []):
            logprobs = choice.get("logprobs")
            if not logprobs or "content" not in logprobs:
                continue
            
            for item in logprobs["content"]:
                # Convert main token to ID
                if "token" in item:
                    ids = tokenizer.encode(item["token"], add_special_tokens=False)
                    if ids:
                        token_id = str(ids[0])
                        item["token"] = token_id
                        item["bytes"] = [ord(c) for c in token_id]
                        item["logprob"] = 0  # Normalize to 0
                
                # Mask top_logprobs like Gonka does
                if "top_logprobs" in item and item["top_logprobs"]:
                    first_top = item["top_logprobs"][0]
                    ids = tokenizer.encode(first_top.get("token", ""), add_special_tokens=False)
                    first_id = str(ids[0]) if ids else "0"
                    
                    # Create masked top_logprobs: first real, rest placeholder
                    masked_tops = [
                        {"token": first_id, "logprob": 0, "bytes": [ord(c) for c in first_id]}
                    ]
                    # Add placeholder tokens like Gonka does
                    for i, placeholder in enumerate(["2", "0", "3", "1"]):
                        masked_tops.append({
                            "token": placeholder,
                            "logprob": -9999,
                            "bytes": [ord(c) for c in placeholder]
                        })
                    item["top_logprobs"] = masked_tops
                    
    except Exception as e:
        logger.error(f"Error converting logprobs: {e}")
    return data


class ProxyMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        if request.url.path.startswith("/v1"):
            return await _proxy_request_to_backend(request, request.url.path)
        return await call_next(request)


async def _proxy_request_to_backend(request: Request, backend_path: str) -> Response:
    if not PROXY_API_KEY:
        return Response(status_code=503, content=b"No API key configured")
    
    if shutdown_event.is_set():
        return Response(status_code=503, content=b"Shutting down")
    
    if not backend_path.startswith("/"):
        backend_path = "/" + backend_path
    if not backend_path.startswith("/v1"):
        backend_path = "/v1" + backend_path.lstrip("/")
    
    url = f"{PROXY_URLS.get(PROXY_PROVIDER, PROXY_URLS['novita'])}{backend_path}"
    
    headers = {k: v for k, v in request.headers.items() if k.lower() not in ["host", "content-length"]}
    headers["Authorization"] = f"Bearer {PROXY_API_KEY}"
    
    if vllm_client is None:
        return Response(status_code=503, content=b"Client not initialized")
    
    body_bytes = b""
    async for chunk in request.stream():
        body_bytes += chunk
    
    is_json = "application/json" in headers.get("content-type", "")
    is_streaming = False
    needs_logprobs = False
    
    if is_json and request.method == "POST":
        try:
            data = json.loads(body_bytes)
            model = data.get("model")
            if model and model in PROXY_MODEL_MAPPING:
                data["model"] = PROXY_MODEL_MAPPING[model]
            is_streaming = data.get("stream", False)
            needs_logprobs = data.get("logprobs", False)
            
            if needs_logprobs:
                data["temperature"] = 0
                data["top_p"] = 1
            
            body_bytes = json.dumps(data).encode()
        except:
            pass
    
    try:
        if is_streaming:
            context_manager = vllm_client.stream(
                request.method, url, headers=headers, content=body_bytes,
                timeout=httpx.Timeout(None, read=900),
            )
            upstream = await context_manager.__aenter__()
            
            resp_headers = {k: v for k, v in upstream.headers.items()
                          if k.lower() not in {"content-length", "transfer-encoding", "connection"}}

            async def stream_with_tracking():
                try:
                    async for chunk in upstream.aiter_raw():
                        yield chunk
                finally:
                    await context_manager.__aexit__(None, None, None)

            return StreamingResponse(stream_with_tracking(), status_code=upstream.status_code, headers=resp_headers)
        
        else:
            max_retries = MAX_LOGPROBS_RETRIES if needs_logprobs else 1
            
            for attempt in range(max_retries):
                response = await vllm_client.post(
                    url, headers=headers, content=body_bytes,
                    timeout=httpx.Timeout(None, read=900),
                )
                
                resp_headers = {k: v for k, v in response.headers.items()
                              if k.lower() not in {"content-length", "transfer-encoding", "connection", "content-encoding"}}
                
                try:
                    data = response.json()
                    
                    has_logprobs = False
                    if data.get("choices"):
                        lp = data["choices"][0].get("logprobs")
                        has_logprobs = lp is not None and lp.get("content") is not None
                    
                    if needs_logprobs and not has_logprobs and attempt < max_retries - 1:
                        logger.info(f"No logprobs, retry {attempt + 1}/{max_retries}")
                        await asyncio.sleep(0.5)
                        continue
                    
                    if has_logprobs:
                        data = convert_logprobs_to_gonka_format(data)
                    
                    return Response(
                        content=json.dumps(data),
                        status_code=response.status_code,
                        headers=resp_headers,
                        media_type="application/json"
                    )
                except Exception as e:
                    logger.error(f"Error processing response: {e}")
                    return Response(content=response.content, status_code=response.status_code, headers=resp_headers)
            
            return Response(content=response.content, status_code=response.status_code, headers=resp_headers)
                    
    except Exception as exc:
        logger.exception(f"Proxy error: {exc}")
        return Response(status_code=502, content=b"Provider connection failed")


async def _health_check_vllm(interval: float = 2.0):
    logger.info("Health check started")
    while True:
        if PROXY_API_KEY:
            for p in vllm_backend_ports:
                vllm_healthy[p] = True
            if not compatibility_server_task:
                await start_backward_compatibility()
        await asyncio.sleep(interval)

def setup_vllm_proxy(backend_ports: List[int]):
    global vllm_backend_ports, vllm_counts
    vllm_backend_ports = backend_ports
    vllm_counts = {p: 0 for p in backend_ports}
    vllm_healthy.update({p: False for p in backend_ports})

async def start_vllm_proxy():
    global vllm_client, health_check_task
    vllm_client = httpx.AsyncClient(http2=True, limits=LIMITS)
    health_check_task = asyncio.create_task(_health_check_vllm())
    logger.info("Proxy started")
    asyncio.get_event_loop().run_in_executor(None, get_tokenizer)

async def stop_vllm_proxy():
    global vllm_client, health_check_task
    if health_check_task:
        health_check_task.cancel()
        health_check_task = None
    await stop_backward_compatibility()
    if vllm_client:
        await vllm_client.aclose()
        vllm_client = None

async def _run_compatibility_server():
    global compatibility_server
    app = FastAPI()
    @app.api_route("/{path:path}", methods=["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"])
    async def proxy_all(request: Request, path: str):
        return await _proxy_request_to_backend(request, path)
    
    config = uvicorn.Config(app, host="0.0.0.0", port=5000, log_level="info")
    server = uvicorn.Server(config)
    compatibility_server = server
    await server.serve()

async def start_backward_compatibility():
    global compatibility_server_task
    if not compatibility_server_task:
        compatibility_server_task = asyncio.create_task(_run_compatibility_server())
        await asyncio.sleep(0.1)

async def stop_backward_compatibility():
    global compatibility_server_task, compatibility_server
    if compatibility_server_task:
        if compatibility_server:
            compatibility_server.should_exit = True
        compatibility_server_task.cancel()
        compatibility_server_task = None
        compatibility_server = None
