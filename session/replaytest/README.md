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
- Summary 覆盖双 Session 归属、覆盖更新、filter-key，以及后端生成的 version、boundary 和 updated-at。`set_replay_window` 根据已持久化的 Summary boundary 构造回放窗口，不删除底层 Event。
- 并发场景通过 barrier 同时释放两个跨 Session 写入；同一 Session 内的依赖操作仍按确定顺序执行。
- 每条标准场景都包含 Snapshot invariant，避免多个后端共同丢失或错误地保存数据时仍比较通过。
- 恢复场景同时覆盖 before-write 和 after-write 重试。

## 快速使用

```go
runner := replaytest.Runner{
	Backends:        []replaytest.Backend{baseline, candidate},
	NormalizeOptions: replaytest.DefaultNormalizeOptions(),
	CompareOptions:   replaytest.DefaultCompareOptions(),
}
report, err := runner.Run(ctx, replaytest.StandardReplayCases())
```

调用方负责实现 `Backend` / `Fixture`，并为每条场景提供隔离的数据空间和清理逻辑。本包不直接依赖 SQLite、Redis、Postgres、MySQL 或 ClickHouse。

## 比较规则

- 只归一化自动生成或后端私有字段；Event、State、Memory、Summary 和 Track 的业务语义保持严格比较。
- Track duration 保留原值，比较器默认使用 `1ms` 绝对误差容限，不做分桶。
- 未声明的差异默认失败，并生成非空 `Explanation`。
- `allowed_diff` 必须精确绑定 backend、case 和 path/capability；通配路径、重复规则和未消费规则均视为配置错误。

## 后端集成

InMemory、SQLite 及可选外部后端的真实适配、运行命令和能力差异说明位于 [`test/replayconsistency`](../../test/replayconsistency)。

## 测试

```bash
go test ./session/replaytest -count=1
go test -race ./session/replaytest -count=1
```
