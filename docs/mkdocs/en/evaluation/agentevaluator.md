# Evaluation Entry Point AgentEvaluator

AgentEvaluator is the evaluation entry for users. It organizes an evaluation run by `evalSetID`, reads evaluation sets and metrics, drives the evaluation service for inference and scoring, aggregates multi-run results, and persists outputs.

## Interface Definition

The AgentEvaluator interface is defined as follows.

```go
type AgentEvaluator interface {
	Evaluate(ctx context.Context, evalSetID string, opt ...Option) (*EvaluationResult, error) // Evaluate runs evaluation and returns aggregated results.
	Close() error                                                              // Close releases resources.
}
```

## Structure Definition

The structures of `EvaluationResult` and `EvaluationCaseResult` are defined as follows.

```go
type EvaluationResult struct {
	AppName       string                    // AppName is the application name.
	EvalSetID     string                    // EvalSetID is the evaluation set identifier.
	OverallStatus status.EvalStatus         // OverallStatus is the overall status.
	ExecutionTime time.Duration             // ExecutionTime is the execution duration.
	InferenceDuration time.Duration        // InferenceDuration is actual agent inference time.
	EvalCases     []*EvaluationCaseResult   // EvalCases are the list of case results.
	EvalResult    *evalresult.EvalSetResult // EvalResult is the persisted EvalSetResult.
}

type EvaluationCaseResult struct {
	EvalCaseID      string                         // EvalCaseID is the case identifier.
	OverallStatus   status.EvalStatus              // OverallStatus is the aggregated status for this case.
	InferenceDuration time.Duration              // InferenceDuration is actual agent inference time across runs for this case.
	EvalCaseResults []*evalresult.EvalCaseResult   // EvalCaseResults are the per-run case results.
	MetricResults   []*evalresult.EvalMetricResult // MetricResults are the aggregated metric results.
	RunDetails      []*EvaluationCaseRunDetails    // RunDetails are optional per-run inference details.
}

type EvaluationCaseRunDetails struct {
	RunID     int                         // RunID identifies the evaluation run.
	Inference *EvaluationInferenceDetails // Inference stores details captured during this run.
}

type EvaluationInferenceDetails struct {
	SessionID       string                // SessionID identifies the inference session.
	UserID          string                // UserID identifies the user used for this run.
	Status          status.EvalStatus     // Status records inference status.
	ErrorMessage    string                // ErrorMessage records inference failure when present.
	InferenceDuration time.Duration      // InferenceDuration is actual agent inference time for this run.
	Inferences      []*evalset.Invocation // Inferences stores invocation outputs.
	ExecutionTraces []*trace.Trace        // ExecutionTraces stores execution traces.
}
```

`EvalResult` contains the aggregated EvalSetResult that can be persisted by EvalResultManager. The set-level `InferenceDuration` is returned on `EvaluationResult`; persisted EvalSetResult values retain case/run durations, so no database schema change is required. `RunDetails` is filled only when run details are enabled, and each item is associated with a specific run ID.

`ExecutionTime` covers the complete evaluation flow, including inference and metric evaluation. `InferenceDuration` sums actual agent inference time across cases and runs. With run-level parallelism, it can exceed the end-to-end wall-clock duration.

By default, `evaluation.New` creates AgentEvaluator and uses in-memory EvalSetManager, MetricManager, EvalResultManager, and the default Registry, and also creates a local Service. If you want to read EvalSet and metric configuration from local files and write results to files, you need to inject Local Managers explicitly.

AgentEvaluator supports running the same evaluation set multiple times via `WithNumRuns`. With the default single run, `OverallStatus` comes from that run's `EvalCaseResult.finalEvalStatus`. When `WithNumRuns` is greater than 1, aggregation is performed by case: metrics with the same name are averaged and compared with thresholds, and the aggregated metric statuses are then used to compute the case `OverallStatus`. Each run's raw results are preserved in `EvalCaseResults`, and aggregated metric results are written to `MetricResults` for display and diagnosis.

## NumRuns: Repeated Runs

Because Agent execution may be nondeterministic, `evaluation.WithNumRuns` provides repeated runs to reduce randomness from a single run. The default is 1. When `evaluation.WithNumRuns(n)` is specified with n greater than 1, the same evaluation set performs n rounds of inference and evaluation within a single Evaluate, averages metrics with the same name at case granularity, and computes the case status from the aggregated metric statuses.

The number of result files does not increase linearly with repeated runs. One Evaluate writes a single result file corresponding to one EvalSetResult. When `NumRuns` is greater than 1, the file contains detailed results for multiple runs. Results for the same case across different runs appear in `EvalCaseResults` and are distinguished by `runId`.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithNumRuns(numRuns))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

## NumRuns-Level Parallel Execution

When the same evaluation set needs to be run repeatedly, total duration grows with `numRuns`. The framework supports run-level parallel execution for repeated runs to reduce overall duration.

Specify the repeated run count and explicitly enable run-level parallelism when creating AgentEvaluator. There is currently no separate run-level parallelism option. Once enabled, the framework runs `numRuns` runs concurrently. If it is not enabled, runs remain serial even when `evaluation.WithNumRuns(n)` is set.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithNumRuns(4),
	evaluation.WithNumRunsParallelEnabled(true),
)
```

Run-level parallelism works at the level of full runs. Each run still performs a complete inference and evaluation cycle independently, and the final results are aggregated into the same `EvalSetResult`. Detailed results for the same EvalCase across different runs are written into `EvalCaseResults` and distinguished by `runId`.

After enabling concurrency, ensure that Runner, tool implementations, external dependencies, and callback logic are safe for concurrent calls. In particular, set-level callbacks from different runs within the same `Evaluate` may run concurrently. If they rely on shared mutable state, you must ensure concurrency safety yourself.
