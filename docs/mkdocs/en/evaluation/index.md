# Evaluation Guide

As model capabilities and tool ecosystems mature, Agent systems are moving from experimental scenarios to business-critical workflows. Release cadence keeps increasing, but delivery quality no longer depends on a single demo output. It depends on stability and regressibility under continuous evolution of models, prompts, tools, knowledge bases, and orchestration. During iterations, key behaviors can drift subtly, such as tool selection, parameter shapes, or output formats, making stable regression urgently needed.

Unlike deterministic systems, Agent issues often appear as probabilistic deviations. Reproduction and replay are difficult, and diagnosis must cross logs, traces, and external dependencies, which significantly increases the cost to close the loop.

The core purpose of evaluation is to turn key scenarios and acceptance criteria into assets and distill them into sustainable regression signals. tRPC-Agent-Go provides out-of-the-box evaluation capabilities, supporting asset management and result persistence based on evaluation sets and metrics. It includes static evaluators and LLM Judge evaluators, and provides multi-turn evaluation, repeated runs, `Trace` evaluation mode, callbacks, context injection, and concurrent inference to support local debugging and pipeline regression at engineering scale.

If you want to further automate prompt optimization on top of evaluation, continue with the [PromptIter Guide](../promptiter.md). PromptIter is built on top of Evaluation and provides train/validation separation, multi-round optimization, asynchronous run management, and HTTP APIs.

## Quick Start

This section provides a minimal example to help you quickly understand how to use tRPC-Agent-Go evaluation.

This example uses local file evaluation. The complete code is at [examples/evaluation/local](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/local). The framework also provides an in-memory evaluation implementation. See [examples/evaluation/inmemory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/inmemory) for the full example.

### Environment Setup

- Go 1.24+
- Accessible LLM model service

Configure the model service environment variables before running.

```bash
export OPENAI_API_KEY="sk-xxx"
# Optional. Defaults to https://api.openai.com/v1 when not set.
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
```

### Local File Evaluation Example

This example uses local file evaluation. The complete code is at [examples/evaluation/local](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/local).

#### Code Example

Two core code snippets are provided below, one for building the Agent and one for running the evaluation.

##### Agent Snippet

This snippet builds a minimal evaluable Agent. It mounts a function tool named `calculator` via `llmagent` and constrains math questions to tool calls through `instruction`, making tool traces stably aligned for evaluation.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newCalculatorAgent(modelName string, stream bool) agent.Agent {
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform arithmetic operations including add, subtract, multiply, and divide."),
	)
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(1.0),
		Stream:      stream,
	}
	return llmagent.New(
		"calculator-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithInstruction("Use the calculator function tool for every math problem."),
		llmagent.WithDescription("Calculator agent demonstrating function calling for evaluation workflow."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

type calculatorArgs struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func calculate(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation %q", args.Operation)
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}
```

##### Evaluation Snippet

This snippet creates a runnable Runner from the Agent, configures three local Managers to read the EvalSet and Metric and write result files, then creates an AgentEvaluator via `evaluation.New` and calls `Evaluate` for the specified evaluation set.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName   = "math-eval-app"
	modelName = "deepseek-v4-flash"
	streaming = true
	evalSetID = "math-basic"
	dataDir   = "./data"
	outputDir = "./output"
)

// Create a Runner from the Agent.
runner := runner.NewRunner(appName, newCalculatorAgent(modelName, streaming))
defer runner.Close()
// Create evaluation managers and evaluator registry.
evalSetManager := evalsetlocal.New(evalset.WithBaseDir(dataDir))
metricManager := metriclocal.New(metric.WithBaseDir(dataDir))
evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(outputDir))
registry := registry.New()
// Create AgentEvaluator.
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalSetManager(evalSetManager),
	evaluation.WithMetricManager(metricManager),
	evaluation.WithEvalResultManager(evalResultManager),
	evaluation.WithRegistry(registry),
)
if err != nil {
	log.Fatalf("create evaluator: %v", err)
}
defer agentEvaluator.Close()
// Run evaluation.
result, err := agentEvaluator.Evaluate(ctx, evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
// Parse evaluation results.
fmt.Println("✅ Evaluation completed with local storage")
fmt.Printf("App: %s\n", result.AppName)
fmt.Printf("Eval Set: %s\n", result.EvalSetID)
fmt.Printf("Overall Status: %s\n", result.OverallStatus)
```

#### Evaluation Files

Evaluation files include the evaluation set file and evaluation metric file, organized as follows.

```bash
data/
  math-eval-app/
    math-basic.evalset.json # Evaluation set file.
    math-basic.metrics.json # Evaluation metric file.
```

##### Evaluation Set File

The evaluation set file path is `data/math-eval-app/math-basic.evalset.json`, which holds evaluation cases. During inference, the system iterates `evalCases` and then uses `userContent` in each `conversation` turn as input.

The example below defines an evaluation set named `math-basic`. During evaluation, `evalSetId` selects the set to run, and `evalCases` contains the case list. This example has only one case `calc_add`. Inference creates a session from `sessionInput` and then runs each turn in `conversation`. Here there is only one turn `calc_add-1`, and the input comes from `userContent`, asking the Agent to handle `calc add 2 3`. This case uses the tool trajectory evaluator, so the expected tool trace is written in `tools`. It specifies that the Agent must call a tool named `calculator` with add and two operands, and the tool result must also match. Tool `id` is usually generated at runtime and is not used for matching.

```json
{
  "evalSetId": "math-basic",
  "name": "math-basic",
  "evalCases": [
    {
      "evalId": "calc_add",
      "conversation": [
        {
          "invocationId": "calc_add-1",
          "userContent": {
            "role": "user",
            "content": "calc add 2 3"
          },
          "tools": [
            {
              "id": "tool_use_1",
              "name": "calculator",
              "arguments": {
                "operation": "add",
                "a": 2,
                "b": 3
              },
              "result": {
                "a": 2,
                "b": 3,
                "operation": "add",
                "result": 5
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "math-eval-app",
        "userId": "user"
      }
    }
  ],
  "creationTimestamp": 1761134484.9804401
}
```

##### Evaluation Metric File

The evaluation metric file path is `data/math-eval-app/math-basic.metrics.json`. It describes metrics, selects the evaluator via `metricName`, defines criteria via `criterion`, and sets thresholds via `threshold`. A file can configure multiple metrics, and the framework will run them in order. `metricNames` is optional: invocations without it inherit all configured metrics. To restrict a metric to selected turns, configure `metricNames` on every relevant invocation and omit that metric from turns where it should not run.

This section configures only the tool trajectory evaluator `tool_trajectory_avg_score`. It compares tool traces per turn; tool `id` is usually generated at runtime and is not used for matching.

The metric compares tool calls per turn. If tool name, arguments, and result all match, the turn scores 1; otherwise 0. The overall score is the average across turns and is compared with `threshold` to decide pass or fail. When `threshold` is 1.0, every turn must match.

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1.0
  }
]
```

#### Run Evaluation

```bash
# Set environment variables.
export OPENAI_API_KEY="sk-xxx"
# Optional. Defaults to https://api.openai.com/v1 when not set.
export OPENAI_BASE_URL="https://api.deepseek.com/v1"

# Run evaluation.
go run .
```

When running evaluation, the framework reads the evaluation set file and metric file, calls the Runner and captures responses and tool calls during inference, then scores according to metrics and writes result files.

#### View Evaluation Results

Results are written to `output/math-eval-app/`, with filenames like `math-eval-app_math-basic_<uuid>.evalset_result.json`.

The result file retains both actual and expected traces. As long as the tool trace meets the metric requirements, the evaluation result is marked as passed.

```json
{
  "evalSetResultId": "math-eval-app_math-basic_538cdf6e-925d-41cf-943b-2849982b195e",
  "evalSetResultName": "math-eval-app_math-basic_538cdf6e-925d-41cf-943b-2849982b195e",
  "evalSetId": "math-basic",
  "evalCaseResults": [
    {
      "evalSetId": "math-basic",
      "evalId": "calc_add",
      "finalEvalStatus": "passed",
      "overallEvalMetricResults": [
        {
          "metricName": "tool_trajectory_avg_score",
          "score": 1,
          "evalStatus": "passed",
          "threshold": 1
        }
      ],
      "evalMetricResultPerInvocation": [
        {
          "actualInvocation": {
            "invocationId": "5cc1f162-37e6-4d07-90e9-eb3ec5205b8d",
            "userContent": {
              "role": "user",
              "content": "calc add 2 3"
            },
            "tools": [
              {
                "id": "call_00_etTEEthmCocxvq7r3m2LJRXf",
                "name": "calculator",
                "arguments": {
                  "a": 2,
                  "b": 3,
                  "operation": "add"
                },
                "result": {
                  "a": 2,
                  "b": 3,
                  "operation": "add",
                  "result": 5
                }
              }
            ]
          },
          "expectedInvocation": {
            "invocationId": "calc_add-1",
            "userContent": {
              "role": "user",
              "content": "calc add 2 3"
            },
            "tools": [
              {
                "id": "tool_use_1",
                "name": "calculator",
                "arguments": {
                  "a": 2,
                  "b": 3,
                  "operation": "add"
                },
                "result": {
                  "a": 2,
                  "b": 3,
                  "operation": "add",
                  "result": 5
                }
              }
            ]
          },
          "evalMetricResults": [
            {
              "metricName": "tool_trajectory_avg_score",
              "score": 1,
              "evalStatus": "passed",
              "threshold": 1,
              "details": {
                "score": 1
              }
            }
          ]
        }
      ],
      "sessionId": "19877398-9586-4a97-b1d3-f8ce636ea54f",
      "userId": "user"
    }
  ],
  "creationTimestamp": 1766455261.342534
}
```

### In-Memory Evaluation Example

`inmemory` maintains evaluation sets, metrics, and results in memory.

See [examples/evaluation/inmemory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/inmemory) for the complete example.

#### Code

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Create Runner.
run := runner.NewRunner(appName, agent)
// Create EvalSet Manager, Metric Manager, EvalResult Manager, and Registry.
evalSetManager := evalsetinmemory.New()
metricManager := metricinmemory.New()
evalResultManager := evalresultinmemory.New()
registry := registry.New()
// Build EvalSet data.
if err := prepareEvalSet(ctx, evalSetManager); err != nil {
	log.Fatalf("prepare eval set: %v", err)
}
// Build Metric data.
if err := prepareMetric(ctx, metricManager); err != nil {
	log.Fatalf("prepare metric: %v", err)
}
// Create AgentEvaluator.
agentEvaluator, err := evaluation.New(
	appName,
	run,
	evaluation.WithEvalSetManager(evalSetManager),
	evaluation.WithMetricManager(metricManager),
	evaluation.WithEvalResultManager(evalResultManager),
	evaluation.WithRegistry(registry),
	evaluation.WithNumRuns(numRuns),
)
if err != nil {
	log.Fatalf("create evaluator: %v", err)
}
defer agentEvaluator.Close()
// Run evaluation.
result, err := agentEvaluator.Evaluate(ctx, evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
```

#### Build EvalSet

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

if _, err := evalSetManager.Create(ctx, appName, evalSetID); err != nil {
	return err
}
cases := []*evalset.EvalCase{
	{
		EvalID: "calc_add",
		Conversation: []*evalset.Invocation{
			{
				InvocationID: "calc_add-1",
				UserContent: &model.Message{
					Role:    model.RoleUser,
					Content: "calc add 2 3",
				},
				FinalResponse: &model.Message{
					Role:    model.RoleAssistant,
					Content: "calc result: 5",
				},
				Tools: []*evalset.Tool{
					{
						ID:   "tool_use_1",
						Name: "calculator",
						Arguments: map[string]any{
							"operation": "add",
							"a":         2,
							"b":         3,
						},
						Result: map[string]any{
							"a":         2,
							"b":         3,
							"operation": "add",
							"result":    5,
						},
					},
				},
			},
		},
		SessionInput: &evalset.SessionInput{
			AppName: appName,
			UserID:  "user",
		},
	},
}
for _, evalCase := range cases {
	if err := evalSetManager.AddCase(ctx, appName, evalSetID, evalCase); err != nil {
		return err
	}
}
```

#### Build Metric

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	ctooltrajectory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

evalMetric := &metric.EvalMetric{
	MetricName: "tool_trajectory_avg_score",
	Threshold:  1.0,
	Criterion: criterion.New(
		criterion.WithToolTrajectory(
			ctooltrajectory.New(
				ctooltrajectory.WithDefault(
					&ctooltrajectory.ToolTrajectoryStrategy{
						Name: &ctext.TextCriterion{
							MatchStrategy: ctext.TextMatchStrategyExact,
						},
						Arguments: &cjson.JSONCriterion{
							MatchStrategy: cjson.JSONMatchStrategyExact,
						},
						Result: &cjson.JSONCriterion{
							MatchStrategy: cjson.JSONMatchStrategyExact,
						},
					},
				),
			),
		),
	),
}
metricManager.Add(ctx, appName, evalSetID, evalMetric)
```

## Core Concepts

As shown below, the framework standardizes the Agent runtime through a unified evaluation workflow. Evaluation input consists of EvalSet and Metric. Evaluation output is EvalResult.

![evaluation](../../assets/img/evaluation/evaluation.png)

- **EvalSet** describes covered scenarios and provides evaluation input. Each case organizes Invocations per turn, including user input and expected `tools` traces or `finalResponse` for comparison. Expected traces can be written statically in EvalSet or pre-generated by ExpectedRunner during the inference phase of the standard evaluation flow.
- **Metric** defines metric configuration and includes `metricName`, `criterion`, and `threshold`. `metricName` selects the evaluator implementation, `criterion` describes evaluation criteria, and `threshold` defines the threshold.
- **Evaluator** reads actual and expected traces, computes `score` based on `criterion`, then compares with `threshold` to determine pass or fail.
- **Registry** maintains mappings between `metricName` and Evaluator. Built-in and custom evaluators integrate through it.
- **Service** runs cases, collects traces, calls evaluators for scoring, and uses the case result aggregator to generate evaluation case-level scores and statuses.
- **AgentEvaluator** is created via `evaluation.New` with Runner, Managers, Registry, and other dependencies, and exposes `Evaluate` to users.

A typical evaluation run includes the following steps.

1. AgentEvaluator reads the EvalSet from EvalSetManager based on `evalSetID` and reads Metric config from MetricManager.
2. Service drives the Runner to execute each case and collects the actual Invocation list.
3. Service fetches Evaluators from Registry for each Metric and computes scores.
4. Service aggregates scores and statuses to produce evaluation results.
5. AgentEvaluator persists results via EvalResultManager. Local mode writes to files, and in-memory mode keeps results in memory.

## Best Practices

Integrating evaluation into engineering workflows often delivers more value than expected. It is not about producing a polished report; it is about turning key Agent behaviors into sustainable regression signals.

Two things are most dangerous in Agent evolution: small-looking changes that cause silent behavior drift, and issues that only surface for users, which multiplies diagnosis cost. Evaluation exists to block these risks early.

tRPC-Agent-Go in [examples/runner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner) encodes critical paths into evaluation sets and metrics and runs them in the release pipeline. The Runner quickstart cases cover common scenarios such as calculator, time tool, and compound interest, with a clear goal to guard tool selection and output shape. If behavior drifts, the pipeline fails early, and you can locate the issue directly with the corresponding case and trace.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/evaluation"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
    localevalresult "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
    localevalset "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
    localmetric "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestTool(t *testing.T) {
	tests := []struct {
		name      string
		evalSetID string
	}{
		{
			name:      "calculator",
			evalSetID: "calculator_tool",
		},
		{
			name:      "currenttime",
			evalSetID: "currenttime_tool",
		},
		{
			name:      "compound_interest",
			evalSetID: "compound_interest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := &multiTurnChat{
				modelName: *modelName,
				streaming: *streaming,
				variant:   *variant,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			err := chat.setup(ctx)
			assert.NoError(t, err)
			defer chat.runner.Close()
			evaluationDir := "evaluation"
			localEvalSetManager := localevalset.New(evalset.WithBaseDir(evaluationDir))
			localMetricManager := localmetric.New(metric.WithBaseDir(evaluationDir))
			localEvalResultManager := localevalresult.New(evalresult.WithBaseDir(evaluationDir))
			evaluator, err := evaluation.New(
				appName,
				chat.runner,
				evaluation.WithEvalSetManager(localEvalSetManager),
				evaluation.WithMetricManager(localMetricManager),
				evaluation.WithEvalResultManager(localEvalResultManager),
			)
			assert.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, evaluator.Close())
			})
			result, err := evaluator.Evaluate(ctx, tt.evalSetID)
			assert.NoError(t, err)
			assert.NotNil(t, result)
			resultData, err := json.MarshalIndent(result, "", "  ")
			assert.NoError(t, err)
			assert.Equal(t, status.EvalStatusPassed, result.OverallStatus, string(resultData))
		})
	}
}
```
