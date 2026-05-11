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

The Langfuse exporter only converts `gen_ai.input.messages.otel` and `gen_ai.output.messages.otel` into the displayed input/output payload. Deprecated compatibility fields remain available on raw spans for other consumers, but Langfuse does not parse them.

This keeps existing raw attributes available while making the OTel payload the only Langfuse conversion path.
