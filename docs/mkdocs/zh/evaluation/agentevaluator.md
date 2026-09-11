# 评估入口 AgentEvaluator

AgentEvaluator 是面向使用方的评估入口。它负责按 `evalSetID` 组织一次评估运行，读取评估集与评估指标，驱动评估服务完成推理与打分，对多次运行的结果做聚合并将结果落盘。

## 接口定义

AgentEvaluator 的接口定义如下。

```go
type AgentEvaluator interface {
	Evaluate(ctx context.Context, evalSetID string, opt ...Option) (*EvaluationResult, error) // Evaluate 执行评估并返回聚合结果
	Close() error                                                              // Close 释放资源
}
```

## 结构定义

`EvaluationResult` 与 `EvaluationCaseResult` 的结构定义如下。

```go
type EvaluationResult struct {
	AppName       string                    // AppName 是应用名
	EvalSetID     string                    // EvalSetID 是评估集标识
	OverallStatus status.EvalStatus         // OverallStatus 是整体状态
	ExecutionTime time.Duration             // ExecutionTime 是执行耗时
	EvalCases     []*EvaluationCaseResult   // EvalCases 是用例结果列表
	EvalResult    *evalresult.EvalSetResult // EvalResult 是持久化的 EvalSetResult
}

type EvaluationCaseResult struct {
	EvalCaseID      string                         // EvalCaseID 是用例标识
	OverallStatus   status.EvalStatus              // OverallStatus 是该用例的聚合状态
	EvalCaseResults []*evalresult.EvalCaseResult   // EvalCaseResults 是每次运行的用例结果
	MetricResults   []*evalresult.EvalMetricResult // MetricResults 是聚合后的指标结果
	RunDetails      []*EvaluationCaseRunDetails    // RunDetails 是可选的逐 run 推理详情
}

type EvaluationCaseRunDetails struct {
	RunID     int                         // RunID 是本次运行标识
	Inference *EvaluationInferenceDetails // Inference 是本次运行采集到的推理详情
}

type EvaluationInferenceDetails struct {
	SessionID       string                // SessionID 是本次运行使用的会话标识
	UserID          string                // UserID 是本次运行使用的用户标识
	Status          status.EvalStatus     // Status 是推理状态
	ErrorMessage    string                // ErrorMessage 是推理失败信息
	Inferences      []*evalset.Invocation // Inferences 是推理输出
	ExecutionTraces []*trace.Trace        // ExecutionTraces 是执行轨迹
}
```

`EvalResult` 包含可由 EvalResultManager 持久化的聚合 EvalSetResult。`RunDetails` 只会在开启 run details 后填充，每条明细都对应一次具体运行。

默认情况下，`evaluation.New` 会创建 AgentEvaluator 并使用 InMemory 的 EvalSetManager、MetricManager、EvalResultManager 与默认 Registry，同时创建本地 Service。若希望从本地文件读取 EvalSet 与指标配置，并将结果写入文件，需要显式注入 Local Manager。

AgentEvaluator 支持通过 `WithNumRuns` 对同一评估集运行多次。默认运行 1 次时，`OverallStatus` 来自该次运行的 `EvalCaseResult.finalEvalStatus`；当 `WithNumRuns` 大于 1 时，聚合会按用例维度对同名指标取平均分并与阈值对比，再由聚合后的指标状态得到用例 `OverallStatus`。每次运行的原始结果保留在 `EvalCaseResults`，聚合后的指标结果写入 `MetricResults` 用于展示和诊断。

## NumRuns 重复运行次数

由于 Agent 的运行过程可能存在不确定性，`evaluation.WithNumRuns` 提供了重复运行机制，用于降低单次运行带来的偶然性。默认运行次数为 1 次，指定 `evaluation.WithNumRuns(n)` 且 n 大于 1 后，同一个评估集会在同一次 Evaluate 中完成 n 次推理与评估，并在汇总时以用例为粒度对同名指标取平均分，再根据聚合后的指标状态计算用例状态。

重复运行次数不会线性增加评估结果文件的数量。一次 Evaluate 只会写入一份评估结果文件，对应一个 EvalSetResult；当 `NumRuns` 大于 1 时，文件内部会包含多次运行的明细结果，同一用例在不同运行中的结果会分别出现在 `EvalCaseResults` 中，并通过 `runId` 区分。

```go

import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithNumRuns(numRuns))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

## NumRuns 级别并发执行

当同一个评估集需要重复运行多次时，总耗时会随着 `numRuns` 增加而增长。框架支持在 run 级别并发执行多个重复运行，用于缩短总体耗时。

在创建 AgentEvaluator 时指定重复运行次数，并显式开启 run 级并发。当前不提供单独的 run 级并发度配置，开启后会按 `numRuns` 个 run 并发执行；如果未开启，即使设置了 `evaluation.WithNumRuns(n)`，也仍按串行方式执行。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithNumRuns(4),
	evaluation.WithNumRunsParallelEnabled(true),
)
```

run 级并发以完整运行过程为单位。每个 run 仍会独立完成一次完整的推理与评估流程，最终结果会汇总到同一个 `EvalSetResult` 中；同一 EvalCase 在不同运行中的明细结果会分别写入 `EvalCaseResults`，并通过 `runId` 区分。

开启并发后，需要保证 Runner、工具实现、外部依赖与回调逻辑可并发调用。特别是同一次 `Evaluate` 中不同 run 的 set 级回调可能并发执行，如果依赖共享可变状态，需要自行保证并发安全。
