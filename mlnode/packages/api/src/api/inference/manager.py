"""Mock inference manager for Novita proxy mode."""
from typing import Optional, List
from pydantic import BaseModel
import asyncio

from common.logger import create_logger
from common.manager import IManager
import api.proxy as proxy_module

logger = create_logger(__name__)


class InferenceInitRequest(BaseModel):
    model: str
    dtype: str
    additional_args: List[str] = []


class StartupStatus(BaseModel):
    status: str
    is_starting: bool
    is_running: bool
    elapsed_seconds: Optional[float] = None
    error: Optional[str] = None


class InferenceManager(IManager):
    """Mock manager - inference handled by Novita proxy."""
    
    def __init__(self, runner_class=None):
        super().__init__()
        self._running = False
        self._model = None
        logger.info("InferenceManager: Using Novita proxy mode")

    def init_vllm(self, init_request: InferenceInitRequest):
        self._model = init_request.model
        logger.info(f"Mock init_vllm: {init_request.model}")

    def _start(self):
        self._running = True
        logger.info("Mock inference started (Novita proxy)")

    def _stop(self):
        self._running = False
        logger.info("Mock inference stopped")

    async def _async_stop(self):
        self._stop()

    def is_running(self) -> bool:
        return self._running

    def is_starting(self) -> bool:
        return False

    def _is_healthy(self) -> bool:
        return True

    def get_startup_status(self) -> StartupStatus:
        return StartupStatus(
            status="running" if self._running else "stopped",
            is_starting=False,
            is_running=self._running,
        )

    async def start_async(self, init_request: InferenceInitRequest):
        self.init_vllm(init_request)
        self._start()
        proxy_module.start_vllm_proxy()
        return {"status": "started", "mode": "novita_proxy"}
