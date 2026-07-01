#!/usr/bin/env python3
"""ADK-backed tRPC-Agent service for PromptIter remote inference."""

from __future__ import annotations

import asyncio
import datetime as dt
import inspect
import json
import os
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import unquote, urlparse

from google.adk.agents.llm_agent import LlmAgent
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService
from google.genai import types


APP_NAME = os.getenv("TRPC_AGENT_APP_NAME", "promptiter-nba-commentary-candidate")
AGENT_NAME = os.getenv("TRPC_AGENT_AGENT_NAME", "candidate")
MODEL_NAME = os.getenv("ADK_MODEL", "gemini-2.0-flash")
HOST = os.getenv("HOST", "127.0.0.1")
PORT = int(os.getenv("PORT", "8081"))
BASE_PATH = os.getenv("TRPC_AGENT_BASE_PATH", "/trpc-agent/v1/apps")
DEFAULT_INSTRUCTION = os.getenv("CANDIDATE_INSTRUCTION", "生成一篇中文体育战报")
SURFACE_ID = f"{AGENT_NAME}#instruction"
STRUCTURE_ID = f"{APP_NAME}:adk-python:v1"
SESSION_SERVICE = InMemorySessionService()
SESSION_LOCK = threading.Lock()
KNOWN_SESSIONS: set[tuple[str, str]] = set()


def structure_payload() -> dict:
    return {
        "structure": {
            "StructureID": STRUCTURE_ID,
            "EntryNodeID": AGENT_NAME,
            "Nodes": [{"NodeID": AGENT_NAME, "Kind": "llm", "Name": AGENT_NAME}],
            "Edges": [],
            "Surfaces": [
                {
                    "SurfaceID": SURFACE_ID,
                    "NodeID": AGENT_NAME,
                    "Type": "instruction",
                    "Value": {"Text": DEFAULT_INSTRUCTION},
                }
            ],
        }
    }


def maybe_await(value):
    if inspect.isawaitable(value):
        return asyncio.run(value)
    return value


def ensure_session(user_id: str, session_id: str) -> None:
    key = (user_id, session_id)
    with SESSION_LOCK:
        if key in KNOWN_SESSIONS:
            return
        try:
            maybe_await(SESSION_SERVICE.create_session(app_name=APP_NAME, user_id=user_id, session_id=session_id))
        except Exception as exc:
            if "already" not in str(exc).lower() and "exist" not in str(exc).lower():
                raise
        KNOWN_SESSIONS.add(key)


def instruction_from_profile(profile: dict | None) -> str:
    if not profile:
        return DEFAULT_INSTRUCTION
    for override in profile.get("overrides") or []:
        if override.get("surfaceID") != SURFACE_ID:
            continue
        value = override.get("value") or override.get("Value") or {}
        text = value.get("Text") or value.get("text")
        if isinstance(text, str) and text.strip():
            return text
    return DEFAULT_INSTRUCTION


def input_text(message: dict) -> str:
    if isinstance(message.get("content"), str):
        return message["content"]
    parts = message.get("content_parts") or message.get("ContentParts") or []
    texts = []
    for part in parts:
        text = part.get("text") or part.get("Text")
        if text:
            texts.append(text)
    return "\n".join(texts)


def run_agent(request: dict) -> str:
    session = request.get("session") or {}
    user_id = session.get("userId") or "promptiter"
    session_id = session.get("sessionId") or str(uuid.uuid4())
    instruction = instruction_from_profile(request.get("profile"))
    ensure_session(user_id, session_id)
    agent = LlmAgent(name=AGENT_NAME, model=MODEL_NAME, instruction=instruction)
    runner = Runner(agent=agent, app_name=APP_NAME, session_service=SESSION_SERVICE)
    content = types.Content(role="user", parts=[types.Part(text=input_text(request.get("input") or {}))])
    final_text = ""
    for event in runner.run(user_id=user_id, session_id=session_id, new_message=content):
        if event.is_final_response() and event.content and event.content.parts:
            final_text = (event.content.parts[0].text or "").strip()
    return final_text


def trace_payload(request: dict, output: str, status: str, error: str = "") -> dict:
    session = request.get("session") or {}
    run_options = request.get("runOptions") or {}
    invocation_id = run_options.get("requestId") or str(uuid.uuid4())
    session_id = session.get("sessionId") or ""
    now = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    return {
        "RootAgentName": AGENT_NAME,
        "RootInvocationID": invocation_id,
        "SessionID": session_id,
        "StartedAt": now,
        "EndedAt": now,
        "Status": status,
        "Steps": [
            {
                "StepID": f"{invocation_id}:candidate",
                "InvocationID": invocation_id,
                "AgentName": AGENT_NAME,
                "NodeID": AGENT_NAME,
                "StartedAt": now,
                "EndedAt": now,
                "AppliedSurfaceIDs": [SURFACE_ID],
                "Input": {"Text": input_text(request.get("input") or {})},
                "Output": {"Text": output},
                "Error": error,
            }
        ],
    }


def run_response(request: dict) -> tuple[int, dict]:
    input_message = request.get("input") or {"role": "user", "content": ""}
    try:
        output = run_agent(request)
        messages = [input_message, {"role": "assistant", "content": output}]
        return 200, {"status": "completed", "messages": messages, "executionTrace": trace_payload(request, output, "completed")}
    except Exception as exc:
        message = str(exc)
        return 200, {
            "status": "failed",
            "messages": [input_message],
            "executionTrace": trace_payload(request, "", "failed", message),
            "errorType": "run_error",
            "errorMessage": message,
        }


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
