#!/usr/bin/env python3
import argparse
import json
import sys
import urllib.error
import urllib.request
import uuid


def main() -> int:
    args = parse_args()
    try:
        run(args)
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


def run(args: argparse.Namespace) -> None:
    suffix = uuid.uuid4().hex[:8]
    thread_id = args.thread_id
    lineage_id = args.lineage_id or f"agenttool-demo-lineage-{suffix}"
    run_id_1 = args.run_id_1 or f"agenttool-demo-run-1-{suffix}"
    run_id_2 = args.run_id_2 or f"agenttool-demo-run-2-{suffix}"
    first_payload = {
        "threadId": thread_id,
        "runId": run_id_1,
        "state": {"lineage_id": lineage_id},
        "messages": [{"role": "user", "content": args.question}],
    }
    print("Call 1: waiting for AgentTool child graph interrupt.")
    first_events = list(stream_events(args.endpoint, first_payload))
    require_no_run_error(first_events)
    require_run_finished(first_events)
    checkpoint_id = extract_checkpoint_id(first_events)
    review_graph_tool_call_id = tool_call_id(first_events, "review_graph_tool")
    print(f"threadId: {thread_id}")
    print(f"lineageId: {lineage_id}")
    print(f"checkpointId: {checkpoint_id}")
    print(f"toolCallId: {review_graph_tool_call_id}")
    second_payload = {
        "threadId": thread_id,
        "runId": run_id_2,
        "state": {
            "lineage_id": lineage_id,
            "checkpoint_id": checkpoint_id,
            "resume_map": {"review_decision": args.decision},
        },
        "messages": [{"role": "user", "content": ""}],
    }
    print("\nCall 2: resuming child graph through parent checkpoint.")
    second_events = list(stream_events(args.endpoint, second_payload))
    require_no_run_error(second_events)
    require_run_finished(second_events)
    require_resume_ack(second_events)
    require_tool_result(
        second_events,
        review_graph_tool_call_id,
        f"review decision: {args.decision}",
    )
    final_text = require_final_text(second_events)
    print("\nFinal answer:")
    print(final_text)
    print(f"\nVerified: child graph resumed with review decision {args.decision}.")


def stream_events(endpoint, payload):
    request = urllib.request.Request(
        endpoint,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request) as response:
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
        raise RuntimeError("RUN_FINISHED was not found.")


def interrupt_values(events):
    for event in events:
        if event.get("type") != "ACTIVITY_DELTA":
            continue
        if event.get("activityType") != "graph.node.interrupt":
            continue
        for patch in event.get("patch", []):
            if patch.get("path") == "/interrupt":
                yield patch.get("value") or {}


def extract_checkpoint_id(events):
    for value in interrupt_values(events):
        if value.get("nodeId") == "execute_tools" and value.get("checkpointId"):
            return value["checkpointId"]
    raise RuntimeError("Parent execute_tools checkpointId was not found.")


def require_resume_ack(events):
    for event in events:
        if event.get("type") != "ACTIVITY_DELTA":
            continue
        if event.get("activityType") != "graph.node.interrupt":
            continue
        if any(patch.get("path") == "/resume" for patch in event.get("patch", [])):
            return
    raise RuntimeError("Graph interrupt resume acknowledgement was not found.")


def tool_call_id(events, tool_name):
    for event in events:
        if event.get("type") != "TOOL_CALL_START":
            continue
        if event.get("toolCallName") == tool_name:
            return event.get("toolCallId")
    raise RuntimeError(f"{tool_name} toolCallId was not found.")


def require_tool_result(events, tool_call_id_value, expected_text):
    for event in events:
        if event.get("type") != "TOOL_CALL_RESULT":
            continue
        if event.get("toolCallId") != tool_call_id_value:
            continue
        if expected_text in event.get("content", ""):
            return
    raise RuntimeError("Expected AgentTool result was not found.")


def require_final_text(events):
    text = "".join(
        event.get("delta", "")
        for event in events
        if event.get("type") == "TEXT_MESSAGE_CONTENT"
    )
    if not text.strip():
        raise RuntimeError("Final text response was not found.")
    return text


def parse_args():
    parser = argparse.ArgumentParser(
        description="Run the AG-UI AgentTool interrupt demo client.",
    )
    parser.add_argument("--endpoint", default="http://127.0.0.1:8080/agui")
    parser.add_argument("--thread-id", default="agenttool-demo-thread")
    parser.add_argument("--lineage-id")
    parser.add_argument("--run-id-1")
    parser.add_argument("--run-id-2")
    parser.add_argument(
        "--question",
        default="Review the AgentTool graph interrupt implementation before release.",
    )
    parser.add_argument("--decision", default="approved")
    return parser.parse_args()


if __name__ == "__main__":
    raise SystemExit(main())
