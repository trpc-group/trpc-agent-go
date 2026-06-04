# AgentNode External Tool AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a `GraphAgent` whose first node is an `AgentNode` running an `LLMAgent` with node-scoped external tools.

The workflow is:

- `research_agent` is an `AgentNode`; its child `LLMAgent` receives `agent.WithExternalTools(...)` through `graph.WithAgentNodeRunOptions(...)`.
- `external_tool_gate` is a normal graph node. It reads the child agent tool call written by the AgentNode output mapper, interrupts with the `toolCallId`, and waits for the AG-UI caller to execute the tool.
- `final_agent` is another `AgentNode`; after resume, it receives the external tool result and writes the final answer.

## Run

From the `examples/agui` module:

```bash
export OPENAI_BASE_URL="https://your-openai-compatible-base-url"
export OPENAI_API_KEY="<your api key>"
go run ./server/externaltool/agentnode \
  -model deepseek-v4-flash \
  -address 127.0.0.1:8080 \
  -path /agui
```

## Resume Contract

Call 1 sends a `role=user` message. The response should include an `external_search` tool call and a `graph.node.interrupt` activity containing `lineageId` and `checkpointId`.

Call 2 sends a trailing `role=tool` message whose `toolCallId` matches the call from call 1. It also sends the call 1 values back through `forwardedProps.lineage_id` and `forwardedProps.checkpoint_id`.

The checkpoint saver is in-memory, so keep the server process running between the two calls.

## Client

In another terminal:

```bash
python3 ./server/externaltool/agentnode/client.py
```

The client sends call 1, prints the `external_search` tool call arguments and checkpoint values, asks for the external search result, then sends call 2 to resume the graph.

For a non-interactive run, pass the caller-side tool result directly:

```bash
python3 ./server/externaltool/agentnode/client.py \
  --tool-result 'external search result provided by the caller'
```

To keep a local run log:

```bash
python3 ./server/externaltool/agentnode/client.py \
  --tool-result 'external search result provided by the caller' \
  | tee /tmp/agentnode-client.log
```

The output has this two-step shape:

```text
Call 1: waiting for external_search interrupt.
toolCallId: call_e4faba84405f47e0912b7eed
toolArgs: {"query": "GraphAgent AgentNode external tool resume"}
lineageId: ca12a238-a997-455f-8d04-ae3a6e1c41aa
checkpointId: 7885895e-4aee-4a73-a297-fa3a1e0d9203
external_search result>

Call 2: resuming graph.

Final answer:
Based on the external tool result, the explanation for "GraphAgent AgentNode external tool resume" is: 123456. The graph resumed after caller-side external tool execution.
```
