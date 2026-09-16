# 外部长时记忆平台集成（`mem0`）

`memory/mem0` 是当前对 [mem0](https://mem0.ai) 的集成实现，适合把长期记忆的提取与存储交给外部托管平台，同时仍然让 Agent 通过标准记忆工具查询结果。

它与上文介绍的内置 Memory 后端不同：`memory/mem0` **不是** 完整的
`memory.Service` 实现，而是采用 ingest-first 模式。每轮结束后，Runner 把当前
Session 交给 ingestor；ingestor 只选择尚未摄取且非空的 user/assistant 增量并
转发给 mem0。mem0 在平台侧完成提取，Agent 再通过只读工具读取结果。

**适用场景**：外部长时记忆平台、每轮响应后的后台提取，以及不需要本地 CRUD 写路径的场景。

## 配置示例

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

mem0Svc, err := memorymem0.NewService(
    memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
    memorymem0.WithLoadToolEnabled(true),
)
if err != nil {
    panic(err)
}
defer mem0Svc.Close()

sessionSvc := sessioninmemory.NewSessionService()
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(mem0Svc.Tools()),
    llmagent.WithPreloadMemory(10), // 可选：只读预加载预算。
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc),
)
defer r.Close()
```

**接入要点**：

- 通过 `llmagent.WithTools(mem0Svc.Tools())` 注册工具
- 通过 `runner.WithSessionIngestor(mem0Svc)` 把 Session 交给 ingestor；它只把此前
  尚未摄取的消息增量发送给 mem0
- 可选启用 `llmagent.WithPreloadMemory(N)`；由于 `mem0Svc` 同时实现 `memory.Reader`，Runner 可以用它执行只读预加载
- 不要对该集成使用 `runner.WithMemoryService(...)`

## 为什么用 `WithSessionIngestor(...)`，而不是 `WithMemoryService(...)`

`runner.WithMemoryService(...)` 面向的是实现完整 `memory.Service` 契约的内置 Memory 后端。这个契约除了读接口，还包括 `AddMemory`、`UpdateMemory`、`DeleteMemory`、`ClearMemories`、`EnqueueAutoMemoryJob(...)` 等由框架直接拥有语义的写入与自动提取能力。

`memory/mem0` 的边界不同。它并不把完整的 CRUD 生命周期暴露给框架，而是接收
当前 Session，选择此前尚未摄取且非空的增量消息，转交给 mem0 做托管提取，
然后再通过只读工具把检索能力暴露给 Agent。

使用 `runner.WithSessionIngestor(...)` 可以更准确地表达这层边界：

- Runner 在每轮结束后传入当前 Session；ingestor 只发送晚于 session watermark
  的 user/assistant 消息
- 记忆提取与存储由 mem0 在服务端完成
- `metadata`、`agent_id`、`run_id` 这类按请求传递的 ingest 字段，可以通过 `session.IngestOption` 透传
- 不会把该集成误解成支持完整框架侧 CRUD 的内置后端；显式启用的框架侧 preload 只使用只读 `memory.Reader` 能力

ingestor 会在每个 session 的锁内预留所选 watermark，再把任务放入队列，因此
并发调用不会重复提交同一段增量。异步提交失败后不会自动从 Session 重放，运维
层面的重试需要在此适配层之外处理。

简单说，`MemoryService` 表示“框架直接管理记忆”，而 `SessionIngestor` 表示
“框架把 Session 交给外部记忆适配层”。`mem0` 属于后者。

## 配置选项

| 选项 | 作用 | 默认值 |
| ---- | ---- | ------ |
| `WithAPIKey(key)` | mem0 API Key；托管平台必填，本地 OSS 且关闭鉴权时可为空。 | 必填 |
| `WithHost(url)` | 覆盖 mem0 API Host / Base URL。 | `https://api.mem0.ai` |
| `WithSelfHostedOSS()` | 使用本地 Mem0 OSS REST API（`/memories`、`/search`、`X-API-Key`）。开启后如果没有设置 `WithHost`，host 默认 `http://localhost:8888`；OSS 模式会拒绝托管平台默认 host。 | 关闭 |
| `WithSelfHostedOSSIncludeUnscopedMemories()` | 包含没有 `metadata.trpc_app_name` 的历史 OSS 记录；已标记为其他 app 的记录仍会隐藏。该选项会弱化 app 隔离，只应在受控迁移期间启用。 | 关闭 |
| `WithSelfHostedIngestPrompt(prompt)` | 为该 service 的所有本地 Mem0 写入设置提取 prompt。 | 服务端默认值 |
| `WithSelfHostedIngestExpirationDateResolver(resolver)` | 为每次本地 Mem0 写入独立解析 `expiration_date`。 | 不发送 |
| `WithIngestInference(bool)` | 控制 Mem0 是否从 transcript 中提取记忆；同时适用于托管和本地写入。 | `true` |
| `WithSelfHostedProceduralMemory()` | 创建本地 procedural memory；必须提供 `agent_id`。 | 关闭 |
| `WithOrgProject(orgID, projectID)` | 追加托管平台的 `org_id` / `project_id`；本地 OSS 不支持。 | 空 |
| `WithAsyncMode(bool)` | 控制托管平台 ingest 请求里的 `async_mode`；本地 OSS 在 REST 层同步写入。 | `true` |
| `WithVersion(v)` | 设置托管平台 mem0 ingest 请求里的版本字段。 | `v2` |
| `WithTimeout(d)` | HTTP 客户端超时时间。 | `10s` |
| `WithLoadToolEnabled(bool)` | 是否在 `Tools()` 里暴露 `memory_load`。 | `false` |
| `WithAsyncMemoryNum(n)` | 后台 ingest worker 数量。 | `1` |
| `WithMemoryQueueSize(n)` | 每个 worker 的队列长度。 | `10` |
| `WithMemoryJobTimeout(d)` | 队列任务与同步 fallback ingest 的超时时间。 | `30s` |

## 本地 OSS 请求字段

标准 Runner 路径会把 session ID 作为 `run_id`、当前 Agent 名称作为
`agent_id`。Mem0 专属行为在创建 service 时统一配置，因此
`IngestSession` 仍是唯一的写入 API：

| Mem0 OSS create 字段 | 来源 |
| -------------------- | ---- |
| `messages` | ingestor 从 session 中选出的非空增量。 |
| `user_id` | `session.Session.UserID`。 |
| `agent_id` | `session.WithIngestAgentID`；Runner 自动提供当前 Agent 名称。 |
| `run_id` | `session.WithIngestRunID`；Runner 自动提供 session ID。 |
| `metadata` | `session.WithIngestMetadata`，以及适配层内部追加的 tRPC app scope。 |
| `prompt` | `WithSelfHostedIngestPrompt`。 |
| `expiration_date` | `WithSelfHostedIngestExpirationDateResolver`。 |
| `infer` | `WithIngestInference`，默认为 `true`。 |
| `memory_type` | `WithSelfHostedProceduralMemory`；普通记忆不发送该字段。 |

```go
package example

import (
    "context"
    "time"

    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

func newProceduralMemoryService() (*memorymem0.Service, error) {
    expirationForSession := func(
        _ context.Context,
        sess *session.Session,
    ) (time.Time, error) {
        if sess.CreatedAt.IsZero() {
            return time.Time{}, nil
        }
        return sess.CreatedAt.AddDate(0, 0, 30), nil
    }

    return memorymem0.NewService(
        memorymem0.WithSelfHostedOSS(),
        memorymem0.WithHost("http://localhost:8888"),
        memorymem0.WithSelfHostedIngestPrompt("提取可复用的部署流程。"),
        memorymem0.WithSelfHostedIngestExpirationDateResolver(
            expirationForSession,
        ),
        memorymem0.WithSelfHostedProceduralMemory(),
    )
}

func newRawMemoryService() (*memorymem0.Service, error) {
    return memorymem0.NewService(
        memorymem0.WithSelfHostedOSS(),
        memorymem0.WithHost("http://localhost:8888"),
        memorymem0.WithIngestInference(false),
    )
}
```

`newRawMemoryService` 跳过 LLM 提取，保存适配层规范化后的非 system 消息文本。
它特意使用一个不包含自定义 prompt 和 procedural memory 的独立 service。
Mem0 仍会调用 embedding 模型来持久化和检索这些原始记忆。

- `session.WithIngestMetadata`、`session.WithIngestAgentID` 与
  `session.WithIngestRunID` 仍用于设置单次 `IngestSession` 的通用字段；
  Runner 会自动提供 agent ID 和 run ID。
- `WithSelfHostedIngestPrompt` 在每次本地 create 请求中透传该 service 的提取
  prompt；该选项要求开启 inference。
- `WithSelfHostedIngestExpirationDateResolver` 会在每次有效且非空的 ingestion
  中、推进 watermark 之前执行一次。回调接收请求 context 和 session，并返回
  `time.Time`；适配层使用该值所在时区的日历日期，以 `YYYY-MM-DD` 发送。返回零值
  时省略该字段；返回错误时不发送请求，也不推进 watermark。resolver 可能并发执行，
  因此必须支持并发，并把传入的 session 视为只读。到期只会让普通读取隐藏该记忆，
  不会删除底层记录。
- `WithIngestInference` 控制 Mem0 的 `infer` 字段。默认值仍为 `true`；设为
  `false` 时，适配层会把规范化后的非 system 消息发送给 Mem0 进行 direct import，
  不经过 LLM 提取，并且不能再配置自定义提取 prompt 或 procedural memory。本地
  OSS 会保存 user 和 assistant 两种角色；托管平台当前只保留 user 角色的 direct
  import 消息。静态不兼容组合会在 `NewService` 阶段直接返回错误。
- `WithSelfHostedProceduralMemory` 选择 Mem0 的 `procedural_memory` 模式。
  未配置时，Mem0 的公开 create API 会自行提取普通记忆；procedural memory 必须
  同时提供 `agent_id`，并且始终使用 inference。
- prompt、expiration-date resolver 与 memory type 仅供本地 OSS 使用；托管模式会
  明确报错，不会静默忽略。`infer` 在两种模式下都支持。
- 当前锁定的 OSS REST create schema 不暴露 `timestamp`；底层 `Memory.add` 会把
  非空 timestamp 视为仅供平台使用并拒绝该值，因此适配层不暴露这个字段。
- 这些本地请求字段以 Mem0 OSS 2.0.11（`mem0ai/mem0@3b9aed8`）为兼容基线。
  更早的 OSS 版本不在这些字段的支持范围内，并且可能静默忽略 REST schema
  无法识别的请求属性。

本地模式与托管模式共用 `ReadMemories` 和 `SearchMemories`。`MaxResults` 限制
本地过滤后的最终结果数量。为了让 kind 和时间等框架侧过滤仍能填满该数量，适配层
可能通过更大的 `top_k` 向服务端获取候选；在本地模式下，还会把非零
`SimilarityThreshold` 作为 `threshold` 发送。返回值统一映射为
`memory.Entry`，包括 ID、正文、score、时间戳，以及 metadata 中保存的 tRPC
结构化记忆字段。对于 `memory.Entry` 无法表达的 provider 专属诊断信息，适配层
不会额外引入第二套公共结果模型。

如果使用官方本地 Mem0 OSS server，并且 LLM 与 embedding 使用不同 endpoint 或
API key，需要在 server 侧分别配置。OSS server 提供 `POST /configure`：
`llm.provider=openai` 配置 LLM 的模型、base URL 和 API key，
`embedder.provider=openai` 配置 embedding 的模型、base URL 和 API key。Go
适配层只访问 Mem0 REST API，不直接读写 OSS server 内部的向量库。

## 注意事项

- `Tools()` 默认暴露 `memory_search`；`memory_load` 可按需开启。
- 默认情况下，读取会基于当前 `<appName, userID>` 做隔离。
- 本地 OSS 没有 top-level `app_id`，适配层使用 `metadata.trpc_app_name` 做 app 隔离。已有 OSS 记录如果缺少这个 metadata，默认会被隐藏，直到重新 ingest 或回填 metadata。`WithSelfHostedOSSIncludeUnscopedMemories()` 会让共用同一 `user_id` 的不同 app 都看到未标记记录，因此会弱化隔离，只应在受控迁移期间启用。
- 当前 OSS `GET /memories` API 最多返回 1000 条 user 级结果，不支持分页，也不能在服务端表达 `metadata.trpc_app_name` 过滤。因此 `ReadMemories` 要求传入大于 0 且不超过 1000 的 limit，并且只会在 OSS 返回的前 1000 条 user 级记录内尽力做本地 app 隔离。
- Runner 会自动把 session 上下文带入 `IngestSession`。自定义调用方可通过 `session.WithIngestMetadata`、`session.WithIngestAgentID` 与 `session.WithIngestRunID` 设置单次调用的通用字段；Mem0 专属字段通过 service option 配置。
- 当同一个 mem0 service 通过 `runner.WithSessionIngestor(mem0Svc)` 配置后，`WithPreloadMemory(N)` 可以使用 mem0 的只读能力；生产环境建议使用正数预算。
- 当 mem0 返回结构化 metadata 时，检索结果仍可携带 `Topics`、`Kind`、`EventTime`、`Participants`、`Location` 等字段。
- 使用完成后请调用 `Close()`，确保后台 worker 干净退出。
- 如果你需要完整的 CRUD 工具面，建议优先选择内置 Memory 后端。
