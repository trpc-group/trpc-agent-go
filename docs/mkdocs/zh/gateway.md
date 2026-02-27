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

本仓库提供了一个 OpenClaw-like 的 demo binary，用它就可以直接跑起来 Gateway：
[openclaw](https://github.com/trpc-group/trpc-agent-go/tree/main/openclaw)。

先用 mock 模型跑起来（不需要任何模型密钥）：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080
```

健康检查：

```bash
curl -sS 'http://127.0.0.1:8080/healthz'
```

通过 HTTP 触发一次消息（webhook 风格）：

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Hello"}'
```

同一个 binary 也可以开启 Telegram long polling（配置步骤见
`openclaw/README.md`）。

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

如果你需要自定义策略，可以在请求里显式传入 `session_id` 覆盖默认值。

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

只允许指定用户访问（openclaw demo）：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -allow-users "123456789,987654321"
```

开启后，其他用户会收到 `403 Forbidden`（HTTP）或被 Telegram 丢弃。

### 2）线程/群消息的 mention gating

忽略群消息，只有被提及时才触发（openclaw demo）：

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -require-mention \
  -mention "@mybot,/agent"
```

开启后，只有当消息 `text` 包含任一 mention pattern 时，且 `thread` 字段不为空，
该消息才会被处理。

## 与 OpenClaw Gateway protocol 的关系

OpenClaw 文档中的 “Gateway protocol” 指的是 **WebSocket 控制面协议**（包含
device / role / approvals / pairing 等控制能力），它并不是“把 HTTP JSON 字段改成
一样”就能直接兼容的东西。

本仓库的 Gateway HTTP API 更偏向 **数据面最小入口**：把一条入站消息稳定地映射成
一次 `runner.Run()`，并提供基础的运行控制（status + cancel）。

如果未来确实需要兼容 OpenClaw 的客户端生态，更合理的做法是：

- 在 `openclaw/` demo binary 里新增一个 WS 控制面（或协议适配层）。
- 保持 HTTP `/v1/gateway/*` 作为稳定的最小数据面接口，避免把它演进成大而全。

## Status 与 cancel

Gateway 提供：

- `GET /v1/gateway/status?request_id=...`
- `POST /v1/gateway/cancel`，请求体 `{"request_id":"..."}`

注意：如果你希望在 run 还没结束时就能 status/cancel，需要在 `/messages` 请求中
自己设置 `request_id`，否则你在运行中无法提前知道 request_id。
