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
Remote internal tool called: calculator expression=17*23+5
Remote tool response: ...
Validation passed: remote A2A agent was injected as an AgentTool and called by the parent agent.
```

## Execution Flow

1. The example starts a local A2A server on `127.0.0.1:18889`.
2. The A2A server exposes `remote_math_agent` as a remote A2A agent.
   This remote agent has its own internal `calculator` tool.
3. The client side creates an `A2AAgent` by reading the remote AgentCard.
4. The client side sets `WithInputSchema` on the `A2AAgent`. This only shapes
   the local AgentTool input declaration that the parent model sees; A2A
   AgentCard does not provide JSON Schema in this version.
5. The `A2AAgent` is wrapped with `agenttool.NewTool(...)`.
6. The parent agent registers that wrapper in `llmagent.WithTools(...)`.
7. The user asks the parent agent to calculate `17*23+5`.
8. The parent agent calls the `remote_math_agent` tool with the explicit
   `request` input schema configured on the local `A2AAgent`.
9. The AgentTool sends the request to the remote A2A server.
10. The remote A2A agent calls its internal `calculator` tool.
11. The calculator returns `396` to the remote A2A agent.
12. The remote A2A agent returns the result through A2A.
13. The parent agent receives the remote tool result and replies to the user.

## Notes

- `A2AAgent` implements `agent.Agent`, so it must be wrapped by
  `agenttool.NewTool(...)` before registering it in `llmagent.WithTools(...)`.
- The parent agent sees only `remote_math_agent` as one tool. The remote
  agent's internal `calculator` tool is used remotely and is not expanded
  locally.
- The example does not set an output schema on `A2AAgent`. Remote output shape
  should be enforced on the A2A service side when runtime guarantees are needed.
- This example uses non-streaming inner execution to keep the final tool result
  easy to inspect.
