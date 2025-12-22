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
            "intermediateData": {
              "toolCalls": [
                {
                  "id": "tool_use_1",
                  "type": "function",
                  "function": {
                    "name": "calculator",
                    "arguments": {
                      "operation": "add",
                      "a": 2,
                      "b": 3
                    }
                  }
                }
              ],
              "toolResponses": [
                {
                  "role": "tool",
                  "toolId": "tool_use_1",
                  "toolName": "calculator",
                  "content": {
                    "a": 2,
                    "b": 3,
                    "operation": "add",
                    "result": 5
                  }
                }
              ]
            }
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
        "defaultStrategy": {
          "name": {
            "matchStrategy": "exact"
          },
          "arguments": {
            "matchStrategy": "exact"
          },
          "response": {
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
  "evalSetResultId": "math-eval-app_math-basic_64377112-1403-4e7d-ab90-8fce26f5aeb0",
  "evalSetResultName": "math-eval-app_math-basic_64377112-1403-4e7d-ab90-8fce26f5aeb0",
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
                "response": {
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
            "invocationId": "74ca1b65-e143-4c98-b42e-3239f3b91ea0",
            "userContent": {
              "role": "user",
              "content": "calc add 2 3"
            },
            "finalResponse": {
              "role": "assistant",
              "content": "2 + 3 = 5"
            },
            "intermediateData": {
              "toolCalls": [
                {
                  "id": "call_00_YFh5dH5naCL8SDmdPGx23lbT",
                  "type": "function",
                  "function": {
                    "name": "calculator",
                    "arguments": {
                      "a": 2,
                      "b": 3,
                      "operation": "add"
                    }
                  }
                }
              ],
              "toolResponses": [
                {
                  "role": "tool",
                  "toolId": "call_00_YFh5dH5naCL8SDmdPGx23lbT",
                  "toolName": "calculator",
                  "content": {
                    "a": 2,
                    "b": 3,
                    "operation": "add",
                    "result": 5
                  }
                }
              ]
            }
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
            "intermediateData": {
              "toolCalls": [
                {
                  "id": "tool_use_1",
                  "type": "function",
                  "function": {
                    "name": "calculator",
                    "arguments": {
                      "a": 2,
                      "b": 3,
                      "operation": "add"
                    }
                  }
                }
              ],
              "toolResponses": [
                {
                  "role": "tool",
                  "toolId": "tool_use_1",
                  "toolName": "calculator",
                  "content": {
                    "a": 2,
                    "b": 3,
                    "operation": "add",
                    "result": 5
                  }
                }
              ]
            }
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
                    "response": {
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
      "sessionId": "154015e2-2126-4ff5-9da0-d70012b819f5",
      "userId": "user"
    }
  ],
  "creationTimestamp": 1765982990.106037
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
				IntermediateData: &evalset.IntermediateData{
					ToolCalls: []*model.ToolCall{
						{
							ID:   "tool_use_1",
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "calculator",
								Arguments: []byte(`{"operation":"add","a":2,"b":3}`),
							},
						},
					},
					ToolResponses: []*model.Message{
						{
							Role:     model.RoleTool,
							ToolID:   "tool_use_1",
							ToolName: "calculator",
							Content:  `{"a":2,"b":3,"operation":"add","result":5}`,
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
import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"

evalMetric := &metric.EvalMetric{
	MetricName: "tool_trajectory_avg_score",
	Threshold:  1.0,
	Criterion: criterion.New(
		criterion.WithToolTrajectory(
			ctooltrajectory.New(
				ctooltrajectory.WithDefault(
					&ctooltrajectory.ToolTrajectoryStrategy{
						Name: &text.TextCriterion{
							MatchStrategy: text.TextMatchStrategyExact,
						},
						Arguments: &cjson.JSONCriterion{
							MatchStrategy: cjson.JSONMatchStrategyExact,
						},
						Response: &cjson.JSONCriterion{
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
  - Inference: Extracting user input from the EvalSet, invoking the Agent to perform inference, and combining the Agent's actual output with the expected output to form the inference result. 
  - Result Evaluation Phase: Evaluate retrieves the corresponding evaluator from the registry based on the evaluation metric name. Multiple evaluators are used to perform a multi-dimensional evaluation of the inference results, ultimately generating the evaluation result, EvalResult.
- Agent Evaluator: To reduce the randomness of the agent's output, the evaluation service is called NumRuns times and aggregates the results to obtain a more stable evaluation result.

### EvalSet

An EvalSet is a collection of EvalCase instances, identified by a unique EvalSetID, serving as session data within the evaluation process.

An EvalCase represents a set of evaluation cases within the same Session and includes a unique identifier (EvalID), the conversation content, and session initialization information.

Conversation data includes three types of content:

- User input
- Agent final response
- Agent intermediate response, including:
  - Tool invocation
  - Tool response
  - Intermediate response information

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
	Conversation      []*Invocation        // Conversation sequence.
	SessionInput      *SessionInput        // Session initialization data.
	CreationTimestamp *epochtime.EpochTime // Creation time.
}

// Invocation represents a user-agent interaction.
type Invocation struct {
	InvocationID      string
	UserContent       *model.Message       // User input.
	FinalResponse     *model.Message       // Agent final response.
	IntermediateData  *IntermediateData    // Agent intermediate response data.
	CreationTimestamp *epochtime.EpochTime // Creation time.
}

// IntermediateData represents intermediate data during execution.
type IntermediateData struct {
	ToolCalls             []*model.ToolCall   // Tool calls.
	ToolResponses         []*model.Message    // Tool responses.
	IntermediateResponses []*model.Message    // Intermediate responses.
}

// SessionInput represents session initialization input.
type SessionInput struct {
	AppName string                 // Application name.
	UserID  string                 // User ID.
	State   map[string]any         // Initial state.
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
	OverallScore         float64               // Overall score.
	OverallStatus        status.EvalStatus     // Overall status, categorized as passed/failed/not evaluated.
	PerInvocationResults []PerInvocationResult // Evaluation results for a single session.
}

// PerInvocationResult represents the evaluation results for a single session.
type PerInvocationResult struct {
	ActualInvocation   *evalset.Invocation // Actual session.
	ExpectedInvocation *evalset.Invocation // Expected session.
	Score              float64             // Current session score.
	Status             status.EvalStatus   // Current session status.
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

- `tool_trajectory_avg_score` tool trajectory consistency evaluator.
  - For a single session:
    - If the actual tool call sequence is exactly the same as the expected one, a score of 1 is assigned;
    - If not, a score of 0 is assigned.
- For multiple sessions: The final score is calculated by averaging the scores from each session.

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
	MetricName string               // Metric name.
	Score      float64              // Actual score.
	EvalStatus status.EvalStatus    // Evaluation status.
	Threshold  float64              // Score threshold.
	Criterion  *criterion.Criterion // Evaluation criterion.
	Details    map[string]any       // Additional information, such as scoring process, error description, etc.
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
- EvalSetResult

The Service interface defines the complete evaluation process, including the inference and evaluation phases. The interface definition is as follows:

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"

// Service defines the core interface of the evaluation service.
type Service interface {
	// Inference performs inference, calls the Agent to process the specified evaluation case, 
	// and returns the inference result.
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error)
	// Evaluate evaluates the inference result, generates and persists the evaluation result.
	Evaluate(ctx context.Context, request *EvaluateRequest) (*evalresult.EvalSetResult, error)
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
```

Because the Agent's execution process may be uncertain, `evaluation.WithNumRuns` provides a mechanism for multiple evaluation runs to reduce the randomness of a single run.

- The default number of runs is 1;
- By specifying `evaluation.WithNumRuns(n)`, each evaluation case can be run multiple times;
- The final result is based on the combined statistical results of multiple runs. The default statistical method is the average of the evaluation scores of multiple runs.

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

### Evaluation Criterion

The evaluation criterion describes the specific evaluation method and can be combined as needed.

The framework has the following built-in types of evaluation criteria:

| Criterion Type          | Applicable Object                                     |
| ----------------------- | ----------------------------------------------------- |
| TextCriterion           | Text string                                           |
| JSONCriterion           | JSON object, usually used to compare `map[string]any` |
| ToolTrajectoryCriterion | Tool invocation trajectory                            |
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
	Ignore        bool               // Whether to skip matching.
	MatchStrategy JSONMatchStrategy  // Matching strategy.
	Compare       func(actual, expected map[string]any) (bool, error) // Custom comparison.
}
```

Explanation of JSONMatchStrategy values:

| JSONMatchStrategy Value | Description                                                         |
| ----------------------- | ------------------------------------------------------------------- |
| exact                   | The actual JSON is exactly the same as the expected JSON (default). |

#### ToolTrajectoryCriterion

ToolTrajectoryCriterion is used to configure the evaluation criteria for tool invocations and responses. You can set default strategies, customize strategies by tool name, and control whether to ignore the invocation order.

```go
// ToolTrajectoryCriterion defines the evaluation criteria for tool invocations and responses.
type ToolTrajectoryCriterion struct {
	DefaultStrategy  *ToolTrajectoryStrategy            // Default strategy.
	ToolStrategy     map[string]*ToolTrajectoryStrategy // Customized strategies by tool name.
	OrderInsensitive bool                               // Whether to ignore invocation order.
	Compare          func(actual, expected *evalset.Invocation) (bool, error) // Custom comparison.
}

// ToolTrajectoryStrategy defines the matching strategy for a single tool.
type ToolTrajectoryStrategy struct {
	Name      *TextCriterion  // Tool name matching.
	Arguments *JSONCriterion  // Invocation arguments matching.
	Response  *JSONCriterion  // Tool response matching.
}
```

DefaultStrategy is used to configure the global default evaluation criterion and applies to all tools.

ToolStrategy overrides the evaluation criterion for specific tools by tool name. When ToolStrategy is not set, all tool invocations use DefaultStrategy.

If no evaluation criterion is configured, the framework uses the default evaluation criterion: tool names are compared using TextCriterion with the `exact` strategy, and arguments and responses are compared using JSONCriterion with the `exact` strategy. This ensures that tool trajectory evaluation always has a reasonable fallback behavior.

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
					Response: &json.JSONCriterion{
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
					Response: &json.JSONCriterion{
						Ignore: true, // Ignore matching of this tool's response.
					},
				},
			}),
		),
	),
)
```

By default, tool invocations are compared one by one in the order in which they appear. The actual tool invocation sequence and the expected tool invocation sequence must match in length, order, and in the tool name, arguments, and response at each step. If the invocation order is different, the evaluation will be considered as failed.

OrderInsensitive controls whether the tool invocation order is ignored. When enabled, the evaluation logic first generates a sorting key for each tool invocation (composed of the tool name and the normalized representation of arguments and response). It then sorts the actual invocation sequence and the expected invocation sequence by this key, producing two invocation lists with stable order. Next, it compares the corresponding invocations in the sorted lists one by one, and determines whether these invocations match according to the configured evaluation criteria. Put simply, as long as the tool invocations on both sides are completely identical in content, the evaluation will not fail due to differences in the original invocation order. For example:

```go
criterion := criterion.New(
	criterion.WithToolTrajectory(
		ctooltrajectory.New(
			ctooltrajectory.WithOrderInsensitive(true),
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
			// Use the default evaluation criterion; tool name, arguments, and response must be strictly identical.
			ctooltrajectory.New(),
		),
	),
}
```
