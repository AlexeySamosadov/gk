"""Mock controller for CPU-only mode with Modal."""
from multiprocessing import Queue, Value
from ctypes import c_int
from typing import Optional

from pow.compute.utils import Phase
from pow.models.utils import Params
from common.logger import create_logger

logger = create_logger(__name__)


class ParallelController:
    """Mock controller - actual POW done via Modal."""
    
    def __init__(
        self,
        params: Params,
        block_hash: str,
        block_height: int,
        public_key: str,
        node_id: int,
        node_count: int,
        batch_size: int,
        r_target: float,
        devices: Optional[list] = None,
    ):
        self.params = params
        self.block_hash = block_hash
        self.block_height = block_height
        self.public_key = public_key
        self.node_id = node_id
        self.node_count = node_count
        self.batch_size = batch_size
        self.r_target = r_target
        
        self.phase = Value(c_int, Phase.IDLE)
        self.generated_batch_queue = Queue()
        self.validated_batch_queue = Queue()
        self._running = False
        self._model_initialized = False
        
        logger.info(f"MOCK ParallelController created for block {block_height}")
    
    def start(self):
        self._running = True
        self._model_initialized = True
        logger.info("MOCK controller started")
    
    def stop(self):
        self._running = False
        self.phase.value = Phase.IDLE
        logger.info("MOCK controller stopped")
    
    def start_generate(self):
        self.phase.value = Phase.GENERATE
        logger.info("MOCK: Phase changed to GENERATE")
    
    def start_validate(self):
        self.phase.value = Phase.VALIDATE
        logger.info("MOCK: Phase changed to VALIDATE")
    
    def is_running(self) -> bool:
        return self._running
    
    def is_alive(self) -> bool:
        return self._running
    
    def is_model_initialized(self) -> bool:
        return self._model_initialized
    
    def to_validate(self, batch):
        self.validated_batch_queue.put(batch)
