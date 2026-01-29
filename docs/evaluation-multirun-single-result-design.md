# Evaluation 多次运行（NumRuns）单文件落盘：改动说明

## 行为改动

- 每次评估执行只会生成并保存一份 `EvalSetResult`（即只产生一个 evalresult 文件）；当 `NumRuns > 1` 时，该文件会包含多次 run 的结果明细与汇总。
- `Service.Evaluate` 只负责“评估一次 evalset（一次 run）”，返回单次 run 的结果 `EvalSetRunResult`，不负责持久化。
- `AgentEvaluator` 负责收集 `NumRuns` 次 run 的结果，聚合为一个 `EvalSetResult`，最终只调用一次 `evalresult.Manager.Save` 落盘。

## 数据结构改动（向后兼容扩展）

- 当 `NumRuns > 1` 时，`EvalSetResult.EvalCaseResults` 会包含所有 runs 的明细，因此同一 `EvalID` 可能出现多条记录。
- 为了让多次运行的结果能被稳定、可调试地聚合，`EvalCaseResult` 新增 `runId` 字段，用于标识该条结果属于第几次 run。
- 为了便于用户直接查看“按 evalset / 按 evalcase 聚合”的统计指标，`EvalSetResult` 新增 `summary` 字段。

### `EvalSetResultSummary` 结构体定义

```go
type EvalSetResultSummary struct {
	OverallStatus     status.EvalStatus         `json:"overallStatus,omitempty"`     // 聚合后的整体状态（跨所有 case 与所有 runs）。
	NumRuns           int                      `json:"numRuns,omitempty"`           // 本次结果中包含的 evalset 运行次数（对应 NumRuns）。
	RunSummaries      []*EvalSetRunSummary     `json:"runSummaries,omitempty"`       // 每次 evalset run 的汇总信息（含 run 级别状态与指标聚合）。
	RunStatusCounts   *EvalStatusCounts        `json:"runStatusCounts,omitempty"`    // 统计各次 evalset run 的整体状态分布（对象是 evalset run 本身）。
	EvalCaseSummaries []*EvalCaseResultSummary `json:"evalCaseSummaries,omitempty"` // 每个 evalcase 跨多次 run 的汇总信息（含 run 明细与指标聚合）。
}
```

## 汇总与落盘策略

- 无论 `NumRuns` 是否大于 1，`AgentEvaluator` 都会为每条 `EvalCaseResult` 填充 `runId=1..NumRuns`，并基于 `runId` 生成 `summary`（`NumRuns==1` 时 `runId=1`、`summary.numRuns=1`）。

## 兼容性说明

- `AgentEvaluator.Evaluate` 的返回值仍为 `*EvaluationResult`，`EvaluationResult` / `EvaluationCaseResult` 不做任何修改。
- `evalresult.Manager` 接口保持不变；`EvalSetResult` / `EvalCaseResult` 仅新增字段，不删除/不重命名已有字段。
- `service.Service` 允许不兼容变更：`Service.Evaluate` 的返回值从“落盘的 `EvalSetResult`”调整为“不落盘的 `EvalSetRunResult`”，落盘职责统一上收至 `AgentEvaluator`。
