# Evaluation Guide

As model capabilities and tool ecosystems mature, Agent systems are moving from experimental scenarios to business-critical workflows. Release cadence keeps increasing, but delivery quality no longer depends on a single demo output. It depends on stability and regressibility under continuous evolution of models, prompts, tools, knowledge bases, and orchestration. During iterations, key behaviors can drift subtly, such as tool selection, parameter shapes, or output formats, making stable regression urgently needed.

Unlike deterministic systems, Agent issues often appear as probabilistic deviations. Reproduction and replay are difficult, and diagnosis must cross logs, traces, and external dependencies, which significantly increases the cost to close the loop.

The core purpose of evaluation is to turn key scenarios and acceptance criteria into assets and distill them into sustainable regression signals. tRPC-Agent-Go provides out-of-the-box evaluation capabilities, supporting asset management and result persistence based on evaluation sets and metrics. It includes static evaluators and LLM Judge evaluators, and provides multi-turn evaluation, repeated runs, `Trace` evaluation mode, callbacks, context injection, and concurrent inference to support local debugging and pipeline regression at engineering scale.

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
		if args.B != 0 {
			result = args.A / args.B
		}
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
	modelName = "deepseek-chat"
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

The evaluation metric file path is `data/math-eval-app/math-basic.metrics.json`. It describes metrics, selects the evaluator via `metricName`, defines criteria via `criterion`, and sets thresholds via `threshold`. A file can configure multiple metrics, and the framework will run them in order.

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

![evaluation](../assets/img/evaluation/evaluation.png)

- **EvalSet** describes covered scenarios and provides evaluation input. Each case organizes Invocations per turn, including user input and expected `tools` traces or `finalResponse`.
- **Metric** defines metric configuration and includes `metricName`, `criterion`, and `threshold`. `metricName` selects the evaluator implementation, `criterion` describes evaluation criteria, and `threshold` defines the threshold.
- **Evaluator** reads actual and expected traces, computes `score` based on `criterion`, then compares with `threshold` to determine pass or fail.
- **Registry** maintains mappings between `metricName` and Evaluator. Built-in and custom evaluators integrate through it.
- **Service** runs cases, collects traces, calls evaluators for scoring, and returns evaluation results.
- **AgentEvaluator** is created via `evaluation.New` with Runner, Managers, Registry, and other dependencies, and exposes `Evaluate` to users.

A typical evaluation run includes the following steps.

1. AgentEvaluator reads the EvalSet from EvalSetManager based on `evalSetID` and reads Metric config from MetricManager.
2. Service drives the Runner to execute each case and collects the actual Invocation list.
3. Service fetches Evaluators from Registry for each Metric and computes scores.
4. Service aggregates scores and statuses to produce evaluation results.
5. AgentEvaluator persists results via EvalResultManager. Local mode writes to files, and in-memory mode keeps results in memory.

## Usage

### EvalSet

EvalSet describes the set of scenarios covered and provides evaluation input. Each scenario corresponds to an EvalCase, and EvalCase organizes Invocations per turn. In default mode, Runner is driven by `conversation` to produce actual traces, and `conversation` is used as expected traces. In trace mode, inference is skipped and `actualConversation` is used as actual traces. During evaluation, Service passes actual and expected traces to Evaluator for comparison and scoring.

#### Structure Definition

EvalSet is a collection of evaluation cases. Each case is an EvalCase. Its Conversation organizes Invocations per turn to describe user input and expected outputs. In trace mode, ActualConversation is used to describe recorded actual traces. The structure definition is as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalSet represents an evaluation set, which organizes a set of evaluation cases.
type EvalSet struct {
	EvalSetID         string               // EvalSetID is the evaluation set identifier.
	Name              string               // Name is the evaluation set name.
	Description       string               // Description is the evaluation set description, optional.
	EvalCases         []*EvalCase          // EvalCases is the list of evaluation cases, required.
	CreationTimestamp *epochtime.EpochTime // CreationTimestamp is the creation timestamp, optional.
}

// EvalCase represents a single evaluation case.
type EvalCase struct {
	EvalID            string               // EvalID is the case identifier.
	EvalMode          EvalMode             // EvalMode is the case mode, optional and can be empty or trace.
	ContextMessages   []*model.Message     // ContextMessages are context messages, optional.
	Conversation      []*Invocation        // Conversation is the expected multi-turn interaction sequence. It is required in default mode and optional in trace mode.
	ActualConversation []*Invocation       // ActualConversation is the actual trace in trace mode. It is required in trace mode.
	SessionInput      *SessionInput        // SessionInput is session initialization info, required.
	CreationTimestamp *epochtime.EpochTime // CreationTimestamp is the creation timestamp, optional.
}

// Invocation represents one turn in a conversation.
type Invocation struct {
	InvocationID          string               // InvocationID is the turn identifier, optional.
	UserContent           *model.Message       // UserContent is the user input for this turn, required.
	FinalResponse         *model.Message       // FinalResponse is the final response, optional.
	Tools                 []*Tool              // Tools are tool traces, optional.
	IntermediateResponses []*model.Message     // IntermediateResponses are intermediate responses, optional.
	CreationTimestamp     *epochtime.EpochTime // CreationTimestamp is the creation timestamp, optional.
}

// Tool represents one tool call and its result.
type Tool struct {
	ID        string // ID is the tool call identifier, optional.
	Name      string // Name is the tool name, required.
	Arguments any    // Arguments are tool inputs, optional.
	Result    any    // Result is tool output, optional.
}

// SessionInput represents session initialization info.
type SessionInput struct {
	AppName string         // AppName is the application name, optional.
	UserID  string         // UserID is the user identifier, required.
	State   map[string]any // State is the initial session state, optional.
}
```

EvalSet is identified by `evalSetId` and contains multiple EvalCases, each identified by `evalId`.

In default mode, the inference phase reads `userContent` per turn from `conversation` as input. `sessionInput.userId` is used to create the session. `sessionInput.state` can inject initial state when needed. `contextMessages` inject additional context before each inference. In trace mode, inference is skipped and `actualConversation` is used directly as actual traces.

`tools` and `finalResponse` in EvalSet describe tool traces and final responses. Whether they are needed depends on the selected evaluation metrics.

In trace mode, you can configure actual output traces explicitly via `actualConversation`.

If both `conversation` and `actualConversation` are provided in trace mode, they must be aligned by turn, and each turn in `actualConversation` should include `userContent`. If only `actualConversation` is provided and `conversation` is omitted, it means no expected outputs are provided.

When `evalMode` is empty, it is the default mode, which performs real-time inference and collects tool traces and final responses. When `evalMode` is `trace`, inference is skipped and `actualConversation` is used as actual traces for evaluation. `conversation` can be provided optionally as expected outputs.

#### EvalSet Manager

EvalSetManager is the storage abstraction for EvalSet, separating evaluation assets from code. By switching implementations, you can use local file or in-memory storage, or implement the interface to connect to a database or configuration platform.

##### Interface Definition

The EvalSetManager interface is defined as follows.

```go
type Manager interface {
	// Get retrieves the evaluation set.
	Get(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// Create creates the evaluation set.
	Create(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// List lists evaluation sets.
	List(ctx context.Context, appName string) ([]string, error)
	// Delete deletes the evaluation set.
	Delete(ctx context.Context, appName, evalSetID string) error
	// GetCase retrieves an evaluation case.
	GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*EvalCase, error)
	// AddCase adds an evaluation case.
	AddCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// UpdateCase updates an evaluation case.
	UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// DeleteCase deletes an evaluation case.
	DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error
	// Close releases resources.
	Close() error
}
```

If you want to read EvalSet from a database, object storage, or configuration platform, you can implement this interface and inject it when creating AgentEvaluator.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

evalSetManager := myevalset.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalSetManager(evalSetManager),
)
```

##### InMemory Implementation

The framework provides an in-memory implementation of EvalSetManager, suitable for dynamically building or temporarily maintaining evaluation sets in code. It is concurrency-safe with read/write locking. To prevent accidental mutation, the read interface returns deep copies.

##### Local Implementation

The framework provides a local file implementation of EvalSetManager, suitable for keeping EvalSet as versioned assets.

It is concurrency-safe with read/write locking. It writes to a temporary file and renames it on success to reduce file corruption risk. Local implementation uses `BaseDir` as the root directory and `Locator` to manage path rules. `Locator` maps `evalSetId` to file paths and lists existing evaluation sets under an `appName`. The default naming rule for evaluation set files is `<BaseDir>/<AppName>/<EvalSetId>.evalset.json`.

If you want to reuse an existing directory structure, you can customize `Locator` and inject it when creating EvalSetManager.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
)

type customLocator struct{}

// Build returns a custom file path format <BaseDir>/<AppName>/custom-<EvalSetId>.evalset.json.
func (l *customLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetID+".evalset.json")
}

// List lists evaluation set IDs under the given appName.
func (l *customLocator) List(baseDir, appName string) ([]string, error) {
	dir := filepath.Join(baseDir, appName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	var results []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "custom-") && strings.HasSuffix(entry.Name(), ".evalset.json") {
			name := strings.TrimPrefix(entry.Name(), "custom-")
			name = strings.TrimSuffix(name, ".evalset.json")
			results = append(results, name)
		}
	}
	return results, nil
}

evalSetManager := local.New(
	evalset.WithBaseDir(dataDir),
	evalset.WithLocator(&customLocator{}),
)
```

##### MySQL Implementation

The MySQL implementation of EvalSetManager persists EvalSet and EvalCase to MySQL.

It stores evaluation sets and evaluation cases in two tables, and returns cases in insertion order when reading an evaluation set.

###### Configuration Options

**Connection:**

- **`WithMySQLClientDSN(dsn string)`**: Connect using DSN directly (recommended). Consider enabling `parseTime=true`.
- **`WithMySQLInstance(instanceName string)`**: Use a registered MySQL instance. You must register it via `storage/mysql.RegisterMySQLInstance` before use. Note: `WithMySQLClientDSN` has higher priority; if both are set, DSN wins.
- **`WithExtraOptions(extraOptions ...any)`**: Extra options passed to the MySQL client builder. Note: When using `WithMySQLInstance`, the registered instance configuration takes precedence and this option will not take effect.

**Tables:**

- **`WithTablePrefix(prefix string)`**: Table name prefix. An empty prefix means no prefix. A non-empty prefix must start with a letter or underscore and contain only letters/numbers/underscores. `trpc` and `trpc_` are equivalent; an underscore separator is added automatically.

**Initialization:**

- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation. Default is `false`.
- **`WithInitTimeout(timeout time.Duration)`**: Automatic table creation timeout. Default is `30s`.

###### Code Example

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalsetmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/mysql"
)

evalSetManager, err := evalsetmysql.New(
	evalsetmysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
	evalsetmysql.WithTablePrefix("trpc_"),
)
if err != nil {
	log.Fatalf("create mysql evalset manager: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalSetManager(evalSetManager),
)
if err != nil {
	log.Fatalf("create evaluator: %v", err)
}
defer agentEvaluator.Close()
```

###### Configuration Reuse

```go
import (
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	evalsetmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/mysql"
)

// Register MySQL instance.
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// Reuse it in EvalSetManager.
evalSetManager, err := evalsetmysql.New(evalsetmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql evalset manager: %v", err)
}
```

###### Storage Layout

When `skipDBInit=false`, the manager creates required tables during initialization. The default value is `false`. If `skipDBInit=true`, you need to create tables yourself. You can use the SQL below, which is identical to `evaluation/evalset/mysql/schema.sql`. Replace `{{PREFIX}}` with the actual table prefix, e.g. `trpc_`. If you don't use a prefix, replace it with an empty string.

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_sets` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `name` VARCHAR(255) NOT NULL,
  `description` TEXT DEFAULT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_eval_sets_app_eval_set` (`app_name`, `eval_set_id`),
  KEY `idx_eval_sets_app_created` (`app_name`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_cases` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `eval_id` VARCHAR(255) NOT NULL,
  `eval_mode` VARCHAR(32) NOT NULL DEFAULT '',
  `eval_case` JSON NOT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_eval_cases_app_set_case` (`app_name`, `eval_set_id`, `eval_id`),
  KEY `idx_eval_cases_app_set_order` (`app_name`, `eval_set_id`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### EvalMetric

EvalMetric defines evaluation metrics. It selects an evaluator implementation by `metricName`, describes criteria with `criterion`, and defines thresholds with `threshold`. A single evaluation can configure multiple metrics. The evaluation run applies them in order and produces scores and statuses for each.

#### Structure Definition

The EvalMetric structure is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// EvalMetric represents one evaluation metric.
type EvalMetric struct {
	MetricName string               // MetricName is the metric name and matches the evaluator name.
	Threshold  float64              // Threshold is the threshold value.
	Criterion  *criterion.Criterion // Criterion is the evaluation criteria.
}

// Criterion represents a collection of evaluation criteria.
type Criterion struct {
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion // ToolTrajectory is the tool trajectory criterion.
	FinalResponse  *finalresponse.FinalResponseCriterion   // FinalResponse is the final response criterion.
	LLMJudge       *llm.LLMCriterion                       // LLMJudge is the LLM Judge criterion.
}
```

`metricName` selects the evaluator implementation from Registry. The following evaluators are built in by default:

- `tool_trajectory_avg_score`: tool trajectory consistency evaluator, requires expected output.
- `final_response_avg_score`: final response evaluator, does not require LLM, requires expected output.
- `llm_final_response`: LLM final response evaluator, requires expected output.
- `llm_rubric_response`: LLM rubric response evaluator, requires EvalSet to provide session input and LLMJudge plus rubrics.
- `llm_rubric_knowledge_recall`: LLM rubric knowledge recall evaluator, requires EvalSet to provide session input and LLMJudge plus rubrics.

`threshold` defines the threshold. Evaluators output a `score` and determine pass or fail based on it. The definition of `score` varies slightly across evaluators, but a common approach is to compute scores per Invocation and aggregate them into an overall score. Under the same EvalSet, `metricName` must be unique. The order of metrics in the file also affects the evaluation execution order and result display order.

Below is an example metric file for tool trajectory.

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1.0
  }
]
```

#### Criterion

Criterion describes evaluation criteria. Each evaluator reads only the sub-criteria it cares about, and you can combine them as needed.

The framework includes the following criterion types:

| Criterion Type           | Applies To                              |
|--------------------------|-----------------------------------------|
| TextCriterion            | Text strings                            |
| JSONCriterion            | JSON objects                            |
| RougeCriterion           | ROUGE text scoring                      |
| ToolTrajectoryCriterion  | Tool call trajectories                  |
| FinalResponseCriterion   | Final response content                  |
| LLMCriterion             | LLM-based evaluation models             |
| Criterion                | Aggregation of multiple criteria        |

##### TextCriterion

TextCriterion compares two strings, commonly used for tool name comparison and final response text comparison. The structure is defined as follows.

```go
// TextCriterion represents a text matching criterion.
type TextCriterion struct {
	Ignore          bool                                        // Ignore indicates skipping comparison.
	CaseInsensitive bool                                        // CaseInsensitive indicates case-insensitive matching.
	MatchStrategy   TextMatchStrategy                           // MatchStrategy is the matching strategy.
	Compare         func(actual, expected string) (bool, error) // Compare is custom comparison logic.
}

// TextMatchStrategy represents a text matching strategy.
type TextMatchStrategy string
```

TextMatchStrategy supports `exact`, `contains`, and `regex`, with a default of `exact`. During comparison, `source` is the actual string and `target` is the expected string. `exact` requires equality, `contains` requires `source` to contain `target`, and `regex` treats `target` as a regular expression and matches `source`.

| TextMatchStrategy Value | Description                                      |
|-------------------------|--------------------------------------------------|
| exact                   | Actual equals expected exactly (default).        |
| contains                | Actual contains expected.                        |
| regex                   | Actual matches expected as a regular expression. |

Example configuration snippet uses regex matching and case-insensitive mode.

```json
{
  "caseInsensitive": true,
  "matchStrategy": "regex"
}
```

TextCriterion provides a `Compare` extension to override default comparison logic.

The following snippet uses `Compare` to trim spaces before comparison.

```go
import ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"

textCriterion := ctext.New(
	ctext.WithCompare(func(actual, expected string) (bool, error) {
		if strings.TrimSpace(actual) == strings.TrimSpace(expected) {
			return true, nil
		}
		return false, fmt.Errorf("text mismatch after trim")
	}),
)
```

##### JSONCriterion

JSONCriterion compares two JSON values, commonly used for tool arguments and tool results. The structure is defined as follows.

```go
// JSONCriterion represents a JSON matching criterion.
type JSONCriterion struct {
	Ignore          bool                                     // Ignore indicates skipping comparison.
	IgnoreTree      map[string]any                           // IgnoreTree indicates the field tree to ignore.
	OnlyTree        map[string]any                           // OnlyTree indicates the only field tree to compare.
	MatchStrategy   JSONMatchStrategy                        // MatchStrategy is the matching strategy.
	NumberTolerance *float64                                 // NumberTolerance is the numeric tolerance.
	Compare         func(actual, expected any) (bool, error) // Compare is custom comparison logic.
}

// JSONMatchStrategy represents a JSON matching strategy.
type JSONMatchStrategy string
```

Currently, `matchStrategy` only supports `exact`, with default `exact`.

During comparison, `actual` is the actual value and `expected` is the expected value. Object comparison requires identical key sets. Array comparison requires identical length and order. Numeric comparison supports a tolerance, default `1e-6`. `ignoreTree` ignores unstable fields; a leaf node set to true ignores that field and its subtree. `onlyTree` compares only selected fields; keys not present in the tree are ignored. A leaf node set to true compares that field and its subtree. `onlyTree` and `ignoreTree` cannot be set at the same time when both are non-empty.

Example configuration ignores `id` and `metadata.timestamp`, and relaxes numeric tolerance.

```json
{
  "ignoreTree": {
    "id": true,
    "metadata": {
      "timestamp": true
    }
  },
  "numberTolerance": 1e-2
}
```

Example configuration compares only `name` and `metadata.id`, and ignores all other fields.

```json
{
  "onlyTree": {
    "name": true,
    "metadata": {
      "id": true
    }
  }
}
```

JSONCriterion provides a `Compare` extension to override default comparison logic.

The following snippet defines custom matching logic: if both actual and expected contain key `common`, it matches.

```go
import cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"

jsonCriterion := cjson.New(
	cjson.WithCompare(func(actual, expected any) (bool, error) {
		actualObj, ok := actual.(map[string]any)
		if !ok {
			return false, fmt.Errorf("actual is not an object")
		}
		expectedObj, ok := expected.(map[string]any)
		if !ok {
			return false, fmt.Errorf("expected is not an object")
		}
		if _, ok := actualObj["common"]; !ok {
			return false, fmt.Errorf("actual missing key common")
		}
		if _, ok := expectedObj["common"]; !ok {
			return false, fmt.Errorf("expected missing key common")
		}
		return true, nil
	}),
)
```

##### RougeCriterion

RougeCriterion scores two strings using ROUGE and treats the pair as a match when the scores meet the configured thresholds.

See [examples/evaluation/rouge](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/rouge) for a complete example.

```go
import crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"

// RougeCriterion defines ROUGE scoring and threshold checks.
type RougeCriterion struct {
	Ignore         bool         // Ignore indicates skipping comparison.
	RougeType      string       // RougeType selects the ROUGE variant.
	Measure        RougeMeasure // Measure selects the primary scalar measure.
	Threshold      Score        // Threshold defines minimum scores to pass.
	UseStemmer     bool         // UseStemmer enables Porter stemming in the built-in tokenizer.
	SplitSummaries bool         // SplitSummaries enables sentence splitting for rougeLsum.
	Tokenizer      Tokenizer    // Tokenizer overrides the built-in tokenizer.
}

// RougeMeasure represents the scalar measure used as the primary score.
type RougeMeasure string

const (
	RougeMeasureF1        RougeMeasure = "f1"
	RougeMeasurePrecision RougeMeasure = "precision"
	RougeMeasureRecall    RougeMeasure = "recall"
)

// Score holds ROUGE precision, recall and F1.
type Score struct {
	Precision float64
	Recall    float64
	F1        float64
}
```

RougeType supports `rougeN`, `rougeL`, and `rougeLsum`, where N is a positive integer. For example: `rouge1`, `rouge2`, `rouge3`, `rougeL`, `rougeLsum`.

Measure supports `f1`, `precision`, and `recall`, with a default of `f1` when unset.

Threshold defines minimum requirements. Precision, recall, and f1 all participate in the pass check. Unset fields default to 0. ROUGE scores are in range `[0, 1]`.

UseStemmer enables Porter stemming for the built-in tokenizer. When Tokenizer is set, UseStemmer is ignored.

SplitSummaries controls sentence splitting for `rougeLsum` only.

Tokenizer injects a custom tokenizer.

The following snippet configures FinalResponseCriterion to match by rougeLsum with thresholds.

```go
import (
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
)

finalResponseCriterion := cfinalresponse.New(
	cfinalresponse.WithRougeCriterion(&crouge.RougeCriterion{
		RougeType:      "rougeLsum",
		Measure:        crouge.RougeMeasureF1,
		Threshold:      crouge.Score{Precision: 0.3, Recall: 0.6, F1: 0.4},
		UseStemmer:     true,
		SplitSummaries: true,
	}),
)
```

Example metric JSON config:

```json
{
  "finalResponse": {
    "rouge": {
      "rougeType": "rougeLsum",
      "measure": "f1",
      "threshold": {
        "precision": 0.3,
        "recall": 0.6,
        "f1": 0.4
      },
      "useStemmer": true,
      "splitSummaries": true
    }
  }
}
```

##### ToolTrajectoryCriterion

ToolTrajectoryCriterion compares tool trajectories per turn by comparing tool call lists. The structure is defined as follows.

```go
 import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// ToolTrajectoryCriterion represents a tool trajectory matching criterion.
type ToolTrajectoryCriterion struct {
	DefaultStrategy *ToolTrajectoryStrategy                                  // DefaultStrategy is the default strategy.
	ToolStrategy    map[string]*ToolTrajectoryStrategy                       // ToolStrategy overrides by tool name.
	OrderSensitive  bool                                                     // OrderSensitive indicates whether to match in order.
	SubsetMatching  bool                                                     // SubsetMatching indicates whether expected is a subset.
	Compare         func(actual, expected *evalset.Invocation) (bool, error) // Compare is custom comparison logic.
}

// ToolTrajectoryStrategy represents the matching strategy for one tool.
type ToolTrajectoryStrategy struct {
	Name      *ctext.TextCriterion // Name compares tool name.
	Arguments *cjson.JSONCriterion // Arguments compares tool arguments.
	Result    *cjson.JSONCriterion // Result compares tool results.
}
```

Tool trajectory comparison only looks at tool name, arguments, and result by default, and does not compare tool `id`.

`orderSensitive` defaults to false, which uses unordered matching. Internally, the framework treats expected tool calls as left nodes and actual tool calls as right nodes. If an expected tool and actual tool satisfy the matching strategy, an edge is created between them. The framework then uses the Kuhn algorithm to solve maximum bipartite matching and obtains a set of one-to-one pairs. If all expected tools can be matched without conflict, it passes. Otherwise, it returns the expected tools that cannot be matched.

`subsetMatching` defaults to false and requires the number of actual tools to match the number of expected tools. When enabled, actual traces may contain extra tool calls, which suits scenarios with unstable tool counts but still need to constrain key calls.

`defaultStrategy` defines the default matching strategy at the tool level. `toolStrategy` allows overrides by tool name. If no override matches, it falls back to the default. Each strategy can configure `name`, `arguments`, and `result`, and you can skip comparison by setting `ignore` to true for a sub-criterion.

The following configuration example uses the tool trajectory evaluator and configures ToolTrajectoryCriterion. Tool name and arguments use strict matching. For `calculator`, it ignores `trace_id` in arguments and relaxes numeric tolerance for results. For `current_time`, it ignores `result` to avoid matching instability from dynamic timestamps.

```json
[
	{
		"metricName": "tool_trajectory_avg_score",
		"threshold": 1.0,
		"criterion": {
			"toolTrajectory": {
				"orderSensitive": false,
				"subsetMatching": false,
				"defaultStrategy": {
					"name": {
						"matchStrategy": "exact"
					},
					"arguments": {
						"matchStrategy": "exact"
					},
					"result": {
						"matchStrategy": "exact"
					}
				},
				"toolStrategy": {
					"calculator": {
						"name": {
							"matchStrategy": "exact"
						},
						"arguments": {
							"ignoreTree": {
								"trace_id": true
							}
						},
						"result": {
							"numberTolerance": 0.001
						}
					},
					"current_time": {
						"name": {
							"matchStrategy": "exact"
						},
						"arguments": {
							"matchStrategy": "exact"
						},
						"result": {
							"ignore": true
						}
					}
				}
			}
		}
	}
]
```

ToolTrajectoryCriterion provides a `Compare` extension to override default comparison logic.

The following snippet uses `Compare` to treat expected tool list as a blacklist. It matches when none of the expected tool names appear in the actual tools.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	ctooltrajectory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

toolTrajectoryCriterion := ctooltrajectory.New(
	ctooltrajectory.WithCompare(func(actual, expected *evalset.Invocation) (bool, error) {
		if actual == nil || expected == nil {
			return false, fmt.Errorf("invocation is nil")
		}
		actualToolNames := make(map[string]struct{}, len(actual.Tools))
		for _, tool := range actual.Tools {
			if tool == nil {
				return false, fmt.Errorf("actual tool is nil")
			}
			actualToolNames[tool.Name] = struct{}{}
		}
		for _, tool := range expected.Tools {
			if tool == nil {
				return false, fmt.Errorf("expected tool is nil")
			}
			if _, ok := actualToolNames[tool.Name]; ok {
				return false, fmt.Errorf("unexpected tool %s", tool.Name)
			}
		}
		return true, nil
	}),
)
```

Assuming `A`, `B`, `C`, and `D` are tool calls, matching examples are as follows:

| SubsetMatching | OrderSensitive | Expected Sequence | Actual Sequence | Result   | Description                                   |
| --- | --- | --- | --- | --- | --- |
| Off | Off | `[A]` | `[A, B]` | Mismatch | Different counts. |
| On  | Off | `[A]` | `[A, B]` | Match | Expected is a subset. |
| On  | Off | `[C, A]` | `[A, B, C]` | Match | Subset and unordered match. |
| On  | On  | `[A, C]` | `[A, B, C]` | Match | Subset and ordered match. |
| On  | On  | `[C, A]` | `[A, B, C]` | Mismatch | Order mismatch. |
| On  | Off | `[C, D]` | `[A, B, C]` | Mismatch | Actual is missing D. |
| Any | Any | `[A, A]` | `[A]` | Mismatch | Insufficient actual calls; one call cannot match twice. |

##### FinalResponseCriterion

FinalResponseCriterion compares final responses per turn. It supports text comparison, JSON structural comparison after parsing content, and ROUGE scoring. The structure is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// FinalResponseCriterion represents a final response matching criterion.
type FinalResponseCriterion struct {
	Text    *ctext.TextCriterion                                      // Text compares final response text.
	JSON    *cjson.JSONCriterion                                      // JSON compares final response JSON.
	Rouge   *crouge.RougeCriterion                                    // Rouge scores final response text with ROUGE.
	Compare func(actual, expected *evalset.Invocation) (bool, error) // Compare is custom comparison logic.
}
```

When using this criterion, you need to fill `finalResponse` on the expected side for the corresponding turn in EvalSet.

`text`, `json`, and `rouge` can be configured together, and all configured sub-criteria must match. When `json` is configured, the content must be parseable as JSON.

To match by ROUGE, configure `rouge` and see RougeCriterion for details.

The following example selects `final_response_avg_score` and configures FinalResponseCriterion to compare final responses by text containment.

```json
[
	{
		"metricName": "final_response_avg_score",
		"threshold": 1.0,
		"criterion": {
			"finalResponse": {
				"text": {
					"matchStrategy": "contains"
				}
			}
		}
	}
]
```

FinalResponseCriterion provides a `Compare` extension to override default comparison logic.

The following snippet uses `Compare` to treat the expected final response as a blacklist. If the actual final response equals it, it is considered a mismatch. This is suitable for forbidding fixed templates.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
)

finalResponseCriterion := cfinalresponse.New(
	cfinalresponse.WithCompare(func(actual, expected *evalset.Invocation) (bool, error) {
		if actual == nil || expected == nil {
			return false, fmt.Errorf("invocation is nil")
		}
		if actual.FinalResponse == nil || expected.FinalResponse == nil {
			return false, fmt.Errorf("final response is nil")
		}
		actualContent := strings.TrimSpace(actual.FinalResponse.Content)
		expectedContent := strings.TrimSpace(expected.FinalResponse.Content)
		if actualContent == expectedContent {
			return false, fmt.Errorf("unexpected final response")
		}
		return true, nil
	}),
)
```

##### LLMCriterion

LLMCriterion configures LLM Judge evaluators. It is suitable for evaluating semantic quality and compliance that are hard to cover with deterministic rules. It selects the judge model and sampling strategy via `judgeModel`, and uses `rubrics` to provide evaluation criteria. The structure is defined as follows.

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

// LLMCriterion represents the LLM Judge criterion.
type LLMCriterion struct {
	JudgeModel *JudgeModelOptions // JudgeModel is the judge model configuration.
	Rubrics    []*Rubric          // Rubrics is the list of evaluation rubrics.
}

// JudgeModelOptions represents judge model configuration.
type JudgeModelOptions struct {
	ProviderName string                  // ProviderName is the model provider.
	ModelName    string                  // ModelName is the model name.
	Variant      string                  // Variant is optional and selects the OpenAI-compatible variant when ProviderName is openai.
	BaseURL      string                  // BaseURL is a custom endpoint.
	APIKey       string                  // APIKey is the access key.
	ExtraFields  map[string]any          // ExtraFields are extra fields.
	NumSamples   *int                    // NumSamples is the sampling count.
	Generation   *model.GenerationConfig // Generation is the generation config.
}

// Rubric represents one evaluation rubric.
type Rubric struct {
	ID          string         // ID is the rubric identifier.
	Content     *RubricContent // Content is the rubric content.
	Description string         // Description is the rubric description.
	Type        string         // Type is the rubric type.
}

type RubricContent struct {
	Text string // Text is the rubric text.
}
```

`judgeModel` supports environment variable references in `providerName`, `modelName`, `variant`, `baseURL`, and `apiKey`, which are expanded at runtime. For security, avoid writing `judgeModel.apiKey` or `judgeModel.baseURL` in plain text in metric configuration files or code.

`variant` is optional and selects the OpenAI-compatible variant, for example `openai`, `hunyuan`, `deepseek`, `qwen`. It is only effective when `providerName` is `openai`. When omitted, the default variant is `openai`.

`Generation` defaults to `MaxTokens=2000`, `Temperature=0.8`, `Stream=false`.

`numSamples` controls the number of samples per turn. The default is 1. More samples reduce judge variance but increase cost.

`providerName` indicates the judge model provider, which maps to the framework Model Provider. The framework creates a judge model instance based on `providerName` and `modelName`. Common values include `openai`, `anthropic`, and `gemini`. See [Provider](./model.md#provider) for details.

`rubrics` split a metric into multiple clear-granularity criteria. Each rubric should be independent and directly verifiable from user input and the final answer, which improves judge stability and makes issues easier to locate. `id` is a stable identifier, and `content.text` is the rubric text used by the judge.

Below is an example metric configuration that selects `llm_rubric_response` and configures a judge model with two rubrics.

```json
[
	{
		"metricName": "llm_rubric_response",
		"threshold": 1.0,
		"criterion": {
			"llmJudge": {
				"judgeModel": {
					"providerName": "openai",
					"modelName": "gpt-4o-mini",
					"baseURL": "${JUDGE_MODEL_BASE_URL}",
					"apiKey": "${JUDGE_MODEL_API_KEY}",
					"numSamples": 3
				},
				"rubrics": [
					{
						"id": "1",
						"content": {
							"text": "The final answer provides a conclusion and includes key numbers."
						}
					},
					{
						"id": "2",
						"content": {
							"text": "The final answer should not ask the user for additional information."
						}
					}
				]
			}
		}
	}
]
```

#### Metric Manager

MetricManager is the storage abstraction for Metric, separating metric configuration from code. By switching implementations, you can use local file or in-memory storage, or implement the interface to connect to a database or configuration platform.

##### Interface Definition

The MetricManager interface is defined as follows.

```go
type Manager interface {
	// List lists metric names under an evaluation set.
	List(ctx context.Context, appName, evalSetID string) ([]string, error)
	// Get retrieves a metric configuration under an evaluation set.
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
	// Add adds an evaluation metric.
	Add(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Delete deletes an evaluation metric.
	Delete(ctx context.Context, appName, evalSetID, metricName string) error
	// Update updates an evaluation metric.
	Update(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Close releases resources.
	Close() error
}
```

If you want to read Metric from a database, object storage, or configuration platform, you can implement this interface and inject it when creating AgentEvaluator.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

metricManager := mymetric.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithMetricManager(metricManager),
)
```

##### InMemory Implementation

The framework provides an in-memory implementation of MetricManager, suitable for dynamically building or temporarily maintaining metric configuration in code. It is concurrency-safe with read/write locking. To prevent accidental mutation, the read interface returns deep copies, and the write interface copies input objects before writing.

##### Local Implementation

The framework provides a local file implementation of MetricManager, suitable for keeping Metric as versioned evaluation assets.

It is concurrency-safe with read/write locking. It writes to a temporary file and renames it on success to reduce file corruption risk. In local mode, the default metric file naming rule is `<BaseDir>/<AppName>/<EvalSetId>.metrics.json`, and you can customize the path rule via `Locator`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
)

type customMetricLocator struct{}

// Build returns a custom file path format <BaseDir>/metrics/<AppName>/<EvalSetId>.json.
func (l *customMetricLocator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, "metrics", appName, evalSetID+".json")
}

metricManager := metriclocal.New(
	metric.WithBaseDir(dataDir),
	metric.WithLocator(&customMetricLocator{}),
)
```

##### MySQL Implementation

The MySQL implementation of MetricManager persists metric configuration to MySQL.

###### Configuration Options

**Connection:**

- **`WithMySQLClientDSN(dsn string)`**: Connect using DSN directly (recommended). Consider enabling `parseTime=true`.
- **`WithMySQLInstance(instanceName string)`**: Use a registered MySQL instance. You must register it via `storage/mysql.RegisterMySQLInstance` before use. Note: `WithMySQLClientDSN` has higher priority; if both are set, DSN wins.
- **`WithExtraOptions(extraOptions ...any)`**: Extra options passed to the MySQL client builder. Note: When using `WithMySQLInstance`, the registered instance configuration takes precedence and this option will not take effect.

**Tables:**

- **`WithTablePrefix(prefix string)`**: Table name prefix. An empty prefix means no prefix. A non-empty prefix must start with a letter or underscore and contain only letters/numbers/underscores. `trpc` and `trpc_` are equivalent; an underscore separator is added automatically.

**Initialization:**

- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation. Default is `false`.
- **`WithInitTimeout(timeout time.Duration)`**: Automatic table creation timeout. Default is `30s`, consistent with components such as memory/mysql.

###### Code Example

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	metricmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/mysql"
)

metricManager, err := metricmysql.New(
	metricmysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
	metricmysql.WithTablePrefix("trpc_"),
)
if err != nil {
	log.Fatalf("create mysql metric manager: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithMetricManager(metricManager),
)
if err != nil {
	log.Fatalf("create evaluator: %v", err)
}
defer agentEvaluator.Close()
```

###### Configuration Reuse

```go
import (
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	metricmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/mysql"
)

// Register MySQL instance.
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// Reuse it in MetricManager.
metricManager, err := metricmysql.New(metricmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql metric manager: %v", err)
}
```

###### Storage Layout

When `skipDBInit=false`, the manager creates required tables during initialization. The default value is `false`. If `skipDBInit=true`, you need to create tables yourself. You can use the SQL below, which is identical to `evaluation/metric/mysql/schema.sql`. Replace `{{PREFIX}}` with the actual table prefix, e.g. `trpc_`. If you don't use a prefix, replace it with an empty string.

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_metrics` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `metric_name` VARCHAR(255) NOT NULL,
  `metric` JSON NOT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_metrics_app_set_name` (`app_name`, `eval_set_id`, `metric_name`),
  KEY `idx_metrics_app_set` (`app_name`, `eval_set_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### Evaluator

Evaluator is the evaluation interface that implements the scoring logic for a single metric. During evaluation, the Evaluator corresponding to `metricName` is fetched from `Registry`, receives actual and expected traces, and returns a score and status.

#### Interface Definition

Evaluator interface is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Evaluator represents the evaluator interface.
type Evaluator interface {
	// Name returns the evaluator name.
	Name() string
	// Description returns the evaluator description.
	Description() string
	// Evaluate runs evaluation and returns results.
	Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*EvaluateResult, error)
}

// EvaluateResult represents evaluator output.
type EvaluateResult struct {
	OverallScore         float64                // OverallScore is the overall score.
	OverallStatus        status.EvalStatus      // OverallStatus is the overall status.
	PerInvocationResults []*PerInvocationResult // PerInvocationResults are per-turn result list.
}

// PerInvocationResult represents one turn evaluation result.
type PerInvocationResult struct {
	ActualInvocation   *evalset.Invocation   // ActualInvocation is the actual trace.
	ExpectedInvocation *evalset.Invocation   // ExpectedInvocation is the expected trace.
	Score              float64               // Score is the turn score.
	Status             status.EvalStatus     // Status is the turn status.
	Details            *PerInvocationDetails // Details are evaluation details.
}

// PerInvocationDetails represents per-turn evaluation details.
type PerInvocationDetails struct {
	Reason       string                    // Reason is the scoring explanation for this turn.
	Score        float64                   // Score is the turn score.
	RubricScores []*evalresult.RubricScore // RubricScores are rubric score list.
}
```

Evaluator input is two Invocation lists. `actuals` are the actual traces collected during inference, and `expecteds` are expected traces from EvalSet. The framework calls Evaluate per EvalCase, and `actuals` and `expecteds` represent the actual and expected traces for the case and are aligned by turn. Most evaluators require both lists to have the same number of turns, otherwise an error is returned.

Evaluator output includes overall results and per-turn details. Overall score is usually aggregated from per-turn scores, and overall status is usually determined by comparing overall score with `threshold`. For deterministic evaluators, `reason` usually records mismatch reasons. For LLM Judge evaluators, `reason` and `rubricScores` preserve judge rationale.

#### Tool Trajectory Evaluator

The built-in tool trajectory evaluator is named `tool_trajectory_avg_score`, and its criterion is [criterion.toolTrajectory](#tooltrajectorycriterion). It compares tool name, arguments, and result per turn.

The default implementation uses binary scoring: a fully matched turn scores 1, otherwise 0. The overall score is the average across turns, then compared with `threshold` to determine pass or fail.

Example tool trajectory metric configuration:

```json
[
    {
      "metricName": "tool_trajectory_avg_score",
      "threshold": 1,
      "criterion": {
        "toolTrajectory": {
          "orderSensitive": false,
          "subsetMatching": false,
          "defaultStrategy": {
            "name": {
              "matchStrategy": "exact"
            },
            "arguments": {
              "matchStrategy": "exact"
            },
            "result": {
              "matchStrategy": "exact"
            }
          },
          "toolStrategy": {
            "get_time": {
              "result": {
                "ignore": true
              }
            },
            "get_ticket": {
              "arguments": {
                "ignoreTree": {
                  "time": true
                },
                "matchStrategy": "exact"
              },
              "result": {
                "ignoreTree": {
                  "time": true
                },
                "matchStrategy": "exact"
              }
            }
          }
        }
      }
    }
]
```

See [examples/evaluation/tooltrajectory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/tooltrajectory) for the full example.

#### Final Response Evaluator

The built-in final response evaluator is named `final_response_avg_score`, and its criterion is [finalResponse](#finalresponsecriterion). It compares `finalResponse` per turn.

This evaluator uses binary scoring and aggregates the overall score by averaging per-turn scores. If you want to compare final answers by conclusions or key fields, adjust matching strategy via `text` and `json` in FinalResponseCriterion first, then consider using the `Compare` extension to override comparison logic.

#### LLM Judge Evaluators

LLM Judge evaluators use a judge model to score semantic output quality, suitable for scenarios such as correctness, completeness, and compliance that are hard to cover with deterministic rules. They select the judge model via `criterion.llmJudge.judgeModel` and support `numSamples` to sample multiple times per turn to reduce judge variance.

The internal flow can be understood as follows.

1. `messagesconstructor` builds judge input based on the current turn and history of `actuals` and `expecteds`.
2. Calls the judge model `numSamples` times to sample.
3. `responsescorer` extracts scores and explanations from judge output and generates sample results.
4. `samplesaggregator` aggregates sample results into the turn result.
5. `invocationsaggregator` aggregates multi-turn results into overall score and status.

To allow different metrics to reuse the same orchestration while swapping individual steps, the framework abstracts these steps as operator interfaces and composes them via `LLMEvaluator`.

The framework includes the following LLM Judge evaluators:

- `llm_final_response` focuses on consistency between the final answer and reference answer, typically requiring `finalResponse` on the expected side.
- `llm_rubric_response` focuses on whether the final answer satisfies evaluation rubrics, requires `criterion.llmJudge.rubrics`, and aggregates scores by rubric pass status.
- `llm_rubric_knowledge_recall` focuses on whether tool retrieval results support rubrics, typically requiring knowledge retrieval tool calls in the actual trace and extracting retrieval content as judge input.

##### Interface Definition

LLM Judge evaluators implement the `LLMEvaluator` interface, which extends `evaluator.Evaluator` and composes four operator interfaces.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator"
)

// LLMEvaluator defines the LLM evaluator interface.
type LLMEvaluator interface {
	evaluator.Evaluator
	messagesconstructor.MessagesConstructor     // MessagesConstructor is the message construction operator, which builds judge input.
	responsescorer.ResponseScorer               // ResponseScorer is the response scoring operator, which parses judge output.
	samplesaggregator.SamplesAggregator         // SamplesAggregator is the sample aggregation operator, which aggregates sample results into the turn result.
	invocationsaggregator.InvocationsAggregator // InvocationsAggregator is the multi-turn aggregation operator, which aggregates multi-turn results into overall score and status.
}
```

##### Messages Constructor Operator

`messagesconstructor` assembles the current turn context into judge-ready input. Different evaluators choose different comparison targets. Common combinations include user input, final answer, reference final answer, and rubrics.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessagesConstructor builds judge input.
type MessagesConstructor interface {
	// ConstructMessages builds judge input messages.
	ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) ([]model.Message, error)
}
```

The framework includes multiple `MessagesConstructor` implementations for different built-in evaluators. Default selection is as follows:

- `messagesconstructor/finalresponse` for `llm_final_response`, organizing user input, actual final response, and expected final response as judge input.
- `messagesconstructor/rubricresponse` for `llm_rubric_response`, organizing user input, actual final response, and `rubrics` as judge input.
- `messagesconstructor/rubricknowledgerecall` for `llm_rubric_knowledge_recall`, extracting knowledge retrieval tool outputs from actual traces as judge evidence, and combining with user input and `rubrics` as judge input.

##### Response Scorer Operator

`responsescorer` parses judge model output and extracts scores. LLM Judge evaluators usually normalize scores to 0-1 and write judge explanations to `reason`. Rubric evaluators also return `rubricScores` for each rubric.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ResponseScorer extracts scores from judge output.
type ResponseScorer interface {
	// ScoreBasedOnResponse extracts scores from judge output.
	ScoreBasedOnResponse(ctx context.Context, resp *model.Response,
		evalMetric *metric.EvalMetric) (*evaluator.ScoreResult, error)
}
```

The framework includes multiple `ResponseScorer` implementations. Default selection is as follows:

- `responsescorer/finalresponse` for `llm_final_response`, parsing `valid` or `invalid` from judge output and mapping to 1 or 0, while preserving `reasoning` as `reason`.
- `responsescorer/rubricresponse` for `llm_rubric_response` and `llm_rubric_knowledge_recall`, parsing verdict `yes` or `no` for each rubric, mapping each to 1 or 0, averaging as the turn score, and outputting `rubricScores`.

##### Samples Aggregator Operator

`samplesaggregator` aggregates `numSamples` judge samples. The default implementation uses majority vote to select the representative sample, and chooses a failure sample on ties to remain conservative.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// SamplesAggregator aggregates samples for one turn.
type SamplesAggregator interface {
	// AggregateSamples aggregates samples for one turn.
	AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error)
}
```

The framework includes `samplesaggregator/majorityvote`, which is the default for built-in evaluators. It splits samples by `threshold` into pass and fail, chooses the majority side as the representative, and chooses failure on ties.

##### Invocations Aggregator Operator

`invocationsaggregator` aggregates multi-turn results into the overall score. The default implementation averages scores of evaluated turns and skips turns with status `not_evaluated`.

Interface definition:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// InvocationsAggregator aggregates multi-turn results.
type InvocationsAggregator interface {
	// AggregateInvocations aggregates multi-turn results.
	AggregateInvocations(ctx context.Context, results []*evaluator.PerInvocationResult,
		evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error)
}
```

The framework includes `invocationsaggregator/average`, which is the default for built-in evaluators. It averages scores of evaluated turns and determines overall status based on `threshold`.

##### Custom Composition

LLM Judge evaluators support injecting different operator implementations via `Option` to adjust evaluation logic without modifying the evaluator itself. The example below replaces the sample aggregation strategy with a minimum strategy, which fails if any sample fails.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	llmfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

type minSamplesAggregator struct{}

func (a *minSamplesAggregator) AggregateSamples(ctx context.Context, samples []*evaluator.PerInvocationResult, evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples")
	}
	min := samples[0]
	for _, s := range samples[1:] {
		if s.Score < min.Score {
			min = s
		}
	}
	return min, nil
}

e := llmfinalresponse.New(
	llmfinalresponse.WithSamplesAggregator(&minSamplesAggregator{}),
)
```

##### LLM Final Response Evaluator

The LLM final response evaluator has the metric name `llm_final_response` and is an LLM Judge evaluator. It uses [LLMCriterion](#llmcriterion) to configure the judge model and makes semantic judgments on the final answer. By default, it organizes user input, expected final response, and actual final response into judge input, suitable for automated validation of final text output.

The evaluator calls the judge model via `criterion.llmJudge.judgeModel` and samples multiple times per turn based on `numSamples`. The judge model must return the field `is_the_agent_response_valid` with value `valid` or `invalid` (case-insensitive). `valid` scores 1, `invalid` scores 0. Other results or parsing failures cause errors. With multiple samples, a majority vote selects the representative sample for the turn, then compares with `threshold` to determine pass or fail.

`llm_final_response` usually requires `finalResponse` on the expected side as the reference answer. If the task has multiple equivalent correct formulations, you can write a more abstract reference answer or use `llm_rubric_response` to reduce judge misclassification. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM final response:

```json
[
  {
    "metricName": "llm_final_response",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-chat",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        }
      }
    }
  }
]
```

See [examples/evaluation/llm/finalresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/finalresponse) for the full example.

##### LLM Rubric Response Evaluator

The LLM rubric response evaluator has the metric name `llm_rubric_response` and is an LLM Judge evaluator. It uses [LLMCriterion](#llmcriterion) to configure the judge model and splits a metric into multiple independent rubrics via `rubrics`. It focuses on whether the final answer satisfies each rubric, suitable for automated evaluation of correctness, relevance, compliance, and other goals that are hard to cover with deterministic rules.

The evaluator constructs judge input based on `criterion.llmJudge.rubrics`, and the judge model returns `yes` or `no` for each rubric. The score for one sample is the average across rubrics, where `yes` is 1 and `no` is 0. When `numSamples` is configured, it uses `samplesaggregator/majorityvote` to select the representative result and then compares with `threshold` to determine pass or fail.

Rubrics should be concrete and directly verifiable from user input and the final answer. Avoid combining multiple requirements into one rubric to reduce judge variance and make issues easier to locate. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM rubric response:

```json
[
  {
    "metricName": "llm_rubric_response",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-chat",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        },
        "rubrics": [
          {
            "id": "1",
            "description": "The final answer is correct.",
            "type": "FINAL_RESPONSE_QUALITY",
            "content": {
              "text": "Evaluate the correctness of the final answer. A final answer can be considered correct if it directly addresses the user's question, provides the requested information, and is free of errors or contradictions."
            }
          },
          {
            "id": "2",
            "description": "The final answer is relevant to the user's prompt.",
            "type": "CONTEXT_RELEVANCE",
            "content": {
              "text": "Evaluate the relevance of the context. A context can be considered relevant if it enhances or clarifies the response, adding value to the user's comprehension of the topic in question. Relevance is determined by the extent to which the provided information addresses the specific question asked, staying focused on the subject without straying into unrelated areas or providing extraneous details."
            }
          }
        ]
      }
    }
  }
]
```

See [examples/evaluation/llm/rubricresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/rubricresponse) for the full example.

##### LLM Rubric Knowledge Recall Evaluator

The LLM rubric knowledge recall evaluator has the metric name `llm_rubric_knowledge_recall` and is an LLM Judge evaluator. It uses [LLMCriterion](#llmcriterion) to configure the judge model and describes key information that retrieved evidence must support via `rubrics`. This evaluator focuses on whether retrieved knowledge is sufficient to support the user's question or key facts in rubrics, and is suitable for automated recall quality evaluation in RAG scenarios.

The evaluator extracts responses from knowledge retrieval tools such as `knowledge_search` and `knowledge_search_with_agentic_filter` as evidence, and constructs judge input together with `criterion.llmJudge.rubrics`. The judge model returns `yes` or `no` for each rubric. A single sample score is the average. With multiple samples, it uses majority vote to select the representative result, then compares with `threshold` to determine pass or fail.

This evaluator requires knowledge retrieval tool calls in actual traces that return usable retrieval results, otherwise it cannot form stable judge input. Rubrics should focus on whether evidence contains and supports key facts, and avoid mixing final answer quality requirements into recall evaluation. For security, avoid writing `judgeModel.apiKey` and `judgeModel.baseURL` in plain text, and use environment variables instead.

Example metric configuration for LLM rubric knowledge recall:

```json
[
  {
    "metricName": "llm_rubric_knowledge_recall",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "deepseek-chat",
          "baseURL": "${JUDGE_MODEL_BASE_URL}",
          "apiKey": "${JUDGE_MODEL_API_KEY}",
          "numSamples": 3,
          "generationConfig": {
            "max_tokens": 512,
            "temperature": 1.0,
            "stream": false
          }
        },
        "rubrics": [
          {
            "id": "1",
            "description": "The knowledge recall is relevant to the user's prompt.",
            "type": "KNOWLEDGE_RELEVANCE",
            "content": {
              "text": "Evaluate the relevance of the knowledge recall. A knowledge recall can be considered relevant if it enhances or clarifies the response, adding value to the user's comprehension of the topic in question. Relevance is determined by the extent to which the provided information addresses the specific question asked, staying focused on the subject without straying into unrelated areas or providing extraneous details."
            }
          }
        ]
      }
    }
  }
]
```

See [examples/evaluation/llm/knowledgerecall](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/knowledgerecall) for the full example.

#### Evaluator Registry

Registry manages evaluator registrations. Evaluation uses `metricName` to fetch the corresponding Evaluator from Registry. The framework registers the following evaluators by default:

- `tool_trajectory_avg_score`: tool trajectory consistency evaluator, requires expected output.
- `final_response_avg_score`: final response evaluator, does not require LLM, requires expected output.
- `llm_final_response`: LLM final response evaluator, requires expected output.
- `llm_rubric_response`: LLM rubric response evaluator, requires EvalSet to provide session input and LLMJudge with rubrics.
- `llm_rubric_knowledge_recall`: LLM rubric knowledge recall evaluator, requires EvalSet to provide session input and LLMJudge with rubrics.

You can register custom evaluators and inject a custom Registry when creating AgentEvaluator.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
)

reg := registry.New()
reg.Register("myEvaluator", myevaluator.New())

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithRegistry(reg),
)
```

### EvalResult

EvalResult holds evaluation output. One evaluation run produces an EvalSetResult, organizes results by EvalCase, and records each metric's score, status, and per-turn details.

#### Structure Definition

The EvalSetResult structure is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalSetResult represents the result of one evaluation set run.
type EvalSetResult struct {
	EvalSetResultID   string               // EvalSetResultID is the result identifier.
	EvalSetResultName string               // EvalSetResultName is the result name.
	EvalSetID         string               // EvalSetID is the evaluation set identifier.
	EvalCaseResults   []*EvalCaseResult    // EvalCaseResults is the list of case results.
	CreationTimestamp *epochtime.EpochTime // CreationTimestamp is the creation timestamp.
}

// EvalCaseResult represents the result of one evaluation case.
type EvalCaseResult struct {
	EvalSetID                     string                           // EvalSetID is the evaluation set identifier.
	EvalID                        string                           // EvalID is the case identifier.
	FinalEvalStatus               status.EvalStatus                // FinalEvalStatus is the final status.
	ErrorMessage                  string                           // ErrorMessage is the error message.
	OverallEvalMetricResults      []*EvalMetricResult              // OverallEvalMetricResults is the list of overall metric results.
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation // EvalMetricResultPerInvocation is the list of per-turn metric results.
	SessionID                     string                           // SessionID is the session identifier.
	UserID                        string                           // UserID is the user identifier.
}

// EvalMetricResult represents the result of one evaluation metric.
type EvalMetricResult struct {
	MetricName string                   // MetricName is the metric name.
	Score      float64                  // Score is the score.
	EvalStatus status.EvalStatus        // EvalStatus is the status.
	Threshold  float64                  // Threshold is the threshold.
	Criterion  *criterion.Criterion     // Criterion is the evaluation criterion.
	Details    *EvalMetricResultDetails // Details is the result details.
}

// EvalMetricResultDetails represents metric result details.
type EvalMetricResultDetails struct {
	Reason       string         // Reason is the scoring explanation for this metric.
	Score        float64        // Score is the score for this metric.
	RubricScores []*RubricScore // RubricScores is the rubric score list.
}

// EvalMetricResultPerInvocation represents per-turn metric results.
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation // ActualInvocation is the actual trace.
	ExpectedInvocation *evalset.Invocation // ExpectedInvocation is the expected trace.
	EvalMetricResults  []*EvalMetricResult // EvalMetricResults is the list of metric results for this turn.
}

// RubricScore represents the score of one rubric.
type RubricScore struct {
	ID     string  // ID is the rubric identifier.
	Reason string  // Reason is the scoring explanation for this rubric.
	Score  float64 // Score is the rubric score.
}
```

Overall results write each metric output into `overallEvalMetricResults`. Per-turn details are written into `evalMetricResultPerInvocation` and retain both `actualInvocation` and `expectedInvocation` traces for troubleshooting.

Below is an example result file snippet.

```json
{
  "evalSetResultId": "math-eval-app_math-basic_xxx",
  "evalSetId": "math-basic",
  "evalCaseResults": [
    {
      "evalId": "calc_add",
      "finalEvalStatus": "passed",
      "overallEvalMetricResults": [
        {
          "metricName": "tool_trajectory_avg_score",
          "score": 1,
          "evalStatus": "passed",
          "threshold": 1
        }
      ]
    }
  ]
}
```

#### EvalResult Manager

EvalResultManager is the storage abstraction for EvalResult. It decouples evaluation result persistence and retrieval from evaluation execution. By switching implementations, you can use local file or in-memory storage, or implement the interface to connect to object storage, databases, or configuration platforms.

##### Interface Definition

The EvalResultManager interface is defined as follows.

```go
type Manager interface {
	// Save saves evaluation results.
	Save(ctx context.Context, appName string, evalSetResult *EvalSetResult) (string, error)
	// Get retrieves evaluation results.
	Get(ctx context.Context, appName, evalSetResultID string) (*EvalSetResult, error)
	// List lists evaluation result IDs.
	List(ctx context.Context, appName string) ([]string, error)
	// Close releases resources.
	Close() error
}
```

If you want to write results to object storage or a database, implement this interface and inject it when creating AgentEvaluator.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

evalResultManager := myresult.New()
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalResultManager(evalResultManager),
)
```

##### InMemory Implementation

The framework provides an in-memory implementation of EvalResultManager, suitable for temporarily storing evaluation results in debugging or interactive scenarios. It is concurrency-safe, and the read interface returns deep copies.

##### Local Implementation

The framework provides a local file implementation of EvalResultManager, suitable for storing evaluation results as files in local or artifact directories.

It is concurrency-safe. It writes to a temporary file and renames it on success to reduce file corruption risk. When `evalSetResultId` is not provided on Save, the implementation generates a result ID and fills in `evalSetResultName` and `creationTimestamp`. The default naming rule is `<BaseDir>/<AppName>/<EvalSetResultId>.evalset_result.json`, and you can customize the path rule via `Locator`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
)

type customResultLocator struct{}

func (l *customResultLocator) Build(baseDir, appName, evalSetResultID string) string {
	return filepath.Join(baseDir, "results", appName, evalSetResultID+".evalset_result.json")
}

func (l *customResultLocator) List(baseDir, appName string) ([]string, error) {
	dir := filepath.Join(baseDir, "results", appName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	var results []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".evalset_result.json") {
			name := strings.TrimSuffix(entry.Name(), ".evalset_result.json")
			results = append(results, name)
		}
	}
	return results, nil
}

evalResultManager := local.New(
	evalresult.WithBaseDir(dataDir),
	evalresult.WithLocator(&customResultLocator{}),
)
```

##### MySQL Implementation

The MySQL implementation of EvalResultManager persists evaluation results to MySQL.

###### Configuration Options

**Connection:**

- **`WithMySQLClientDSN(dsn string)`**: Connect using DSN directly (recommended). Consider enabling `parseTime=true`.
- **`WithMySQLInstance(instanceName string)`**: Use a registered MySQL instance. You must register it via `storage/mysql.RegisterMySQLInstance` before use. Note: `WithMySQLClientDSN` has higher priority; if both are set, DSN wins.
- **`WithExtraOptions(extraOptions ...any)`**: Extra options passed to the MySQL client builder. Note: When using `WithMySQLInstance`, the registered instance configuration takes precedence and this option will not take effect.

**Tables:**

- **`WithTablePrefix(prefix string)`**: Table name prefix. An empty prefix means no prefix. A non-empty prefix must start with a letter or underscore and contain only letters/numbers/underscores. `trpc` and `trpc_` are equivalent; an underscore separator is added automatically.

**Initialization:**

- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation. Default is `false`.
- **`WithInitTimeout(timeout time.Duration)`**: Automatic table creation timeout. Default is `30s`, consistent with components such as memory/mysql.

###### Code Example

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/mysql"
)

evalResultManager, err := evalresultmysql.New(
	evalresultmysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
	evalresultmysql.WithTablePrefix("trpc_"),
)
if err != nil {
	log.Fatalf("create mysql evalresult manager: %v", err)
}

agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalResultManager(evalResultManager),
)
if err != nil {
	log.Fatalf("create evaluator: %v", err)
}
defer agentEvaluator.Close()
```

###### Configuration Reuse

```go
import (
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	evalresultmysql "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/mysql"
)

// Register MySQL instance.
storagemysql.RegisterMySQLInstance(
	"my-evaluation-mysql",
	storagemysql.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true&charset=utf8mb4"),
)

// Reuse it in EvalResultManager.
evalResultManager, err := evalresultmysql.New(evalresultmysql.WithMySQLInstance("my-evaluation-mysql"))
if err != nil {
	log.Fatalf("create mysql evalresult manager: %v", err)
}
```

###### Storage Layout

When `skipDBInit=false`, the manager creates required tables during initialization. The default value is `false`. If `skipDBInit=true`, you need to create tables yourself. You can use the SQL below, which is identical to `evaluation/evalresult/mysql/schema.sql`. Replace `{{PREFIX}}` with the actual table prefix, e.g. `trpc_`. If you don't use a prefix, replace it with an empty string.

```sql
CREATE TABLE IF NOT EXISTS `{{PREFIX}}evaluation_eval_set_results` (
  `id` BIGINT NOT NULL AUTO_INCREMENT,
  `app_name` VARCHAR(255) NOT NULL,
  `eval_set_result_id` VARCHAR(255) NOT NULL,
  `eval_set_id` VARCHAR(255) NOT NULL,
  `eval_set_result_name` VARCHAR(255) NOT NULL,
  `eval_case_results` JSON NOT NULL,
  `summary` JSON DEFAULT NULL,
  `created_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  `updated_at` TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_results_app_result_id` (`app_name`, `eval_set_result_id`),
  KEY `idx_results_app_set_created` (`app_name`, `eval_set_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### Evaluation Service

Service is the evaluation execution entry. It splits an evaluation into inference and evaluation phases. Inference runs the Agent and collects actual traces. Evaluation scores actual and expected traces based on metrics and passes results to EvalResultManager for persistence.

#### Interface Definition

Service interface is defined as follows.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// Service is the evaluation service interface.
type Service interface {
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error) // Inference runs inference phase.
	Evaluate(ctx context.Context, request *EvaluateRequest) (*EvalSetRunResult, error)    // Evaluate runs evaluation phase.
	Close() error                                                                         // Close releases resources.
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
	SessionID    string                // SessionID is the inference session identifier.
	UserID       string                // UserID is the inference user identifier.
	Status       status.EvalStatus     // Status is the inference status.
	ErrorMessage string                // ErrorMessage is the inference failure reason.
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
	EvalCaseResults []*evalresult.EvalCaseResult // EvalCaseResults are the evaluation case results.
}
```

The framework provides a local Service implementation that depends on Runner for inference, EvalSetManager for EvalSet loading, and Registry for evaluator lookup.

#### Inference Phase

The inference phase is handled by `Inference`. It reads EvalSet, filters cases by `EvalCaseIDs`, then generates an independent `SessionID` for each case and runs inference.

When `evalMode` is empty, it runs the Runner turn by turn based on `conversation` and writes actual Invocations into `Inferences`.

When `evalMode` is `trace`, it does not run the Runner and instead returns `actualConversation` as actual traces.

The local implementation supports EvalCase-level concurrent inference. When enabled, multiple cases are run in parallel, while turns within a case remain sequential.

#### Evaluation Phase

The evaluation phase is handled by `Evaluate`. It takes `InferenceResult` as input, loads the corresponding EvalCase, constructs actuals and expecteds, and executes evaluators according to `EvaluateConfig.EvalMetrics`.

The local implementation looks up Evaluators by `MetricName` from Registry and calls `Evaluator.Evaluate`. This operates per EvalCase, with actuals and expecteds from the same case aligned by turn.

When `evalMode` is `trace`, inference is skipped, actual traces come from `actualConversation`, and expected traces are provided by `conversation`.

After evaluation, it returns `EvalSetRunResult` to AgentEvaluator.

### AgentEvaluator

AgentEvaluator is the evaluation entry for users. It organizes an evaluation run by `evalSetID`, reads evaluation sets and metrics, drives the evaluation service for inference and scoring, aggregates multi-run results, and persists outputs.

#### Interface Definition

The AgentEvaluator interface is defined as follows.

```go
type AgentEvaluator interface {
	Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error) // Evaluate runs evaluation and returns aggregated results.
	Close() error                                                              // Close releases resources.
}
```

#### Structure Definition

The structures of `EvaluationResult` and `EvaluationCaseResult` are defined as follows.

```go
type EvaluationResult struct {
	AppName       string                  // AppName is the application name.
	EvalSetID     string                  // EvalSetID is the evaluation set identifier.
	OverallStatus status.EvalStatus       // OverallStatus is the overall status.
	ExecutionTime time.Duration           // ExecutionTime is the execution duration.
	EvalCases     []*EvaluationCaseResult // EvalCases are the list of case results.
}

type EvaluationCaseResult struct {
	EvalCaseID      string                         // EvalCaseID is the case identifier.
	OverallStatus   status.EvalStatus              // OverallStatus is the aggregated status for this case.
	EvalCaseResults []*evalresult.EvalCaseResult   // EvalCaseResults are the per-run case results.
	MetricResults   []*evalresult.EvalMetricResult // MetricResults are the aggregated metric results.
}
```

By default, `evaluation.New` creates AgentEvaluator and uses in-memory EvalSetManager, MetricManager, EvalResultManager, and the default Registry, and also creates a local Service. If you want to read EvalSet and metric configuration from local files and write results to files, you need to inject Local Managers explicitly.

AgentEvaluator supports running the same evaluation set multiple times via `WithNumRuns`. During aggregation, it summarizes multiple runs by case, averages scores for metrics with the same name, compares with thresholds to determine aggregated status, and writes aggregated results into `MetricResults`. Each run's raw results are preserved in `EvalCaseResults`.

### NumRuns: Repeated Runs

Because Agent execution may be nondeterministic, `evaluation.WithNumRuns` provides repeated runs to reduce randomness from a single run. The default is 1. When `evaluation.WithNumRuns(n)` is specified, the same evaluation set will perform n rounds of inference and evaluation within a single Evaluate, and aggregation will average scores by metric name at case granularity.

The number of result files does not increase linearly with repeated runs. One Evaluate writes a single result file corresponding to one EvalSetResult. When `NumRuns` is greater than 1, the file contains detailed results for multiple runs. Results for the same case across different runs appear in `EvalCaseResults` and are distinguished by `runId`.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithNumRuns(numRuns))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

### Trace Evaluation Mode

Trace mode evaluates existing traces by writing Invocation traces from a real run into EvalSet and skipping inference during evaluation.

Enable it by setting `evalMode` to `trace` in EvalCase. In trace mode, `actualConversation` represents actual outputs and `conversation` represents expected outputs. There are three supported layouts:

- `actualConversation` only: `actualConversation` is used as actual traces, without expected traces.
- `actualConversation` + `conversation`: `actualConversation` is used as actual traces, and `conversation` is used as expected traces, aligned by turn.
- `conversation` only: `conversation` is used as actual traces without expected traces (for backward compatibility only).

```json
{
  "evalSetId": "trace-basic",
  "name": "trace-basic",
  "evalCases": [
    {
      "evalId": "trace_calc_add",
      "evalMode": "trace",
      "conversation": [
        {
          "invocationId": "trace_calc_add-1",
          "userContent": {
            "role": "user",
            "content": "calc add 123 456"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "calc result: 579"
          },
          "tools": [
            {
              "id": "call_00_example",
              "name": "calculator",
              "arguments": {
                "a": 123,
                "b": 456,
                "operation": "add"
              },
              "result": {
                "a": 123,
                "b": 456,
                "operation": "add",
                "result": 579
              }
            }
          ]
        }
      ],
      "actualConversation": [
        {
          "invocationId": "trace_calc_add-1",
          "userContent": {
            "role": "user",
            "content": "calc add 123 456"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "calc result: 579"
          },
          "tools": [
            {
              "id": "call_00_example",
              "name": "calculator",
              "arguments": {
                "a": 123,
                "b": 456,
                "operation": "add"
              },
              "result": {
                "a": 123,
                "b": 456,
                "operation": "add",
                "result": 579
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "trace-eval-app",
        "userId": "demo-user"
      }
    }
  ]
}
```

In Trace mode, the inference phase does not run Runner and instead writes `actualConversation` into `InferenceResult.Inferences` as actual traces. `conversation` provides expected traces. If `conversation` is omitted, the evaluation phase builds placeholder expecteds that keep only per-turn `userContent`, to avoid treating trace outputs as reference answers in comparisons.

When only actual traces are provided, it is suitable for metrics that depend only on actual traces, such as `llm_rubric_response` and `llm_rubric_knowledge_recall`. If you need metrics that compare reference tool traces or reference final responses, you can additionally configure expected traces.

See [examples/evaluation/trace](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/trace) for the full example.

### Callback

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

A single EvalCase inference or evaluation failure usually does not return through `error`. It is written into `Result.Status` and `Result.ErrorMessage`. Therefore, `After*CaseArgs.Error` does not carry per-case failure reasons. Check `args.Result.Status` and `args.Result.ErrorMessage` to detect failures.

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

### Context Injection

`contextMessages` provides additional context messages for an EvalCase. It is commonly used to supply background information, role setup, or examples. It is also suitable for pure model evaluation scenarios, where a system prompt is configured per case to compare different model and prompt combinations.

Context injection example:

```json
{
  "evalSetId": "contextmessage-basic",
  "name": "contextmessage-basic",
  "evalCases": [
    {
      "evalId": "identity_name",
      "contextMessages": [
        {
          "role": "system",
          "content": "You are trpc-agent-go bot."
        }
      ],
      "conversation": [
        {
          "invocationId": "identity_name-1",
          "userContent": {
            "role": "user",
            "content": "Who are you?"
          }
        }
      ],
      "sessionInput": {
        "appName": "contextmessage-app",
        "userId": "demo-user"
      }
    }
  ]
}
```

See [examples/evaluation/contextmessage](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/contextmessage) for the full example.

### pass@k and pass^k

When evaluation repeats runs with `NumRuns`, each run can be viewed as an independent Bernoulli trial. Two derived metrics `pass@k` and `pass^k` provide measures closer to capability and stability. Let `n` be total runs, `c` be the number of passes, and `k` be the number of attempts of interest.

`pass@k` measures the probability of at least one pass in up to `k` independent attempts. The unbiased estimate based on `n` observations is

$$
\mathrm{pass}@k = 1 - \frac{\binom{n-c}{k}}{\binom{n}{k}}
$$

It represents the probability that a random draw of `k` runs without replacement from `n` includes at least one pass. This estimate is widely used in benchmarks like Codex and HumanEval. It avoids order bias from taking the first `k` runs and uses all sample information when `n` is greater than `k`.

`pass^k` measures the probability that the system passes `k` consecutive runs. It estimates the single-run pass rate as $c / n$ and then computes

$$
\text{pass^k} = \left( \frac{c}{n} \right)^k
$$

This metric emphasizes stability and consistency, and complements pass@k, which focuses on at least one pass.

Example usage:

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

result, err := agentEvaluator.Evaluate(ctx, evalSetId)
n, c, err := evaluation.ParsePassNC(result)
passAtK, err := evaluation.PassAtK(n, c, k)
passHatK, err := evaluation.PassHatK(n, c, k)
```

The computation of pass@k and pass^k relies on independence and identical distribution across runs. When doing repeated runs, ensure each run is independently sampled with necessary state reset, and avoid reusing session memory, tool caches, or external dependencies that would systematically inflate the metrics.

### Skills Evaluation

Agent Skills are exposed as built-in tools: `skill_load` and `skill_run`, so you can evaluate whether the agent uses Skills correctly with the same tool trajectory evaluator. In practice, `skill_run` results contain volatile fields such as `stdout`, `stderr`, `duration_ms`, and inline `output_files` content. Prefer using `onlyTree` in a per-tool strategy to assert only stable fields such as `skill`, requested `output_files`, and `exit_code` and `timed_out`, letting other volatile keys be ignored.

A minimal example is shown below.

EvalSet `tools` snippet:

```json
{
  "invocationId": "write_ok-1",
  "userContent": {
    "role": "user",
    "content": "Use skills to generate an OK file and confirm when done."
  },
  "tools": [
    {
      "id": "tool_use_1",
      "name": "skill_load",
      "arguments": {
        "skill": "write-ok"
      }
    },
    {
      "id": "tool_use_2",
      "name": "skill_run",
      "arguments": {
        "skill": "write-ok",
        "output_files": [
          "out/ok.txt"
        ]
      },
      "result": {
        "exit_code": 0,
        "timed_out": false
      }
    }
  ]
}
```

Metric `toolTrajectory` snippet:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": true,
        "subsetMatching": true,
        "toolStrategy": {
          "skill_load": {
            "arguments": {
              "onlyTree": {
                "skill": true
              },
              "matchStrategy": "exact"
            },
            "result": {
              "ignore": true
            }
          },
          "skill_run": {
            "arguments": {
              "onlyTree": {
                "skill": true,
                "output_files": true
              },
              "matchStrategy": "exact"
            },
            "result": {
              "onlyTree": {
                "exit_code": true,
                "timed_out": true
              },
              "matchStrategy": "exact"
            }
          }
        }
      }
    }
  }
]
```

See [examples/evaluation/skill](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/skill) for a runnable example.

### Claude Code Evaluation

The framework provides a Claude Code Agent. It executes a local Claude Code CLI and maps `tool_use` / `tool_result` records from the CLI output into framework tool events. Therefore, when you need to evaluate Claude Code MCP tool calls, Skills, and subagent behaviors, you can reuse the tool trajectory evaluator `tool_trajectory_avg_score` to align tool traces.

When authoring EvalSets and Metrics, note the Claude Code tool naming and normalization rules:

- MCP tool names follow the `mcp__<server>__<tool>` convention, where `<server>` corresponds to the server key in the project `.mcp.json`.
- Claude Code CLI `Skill` tool calls are normalized to `skill_run`, and `skill` is written into tool arguments `arguments` for matching.
- Subagent routing is usually represented by a `Task` tool call, with `subagent_type` included in tool arguments `arguments`.

A minimal example is shown below. It demonstrates how to declare the expected tool trajectory in the EvalSet and how to use `onlyTree` / `ignore` in the Metric to assert only stable fields.

EvalSet file example below covers MCP, Skill, and Task tools:

```json
{
  "evalSetId": "claudecode-basic",
  "name": "claudecode-basic",
  "evalCases": [
    {
      "evalId": "mcp_calculator",
      "conversation": [
        {
          "invocationId": "mcp_calculator-1",
          "userContent": {
            "role": "user",
            "content": "Compute 1+2."
          },
          "tools": [
            {
              "id": "tool_use_1",
              "name": "mcp__eva_cli_example__calculator",
              "arguments": {
                "operation": "add",
                "a": 1,
                "b": 2
              },
              "result": {
                "operation": "add",
                "a": 1,
                "b": 2,
                "result": 3
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "claudecode-eval-app",
        "userId": "user"
      }
    },
    {
      "evalId": "skill_call",
      "conversation": [
        {
          "invocationId": "skill_call-1",
          "userContent": {
            "role": "user",
            "content": "What's the weather in Shenzhen?"
          },
          "tools": [
            {
              "id": "tool_use_1",
              "name": "skill_run",
              "arguments": {
                "skill": "weather-query"
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "claudecode-eval-app",
        "userId": "user"
      }
    },
    {
      "evalId": "subagent_task",
      "conversation": [
        {
          "invocationId": "subagent_task-1",
          "userContent": {
            "role": "user",
            "content": "Look up the phone number for Alice."
          },
          "tools": [
            {
              "id": "tool_use_1",
              "name": "Task",
              "arguments": {
                "subagent_type": "contact-lookup-agent"
              }
            }
          ]
        }
      ],
      "sessionInput": {
        "appName": "claudecode-eval-app",
        "userId": "user"
      }
    }
  ],
  "creationTimestamp": 1771929600
}
```

Metric file example below:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": true,
        "subsetMatching": true,
        "defaultStrategy": {
          "name": {
            "matchStrategy": "exact"
          },
          "arguments": {
            "matchStrategy": "exact"
          },
          "result": {
            "matchStrategy": "exact"
          }
        },
        "toolStrategy": {
          "skill_run": {
            "name": {
              "matchStrategy": "exact"
            },
            "arguments": {
              "onlyTree": {
                "skill": true
              },
              "matchStrategy": "exact"
            },
            "result": {
              "ignore": true
            }
          },
          "Task": {
            "name": {
              "matchStrategy": "exact"
            },
            "arguments": {
              "onlyTree": {
                "subagent_type": true
              },
              "matchStrategy": "exact"
            },
            "result": {
              "ignore": true
            }
          }
        }
      }
    }
  }
]
```

See [examples/evaluation/claudecode](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/claudecode) for a runnable example.

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
