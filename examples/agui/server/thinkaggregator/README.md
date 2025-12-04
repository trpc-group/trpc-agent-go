# Think Aggregator AG-UI Server

This example exposes an AG-UI SSE endpoint that surfaces the model's reasoning (“think”) stream as custom events and aggregates them before persistence. It builds on the standard `tRPC-Agent-Go` runner and shows how to combine a custom translator with a per-session aggregator.

The server emits three custom AG-UI events to represent reasoning: `think_start`, `think_content`, and `think_end`. A custom aggregator buffers adjacent `think_content` fragments so they are stored and replayed as larger chunks, while non-think events keep the default aggregation for text deltas.

## Prerequisites

- An API key for the configured model provider (e.g., `OPENAI_API_KEY` when using `deepseek-chat` via the OpenAI-compatible endpoint).
- `curl` to hit the SSE and history endpoints (see commands below); any AG-UI client will also work once the server is running.

## Run

From the `examples/agui` module:

```bash
# Start the server at http://127.0.0.1:8080/agui with history at /history
go run ./server/thinkaggregator \
  -model deepseek-chat \
  -stream=true \
  -address 127.0.0.1:8080 \
  -path /agui \
  -messages-snapshot-path /history
```

On startup you will see logs similar to:

```
2025-12-03T12:45:43+08:00       INFO    main.go:63  AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
2025-12-03T12:45:43+08:00       INFO    main.go:64  AG-UI: messages snapshot available at http://127.0.0.1:8080/history
```

## Try With curl

### Live Conversation

Send a live request with curl and watch the SSE stream:

```shell
curl --location 'http://127.0.0.1:8080/agui' \
--header 'Content-Type: application/json' \
--data '{
    "threadId": "1",
    "runId": "1",
    "messages": [
        {
            "role": "user",
            "content": "Calculate 123+456"
        }
    ],
    "forwardedProps": {
        "user": "userId"
    }
}' > client.log
```

The custom aggregator merges adjacent `think_content` fragments before persistence and replay, while regular text still follows the default aggregation rules.

### History Query

Call the messages snapshot endpoint to inspect the persisted aggregation result:

```shell
curl --location 'http://127.0.0.1:8080/history' \
--header 'Content-Type: application/json' \
--data '{
    "threadId": "1",
    "runId": "1",
    "messages": [
        {
            "role": "user",
            "content": ""
        }
    ],
    "forwardedProps": {
        "user": "userId"
    }
}' > history.log
```

Full response example from the history query:

```json
{
    "type": "MESSAGES_SNAPSHOT",
    "timestamp": 1764764066684,
    "messages": [
        {
            "content": "Calculate 123+456",
            "id": "659606d6-f76d-435f-824e-9385b0ec790b",
            "name": "anonymous",
            "role": "user"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_start",
                "value": null
            },
            "id": "CUSTOM_1764764054441",
            "role": "activity"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_content",
                "value": "\nWe are given the task to calculate 123 + 456.\n We have a calculator tool that can perform operations (add, subtract, multiply, divide, power) on"
            },
            "id": "CUSTOM_1764764055077",
            "role": "activity"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_content",
                "value": " two numbers a and b.\n The operation for addition is \"add\".\n We'll set a = 123, b = 456, operation = \"add\".\n"
            },
            "id": "CUSTOM_1764764055965",
            "role": "activity"
        },
        {
            "activityType": "CUSTOM",
            "content": {
                "name": "think_end",
                "value": null
            },
            "id": "CUSTOM_1764764055965",
            "role": "activity"
        },
        {
            "content": "'ll calculate that for you using the calculator tool.\n\n",
            "id": "chatcmpl-0d21c49905b74f1c9a717ff9a8ed884a",
            "name": "demo-app",
            "role": "assistant",
            "toolCalls": [
                {
                    "id": "chatcmpl-tool-b19be9c76ebe44bd928eff476932c6cd",
                    "type": "function",
                    "function": {
                        "name": "calculator",
                        "arguments": "{\"a\": 123, \"b\": 456, \"operation\": \"add\"}"
                    }
                }
            ]
        },
        {
            "content": "{\"result\":579}",
            "id": "40c4fbbe-457a-4184-8e99-2d089f5abea9",
            "role": "tool",
            "toolCallId": "chatcmpl-tool-b19be9c76ebe44bd928eff476932c6cd"
        },
        {
            "content": "\nThe result of 123 + 456 is **579**.",
            "id": "chatcmpl-e3c3bbb40e36432cb709cdaaa54a2f36",
            "name": "demo-app",
            "role": "assistant"
        }
    ]
}
```
