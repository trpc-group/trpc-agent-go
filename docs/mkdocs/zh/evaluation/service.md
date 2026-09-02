# 评估服务 Service

Service 是评估执行入口，负责将一次评估拆分为推理阶段与评估阶段。推理阶段运行 Agent 并采集实际轨迹，评估阶段基于评估指标对实际轨迹与预期轨迹打分，并将结果交给 EvalResultManager 保存。

## 接口定义

Service 的接口定义如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Service 是评估服务接口
type Service interface {
	Inference(ctx context.Context, request *InferenceRequest, opt ...Option) ([]*InferenceResult, error) // Inference 执行推理阶段
	Evaluate(ctx context.Context, request *EvaluateRequest, opt ...Option) (*EvalSetRunResult, error)    // Evaluate 执行评估阶段
	Close() error                                                                                        // Close 释放资源
}

// InferenceRequest 是推理请求
type InferenceRequest struct {
	AppName     string   // AppName 是应用名
	EvalSetID   string   // EvalSetID 是评估集标识
	EvalCaseIDs []string // EvalCaseIDs 是用例标识列表，空表示运行评估集下全部用例
}

// InferenceResult 是推理结果
type InferenceResult struct {
	AppName      string                // AppName 是应用名
	EvalSetID    string                // EvalSetID 是评估集标识
	EvalCaseID   string                // EvalCaseID 是用例标识
	EvalMode     evalset.EvalMode      // EvalMode 是评估模式
	Inferences   []*evalset.Invocation // Inferences 是推理阶段采集到的实际轨迹
	ExpectedInferences []*evalset.Invocation // ExpectedInferences 是开启时在推理阶段预生成的预期轨迹
	SessionID    string                // SessionID 是推理阶段会话标识
	UserID       string                // UserID 是推理阶段用户标识
	Status       status.EvalStatus     // Status 是推理状态
	ErrorMessage string                // ErrorMessage 是推理失败原因
}

// EvaluateRequest 是评估请求
type EvaluateRequest struct {
	AppName          string             // AppName 是应用名
	EvalSetID        string             // EvalSetID 是评估集标识
	InferenceResults []*InferenceResult // InferenceResults 是推理阶段产出的结果
	EvaluateConfig   *EvaluateConfig    // EvaluateConfig 是评估配置
}

// EvaluateConfig 是评估配置
type EvaluateConfig struct {
	EvalMetrics []*metric.EvalMetric // EvalMetrics 是参与评估的指标列表
}

// EvalSetRunResult 是评估结果
type EvalSetRunResult struct {
	AppName         string                       // AppName 是应用名
	EvalSetID       string                       // EvalSetID 是评估集标识
	EvalCaseResults []*evalresult.EvalCaseResult // EvalCaseResults 是评估用例结果
}

// EvalCaseResultAggregator 聚合单个评估用例下的多条指标结果
type EvalCaseResultAggregator interface {
	Aggregate(ctx context.Context, input *EvalCaseResultAggregationInput) (*EvalCaseResultAggregationResult, error)
}

// EvalCaseResultAggregationInput 是聚合单个评估用例结果所需的上下文
type EvalCaseResultAggregationInput struct {
	AppName         string                         // AppName 是应用名
	EvalSetID       string                         // EvalSetID 是评估集标识
	EvalCase        *evalset.EvalCase              // EvalCase 是当前评估用例配置
	InferenceResult *InferenceResult               // InferenceResult 是当前评估用例的推理结果
	EvalMetrics     []*metric.EvalMetric           // EvalMetrics 是实际执行的评估指标列表
	MetricResults   []*evalresult.EvalMetricResult // MetricResults 是对应指标的整体结果
}

// EvalCaseResultAggregationResult 是聚合后的评估用例结果
type EvalCaseResultAggregationResult struct {
	Score  float64           // Score 是用例级分数
	Status status.EvalStatus // Status 是用例级状态
}
```

框架提供了 Service 的本地实现，依赖 Runner 执行推理，EvalSetManager 读取 EvalSet，Registry 定位评估器实现。

## 推理阶段

推理阶段由 `Inference` 方法负责，读取 EvalSet 并按 `EvalCaseIDs` 过滤用例，然后为每个用例生成一个独立的 `SessionID` 并执行推理。

当 `evalMode` 为空值时，推理阶段会根据用例配置选择输入来源：若配置了 `conversationScenario`，则由 UserSimulation 动态生成每轮用户输入；否则按 `conversation` 的轮次依次调用 Runner，并把每轮采集到的实际 Invocation 写入 `Inferences`。

当 `evalMode` 为 `trace` 时，推理阶段不会运行 Runner；若配置了 `actualConversation`，则直接将其作为实际轨迹返回，否则会将 `conversation` 视为实际轨迹返回。

Local 实现支持 EvalCase 级并发推理。开启后会并行运行多个用例，单个用例内部仍按轮次顺序执行。

## 评估阶段

评估阶段由 `Evaluate` 方法负责，以 `InferenceResult` 为输入，加载对应的 EvalCase，构造 actuals 与 expecteds 两组 Invocation 列表。默认情况下，expecteds 来自 EvalSet 的 `conversation`；若用例使用 `conversationScenario` 且未开启 `expectedRunnerEnabled`，则会基于实际轨迹构造仅保留 `userContent` 的占位 expecteds；当 EvalCase 开启 `expectedRunnerEnabled` 时，评估阶段直接复用推理阶段已经生成好的 `ExpectedInferences`。然后按 `EvaluateConfig.EvalMetrics` 逐条执行评估器；Invocation 配置 `metricNames` 时只执行其中列出的指标，未配置时使用全部已配置指标。

Local 实现会通过 Registry 获取 Evaluator，并调用 `Evaluator.Evaluate` 完成打分。该调用以 EvalCase 为粒度，actuals 与 expecteds 均来自同一个用例，并按轮次对齐。

当 `evalMode` 为 `trace` 时，推理阶段跳过 Runner，实际轨迹 actuals 来自 `actualConversation`。`conversation` 可选用于提供预期输出；开启 `expectedRunnerEnabled` 时，预期轨迹来自推理阶段生成好的 `ExpectedInferences`。

所有指标评估完成后，Local 实现会把当前用例、实际推理结果、实际执行的指标列表和对应指标结果交给 `EvalCaseResultAggregator`，由它计算 `EvalCaseResult.score` 与 `EvalCaseResult.finalEvalStatus`。评估完成后会生成 `EvalSetRunResult` 并返回给 AgentEvaluator。

## 评估用例结果聚合

一个评估用例可以包含多个指标。各 Evaluator 先产出指标级 `score`、`threshold` 与 `evalStatus`，再由 `EvalCaseResultAggregator` 汇总为用例级 `score` 和 `finalEvalStatus`。默认聚合器沿用框架原有的全指标通过语义，任一指标失败则用例失败，没有失败且至少一个指标通过则用例通过，没有可用指标结果则用例未评估。默认分数是二值的，用例通过时为 1，失败或未评估时为 0。

`EvalCaseResultAggregator` 接口定义如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type EvalCaseResultAggregator interface {
	// Aggregate 聚合单个评估用例的指标结果
	Aggregate(ctx context.Context, input *EvalCaseResultAggregationInput) (*EvalCaseResultAggregationResult, error)
}

type EvalCaseResultAggregationInput struct {
	AppName         string                         // AppName 是应用名
	EvalSetID       string                         // EvalSetID 是评估集标识
	EvalCase        *evalset.EvalCase              // EvalCase 是当前评估用例配置
	InferenceResult *InferenceResult               // InferenceResult 是当前评估用例的推理结果
	EvalMetrics     []*metric.EvalMetric           // EvalMetrics 是实际执行的评估指标列表
	MetricResults   []*evalresult.EvalMetricResult // MetricResults 是对应指标的整体结果
}

type EvalCaseResultAggregationResult struct {
	Score  float64           // Score 是用例级分数
	Status status.EvalStatus // Status 是用例级状态
}
```

下面示例按 `EvalMetric.Extension.weight` 计算加权分。完整示例参见 [examples/evaluation/caseaggregation](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/caseaggregation)。

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

如果自定义聚合器返回错误，Local 实现会将当前用例标记为 `failed`，并把错误信息写入 `errorMessage`。

读取结果时需要区分用例级结果和指标级结果。自定义聚合器只决定用例级 `score` 与 `finalEvalStatus`。单条指标自己的 `score`、`threshold` 与 `evalStatus` 仍由对应 Evaluator 计算，并保留在 `overallEvalMetricResults` 中。因此，自定义聚合策略可能让某个指标失败但用例整体通过。

## Callback 回调

框架支持在评估流程的关键节点注册回调，用于观测/埋点、上下文传递以及调整请求参数。

通过 `service.NewCallbacks()` 创建回调注册表，注册回调组件后在创建 `AgentEvaluator` 时使用 `evaluation.WithCallbacks` 传入，代码示例如下。

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

如果只需要注册单个回调点，也可以使用对应回调点的注册方法，例如 `callbacks.RegisterBeforeInferenceSet(name, fn)`。

完整示例参见 [examples/evaluation/callbacks](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/callbacks)。

回调点说明如下表所示。

| 回调点 | 触发时机 |
| --- | --- |
| `BeforeInferenceSet` | Inference 阶段开始前，每个 EvalSet 触发一次 |
| `AfterInferenceSet` | Inference 阶段结束后，每个 EvalSet 触发一次 |
| `BeforeInferenceCase` | 单个 EvalCase 推理开始前，每个 EvalCase 触发一次 |
| `AfterInferenceCase` | 单个 EvalCase 推理结束后，每个 EvalCase 触发一次 |
| `BeforeEvaluateSet` | Evaluate 阶段开始前，每个 EvalSet 触发一次 |
| `AfterEvaluateSet` | Evaluate 阶段结束后，每个 EvalSet 触发一次 |
| `BeforeEvaluateCase` | 单个 EvalCase 评估开始前，每个 EvalCase 触发一次 |
| `AfterEvaluateCase` | 单个 EvalCase 评估结束后，每个 EvalCase 触发一次 |

同一回调点的多个回调会按注册顺序依次执行。任一回调返回 `error` 会立即中断该回调点，错误信息会携带回调点、序号与组件名。

回调的返回值由 `Result` 与 `error` 两部分组成。`Result` 是可选的，用于在同一回调点内以及后续阶段传递更新后的 `Context`；`error` 用于中断流程并向上返回。常见返回形式含义如下：

- `return nil, nil`：继续沿用当前 `ctx` 执行后续回调；如果同一回调点内前序回调已经通过 `Result.Context` 更新过 `ctx`，该返回方式不会覆盖它。
- `return result, nil`：将 `ctx` 更新为 `result.Context`，后续回调与后续阶段使用更新后的 `ctx`。
- `return nil, err`：中断当前回调点并向上返回错误。

通过 `evaluation.WithEvalCaseParallelInferenceEnabled(true)` 开启并行推理后，推理阶段的 case 级回调可能并发执行，由于 `args.Request` 指向同一份 `*InferenceRequest`，因此建议只读；如需改写请求，可以在 set 级回调中完成。

通过 `evaluation.WithEvalCaseParallelEvaluationEnabled(true)` 开启并发评估后，评估阶段的 case 级回调也可能并发执行；同样由于 `args.Request` 指向同一份 `*EvaluateRequest`，因此建议只读；如需改写请求，可以在 set 级回调中完成。

通过 `evaluation.WithNumRunsParallelEnabled(true)` 开启 run 级并发后，同一次 `Evaluate` 中不同 run 的 set 级回调也可能并发执行；虽然每个 run 使用的是各自独立的 `Request`，但回调内部如果依赖共享可变状态，仍需要自行保证并发安全。

单个 EvalCase 的推理或评估失败通常不会通过 `error` 向上传递，而是写入 `Result.Status` 与 `Result.ErrorMessage`，因此 `After*CaseArgs.Error` 不用于承载单个用例失败原因，需要判断失败可以查看 `args.Result.Status` 与 `args.Result.ErrorMessage`。

## 并发执行

框架支持从 EvalCase 推理、EvalCase 评估和 NumRuns 三个层面开启并发，以缩短整体评测耗时。不同层面的并发能力彼此独立，可以按需单独开启或组合使用。

### EvalCase 级别并发推理

当评估集用例较多时，推理阶段往往是主要耗时。框架支持在推理阶段按 EvalCase 并发运行，用于缩短总体耗时。

在创建 AgentEvaluator 时开启并发推理，并设置最大并发数。不设置时并发数默认值为 `runtime.GOMAXPROCS(0)`。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalCaseParallelInferenceEnabled(true),
	evaluation.WithEvalCaseParallelism(8),
)
```

并发推理只影响不同用例之间的推理。单个用例内部仍按 `conversation` 的轮次顺序执行，评估阶段也会按用例顺序逐个评估。

开启并发后，需要保证 Runner、工具实现、外部依赖与回调逻辑可并发调用，避免共享可变状态导致相互干扰。

### EvalCase 级别并发评估

当评估器耗时较长时，例如 LLM Judge，评估阶段也可能成为瓶颈。框架支持在评估阶段按 EvalCase 并发执行评估器，以缩短总体耗时。

在创建 AgentEvaluator 时开启并发评估，并设置最大并发数。不设置时并发数默认值为 `runtime.GOMAXPROCS(0)`。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalCaseParallelEvaluationEnabled(true),
	evaluation.WithEvalCaseParallelism(8),
)
```

并发评估只影响不同用例之间的评估。单个用例内部仍会按指标顺序逐条执行评估器，且返回的 `EvalCaseResults` 顺序与输入的 `InferenceResults` 一致。
