# TencentDB Agent Memory 集成（`memory/tencentdb`）

`memory/tencentdb` 通过独立 gateway sidecar 接入
[TencentDB Agent Memory](https://github.com/Tencent/TencentDB-Agent-Memory)。
它适合把 L0-L3 记忆流水线交给 TencentDB Agent Memory SDK，而
tRPC-Agent-Go 侧继续负责 Runner、Session、Plugin 和 Tool 生命周期的场景。

这个集成与内置后端的边界不同：

- TencentDB Agent Memory gateway 负责 capture、提取、存储、recall 和 search。
- Go adapter 通过 `session.Ingestor` 把每轮完成后的会话内容发送给 gateway。
- Runner plugin 在每次模型调用前请求 `/recall`，并把返回的上下文注入模型请求（需通过 `WithRecallEnabled(true)` 显式开启）。
- 通过 `tdai_conversation_search`（按 session 作用域，默认开启）和 `tdai_memory_search`（需 `WithMemorySearchTool(true)` 显式开启）暴露只读检索工具。
- 可选的短期上下文卸载 plugin 会把工具结果外置化、L1/L1.5/L2/L3、
  drill-down 和持久化委托给 TencentDB Agent Memory gateway hook API。
  Go adapter 不写本地 offload 文件。它与 recall 相互独立，默认关闭。

> **多租户提示**：自动 recall 和 `tdai_memory_search` 会读取 gateway 的共享长期
> 存储，而当前 gateway 并不会在这些路径上强制按 user/session 隔离，因此它们默认
> 关闭，只有在 gateway 能保证按租户隔离时才应开启。默认只开启按 session 作用域的
> capture 和 `tdai_conversation_search`。

即使 SDK 配置为本地 SQLite 存储，gateway 仍然是必需的，因为记忆引擎运行在
gateway/SDK 侧。直接访问 VectorDB 或 SQLite 只能访问存储层，不会执行 SDK 的
提取与召回流水线。

**适用场景**：sidecar 记忆引擎、本地或自托管存储、模型调用前自动召回，以及由外部 SDK 托管的记忆提取。

## 启动 TencentDB Agent Memory Gateway

[上游 package](https://github.com/Tencent/TencentDB-Agent-Memory/blob/main/package.json)
要求 Node.js 22.16.0 或更高版本。先克隆 SDK 仓库并启动 standalone gateway：

```bash
git clone https://github.com/Tencent/TencentDB-Agent-Memory.git
cd TencentDB-Agent-Memory
npm install

export TDAI_LLM_API_KEY="your-openai-compatible-api-key"
export TDAI_LLM_BASE_URL="https://api.openai.com/v1"
export TDAI_LLM_MODEL="deepseek-v4-flash"

node --import tsx src/gateway/server.ts
```

gateway 默认监听 `http://127.0.0.1:8420`。它会读取
`TDAI_LLM_API_KEY`、`TDAI_LLM_BASE_URL` 和 `TDAI_LLM_MODEL`，用于记忆提取、
场景/画像生成和召回。

## 配置示例

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memorytencentdb "trpc.group/trpc-go/trpc-agent-go/memory/tencentdb"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

gatewayURL := os.Getenv("TENCENTDB_AGENT_MEMORY_GATEWAY")
if gatewayURL == "" {
    gatewayURL = "http://127.0.0.1:8420"
}

memSvc, err := memorytencentdb.NewService(
    memorytencentdb.WithGatewayURL(gatewayURL),
    // 跨 session/user 的读取属于 opt-in，仅在 gateway 可信/隔离时开启。
    memorytencentdb.WithRecallEnabled(true),
    memorytencentdb.WithMemorySearchTool(true),
    // 可选短期上下文卸载，通过 gateway v2 API 完成。
    // memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    //     Enabled:   true,
    //     ServiceID: os.Getenv("TDAI_SERVICE_ID"),
    // }),
    // memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
)
if err != nil {
    panic(err)
}
defer memSvc.Close()

sessionSvc := sessioninmemory.NewSessionService()
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(memSvc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(memSvc),
    runner.WithPlugins(
        memSvc.Plugin(),
        // memSvc.ContextOffloadPlugin(),
    ),
)
defer r.Close()
```

**接入要点**：

- 通过 `llmagent.WithTools(memSvc.Tools())` 注册 TencentDB 原生检索工具。
- 通过 `runner.WithSessionIngestor(memSvc)` 把带时间戳的 session transcript 发送给 `/capture`。
- 通过 `runner.WithPlugins(memSvc.Plugin())` 在模型调用前启用自动 `/recall`。
- 只有在配置 `WithContextOffload(...)`（包括 `Enabled: true` 和
  `ServiceID`）且需要短期工具结果卸载时，才额外注册
  `runner.WithPlugins(memSvc.ContextOffloadPlugin())`。启用后，配套的
  `tdai_read_offload_ref` 工具会通过 `memSvc.Tools()` 暴露。
- 不要对该集成使用 `runner.WithMemoryService(...)`。

## 启用 Context Offload

context offload 是独立、显式开启的 TencentDB Agent Memory v2 能力。它会将
工具结果交给 gateway 异步处理，在上下文占用达到阈值时请求 gateway 压缩
model context，并提供一个工具用于有界恢复已归档的原始结果。

最小接入方式：

```go
memSvc, err := memorytencentdb.NewService(
    memorytencentdb.WithGatewayURL(gatewayURL),
    memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
    memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
        Enabled:   true,
        ServiceID: os.Getenv("TDAI_SERVICE_ID"),
    }),
)
if err != nil {
    panic(err)
}

agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(memSvc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(memSvc),
    runner.WithPlugins(memSvc.ContextOffloadPlugin()),
)
```

v2 API 要求同时提供 `Authorization: Bearer <key>` 和
`X-TDAI-Service-Id`。上游 standalone gateway 约定使用 `local` 和 `default`；
托管服务应使用对应 memory 实例分配的凭据。

如果 offload 流量需要使用与普通 capture/search/recall 不同的 gateway 或
API key，可以在 `ContextOffloadConfig` 中覆盖：

```go
memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    Enabled:    true,
    GatewayURL: offloadGatewayURL,
    APIKey:     os.Getenv("TDAI_OFFLOAD_API_KEY"),
    ServiceID:  os.Getenv("TDAI_SERVICE_ID"),
})
```

运行时行为：

- 工具执行后，`ContextOffloadPlugin()` 将真实的 tool call/result pair 发送到
  `POST /v2/offload/ingest`，同时携带最新 user prompt 和有界的近期对话上下文；
  不会立即改写本轮 tool result message。
- 每次模型调用前，plugin 会发送一个不含 tool pair 的 ingest 触发 L1.5
  任务判断；该请求仍会携带最新 user prompt 和有界的近期对话上下文。之后
  plugin 会估算 message tokens，并在达到 `CompactionRatio` 后调用
  `POST /v2/offload/compact`。失败时保留原始 model context。
- 除其他已启用的检索工具外，`memSvc.Tools()` 还会增加 offload 专用的
  `tdai_read_offload_ref`，底层调用 `POST /v2/offload/read-ref`，支持全文、
  关键词附近或行范围读取，并受 `max_tokens` 限制。
- adapter 将集成面严格限制在完成该生命周期所需的三个路由。存储、摘要、
  任务图和 offload 策略仍由 gateway 负责。

两种 ingest 最多都会发送过滤后的 10 条近期 user 或 assistant message，每条
截断为 400 个 Unicode code point。最新 user prompt 会单独发送并截断为
500 个 code point。tool message、带 tool call 的 assistant message、与当前
prompt 重复的 message，以及可识别的内部控制消息不会进入
`recent_messages`。

compact 是否触发由 adapter 在本地判断。context window 按以下顺序解析：
当前 run 的 `agent.WithModelContextWindow(...)`、model 的
`Info().ContextWindow`（provider 通常可通过 `WithContextWindow(...)` 等
option 设置）、通过 `model.RegisterModelContextWindow(...)` 为 model name
注册的值，最后兜底为 128,000 tokens。`TokenCounter` 的逐 message 计数既用于
本地 `CompactionRatio` 判断，也作为 compact request metadata；未配置时使用
简单 token 估算器，自定义 counter 报错或返回负数时，本轮 model call 也会回退
到简单估算器。

## 交互式示例

gateway 就绪后运行示例：

```bash
cd examples/memory/tencentdb
export OPENAI_API_KEY="your-openai-compatible-api-key"
export TENCENTDB_AGENT_MEMORY_GATEWAY="http://127.0.0.1:8420"
go run .
```

然后发送事实、flush 后新建会话，再提相关问题：

```text
You: 请记住以下信息：我的项目代号是 Apollo Lake，部署窗口是周五晚上，回答偏好是简洁。
You: /new
You: 我的项目代号、部署窗口和回答偏好是什么？
```

前两条消息会被 gateway capture。示例里的 `/new` 命令会先 flush 当前 gateway session，再切换到新 session；对话历史会重置，但同一个用户仍然可以通过 plugin 和原生检索工具召回已经提取的长期记忆。

## 配置选项

| 选项 | 作用 | 默认值 |
| ---- | ---- | ------ |
| `WithGatewayURL(url)` | TencentDB Agent Memory gateway URL。 | `http://127.0.0.1:8420` |
| `WithTimeout(d)` | gateway HTTP 客户端超时时间。 | `5s` |
| `WithIngestWorkers(n)` | 异步 capture worker 数量。 | `1` |
| `WithIngestQueueSize(n)` | 异步 capture 任务队列长度。 | `10` |
| `WithIngestJobTimeout(d)` | 队列中 capture 任务的超时时间。 | `30s` |
| `WithSessionKeyFunc(fn)` | 自定义 framework session 到 gateway `session_key` 的映射。 | `base64url(app):base64url(user):base64url(session)` |
| `WithAPIKey(key)` | 发送 `Authorization: Bearer <key>`（对应 gateway 的 `TDAI_GATEWAY_API_KEY`）。 | 无 |
| `WithRecallEnabled(bool)` | 是否启用自动 recall plugin（opt-in；读取共享存储）。 | `false` |
| `WithMemorySearchTool(bool)` | 是否暴露 `tdai_memory_search`（opt-in；读取共享存储）。 | `false` |
| `WithConversationSearchTool(bool)` | 是否暴露 `tdai_conversation_search`。 | `true` |
| `WithStandardAliases(bool)` | 是否额外暴露标准 `memory_search` 别名（需先启用 memory search）。 | `false` |
| `WithToolPrefix(prefix)` | 修改原生工具名前缀。 | `tdai` |
| `WithContextOffload(ContextOffloadConfig)` | 配置较大工具结果的显式短期上下文卸载。 | 关闭 |

`ContextOffloadConfig` 只控制 Go adapter 的 gateway 对接。offload 层级、
状态、存储、TTL 和隔离由 TencentDB Agent Memory gateway 负责。Go adapter
不暴露 local/backend offload 模式，也不会写本地 offload state。

| 字段 | 作用 | 默认值 |
| ---- | ---- | ------ |
| `Enabled` | 是否启用 context offload plugin 和 result-reference 工具。 | `false` |
| `GatewayURL` | v2 offload 调用的可选 gateway URL 覆盖。为空时复用 `WithGatewayURL`。 | 无 |
| `APIKey` | v2 offload 调用的可选 Bearer key 覆盖。为空时复用 `WithAPIKey`。 | 无 |
| `ServiceID` | 通过 `X-TDAI-Service-Id` 发送的 memory service ID；启用时必填。 | 无 |
| `CompactionRatio` | 触发 `/v2/offload/compact` 的上下文窗口占用率，取值 `(0, 2]`。 | `0.5` |
| `TokenCounter` | 同时用于本地 `CompactionRatio` 触发判断和 compact request metadata 的可选 `model.TokenCounter`；失败时回退到简单估算器。 | 简单估算器 |

## 注意事项

- adapter 会把 app、user、session 标识传给 gateway，但强多租户隔离依赖 gateway 和 SDK 端完整遵守这些字段；正因如此，自动 recall 和 `tdai_memory_search` 默认关闭，需显式 opt-in。
- 当 gateway 设置了 `TDAI_GATEWAY_API_KEY` 时，请用 `WithAPIKey(...)` 让请求携带 `Authorization: Bearer <key>`，否则除 `/health` 外的路由都会返回 401（health 仍可通过）。
- `tdai_memory_search` 检索已提取的长期记忆；提取是异步的，新捕获的信息可能需要短暂等待后才可检索。
- `tdai_conversation_search` 检索对话历史，默认使用当前 gateway `session_key`。
- context offload 是显式开启、由 gateway 承载的能力，只调用
  `/v2/offload/ingest`、`/v2/offload/compact` 和
  `/v2/offload/read-ref`，不调用 `/capture` 或 `/recall`。
- gateway 可能将归档后的工具结果替换为“原始工具结果已存档，如需查看完整内容
  请调用 Offload V2 result_ref 恢复接口”这类提示。请注册
  `memSvc.Tools()`，让模型能够通过 `tdai_read_offload_ref` 完成恢复。
- adapter 不会创建本地 refs、JSONL 索引、Mermaid 文件或本地 offload state。
  scope 校验、存储 ACL、token 限制和持久化都由 gateway 负责。
- 使用完成后请调用 `Close()`，确保后台 capture worker 干净退出。
