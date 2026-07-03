"""Sample Python module for testing AST parsing."""
import os
from typing import List, Optional
from collections import OrderedDict


# Configuration constant
MAX_RETRIES: int = 3

DEFAULT_TIMEOUT = 30


class BaseHandler:
    """Base class for all handlers."""

    def __init__(self, name: str):
        self.name = name

    def handle(self, request: dict) -> dict:
        """Handle a request."""
        raise NotImplementedError


class HTTPHandler(BaseHandler):
    """HTTP request handler."""

    def __init__(self, name: str, timeout: int = DEFAULT_TIMEOUT):
        super().__init__(name)
        self.timeout = timeout

    def handle(self, request: dict) -> dict:
        """Handle an HTTP request."""
        return self._send_request(request)

    def _send_request(self, request: dict) -> dict:
        """Send the actual HTTP request."""
        path = os.path.join("/api", request.get("path", ""))
        return {"status": 200, "path": path}


def create_handler(name: str, handler_type: str = "http") -> BaseHandler:
    """Factory function to create handlers."""
    if handler_type == "http":
        return HTTPHandler(name)
    return BaseHandler(name)


async def fetch_data(url: str, retries: int = MAX_RETRIES) -> Optional[dict]:
    """Fetch data from a URL with retries."""
    for _ in range(retries):
        result = create_handler("fetcher")
        if result:
            return result.handle({"path": url})
    return None
