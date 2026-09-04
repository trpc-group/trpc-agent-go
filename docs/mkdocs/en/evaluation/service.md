# Evaluation Service

Service is the evaluation execution entry. It splits an evaluation into inference and evaluation phases. Inference runs the Agent and collects actual traces. Evaluation scores actual and expected traces based on metrics and passes results to EvalResultManager for persistence.

## Interface Definition

Service interface is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Service is the evaluation service interface.
type Service interface {
	Inference(ctx context.Context, request *InferenceRequest, opt ...Option) ([]*InferenceResult, error) // Inference runs inference phase.
	Evaluate(ctx context.Context, request *EvaluateRequest, opt ...Option) (*EvalSetRunResult, error)    // Evaluate runs evaluation phase.
	Close() error                                                                                        // Close releases resources.
}

// InferenceRequest is the inference request.
type InferenceRequest struct {
	AppName     string   // AppName is the application name.
	EvalSetID   string   // EvalSetID is the evaluation set identifier.
	EvalCaseIDs []string // EvalCaseIDs is the list of case identifiers. Empty means all cases in the set.
}

// InferenceResult is the inference result.
type InferenceResult struct {
	AppName      string                // AppName is the application name.
	EvalSetID    string                // EvalSetID is the evaluation set identifier.
	EvalCaseID   string                // EvalCaseID is the case identifier.
	EvalMode     evalset.EvalMode      // EvalMode is the evaluation mode.
	Inferences   []*evalset.Invocation // Inferences are actual traces collected in inference.
	ExpectedInferences []*evalset.Invocation // ExpectedInferences are pre-generated expected traces collected in inference when enabled.
	SessionID    string                // SessionID is the inference session identifier.
	UserID       string                // UserID is the inference user identifier.
	Status       status.EvalStatus     // Status is the inference status.
	ErrorMessage string                // ErrorMessage is the inference failure reason.
	InferenceDuration time.Duration   // InferenceDuration is actual agent inference time.
}

// EvaluateRequest is the evaluation request.
type EvaluateRequest struct {
	AppName          string             // AppName is the application name.
	EvalSetID        string             // EvalSetID is the evaluation set identifier.
	InferenceResults []*InferenceResult // InferenceResults are outputs from the inference phase.
	EvaluateConfig   *EvaluateConfig    // EvaluateConfig is the evaluation config.
}

// EvaluateConfig is the evaluation config.
type EvaluateConfig struct {
	EvalMetrics []*metric.EvalMetric // EvalMetrics are the metrics participating in evaluation.
}

// EvalSetRunResult is the evaluation result.
type EvalSetRunResult struct {
	AppName         string                       // AppName is the application name.
	EvalSetID       string                       // EvalSetID is the evaluation set identifier.
	InferenceDuration time.Duration             // InferenceDuration is actual agent inference time for this run.
	EvalCaseResults []*evalresult.EvalCaseResult // EvalCaseResults are the evaluation case results.
}

// EvalCaseResultAggregator aggregates metric results for a single evaluation case.
type EvalCaseResultAggregator interface {
	Aggregate(ctx context.Context, input *EvalCaseResultAggregationInput) (*EvalCaseResultAggregationResult, error)
}

// EvalCaseResultAggregationInput is the context required to aggregate a single evaluation case result.
type EvalCaseResultAggregationInput struct {
	AppName         string                         // AppName is the application name.
	EvalSetID       string                         // EvalSetID is the evaluation set identifier.
	EvalCase        *evalset.EvalCase              // EvalCase is the current evaluation case configuration.
	InferenceResult *InferenceResult               // InferenceResult is the inference result for the current evaluation case.
	EvalMetrics     []*metric.EvalMetric           // EvalMetrics are the actually executed evaluation metrics.
	MetricResults   []*evalresult.EvalMetricResult // MetricResults are the corresponding overall metric results.
}

// EvalCaseResultAggregationResult is the aggregated evaluation case result.
type EvalCaseResultAggregationResult struct {
	Score  float64           // Score is the case-level score.
	Status status.EvalStatus // Status is the case-level status.
}
```

The framework provides a local Service implementation that depends on Runner for inference, EvalSetManager for EvalSet loading, and Registry for evaluator lookup.

## Inference Phase

The inference phase is handled by `Inference`. It reads EvalSet, filters cases by `EvalCaseIDs`, then generates an independent `SessionID` for each case and runs inference.

When `evalMode` is empty, the inference phase chooses the input source from the EvalCase: if `conversationScenario` is configured, UserSimulation generates each user turn dynamically; otherwise it runs the Runner turn by turn based on `conversation` and writes actual Invocations into `Inferences`.

When `evalMode` is `trace`, it does not run the Runner. If `actualConversation` is configured, it returns that as the actual trace; otherwise it treats `conversation` as the actual trace.

The local implementation supports EvalCase-level concurrent inference. When enabled, multiple cases are run in parallel, while turns within a case remain sequential.

## Evaluation Phase

The evaluation phase is handled by `Evaluate`. It takes `InferenceResult` as input, loads the corresponding EvalCase, and constructs actuals and expecteds. By default, expecteds come from EvalSet `conversation`. If a case uses `conversationScenario` without enabling `expectedRunnerEnabled`, the evaluation phase builds placeholder expecteds from the actual trace that preserve only `userContent`. When an EvalCase enables `expectedRunnerEnabled`, the evaluation phase reuses the `ExpectedInferences` that were already generated during inference. It then executes evaluators according to `EvaluateConfig.EvalMetrics`.

The local implementation looks up Evaluators from Registry and calls `Evaluator.Evaluate`. This operates per EvalCase, with actuals and expecteds from the same case aligned by turn.

When `evalMode` is `trace`, inference is skipped and actual traces come from `actualConversation`. `conversation` can optionally provide expected outputs. When `expectedRunnerEnabled` is enabled, expected traces come from the `ExpectedInferences` generated during inference.

After all metrics are evaluated, the local implementation passes the current case, actual inference result, actually executed metric list, and corresponding metric results to `EvalCaseResultAggregator`. The aggregator computes `EvalCaseResult.score` and `EvalCaseResult.finalEvalStatus`. Evaluation then generates `EvalSetRunResult` and returns it to AgentEvaluator.

## Evaluation Case Result Aggregation

An evaluation case can contain multiple metrics. Each Evaluator first produces metric-level `score`, `threshold`, and `evalStatus`, and `EvalCaseResultAggregator` then aggregates them into case-level `score` and `finalEvalStatus`. The default aggregator preserves the framework's existing all-metrics-pass semantics. If any metric fails, the case fails; if no metric fails and at least one metric passes, the case passes; if no metric result is available, the case is not evaluated. The default score is binary: passed cases score 1, while failed or not-evaluated cases score 0.

The `EvalCaseResultAggregator` interface is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type EvalCaseResultAggregator interface {
	// Aggregate aggregates metric results for a single evaluation case.
	Aggregate(ctx context.Context, input *EvalCaseResultAggregationInput) (*EvalCaseResultAggregationResult, error)
}

type EvalCaseResultAggregationInput struct {
	AppName         string                         // AppName is the application name.
	EvalSetID       string                         // EvalSetID is the evaluation set identifier.
	EvalCase        *evalset.EvalCase              // EvalCase is the current evaluation case configuration.
	InferenceResult *InferenceResult               // InferenceResult is the inference result for the current evaluation case.
	EvalMetrics     []*metric.EvalMetric           // EvalMetrics are the actually executed evaluation metrics.
	MetricResults   []*evalresult.EvalMetricResult // MetricResults are the corresponding overall metric results.
}

type EvalCaseResultAggregationResult struct {
	Score  float64           // Score is the case-level score.
	Status status.EvalStatus // Status is the case-level status.
}
```

The following example computes a weighted score from `EvalMetric.Extension.weight`. See [examples/evaluation/caseaggregation](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/caseaggregation) for the complete example.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type weightedAggregator struct {
	Threshold float64
}

func (a weightedAggregator) Aggregate(ctx context.Context, input *service.EvalCaseResultAggregationInput) (*service.EvalCaseResultAggregationResult, error) {
	var totalScore float64
	var totalWeight float64
	for i, evalMetric := range input.EvalMetrics {
		weight := weightFromExtension(evalMetric.Extension)
		totalScore += input.MetricResults[i].Score * weight
		totalWeight += weight
	}
	if totalWeight == 0 {
		return &service.EvalCaseResultAggregationResult{Status: status.EvalStatusNotEvaluated}, nil
	}
	score := totalScore / totalWeight
	resultStatus := status.EvalStatusFailed
	if score >= a.Threshold {
		resultStatus = status.EvalStatusPassed
	}
	return &service.EvalCaseResultAggregationResult{Score: score, Status: resultStatus}, nil
}

func weightFromExtension(extension any) float64 {
	values, ok := extension.(map[string]any)
	if !ok {
		return 1
	}
	weight, ok := values["weight"].(float64)
	if !ok || weight <= 0 {
		return 1
	}
	return weight
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalCaseResultAggregator(weightedAggregator{
		Threshold: 0.8,
	}),
)
```

If a custom aggregator returns an error, the local implementation marks the current case as `failed` and writes the error message to `errorMessage`.

When reading results, distinguish case-level results from metric-level results. A custom aggregator only decides the case-level `score` and `finalEvalStatus`. Each metric's own `score`, `threshold`, and `evalStatus` are still computed by the corresponding Evaluator and kept in `overallEvalMetricResults`. Therefore, a custom aggregation strategy can allow a case to pass even when one metric fails.

## Callback

The framework supports registering callbacks at key points in the evaluation flow for observation, telemetry, context passing, and request parameter adjustments.

Create a callback registry with `service.NewCallbacks()`, register callback components, and pass them to `evaluation.WithCallbacks` when creating `AgentEvaluator`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

callbacks := service.NewCallbacks()
callbacks.Register("noop", &service.Callback{
	BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
		return nil, nil
	},
	AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
		return nil, nil
	},
	BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
		return nil, nil
	},
	AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
		return nil, nil
	},
	BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
		return nil, nil
	},
	AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
		return nil, nil
	},
	BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
		return nil, nil
	},
	AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
		return nil, nil
	},
})

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithCallbacks(callbacks),
)
```

If you only need a single callback point, you can use the specific registration method, such as `callbacks.RegisterBeforeInferenceSet(name, fn)`.

See [examples/evaluation/callbacks](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/callbacks) for the full example.

Callback points are described in the following table.

| Callback Point | Trigger Timing |
| --- | --- |
| `BeforeInferenceSet` | Before inference phase starts, once per EvalSet |
| `AfterInferenceSet` | After inference phase ends, once per EvalSet |
| `BeforeInferenceCase` | Before a single EvalCase inference starts, once per EvalCase |
| `AfterInferenceCase` | After a single EvalCase inference ends, once per EvalCase |
| `BeforeEvaluateSet` | Before evaluation phase starts, once per EvalSet |
| `AfterEvaluateSet` | After evaluation phase ends, once per EvalSet |
| `BeforeEvaluateCase` | Before a single EvalCase evaluation starts, once per EvalCase |
| `AfterEvaluateCase` | After a single EvalCase evaluation ends, once per EvalCase |

Multiple callbacks at the same point run in registration order. If any callback returns an `error`, that callback point stops immediately, and the error includes the callback point, index, and component name.

A callback returns `Result` and `error`. `Result` is optional and is used to pass updated `Context` within the same callback point and to later stages. `error` stops the flow and is returned upward. Common return patterns:

- `return nil, nil`: continue using the current `ctx` for subsequent callbacks. If a previous callback at the same point already updated `ctx` via `Result.Context`, this return does not override it.
- `return result, nil`: update `ctx` to `result.Context` and use it for subsequent callbacks and later stages.
- `return nil, err`: stop at the current callback point and return the error.

When parallel inference is enabled via `evaluation.WithEvalCaseParallelInferenceEnabled(true)`, inference case-level callbacks may run concurrently. Because `args.Request` points to the same `*InferenceRequest`, treat it as read-only. If you need to modify the request, do it in a set-level callback.

When parallel evaluation is enabled via `evaluation.WithEvalCaseParallelEvaluationEnabled(true)`, evaluation case-level callbacks may also run concurrently. Because `args.Request` points to the same `*EvaluateRequest`, treat it as read-only. If you need to modify the request, do it in a set-level callback.

When run-level parallelism is enabled via `evaluation.WithNumRunsParallelEnabled(true)`, set-level callbacks from different runs within the same `Evaluate` may also run concurrently. Although each run uses its own `Request`, callback logic must still ensure concurrency safety if it depends on shared mutable state.

A single EvalCase inference or evaluation failure usually does not return through `error`. It is written into `Result.Status` and `Result.ErrorMessage`. Therefore, `After*CaseArgs.Error` does not carry per-case failure reasons. Check `args.Result.Status` and `args.Result.ErrorMessage` to detect failures.

## Parallel Execution

The framework supports concurrency at three levels: EvalCase inference, EvalCase evaluation, and NumRuns. These concurrency controls are independent and can be enabled individually or combined as needed to reduce overall evaluation time.

### EvalCase-Level Parallel Inference

When an evaluation set has many cases, inference is often the dominant cost. The framework supports EvalCase-level parallel inference to reduce overall duration.

Enable parallel inference when creating AgentEvaluator and set the maximum parallelism. If not set, the default is `runtime.GOMAXPROCS(0)`.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalCaseParallelInferenceEnabled(true),
	evaluation.WithEvalCaseParallelism(8),
)
```

Parallel inference only affects inference across different cases. Turns within a single case still run sequentially, and evaluation still processes cases in order.

After enabling concurrency, ensure that Runner, tool implementations, external dependencies, and callback logic are safe for concurrent calls to avoid interference from shared mutable state.

### EvalCase-Level Parallel Evaluation

When evaluators are slow, such as LLM judges, the evaluation phase can become the bottleneck. The framework supports EvalCase-level parallel evaluation to reduce overall duration.

Enable parallel evaluation when creating AgentEvaluator and set the maximum parallelism. If not set, the default is `runtime.GOMAXPROCS(0)`.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalCaseParallelEvaluationEnabled(true),
	evaluation.WithEvalCaseParallelism(8),
)
```

Parallel evaluation only affects evaluation across different cases. Turns within a case are still sequential, and evaluators are executed in metric order. The returned `EvalCaseResults` preserve the order of the input `InferenceResults`.
