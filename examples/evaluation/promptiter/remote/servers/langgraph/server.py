#!/usr/bin/env python3
"""LangGraph-backed tRPC-Agent service for PromptIter remote inference."""

from __future__ import annotations

import datetime as dt
import json
import operator
import os
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Annotated
from urllib.parse import unquote, urlparse

from langchain.chat_models import init_chat_model
from langchain.messages import AnyMessage, HumanMessage, SystemMessage
from langgraph.graph import END, START, StateGraph
from typing_extensions import TypedDict


APP_NAME = os.getenv("TRPC_AGENT_APP_NAME", "promptiter-nba-commentary-candidate")
AGENT_NAME = os.getenv("TRPC_AGENT_AGENT_NAME", "candidate")
MODEL_NAME = os.getenv("LANGGRAPH_MODEL", "openai:gpt-5.2")
HOST = os.getenv("HOST", "127.0.0.1")
PORT = int(os.getenv("PORT", "8081"))
BASE_PATH = os.getenv("TRPC_AGENT_BASE_PATH", "/trpc-agent/v1/apps")
DEFAULT_INSTRUCTION = os.getenv("CANDIDATE_INSTRUCTION", "生成一篇中文体育战报")
SURFACE_ID = f"{AGENT_NAME}#instruction"
STRUCTURE_ID = f"{APP_NAME}:langgraph:v1"


class MessagesState(TypedDict):
    messages: Annotated[list[AnyMessage], operator.add]


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


def build_graph(instruction: str):
    model = init_chat_model(MODEL_NAME, temperature=0)

    def llm_call(state: MessagesState):
        return {"messages": [model.invoke([SystemMessage(content=instruction)] + state["messages"])]}

    graph = StateGraph(MessagesState)
    graph.add_node("llm_call", llm_call)
    graph.add_edge(START, "llm_call")
    graph.add_edge("llm_call", END)
    return graph.compile()


def message_content(message: AnyMessage) -> str:
    content = getattr(message, "content", "")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(part.get("text", "") for part in content if isinstance(part, dict))
    return str(content)


def run_graph(request: dict) -> str:
    instruction = instruction_from_profile(request.get("profile"))
    graph = build_graph(instruction)
    user_message = HumanMessage(content=input_text(request.get("input") or {}))
    result = graph.invoke({"messages": [user_message]})
    return message_content(result["messages"][-1]).strip()


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
        output = run_graph(request)
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
    print(f"LangGraph tRPC-Agent service listening on http://{HOST}:{PORT}{BASE_PATH}/{APP_NAME}")
    server.serve_forever()


if __name__ == "__main__":
    main()
