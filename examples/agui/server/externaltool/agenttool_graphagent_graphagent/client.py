#!/usr/bin/env python3
import argparse
import json
import sys
import urllib.error
import urllib.request
import uuid


def post_sse(endpoint, payload):
    data = json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(
        endpoint,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    events = []
    with urllib.request.urlopen(request) as response:
        for raw_line in response:
            line = raw_line.decode("utf-8").rstrip("\r\n")
            if line:
                print(line, flush=True)
            if not line.startswith("data: "):
                continue
            event = json.loads(line.removeprefix("data: "))
            events.append(event)
    return events


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


def main():
    args = parse_args()
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
    first_events = post_sse(args.endpoint, first_payload)
    require_no_run_error(first_events)
    require_run_finished(first_events)
    checkpoint_id = extract_checkpoint_id(first_events)
    review_graph_tool_call_id = tool_call_id(first_events, "review_graph_tool")
    print(f"threadId={thread_id}", file=sys.stderr, flush=True)
    print(f"lineageId={lineage_id}", file=sys.stderr, flush=True)
    print(f"checkpointId={checkpoint_id}", file=sys.stderr, flush=True)
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
    second_events = post_sse(args.endpoint, second_payload)
    require_no_run_error(second_events)
    require_run_finished(second_events)
    require_resume_ack(second_events)
    require_tool_result(
        second_events,
        review_graph_tool_call_id,
        f"review decision: {args.decision}",
    )
    final_text = require_final_text(second_events)
    print(f"finalText={final_text}", file=sys.stderr, flush=True)
    print("verified=true", file=sys.stderr, flush=True)


if __name__ == "__main__":
    try:
        main()
    except urllib.error.HTTPError as exc:
        print(f"HTTP {exc.code}: {exc.read().decode('utf-8')}", file=sys.stderr)
        sys.exit(1)
    except Exception as exc:
        print(f"Error: {exc}", file=sys.stderr)
        sys.exit(1)
