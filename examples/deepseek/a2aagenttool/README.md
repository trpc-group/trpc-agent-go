# A2A AgentTool Example

This example shows how to register a remote A2A agent as an AgentTool for a
parent LLM agent.

## Run

From the repository root:

```bash
bash ./dpskv3.sh
```

Or run the example directly from the examples module:

```bash
cd examples
OPENAI_BASE_URL="https://api.deepseek.com/v1" \
OPENAI_API_KEY="<your-api-key>" \
MODEL_NAME="deepseek-v4-flash" \
go run ./deepseek/a2aagenttool
```

Expected key output:

```text
Tool call: remote_math_agent ...
Remote tool response: "Remote A2A result: 17*23+5 = 396."
Validation passed: remote A2A agent was injected as an AgentTool and called by the parent agent.
```

## Execution Flow

1. The example starts a local A2A server on `127.0.0.1:18889`.
2. The A2A server exposes `remote_math_agent` as a remote A2A agent.
3. The client side creates an `A2AAgent` by reading the remote AgentCard.
4. The `A2AAgent` is wrapped with `agenttool.NewTool(...)`.
5. The parent agent registers that wrapper in `llmagent.WithTools(...)`.
6. The user asks the parent agent to calculate `17*23+5`.
7. The parent agent calls the `remote_math_agent` tool.
8. The tool sends the request to the remote A2A server.
9. The remote agent returns `Remote A2A result: 17*23+5 = 396.`
10. The parent agent receives the tool result and replies to the user.

## Notes

- `A2AAgent` implements `agent.Agent`, so it must be wrapped by
  `agenttool.NewTool(...)` before registering it in `llmagent.WithTools(...)`.
- The parent agent sees only `remote_math_agent` as one tool. The remote
  agent's internal tools are not expanded locally.
- This example uses non-streaming inner execution to keep the final tool result
  easy to inspect.
