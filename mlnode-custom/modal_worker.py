"""Modal worker for POW generation using serverless GPU."""
import time
import requests
from multiprocessing import Process, Value
from typing import Optional
from ctypes import c_int

from common.logger import create_logger
from pow.compute.utils import Phase

logger = create_logger(__name__)

MODAL_GENERATE_URL = "https://asamosadov--pow-worker-powworker-pow-endpoint.modal.run"
MODAL_WARMUP_URL = "https://asamosadov--pow-worker-powworker-warmup.modal.run"


def warmup_modal():
    """Warmup Modal container before PoC starts."""
    logger.info("Warming up Modal container...")
    try:
        response = requests.post(MODAL_WARMUP_URL, timeout=300)
        logger.info(f"Modal warmup response: {response.status_code}, time: {response.elapsed.total_seconds():.1f}s")
        return response.status_code == 200
    except Exception as e:
        logger.error(f"Modal warmup error: {e}")
        return False


class ModalWorker(Process):
    """Worker that calls Modal serverless GPU for POW generation."""
    
    def __init__(
        self,
        phase: Value,
        generated_batch_queue,
        block_hash: str,
        block_height: int,
        public_key: str,
        r_target: float,
        node_id: int,
        batch_size: int = 100,
        batches_per_call: int = 20,
    ):
        super().__init__()
        self.phase = phase
        self.generated_batch_queue = generated_batch_queue
        self.block_hash = block_hash
        self.block_height = block_height
        self.public_key = public_key
        self.r_target = r_target
        self.node_id = node_id
        self.batch_size = batch_size
        self.batches_per_call = batches_per_call
        self._stop_flag = Value(c_int, 0)
        
    def stop(self):
        self._stop_flag.value = 1
        
    def run(self):
        logger.info(f"ModalWorker started for block {self.block_height}")
        total_batches = 0
        first_request = True
        
        while self._stop_flag.value == 0 and self.phase.value == Phase.GENERATE:
            try:
                start_time = time.time()
                response = requests.post(
                    MODAL_GENERATE_URL,
                    json={
                        "block_hash": self.block_hash,
                        "block_height": self.block_height,
                        "public_key": self.public_key,
                        "r_target": self.r_target,
                        "node_id": self.node_id,
                        "batch_size": self.batch_size,
                        "num_batches": self.batches_per_call,
                    },
                    timeout=600,
                )
                elapsed = time.time() - start_time
                response.raise_for_status()
                result = response.json()
                
                batches = result.get("batches", [])
                for batch in batches:
                    if self._stop_flag.value == 1 or self.phase.value != Phase.GENERATE:
                        break
                    self.generated_batch_queue.put(batch)
                    total_batches += 1
                
                logger.info(f"Modal generated {len(batches)} batches in {elapsed:.1f}s, total: {total_batches}")
                first_request = False
                
            except Exception as e:
                logger.error(f"Modal generation error: {e}")
                if first_request:
                    logger.info("First request failed, trying warmup...")
                    warmup_modal()
                time.sleep(2)
                
        logger.info(f"ModalWorker stopped, total batches: {total_batches}")
