"""POW utilities."""
from enum import IntEnum


class Phase(IntEnum):
    IDLE = 0
    GENERATE = 1
    VALIDATE = 2


class NonceIterator:
    def __init__(self, node_id: int, node_count: int, start: int = 0):
        self.node_id = node_id
        self.node_count = node_count
        self.current = start + node_id
    
    def __iter__(self):
        return self
    
    def __next__(self):
        result = self.current
        self.current += self.node_count
        return result
