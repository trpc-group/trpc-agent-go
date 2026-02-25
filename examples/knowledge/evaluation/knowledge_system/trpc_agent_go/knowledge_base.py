"""
tRPC-Agent-Go Knowledge Base Client.

This module provides a Python client that calls the Go HTTP service
implementing the KnowledgeBase interface.
"""

import atexit
import os
import subprocess
import sys
import time
from typing import List, Optional, Tuple

import requests

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from knowledge_system.base import KnowledgeBase, SearchResult

DEFAULT_PORT = 8765
GO_SERVICE_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "trpc_knowledge")
GO_SERVICE_PATH = os.path.join(GO_SERVICE_DIR, "trpc_knowledge")


class TRPCAgentGoKnowledgeBase(KnowledgeBase):
    """Knowledge base client that calls tRPC-Agent-Go HTTP service."""

    _process: Optional[subprocess.Popen] = None

    def __init__(
        self,
        port: int = DEFAULT_PORT,
        timeout: int = 120,
        auto_start: bool = True,
        vectorstore: str = "pgvector",
        search_mode: int = 0,
    ):
        """
        Initialize the knowledge base client.

        Args:
            port: Port for the Go HTTP service.
            timeout: Request timeout in seconds.
            auto_start: If True, automatically build and start the Go service.
            vectorstore: Vector store type: inmemory|pgvector.
            search_mode: Search mode for the Go service: 0=hybrid (default), 1=vector, 2=keyword, 3=filter.
        """
        self.port = port
        self.service_url = f"http://localhost:{port}"
        self.timeout = timeout
        self.vectorstore = vectorstore
        self.search_mode = search_mode
        self.last_trace: Optional[dict] = None

        if auto_start:
            self._ensure_service_running()

    def _ensure_service_running(self):
        """Build and start the Go service if not already running."""
        if self._check_health():
            print(f"Go service already running at {self.service_url}")
            return

        self._build_service()
        self._start_service()

    def _build_service(self):
        """Build the Go service binary."""
        print(f"Building Go service in {GO_SERVICE_DIR}...")
        result = subprocess.run(
            ["go", "build", "-o", "trpc_knowledge", "."],
            cwd=GO_SERVICE_DIR,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            raise RuntimeError(f"Failed to build Go service: {result.stderr}")
        print("Go service built successfully.")

    def _start_service(self):
        """Start the Go service process."""
        if not os.path.exists(GO_SERVICE_PATH):
            raise RuntimeError(f"Go binary not found at {GO_SERVICE_PATH}")

        print(f"Starting Go service on port {self.port} (search_mode={self.search_mode})...")
        # nosec B603: GO_SERVICE_PATH is a static local binary, args are internal config not user input.
        TRPCAgentGoKnowledgeBase._process = subprocess.Popen(  # nosec B603
            [GO_SERVICE_PATH, f"--port={self.port}", f"--vectorstore={self.vectorstore}", f"--search-mode={self.search_mode}"],
            cwd=GO_SERVICE_DIR,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )

        atexit.register(self._stop_service)

        # Wait for service to be ready
        max_retries = 30
        for i in range(max_retries):
            # Check if process has exited
            if TRPCAgentGoKnowledgeBase._process.poll() is not None:
                stdout, _ = TRPCAgentGoKnowledgeBase._process.communicate()
                raise RuntimeError(f"Go service exited unexpectedly:\n{stdout}")

            if self._check_health():
                print(f"Go service started successfully (PID: {TRPCAgentGoKnowledgeBase._process.pid})")
                return
            time.sleep(0.5)

        # If we get here, service failed to start - capture output
        output_lines = []
        if TRPCAgentGoKnowledgeBase._process.stdout:
            import select
            while select.select([TRPCAgentGoKnowledgeBase._process.stdout], [], [], 0)[0]:
                line = TRPCAgentGoKnowledgeBase._process.stdout.readline()
                if not line:
                    break
                output_lines.append(line)

        self._stop_service()
        raise RuntimeError(f"Go service failed to start within timeout. Output:\n{''.join(output_lines)}")

    @classmethod
    def _stop_service(cls):
        """Stop the Go service process."""
        if cls._process is not None:
            print("Stopping Go service...")
            cls._process.terminate()
            try:
                cls._process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                cls._process.kill()
            cls._process = None

    def _check_health(self) -> bool:
        """Check if the service is healthy."""
        try:
            response = requests.get(
                f"{self.service_url}/health",
                timeout=5,
            )
            return response.status_code == 200
        except requests.RequestException:
            return False

    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """
        Load documents into the knowledge base.

        The Go service will use knowledge.Load to read and process the files.

        Args:
            file_paths: List of file paths to load.
            metadatas: Optional list of metadata dicts (currently ignored, metadata is extracted from files).
        """
        payload = {
            "file_paths": file_paths,
        }

        response = requests.post(
            f"{self.service_url}/load",
            json=payload,
            timeout=600,  # 10 minutes for large document sets
        )
        response.raise_for_status()

        result = response.json()
        if not result.get("success"):
            raise RuntimeError(f"Load failed: {result.get('message')}")

    def search(self, query: str, k: int = 4) -> List[SearchResult]:
        """
        Search for relevant documents.

        Args:
            query: Search query string.
            k: Number of documents to return.

        Returns:
            List of SearchResult objects.
        """
        response = requests.post(
            f"{self.service_url}/search",
            json={"query": query, "k": k},
            timeout=self.timeout,
        )
        response.raise_for_status()

        result = response.json()
        raw_documents = result.get("documents") or []
        return [
            SearchResult(
                content=r["text"],
                score=r.get("score", 0.0),
                metadata=r.get("metadata", {}),
            )
            for r in raw_documents
        ]

    def answer(self, question: str, k: int = 4) -> Tuple[str, List[SearchResult]]:
        """
        Answer a question using the knowledge base.

        Each call creates a fresh session on the Go side (no conversation history).

        Args:
            question: The question to answer.
            k: Number of documents to retrieve for context.

        Returns:
            Tuple of (answer_text, search_results).
        """
        response = requests.post(
            f"{self.service_url}/answer",
            json={"question": question, "k": k},
            timeout=self.timeout,
        )
        response.raise_for_status()

        result = response.json()
        answer = result.get("answer", "")
        trace = result.get("trace")  # Extract trace from response
        self.last_trace = trace if isinstance(trace, dict) else None

        # Guard against JSON null: dict.get("documents", []) returns None
        # when the key exists but its value is null, not the default [].
        raw_documents = result.get("documents") or []
        search_results = [
            SearchResult(
                content=r["text"],
                score=r.get("score", 0.0),
                metadata=r.get("metadata", {}),
                trace=trace,  # Attach trace to first result for easy access
            )
            for r in raw_documents
        ]

        return answer, search_results
