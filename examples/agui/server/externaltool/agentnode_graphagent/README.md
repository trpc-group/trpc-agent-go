# AgentNode GraphAgent External Tool AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a parent `GraphAgent` with two `AgentNode` children. The first child is a `GraphAgent`; the second child is a simple `LLMAgent`.

The child `GraphAgent` emits the external tool call from its own LLM node. A normal node inside the child graph calls `graph.Interrupt`. The interrupt bubbles to the parent graph, and the AG-UI caller resumes from the parent checkpoint.

The workflow is:

- The parent graph runs `research_graph_agent` as the first `AgentNode`.
- The child graph's `research_llm` node calls `external_search`.
- The child graph's `external_tool_interrupt` node calls `graph.Interrupt` with the pending `toolCallId`.
- AG-UI emits a top-level `graph.node.interrupt` activity containing the parent `lineageId` and `checkpointId`.
- The caller sends a trailing `role=tool` message with the external search result.
- The parent graph resumes from its checkpoint, then the framework resumes the child graph checkpoint automatically.
- After `research_graph_agent` finishes, the parent graph runs `review_agent` as the second `AgentNode`, making the resumed parent execution visible.

## Run

From the `examples/agui` module:

```bash
export OPENAI_BASE_URL="https://your-openai-compatible-base-url"
export OPENAI_API_KEY="<your api key>"
go run ./server/externaltool/agentnode_graphagent \
  -model deepseek-v4-flash \
  -address 127.0.0.1:8080 \
  -path /agui
```

## Resume Contract

Call 1 sends a `role=user` message. The response should include an `external_search` tool call and a `graph.node.interrupt` activity containing parent graph `lineageId` and `checkpointId`.

Call 2 sends a trailing `role=tool` message whose `toolCallId` matches the call from call 1. It also sends the call 1 values back through `forwardedProps.lineage_id` and `forwardedProps.checkpoint_id`.

The checkpoint saver is in-memory, so keep the server process running between the two calls.

## What Proves Resume Works

The example enables `graph.node.lifecycle` and top-level `graph.node.interrupt` AG-UI activities. The client checks three things:

- Call 1 receives a `graph.node.interrupt` activity with parent graph `lineageId` and `checkpointId`.
- Call 2 sends the external tool result with those checkpoint values.
- Call 2 then receives lifecycle completion events for both parent nodes: `research_graph_agent -> review_agent`.

## Client

In another terminal:

```bash
python3 ./server/externaltool/agentnode_graphagent/client.py
```

For a non-interactive run, pass the caller-side tool result directly:

```bash
python3 ./server/externaltool/agentnode_graphagent/client.py \
  --tool-result 'external search result provided by the caller'
```

The output has this two-step shape:

```text
Call 1: waiting for external_search interrupt.
toolCallId: call_e4faba84405f47e0912b7eed
toolArgs: {"query": "GraphAgent AgentNode nested external tool resume"}
lineageId: ca12a238-a997-455f-8d04-ae3a6e1c41aa
checkpointId: 7885895e-4aee-4a73-a297-fa3a1e0d9203
external_search result>

Call 2: resuming graph.
completed parent nodes: research_graph_agent -> review_agent

Final answer:
Parent GraphAgent continued to review_agent after child GraphAgent resume.
```
