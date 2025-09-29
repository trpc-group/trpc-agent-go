# Raw AG-UI SSE Client

This minimal terminal client shows how to consume AG-UI events without any UI framework. It opens an SSE stream, parses each frame with the community Go SDK, and prints the events as they arrive so you can watch the agent think step by step.

## Run the Client

From `examples/agui`:

```bash
go run .
```

Pass `--endpoint` to target a different server URL. Prompts are read interactively from standard input (Ctrl+D exits, or type `quit`).

## Sample Output

Submitting `calculate 1.2+3.5` produces output similar to the following (IDs truncated for brevity):

```text
Simple AG-UI client. Endpoint: http://127.0.0.1:8080/agui
Type your prompt and press Enter (Ctrl+D to exit).
You> calculate 1.2+3.5
Agent> [RUN_STARTED]
Agent> [TEXT_MESSAGE_START]
Agent> [TEXT_MESSAGE_CONTENT] I'll calculate 1.2 + 3.5 for you.
Agent> [TEXT_MESSAGE_END]
Agent> [TOOL_CALL_START] tool call 'calculator' started, id: call_00_rwe3...
Agent> [TOOL_CALL_ARGS] tool args: {"a": 1.2, "b": 3.5, "operation": "add"}
Agent> [TOOL_CALL_END] tool call completed, id: call_00_rwe3...
Agent> [TOOL_CALL_RESULT] tool result: {"result":4.7}
Agent> [TEXT_MESSAGE_START]
Agent> [TEXT_MESSAGE_CONTENT] The result of 1.2 + 3.5 is **4.7**.
Agent> [TEXT_MESSAGE_END]
Agent> [RUN_FINISHED]
```
