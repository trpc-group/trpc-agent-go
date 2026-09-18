# Replay 一致性测试框架

`replaytest` 是后端无关的 Session / Memory / Summary / Track 回放一致性测试框架。它用同一组操作驱动多个后端，读取结构化快照，完成归一化比较并生成 JSON 差异报告。

## 核心能力

| 组件 | 职责 |
| --- | --- |
| `StandardReplayCases` | 提供 10 条标准回放场景 |
| `Backend` / `Fixture` | 接入并隔离具体后端 |
| `Runner` | 按确定顺序执行操作并采集快照 |
| Normalizer | 处理自动 ID、时间、JSON 和无序字段 |
| Comparator | 生成字段级差异并校验 `allowed_diff` |
| Reporter | 输出稳定排序的 JSON 差异报告 |

标准场景覆盖单轮及多轮对话、工具调用、State、Memory、Summary、事件截断、Track、并发写入和异常恢复，并额外保证：

- Memory 覆盖两个逻辑 scope、正向检索和跨 scope 负向检索。
- Tool 场景同时校验 call、response、response extra 及 `trpc_agent.tool_call_args` args extension。
- Summary 覆盖双 Session 归属、覆盖更新、filter-key，以及后端生成的 version、boundary 和 updated-at。`set_replay_window` 根据已持久化的 Summary boundary 构造回放窗口，不删除底层 Event。
- 并发场景通过 barrier 释放跨 Session 写入，并在同一 Session 中受控追加时间/ID 乱序的 tool、sub-agent 和 assistant 事件；不变量同时校验追加顺序和规范化时间等级。
- 每条标准场景都包含 Snapshot invariant，避免多个后端共同丢失或错误地保存数据时仍比较通过。
- 恢复场景覆盖 before-write 和 after-write 重试；真实后端 fixture 另有 Event、State、Summary 的定向 uncertain-commit 测试。

## 快速使用

```go
normalizeOptions := replaytest.DefaultNormalizeOptions()
normalizeOptions.PreserveEventIDs = true // StandardReplayCases 使用显式 Event ID。
runner := replaytest.Runner{
	Backends:        []replaytest.Backend{baseline, candidate},
	NormalizeOptions: normalizeOptions,
	CompareOptions:   replaytest.DefaultCompareOptions(),
}
report, err := runner.Run(ctx, replaytest.StandardReplayCases())
```

调用方负责实现 `Backend` / `Fixture`，并为每条场景提供隔离的数据空间和清理逻辑。本包不直接依赖 SQLite、Redis、Postgres、MySQL 或 ClickHouse。

## 比较规则

- 只归一化自动生成或后端私有字段；标准矩阵保留 fixture 显式指定的 Event ID，防止后端篡改被逻辑 ID 映射掩盖。
- `TimePrecision` 覆盖 Session/Summary/Track/Memory 时间及 Memory `metadata["event_time"]`；Memory 空检索结果统一规范化为 `[]`；开启 `NormalizeToolCallIDs` 时，`trpc_agent.tool_call_args` 的 tool-call-ID key 也会使用同一逻辑 ID 映射。
- Track duration 保留原值，比较器默认使用 `1ms` 绝对误差容限，不做分桶。
- 未声明的差异默认失败，并生成非空 `Explanation`。
- `allowed_diff` 必须精确绑定 backend、case 和 path/capability；未知 backend/case、通配路径、重复规则和未消费规则均视为配置错误。
- `MarshalReport` 使用 `baseline_missing` / `actual_missing` 明确表示字段不存在，缺失侧不输出 `baseline` / `actual` 值；显式 JSON `null` 仍写入 `baseline` / `actual`。报告 JSON 解码回 `Report` 后仍保留缺失标记，`baseline` / `actual` 中的 JSON number 以 `json.Number` 保留精度，重新调用 `MarshalReport` 不会把 missing 退化为 `null` 或改变数字文本。
- 无法解码的 raw JSON 使用 `baseline_invalid_json_raw` / `actual_invalid_json_raw` 标记，避免和合法业务字符串或对象值碰撞。
- `Runner.Run` 会在创建任何 Fixture 前校验完整 case 配置，包括 Operation 负载、顶层依赖、并发依赖图和 Snapshot invariant 形状。
- Operation 中的 JSON-like 负载会在进入 Fixture 前深拷贝。包含私有可变字段、无法安全隔离的自定义结构会在校验阶段被拒绝。

## 后端集成

InMemory、SQLite 及可选外部后端的真实适配、运行命令和能力差异说明位于 [`test/replayconsistency`](../../test/replayconsistency)。

## 测试

```bash
go test ./session/replaytest -count=1
go test -race ./session/replaytest -count=1
```
