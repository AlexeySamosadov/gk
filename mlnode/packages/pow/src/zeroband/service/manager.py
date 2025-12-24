"""Mock train manager."""
from common.manager import IManager
from common.logger import create_logger

logger = create_logger(__name__)


class TrainManager(IManager):
    def __init__(self):
        super().__init__()
        self._running = False

    def _start(self):
        self._running = True

    def _stop(self):
        self._running = False

    def is_running(self) -> bool:
        return self._running

    def _is_healthy(self) -> bool:
        return True
