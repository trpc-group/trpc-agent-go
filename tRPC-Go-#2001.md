# tRPC-Go-#2001 开发记录

## 一、仓库定位

tRPC-Agent-Go 是腾讯开源的生产级 AI Agent 框架，采用 **Go 多模块单体仓库**（multi-module monorepo）架构，约 80 个独立的 `go.mod` 子模块。它**不是**独立应用，而是以 SDK 库的形式供外部项目集成。

| 属性 | 值 |
|------|-----|
| 根模块路径 | `trpc.group/trpc-go/trpc-agent-go` |
| Go 版本 | 根模块 Go 1.21+，部分子模块需 Go 1.24+ |
| 许可证 | Apache-2.0 |
| 子模块数量 | ~80 个 `go.mod` |
| 测试 Mock | 全部使用 Mock，无需外部 API Key |
| CGO 依赖 | SQLite（`mattn/go-sqlite3`）需 CGO 启用 |
| 文档站点 | https://trpc-group.github.io/trpc-agent-go/ |

**核心定位**：将 LLM Agent、图工作流、工具调用、Session/Memory 状态、知识检索、Agent 自进化、评测与 OpenTelemetry 可观测性整合到一套 Go-native 技术栈中，让 Agent 应用天然适配 Go 服务开发、并发执行和可观测部署。同时支持接入 A2A、AG-UI、MCP 等协议与其他语言服务互通。

## 二、核心模块

| 模块 | 路径 | 说明 |
|------|------|------|
| Agent 系统 | `agent/` | LLM Agent、GraphAgent、Chain/Parallel/Cycle Agent、A2A/Dify/n8n/Claude Code/Codex 集成 |
| 运行时引擎 | `runner/` | Session 管理、事件持久化、后处理链路、tRPC 微服务集成 |
| 模型层 | `model/` | OpenAI/Anthropic/Gemini/Ollama/HuggingFace/混元/Bedrock，含 Failover/Hedge 策略 |
| 知识库/RAG | `knowledge/` | 文档解析→分块→嵌入→向量存储→检索→重排序→OCR 全链路 |
| 代码执行沙箱 | `codeexecutor/` | 容器/本地/E2B/Jupyter/CodeAct 运行时 + 安全策略 |
| 记忆系统 | `memory/` | SQLite/MySQL/PGVector/Redis/Mem0/腾讯云 等 10+ 后端 |
| 制品存储 | `artifact/` | InMemory/COS/S3 |
| 评测系统 | `evaluation/` | EvalSet、LLM 评判、用户模拟、工具轨迹 |
| 自进化 | `evolution/` | Hermes 式会话复盘→技能提取→门禁→发布 |
| 事件系统 | `event/` | 事件驱动 + 延迟诊断 |
| 示例 | `examples/` | 50+ 可运行示例 |
| E2E 测试 | `test/` | 集成测试模块 |
| 基准测试 | `benchmark/` | GAIA、Memory、SkillCraft 等 |
| 文档站点 | `docs/` | MkDocs |

## 三、构建与测试

| 命令 | 说明 |
|------|------|
| `go build ./...` | 根模块构建 |
| `go test ./...` | 根模块测试（全 Mock，无需 API Key） |
| `bash .github/scripts/run-go-tests.sh` | 全子模块测试 |
| `golangci-lint run --timeout=10m` | 代码检查 |

**注意**：CGO 必须启用（SQLite）；License Header 强制；GOPATH/bin 需在 PATH。

---

## 四、Issue #2001 需求分析

### 4.1 背景

项目支持 InMemory、SQL、Redis 等 Session/Memory 后端，并支持多轮对话、状态读写、事件追加、长期记忆、Session Summary 等能力。生产环境经常先用 InMemory 开发，再切换到 SQL、Redis 或其他持久化后端。如果不同后端在同一条 Agent 轨迹下保存的事件顺序、state、memory 或 summary 不一致，就会导致回放错乱、上下文丢失、长期记忆污染、摘要覆盖错误等问题。

**目标**：构建一个可复用的回放一致性框架，用同一组标准化输入驱动多个后端，并自动生成差异报告。

### 4.2 现有接口要点

**Session Service**（`session.Service`）：

| 方法 | 说明 |
|------|------|
| `CreateSession` | 创建会话 |
| `GetSession` | 获取会话（含 Events、State、Summaries、Tracks） |
| `ListSessions` | 按用户列出会话 |
| `DeleteSession` | 删除会话 |
| `AppendEvent` | 追加事件 |
| `UpdateSessionState` | 更新会话状态 |
| `CreateSessionSummary` | 触发摘要生成 |
| `GetSessionSummaryText` | 获取摘要文本（支持 filter-key） |
| `EnqueueSummaryJob` | 异步摘要入队 |

**Track Service**（`session.TrackService`）：

| 方法 | 说明 |
|------|------|
| `AppendTrackEvent` | 追加轨道事件 |

**Window Service**（`session.WindowService`）：

| 方法 | 说明 |
|------|------|
| `GetEventWindow` | 以锚点事件为中心获取事件窗口 |

**Memory Service**（`memory.Service`）：

| 方法 | 说明 |
|------|------|
| `AddMemory` | 添加记忆（幂等） |
| `UpdateMemory` | 更新记忆 |
| `DeleteMemory` | 删除记忆 |
| `ClearMemories` | 清除用户所有记忆 |
| `ReadMemories` | 读取记忆 |
| `SearchMemories` | 搜索记忆 |

**Session 关键数据结构**：

- `Session.ID`, `Session.State`（`StateMap = map[string][]byte`）, `Session.Events`（`[]event.Event`）
- `Session.Tracks`（`map[Track]*TrackEvents`）, `Session.Summaries`（`map[string]*Summary`）
- `Summary` 含 `Summary` 文本、`Topics`、`UpdatedAt`、`Boundary`（`SummaryBoundary` 含 `FilterKey`、`CutoffAt`、`LastEventID`、`Version`）
- `TrackEvent` 含 `Track`, `Payload`（`json.RawMessage`）, `Timestamp`

**Memory 关键数据结构**：

- `Entry` 含 `ID`, `AppName`, `Memory`（`*Memory`）, `UserID`, `CreatedAt`, `UpdatedAt`, `Score`
- `Memory` 含 `Memory` 文本、`Topics`, `LastUpdated`, `Kind`（fact/episode）, `EventTime`, `Participants`, `Location`

### 4.3 已有后端

| 类型 | 后端 | Session | Memory | 必选/可选 |
|------|------|---------|--------|-----------|
| 内存 | InMemory | ✅ | ✅ | **必选** |
| 本地持久化 | SQLite | ✅ | ✅ | **必选** |
| 可选持久化 | MySQL | ✅ | ✅ | 可选 |
| 可选持久化 | Postgres | ✅ | ✅ | 可选 |
| 可选持久化 | Redis | ✅ | ✅ | 可选 |
| 可选持久化 | ClickHouse | ✅ | ❌ | 可选 |
| 可选持久化 | MongoDB | ✅ | ❌ | 可选 |
| 可选持久化 | PGVector | ✅ | ✅ | 可选 |
| 可选持久化 | SQLiteVec | ❌ | ✅ | 可选 |
| 可选持久化 | Mem0 | ❌ | ✅ | 可选 |
| 可选持久化 | TencentDB | ❌ | ✅ | 可选 |

### 4.4 Go 版特有覆盖范围

相比 Python 版，Go 版还需额外覆盖：

- **Summary filter-key**：`session.Summary` 支持按 `FilterKey` 分层管理摘要，非空 key 表示按 event filter 分支汇总，空 key 表示全量摘要。比较时需检查 filter-key 归属、覆盖关系和版本
- **Track 观测轨迹**：`session.TrackService.AppendTrackEvent` 存储工具执行耗时、子任务状态、异常记录等，Go 版多处已有实现（Redis、SQLite、Postgres、MySQL 均支持 Track）
- **事件分页**：部分后端（Postgres、MySQL）支持 `WithGetSessionEventPage`，需标记 unsupported 后端
- **TTL**：部分后端支持 `sessionTTL`，需在比较时考虑

---

## 五、框架设计

### 5.1 目录结构

```
session/replaytest/         ← 多后端回放一致性测试框架
├── harness.go              # 主 Harness：编排各后端执行 replay case
├── harness_test.go         # Harness 集成测试
├── case.go                 # ReplayCase + ReplayOp 定义
├── case_test.go            # Case 单元测试
├── normalizer.go           # 字段归一化（ID/时间戳/JSON 顺序/map 遍历/浮点）
├── normalizer_test.go      # 归一化器测试
├── comparator.go           # 跨后端比较逻辑
├── comparator_test.go      # 比较器测试
├── reporter.go             # 差异报告生成（JSON + 文本）
├── reporter_test.go        # 报告生成器测试
├── trap.go                 # 异常注入机制（TrapInjector）：故意篡改一个后端的数据
├── trap_test.go            # 异常注入测试
├── mockmodel.go            # Mock 模型/工具：生成逼真的 tool call 事件序列
├── mockmodel_test.go       # Mock 模型测试
├── backends.go             # 后端注册 + 启动/清理
├── backends_test.go        # 后端接入测试
├── fixtures.go             # 10 条 replay case 公共 fixture
├── fixtures_test.go        # Fixture 正确性测试
├── design.md               # 150-300 字设计说明
└── testdata/
    └── session_memory_summary_track_diff_report.json  # 示例输出
```

### 5.2 核心类型设计

```go
// ReplayOp 定义一条原子操作
type ReplayOp struct {
    Type  OpType        // CreateSession | AppendEvent | UpdateState | ...
    Key   session.Key   // 操作目标 session
    Data  any           // 操作数据（event / state / memory / summary 等）
}

// ReplayCase 定义一组操作序列 + 期望结果
type ReplayCase struct {
    Name string       // 用例名称
    Ops  []ReplayOp   // 操作序列
    Want WantResult    // 期望结果（用于注入不一致检测）
}

// BackendResult 存储单个后端执行结果
type BackendResult struct {
    BackendName  string
    Session      *session.Session        // 最终 session 快照
    Memories     []*memory.Entry         // 最终记忆条目
    SummaryTexts map[string]string       // filterKey → summary text
    Tracks       map[session.Track]*session.TrackEvents
    Duration     time.Duration
    Error        error
}

// DiffEntry 记录一个差异
type DiffEntry struct {
    CaseName    string      // 所属 case
    BackendA    string      // 基准后端
    BackendB    string      // 对比后端
    FieldPath   string      // 字段路径（如 "events[2].content"）
    SessionID   string      // session id
    EventIndex  int         // 事件索引（若适用）
    SummaryKey  string      // summary filter-key（若适用）
    TrackName   string      // track name（若适用）
    MemoryID    string      // memory id（若适用）
    Baseline    any         // 基准值
    Actual      any         // 对比值
    AllowedDiff bool        // 是否属于允许差异
    DiffReason  string      // 差异解释
}

// TrapInjector 定义异常注入策略
// 故意篡改一个后端的数据，验证框架能否检出
type TrapInjector struct {
    Name        string                  // 注入名称，如 "swap_event_order"
    Description string                  // 描述
    Inject      func(result *BackendResult) // 篡改函数
    ExpectKeys  []string                // 预期差异报告中应出现的字段路径
    ExpectCount int                     // 预期检出差异数
}

// MockModel 生成逼真的多轮对话 + 工具调用事件序列
// 不依赖真实 LLM API，输出可重复、可验证的事件流
type MockModel struct {
    Seed int64  // 随机种子，保证可重复
}

// GenerateConversation 生成一轮包含 tool call 的对话
func (m *MockModel) GenerateConversation(turns int) []event.Event

// GenerateToolCall 生成一个带复杂参数的工具调用事件
// 参数类型覆盖 string/int/float/array/object/nested
func (m *MockModel) GenerateToolCall() event.Event
```

### 5.3 10 条 Replay Case

每条 case 支持两种运行模式：
- **一致性模式**（默认）：在多个后端上执行相同操作序列，验证结果一致
- **Trap 模式**：对其中一个后端的结果注入人为不一致，验证框架能否检出

| # | 名称 | 覆盖场景 | 操作序列 | Trap 验证点 |
|---|------|---------|---------|------------|
| 1 | 单轮普通对话 | 基本 session 创建 + 事件追加 | CreateSession → AppendEvent(user) → AppendEvent(assistant) → GetSession | 事件内容/顺序/role 篡改 |
| 2 | 多轮对话 | 连续追加、顺序读取 | CreateSession → AppendEvent×6（user/assistant 交替）→ GetSession 检查顺序 | 事件交换/丢失 |
| 3 | 工具调用对话 | tool call + tool response + args extension | CreateSession → AppendEvent(user) → AppendEvent(tool_call) → AppendEvent(tool_response) → AppendEvent(assistant) | tool_call args 篡改/tool response 内容篡改 |
| 4 | State 更新 | 写入、覆盖、删除、清空 | CreateSession → UpdateState×3 → DeleteState → GetSession 检查最终 state | state value 篡改/key 丢失 |
| 5 | Memory 写入和读取 | 偏好、事实、任务经验 | AddMemory(fact) → AddMemory(episode) → ReadMemories → SearchMemories | 记忆内容篡改/scope 丢失/相似度偏移 |
| 6 | Summary 生成和更新 | 内容、filter-key、版本、归属 | AppendEvent×4 → CreateSessionSummary("") → CreateSessionSummary("branch-a") → GetSessionSummaryText | summary 内容丢失/filter-key 篡改/归属错误 |
| 7 | Summary 与事件截断 | 长对话压缩后上下文还原 | AppendEvent×10 → CreateSessionSummary → AppendEvent×2 → GetSession 检查事件+summary | 截断后事件丢失/summary 覆盖错误 |
| 8 | Track 事件 | 耗时、子任务、异常 | CreateSession → AppendTrackEvent×3 → GetSession 检查 tracks | track 时间偏移/event type 篡改/关联 invocation 丢失 |
| 9 | 并发或乱序写入 | 事件交错追加、最终顺序 | CreateSession → 并发 AppendEvent×5 → GetSession 检查归一化顺序 | 最终顺序错乱/事件重复 |
| 10 | 异常恢复 | 重复写入、重试、中途失败 | CreateSession → AppendEvent(idempotent) → AppendEvent(same) → 检查无重复 | 重复 event/脏 state/重复 memory

### 5.4 比较字段范围

Comparator 逐字段比较时，至少覆盖以下范围：

**Event 字段**：`author`、`role`、`content`、`tool_call_id`、`tool_call`（含 name、arguments、args extension）、`tool_response`、`branch`、`tag`、`filterKey`、`stateDelta`、`extensions`、`create_time`

**State 字段**：`key`、`value`（`[]byte`）、覆盖顺序、删除语义、最终状态

**Memory 字段**：`memory_id`、`content`、`metadata`（含 kind、event_time、participants、location）、`scope`（app/user 归属）、检索结果顺序、相似度分数

**Summary 字段**：`filter-key`、`summary_text`、`version`（boundary.version）、`session_归属`、`覆盖关系`、`更新时间`、`cutoff_at`、`last_event_id`

**Track 字段**：`track_name`、`event_type`（payload 中的事件类型标识）、`关联 invocation`、`timestamp`、`error_message`、`duration_ms`（耗时字段）

### 5.5 归一化策略

| 字段 | 归一化处理 | 说明 |
|------|-----------|------|
| 自动生成 ID | 忽略或替换为占位符 | session.ID、event.ID、memory.ID 等 |
| 时间戳 | 归一化到同一时区（UTC），允许 ±1s 误差 | CreatedAt、UpdatedAt、Timestamp 等 |
| JSON 字段顺序 | 反序列化后重新序列化排序 | event.Payload、track.Payload 等 |
| map 遍历顺序 | 排序后比较 key-value 对 | StateMap、Summaries、Tracks 等 |
| 浮点相似度 | 允许 ±0.01 误差 | Score、DenseScore 等 |
| 后端私有 metadata | 忽略 | ServiceMeta 等 |

### 5.6 Allowed Diff 规则

| 场景 | 规则 | 说明 |
|------|------|------|
| 事件分页 | 不支持分页的后端标记 `unsupported` | Postgres/MySQL 支持，InMemory/SQLite 不支持 |
| TTL | 支持 TTL 的后端返回过期数据 | 非 TTL 后端不返回过期数据 |
| 向量搜索 | 不同后端排序/相似度不同 | 允许浮点差异，检查结果数量而非完全顺序 |
| Track | 不支持 Track 的后端标记 `unsupported` | 部分后端可能不支持 |
| Summary filter-key | 不支持 filter-key 的后端只返回空 key 摘要 | 需明确标记 |
| 并发写入 | 最终事件顺序可能因实现不同而不同 | 按时间戳排序后归一化比较 |

### 5.7 后端接入方式

```go
// BackendFactory 定义后端工厂
type BackendFactory struct {
    Name    string
    Enabled bool                                   // 是否启用（受环境变量控制）
    New     func() (session.Service, memory.Service, error)  // 创建后端实例
}

// 注册方式
func RegisterBackend(factory BackendFactory)

// 环境变量控制
// REPLAYTEST_SQLITE_ENABLED=true   (默认开启)
// REPLAYTEST_REDIS_ENABLED=false   (默认关闭)
// REPLAYTEST_POSTGRES_ENABLED=false
// REPLAYTEST_MYSQL_ENABLED=false
// REPLAYTEST_CLICKHOUSE_ENABLED=false
```

### 5.8 真实模型集成模式（可选）

为进一步验证框架在真实场景下的检出能力，提供可选的真实模型集成模式，由环境变量 `REPLAYTEST_REAL_MODEL=true` 开启。

**工作流程**：

```
设置 OPENAI_API_KEY / ANTHROPIC_API_KEY
    │
    ▼
LlMAgent 或 Runner 用真实模型执行一轮含工具调用的对话
    │
    ├──→ 将产生的事件序列导出为 ReplayOp
    ├──→ 在各后端上回放
    └──→ 跑 Comparator 验证一致性
```

**覆盖的异常场景**（真实模型产生的事件结构更复杂，更容易暴露差异）：

- 多轮 tool call 链：tool call → tool response → 二次 tool call 的嵌套结构
- 长上下文事件：超过 100 条事件的 session 分页读取一致性
- 异步 summary 触发：`EnqueueSummaryJob` 在不同后端上的完成时序差异
- 复杂 tool call args：`json.RawMessage` 嵌套、字段顺序、特殊字符转义

**注意**：此模式需 API Key，仅用于集成验证，不纳入 CI 自动化测试。环境变量未设置时跳过。

### 5.9 记忆细微差异检测要点

老师特别强调"记忆细微的出错，可能导致整个语义产生偏差"。Comparator 在处理 Memory 比较时，需特别注意：

- **字节级比较**：`content` 字段做逐字节比较，单字节差异也必须检出（如 `"Alice"` 被篡改为 `"alice"` 或 `"Blice"`）
- **语义偏差检测**：对于 `topics` 数组，检查词语替换而非仅检查长度/数量
- **元数据偏差**：`kind`（fact vs episode）错标、`event_time` 偏移、`participants` 遗漏或多余
- **检索结果顺序**：跨后端 `SearchMemories` 返回顺序可能不同，需检查结果集合是否相等而非仅顺序一致

---

## 六、实施计划

### Phase 1：核心框架

| 序号 | 任务 | 交付物 |
|------|------|--------|
| 1.1 | 定义 ReplayOp、ReplayCase、BackendResult 等核心类型 | `case.go` |
| 1.2 | 实现 Normalizer（ID/时间戳/JSON 顺序/map 遍历/浮点归一化） | `normalizer.go` |
| 1.3 | 实现 Comparator（逐字段比较 + allowed_diff 判断） | `comparator.go` |
| 1.4 | 实现 Reporter（JSON 差异报告生成） | `reporter.go` |
| 1.5 | 实现 Backend 注册 + 环境变量控制 | `backends.go` |
| 1.6 | 实现 Harness 主循环：执行所有 case → 比较 → 输出报告 | `harness.go` |

**验证**：`go test ./session/replaytest/...` 通过，比较器/归一化器/报告生成器单元测试覆盖

### Phase 2：后端接入

| 序号 | 任务 | 交付物 |
|------|------|--------|
| 2.1 | 接入 InMemory Session + InMemory Memory | `backends.go` 注册 |
| 2.2 | 接入 SQLite Session + SQLite Memory | `backends.go` 注册 |
| 2.3 | 可选：接入 Redis（环境变量控制） | `backends.go` 注册 |
| 2.4 | 可选：接入 Postgres（环境变量控制） | `backends.go` 注册 |
| 2.5 | 可选：接入 MySQL（环境变量控制） | `backends.go` 注册 |
| 2.6 | 可选：接入 ClickHouse（环境变量控制） | `backends.go` 注册 |

**验证**：轻量模式（InMemory + SQLite）完整运行 ≤ 30 秒

### Phase 3：Mock 模型 + 异常注入机制

| 序号 | 任务 | 交付物 |
|------|------|--------|
| 3.1 | 实现 MockModel：生成可重复的多轮对话 + tool call 事件序列 | `mockmodel.go` |
| 3.2 | 实现 MockModel.GenerateToolCall：覆盖 string/int/float/array/object/nested 参数 | `mockmodel.go` |
| 3.3 | 实现 TrapInjector 框架 + 预置注入策略（事件交换/记忆篡改/摘要删除/时间偏移/状态篡改/事件重复/filter-key 篡改） | `trap.go` |
| 3.4 | 实现 Harness.TrapRun：执行 case → 对结果注入陷阱 → 验证框架能否检出 | `harness.go` |
| 3.5 | 集成验证：用 MockModel 生成事件序列 → 写入后端 → 注入陷阱 → 跑比较器 → 验证检出 | `trap_test.go` |

**验证**：7 种预置陷阱全部被框架检出，ExpectKeys 和 ExpectCount 匹配

### Phase 4：Replay Case 实现

| 序号 | 任务 | 交付物 |
|------|------|--------|
| 4.1 | Case 1-2：单轮/多轮对话 | `fixtures.go` |
| 4.2 | Case 3：工具调用对话（用 MockModel 生成） | `fixtures.go` |
| 4.3 | Case 4：State 更新 | `fixtures.go` |
| 4.4 | Case 5：Memory 写入和读取 | `fixtures.go` |
| 4.5 | Case 6：Summary 生成和更新 | `fixtures.go` |
| 4.6 | Case 7：Summary 与事件截断 | `fixtures.go` |
| 4.7 | Case 8：Track 事件 | `fixtures.go` |
| 4.8 | Case 9-10：并发写入 + 异常恢复 | `fixtures.go` |

**验证**：10 条 case 在 InMemory 和 SQLite 上执行结果一致

### Phase 5：测试与文档

| 序号 | 任务 | 交付物 |
|------|------|--------|
| 5.1 | 编写 fixtures_test.go（验证 fixture 正确性） | `fixtures_test.go` |
| 5.2 | 编写 harness_test.go（集成测试） | `harness_test.go` |
| 5.3 | 编写 trap_test.go（7 种陷阱注入 → 验证框架 100% 检出） | `trap_test.go` |
| 5.4 | 编写 mockmodel_test.go（MockModel 生成的事件序列可重复、结构正确） | `mockmodel_test.go` |
| 5.5 | 生成示例差异报告 | `testdata/session_memory_summary_track_diff_report.json` |
| 5.6 | 编写 design.md（150-300 字设计说明） | `design.md` |
| 5.7 | 后端接入说明文档 | 注释 + README |

**验证**：
- 10 条公开 case 在 InMemory 和 SQLite 上执行结果一致 ✅
- 10 条 case 在 Trap 模式下，对结果注入人为不一致后，Comparator 100% 检出差异 ✅
- 7 种预置陷阱（事件交换/记忆篡改/摘要删除/时间偏移/状态篡改/事件重复/filter-key 篡改）的 ExpectKeys 和 ExpectCount 全部匹配 ✅
- 正常 case 误报率 ≤ 5%（归一化策略 + Allowed Diff 规则保障） ✅
- MockModel 生成的 tool call 事件序列可重复、结构完整 ✅
- Summary 丢失/覆盖错误/归属错误检出率 100% ✅
- Filter-key 错误检出率 100% ✅
- 差异报告定位到 session id、event index、summary filter-key、track name、memory id ✅
- 轻量模式 ≤ 30s ✅

---

## 开发规范

### 提交信息

**格式**：`<包名>: <简短描述>`（首行），空行后接正文，`Fixes #12345` 关联 issue，`RELEASE NOTES:` 描述用户可见变更。

**PR 标签**：`type/bug` / `type/feature` / `type/enhancement` / `type/documentation` / `type/api-change` / `type/failing-test` / `type/performance` / `type/ci`

**硬性规则**：
- Author = Committer = `Stelquis <3420761503@qq.com>`
- CNB 环境变量会覆盖 Committer，每次 session 执行 `export GIT_COMMITTER_NAME=Stelquis && export GIT_COMMITTER_EMAIL=3420761503@qq.com`
- 关闭 GPG 签名：`git config commit.gpgsign false`
- 禁止 `Co-Authored-By`
- 含密文的提交必须 squash，不可叠加修复掩盖
- Force push 仅 squash 后使用 `--force-with-lease`

### 分支策略

- `main`：跟踪上游
- `feature/<name>`：开发分支

### 推送策略

- `origin`（CNB）：每次 commit 后
- `fork`（GitHub）：阶段性完成时

### 代码格式（CI 强制）

| 检查项 | 工具 | 要求 |
|--------|------|------|
| Go 基本格式 | `gofmt` | 标准 Go 格式 |
| 导入排序 | `goimports` | 标准库→第三方→内部 |
| 类型别名 | `gofmt -r` | **必须用 `any`**，禁止 `interface{}` |
| 圈复杂度 | `gocyclo` | 单函数 ≤ 20 |
| 无效赋值 | `ineffassign` | 禁止无效赋值 |
| 安全性 | `gosec` | 安全扫描 |

Python（如有）：`snake_case` + `PascalCase` + 类型注解 + Google docstring。异步优先，错误处理不抛出未处理异常。

### 注释要求（CI 强制）

- 每个包必须有 **package 注释**
- 所有导出的类型、函数、方法、常量、变量必须有注释
- 例外：`.pb.go`（protobuf 生成）、`_mock.go`、`_test.go` 不检查

### 版权头

所有 `.go` 文件必须包含腾讯 Apache-2.0 License Header：

```go
//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
```

### 文件命名

`snake_case.go` / `.py` / `.sql`，`UPPER_CASE.md`，`snake_case.json`，`NN_类别.diff`

### 测试规范

Go 标准 `testing` / pytest，全 Mock 无 API Key，Dry-run 优先验证链路，License Header 强制

### 文档规范

中文注释（Why 非 What），README/DESIGN/SKILL.md 各司其职，开发日志每次更新

### 重要提醒

- 本文件 `tRPC-Go-#2001.md` **永不提交不推送**
- CNB 环境变量会覆盖 Committer，每次打开环境后先执行 export
- 所有测试无需 API Key（全 Mock）
- CGO 依赖需要 C 编译器

---

## 复盘记录

### 问题：代码块未闭合导致分隔线失效

**现象**：文档第 339 行的 `---` 分隔线没有起到分隔作用，被 Markdown 解析器当成了代码块内的纯文本。反复检查多次才在用户提示下定位到根因。

**根因**：第 331 行用 ` ``` ` 开启了代码块（时间线 ASCII 图），但忘记用 ` ``` ` 闭合。第 339 行的 `---` 实际上仍在代码块内，不是水平分隔线。

**纠正**：在 `---` 前补上 ` ``` ` 闭合代码块。

**教训**：

1. **任何修改都有语法**——无论是 Go、Python、Shell、YAML、Markdown 还是 JSON，每一类文件都有自己的语法规则。代码块要配对、括号要闭合、缩进要一致，这些规则不因文件类型而豁免。不能因为"只是文档"就跳过验证。

2. **验证提示是检查点，不是干扰**——系统每次提醒 `verify code edits`，本质是要求确认当前修改的正确性。反复跳过等于放弃了每一次提前发现问题的机会。正确的做法是：根据修改的文件类型，执行对应的检查手段（编译、lint、格式化校验、语法检查）。

3. **检查手段应与文件类型匹配**：

   | 文件类型 | 检查方式 |
   |---------|---------|
   | Go | `go build`, `go vet`, `golangci-lint` |
   | Python | `python -m py_compile`, `ruff`, `mypy` |
   | YAML | `yamllint` |
   | Markdown | `markdownlint`, 代码块配对检查, 渲染预览 |
   | JSON | `jq .`, `python -m json.tool` |
   | Shell | `shellcheck`, `bash -n` |

   核心原则：**不因文件类型简单而跳过语法检查，不因修改量小而省略验证步骤。**