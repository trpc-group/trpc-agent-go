# Telemetry Multimodal Protocol

## Overview

This page defines the tracing contract, the framework-to-OTel mapping, and
provider capabilities.

## OTel References

- [OpenTelemetry GenAI attribute: `gen_ai.input.messages`](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/#gen-ai-input-messages)
- [OpenTelemetry GenAI input messages JSON schema](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-input-messages.json)
- [OpenTelemetry GenAI output messages JSON schema](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-output-messages.json)
- [OpenTelemetry GenAI OpenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/openai/)

## Telemetry Contract

- `gen_ai.input.messages` is an OTel semantic payload. It is not a raw provider
  request body.
- `gen_ai.output.messages` is an OTel semantic payload for model outputs. It is
  not guaranteed to match a provider SDK response shape.
- `llm.request` and `llm.response` remain provider-oriented snapshots and can
  still differ from OTel message telemetry.

## OTel Message Shape

Each message uses:

- `role`
- optional `name`
- required `parts`

Supported part shapes used by the framework:

- `text`: text content.
- `blob`: inline binary data, serialized as base64 in `content`.
- `uri`: external references such as image URLs.
- `file`: file identifiers such as uploaded provider files.
- `tool_call`: assistant tool requests.
- `tool_call_response`: tool outputs sent back to the model.
- `reasoning`: provider reasoning or thinking content.

Example:

```json
[
  {
    "role": "user",
    "parts": [
      {
        "type": "text",
        "content": "What is in this document?"
      },
      {
        "type": "file",
        "modality": "file",
        "mime_type": "application/pdf",
        "file_id": "file-123",
        "filename": "paper.pdf"
      }
    ]
  },
  {
    "role": "assistant",
    "parts": [
      {
        "type": "tool_call",
        "id": "call_1",
        "name": "search",
        "arguments": {
          "q": "OpenTelemetry"
        }
      }
    ]
  },
  {
    "role": "tool",
    "name": "search",
    "parts": [
      {
        "type": "tool_call_response",
        "id": "call_1",
        "response": {
          "result": "ok"
        }
      }
    ]
  }
]
```

## Framework Mapping

`model.Message` and `model.ContentPart` are mapped as follows:

- `Message.Content` becomes a `text` part.
- `ContentParts[text]` becomes a `text` part.
- `ContentParts[image]`:
  - URL input becomes `uri` with `modality: image`
  - inline bytes become `blob` with `modality: image`
  - `detail` is preserved as an additional property
- `ContentParts[audio]` becomes `blob` with `modality: audio`
- `ContentParts[file]`:
  - `file_id` becomes `file`
  - inline bytes become `blob`
  - modality is inferred from MIME type, so image/audio/video files are visible
    in telemetry even though the core model currently has no `video` content type
- `Message.ToolCalls` becomes `tool_call` parts
- `role=tool` messages become `tool_call_response` parts
- `Message.ReasoningContent` becomes a `reasoning` part

## Capability Matrix

The framework normalizes multimodal data at the telemetry layer, but provider
support still depends on each model adapter.

- OpenAI-compatible adapters in `model/openai` support user text, image, audio,
  and file input. System and assistant `ContentParts` are effectively text-only.
  Tool calls are supported. The current built-in OpenAI adapter uses Chat
  Completions, not Responses.
- Gemini in `model/gemini` supports text, image, audio, and inline file bytes.
  File-by-ID is not a first-class generic path in the current adapter.
- Anthropic in `model/anthropic` currently keeps only text parts in message
  conversion. Non-text parts are dropped before the provider call.
- Ollama in `model/ollama` supports text and inline image bytes. Audio and file
  inputs are not mapped.
- Hunyuan in `model/hunyuan` supports text, image, and audio, but file content
  is not converted today.
- Hugging Face in `model/huggingface` supports text and image in its converter.
  Audio and file inputs are rejected.

## Video Strategy

Video handling is split by layer:

- Telemetry layer:
  - represent video as `blob`, `uri`, or `file`
  - set `modality: video`
  - preserve `mime_type` and `filename` when available
- Model abstraction layer:
  - there is no first-class `ContentTypeVideo` yet
  - current short-term strategy is to stage video through file-like telemetry
    representations

When the shared model abstraction gains `ContentTypeVideo`, this page should be
updated with the exact field mapping and provider matrix changes.
