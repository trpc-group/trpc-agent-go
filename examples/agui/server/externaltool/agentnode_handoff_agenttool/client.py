#!/usr/bin/env python3

import argparse
import json
import sys
import time
import urllib.error
import urllib.request
import uuid


HANDOFF_TOOL_NAME = "handoff_task"
INNER_EXTERNAL_TOOL_NAME = "inner_external_search"


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run the AgentNode handoff AgentTool AG-UI example.",
    )
    parser.add_argument("--endpoint", default="http://127.0.0.1:8080/agui")
    parser.add_argument("--thread-id", default="agentnode-handoff-agenttool-demo")
    parser.add_argument(
        "--question",
        default=(
            "Hand off to the research agent. Find the old version and the new "
            "version for the release upgrade verification. Search the old "
            "version first, then search the new version."
        ),
    )
    parser.add_argument("--timeout", type=float, default=300)
    args = parser.parse_args()
    try:
        run(args)
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


def run(args: argparse.Namespace) -> None:
    suffix = uuid.uuid4().hex[:8]
    lineage_id = f"handoff-agenttool-lineage-{suffix}"
    payload = {
        "threadId": args.thread_id,
        "runId": f"handoff-run-{time.time_ns()}",
        "state": {"lineage_id": lineage_id},
        "messages": [{"role": "user", "content": args.question}],
    }
    print("Call 1: waiting for inner_external_search interrupt.")
    events = list(stream_events(args.endpoint, payload, args.timeout))
    require_no_run_error(events)
    require_run_finished(events)
    handoff = require_tool_call(events, HANDOFF_TOOL_NAME)
    print(f"{HANDOFF_TOOL_NAME} toolCallId: {handoff['toolCallId']}")
    print(f"{HANDOFF_TOOL_NAME} args: {handoff['args'] or '{}'}")
    print(f"lineageId: {lineage_id}")
    call_no = 1
    while True:
        interrupt = optional_interrupt(events)
        if interrupt is None:
            break
        print(f"{INNER_EXTERNAL_TOOL_NAME} toolCallId: {interrupt['toolCallId']}")
        print(f"{INNER_EXTERNAL_TOOL_NAME} args: {interrupt['args'] or '{}'}")
        print(f"checkpointId: {interrupt['checkpointId']}")
        tool_result = resolve_tool_result()
        payload = {
            "threadId": args.thread_id,
            "runId": f"handoff-resume-{time.time_ns()}",
            "state": {
                "lineage_id": lineage_id,
                "checkpoint_id": interrupt["checkpointId"],
                "resume_map": {interrupt["toolCallId"]: tool_result},
            },
            "messages": [{"role": "user", "content": ""}],
        }
        call_no += 1
        print(f"\nCall {call_no}: resuming AgentTool child graph.")
        events = list(stream_events(args.endpoint, payload, args.timeout))
        require_no_run_error(events)
        require_run_finished(events)
    final_text = require_final_text(events)
    print("Verified: child graph completed.")
    print("\nFinal answer:")
    print(final_text)


def resolve_tool_result():
    tool_result = input(f"{INNER_EXTERNAL_TOOL_NAME} result> ").strip()
    if not tool_result:
        raise ValueError(f"{INNER_EXTERNAL_TOOL_NAME} result is empty")
    return tool_result


def stream_events(endpoint: str, payload: dict, timeout: float):
    request = urllib.request.Request(
        endpoint,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            data_lines = []
            for raw_line in response:
                line = raw_line.decode("utf-8").rstrip("\r\n")
                if line.startswith("data:"):
                    data_lines.append(line[5:].lstrip())
                    continue
                if line == "" and data_lines:
                    yield parse_event(data_lines)
                    data_lines = []
            if data_lines:
                yield parse_event(data_lines)
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code}: {body}") from exc


def parse_event(data_lines):
    data = "\n".join(data_lines)
    if data == "[DONE]":
        return {"type": "DONE"}
    return json.loads(data)


def require_no_run_error(events):
    for event in events:
        if event.get("type") == "RUN_ERROR":
            raise RuntimeError(event.get("message", "RUN_ERROR"))


def require_run_finished(events):
    if not any(event.get("type") == "RUN_FINISHED" for event in events):
        raise RuntimeError("RUN_FINISHED was not found")


def require_tool_call(events, tool_name):
    tool_call_id = ""
    args_by_id = {}
    for event in events:
        event_type = event.get("type")
        if event_type == "TOOL_CALL_START" and event.get("toolCallName") == tool_name:
            tool_call_id = event.get("toolCallId", "")
            args_by_id.setdefault(tool_call_id, [])
            continue
        if event_type == "TOOL_CALL_ARGS":
            call_id = event.get("toolCallId", "")
            args_by_id.setdefault(call_id, []).append(event.get("delta", ""))
    if not tool_call_id:
        raise RuntimeError(f"{tool_name} tool call was not found")
    return {
        "toolCallId": tool_call_id,
        "args": "".join(args_by_id.get(tool_call_id, [])),
    }


def require_interrupt(events):
    interrupt = optional_interrupt(events)
    if interrupt is not None:
        return interrupt
    raise RuntimeError("inner_external_search graph interrupt was not found")


def optional_interrupt(events):
    for event in events:
        if event.get("type") != "ACTIVITY_DELTA":
            continue
        if event.get("activityType") != "graph.node.interrupt":
            continue
        for patch in event.get("patch", []):
            if patch.get("path") != "/interrupt":
                continue
            value = patch.get("value") or {}
            request = value.get("prompt") or value.get("interruptValue") or {}
            if request.get("name") != INNER_EXTERNAL_TOOL_NAME:
                continue
            checkpoint_id = value.get("checkpointId", "")
            tool_call_id = request.get("toolCallId", "")
            if checkpoint_id and tool_call_id:
                return {
                    "checkpointId": checkpoint_id,
                    "toolCallId": tool_call_id,
                    "args": request.get("args", ""),
                }
    return None


def require_final_text(events):
    text = "".join(
        event.get("delta", "")
        for event in events
        if event.get("type") == "TEXT_MESSAGE_CONTENT"
    )
    if not text.strip():
        raise RuntimeError("final text response was not found")
    return text.strip()


if __name__ == "__main__":
    raise SystemExit(main())
