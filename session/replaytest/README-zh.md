# 回放测试 — 多后端回放一致性测试框架

一个确定性的、基于快照的测试框架，对多个 Session / Memory 后端执行同一组操作，
并对每个中间状态做 diff。它面向生产级回归检查，支持归一化、allowlist diff、
golden trace、checkpoint、circuit breaker 和机器可读报告。

## 设计说明

框架会先把后端数据归一化成纯 JSON `Snapshot`，再做比较，因此比较逻辑不依赖
Go 结构体布局。

- `IDAliasMap` 会把不稳定 ID 替换成稳定别名，例如 `event-000`、
  `tool-call-001`，同时保留交叉引用关系。
- `StateDelta` 里的 `nil` 会归一化成 `MissingValue{}`，序列化为
  `{"__missing":true}`，从而严格区分“字段缺失”和“字段存在但值为 null”。
- `duration`、`duration_ms`、`elapsed`、`elapsed_ms`、`latency`、
  `latency_ms` 等易变字段会在比较前移除。
- Summary 比较会把时间类元数据转换为 event index，Track 比较会归一化
  每个事件的 payload。
- `AllowedDiff` 规则要求精确的 `section + path` 匹配、必填 reason，并校验
  section/path 一致性。

会影响本 README 所有命令的代码级约束：

- 这个模块的测试文件使用 `//go:build cgo`
- SQLite 使用 `github.com/mattn/go-sqlite3`

因此，下面所有测试命令都应使用 `CGO_ENABLED=1`，包括 InMemory 自检路径。

## 项目结构

```text
session/replaytest/
├── case.go             # Case 和 operation 类型定义
├── normalize.go        # Snapshot 归一化
├── diff.go             # 以 Snapshot 为中心的比较引擎
├── harness.go          # Run / RunSuite、capture、checkpoint、report I/O
├── factory.go          # BackendFactory 与后端构造逻辑
├── golden.go           # Golden trace 保存/加载/回归辅助逻辑
├── types.go            # 共享类型
├── cases_test.go       # 公开 replay 回归入口
├── helpers_test.go     # 测试辅助函数
├── unit_test.go        # 单元测试和 factory 覆盖测试
├── README-en.md        # 英文版
├── README-zh.md        # 本文件
└── go.mod              # 独立 Go module
```

## 快速开始

下面的命令都应从仓库根目录执行。每个命令都显式进入
`session/replaytest`，可以直接复制粘贴执行。

前置要求：

- Go `1.24.1` 或更高版本
- 可用的 C toolchain
- `go` 已经在 `PATH` 中

Windows 示例使用 PowerShell。macOS 和 Linux 示例使用 POSIX shell，例如
`bash` 或 `zsh`。go 

### 轻量模式（默认）

这是主回归入口，对应 `TestReplay_All`。它会在 InMemory 和 SQLite 上跑完
15 个公开 replay case。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_All
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_All
```

### 自检模式

这个命令运行 `TestReplay_Smoke_InMemorySelfVerify`，比较两个隔离的
InMemory backend。虽然不与 SQLite 比较，但由于测试包受 `cgo` build tag
约束，仍然需要 `CGO_ENABLED=1`。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_Smoke_InMemorySelfVerify
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_Smoke_InMemorySelfVerify
```

### 运行单个 Case

这个例子只运行 `TestReplay_All` 下的 `case01_single_turn`。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run "TestReplay_All/case01_single_turn$"
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run 'TestReplay_All/case01_single_turn$'
```

### 生成干净报告

这个命令运行 `TestReplay_Report`。报告会写到 Go 的测试临时目录，而不是固定的
仓库路径。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_Report
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_Report
```

### 生成样例 Diff 报告

这个命令运行 `TestReplay_ReportWithDiffs`。测试日志会打印临时输出路径。
仓库中还提交了一份样例文件：`test/session_memory_summary_track_diff_report.json`。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_ReportWithDiffs
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_ReportWithDiffs
```

### 运行整个 Module 的测试套件

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test ./... -count=1
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test ./... -count=1
```

### 竞态检测

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -race -v -count=1
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -race -v -count=1
```

## 后端接入

### 轻量模式

默认回归路径是 InMemory vs SQLite：

- **InMemory**：纯内存实现
- **SQLite**：通过 `go-sqlite3` 使用内存 SQLite

### 集成 / Factory 检查

`factory.go` 确实会读取外部后端 DSN 环境变量，但当前公开的 replay 测试入口
并没有提供“一条 `go test` 命令直接跑完整外部后端矩阵”的路径。当前可直接运行、
并且已经验证过的公开入口是：

- 使用进程内 `miniredis` 的 Redis factory 验证
- Postgres / MySQL / ClickHouse 的 factory / selector 相关测试

#### 无需外部 Redis 的 Redis Factory 验证

这个命令运行 `TestFactory_RedisFactory_Create_WithMiniredis`。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestFactory_RedisFactory_Create_WithMiniredis
```

macOS/Linux：

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_RedisFactory_Create_WithMiniredis
```

#### Postgres Factory 构造验证

这个命令运行 `TestFactory_PostgresFactory_Create_WithSkipDBInit`。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; $env:REPLAY_BACKEND = "postgres"; $env:TRPC_AGENT_REPLAY_POSTGRES_DSN = "postgres://localhost:5432/testdb"; $env:TRPC_AGENT_REPLAY_SKIP_DB_INIT = "1"; go test . -v -count=1 -run TestFactory_PostgresFactory_Create_WithSkipDBInit
```

macOS/Linux：

```bash
cd session/replaytest && REPLAY_BACKEND=postgres TRPC_AGENT_REPLAY_POSTGRES_DSN='postgres://localhost:5432/testdb' TRPC_AGENT_REPLAY_SKIP_DB_INIT=1 CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_PostgresFactory_Create_WithSkipDBInit
```

#### MySQL Factory 构造验证

这个命令运行 `TestFactory_MysqlFactory_Create_WithSkipDBInit`。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; $env:REPLAY_BACKEND = "mysql"; $env:TRPC_AGENT_REPLAY_MYSQL_DSN = "test:test@tcp(localhost:3306)/testdb"; $env:TRPC_AGENT_REPLAY_SKIP_DB_INIT = "1"; go test . -v -count=1 -run TestFactory_MysqlFactory_Create_WithSkipDBInit
```

macOS/Linux：

```bash
cd session/replaytest && REPLAY_BACKEND=mysql TRPC_AGENT_REPLAY_MYSQL_DSN='test:test@tcp(localhost:3306)/testdb' TRPC_AGENT_REPLAY_SKIP_DB_INIT=1 CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_MysqlFactory_Create_WithSkipDBInit
```

#### ClickHouse Factory 构造验证

这个命令运行 `TestFactory_ClickhouseFactory_Create_WithSkipDBInit`。

PowerShell：

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; $env:REPLAY_BACKEND = "clickhouse"; $env:TRPC_AGENT_REPLAY_CLICKHOUSE_DSN = "clickhouse://localhost:9000"; $env:TRPC_AGENT_REPLAY_SKIP_DB_INIT = "1"; go test . -v -count=1 -run TestFactory_ClickhouseFactory_Create_WithSkipDBInit
```

macOS/Linux：

```bash
cd session/replaytest && REPLAY_BACKEND=clickhouse TRPC_AGENT_REPLAY_CLICKHOUSE_DSN='clickhouse://localhost:9000' TRPC_AGENT_REPLAY_SKIP_DB_INIT=1 CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_ClickhouseFactory_Create_WithSkipDBInit
```

### 环境变量

下面这些环境变量是当前模块真正会读取的：

| 变量 | 使用位置 | 含义 |
| --- | --- | --- |
| `REPLAY_BACKEND` | `ResolvePair` | 为 factory 相关 helper 和单测选择目标 backend。支持值：`inmemory`、`sqlite`、`miniredis`、`redis`、`postgres`、`mysql`、`clickhouse`。 |
| `TRPC_AGENT_REPLAY_REDIS_URL` | `redisFactory`、`ResolveBackends` | 构造真实 Redis backend 时使用的 Redis 连接 URL。 |
| `TRPC_AGENT_REPLAY_POSTGRES_DSN` | `postgresFactory`、`ResolveBackends` | 构造真实 Postgres backend 时使用的 PostgreSQL DSN。 |
| `TRPC_AGENT_REPLAY_MYSQL_DSN` | `mysqlFactory`、`ResolveBackends` | 构造真实 MySQL backend 时使用的 MySQL DSN。 |
| `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | `clickhouseFactory`、`ResolveBackends` | 构造真实 ClickHouse backend 时使用的 ClickHouse DSN。 |
| `TRPC_AGENT_REPLAY_SKIP_DB_INIT` | `postgresFactory`、`mysqlFactory`、`clickhouseFactory` | 设置后在 factory 创建阶段跳过 DB 初始化。 |

当前代码里**不存在**的内容：

- `REPLAY_BACKEND` 不会改变 `TestReplay_All`
- `TRPC_AGENT_REPLAY_REPORT_PATH` 没有实现
- 当前模块没有一条公开 replay 命令，可以仅靠导出 DSN 就把完整 replay 矩阵跑到 Redis / Postgres / MySQL / ClickHouse 上

## 15 个 Replay Case

`TestReplay_All` 和 `TestReplay_Smoke_InMemorySelfVerify` 执行的是同样的
15 个公开 case：

| # | Case | 必需能力 | 覆盖内容 |
| --- | --- | --- | --- |
| 1 | `case01_single_turn` | `events` | 创建 session、追加 user/assistant 事件、回读 session |
| 2 | `case02_multi_turn` | `events` | 10 条事件的多轮顺序完整性 |
| 3 | `case03_tool_call_cross_ref` | `events` | tool call / tool response 交叉引用归一化 |
| 4 | `case04_state_update_overwrite_delete` | `state` | state 创建、覆盖、追加、通过 `nil` 删除 |
| 5 | `case05_memory_search_and_score` | `memory` | memory 写入、metadata、无序比较 |
| 6 | `case06_summary_filter_and_update` | `summary` | 默认 filterKey 与 branch filterKey 的 summary 生成 |
| 7 | `case07_summary_event_window_recovery` | `summary`, `events` | 带 allowlist 的 summary 边界恢复 |
| 8 | `case08_track_status_and_error` | `track` | 去除易变字段后的 track payload 比较 |
| 9 | `case09_concurrent_tool_interleaving` | `events` | 面向交织场景的 count-only 事件比较 |
| 10 | `case10_failure_recovery_without_duplicates` | `events`, `summary` | 重复追加、覆盖重试、幂等 summary |
| 11 | `case11_state_delta_null` | `state`, `events` | `nil` `StateDelta` 归一化为 `MissingValue` |
| 12 | `case12_boundary_and_error` | `events`, `state` | 空 state、extensions、branch/tag/filterKey、边界读取 |
| 13 | `case13_state_delete` | `state` | 通过写入 `nil` 删除 state key |
| 14 | `case14_state_scopes` | `state` | app 级与 user 级 scoped state 抓取 |
| 15 | `case15_summary_filter_key` | `summary`, `events` | 限定到特定 `filterKey` 的 summary 生成 |

## 能力声明

| 能力 | 说明 |
| --- | --- |
| `events` | 可存储并读取 session event |
| `state` | 可存储并读取 session state |
| `memory` | 可存储并读取 memory entry |
| `summary` | 可创建并读取 session summary |
| `track` | 可追加并读取 track event |
| `event_state_delta_null` | 支持 `StateDelta` 中的 `nil` 值 |

如果某个 backend 缺少 case 要求的能力，该 backend 会跳过该 case。
如果剩余有效 backend 少于两个，结果将是 `inconclusive` 而不是 `pass`。

## Diff 严重级别

| 严重级别 | 条件 | 示例 |
| --- | --- | --- |
| `critical` | 数据丢失或 section 缺失 | `MissingValue` vs 普通值、一个 backend 缺失 event |
| `major` | 值不匹配 | 内容错误或 key 错误 |
| `minor` | 允许的差异 | 已由 `AllowedDiff` 覆盖的已知 backend 差异 |

## 报告格式

仓库里提交的样例报告是：

- `test/session_memory_summary_track_diff_report.json`

生成样例 diff 报告的测试是 `TestReplay_ReportWithDiffs`。该测试会把生成的
JSON 写入临时测试目录，并在日志中打印输出路径。

`Report` 包含：

- `report_id`、`version`、`run_id` 等顶层运行元数据
- backend 列表
- `cases` 下的逐 case 结果
- `summary` 下的聚合统计
- 每条 diff 的 `severity`、`path`、`section` 和两侧 backend 的值

`ReadReportWithVerify` 会校验 checksum sidecar，并拒绝损坏文件。

## 验收结果

更新本 README 时重新执行并通过了以下命令：

| 命令 / 测试 | 结果 |
| --- | --- |
| `TestReplay_All` | PASS |
| `TestReplay_Smoke_InMemorySelfVerify` | PASS |
| `TestReplay_Report` | PASS |
| `TestReplay_ReportWithDiffs` | PASS |
| `TestFactory_RedisFactory_Create_WithMiniredis` | PASS |
| `TestFactory_PostgresFactory_Create_WithSkipDBInit` | PASS |
| `TestFactory_MysqlFactory_Create_WithSkipDBInit` | PASS |
| `TestFactory_ClickhouseFactory_Create_WithSkipDBInit` | PASS |
| `go test ./... -count=1` | PASS |
| `go test . -race -v -count=1` | PASS |

### 验收标准逐项验证

| 验收标准 | 结果 | 说明 |
| --- | --- | --- |
| module-local 命令可直接复制执行 | PASS | 所有命令都显式进入 `session/replaytest` 后再执行 `go test` |
| PowerShell 命令有效 | PASS | 更新 README 时已重新执行 |
| macOS/Linux shell 命令有效 | PASS | 更新 README 时已重新执行 |
| 中英文 README 命令保持一致 | PASS | 两个文件使用同一组命令 |
| README 命令与当前公开测试入口一致 | PASS | 只保留了已经验证过的真实测试名 |

## BackendFactory 接口

```go
type BackendFactory interface {
    Kind() string
    Capabilities() Capabilities
    Create(ctx context.Context, t *testing.T) *Backend
}
```

要添加新的 backend，请实现该接口，并在 `ResolveBackends` 或 `ResolvePair`
中注册。
