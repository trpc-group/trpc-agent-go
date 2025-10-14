# React AG-UI Server

This example demonstrates how to expose an AG-UI SSE endpoint powered by the `tRPC-Agent-Go` runner and the React planner.

The server showcases how React planner tags are streamed as custom AG-UI events. These structured tags describe various reasoning steps, tool calls, and final outputs. Common tags include `/*THOUGHT*/`, `/*ACTION*/`, `/*ACTION_INPUT*/`, and `/*FINAL_ANSWER*/`. For detailed information, refer to the [React planner documentation](../../../../docs/mkdocs/en/planner.md).

In this example, a custom translator converts the React tags into events that the frontend can process. The `/*FINAL_ANSWER*/` tag is translated into regular text message events, while `/*THOUGHT*/`, `/*ACTION*/`, and `/*ACTION_INPUT*/` tags are mapped to custom events like `react.thought`, `react.action`, and `react.action_input`. This allows the frontend to fully reconstruct the React interaction flow.

## How to Run

Navigate to the `examples/agui` module and run the following command:

```bash
# Start the server at http://localhost:8080/agui
go run .
```

The server will display startup logs indicating the bound address:

```
2025-10-10T17:01:47+08:00       INFO    react/main.go:75        AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
```

If you're using a raw client, the output will look like this:

```log
Simple AG-UI client. Endpoint: http://127.0.0.1:8080/agui
Type your prompt and press Enter (Ctrl+D to exit).
You> calculate 1.2+3.5
Agent> [RUN_STARTED]
Agent> [CUSTOM] 'react.planning': {"content":"\n1. Use the calculator tool to add 1.2 and 3.5\n2. Return the result to the user\n\n","messageId":"12edf7d8-60a4-40a5-94b7-da0accfd29f9","tag":"/*PLANNING*/"}
Agent> [CUSTOM] 'react.action': {"content":"\nI will use the calculator tool to add 1.2 and 3.5.","messageId":"12edf7d8-60a4-40a5-94b7-da0accfd29f9","tag":"/*ACTION*/"}
Agent> [TOOL_CALL_START] tool call 'calculator' started, id: call_00_mXpifc8VGd6XFEHd6Rr09SI3
Agent> [TOOL_CALL_ARGS] tool args: {"a": 1.2, "b": 3.5, "operation": "add"}
Agent> [TOOL_CALL_END] tool call completed, id: call_00_mXpifc8VGd6XFEHd6Rr09SI3
Agent> [TOOL_CALL_RESULT] tool result: {"result":4.7}
Agent> [CUSTOM] 'react.reasoning': {"content":"\nThe calculator tool successfully performed the addition operation and returned the result of 4.7. This completes the calculation requested by the user.\n\n","messageId":"a38439ae-3122-4979-9eb6-5921b19231e6","tag":"/*REASONING*/"}
Agent> [TEXT_MESSAGE_START]
Agent> [TEXT_MESSAGE_CONTENT] 
1.2 + 3.5 = 4.7
Agent> [TEXT_MESSAGE_END]
Agent> [RUN_FINISHED]
```

If you're using a copilotkit client, the output will look like this:

![copilotkit](../../../../.resource/images/examples/agui-copilotkit-react.png)
