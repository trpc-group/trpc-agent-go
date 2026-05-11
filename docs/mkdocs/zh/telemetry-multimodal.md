# 多模态遥测消息

## 消息属性

tRPC-Agent-Go 会上报两组消息属性：

- `gen_ai.input.messages` 和 `gen_ai.output.messages` 是废弃的兼容字段。它们的 payload 形状保持不变，用于兼容已有遥测消费方。
- `gen_ai.input.messages.otel` 和 `gen_ai.output.messages.otel` 是推荐给新接入方使用的字段。它们使用 OpenTelemetry 对齐的 `role` + `parts` 结构。

本次改动不会移除旧字段，但新的适配器应优先读取 `.otel` 字段。

## OTel Payload 结构

`gen_ai.input.messages.otel` 是消息数组：

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

`gen_ai.output.messages.otel` 是输出消息数组：

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

支持的 part 类型包括：

- `text`：文本内容。
- `uri`：基于 URI 的多模态内容，包含 `modality` 和可选 `mime_type`。
- `blob`：base64 编码的二进制内容，包含 `modality` 和可选 `mime_type`。
- `file`：通过 `file_id` 引用的已上传文件。
- `tool_call`：assistant 发起的工具调用请求。
- `tool_call_response`：发送回模型的工具调用结果。
- `reasoning`：模型供应商返回的 reasoning 或 thinking 内容。

## Langfuse 转换

Langfuse exporter 只会把 `gen_ai.input.messages.otel` 和 `gen_ai.output.messages.otel` 转换为最终展示的 input/output 内容。废弃兼容字段仍保留在原始 span 上，供其他消费方读取，但 Langfuse 不再解析它们。

这样既保留了原始兼容属性，也让 OTel payload 成为 Langfuse 唯一的转换路径。
