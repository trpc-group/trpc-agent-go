# TencentDB Agent Memory 集成（`memory/tencentdb`）

`memory/tencentdb` 通过 gateway 接入
[TencentDB Agent Memory](https://github.com/TencentCloud/TencentDB-Agent-Memory)。
它适合把 L0-L3 记忆流水线交给 TencentDB Agent Memory SDK，而
tRPC-Agent-Go 侧继续负责 Runner、Session、Plugin 和 Tool 生命周期的场景。

这个集成与内置后端的边界不同：

- TencentDB Agent Memory gateway 负责 capture、提取、存储、recall 和 search。
- Go adapter 通过 `session.Ingestor` 把每轮完成后的会话内容发送给 gateway。
- Runner plugin 在每次模型调用前执行 recall，并把返回的上下文注入模型请求（需
  通过 `WithRecallEnabled(true)` 显式开启）。Legacy 模式请求 `/recall`；
  V3 会组合 L1 atomic search、L2 scene navigation 和 L3 core read。
- 通过 `tdai_conversation_search`（按 session 作用域，默认开启）和
  `tdai_memory_search`（需 `WithMemorySearchTool(true)` 显式开启）暴露只读检索工具。
  V3 接入还会提供 `tdai_read_scenario`，按 scene navigation 返回的路径读取有界的
  L2 内容。
- 可选的短期上下文卸载 plugin 会把工具结果外置化、L1/L1.5/L2/L3、
  drill-down 和持久化委托给 TencentDB Agent Memory gateway hook API。
  Go adapter 不写本地 offload 文件。它与 recall 相互独立，默认关闭。

> **多租户提示**：Legacy 自动 recall 和 `tdai_memory_search` 可能读取未按
> user/session 隔离的共享长期存储。V3 的 L0/L1 按 Service、Team、Agent、User
> 隔离，L2/L3 则在相同 Service、Team、Agent 下跨 User 和 Session 共享。Recall
> 和 memory search 仍默认关闭，以保持现有默认行为。`AppName` 和
> `WithSessionKeyFunc` 不是 V3 隔离字段；需要隔离不同应用时，应为其分配不同的
> Service、Team 或 Agent identity。

即使 SDK 配置为本地 SQLite 存储，gateway 仍然是必需的，因为记忆引擎运行在
gateway/SDK 侧。直接访问 VectorDB 或 SQLite 只能访问存储层，不会执行 SDK 的
提取与召回流水线。

**适用场景**：gateway 记忆引擎、云端或自托管存储、模型调用前自动召回，以及由外部 SDK 托管的记忆提取。

## 启动 TencentDB Agent Memory Gateway

[上游 package](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/feat/server_team/MemoryCore/package.json)
要求 Node.js 22.16.0 或更高版本。先克隆 SDK 仓库并启动 standalone gateway：

```bash
git clone --branch feat/server_team --single-branch https://github.com/TencentCloud/TencentDB-Agent-Memory.git
cd TencentDB-Agent-Memory/MemoryCore
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

identity := memorytencentdb.NewServiceIdentity(
    os.Getenv("TDAI_SERVICE_ID"),
    os.Getenv("TDAI_TEAM_ID"),
    os.Getenv("TDAI_AGENT_ID"),
)
memSvc, err := memorytencentdb.NewServiceWithIdentity(
    identity,
    memorytencentdb.WithGatewayURL(gatewayURL),
    // 新接入的云端版和自建版都推荐启用身份隔离数据面；三个 ID 必填。
    // 只有 gateway 启用共享密钥鉴权时才需要 API key；未配置时，adapter
    // 会自动发送自建 gateway 解析器要求的非敏感 Bearer 占位值。
    // memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
    // Recall/search 保持 opt-in。
    memorytencentdb.WithRecallEnabled(true),
    memorytencentdb.WithMemorySearchTool(true),
    // 可选短期上下文卸载，通过 gateway v2 API 完成。
    // memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    //     Enabled:   true,
    //     ServiceID: os.Getenv("TDAI_OFFLOAD_SERVICE_ID"),
    // }),
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
- 通过 `runner.WithSessionIngestor(memSvc)` 把 session transcript 发送给
  Legacy `/capture` 或 V3 `/v3/conversation/add`；V3 会保留事件时间戳，并按
  顺序发送每批最多 100 条消息的 conversation-add 请求。
- 通过 `runner.WithPlugins(memSvc.Plugin())` 在模型调用前启用自动 recall；
  Legacy 请求 `/recall`，V3 组合读取 L1/L2/L3。
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
        ServiceID: os.Getenv("TDAI_OFFLOAD_SERVICE_ID"),
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
    ServiceID:  os.Getenv("TDAI_OFFLOAD_SERVICE_ID"),
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
go run . -turn-wait 10s
```

发送事实后，等程序重新显示下一个 `You:` 提示符，再输入 `/new`。配置的等待
发生在本轮完成之后、程序接受下一条命令之前：

```text
You: 请记住以下信息：我的项目代号是 Apollo Lake，部署窗口是周五晚上，回答偏好是简洁。
Waiting 10s to allow asynchronous gateway extraction...
You: /new
You: 我的项目代号、部署窗口和回答偏好是什么？
```

`-turn-wait` 只是为 gateway 的异步长期记忆提取预留一段固定时间，并不代表
服务端已经就绪；如果当前部署需要更久，应增加等待时间，或通过 gateway 的
观测能力确认提取完成。`/new` 只会等待本地待处理的 capture，再切换到新
session。Legacy gateway 还会收到 `/session/end`；V3 没有对应的远端结束或
提取屏障接口。V3 conversation search 只查询新 session，因此跨 session 召回
依赖上一轮已经生成可检索的长期记忆。

## 配置选项

`NewService` 使用 Legacy Gateway API。接入云端版和自建版共用的 V3
数据面时，先通过 `NewServiceIdentity(serviceID, teamID, agentID)` 创建
`ServiceIdentity`，再调用 `NewServiceWithIdentity(identity, opts...)`；三个
ID 均为必填项。

| 选项 | 作用 | 默认值 |
| ---- | ---- | ------ |
| `WithGatewayURL(url)` | TencentDB Agent Memory gateway URL。 | `http://127.0.0.1:8420` |
| `WithTimeout(d)` | gateway HTTP 客户端超时时间。 | `5s` |
| `WithIngestWorkers(n)` | 异步 capture worker 数量。 | `1` |
| `WithIngestQueueSize(n)` | 异步 capture 任务队列长度。 | `10` |
| `WithIngestJobTimeout(d)` | 队列中 capture 任务的超时时间。 | `30s` |
| `WithSessionKeyFunc(fn)` | 自定义 framework session 到 gateway `session_key` 的映射。 | `base64url(app):base64url(user):base64url(session)` |
| `WithAPIKey(key)` | 发送 `Authorization: Bearer <key>`（对应 gateway 的 `TDAI_GATEWAY_API_KEY`）。 | 无 |
| `WithRecallEnabled(bool)` | 是否启用自动 recall；Legacy 可能读取共享存储，V3 的 L1 按 User、L2/L3 按 Team/Agent。 | `false` |
| `WithMemorySearchTool(bool)` | 是否暴露 `tdai_memory_search`；Legacy 可能读取共享存储，V3 的 L1 按 User。 | `false` |
| `WithConversationSearchTool(bool)` | 是否暴露 `tdai_conversation_search`。 | `true` |
| `WithStandardAliases(bool)` | 是否额外暴露标准 `memory_search` 别名（需先启用 memory search）。 | `false` |
| `WithToolPrefix(prefix)` | 修改原生工具名前缀。 | `tdai` |
| `WithContextOffload(ContextOffloadConfig)` | 配置较大工具结果的显式短期上下文卸载。 | 关闭 |

新接入推荐使用 `NewServiceWithIdentity`。不透明的 `ServiceIdentity` 将 V3
身份配置与保持 Legacy 兼容的 `Options` 分开。V3 客户端把 `service_id`
放入 `X-TDAI-Service-Id`，并从当前 framework session 派生 `user_id` 和
`session_id`，随后调用身份隔离的数据面接口。`NewService` 继续沿用
Legacy `/capture`、`/recall` 和 `/search/*`，现有用户行为不变。构造函数
描述的是接口语义，云端版和自建版使用相同的接入方式。L0/L1 按
Service、Team、Agent、User 隔离；L2/L3 在相同 Service、Team、Agent
下跨 User 和 Session 共享。adapter 暂不发送可选的 TencentDB `task_id`。
自建 gateway 未开启鉴权时可以省略 `WithAPIKey`；开启鉴权的自建 gateway
和云端服务仍需提供 API key。

启用 V3 后，`memSvc.Tools()` 会包含 `tdai_read_scenario`，用于读取 scene
navigation 选中的 L2 文件。L1/L2/L3 自动 recall 的完整载荷最多为 24 KiB，
其中 L1 atomic 和 L3 core 各自最多占 8 KiB；scene navigation 最多注入
100 个非空路径和 8 KiB 文本。Recall 某一段被截断时会保留闭合标签，并以
`...[truncated]` 标记省略内容。场景文件最多返回 16 KiB，截断时也会包含该标记，
且 `truncated` 为 `true`。

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

- Legacy 模式会把 app、user、session 编码进 `session_key`，但这不是强租户边界；
  V3 会发送配置的 Service、Team、Agent 以及 framework User、Session ID。自动
  recall 和 `tdai_memory_search` 仍保持 opt-in，以维持现有默认行为。
- V3 请求始终携带非空 Bearer header。配置 `WithAPIKey(...)` 时发送真实
  key；未配置时，adapter 会发送自建 gateway 解析器要求的非敏感 `local`
  占位值。该占位值只适用于关闭共享密钥鉴权的部署。如果 gateway 设置了
  `TDAI_GATEWAY_API_KEY`，必须配置匹配的 key，否则除 `/health` 外的路由
  都会返回 401（health 仍可通过）。
- `tdai_memory_search` 检索已提取的长期记忆；提取是异步的，新捕获的信息可能需要短暂等待后才可检索。
- `tdai_conversation_search` 检索当前 Legacy `session_key` 或 V3 `session_id`
  范围内的对话历史。
- capture 采用 at-least-once 语义，只有完整 capture 得到确认后才推进
  checkpoint。结果不确定的 gateway 失败可能导致已接收的 L0 消息被重放，
  因为当前 V3 API 尚未提供客户端写入幂等键。
- V3 capture 会将超过 gateway 8192 字符上限的单条 L0 消息截断，并追加
  `...[truncated]`；后续消息仍会继续写入，收到有界请求的完整确认后即可
  推进 checkpoint。V3 检索 query 同样会限制在 gateway 的 2048 字符上限内。
- context offload 是显式开启、由 gateway 承载的能力，只调用
  `/v2/offload/ingest`、`/v2/offload/compact` 和
  `/v2/offload/read-ref`，不调用 `/capture` 或 `/recall`。
- gateway 可能将归档后的工具结果替换为“原始工具结果已存档，如需查看完整内容
  请调用 Offload V2 result_ref 恢复接口”这类提示。请注册
  `memSvc.Tools()`，让模型能够通过 `tdai_read_offload_ref` 完成恢复。
- adapter 不会创建本地 refs、JSONL 索引、Mermaid 文件或本地 offload state。
  scope 校验、存储 ACL、token 限制和持久化都由 gateway 负责。
- 使用完成后请调用 `Close()`，确保后台 capture worker 干净退出。
