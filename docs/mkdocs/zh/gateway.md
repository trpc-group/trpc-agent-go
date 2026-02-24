# Gateway 服务

**Gateway 服务**是在 `runner.Run()` 之上提供的一层轻量 HTTP 能力，用来帮你把
Agent 做成类似 OpenClaw 的“常驻助手”形态：

- 消息来自外部入口（HTTP webhook、IM 桥接等）。
- Gateway 将每条消息转换为一次 `runner.Run()`。
- Gateway 为每个对话生成稳定的 `session_id`（支持多轮对话）。
- Gateway 确保 **同一 session 同时只运行一个任务**（避免上下文与状态冲突）。

当你想把 Agent 放到真实使用场景里长期运行时（而不是只写一个单次调用的 demo），
这一层会非常实用：多轮会话、安全默认值、以及基础的运行控制（status + cancel）。

## 快速上手

Gateway 只需要一个 `runner.Runner` 即可启动：

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/gateway"
)

ag := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New(
        "deepseek-chat",
        openai.WithVariant(openai.VariantDeepSeek),
    )),
)

r := runner.NewRunner("gateway-demo", ag)
srv, _ := gateway.New(r)

_ = http.ListenAndServe(":8080", srv.Handler())
```

完整可运行示例见
[examples/gateway](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/gateway)。

一个更接近 OpenClaw 形态的 demo binary（Telegram long polling + HTTP gateway）
见
[openclaw](https://github.com/trpc-group/trpc-agent-go/tree/main/openclaw)。

注意：对于 DeepSeek，`model/openai` 在你设置
`openai.WithVariant(openai.VariantDeepSeek)` 后，会读取 `DEEPSEEK_API_KEY`，
并默认使用 `https://api.deepseek.com` 作为 base URL。

## 接口

默认路由：

- `POST /v1/gateway/messages`
- `GET  /v1/gateway/status?request_id=...`
- `POST /v1/gateway/cancel`
- `GET  /healthz`

### POST /v1/gateway/messages

发送一条入站消息。

最小请求 JSON：

```json
{
  "from": "alice",
  "text": "Hello!"
}
```

你可以使用的字段：

- `channel`（string）：可选，渠道名，默认 `"http"`。
- `from`（string）：发送者标识，同时也会作为 `user_id` 的默认值。
- `thread`（string）：可选，线程/群标识。
- `text`（string）：用户文本内容。
- `user_id`（string）：可选，显式指定用于 allowlist 的用户 ID。
- `session_id`（string）：可选，覆盖默认 session id。
- `request_id`（string）：可选，覆盖默认 request id（如果你需要在运行中
  轮询 status 或 cancel，建议你自己设置这个字段）。

响应 JSON：

```json
{
  "session_id": "http:dm:alice",
  "request_id": "req-1",
  "reply": "..."
}
```

如果开启了 mention gating 且该消息被忽略，响应包含：

```json
{"ignored": true}
```

## Session ID 生成规则

默认情况下，Gateway 会从入站消息自动推导 `session_id`：

- 私聊（没有 `thread`）：`<channel>:dm:<from>`
- 群聊/线程（有 `thread`）：`<channel>:thread:<thread>`

这样你不需要自己生成或存储 session id，也能获得稳定的多轮对话体验。

如果你需要自定义策略，可以使用 `gateway.WithSessionIDFunc`。

## 同一 session 串行（避免并发）

对于同一个 `session_id`，Gateway 保证同时只会有一个 run 在执行。

如果同一个 `session_id` 的两条请求并发到达，第二条会等待第一条执行完成。
这样可以保证：

- 对话历史稳定，不会被并发打乱。
- 工具调用更安全（不会并发写同一份 session 状态）。

不同 session 之间仍然可以并行执行。

## 安全默认值

外部消息输入属于不可信输入。Gateway 提供两个常见的安全控制：

### 1）用户 allowlist

只允许指定用户访问：

```go
srv, _ := gateway.New(r,
    gateway.WithAllowUsers("alice", "bob"),
)
```

开启后，其他用户会收到 `403 Forbidden`。

### 2）线程/群消息的 mention gating

忽略群消息，只有被提及时才触发：

```go
srv, _ := gateway.New(r,
    gateway.WithRequireMentionInThreads(true),
    gateway.WithMentionPatterns("@bot"),
)
```

注意：如果开启了 `WithRequireMentionInThreads(true)`，必须同时至少配置一个
`WithMentionPatterns`，否则 `gateway.New` 会返回错误。

开启后，只有当消息 `text` 包含任一 mention pattern 时，且 `thread` 字段不为空，
该消息才会被处理。

## Status 与 cancel

Gateway 提供：

- `GET /v1/gateway/status?request_id=...`
- `POST /v1/gateway/cancel`，请求体 `{"request_id":"..."}`

注意：如果你希望在 run 还没结束时就能 status/cancel，需要在 `/messages` 请求中
自己设置 `request_id`，否则你在运行中无法提前知道 request_id。
