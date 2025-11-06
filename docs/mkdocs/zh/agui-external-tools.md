# AG-UI 外部工具调用设计方案

本文面向工程设计，描述如何在 AG-UI 协议下支持“前端执行外部工具”的两阶段交互流程，并对事件对齐、后端行为、可选增强与注意事项给出统一约定与落地建议。

## 背景与目标

- 背景：部分工具（如改变页面背景色）应在前端执行以降低权限风险与提升体验，后端仅负责触发与编排。
- 目标：
  - 第一次请求用于发现并下发工具调用指令，返回 RunFinished 让出控制权给前端。
  - 第二次请求仅回传前端工具执行结果，不含新的用户提示词，由 Agent 基于会话继续推理与总结。

## 总体流程（两阶段）

1) 第一次请求（发现与指令）。
- 前端以用户消息发起请求到 AG-UI 服务（SSE）。
- 模型生成带 `tool_calls` 的响应，Translator 输出 `ToolCallStart`/`ToolCallArgs`。
- 后端本地可注册同名工具，但仅用于“触发信号”，在 `Call` 中返回 `agent.NewStopError(..., agent.WithStopReason(agent.StopReasonExternalTool))` 以中止本轮，让 Runner 结束并发出 `RunFinished`（同时避免被当作错误事件）。
- 前端据 `ToolCallArgs` 在本地执行真实工具（如修改背景色）。

2) 第二次请求（结果回传与继续）。
- 前端仅发送一条 `role=tool` 消息承载工具执行结果，不包含新的用户提示词。
- Agent 读取会话中的工具结果，继续推理或给出总结性回答。

## 首次请求：事件时序（必须包含 ToolCallEnd）

- 规范序列：`RunStarted → ToolCallStart → ToolCallArgs → ToolCallEnd → RunFinished`。
- 不发送 `ToolCallResult`：StopError 路径不产生 `tool.response`，因此不会有 `ToolCallResult`。

实现要求（Translator）。
- 在 Translator 维护 `openToolCallIDs` 集合：收到 `ToolCallStart` 加入，收到 `tool.response` 则移除。
- 当遇到 Runner 完成事件，需为仍在集合中的每个 `tool_id` 追加一个 `ToolCallEnd`，然后再发 `RunFinished`（强制补齐 ToolCallEnd）。
- 即使工具被标记为长耗时，本规范仍要求在首次请求结束前补发 `ToolCallEnd`，以统一前端 UI 状态收敛。

框架改动清单（首轮）。
- `server/agui/translator/translator.go`：新增 `openToolCallIDs` 状态，处理首轮补发 `ToolCallEnd` 并确保 `StopReasonExternalTool` 路径不会生成错误事件。
- `server/agui/runner/runner.go`：当最后一条消息为 tool 时放宽校验，并将其交给底层 runner 处理。
- `runner/runner.go`：支持 `message.Role == model.RoleTool`，把第二轮的工具结果写入 session（仅更新会话，不触发新的工具生命周期事件），随后照常调用 LLM。

## 第二次请求：仅 tool 消息

请求示例（SSE POST `/agui`）。

```json
{
  "threadId": "sess-1",
  "runId": "run-2",
  "messages": [
    { "role": "tool", "tool_id": "tool-abc", "tool_name": "change_background", "content": "{\"status\":\"ok\"}" }
  ],
  "state": {},
  "forwardedProps": {}
}
```

Runner 对齐（需要框架支持）。
- 现状：AG-UI Runner 仅将“最后一条用户消息”作为输入调用底层 Runner，tool 消息既不会写入会话，也无法触发后续推理。
- 必要修改：
  - `runner.Run` 在检测到 `message.Role == model.RoleTool` 时，直接把该消息转换为 `tool.response` 事件并写入 session（即使会话已有历史）。该事件仅用于更新会话，不再通过 Translator 生成额外的工具生命周期事件，避免出现“无 ToolCallStart 的 tool.response”。
  - 写入动作完成后，Runner 按既有流程立即进入下一轮 LLM 调用；无需伪造新的 user 消息，现有 Flow 会基于 session 中更新后的历史重新构建请求并推动模型继续推理。
- AG-UI Runner 调整：
  - 放宽“最后一条必须是 user”的校验，允许仅 tool 消息的第二次请求，直接把该消息传给 `runner.Run`，由 runner 负责写入会话。
- 成果：模型能在会话中读取工具结果继续推理，同时保持工具生命周期在第一轮完整闭合（ToolCallEnd 即收尾）。

## 注意事项

- 工具生命周期：第二次请求不会重新广播新的 `tool.response` 事件，生命周期仍在第一轮以 `ToolCallEnd` 收束，工具结果仅以会话事件的方式存在。
- 幂等控制：若前端可能重试第二次请求，应以 `tool_id` 去重，runner 不做重复过滤。
- 会话一致性：确保第二次请求提交的 `tool_id`、`tool_name` 与首次工具调用一致。
- 安全与校验：前端需要验证工具参数与回传 payload，避免恶意注入或越权。

## 参考路径

- 事件结构：`event/event.go`，`model/response.go`。
- AG-UI 服务：`server/agui/service/sse/sse.go`。
- AG-UI Runner：`server/agui/runner/runner.go`。
- 事件翻译：`server/agui/translator/translator.go`。
- 函数调用处理：`internal/flow/processor/functioncall.go`，`internal/flow/llmflow/llmflow.go`。
- Runner 完成事件：`runner/runner.go`。
- 示例代码：`examples/agui/externaltool/server`、`examples/agui/externaltool/client`。
