# Multimodal Telemetry Messages

## Message Attributes

tRPC-Agent-Go emits two message attribute families:

- `gen_ai.input.messages` and `gen_ai.output.messages` are deprecated compatibility fields. Their payload shape is kept unchanged for existing telemetry consumers.
- `gen_ai.input.messages.otel` and `gen_ai.output.messages.otel` are the recommended fields for new integrations. Their payload follows an OpenTelemetry-aligned `role` plus `parts` schema.

The old fields will remain available in this change, but new adapters should read the `.otel` fields first.

## OTel Payload Shape

`gen_ai.input.messages.otel` is a JSON array of messages:

```json
[
  {
    "role": "user",
    "parts": [
      {"type": "text", "content": "describe this image"},
      {"type": "uri", "modality": "image", "mime_type": "image/png", "uri": "https://example.com/image.png"}
    ]
  }
]
```

`gen_ai.output.messages.otel` is a JSON array of output messages:

```json
[
  {
    "role": "assistant",
    "parts": [
      {"type": "text", "content": "The image shows a city skyline."}
    ],
    "finish_reason": "stop"
  }
]
```

Supported part types include:

- `text`: plain text content.
- `uri`: URI-backed multimodal content with `modality` and optional `mime_type`.
- `blob`: base64-encoded binary content with `modality` and optional `mime_type`.
- `file`: uploaded file references via `file_id`.
- `tool_call`: assistant tool requests.
- `tool_call_response`: tool outputs sent back to the model.
- `reasoning`: provider reasoning or thinking content.

## Langfuse Conversion

The Langfuse exporter folds messages into `langfuse.observation.input` / `output` in this order:

1. `gen_ai.input.messages.otel` / `gen_ai.output.messages.otel` (GenAI `role` + `parts`, passed through for Langfuse to convert)
2. If `.otel` is missing: legacy `gen_ai.input.messages` / `gen_ai.output.messages` (passed through as-is; output is conversation-shaped and preferred over `llm_response`)
3. On chat/generation spans only: `trpc.go.agent.llm_request` / `llm_response`

Jaeger and other generic OTLP backends still read raw span attributes, so their Drop policy differs from Langfuse. See Span Attribute Policy in [Observability](observability.md).
