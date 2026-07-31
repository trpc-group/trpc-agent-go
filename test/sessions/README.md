# Session / Memory 多后端回放一致性测试

本目录实现一个后端无关的 replay consistency harness。它把同一组 JSONL 业务轨迹依次写入 InMemory、SQLite 和 Redis，再通过公开的 Session/Memory Service API 读回真实持久化数据，生成统一快照并比较 Event、State、Memory、Summary 与 Track 语义。

测试关注存储、恢复和回放结果，不评价模型回答质量。Summary 使用确定性 summarizer，默认 Redis 使用 miniredis，因此本地运行不需要外部服务或 API Key。

## 工作流程

~~~text
JSONL fixtures
      │
      ▼
加载与校验 ──► 时间戳整体平移
      │
      ▼
为每个 case 创建隔离后端
      │
      ▼
顺序执行 ReplayAction
      │
      ▼
通过公共 API 重新读取
      │
      ▼
Snapshot ──► Normalize ──► Compare
                               │
                  allowed_diff / mutation check
                               │
                               ▼
                         JSON Report
~~~

每条 case × backend 轨迹遇到错误后立即停止，但 Runner 会继续执行其他后端和后续 case，最后统一生成完整报告。只有 context 取消或超时等全局终止条件会提前停止。

## 文件结构

| 文件 | 职责 |
| --- | --- |
| replay.go | JSONL 模型、动作校验、后端工厂、回放执行、快照收集和故障注入 |
| normalize.go | 消除已知的非业务表示差异 |
| compare.go | 递归比较快照、保留字段存在性并应用 allowed_diff |
| runner.go | 编排 case/backend 矩阵、隔离资源并聚合错误 |
| report.go | 汇总指标并原子写出 JSON 报告 |
| *_test.go | 单元测试与完整一致性回归测试 |
| testdata/replay_cases/*.jsonl | 后端无关的标准输入轨迹 |
| DESIGN.md | 150–300 字核心设计说明 |

## 当前覆盖范围

当前共有 20 个 replay case：

| Case | 覆盖内容 |
| --- | --- |
| 01 | 单轮 user/assistant 对话 |
| 02 | 多轮事件及读取顺序 |
| 03 | tool call 与 tool response |
| 04 | 初始 State、state delta 和直接更新 |
| 05 | Memory 新增、ID 旋转更新与删除 |
| 06–07 | Summary 首次生成和增量更新 |
| 08 | 写入前失败及重试 |
| 09 | Memory Add/Update/Delete 写后结果确认 |
| 10–12 | Summary 丢失、覆盖、错误 Session 归属的变异检测 |
| 13 | 长会话触发异步 Summary 并等待落库 |
| 14 | Summary 边界与后续 Event tail 重建上下文 |
| 15 | Event 写后歧义确认，避免重复事件 |
| 16 | State/Summary 失败恢复与脏状态检测 |
| 17 | Summary filter-key 隔离 |
| 18 | 工具耗时、子任务状态和异常 Track |
| 19 | 工具/子 Agent 交错写入与父子关联顺序 |
| 20 | 相同正文、不同事件身份的 Memory 引用 |

当前快照覆盖：

- Event：ID、顺序、author、role、content、tool call/response、父子 invocation、filter-key、state delta、timestamp。
- State：key 是否存在及对应 JSON 值。
- Memory：ID、content、topics、kind、event time、participants、location。
- Summary：filter-key、content、topics、Session 归属、version、updated time、cutoff 和 last event 边界。
- Track：track name，以及每条事件的 index、timestamp、JSON payload。

事件分页和 TTL 目前没有独立 ReplayAction，也不在当前报告中标记为 unsupported；新增这些能力时需要同时补充动作模型、后端能力判断、快照字段和 fixture。

## 运行测试

从独立的 test Go module 运行：

~~~bash
cd test
go test ./sessions -count=1
~~~

只运行完整回放矩阵：

~~~bash
go test ./sessions -run '^TestReplayConsistency$' -count=1 -v
~~~

运行整个 test module：

~~~bash
go test ./... -count=1
~~~

默认报告位于测试临时目录，测试结束后会被清理。需要保留报告时指定：

~~~bash
REPLAY_REPORT_PATH=./artifacts/session_memory_summary_track_diff_report.json \
  go test ./sessions -run '^TestReplayConsistency$' -count=1 -v
~~~

成功时终端显示 PASS。失败时先查看测试输出中的 case、backend、JSON Path，再打开报告中的 cases[].runs、comparisons 和 mutations。

## 后端和环境变量

默认矩阵为：

~~~text
inmemory + sqlite + redis(miniredis)
~~~

可用环境变量：

| 变量 | 作用 |
| --- | --- |
| REPLAY_BACKENDS | 逗号分隔的后端白名单，当前只接受 inmemory,sqlite,redis |
| REPLAY_SKIP_INMEMORY | 跳过 InMemory |
| REPLAY_SKIP_SQL / REPLAY_SKIP_SQLITE | 跳过 SQLite |
| REPLAY_SKIP_REDIS | 跳过 Redis |
| REPLAY_REDIS_URL | 使用真实 Redis，例如 redis://127.0.0.1:6379 |
| REPLAY_SQLITE_DIR | 保留 SQLite 临时数据库的根目录 |
| REPLAY_REPORT_PATH | 保留 JSON 报告的路径 |

Skip 变量使用 Go strconv.ParseBool 解析。示例：

~~~bash
# 最快的单后端检查
REPLAY_BACKENDS=inmemory go test ./sessions -count=1

# InMemory 与 SQLite 对比
REPLAY_BACKENDS=inmemory,sqlite go test ./sessions -count=1

# 使用真实 Redis
REPLAY_BACKENDS=inmemory,redis \
REPLAY_REDIS_URL=redis://127.0.0.1:6379 \
go test ./sessions -count=1
~~~

SQLite 为每个 case 分别创建 Session DB 和 Memory DB；Redis 使用 replay:<run-id>:<case-id> 前缀，防止不同运行相互污染。

## Replay Case 格式

每个文件为 JSONL，一行一个对象；首行必须是 schema version 1 的 metadata：

~~~json
{"action":"metadata","version":1,"id":"example","description":"example case"}
{"action":"create_session","session_id":"session-1","state":{"mode":"test"}}
{"action":"append_event","session_id":"session-1","event":{"id":"event-1","role":"user","content":"hello","timestamp":"2026-01-01T10:00:00Z"}}
{"action":"checkpoint","checkpoint":"after-message"}
~~~

支持的动作：

| Action | 功能 |
| --- | --- |
| create_session | 创建 Session 和初始 State |
| append_event | 追加文本、工具或子 Agent Event |
| append_track | 追加 Track 观测事件 |
| update_state | 覆盖、写入或删除 State key |
| add_memory / update_memory / delete_memory | Memory CRUD |
| create_summary | 同步生成指定 filter-key 的 Summary |
| enqueue_summary | 入队 Summary，可配置 await 和 timeout |
| assert_session / assert_memory | 检查单后端不变量 |
| checkpoint | 保存中间持久化快照 |
| allow_diff | 声明有原因、可审计的后端差异 |

Fixture 时间表达相对顺序。Runner 会整体平移 Event 和 Track 时间，保持间隔及顺序不变，同时避免 Summary cutoff 早于 Session 创建时间。

## 故障注入

写操作可携带：

~~~json
{"failure":{"fail_before":true,"retry":true}}
{"failure":{"fail_after":true,"retry":true}}
{"failure":{"duplicate":true}}
~~~

- fail_before：操作执行前失败；启用 retry 时执行一次真实操作。
- fail_after：操作已成功，但模拟调用方没有收到成功结果。
- duplicate：主动执行两次，用于暴露非幂等行为。

对于 Event、Summary 和 Memory，fail_after + retry 会先通过读接口确认写入结果。Memory Add/Update 检查有效 ID 及业务后镜像，Delete 检查原 ID 已不存在；确认成功后不会再次写入。

## 归一化和比较

TestReplayConsistency 启用：

~~~go
NormalizeOptions{
    NormalizeGeneratedMemoryIDs: true,
    NilEqualsEmpty:              true,
}
~~~

归一化规则：

- 清除快照中的 backend 名称。
- 时间统一为 UTC；State、state delta、tool arguments 和 Track payload 规范为稳定 JSON。
- 可选地统一 nil 与空 map/slice。
- Summary topics、Memory topics/participants 按无序集合排序。
- Track 流按名称排序，但每个 Track 内事件顺序不变。
- Memory 先按业务元组排序，再将后端生成 ID 替换为 memory-001 等稳定值。
- Event 顺序、Track 内部顺序、State key 是否存在均属于业务语义，不会被模糊处理。

比较器递归检查 map、slice 和标量，并区分“字段缺失”和“字段存在但值为 null”。差异会定位到 JSON Path，并附带 Session ID、Event index 或 Summary ID 等上下文。

### Summary 比较

Summary 以 Session 下的 filter-key map 为作用域，比较正文、topics、Session 归属、version、更新时间、cutoff 时间和 last event ID。filter-key、版本、归属和边界不会被模糊跳过。

### Track 比较

Track 名称用于稳定排列不同观测流；同一 Track 中按存储顺序比较 index、timestamp 与规范化后的 payload。这样既消除 map 遍历顺序差异，也保留工具执行和子任务状态的真实先后关系。

### allowed_diff

规则必须包含 path 和 reason，backend 可选：

~~~json
{"action":"allow_diff","allowed_diff":{"path":"$.memories[*].id","backend":"redis","reason":"documented backend-specific representation"}}
~~~

星号匹配一个 JSON Path 片段。规则只对指定 actual backend 生效；省略 backend 时对所有对比后端生效。命中的差异仍写入报告并标记 allowed=true，不会计入 unexpected diff。不要用宽泛规则掩盖 Event 顺序、Summary 归属或 State key 缺失。

## 报告

ReplayReport 包含：

- status、error 和 generated_at；
- case/backend/comparison 数量和总耗时；
- unexpected/allowed diff 数量；
- mutation 检出率；
- 每个 backend 的 action 结果、checkpoint 和最终快照；
- reference/actual 后端、字段路径、两侧值及差异解释。

报告通过“临时文件 + rename”原子发布，不会留下半写入 JSON。

## 接入新后端

新后端只需在测试模块实现以下适配接口：

~~~go
type Backend interface {
    Name() string
    SessionService() session.Service
    MemoryService() memory.Service
    Close() error
}

type BackendFactory interface {
    Name() string
    Create(context.Context, BackendConfig) (Backend, error)
}
~~~

接入步骤：

1. 在 replay.go 实现 Factory，为每个 case 创建隔离的 Session/Memory Service。
2. 使用 BackendConfig 的 case ID、临时目录和 key prefix 隔离数据。
3. 在 BackendFactoriesFromEnv 的 available 表中注册名称；需要时增加对应 DSN 和 skip 配置。
4. Close 必须释放连接、临时服务和后台 worker。
5. 若写入具有最终一致性，应提供明确的 flush/await 机制，不能依赖固定 sleep。
6. 先用单后端验证 fixture，再加入至少两个后端的比较矩阵。

如果后端不支持 Track、Summary filter-key、分页或 TTL，应先定义明确的能力表达和报告行为，再加入默认矩阵；当前实现不会自动把缺失能力转换成 allowed diff。
