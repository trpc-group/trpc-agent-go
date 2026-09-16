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

Langfuse exporter 把消息折叠进 `langfuse.observation.input` / `output`，读取顺序：

1. `gen_ai.input.messages.otel` / `gen_ai.output.messages.otel`（GenAI `role` + `parts`，原样上报，由 Langfuse 转换）
2. 若 `.otel` 缺失：legacy `gen_ai.input.messages` / `gen_ai.output.messages`（原样上报；output 更接近对话形态，优先于 `llm_response`）
3. 仅 chat/generation span：`trpc.go.agent.llm_request` / `llm_response`

Jaeger 等通用 OTLP 后端仍读取原始 span attribute，Drop 策略与 Langfuse 不同，见 [可观测性](observability.md) 中的 Span Attribute 策略。
