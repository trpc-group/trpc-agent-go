# Session / Memory 多后端回放一致性测试

本目录将同一组公开操作写入不同的 Session / Memory 后端，再比较 Event、State、Memory、Summary、Track 和 Memory Search 的归一化快照。默认轻量矩阵使用 InMemory 与 SQLite，不依赖外部服务。

## 快速开始

SQLite 驱动依赖 CGO。在仓库根目录运行：

```powershell
cd test
$env:CGO_ENABLED = "1"
go test ./replayconsistency -run TestLightweightReplayMatrix -count=1
```

轻量矩阵设有 30 秒上下文超时，测试通常在 1 秒内完成；首次 CGO 编译时间不计入矩阵执行时间。

## 场景覆盖

| Case | 覆盖语义 |
| --- | --- |
| `single-turn` | user message 与 assistant text event |
| `multi-turn` | 多轮追加及读取顺序 |
| `tool-call` | tool call、response、args extension |
| `state-update` | 写入、覆盖、删除和清空 |
| `memory-read-write` | 多 scope 写入、读取和检索 |
| `summary-update` | 内容、filter-key、版本、归属和覆盖 |
| `summary-truncation` | Summary、保留事件和后续事件共同回放 |
| `track-events` | 调用关联、时序、错误和耗时 |
| `concurrent-out-of-order` | 跨 Session 并发及同 Session 时间/ID 乱序追加后的确定性结果 |
| `failure-retry` | 写入失败、重复写入和幂等重试 |

## 可选集成后端

设置一个或多个环境变量后，在 `test` module 运行 `go test ./replayconsistency -count=1`：

| 后端 | 环境变量 | Memory 配对 |
| --- | --- | --- |
| Redis | `TRPC_AGENT_GO_REPLAY_REDIS_DSN` | Redis |
| Postgres | `TRPC_AGENT_GO_REPLAY_POSTGRES_DSN` | Postgres |
| MySQL | `TRPC_AGENT_GO_REPLAY_MYSQL_DSN` | MySQL |
| ClickHouse | `TRPC_AGENT_GO_REPLAY_CLICKHOUSE_DSN` | InMemory |

未配置的后端会明确标记为 skipped，不影响默认 CI。每条 case 使用独立 fixture；测试结束后会删除 Session、清空测试 Memory 并关闭服务。

Memory fixture 将逻辑 `(app_name, user_id)` 映射为稳定且隔离的物理 key。读取和检索结果在恢复逻辑 scope 前会先校验物理归属；`SearchOptions` 同时传入 Query、limit 和 similarity threshold，避免高级选项覆盖查询内容。

## 后端能力差异

当前 ClickHouse Session 未实现 `session.TrackService`。已验证的 ClickHouse 25.3 镜像还存在 Summary JSON 读取不兼容和 Session TTL 无法在确定期限内验证的问题。相关 case/probe 会输出精确的 `unsupported` allowed-diff；Event、State、Memory 等其余能力仍严格比较。本测试框架不会修改生产 ClickHouse 实现。

ClickHouse 25.3 使用新 `JSON` 类型时，DSN 需包含：

```text
output_format_native_write_json_as_string=1&input_format_binary_read_json_as_string=1&output_format_json_quote_64bit_integers=0
```

要求四个外部后端全部配置并同场运行：

```powershell
$env:TRPC_AGENT_GO_REPLAY_REQUIRE_EXTERNAL = "1"
go test ./replayconsistency -run TestRequiredExternalReplayMatrix -count=1
```

TTL 是独立慢探针，不计入轻量模式的 30 秒预算：

```powershell
$env:TRPC_AGENT_GO_REPLAY_RUN_TTL = "1"
go test ./replayconsistency -run TestOptionalIntegrationTTLProbes -count=1
```

## 设计说明（150–300 字）

框架用十类后端无关操作驱动隔离 fixture，并读取统一快照。归一化处理自动 ID、时间戳、JSON/map 顺序及私有 metadata；标准轨迹中的显式 Event ID、事件顺序和业务字段严格保留。Memory 保留检索排名，仅容忍微小相似度误差；Summary 分别检查文本语义、filter-key、版本、归属和覆盖；Track 检查名称、类型、调用、错误及时序，仅容忍微小耗时误差。同 Session 乱序轨迹用明确依赖和时间等级避免抖动。`allowed_diff` 必须绑定后端、场景、能力或字段路径并说明原因，未声明或未消费规则均失败。轻量模式使用 InMemory 与 SQLite，外部后端由环境变量启用。

## 故障检测

`write_fault_test.go` 在真实 fixture 的 `Apply` / `ApplyWithFault` 写边界注入故障，不修改读取后的 Snapshot。10 条标准场景分别验证目标 fault 能被检出并定位到预期 path；Summary 额外覆盖 missing、overwrite、wrong-session 和 wrong-filter-key。

异常恢复标准场景注入 after-write Memory 故障；定向测试另覆盖 Event 重复、State 脏 key，以及 Summary 重复/错误覆盖、归属、filter-key、版本、更新时间和 boundary。State 与 Summary 测试同时运行 InMemory、SQLite。Operation 在注入前执行深拷贝，避免 baseline、candidate 或并发子操作之间共享污染。

## 差异报告

报告按 case、backend 和字段路径稳定排序，并通过 locator 定位 Session ID、Event index、Memory ID/scope、Summary filter-key 或 Track name。每条差异均包含 baseline、actual、`allowed_diff` 状态和非空 explanation。

示例见 [`testdata/session_memory_summary_track_diff_report.json`](testdata/session_memory_summary_track_diff_report.json)。

`allowed_diff` 不是通配跳过机制。规则缺少 case、backend、path 或 explanation 时，比较器直接返回错误；后端能力缺失也默认产生非允许差异。
