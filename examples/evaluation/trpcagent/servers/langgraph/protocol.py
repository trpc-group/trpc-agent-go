"""tRPC-Agent HTTP protocol helpers for the LangGraph example server."""

from __future__ import annotations

import datetime as dt
import uuid

from agent import APP_NAME, INSTRUCTION, MODEL_NAME, STRUCTURE_ID, run_agent

SURFACE_ID = f"{APP_NAME}#instruction"
MODEL_SURFACE_ID = f"{APP_NAME}#model"


def structure_payload() -> dict:
    return {
        "structure": {
            "StructureID": STRUCTURE_ID,
            "EntryNodeID": APP_NAME,
            "Nodes": [{"NodeID": APP_NAME, "Kind": "agent", "Name": APP_NAME}],
            "Edges": [],
            "Surfaces": [
                {"SurfaceID": SURFACE_ID, "NodeID": APP_NAME, "Type": "instruction", "Value": {"Text": INSTRUCTION}},
                {"SurfaceID": MODEL_SURFACE_ID, "NodeID": APP_NAME, "Type": "model", "Value": {"Model": {"Name": MODEL_NAME}}},
            ],
        }
    }


def run_response(request: dict) -> tuple[int, dict]:
    request_id = request_id_from(request)
    invocation_id = request_id
    started_at = now()
    try:
        output, usage = run_agent(request)
    except Exception as exc:
        message = str(exc)
        events = [error_event(request_id, invocation_id, message), completion_event(request_id, invocation_id, message)]
        response = {"status": "failed", "events": events, "errorMessage": message}
        if trace_enabled(request):
            response["executionTrace"] = trace_payload(request, invocation_id, "", "failed", message, started_at=started_at)
        return 200, response
    events = [final_event(request_id, invocation_id, output), completion_event(request_id, invocation_id)]
    response = {"status": "completed", "events": events}
    if trace_enabled(request):
        response["executionTrace"] = trace_payload(request, invocation_id, output, "completed", usage=usage, started_at=started_at)
    return 200, response


def request_id_from(request: dict) -> str:
    run_options = request.get("runOptions") or {}
    return run_options.get("requestID") or run_options.get("requestId") or str(uuid.uuid4())


def trace_enabled(request: dict) -> bool:
    run_options = request.get("runOptions") or {}
    return bool(run_options.get("executionTraceEnabled") or run_options.get("ExecutionTraceEnabled"))


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def trace_payload(
    request: dict,
    invocation_id: str,
    output: str,
    status: str,
    error: str = "",
    usage: dict | None = None,
    started_at: str = "",
) -> dict:
    ended_at = now()
    started_at = started_at or ended_at
    user_input = ((request.get("input") or {}).get("content") or "").strip()
    session_id = (request.get("session") or {}).get("sessionId") or ""
    step = {
        "StepID": f"{invocation_id}:root",
        "InvocationID": invocation_id,
        "AgentName": APP_NAME,
        "NodeID": APP_NAME,
        "NodeType": "agent",
        "StartedAt": started_at,
        "EndedAt": ended_at,
        "AppliedSurfaceIDs": [SURFACE_ID, MODEL_SURFACE_ID],
        "Input": {"Text": user_input},
        "Output": {"Text": output},
        "Error": error,
    }
    trace = {
        "RootAgentName": APP_NAME,
        "RootInvocationID": invocation_id,
        "SessionID": session_id,
        "StartedAt": started_at,
        "EndedAt": ended_at,
        "Status": status,
        "Input": {"Text": user_input},
        "Output": {"Text": output},
        "Steps": [step],
    }
    if usage:
        trace["Usage"] = usage
        step["Usage"] = usage
    return trace


def final_event(request_id: str, invocation_id: str, output: str) -> dict:
    return {
        "requestID": request_id,
        "invocationId": invocation_id,
        "author": APP_NAME,
        "id": f"{invocation_id}:final",
        "timestamp": now(),
        "object": "chat.completion",
        "done": True,
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": output or ""},
                "finish_reason": "stop",
            }
        ],
    }


def error_event(request_id: str, invocation_id: str, message: str) -> dict:
    return {
        "requestID": request_id,
        "invocationId": invocation_id,
        "author": APP_NAME,
        "id": f"{invocation_id}:error",
        "timestamp": now(),
        "object": "error",
        "done": True,
        "error": {"type": "run_error", "message": message},
    }


def completion_event(request_id: str, invocation_id: str, error_message: str = "") -> dict:
    event = {
        "requestID": request_id,
        "invocationId": invocation_id,
        "author": APP_NAME,
        "id": f"{invocation_id}:done",
        "timestamp": now(),
        "object": "runner.completion",
        "done": True,
    }
    if error_message:
        event["error"] = {"type": "run_error", "message": error_message}
    return event
