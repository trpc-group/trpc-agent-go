# Bubble Tea AG-UI Client

This sample uses [Bubble Tea](https://github.com/charmbracelet/bubbletea) to present a terminal chat UI that consumes the AG-UI SSE stream exposed by the example server. Events are rendered as soon as they arrive so you can watch the agent reason step by step.

## Run the Client

From `examples/agui`:

```bash
go run ./client/bubbletea/
```

You can customise the endpoint with `--endpoint` if the server is hosted elsewhere.

## Sample Output

The client streams every AG-UI event. Submitting `calculate 1.2+3.5` produces output like the following (truncated IDs for clarity):

```text
Simple AG-UI Client. Press Ctrl+C to quit.
You> calculate 1.2+3.5
Agent> [RUN_STARTED]
Agent> [TEXT_MESSAGE_START]
Agent> [TEXT_MESSAGE_CONTENT] 我来
Agent> [TEXT_MESSAGE_CONTENT] 帮
Agent> [TEXT_MESSAGE_CONTENT] 您
Agent> [TEXT_MESSAGE_CONTENT] 计算
Agent> [TEXT_MESSAGE_CONTENT] 1
Agent> [TEXT_MESSAGE_CONTENT] .
Agent> [TEXT_MESSAGE_CONTENT] 2
Agent> [TEXT_MESSAGE_CONTENT]  +
Agent> [TEXT_MESSAGE_CONTENT] 3
Agent> [TEXT_MESSAGE_CONTENT] .
Agent> [TEXT_MESSAGE_CONTENT] 5
Agent> [TEXT_MESSAGE_CONTENT] 。
Agent> [TOOL_CALL_START] tool call 'calculator' started, id: call_...
Agent> [TOOL_CALL_ARGS] tool args: {"a":1.2,"b":3.5,"operation":"plus"}
Agent> [TOOL_CALL_END] tool call completed, id: call_...
Agent> [TOOL_CALL_RESULT] tool result: {"result":4.7}
Agent> [TEXT_MESSAGE_START]
Agent> [TEXT_MESSAGE_CONTENT] 1
Agent> [TEXT_MESSAGE_CONTENT] .
Agent> [TEXT_MESSAGE_CONTENT] 2
Agent> [TEXT_MESSAGE_CONTENT]  +
Agent> [TEXT_MESSAGE_CONTENT] 3
Agent> [TEXT_MESSAGE_CONTENT] .
Agent> [TEXT_MESSAGE_CONTENT] 5
Agent> [TEXT_MESSAGE_CONTENT]  =
Agent> [TEXT_MESSAGE_CONTENT] 4
Agent> [TEXT_MESSAGE_CONTENT] .
Agent> [TEXT_MESSAGE_CONTENT] 7
Agent> [TEXT_MESSAGE_END]
Agent> [RUN_FINISHED]
```
