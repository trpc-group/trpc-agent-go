# Telemetry 多模态协议

## 概述

本文档说明 tracing 协议约束、框架到 OTel 的映射规则，以及 provider 能力边界。

## OTel 参考

- [OpenTelemetry GenAI 属性：`gen_ai.input.messages`](https://opentelemetry.io/docs/specs/semconv/registry/attributes/gen-ai/#gen-ai-input-messages)
- [OpenTelemetry GenAI input messages JSON schema](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-input-messages.json)
- [OpenTelemetry GenAI output messages JSON schema](https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-output-messages.json)
- [OpenTelemetry GenAI OpenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/openai/)

## Telemetry 协议约束

- `gen_ai.input.messages` 是 OTel 语义层 payload，不是 provider 原始请求体。
- `gen_ai.output.messages` 是 OTel 语义层输出，不保证与 provider SDK 的返回结构完全一致。
- `llm.request` 和 `llm.response` 仍然是更偏 provider 的快照，因此可能与
  OTel message telemetry 不同。

## OTel 消息结构

每条消息使用：

- `role`
- 可选 `name`
- 必选 `parts`

框架当前使用到的 part 形态：

- `text`：文本内容
- `blob`：内联二进制数据，`content` 里是 base64
- `uri`：外部引用，例如图片 URL
- `file`：文件 ID，例如 provider 已上传文件
- `tool_call`：模型发起的工具调用
- `tool_call_response`：工具返回结果
- `reasoning`：provider 的推理或思考内容

示例：

```json
[
  {
    "role": "user",
    "parts": [
      {
        "type": "text",
        "content": "这个文档里写了什么？"
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

## 框架映射规则

`model.Message` 和 `model.ContentPart` 的映射如下：

- `Message.Content` 映射为 `text` part
- `ContentParts[text]` 映射为 `text` part
- `ContentParts[image]`
  - URL 输入映射为 `uri`，`modality: image`
  - 内联字节映射为 `blob`，`modality: image`
  - `detail` 会作为附加属性保留
- `ContentParts[audio]` 映射为 `blob`，`modality: audio`
- `ContentParts[file]`
  - `file_id` 映射为 `file`
  - 内联字节映射为 `blob`
  - `modality` 会基于 MIME 推断，因此即使当前共享模型层还没有
    `video` content type，trace 里也能表达 image/audio/video 文件
- `Message.ToolCalls` 映射为 `tool_call` parts
- `role=tool` 的消息映射为 `tool_call_response` parts
- `Message.ReasoningContent` 映射为 `reasoning` part

## 能力边界

框架会在 telemetry 层统一表达多模态输入，但真正的 provider 支持能力仍取决于
各个 model adapter。

- `model/openai` 下的 OpenAI-compatible adapter 支持用户侧 text、image、audio、
  file 输入。system 和 assistant 的 `ContentParts` 实际上仍然主要是 text-only。
  支持工具调用。当前内置 OpenAI adapter 使用的是 Chat Completions，不是
  Responses。
- `model/gemini` 支持 text、image、audio，以及 inline file bytes。file-by-ID
  不是当前通用路径。
- `model/anthropic` 当前只保留 text parts，非文本 part 在 provider 请求前会被丢弃。
- `model/ollama` 支持 text 和 inline image bytes，不处理 audio/file。
- `model/hunyuan` 支持 text、image、audio，但 file 还没有接入转换。
- `model/huggingface` 当前 converter 支持 text 和 image，audio/file 会直接报不支持。

## Video 策略

video 的处理按层拆开：

- Telemetry 层：
  - 使用 `blob`、`uri` 或 `file` 表达 video
  - 设置 `modality: video`
  - 尽量保留 `mime_type` 和 `filename`
- 模型抽象层：
  - 当前还没有一等 `ContentTypeVideo`
  - 短期策略是把 video 先作为 file-like telemetry 表达

当共享模型抽象未来增加 `ContentTypeVideo` 时，需要同步更新本文档中的字段映射和
provider 能力边界。
