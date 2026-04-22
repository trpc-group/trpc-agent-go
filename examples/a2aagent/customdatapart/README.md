# A2AAgent Custom DataPart Example

This example shows how to carry a custom structured payload across an A2A boundary without putting that payload into `Message.Content`.

## What This Example Shows

1. A remote agent produces a normal text response.
2. A wrapper emits one extra `graph.node.custom` event and stores a fixed payload in `event.Extensions`.
3. The A2A server uses `a2a.WithEventToA2APartMapper()` to convert that extension payload into a custom `DataPart` with `custom_part_kind="custom_data"`.
4. The A2A client agent uses `a2aagent.WithA2ADataPartMapper()` to convert the custom `DataPart` back into `event.Extensions`.
5. The local UI reads the restored extension payload and prints a visible custom line.

## DataPart Mapping Path

The custom payload goes through the following round-trip:

1. Local event payload:
   `event.Extensions["trpc.a2a.custom_payload"] = customPayload{...}`
2. Server-side mapping:
   `WithEventToA2APartMapper()` reads that extension and emits a custom `DataPart` with `custom_part_kind="custom_data"`.
3. A2A transport:
   the payload is now carried as a custom A2A `DataPart`.
4. Client-side mapping:
   `WithA2ADataPartMapper()` detects `custom_part_kind="custom_data"` and writes the payload back into `event.Extensions`.
5. Local consumption:
   application code reads the restored extension and handles it as structured data.

In other words, the payload shape is:

`event.Extensions -> A2A DataPart -> event.Extensions`

## Environment Variables

This example needs the same model configuration as [`a2aagent`](../README.md).

```bash
# Example: DeepSeek
export OPENAI_API_KEY="your-deepseek-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export MODEL_NAME="deepseek-chat"
```

## Run

From the repository root:

```bash
cd examples && go run ./a2aagent/customdatapart \
  -model "${MODEL_NAME:-deepseek-chat}" \
  -streaming=true
```

The example automatically picks an available local port and prints the actual A2A server URL at startup.

## Expected Output

You should see two kinds of output:

- Normal assistant text:
  `🤖 Assistant: ...`
- Custom mapper output:
  `🧩 Agent mapper(custom_data): Custom data part data`

The second line is produced after the client-side mapper restores the custom payload into `event.Extensions` and the local UI reads it back.
