# AgentNode Handoff AgentTool AG-UI Server

This example exposes an AG-UI SSE endpoint backed by a parent `GraphAgent` that performs a server-side handoff from an `AgentNode` tool call to an `AgentTool`-wrapped child `GraphAgent`.

The workflow is:

- `handoff_planner` is an outer `AgentNode` whose child `LLMAgent` declares `handoff_task` with `agent.WithExternalTools(...)`.
- `resolve_handoff` is a normal graph node that maps `agent_id` to a concrete AgentTool call.
- `execute_agenttool` is a `ToolsNode` that executes the selected AgentTool, so AgentTool graph runtime and interrupt bridging stay on the supported path.
- `return_handoff_result` stores the AgentTool result as the original `handoff_task` tool message.
- The graph returns to `handoff_planner`, which consumes that tool message and produces the final answer.
- The selected `AgentTool` wraps `dynamic_research_graph`; that child graph interrupts for `inner_external_search`, then resumes into an internal `AgentNode` with the caller-provided tool result.

The key point is that the selected `AgentTool` is executed by a `ToolsNode`, not directly by a normal node. This keeps nested GraphAgent interrupts on the supported resume path.

## Run

From the `examples/agui` module:

```bash
export OPENAI_BASE_URL="https://your-openai-compatible-base-url"
export OPENAI_API_KEY="<your api key>"
go run ./server/externaltool/agentnode_handoff_agenttool \
  -model deepseek-v4-flash \
  -address 127.0.0.1:8080 \
  -path /agui
```

## Client

In another terminal:

```bash
python3 ./server/externaltool/agentnode_handoff_agenttool/client.py
```

The client sends one user message, waits for child graph `inner_external_search` interrupts, prompts for caller-provided tool results, sends each value through `state.resume_map`, and keeps resuming until the child graph completes and prints the final response.

When the client prints `inner_external_search result>`, enter the external search result that should be returned to the inner worker. For example:

```text
The old version is v0.5.2.
```

If the worker asks for another search result, enter the next result. For example:

```text
The new version is v0.6.0.
```

The interactive output has this shape. Each `inner_external_search result>` line is user input.

```text
Call 1: waiting for inner_external_search interrupt.
handoff_task toolCallId: call_xxx
handoff_task args: {"agent_id":"research","task":"Find the old version and the new version for the release upgrade verification."}
lineageId: handoff-agenttool-lineage-xxxx
inner_external_search toolCallId: inner_external_search
inner_external_search args: {"query":"old version release upgrade verification"}
checkpointId: checkpoint-xxxx
inner_external_search result> The old version is v0.5.2.

Call 2: resuming AgentTool child graph.
inner_external_search toolCallId: inner_external_search
inner_external_search args: {"query":"new version release upgrade verification"}
checkpointId: checkpoint-yyyy
inner_external_search result> The new version is v0.6.0.

Call 3: resuming AgentTool child graph.
Verified: child graph completed.

Final answer:
The old version is **v0.5.2** and the new version is **v0.6.0**.
```
