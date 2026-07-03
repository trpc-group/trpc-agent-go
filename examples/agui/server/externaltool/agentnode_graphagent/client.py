#!/usr/bin/env python3

import argparse
import json
import sys
import time
import urllib.error
import urllib.request


EXTERNAL_TOOL_NAME = "external_search"
PARENT_NODES = ["research_graph_agent", "review_agent"]


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run the AgentNode GraphAgent external-tool AG-UI example end to end.",
    )
    parser.add_argument("--endpoint", default="http://127.0.0.1:8080/agui")
    parser.add_argument("--thread-id", default="agentnode-graphagent-externaltool-demo")
    parser.add_argument(
        "--question",
        default="Use external search to explain GraphAgent AgentNode nested external tool resume.",
    )
    parser.add_argument("--tool-result", help="External search result content.")
    parser.add_argument("--timeout", type=float, default=300)
    args = parser.parse_args()
    try:
        run(args)
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


def run(args: argparse.Namespace) -> None:
    run1_id = f"agentnode-graphagent-run-1-{time.time_ns()}"
    payload = {
        "threadId": args.thread_id,
        "runId": run1_id,
        "messages": [{"role": "user", "content": args.question}],
    }
    print("Call 1: waiting for external_search interrupt.")
    first = collect_first_run(stream_events(args.endpoint, payload, args.timeout))
    print(f"toolCallId: {first['toolCallId']}")
    print(f"toolArgs: {first['toolArgs'] or '{}'}")
    print(f"lineageId: {first['lineageId']}")
    print(f"checkpointId: {first['checkpointId']}")
    tool_result = args.tool_result
    if tool_result is None:
        tool_result = input("external_search result> ").strip()
    if not tool_result:
        raise ValueError("external search result is empty")
    run2_id = f"agentnode-graphagent-run-2-{time.time_ns()}"
    payload = {
        "threadId": args.thread_id,
        "runId": run2_id,
        "forwardedProps": {
            "lineage_id": first["lineageId"],
            "checkpoint_id": first["checkpointId"],
        },
        "messages": [
            {
                "id": f"tool-result-{first['toolCallId']}",
                "role": "tool",
                "toolCallId": first["toolCallId"],
                "name": EXTERNAL_TOOL_NAME,
                "content": tool_result,
            }
        ],
    }
    print("\nCall 2: resuming graph.")
    second = collect_final_run(stream_events(args.endpoint, payload, args.timeout))
    print_node_trace(second["nodes"])
    print("\nFinal answer:")
    print(second["finalText"] or "(no final text)")


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


def collect_first_run(events):
    tool_call_id = ""
    tool_args_by_id = {}
    interrupt = {}
    for event in events:
        event_type = event.get("type")
        if event_type == "RUN_ERROR":
            raise RuntimeError(event.get("message", "RUN_ERROR"))
        if event_type == "TOOL_CALL_START" and event.get("toolCallName") == EXTERNAL_TOOL_NAME:
            if tool_call_id:
                raise RuntimeError(f"expected one {EXTERNAL_TOOL_NAME} tool call")
            tool_call_id = event.get("toolCallId", "")
            tool_args_by_id[tool_call_id] = []
            continue
        if event_type == "TOOL_CALL_ARGS":
            call_id = event.get("toolCallId", "")
            tool_args_by_id.setdefault(call_id, []).append(event.get("delta", ""))
            continue
        if event_type == "ACTIVITY_DELTA" and event.get("activityType") == "graph.node.interrupt":
            interrupt = interrupt_value(event) or interrupt
    tool_call_id = tool_call_id or interrupt.get("key", "")
    prompt = interrupt.get("prompt")
    tool_args = "".join(tool_args_by_id.get(tool_call_id, []))
    if isinstance(prompt, dict):
        tool_call_id = tool_call_id or prompt.get("toolCallId", "")
        tool_args = tool_args or prompt.get("args", "")
    if not tool_call_id:
        raise RuntimeError(f"{EXTERNAL_TOOL_NAME} tool call was not found")
    if not interrupt.get("lineageId") or not interrupt.get("checkpointId"):
        raise RuntimeError("graph interrupt checkpoint was not found")
    return {
        "toolCallId": tool_call_id,
        "toolArgs": tool_args,
        "lineageId": interrupt["lineageId"],
        "checkpointId": interrupt["checkpointId"],
    }


def interrupt_value(event):
    for patch in event.get("patch", []):
        if patch.get("path") != "/interrupt":
            continue
        value = patch.get("value")
        if isinstance(value, dict):
            return value
    return None


def collect_final_run(events):
    chunks = []
    nodes = []
    for event in events:
        event_type = event.get("type")
        if event_type == "RUN_ERROR":
            raise RuntimeError(event.get("message", "RUN_ERROR"))
        if event_type == "ACTIVITY_DELTA" and event.get("activityType") == "graph.node.lifecycle":
            node = lifecycle_value(event)
            if node:
                nodes.append(node)
            continue
        if event_type == "TEXT_MESSAGE_CONTENT":
            chunks.append(event.get("delta", ""))
    ensure_resumed_parent_continued(nodes)
    return {
        "finalText": "".join(chunks).strip(),
        "nodes": nodes,
    }


def lifecycle_value(event):
    for patch in event.get("patch", []):
        if patch.get("path") != "/node":
            continue
        value = patch.get("value")
        if isinstance(value, dict):
            return value
    return None


def ensure_resumed_parent_continued(nodes):
    completed = completed_parent_nodes(nodes)
    if "research_graph_agent" not in completed:
        raise RuntimeError("resumed run did not complete research_graph_agent")
    if "review_agent" not in completed:
        raise RuntimeError("resumed run did not continue to review_agent")


def print_node_trace(nodes):
    completed = completed_parent_nodes(nodes)
    if completed:
        print("completed parent nodes: " + " -> ".join(completed))


def completed_parent_nodes(nodes):
    return [
        node.get("nodeId")
        for node in nodes
        if node.get("phase") == "complete" and node.get("nodeId") in PARENT_NODES
    ]


if __name__ == "__main__":
    raise SystemExit(main())
