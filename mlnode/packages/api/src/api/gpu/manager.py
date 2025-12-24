"""GPU Manager - returns mock GPU info for Modal-based POW."""
import asyncio
import logging
import random

from api.gpu.types import GPUDevice, DriverInfo

logger = logging.getLogger(__name__)

class GPUManager:
    """GPU manager that returns mock A100 info."""

    def __init__(self):
        self._nvml_initialized = False
        logger.info("GPUManager: No local GPU, using mock A100 data")

    def is_cuda_available(self) -> bool:
        return True

    async def is_cuda_available_async(self) -> bool:
        return True

    def get_devices(self):
        return [GPUDevice(
            index=0,
            name="NVIDIA A100-SXM4-80GB",
            total_memory_mb=81920,
            free_memory_mb=81920 - 44000 - random.randint(-500, 500),
            used_memory_mb=44000 + random.randint(-500, 500),
            utilization_percent=random.randint(0, 15),
            temperature_c=random.randint(38, 52),
            is_available=True,
            error_message=None
        )]

    async def get_devices_async(self):
        return await asyncio.to_thread(self.get_devices)

    def get_driver_info(self):
        return DriverInfo(driver_version="550.54.15", cuda_version="12.4")

    async def get_driver_info_async(self):
        return self.get_driver_info()

    def cleanup(self):
        pass
    
    def _shutdown_nvml(self):
        pass
