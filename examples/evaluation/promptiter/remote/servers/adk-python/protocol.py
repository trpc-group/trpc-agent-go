"""tRPC-Agent HTTP protocol payloads for the ADK PromptIter server."""

from __future__ import annotations

import datetime as dt
import uuid

from agent import (
    APP_NAME,
    HEADLINE_AGENT,
    HIGHLIGHTS_AGENT,
    INSTRUCTIONS,
    NODE_JOIN_RECAP_PARTS,
    NODE_PREPARE_GAME_INPUT,
    RECAP_WRITER_AGENT,
    SPORTS_EDITOR_AGENT,
    STAGE_AGENTS,
    STATS_ANGLE_AGENT,
    STRUCTURE_ID,
    input_text,
    node_id,
    run_agent,
    surface_id,
)


def structure_payload() -> dict:
    return {
        "structure": {
            "StructureID": STRUCTURE_ID,
            "EntryNodeID": APP_NAME,
            "Nodes": [
                {"NodeID": APP_NAME, "Kind": "agent", "Name": APP_NAME},
                {"NodeID": node_id(NODE_PREPARE_GAME_INPUT), "Kind": "function", "Name": NODE_PREPARE_GAME_INPUT},
                {"NodeID": node_id(HEADLINE_AGENT), "Kind": "agent", "Name": HEADLINE_AGENT},
                {"NodeID": node_id(HIGHLIGHTS_AGENT), "Kind": "agent", "Name": HIGHLIGHTS_AGENT},
                {"NodeID": node_id(STATS_ANGLE_AGENT), "Kind": "agent", "Name": STATS_ANGLE_AGENT},
                {"NodeID": node_id(NODE_JOIN_RECAP_PARTS), "Kind": "function", "Name": NODE_JOIN_RECAP_PARTS},
                {"NodeID": node_id(RECAP_WRITER_AGENT), "Kind": "agent", "Name": RECAP_WRITER_AGENT},
                {"NodeID": node_id(SPORTS_EDITOR_AGENT), "Kind": "agent", "Name": SPORTS_EDITOR_AGENT},
            ],
            "Edges": [
                {"FromNodeID": APP_NAME, "ToNodeID": node_id(NODE_PREPARE_GAME_INPUT)},
                {"FromNodeID": node_id(NODE_PREPARE_GAME_INPUT), "ToNodeID": node_id(HEADLINE_AGENT)},
                {"FromNodeID": node_id(NODE_PREPARE_GAME_INPUT), "ToNodeID": node_id(HIGHLIGHTS_AGENT)},
                {"FromNodeID": node_id(NODE_PREPARE_GAME_INPUT), "ToNodeID": node_id(STATS_ANGLE_AGENT)},
                {"FromNodeID": node_id(HEADLINE_AGENT), "ToNodeID": node_id(NODE_JOIN_RECAP_PARTS)},
                {"FromNodeID": node_id(HIGHLIGHTS_AGENT), "ToNodeID": node_id(NODE_JOIN_RECAP_PARTS)},
                {"FromNodeID": node_id(STATS_ANGLE_AGENT), "ToNodeID": node_id(NODE_JOIN_RECAP_PARTS)},
                {"FromNodeID": node_id(NODE_JOIN_RECAP_PARTS), "ToNodeID": node_id(RECAP_WRITER_AGENT)},
                {"FromNodeID": node_id(RECAP_WRITER_AGENT), "ToNodeID": node_id(SPORTS_EDITOR_AGENT)},
            ],
            "Surfaces": [
                {
                    "SurfaceID": surface_id(agent_name),
                    "NodeID": node_id(agent_name),
                    "Type": "instruction",
                    "Value": {"Text": INSTRUCTIONS[agent_name]},
                }
                for agent_name in STAGE_AGENTS
            ],
        }
    }


def trace_payload(request: dict, output: str, status: str, steps: list[dict], error: str = "") -> dict:
    session = request.get("session") or {}
    run_options = request.get("runOptions") or {}
    invocation_id = run_options.get("requestID") or run_options.get("requestId") or str(uuid.uuid4())
    session_id = session.get("sessionId") or ""
    now = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    trace_steps = []
    for index, step in enumerate(steps):
        current = dict(step)
        current["StepID"] = current.get("StepID") or f"{invocation_id}:{index}:{current['AgentName']}"
        current["InvocationID"] = invocation_id
        current["StartedAt"] = now
        current["EndedAt"] = now
        current["Error"] = error if index == len(steps) - 1 else ""
        trace_steps.append(current)
    if not trace_steps:
        trace_steps.append(
            {
                "StepID": f"{invocation_id}:error",
                "InvocationID": invocation_id,
                "AgentName": APP_NAME,
                "NodeID": APP_NAME,
                "StartedAt": now,
                "EndedAt": now,
                "Input": {"Text": input_text(request.get("input") or {})},
                "Output": {"Text": output},
                "Error": error,
            }
        )
    return {
        "RootAgentName": APP_NAME,
        "RootInvocationID": invocation_id,
        "SessionID": session_id,
        "StartedAt": now,
        "EndedAt": now,
        "Status": status,
        "Steps": trace_steps,
    }


def run_events(request: dict, output: str, trace: dict) -> list[dict]:
    run_options = request.get("runOptions") or {}
    request_id = run_options.get("requestID") or run_options.get("requestId") or trace["RootInvocationID"]
    now = dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")
    return [
        {
            "requestID": request_id,
            "invocationId": trace["RootInvocationID"],
            "author": APP_NAME,
            "id": f"{trace['RootInvocationID']}:message",
            "timestamp": now,
            "object": "chat.completion",
            "done": True,
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": output},
                    "finish_reason": "stop",
                }
            ],
        },
        {
            "requestID": request_id,
            "invocationId": trace["RootInvocationID"],
            "author": APP_NAME,
            "id": f"{trace['RootInvocationID']}:done",
            "timestamp": now,
            "object": "runner.completion",
            "done": True,
        },
    ]


def run_response(request: dict) -> tuple[int, dict]:
    try:
        output, steps = run_agent(request)
        trace = trace_payload(request, output, "completed", steps)
        return 200, {"status": "completed", "events": run_events(request, output, trace), "executionTrace": trace}
    except Exception as exc:
        message = str(exc)
        trace = trace_payload(request, "", "failed", [], message)
        return 200, {
            "status": "failed",
            "events": run_events(request, "", trace),
            "executionTrace": trace,
            "errorMessage": message,
        }
