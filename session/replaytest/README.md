# session/replaytest — Replay 一致性测试框架

用同一组**确定性脚本**驱动多个 Session / Memory 后端，读出事件、State、Memory、Summary、Track 五维快照，归一化后逐字段比较，判定后端行为是否一致。它既是回归测试工具，也是后端实现质量的基准。

```
Case (确定性脚本) ──► Runner ──► Snapshotter ──► Normalizer ──► Differ ──► Reporter
                   每个 Target       读回五维数据     抹平合法噪声    字段路径级   JSON 报告
                   回放同一脚本                                     diff
```

## 快速开始（轻量模式，≤30s）

```bash
# 框架自测：InMemory vs InMemory（应 0 diff）+ 归一化器/比较器/报告器单测 + 变异检出矩阵
cd session/replaytest && go test ./...

# 轻量模式验收：InMemory（参照）vs SQLite（候选），11 条公开 case
cd session/replaytest && go test ./sqlite/ -run TestReplayConsistencySQLite -v

# 可选：把差异报告写到任意路径
cd session/replaytest && REPLAY_REPORT_OUT=/tmp/diff_report.json \
  go test ./sqlite/ -run TestReplayConsistencySQLite
```

无需任何外部服务；SQLite 使用临时文件数据库，每个 case 独立隔离。

## 集成模式（环境变量开启）

| 后端 | 环境变量 | 示例 |
|------|----------|------|
| Redis | `TRPC_REPLAYTEST_REDIS_URL` | `redis://localhost:6379` |

未设置变量时测试自动 `t.Skip`。Redis 另有一个免服务器的冒烟测试（miniredis），CI 默认运行：

```bash
cd session/replaytest && go test ./redis/ -v   # miniredis 冒烟 + 真实 redis 按需开启
```

**验证状态说明**：Redis 绑定目前只在 miniredis（进程内模拟）上通过；真实 Redis 服务器路径（`TRPC_REPLAYTEST_REDIS_URL`）在本仓库 CI 中未运行，miniredis 不覆盖真实服务器的序列化、TTL 与持久化语义，指向真实实例前请自行验证。

Redis 绑定用**随机 run ID + 轮转序号**的 key 前缀隔离 case（`replaytest:<name>:<runID>:<seq>`，run ID 每个 target 进程内随机生成一次），跨进程绝不重复，下次运行不会复用旧 key；同时绝不 flush 服务器，可安全指向共享实例。Session 侧 key 带 1 小时 TTL（`WithSessionTTL`/`WithAppStateTTL`/`WithUserStateTTL`），到期自动清理；`memory/redis` 没有 TTL 选项，memory 侧 key 不会自动过期——请勿长期指向生产实例，或定期清理 `replaytest:*` 前缀。

## 后端接入（三步）

框架与全部后端绑定都住在 `session/replaytest/` 这一个**独立 Go module** 里（自带 `go.mod`）。它是叶子 module：通过 `replace` 同时依赖根 module 和各后端 module，因此既能 import `session/sqlite`、`session/redis` 等后端，又不让根 module 反向依赖它们（根 module import 后端 module 会构成循环依赖）。接入新后端**不需要改动任何既有 module 的文件**。

1. 在本目录下新建子包（参考 `session/replaytest/sqlite/target.go`），实现 `replaytest.Target` 接口：

```go
type Target struct { /* ... */ }

func (t *Target) Name() string                  { return "sqlite" }
func (t *Target) Caps() replaytest.Capability   { return replaytest.CapAll } // 如实声明
func (t *Target) SessionService() session.Service { return t.sess }
func (t *Target) MemoryService() memory.Service   { return t.mem }
func (t *Target) Reset(ctx context.Context) error { /* 清场：新库/新前缀 */ }
```

2. 写接入测试：

```go
func TestReplayConsistency(t *testing.T) {
    ref := replaytest.NewInMemoryTarget("inmemory")
    defer ref.Close()
    cand, _ := NewTarget("mybackend") // 你的 Target 工厂
    defer cand.Close()
    replaytest.RunPairT(t, cases.All(), ref, cand)
}
```

3. 能力不齐全时在 `Caps()` 关闭对应字段（如 `c := replaytest.CapAll; c.Tracks = false`）；相关 case 在报告中记为 `unsupported` 并附 note 说明属 allowed_diff，不算失败。

跨 module 依赖（如 session 后端需要搭配对应的 memory 后端）参照 `session/replaytest/go.mod`：为每个被依赖的后端 module 加一行 `require` 和一行指向本地目录的 `replace`，然后 `go mod tidy`。

## 11 条公开 replay case

全部位于 `session/replaytest/cases/`，经 `cases.All()` 导出，新增后端零改动复用：

| # | Case | 验证点 |
|---|------|--------|
| 1 | `basic/single_turn` | author/role/content 保真，不丢不重 |
| 2 | `basic/multi_turn_order` | 读出顺序 == 写入顺序 |
| 3 | `toolcall/full_cycle` | tool call/response 配对、args canonical 相等、branch/tag/filterKey/stateDelta/extensions 五字段 |
| 4 | `state/overwrite_delete_clear` | 覆盖顺序、删除语义、空写 no-op、app/user 作用域清空至空、temp/app/user 合并视图、事件 state delta 合并入最终 state |
| 5 | `memory/write_read` | 偏好/事实/经验/历史摘要四类 memory 的增改删（更新同时改 content/topics/metadata，终态快照可检出序列化缺陷）+ 检索结果集一致；清空语义由 case 11 覆盖 |
| 6 | `summary/generate_update` | 摘要覆盖（v2 替 v1）、filter-key 隔离、session 归属、版本/边界字段 |
| 7 | `summary/truncation_retain` | 长历史摘要 + 新事件后做两种读回：全量事件列表与 `WithEventNum` 截断上下文窗口（压缩对话真实回放路径）；两视图均需保持 保留事件 + summary + 新事件连贯，boundary 锚定在窗口外的事件上 |
| 8 | `track/tool_and_subtask` | track 名隔离、时序保持、payload（含错误字段）保真；耗时字段归一化时抹除 |
| 9 | `concurrency/interleaved_append` | 并发交错写：集合相等 + 同 branch 偏序保持（全局全序豁免） |
| 10 | `recovery/dirty_retry` | 写入经服务边界真实失败 N 次后重试、事件恰好落库一次；同内容重复写两端一致（不静默去重）；重试/重复写之后 summary 生成与归属一致；客户端重试重复提交同一 memory 两端一致处理；错误类别一致；后端级静默丢写与"事件落库但 state delta 丢失"脏写的端到端检出由 `e2e_fault_test.go` 验证 |
| 11 | `memory/scope_isolation` | 同一 app 下两个 user 写入/更新/删除/清空 memory；按 user 分桶读回，跨 user 不泄漏、清空只影响本 user（scope 归属参与比较） |

## 比较规则

### 归一化（只抹平"同语义的不同表示"）

| 维度 | 规则 |
|------|------|
| 时间戳 | 丢弃；顺序由列表位置/序号表达 |
| UUID / 生成 ID | 按排序后符号化（`evt#1`、`inv#2`、`call#1`、`mem#1`），引用处（ToolID、ToolCalls、LongRunningToolIDs、ParentInvocationID、summary boundary.last_event_id）同步替换 |
| `json.RawMessage` | canonical JSON：key 排序、数字归一（`1` ≡ `1.0`）、耗时键替换为 `"*"`（内置 `duration_ms`/`latency_ms`/`elapsed_ms` 等 5 个精确键、任意 `_ms` 后缀键、以及小写后为 `duration`/`latency`/`elapsed` 的键；`timeout` 等非耗时键不抹除） |
| State | 按键逐一比较（canonical 值） |
| Memory | 按 (user, content) 排序后逐一比较，scope 归属（user_id）参与比较；返回顺序差异不抹掉，记为 allowed_diff note（见下） |

### Summary 比较

以 **filter-key** 为键，比较文本、topics、Boundary 版本与 last_event_id（符号化）、session 归属；`updated_at` 仅断言存在性。覆盖、隔离、归属三类语义由 case 6 专项验证；对应的变异回归用例见 `mutate_test.go`。此外，每个 summary case 的步骤结束后，Runner 会新建一个**探针 session** 读回并断言其不含任何摘要——后端若把摘要写到错误的作用域（如按 app/user 而非 session 键控），泄漏对 case 自身快照不可见，但逃不过探针（`runner.go` `verifySummaryIsolation`，端到端验证见 `e2e_fault_test.go`）。

### Track 比较

以 **(track 名， 序号）** 为键比较 canonical payload；时间序列顺序必须保持；耗时字段在归一化阶段即抹除。

### allowed_diff（显式，其余一律 fail）

时间戳、生成 ID、耗时字段在归一化阶段即被抹除，根本不会进入比较；错误只比较类别、不比较文案；并发 case 的全局全序通过 multiset + per-branch 偏序模式天然豁免。真正进入报告、被显式标记为 `allowed: true` 的差异只有两类：

1. Memory / 检索结果的**返回顺序**（实现相关，如时间戳精度）；差异记入报告 `notes`，不判失败，内容集合本身仍严格比较。
2. Case 通过 `FloatDelta` 显式声明的**浮点容差**：被比较的 JSON 值（state、state delta、extensions、tool call args、track payload）内，数值差不超过 delta 时记 allowed note；比较用精确十进制（big.Rat）而非 float64，超限值（如 `1e1000000`）直接拒绝而非容忍。默认 `FloatDelta = 0`，即数字精确比较。

能力缺失记 `unsupported` 并附 note 说明属 allowed_diff，不计失败。

## 差异定位

每条 diff 携带：`dimension` / `severity` / `session_id` / `event_index` / `filter_key` / `track_name` / `memory_id` / `path`（字段路径，如 `events[3].tool_calls[0].args`）/ `value_a` / `value_b` / `allowed` / `note`。

本目录下的 `session_memory_summary_track_diff_report.json` 是一份真实生成的示例报告：参照端 InMemory，对比端注入了「静默丢写 + memory 列表反转」两类确定性故障的 InMemory，包含 fail case 的字段路径与两端值、以及一条 allowed_diff note（memory 返回顺序）。重新生成（结果逐字节一致）：

```bash
cd session/replaytest && REPLAY_REPORT_OUT=session_memory_summary_track_diff_report.json \
  go test . -run TestGenerateExampleReport
```

真实后端对比的报告用 `REPLAY_REPORT_OUT=<路径>` 环境变量让对应测试写到任意路径（见「快速开始」）。

## 检出率与误报的回归自证

以下是框架自身的回归测试，用于防止改动破坏比较链路；它们按比较器代码路径选取故障注入点，**不构成对「所有可能不一致都能检出」的证明**。

- **比较器层**：`mutate_test.go` 对每条公开 case 施加 9 类变异（丢事件、同 branch 换序、state 污染、summary 丢失/过期覆盖/filter-key 错/归属错、memory 污染、track 污染），断言 Differ 必出非 allowed diff 且维度、路径正确。变异按比较器代码路径去重，每条路径一个代表。
- **端到端**：`e2e_fault_test.go` 在真实 `session.Service` / `memory.Service` 边界注入四类故障——静默丢写（ack 成功但未落库）、脏半写（事件落库但 state delta 丢失）、memory 归属错（读回 entry 的 UserID 与写入作用域不符）、跨 session 摘要泄漏（摘要从新建 session 中可读），断言 Runner → Snapshot → Normalize → Diff 全链路判 fail 且维度正确（丢写 → event 维度；丢 delta → event + state 双维度；归属错 → memory 维度；摘要泄漏 → 探针直接终止该 case 运行），堵住"快照漏读/归一化误抹"导致的证明链缺口。
- **误报（目标 ≤5%，显式断言）**：设计上靠符号化归一与稳定轮询消除合法噪声；`selftest_test.go` 的 `TestFalsePositiveRateWithinBudget` 将全套件在双 InMemory 实例上重复 10 轮（110 次 case 运行），显式断言失败率 ≤5%（实测 0）；并发 case 另由 `TestSelfTestConcurrentStability` 重复 100 次验证无 flaky。轻量模式 ≤30s 同样落成显式断言：`sqlite/replay_test.go` 的 `TestReplayConsistencySQLite` 计时并断言全套件耗时 < 30s（实测约 6–9s）。

## 局限性

- **一致性 ≠ 正确性**：harness 只证明候选后端与参照后端（InMemory）行为相同。若参照后端本身在某维度上就错了（例如 summary version 不递增），两端一致也会判 pass。后端正确性由各自的单元测试负责。
- **State 的"清空"作用域**：`session.Service` 没有 session 级删 key 接口（只有 `UpdateSessionState` 和 app/user 作用域的 delete），所以 case 4 的"清空"在 app/user 作用域验证（删掉所有已写 key、断言读回为空）；session 作用域的 key 只能覆盖、不能删除，这一语义差异不参与比较。
- **Memory search 跨后端语义**：不同后端的检索实现（分词关键词 vs 向量）结果集**合法地不同**，case 5 的检索结果集比较只适用于关键词检索语义的后端。向量类后端应在 `Caps()` 关闭 `MemorySearch` 退出该维度，退出后 case 5 对其只验证增删改与列表读回。
- **Track 的 invocation 关联**：`session.TrackEvent` 只有 track 名、payload、timestamp 三个字段，没有独立的 invocation 字段；case 8 通过 payload 内嵌字段验证透传保真，不验证后端对 invocation 的专门处理。
- **Memory scope 的覆盖边界**：case 11 验证了同一 app 下多 user 的写入隔离、更新/删除归属和 scoped 清空；app 维度固定为 `replay-app`（runner 统一命名，跨后端无可分歧输入），不参与比较。
- **Case 10 的失败注入是确定性的**：瞬时失败由 runner 的服务装饰器在 `AppendEvent` 边界注入，验证"失败-重试-恰好落库一次"的客户端路径；它不模拟后端内部的部分提交。因此这条 case **检不出真实后端的非原子写入 bug**（如事件落库但 state delta 丢失），它只验证两个后端对同一失败/重试脚本行为一致；脏半写的检出能力由 `e2e_fault_test.go` 的装饰器模拟证明，同样不等于真实后端故障。
- **Summary 文本的一致性边界**：`OpSummary` 调用 `CreateSessionSummary` 时传的是 runner 内存中的 session 对象（各后端相同输入），因此 summary 文本一致不证明后端事件存储正确；后端事件存储由事件维度 case 保证，summary 与后端持久化事件的联动由 case 7 的 `WithEventNum` 截断读回覆盖。
- **syncPoint 的稳定轮询退出假定写同步可见**：读回同步点连续 3 次计数不变即停止等待，这假定后端写入同步可见；对未来异步持久化后端（如某些列存），可能把"写入尚未可见"误判为事件缺失 diff 或提前通过，接入此类后端时应调大 `PollTimeout` 或改用其他同步手段。

## 设计说明（约 210 字）

以 InMemory 为参照，把确定性脚本回放到各后端，读回事件/State/Memory/Summary/Track 快照后归一化逐字段比较。归一化只抹平语义等价表示：时间戳丢弃，ID 符号化并同步改写引用，JSON canonical 化，耗时键抹除；浮点按 FloatDelta 以 big.Rat 判容差。Summary 按 filter-key 比文本、版本、Boundary 与归属，updated_at 仅断言存在，探针 session 专查跨 session 泄漏。Track 按（名，序号）比 canonical payload，时序须保持。allowed_diff 仅 memory 返回顺序与容差内浮点，记 note 不判失败；能力缺失记 unsupported。框架是独立叶子 module，replace 依赖根及后端 module；新后端实现 Target 接口三步接入，轻量模式 ≤30s，Redis 经环境变量开启。
