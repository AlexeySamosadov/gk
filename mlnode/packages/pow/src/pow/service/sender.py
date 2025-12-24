import time
import requests
from requests.exceptions import RequestException
from typing import List
from multiprocessing import Process, Queue, Event

from pow.data import ProofBatch, ValidatedBatch, InValidation
from pow.compute.utils import Phase
from common.logger import create_logger

logger = create_logger(__name__)


def get_from_queue(queue: Queue, timeout: float = 0.1) -> List:
    items = []
    while True:
        try:
            item = queue.get(timeout=timeout)
            items.append(item)
        except:
            break
    return items


class Sender(Process):
    def __init__(
        self,
        url: str,
        generation_queue: Queue,
        validation_queue: Queue,
        phase,
        r_target: float,
        fraud_threshold: float,
        block_height: int = 0,
        node_id: int = 0,
    ):
        super().__init__()
        self.url = url
        self.phase = phase
        self.generation_queue = generation_queue
        self.validation_queue = validation_queue
        self.in_validation_queue = Queue()
        self.r_target = r_target
        self.fraud_threshold = fraud_threshold
        self.block_height = block_height
        self.node_id = node_id

        self.in_validation: List[InValidation] = []
        self.generated_not_sent: List[ProofBatch] = []
        self.validated_not_sent: List[ValidatedBatch] = []
        self.stop_event = Event()

    def _send_generated(self):
        if not self.generated_not_sent:
            return

        failed_batches = []
        for batch in self.generated_not_sent:
            try:
                send_url = "http://api:9100/v1/poc-batches/generated"
                logger.info(f"Sending generated batch to {send_url}")
                
                if isinstance(batch, dict):
                    nonces = batch.get("nonces", [])
                    dist = batch.get("dist") or batch.get("dists", [])
                    data = {"nonces": nonces, "dist": [float(d) for d in dist]}
                else:
                    data = {"nonces": batch.nonces, "dist": [float(d) for d in batch.dist]}
                
                if self.block_height > 0:
                    data["block_height"] = self.block_height
                if self.node_id >= 0:
                    data["node_id"] = self.node_id  # API maps this to NodeNum
                    
                response = requests.post(send_url, json=data, timeout=10)
                response.raise_for_status()
                logger.info("Successfully sent generated batch")
            except RequestException as e:
                failed_batches.append(batch)
                logger.error(f"Error sending generated batch: {e}")

        self.generated_not_sent = failed_batches

    def _send_validated(self):
        if not self.validated_not_sent:
            return

        failed_batches = []
        for batch in self.validated_not_sent:
            try:
                send_url = "http://api:9100/v1/poc-batches/validated"
                logger.info(f"Sending validated batch to {send_url}")
                if isinstance(batch, dict):
                    data = batch.copy()
                    if "dists" in data and "dist" not in data:
                        data["dist"] = data.pop("dists")
                else:
                    data = batch.__dict__.copy()
                if self.block_height > 0:
                    data["block_height"] = self.block_height
                response = requests.post(send_url, json=data, timeout=10)
                response.raise_for_status()
                logger.info("Successfully sent validated batch")
            except RequestException as e:
                failed_batches.append(batch)
                logger.error(f"Error sending validated batch: {e}")

        self.validated_not_sent = failed_batches

    def stop(self):
        self.stop_event.set()

    def run(self):
        logger.info(f"Sender started with block_height={self.block_height}, node_id={self.node_id}")
        while not self.stop_event.is_set():
            try:
                phase = self.phase.value
                generated = get_from_queue(self.generation_queue)
                self.generated_not_sent.extend(generated)
                validated = get_from_queue(self.validation_queue)
                self.validated_not_sent.extend(validated)
                
                if phase == Phase.GENERATE:
                    self._send_generated()
                elif phase == Phase.VALIDATE:
                    self._send_validated()
                    
                time.sleep(0.1)
            except Exception as e:
                logger.error(f"Sender error: {e}")
                time.sleep(1)
                
        logger.info("Sender stopped")
