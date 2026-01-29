# Evaluation Usage Guide

Evaluation provides a comprehensive framework for agent assessment, supporting evaluation data management in both local file and in-memory modes, and offering multi-dimensional evaluation capabilities for agents.

## Quick Start

This section describes how to execute the Agent evaluation process in local file system or inmemory mode.

### Local File System

local maintains evaluation sets, evaluation metrics, and evaluation results on the local file system.

For a complete example, see [examples/evaluation/local](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/local).

#### Code

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

// Create Runner.
runner := runner.NewRunner(appName, agent)
// Create EvalSet Manager、Metric Manager、EvalResult Manager、Registry.
evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*inputDir))
metricManager := metriclocal.New(metric.WithBaseDir(*inputDir))
evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
registry := registry.New()
// Create AgentEvaluator.
agentEvaluator, err := evaluation.New(
	appName,
	runner,
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
// Perform Evaluation.
result, err := agentEvaluator.Evaluate(context.Background(), evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
```

#### Evaluation Set File Example

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
          "finalResponse": {
            "role": "assistant",
            "content": "calc result: 5"
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

#### Evaluation Metric File Example

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": false,
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
        }
      }
    }
  }
]
```

#### Evaluation Result File Example

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
          "threshold": 1,
          "criterion": {
            "toolTrajectory": {
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
              }
            }
          },
          "details": {
            "score": 1
          }
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
            "finalResponse": {
              "role": "assistant",
              "content": "The result of 2 + 3 is **5**."
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
            "finalResponse": {
              "role": "assistant",
              "content": "calc result: 5"
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
              "criterion": {
                "toolTrajectory": {
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
                  }
                }
              },
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

### inmemory

inmemory maintains the evaluation set, evaluation metrics, and evaluation results in memory.

For a complete example, see [examples/evaluation/inmemory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/inmemory).

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
// Create EvalSet Manager、Metric Manager、EvalResult Manager、Registry.
evalSetManager := evalsetinmemory.New()
metricManager := metricinmemory.New()
evalResultManager := evalresultinmemory.New()
registry := registry.New()
// Constructing evaluation set data.
if err := prepareEvalSet(ctx, evalSetManager); err != nil {
	log.Fatalf("prepare eval set: %v", err)
}
// Constructing evaluation metric data.
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
// Perform Evaluation.
result, err := agentEvaluator.Evaluate(ctx, evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
```

#### EvalSet Construction

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

#### Metric Construction

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

![evaluation](../assets/img/evaluation/evaluation.png)

- The EvalSet provides the dataset required for evaluation, including user input and its corresponding expected agent output.
- The Metric defines the metric used to measure model performance, including the metric name and corresponding score threshold.
- The Evaluator compares the actual session results with the expected session results, calculates the specific score, and determines the evaluation status based on the metric threshold.
- The Evaluator Registry maintains the mapping between metric names and corresponding evaluators and supports dynamic registration and search of evaluators.
- The Evaluation Service, as a core component, integrates the Agent to be evaluated, the EvalSet, the Metric, the Evaluator Registry, and the EvalResult Registry. The evaluation process is divided into two phases:
  - Inference: In default mode, extract user input from the EvalSet, invoke the Agent to perform inference, and combine the Agent's actual output with the expected output to form the inference result; in trace mode, treat the EvalSet `conversation` as the actual output trace and skip runner inference.
  - Result Evaluation Phase: Evaluate retrieves the corresponding evaluator from the registry based on the evaluation metric name. Multiple evaluators are used to perform a multi-dimensional evaluation of the inference results, ultimately generating the evaluation result, EvalResult.
- Agent Evaluator: To reduce the randomness of the agent's output, the evaluation service is called NumRuns times and aggregates the results to obtain a more stable evaluation result.

### EvalSet

An EvalSet is a collection of EvalCase instances, identified by a unique EvalSetID, serving as session data within the evaluation process.

An EvalCase represents a set of evaluation cases within the same Session and includes a unique identifier (EvalID), the conversation content, optional `contextMessages`, and session initialization information.

Conversation data includes four types of content:

- User input
- Agent final response
- Tool invocation and result
- Intermediate response information

EvalCase supports configuring the evaluation mode via `evalMode`:

- Default mode (`evalMode` omitted or empty string): `conversation` is treated as the expected output, and evaluation invokes the Runner/Agent to generate the actual output.
- Trace mode (`evalMode` is `"trace"`): `conversation` is treated as the actual output trace, and evaluation does not invoke the Runner/Agent for inference.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalMode represents the evaluation mode type.
type EvalMode string

const (
	EvalModeDefault EvalMode = ""      // EvalModeDefault indicates the default mode.
	EvalModeTrace   EvalMode = "trace" // EvalModeTrace indicates the trace evaluation mode.
)

// EvalSet represents an evaluation set.
type EvalSet struct {
	EvalSetID         string               // Unique identifier of the evaluation set.
	Name              string               // Evaluation set name.
	Description       string               // Evaluation set description.
	EvalCases         []*EvalCase          // All evaluation cases.
	CreationTimestamp *epochtime.EpochTime // Creation time.
}

// EvalCase represents a single evaluation case.
type EvalCase struct {
	EvalID            string               // Unique identifier of the case.
	EvalMode          EvalMode             // Evaluation mode.
	ContextMessages   []*model.Message     // Context messages injected into each inference run.
	Conversation      []*Invocation        // Conversation sequence.
	SessionInput      *SessionInput        // Session initialization data.
	CreationTimestamp *epochtime.EpochTime // Creation time.
}

// Invocation represents a user-agent interaction.
type Invocation struct {
	InvocationID          string
	ContextMessages       []*model.Message     // Context messages injected into this invocation run.
	UserContent           *model.Message       // User input.
	FinalResponse         *model.Message       // Agent final response.
	Tools                 []*Tool              // Tool calls and results.
	IntermediateResponses []*model.Message     // Intermediate responses.
	CreationTimestamp     *epochtime.EpochTime // Creation time.
}

// Tool represents a single tool invocation and its execution result.
type Tool struct {
	ID        string // Tool invocation ID.
	Name      string // Tool name.
	Arguments any    // Tool invocation parameters.
	Result    any    // Tool execution result.
}

// SessionInput represents session initialization input.
type SessionInput struct {
	AppName string         // Application name.
	UserID  string         // User ID.
	State   map[string]any // Initial state.
}
```

The EvalSet Manager is responsible for performing operations such as adding, deleting, modifying, and querying evaluation sets. The interface definition is as follows:

```go
type Manager interface {
	// Get the specified EvalSet.
	Get(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// Create a new EvalSet.
	Create(ctx context.Context, appName, evalSetID string) (*EvalSet, error)
	// List all EvalSet IDs.
	List(ctx context.Context, appName string) ([]string, error)
	// Delete the specified EvalSet.
	Delete(ctx context.Context, appName, evalSetID string) error
	// Get the specified case.
	GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*EvalCase, error)
	// Add a case to the evaluation set.
	AddCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// Update a case.
	UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error
	// Delete case.
	DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error
}
```

The framework provides two implementations of the EvalSet Manager:

- local: Stores the evaluation set in the local file system, with a file name format of `<EvalSetID>.evalset.json`.
- inmemory: Stores the evaluation set in memory, ensuring a deep copy of all operations. This is suitable for temporary testing scenarios.

### Metric

Metric represents an evaluation indicator used to measure a certain aspect of EvalSet’s performance. Each evaluation indicator includes the metric name, evaluation criterion, and score threshold.

During the evaluation process, the evaluator compares the actual conversation with the expected conversation according to the configured evaluation criterion, calculates the evaluation score for this metric, and compares it with the threshold:

- When the evaluation score is lower than the threshold, the metric is determined as not passed.
- When the evaluation score reaches or exceeds the threshold, the metric is determined as passed.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// EvalMetric represents a single metric used to evaluate an EvalCase.
type EvalMetric struct {
	MetricName string               // Metric name.
	Threshold  float64              // Score threshold.
	Criterion  *criterion.Criterion // Evaluation criterion.
}

// Criterion aggregates various evaluation criteria.
type Criterion struct {
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion // Tool trajectory evaluation criterion.
	LLMJudge       *llm.LLMCriterion                       // LLM evaluation criterion.
}
```

The Metric Manager is responsible for managing evaluation metrics.

Each EvalSet can have multiple evaluation metrics, identified by `MetricName`.

The interface definition is as follows:

```go
type Manager interface {
	// Returns all metric names for a specified EvalSet.
	List(ctx context.Context, appName, evalSetID string) ([]string, error)
	// Gets a single metric from a specified EvalSet.
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
	// Adds the metric to a specified EvalSet.
	Add(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Deletes the specified metric.
	Delete(ctx context.Context, appName, evalSetID, metricName string) error
	// Updates the specified metric.
	Update(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
}
```

The framework provides two implementations of the Metric Manager:

- local: Stores evaluation metrics in the local file system, with file names in the format `<EvalSetID>.metric.json`.
- inmemory: Stores evaluation metrics in memory, ensuring a deep copy for all operations. Suitable for temporary testing or quick verification scenarios.

### Evaluator

The Evaluator calculates the final evaluation result based on actual sessions, expected sessions, and the evaluation metric.

The Evaluator outputs the following:

- Overall evaluation score
- Overall evaluation status
- A list of session-by-session evaluation results

The evaluation results for a single session include:

- Actual sessions
- Expected sessions
- Evaluation score
- Evaluation status

The evaluation status is typically determined by both the score and the metric threshold:

- If the evaluation score ≥ the metric threshold, the status is Passed
- If the evaluation score < the metric threshold, the status is Failed

**Note**: The evaluator name `Evaluator.Name()` must match the evaluation metric name `metric.MetricName`.

The Evaluator interface is defined as follows:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Evaluator defines the general interface for evaluators.
type Evaluator interface {
	// Name returns the evaluator name.
	Name() string
	// Description returns the evaluator description.
	Description() string
	// Evaluate executes the evaluation logic, compares the actual and expected sessions, and returns the result.
	Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) (*EvaluateResult, error)
}

// EvaluateResult represents the aggregated results of the evaluator across multiple sessions.
type EvaluateResult struct {
	OverallScore         float64                // Overall score.
	OverallStatus        status.EvalStatus      // Overall status, categorized as passed/failed/not evaluated.
	PerInvocationResults []*PerInvocationResult // Evaluation results for a single session.
}

// PerInvocationResult represents the evaluation results for a single session.
type PerInvocationResult struct {
	ActualInvocation   *evalset.Invocation   // Actual session.
	ExpectedInvocation *evalset.Invocation   // Expected session.
	Score              float64               // Current session score.
	Status             status.EvalStatus     // Current session status.
	Details            *PerInvocationDetails // Additional information such as reason and score.
}

// PerInvocationDetails represents additional information for a single evaluation round.
type PerInvocationDetails struct {
	Reason       string                    // Scoring reason.
	Score        float64                   // Evaluation score.
	RubricScores []*evalresult.RubricScore // Results of each rubric item.
}
```

### Registry

Registry is used to centrally manage and access various evaluators.

Methods include:

- `Register(name string, e Evaluator)`: Registers an evaluator with a specified name.
- `Get(name string)`: Gets an evaluator instance by name.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"

// Registry defines the evaluator registry interface.
type Registry interface {
	// Register registers an evaluator with the global registry.
	Register(name string, e evaluator.Evaluator) error
	// Get gets an instance by evaluator name.
	Get(name string) (evaluator.Evaluator, error)
}
```

The framework registers the following evaluators by default:

- `tool_trajectory_avg_score` tool trajectory consistency evaluator, requires expected outputs.
- `final_response_avg_score` final response evaluator, does not require an LLM, and requires expected outputs.
- `llm_final_response` LLM final response evaluator, requires expected outputs.
- `llm_rubric_response` LLM rubric response evaluator, requires EvalSet to provide conversation input and configure LLMJudge/rubrics.
- `llm_rubric_knowledge_recall` LLM rubric knowledge recall evaluator, requires EvalSet to provide conversation input and configure LLMJudge/rubrics.

### EvalResult

The EvalResult module is used to record and manage evaluation result data.

EvalSetResult records the evaluation results of an evaluation set (EvalSetID) and contains multiple EvalCaseResults, which display the execution status and score details of each evaluation case.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"

// EvalSetResult represents the overall evaluation result of the evaluation set.
type EvalSetResult struct {
	EvalSetResultID   string               // Unique identifier of the evaluation result.
	EvalSetResultName string               // Evaluation result name.
	EvalSetID         string               // Corresponding evaluation set ID.
	EvalCaseResults   []*EvalCaseResult    // Results of each evaluation case.
	CreationTimestamp *epochtime.EpochTime // Result creation time.
}
```
EvalCaseResult represents the evaluation result of a single evaluation case, including the overall evaluation status, scores for each indicator, and evaluation details for each round of dialogue.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// EvalCaseResult represents the evaluation result of a single evaluation case.
type EvalCaseResult struct {
	EvalSetID                     string                           // Evaluation set ID.
	EvalID                        string                           // Unique identifier of the case.
	FinalEvalStatus               status.EvalStatus                // Final evaluation status of the case.
	OverallEvalMetricResults      []*EvalMetricResult              // Overall score for each metric.
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation // Metric evaluation results per invocation.
	SessionID                     string                           // Session ID generated during the inference phase.
	UserID                        string                           // User ID used during the inference phase.
}
```

EvalMetricResult represents the evaluation result of a specific metric, including the score, status, threshold, and additional information.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalMetricResult represents the evaluation result of a single metric.
type EvalMetricResult struct {
	MetricName string                   // Metric name.
	Score      float64                  // Actual score.
	EvalStatus status.EvalStatus        // Evaluation status.
	Threshold  float64                  // Score threshold.
	Criterion  *criterion.Criterion     // Evaluation criterion.
	Details    *EvalMetricResultDetails // Additional information, such as scoring process, error description, etc.
}

// EvalMetricResultDetails represents additional information for metric evaluation.
type EvalMetricResultDetails struct {
	Reason       string         // Scoring reason.
	Score        float64        // Evaluation score.
	RubricScores []*RubricScore // Results of each rubric item.
}

// RubricScore represents the result of a single rubric item.
type RubricScore struct {
	ID     string  // Rubric ID.
	Reason string  // Scoring reason.
	Score  float64 // Evaluation score.
}

// ScoreResult represents the scoring result of a single metric.
type ScoreResult struct {
	Reason       string         // Scoring reason.
	Score        float64        // Evaluation score.
	RubricScores []*RubricScore // Results of each rubric item.
}
```

EvalMetricResultPerInvocation represents the metric-by-metric evaluation result of a single conversation turn, used to analyze the performance differences of a specific conversation under different metrics.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"

// EvalMetricResultPerInvocation represents the metric-by-metric evaluation results for a single conversation.
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation // Actual conversation executed.
	ExpectedInvocation *evalset.Invocation // Expected conversation result.
	EvalMetricResults  []*EvalMetricResult // Evaluation results for each metric.
}
```

The EvalResult Manager manages the storage, query, and list operations of evaluation results. The interface definition is as follows:

```go
// Manager defines the management interface for evaluation results.
type Manager interface {
	// Save saves the evaluation result and returns the EvalSetResultID.
	Save(ctx context.Context, appName string, evalSetResult *EvalSetResult) (string, error)
	// Get retrieves the specified evaluation result based on the evalSetResultID.
	Get(ctx context.Context, appName, evalSetResultID string) (*EvalSetResult, error)
	// List returns all evaluation result IDs for the specified application.
	List(ctx context.Context, appName string) ([]string, error)
}
```

The framework provides two implementations of the EvalResult Manager:

- local: Stores the evaluation results in the local file system. The default file name format is `<EvalSetResultID>.evalset_result.json`. The default naming convention for `EvalSetResultID` is `<appName>_<EvalSetID>_<UUID>`.
- inmemory: Stores the evaluation results in memory. All operations ensure a deep copy, which is suitable for debugging and quick verification scenarios.

### Service

Service is an evaluation service that integrates the following modules:

- EvalSet
- Metric
- Registry
- Evaluator
- EvalSetRunResult

The Service interface defines the complete evaluation process, including the inference and evaluation phases. The interface definition is as follows:

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"

// Service defines the core interface of the evaluation service.
type Service interface {
	// Inference performs inference, calls the Agent to process the specified evaluation case, 
	// and returns the inference result.
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error)
	// Evaluate evaluates the inference result and generates the evaluation result.
	Evaluate(ctx context.Context, request *EvaluateRequest) (*EvalSetRunResult, error)
}
```

The framework provides a default local evaluation service `local` implementation for the Service interface: it calls the local Agent to perform reasoning and evaluation locally.

#### Inference

The inference phase is responsible for running the agent and capturing the actual responses to the test cases.

The input is `InferenceRequest`, and the output is a list of `InferenceResult`.

```go
// InferenceRequest represents an inference request.
type InferenceRequest struct {
	AppName     string   // Application name.
	EvalSetID   string   // Evaluation set ID.
	EvalCaseIDs []string // List of evaluation case IDs to be inferred.
}
```

Description:

- `AppName` specifies the application name.
- `EvalSetID` specifies the evaluation set.
- `EvalCaseIDs` specifies the list of use cases to be evaluated. If left blank, all use cases in the evaluation set are evaluated by default.

During the inference phase, the system sequentially reads the `Invocation` of each evaluation case, uses the `UserContent` as user input to invoke the agent, and records the agent's response.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// InferenceResult represents the inference result of a single evaluation case.
type InferenceResult struct {
	AppName      string                // Application name.
	EvalSetID    string                // Evaluation set ID.
	EvalCaseID   string                // Evaluation case ID.
	Inferences   []*evalset.Invocation // Session ID for the actual inference.
	SessionID    string                // Session ID for the inference phase.
	Status       status.EvalStatus     // Inference status.
	ErrorMessage string                // Error message if inference fails.
}
```

Note:

- Each `InferenceResult` corresponds to an `EvalCase`.
- Since an evaluation set may contain multiple evaluation cases, `Inference` returns a list of `InferenceResult`s.

#### Evaluate

The evaluation phase evaluates inference results. Its input is `EvaluateRequest`, and its output is the evaluation result `EvalSetResult`.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"

// EvaluateRequest represents an evaluation request.
type EvaluateRequest struct {
	AppName          string             // Application name.
	EvalSetID        string             // Evaluation set ID.
	InferenceResults []*InferenceResult // Inference phase results.
	EvaluateConfig   *EvaluateConfig    // Evaluation configuration.
}

// EvaluateConfig represents the configuration for the evaluation phase.
type EvaluateConfig struct {
	EvalMetrics []*metric.EvalMetric // Metric set to be evaluated.
}
```

Description:

- The framework will call the corresponding evaluator based on the configured `EvalMetrics` to perform evaluation and scoring.
- Each metric result will be aggregated into the final `EvalSetResult`.

### AgentEvaluator

`AgentEvaluator` evaluates an agent based on the configured evaluation set EvalSetID.

```go
// AgentEvaluator evaluates an agent based on an evaluation set.
type AgentEvaluator interface {
	// Evaluate evaluates the specified evaluation set.
	Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error)
	// Close closes the evaluator and releases owned resources.
	Close() error
}
```

`EvaluationResult` represents the final result of a complete evaluation task, including the overall evaluation status, execution time, and a summary of the results of all evaluation cases.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// EvaluationResult contains the aggregated results of multiple evaluation runs.
type EvaluationResult struct {
	AppName       string                  // Application name.
	EvalSetID     string                  // Corresponding evaluation set ID.
	OverallStatus status.EvalStatus       // Overall evaluation status.
	ExecutionTime time.Duration           // Execution duration.
	EvalCases     []*EvaluationCaseResult // Results of each evaluation case.
}
```

`EvaluationCaseResult` aggregates the results of multiple runs of a single evaluation case, including the overall evaluation status, detailed results of each run, and metric-level statistics.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvaluationCaseResult summarizes the results of a single evaluation case across multiple executions.
type EvaluationCaseResult struct {
	EvalCaseID      string                         // Evaluation case ID.
	OverallStatus   status.EvalStatus              // Overall evaluation status.
	EvalCaseResults []*evalresult.EvalCaseResult   // Individual run results.
	MetricResults   []*evalresult.EvalMetricResult // Metric-level results.
}
```

An `AgentEvaluator` instance can be created using `evaluation.New`. By default, it uses the `local` implementations of the `EvalSet Manager`, `Metric Manager`, and `EvalResult Manager`.

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation"

agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithNumRuns(numRuns))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

Because the Agent's execution process may be uncertain, `evaluation.WithNumRuns` provides a mechanism for multiple evaluation runs to reduce the randomness of a single run.

- The default number of runs is 1;
- By specifying `evaluation.WithNumRuns(n)`, each evaluation case can be run multiple times;
- The final result is based on the combined statistical results of multiple runs. The default statistical method is the average of the evaluation scores of multiple runs.

To accelerate the inference phase for large evaluation sets, parallel inference across evaluation cases can be enabled.

- `evaluation.WithEvalCaseParallelInferenceEnabled(true)` enables parallel inference across eval cases. It is disabled by default.
- `evaluation.WithEvalCaseParallelism(n)` sets the maximum number of eval cases inferred in parallel. The default value is `runtime.GOMAXPROCS(0)`.

```go
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalCaseParallelInferenceEnabled(true),
	evaluation.WithEvalCaseParallelism(runtime.GOMAXPROCS(0)),
)
defer agentEvaluator.Close()
```

## Usage Guide

### Local File Path

There are three types of local files:

- EvalSet file
- Metric file
- EvalResult file

#### EvalSet File
The default path for the evaluation set file is `./<AppName>/<EvalSetID>.evalset.json`.

You can set a custom `BaseDir` using `WithBaseDir`, which means the file path will be `<BaseDir>/<AppName>/<EvalSetID>.evalset.json`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
)

evalSetManager := evalsetlocal.New(evalset.WithBaseDir("<BaseDir>"))
agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithEvalSetManager(evalSetManager))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

In addition, if the default path structure does not meet your requirements, you can customize the file path rules by implementing the `Locator` interface. The interface definition is as follows:

```go
// Locator is used to define the path generation and enumeration logic for evaluation set files.
type Locator interface {
	// Build specifies the appName and evalSetID Path to the evaluation set file.
	Build(baseDir, appName, evalSetID string) string
	// List all evaluation set IDs under the specified appName.
	List(baseDir, appName string) ([]string, error)
}
```

For example, set the evaluation set file format to `custom-<EvalSetID>.evalset.json`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
)

evalSetManager := evalsetlocal.New(evalset.WithLocator(&customLocator{}))
agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithEvalSetManager(evalSetManager))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()

type customLocator struct {
}

// Build returns the custom file path format: <BaseDir>/<AppName>/custom-<EvalSetID>.evalset.json.
func (l *customLocator) Build(baseDir, appName, EvalSetID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetID+".evalset.json")
}

// List lists all evaluation set IDs under the specified app.
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
		if strings.HasSuffix(entry.Name(), ".evalset.json") {
			name := strings.TrimPrefix(entry.Name(), "custom-")
			name = strings.TrimSuffix(name, defaultResultFileSuffix)
			results = append(results, name)
		}
	}
	return results, nil
}
```

#### Metric File

The default path for the metrics file is `./<AppName>/<EvalSetID>.metrics.json`.

You can use `WithBaseDir` to set a custom `BaseDir`, meaning the file path will be `<BaseDir>/<AppName>/<EvalSetID>.metrics.json`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
)

metricManager := metriclocal.New(metric.WithBaseDir("<BaseDir>"))
agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithMetricManager(metricManager))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

In addition, if the default path structure does not meet your requirements, you can customize the file path rules by implementing the `Locator` interface. The interface definition is as follows:

```go
// Locator is used to define the path generation for evaluation metric files.
type Locator interface {
	// Build builds the evaluation metric file path for the specified appName and evalSetID.
	Build(baseDir, appName, evalSetID string) string
}
```

For example, set the evaluation set file format to `custom-<EvalSetID>.metrics.json`.

```go
import ( 
	"trpc.group/trpc-go/trpc-agent-go/evaluation" 
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric" 
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
)

metricManager := metriclocal.New(metric.WithLocator(&customLocator{}))
agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithMetricManager(metricManager))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()

type customLocator struct {
}

// Build returns the custom file path format: <BaseDir>/<AppName>/custom-<EvalSetID>.metrics.json.
func (l *customLocator) Build(baseDir, appName, EvalSetID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetID+".metrics.json")
}
```

#### EvalResult File

The default path for the evaluation result file is `./<AppName>/<EvalSetResultID>.evalresult.json`.

You can set a custom `BaseDir` using `WithBaseDir`. For example, the file path will be `<BaseDir>/<AppName>/<EvalSetResultID>.evalresult.json`. The default naming convention for `EvalSetResultID` is `<appName>_<EvalSetID>_<UUID>`.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
)

evalResultManager := evalresultlocal.New(evalresult.WithBaseDir("<BaseDir>"))
agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithEvalResultManager(evalResultManager))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

In addition, if the default path structure does not meet your requirements, you can customize the file path rules by implementing the `Locator` interface. The interface definition is as follows:

```go
// Locator is used to define the path generation and enumeration logic for evaluation result files.
type Locator interface {
	// Build the specified appName and The evaluation result file path for the evalSetResultID.
	Build(baseDir, appName, evalSetResultID string) string
	// List all evaluation result IDs under the specified appName.
	List(baseDir, appName string) ([]string, error)
}
```

For example, set the evaluation result file format to `custom-<EvalSetResultID>.evalresult.json`.

```go
import ( 
	"trpc.group/trpc-go/trpc-agent-go/evaluation" 
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult" 
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
)

evalResultManager := evalresultlocal.New(evalresult.WithLocator(&customLocator{}))
agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithEvalResultManager(evalResultManager))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()

type customLocator struct {
}

// Build returns the custom file path format: <BaseDir>/<AppName>/custom-<EvalSetResultID>.evalresult.json.
func (l *customLocator) Build(baseDir, appName, evalSetResultID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetResultID+".evalresult.json")
}

// List lists all evaluation result IDs under the specified app.
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
		if strings.HasSuffix(entry.Name(), ".evalresult.json") {
			name := strings.TrimPrefix(entry.Name(), "custom-")
			name = strings.TrimSuffix(name, ".evalresult.json")
			results = append(results, name)
		}
	}
	return results, nil
}
```

### Trace evaluation mode

Trace evaluation mode is used to evaluate an offline-collected execution trace, and evaluation does not invoke the Runner for inference.

Set `evalMode: "trace"` in the target evalCase of the EvalSet, and fill `conversation` with the actual output invocation sequence, such as `userContent`, `finalResponse`, `tools`, and `intermediateResponses`. Since trace mode does not provide expected outputs, choose metrics that do not depend on expected outputs, such as `llm_rubric_response`.

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
      "sessionInput": {
        "appName": "trace-eval-app",
        "userId": "demo-user"
      }
    }
  ]
}
```

For a complete example, see [examples/evaluation/trace][trace-eval-example].

[trace-eval-example]: https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/trace

### Evaluation Criterion

The evaluation criterion describes the specific evaluation method and can be combined as needed.

The framework has the following built-in types of evaluation criteria:

| Criterion Type          | Applicable Object                                     |
| ----------------------- | ----------------------------------------------------- |
| TextCriterion           | Text string                                           |
| JSONCriterion           | JSON object, usually used to compare `map[string]any` |
| ToolTrajectoryCriterion | Tool invocation trajectory                            |
| LLMCriterion            | Evaluation based on an LLM judge model                |
| Criterion               | Aggregation of multiple criteria                      |

#### TextCriterion

TextCriterion is used for string matching and can be configured to ignore case and to use a specific matching strategy.

```go
// TextCriterion defines the matching method for strings.
type TextCriterion struct {
	Ignore          bool              // Whether to skip matching.
	CaseInsensitive bool              // Whether case-insensitive.
	MatchStrategy   TextMatchStrategy // Matching strategy.
	Compare         func(actual, expected string) (bool, error) // Custom comparison.
}
```

Explanation of TextMatchStrategy values:

| TextMatchStrategy Value | Description                                                             |
| ----------------------- | ----------------------------------------------------------------------- |
| exact                   | The actual string is exactly the same as the expected string (default). |
| contains                | The actual string contains the expected string.                         |
| regex                   | The actual string matches the expected string as a regular expression.  |

#### JSONCriterion

JSONCriterion is used to compare structured JSON data. You can configure whether to ignore the comparison and choose a specific matching strategy.

```go
// JSONCriterion defines the matching method for JSON objects.
type JSONCriterion struct {
	Ignore          bool                                                // Whether to skip matching.
	IgnoreTree      map[string]any                                      // Ignore tree; a true leaf skips the key and its subtree.
	NumberTolerance *float64                                            // Numeric tolerance; default is 1e-6, 0 means exact; applied to numeric leaf values.
	MatchStrategy   JSONMatchStrategy                                   // Matching strategy.
	Compare         func(actual, expected map[string]any) (bool, error) // Custom comparison.
}
```

Explanation of JSONMatchStrategy values:

| JSONMatchStrategy Value | Description                                                         |
| ----------------------- | ------------------------------------------------------------------- |
| exact                   | The actual JSON is exactly the same as the expected JSON (default). |

`IgnoreTree` lets you skip specific fields and their subtrees while checking the remaining fields.

For example, ignore `metadata.updatedAt` but verify other fields:

```go
criterion := &json.JSONCriterion{
	IgnoreTree: map[string]any{
		"metadata": map[string]any{
			"updatedAt": true,
		},
	},
	NumberTolerance: 1e-6,
}
```

Configuration file example:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": false,
        "defaultStrategy": {
          "name": {
            "matchStrategy": "exact"
          },
          "arguments": {
            "matchStrategy": "exact",
            "numberTolerance": 1e-6,
          },
          "result": {
            "matchStrategy": "exact",
            "numberTolerance": 1e-6,
            "ignoreTree": {
              "metadata": {
                "updatedAt": true
              }
            }
          }
        }
      }
    }
  }
]
```

#### ToolTrajectoryCriterion

ToolTrajectoryCriterion is used to configure the evaluation criteria for tool invocations and results. You can set default strategies, customize strategies by tool name, and control whether invocation order must be preserved.

```go
// ToolTrajectoryCriterion defines the evaluation criteria for tool invocations and results.
type ToolTrajectoryCriterion struct {
	DefaultStrategy *ToolTrajectoryStrategy                                  // Default strategy.
	ToolStrategy    map[string]*ToolTrajectoryStrategy                       // Customized strategies by tool name.
	OrderSensitive  bool                                                     // Whether to require strict order matching.
	SubsetMatching  bool                                                     // Whether expected calls can be a subset of actual.
	Compare         func(actual, expected *evalset.Invocation) (bool, error) // Custom comparison.
}

// ToolTrajectoryStrategy defines the matching strategy for a single tool.
type ToolTrajectoryStrategy struct {
	Name      *TextCriterion // Tool name matching.
	Arguments *JSONCriterion // Invocation arguments matching.
	Result    *JSONCriterion // Tool result matching.
}
```

DefaultStrategy is used to configure the global default evaluation criterion and applies to all tools.

ToolStrategy overrides the evaluation criterion for specific tools by tool name. When ToolStrategy is not set, all tool invocations use DefaultStrategy.

If no evaluation criterion is configured, the framework uses the default evaluation criterion: tool names are compared using TextCriterion with the `exact` strategy, and arguments and results are compared using JSONCriterion with the `exact` strategy. This ensures that tool trajectory evaluation always has a reasonable fallback behavior.

The following example illustrates a typical scenario: for most tools you want strict alignment of tool invocations and results, but for time-related tools such as `current_time`, the response value itself is unstable. Therefore, you only need to check whether the correct tool and arguments were invoked as expected, without requiring the time value itself to be exactly the same.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

criterion := criterion.New(
	criterion.WithToolTrajectory(
		tooltrajectory.New(
			tooltrajectory.WithDefault(
				&tooltrajectory.ToolTrajectoryStrategy{
					Name: &text.TextCriterion{
						MatchStrategy: text.TextMatchStrategyExact,
					},
					Arguments: &json.JSONCriterion{
						MatchStrategy: json.JSONMatchStrategyExact,
					},
					Result: &json.JSONCriterion{
						MatchStrategy: json.JSONMatchStrategyExact,
					},
				},
			),
			tooltrajectory.WithTool(map[string]*tooltrajectory.ToolTrajectoryStrategy{
				"current_time": {
					Name: &text.TextCriterion{
						MatchStrategy: text.TextMatchStrategyExact,
					},
					Arguments: &json.JSONCriterion{
						MatchStrategy: json.JSONMatchStrategyExact,
					},
					Result: &json.JSONCriterion{
						Ignore: true, // Ignore matching of this tool's result.
					},
				},
			}),
		),
	),
)
```
	
By default, tool invocation matching is order-insensitive. Each expected tool attempts to pair with any actual tool that satisfies the strategy, and a single actual invocation will not be reused. Matching passes when all expected tools find a partner. Specifically, the evaluator builds a bipartite graph with expected invocations as left nodes and actual invocations as right nodes; for every expected/actual pair that satisfies the tool strategy, it adds an edge. It then uses the Kuhn algorithm to compute the maximum matching and checks unmatched expected nodes. If every expected node is matched, tool matching passes; otherwise, the unmatched expected nodes are returned.
	
If you want to strictly compare in the order tools appear, enable `WithOrderSensitive(true)`. The evaluator scans the expected and actual lists in order and fails if an expected invocation cannot find a matching actual invocation.

```go
criterion := criterion.New(
	criterion.WithToolTrajectory(
		ctooltrajectory.New(
			ctooltrajectory.WithOrderSensitive(true), // Enable order-sensitive matching.
		),
	),
)
```

Configuration example for strict order matching:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
        "orderSensitive": true,
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
        }
      }
    }
  }
]
```

SubsetMatching controls whether the expected tool sequence can be just a subset of the actual tool sequence. It is off by default.

- Off: the expected and actual tool call counts must be the same.
- On: the actual sequence may be longer, allowing the expected tools to be a subset of the actual tools.

```go
criterion := criterion.New(
	criterion.WithToolTrajectory(
		ctooltrajectory.New(
			ctooltrajectory.WithSubsetMatching(true),
		),
	),
)
```

Configuration example with subset matching:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {
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
        }
      }
    }
  }
]
```

Assume `A`, `B`, `C`, and `D` each denote one tool call. Matching examples:

| SubsetMatching | OrderSensitive | Expected | Actual | Result | Note |
| --- | --- | --- | --- | --- | --- |
| Off | Off | `[A]` | `[A, B]` | Mismatch | Count differs |
| On | Off | `[A]` | `[A, B]` | Match | Expected is subset |
| On | Off | `[C, A]` | `[A, B, C]` | Match | Expected is subset with order-insensitive matching |
| On | On | `[A, C]` | `[A, B, C]` | Match | Expected is subset and order matches |
| On | On | `[C, A]` | `[A, B, C]` | Mismatch | Order not satisfied |
| On | Off | `[C, D]` | `[A, B, C]` | Mismatch | Actual tool sequence missing D |
| Any | Any | `[A, A]` | `[A]` | Mismatch | Actual calls insufficient; a single call cannot be reused |


#### LLMCriterion

LLMCriterion is used to configure an LLM-based evaluation criterion for scenarios where a model produces the judgment.

```go
// LLMCriterion configures the judge model.
type LLMCriterion struct {
	Rubrics    []*Rubric          // Rubric configuration.
	JudgeModel *JudgeModelOptions // Judge model configuration.
}

// Rubric defines a rubric item.
type Rubric struct {
	ID          string         // Unique rubric ID.
	Description string         // Human-readable description.
	Type        string         // Rubric type.
	Content     *RubricContent // Rubric content for the judge model.
}

// RubricContent defines rubric content.
type RubricContent struct {
	Text string // Concrete rubric content.
}

// JudgeModelOptions defines judge model parameters.
type JudgeModelOptions struct {
	ProviderName string                  // Model provider name.
	ModelName    string                  // Judge model name.
	BaseURL      string                  // Model base URL.
	APIKey       string                  // Model API key.
	ExtraFields  map[string]any          // Extra request fields.
	NumSamples   int                     // Number of evaluation samples.
	Generation   *model.GenerationConfig // Generation config for the judge model.
}
```

- `Rubrics` defines rubric items and is only used in rubric-style evaluators. Expected outputs are not needed; the judge model evaluates each rubric.
- `NumSamples` controls how many times the judge model is called, defaulting to 1 when not set.
- `Generation` defaults to `MaxTokens=2000`, `Temperature=0.8`, and `Stream=false`.

For security reasons, it is recommended not to write `judgeModel.apiKey` / `judgeModel.baseURL` in plaintext in metric config files or code.

The framework supports environment variable placeholders for `judgeModel.providerName`, `judgeModel.modelName`, `judgeModel.apiKey` and `judgeModel.baseURL` in `.metrics.json`. When loading the config, the placeholders are automatically expanded to the corresponding environment variable values.

For example:

```json
[
  {
    "metricName": "llm_final_response",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "${JUDGE_MODEL_PROVIDER_NAME}",
          "modelName":  "${JUDGE_MODEL_NAME}",
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

You can pass a custom configuration via `criterion.WithLLMJudge`, for example:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

criterion := criterion.New(
	criterion.WithLLMJudge(
		llm.New(
			"openai",
			"deepseek-chat",
			llm.WithNumSamples(3),
			llm.WithGeneration(&model.GenerationConfig{
				MaxTokens:   floatPtr(512),
				Temperature: floatPtr(1.0),
				Stream:      false,
			}),
			llm.WithRubrics([]*llm.Rubric{
				{
					ID:          "1",
					Type:        "FINAL_RESPONSE_QUALITY",
					Description: "The final answer is correct.",
					Content: &llm.RubricContent{
						Text: "The final answer directly addresses the user question, provides the required result, and is consistent with the facts given.",
					},
				},
				{
					ID:          "2",
					Type:        "CONTEXT_RELEVANCE",
					Description: "The final answer is relevant to the user prompt.",
					Content: &llm.RubricContent{
						Text: "The final answer stays on topic and does not include unrelated or missing key points from the user prompt.",
					},
				},
			}),
		),
	),
)
```

### Evaluator

#### Tool Trajectory Evaluator

The metric name corresponding to the tool trajectory evaluator is `tool_trajectory_avg_score`. It is used to evaluate whether the Agent’s use of tools across multiple conversations conforms to expectations.

In a single conversation, the evaluator compares the actual tool invocation trajectory with the expected trajectory using `ToolTrajectoryCriterion`:

* If the entire tool invocation trajectory satisfies the evaluation criterion, the score of this conversation on this metric is 1.
* If any step of the invocation does not satisfy the evaluation criterion, the score of this conversation on this metric is 0.

In the scenario of multiple conversations, the evaluator takes the average of the scores of all conversations on this metric as the final `tool_trajectory_avg_score`, and compares it with `EvalMetric.Threshold` to determine whether the result is pass or fail.

A typical way to combine the tool trajectory evaluator with Metric and Criterion is as follows:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	ctooltrajectory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

evalMetric := &metric.EvalMetric{
	MetricName: "tool_trajectory_avg_score",
	Threshold:  1.0,
	Criterion: criterion.New(
		criterion.WithToolTrajectory(
			// Use the default evaluation criterion; tool name, arguments, and result must be strictly identical.
			ctooltrajectory.New(),
		),
	),
}
```

An example of the corresponding metric config file:

```json
[
  {
    "metricName": "tool_trajectory_avg_score",
    "threshold": 1,
    "criterion": {
      "toolTrajectory": {}
    }
  }
]
```

For a complete example, see [examples/evaluation/tooltrajectory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/tooltrajectory).

#### Final Response Evaluator

The metric name corresponding to the final response evaluator is `final_response_avg_score`. It does not require an LLM and compares the Agent’s final response with the expected output using deterministic rules. It is suitable for cases where you need strict text or JSON output validation.

Evaluation logic:

- Use `FinalResponseCriterion` to compare `Invocation.FinalResponse.Content` for each invocation; a match scores 1, otherwise 0.
- For multiple runs, take the average score across invocations and compare it with `EvalMetric.Threshold` to determine pass/fail.

`FinalResponseCriterion` supports two criteria:

- `text`: Compare plain text using `TextCriterion` with strategies like `exact/contains/regex`. For details, see [TextCriterion](#textcriterion).
- `json`: Parse `FinalResponse.Content` as JSON and compare using `JSONCriterion`. You can configure `ignoreTree`, `numberTolerance`, and more. For details, see [JSONCriterion](#jsoncriterion).

Code example:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	ctext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

evalMetric := &metric.EvalMetric{
	MetricName: "final_response_avg_score",
	Threshold:  1.0,
	Criterion: criterion.New(
		criterion.WithFinalResponse(
			cfinalresponse.New(
				cfinalresponse.WithJSONCriterion(cjson.New()),
				cfinalresponse.WithTextCriterion(ctext.New()),
			),
		),
	),
}
```

An example metric config file

```json
[
  {
    "metricName": "final_response_avg_score",
    "threshold": 1,
    "criterion": {
      "finalResponse": {
        "text": {
          "matchStrategy": "exact"
        },
        "json": {
          "matchStrategy": "exact"
        }
      }
    }
  }
]
```

#### LLM Final Response Evaluator

The metric name for the LLM final response evaluator is `llm_final_response`. It uses a judge model to determine whether the Agent’s final answer is valid. The judge prompt includes the user input, reference answer, and the Agent’s final answer, making it suitable for automatically checking the final text output.

Evaluation logic:

- Use the `JudgeModel` in `LLMCriterion` to call the judge model, sampling multiple times according to `NumSamples`.
- The judge model must return the field `is_the_agent_response_valid` with values `valid` or `invalid` (case-insensitive); `valid` scores 1, `invalid` scores 0. Other results or parse failures produce an error.
- With multiple samples, use majority voting to aggregate, then compare the final score against `EvalMetric.Threshold` to get the evaluation result.

A typical configuration looks like this:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	cllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

evalMetric := &metric.EvalMetric{
	MetricName: "llm_final_response",
	Threshold:  0.9,
	Criterion: criterion.New(
		criterion.WithLLMJudge(
			cllm.New(
				"openai",
				"gpt-4o",
				cllm.WithBaseURL(os.Getenv("JUDGE_MODEL_BASE_URL")),
				cllm.WithAPIKey(os.Getenv("JUDGE_MODEL_API_KEY")),
				cllm.WithNumSamples(3),
				cllm.WithGeneration(&model.GenerationConfig{
					MaxTokens:   ptr(512),
					Temperature: ptr(1.0),
					Stream:      false,
				}),
			),
		),
	),
}
```

An example metric config file:

```json
[
  {
    "metricName": "llm_final_response",
    "threshold": 0.9,
    "criterion": {
      "llmJudge": {
        "judgeModel": {
          "providerName": "openai",
          "modelName": "gpt-4o",
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

See the complete example at [examples/evaluation/llm/finalresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/finalresponse).

#### LLM Rubric Response Evaluator

The metric name for the LLM rubric response evaluator is `llm_rubric_response`. It checks whether the Agent’s final answer meets each rubric requirement.

Evaluation logic:

- Use `Rubrics` in `LLMCriterion` to build the prompt, and the judge model returns `yes`/`no` for each rubric.
- The score for a single sample is the average of all rubric scores (`yes`=1, `no`=0).
- With multiple samples, pick the representative result via majority voting, then compare against `EvalMetric.Threshold` to determine pass/fail.

Typical configuration:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	cllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

evalMetric := &metric.EvalMetric{
	MetricName: "llm_rubric_response",
	Threshold:  0.9,
	Criterion: criterion.New(
		criterion.WithLLMJudge(
			cllm.New(
				"openai",
				"deepseek-chat",
				cllm.WithBaseURL(os.Getenv("JUDGE_MODEL_BASE_URL")),
				cllm.WithAPIKey(os.Getenv("JUDGE_MODEL_API_KEY")),
				cllm.WithNumSamples(3),
				cllm.WithGeneration(&model.GenerationConfig{
					MaxTokens:   ptr(512),
					Temperature: ptr(1.0),
					Stream:      false,
				}),
				cllm.WithRubrics([]*cllm.Rubric{
					{
						ID:          "1",
						Type:        "FINAL_RESPONSE_QUALITY",
						Description: "The final answer is correct.",
						Content: &cllm.RubricContent{
							Text: "The final answer is correct and consistent with the user request.",
						},
					},
					{
						ID:          "2",
						Type:        "CONTEXT_RELEVANCE",
						Description: "The final answer is relevant to the user prompt.",
						Content: &cllm.RubricContent{
							Text: "The final answer is relevant to the user prompt without unrelated content.",
						},
					},
				}),
			),
		),
	),
}
```

An example metric config file:

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
            "type": "FINAL_RESPONSE_QUALITY",
            "description": "The final answer is correct.",
            "content": {
              "text": "The final answer is correct and consistent with the user request."
            }
          },
          {
            "id": "2",
            "type": "CONTEXT_RELEVANCE",
            "description": "The final answer is relevant to the user prompt.",
            "content": {
              "text": "The final answer is relevant to the user prompt without unrelated content."
            }
          }
        ]
      }
    }
  }
]
```

See the complete example at [examples/evaluation/llm/rubricresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/rubricresponse).

#### LLM Rubric Knowledge Recall Evaluator

The metric name for the LLM rubric knowledge recall evaluator is `llm_rubric_knowledge_recall`. It determines whether the retrieved knowledge supports the key information in the user question.

Evaluation logic:

- Extract responses from the `knowledge_search`/`knowledge_search_with_agentic_filter` tools in `IntermediateData.ToolResponses` as retrieval results.
- Combine `Rubrics` to build the prompt. The judge model returns `yes`/`no` for each rubric, and the score for a single sample is the average.
- With multiple samples, use majority voting to pick the representative result, then compare with the threshold for the final conclusion.

Typical configuration:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	cllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

evalMetric := &metric.EvalMetric{
	MetricName: "llm_rubric_knowledge_recall",
	Threshold:  0.9,
	Criterion: criterion.New(
		criterion.WithLLMJudge(
			cllm.New(
				"openai",
				"deepseek-chat",
				cllm.WithBaseURL(os.Getenv("JUDGE_MODEL_BASE_URL")),
				cllm.WithAPIKey(os.Getenv("JUDGE_MODEL_API_KEY")),
				cllm.WithNumSamples(3),
				cllm.WithGeneration(&model.GenerationConfig{
					MaxTokens:   ptr(512),
					Temperature: ptr(1.0),
					Stream:      false,
				}),
				cllm.WithRubrics([]*cllm.Rubric{
					{
						ID:          "1",
						Type:        "KNOWLEDGE_RELEVANCE",
						Description: "The recalled knowledge is relevant to the user's prompt.",
						Content: &cllm.RubricContent{
							Text: "The retrieved knowledge directly supports the user prompt and includes key facts.",
						},
					},
				}),
			),
		),
	),
}
```

An example metric config file:

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
            "type": "KNOWLEDGE_RELEVANCE",
            "description": "The recalled knowledge is relevant to the user's prompt.",
            "content": {
              "text": "The retrieved knowledge directly supports the user prompt and includes key facts."
            }
          }
        ]
      }
    }
  }
]
```

This evaluator requires the Agent’s tool calls to return retrieval results. See the complete example at [examples/evaluation/llm/knowledgerecall](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/knowledgerecall).

### Callback

Evaluation supports registering callbacks at key points in the evaluation flow. Callbacks can be used for observability and instrumentation, passing `Context`, and adjusting request parameters.

Create a callback registry with `service.NewCallbacks()`, register callback components, then pass it into `evaluation.New` using `evaluation.WithCallbacks`:

```go
import (
	"context"
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

For registering a single callback point, you can also use point-specific registration methods such as `callbacks.RegisterBeforeInferenceSet(name, fn)`.

For a complete example, see [examples/evaluation/callbacks](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/callbacks).

Callback points are described in the table below.

| Callback | When |
| --- | --- |
| `BeforeInferenceSet` | Before the inference phase starts; runs once per EvalSet |
| `AfterInferenceSet` | After the inference phase ends; runs once per EvalSet |
| `BeforeInferenceCase` | Before a single EvalCase inference starts; runs once per EvalCase |
| `AfterInferenceCase` | After a single EvalCase inference ends; runs once per EvalCase |
| `BeforeEvaluateSet` | Before the evaluation phase starts; runs once per EvalSet |
| `AfterEvaluateSet` | After the evaluation phase ends; runs once per EvalSet |
| `BeforeEvaluateCase` | Before a single EvalCase evaluation starts; runs once per EvalCase |
| `AfterEvaluateCase` | After a single EvalCase evaluation ends; runs once per EvalCase |

Callbacks at the same point run in registration order. If any callback returns an `error`, execution aborts at that point. The error is wrapped with callback point, index, and component name.

A callback returns `Result` and `error`. `Result` is optional and is used to pass an updated `Context` within the same callback point and to later stages; `error` aborts the flow and is returned. Common return forms include:

- `return nil, nil`: continue using the current `ctx` for subsequent callbacks. If a previous callback at the same point has already updated `ctx` via `Result.Context`, this return form does not overwrite it.
- `return result, nil`: update `ctx` to `result.Context`. Subsequent callbacks and later stages use the updated `ctx`.
- `return nil, err`: abort the current callback point and return the error.

When parallel inference is enabled with `evaluation.WithEvalCaseParallelInferenceEnabled(true)`, case-level callbacks may run concurrently. Since `args.Request` points to the same `*InferenceRequest`, treat it as read-only. If you need to mutate requests, do it in set-level callbacks.

Per-case inference or evaluation failures usually are not propagated via `error`; they are written into `Result.Status` and `Result.ErrorMessage`. `After*CaseArgs.Error` is not used to carry per-case failure reasons. To determine whether a case failed, check `args.Result.Status` and `args.Result.ErrorMessage`.

## Best Practices

### Context Injection

`contextMessages` provides additional context messages for an EvalCase. It is commonly used to add background information, role setup, or examples. It also supports pure model evaluation by configuring the system prompt per case as evaluation data to compare different model and prompt combinations.

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

For a complete example, see [examples/evaluation/contextmessage](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/contextmessage).
