# AgentNode LLMAgent External Tool Resume

This example demonstrates a `GraphAgent` workflow where an `AgentNode` runs an `LLMAgent` with node-scoped external tools.

The child `LLMAgent` emits the external tool call. A following normal graph node performs the checkpoint interrupt and later feeds the caller-provided tool result back to the same `AgentNode`.

The flow is:

- `research_agent` receives `agent.WithExternalTools(...)` through `graph.WithAgentNodeRunOptions(...)`.
- `external_tool_gate` is a normal graph node. It reads the child agent tool call written by the AgentNode output mapper and calls `graph.Interrupt` with the `toolCallId`.
- The caller supplies the external search result.
- The graph resumes from the checkpoint with `graph.NewResumeCommand().WithResumeMap(...)`.
- `external_tool_gate` stores the caller result as a tool message in graph state.
- `research_agent` uses `graph.WithAgentNodeInputMapper(...)` to project that tool message into `graph.StateKeyAgentInputMessage`.
- `research_agent` runs again with that tool message and writes the final answer.
- The process keeps one runner session open so later turns reuse the same conversation history.

## Run

From the `examples/graph` module:

```bash
export OPENAI_BASE_URL="https://your-openai-compatible-base-url"
export OPENAI_API_KEY="<your api key>"
go run ./agentnode_llmagent_externaltool -model deepseek-v4-flash
```

Type a user request. When the graph pauses, paste the external tool result at the `external_search result>` prompt. Type another user request to start the next turn, or type `/exit` to quit.

The output has this shape:

```text
Session: agentnode-llmagent-externaltool-session-xxx
Type a request to start a turn. Type /exit to quit.
user> Use external search to get user input and say hello+tool result after you receive it

Turn #1: waiting for external_search interrupt.
toolCallId: call_xxx
toolArgs: {"query": "user input"}
checkpointId: checkpoint-xxx
external_search result> 1234

Turn #1: resuming graph.

Final answer:
Hello 1234
user> Use external search to get user input and say hi+tool result after you receive it

Turn #2: waiting for external_search interrupt.
toolCallId: call_xxx
toolArgs: {"query": "user input"}
checkpointId: checkpoint-xxx
external_search result> 56789

Turn #2: resuming graph.

Final answer:
Hi 56789
```
