#!/usr/bin/env python3
"""ADK-backed tRPC-Agent service for PromptIter remote inference."""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import unquote, urlparse

from agent import APP_NAME
from protocol import run_response, structure_payload


HOST = os.getenv("HOST", "127.0.0.1")
PORT = int(os.getenv("PORT", "8081"))
BASE_PATH = os.getenv("TRPC_AGENT_BASE_PATH", "/trpc-agent/v1/apps")


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.route("structure"):
            self.write_json(200, structure_payload())
            return
        self.write_json(404, {"error": "not found"})

    def do_POST(self) -> None:
        if not self.route("runs"):
            self.write_json(404, {"error": "not found"})
            return
        status, payload = run_response(self.read_json())
        self.write_json(status, payload)

    def route(self, resource: str) -> bool:
        path = urlparse(self.path).path
        prefix = f"{BASE_PATH}/{APP_NAME}/"
        if not path.startswith(prefix):
            return False
        return unquote(path[len(prefix) :]) == resource

    def read_json(self) -> dict:
        length = int(self.headers.get("Content-Length") or "0")
        if length == 0:
            return {}
        return json.loads(self.rfile.read(length).decode("utf-8"))

    def write_json(self, status: int, payload: dict) -> None:
        raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main() -> None:
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"ADK tRPC-Agent service listening on http://{HOST}:{PORT}{BASE_PATH}/{APP_NAME}")
    server.serve_forever()


if __name__ == "__main__":
    main()
