# Evaluation 使用文档

Evaluation 提供完整的 Agent 评估框架，支持本地文件和内存两种模式的评估数据管理，提供了 Agent 的多维度评估功能。

## 快速开始

本节介绍如何在本地文件系统 local 或内存 inmemory 模式下执行 Agent 评估流程。

### 本地文件系统 local

local 在本地文件系统上维护评估集、评估指标和评估结果。

完整示例参见 [examples/evaluation/local](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/local)。

#### 代码

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

// 创建 Runner
runner := runner.NewRunner(appName, agent)
// 创建评估集 EvalSet Manager、评估指标 Metric Manager、评估结果 EvalResult Manager、评估器注册中心 Registry
evalSetManager := evalsetlocal.New(evalset.WithBaseDir(*inputDir))
metricManager := metriclocal.New(metric.WithBaseDir(*inputDir))
evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(*outputDir))
registry := registry.New()
// 创建 AgentEvaluator
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
// 执行评估
result, err := agentEvaluator.Evaluate(context.Background(), evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
```

#### 评估集 EvalSet 文件示例

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

#### 评估指标 Metric 文件示例

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

#### 评估结果 EvalResult 文件示例

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

### 内存 inmemory

inmemory 在内存中维护评估集、评估指标和评估结果。

完整示例参见 [examples/evaluation/inmemory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/inmemory)。

#### 代码

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

// 创建 Runner
run := runner.NewRunner(appName, agent)
// 创建评估集 EvalSet Manager、评估指标 Metric Manager、评估结果 EvalResult Manager、评估器注册中心 Registry
evalSetManager := evalsetinmemory.New()
metricManager := metricinmemory.New()
evalResultManager := evalresultinmemory.New()
registry := registry.New()
// 构建评估集数据
if err := prepareEvalSet(ctx, evalSetManager); err != nil {
	log.Fatalf("prepare eval set: %v", err)
}
// 构建评估指标数据
if err := prepareMetric(ctx, metricManager); err != nil {
	log.Fatalf("prepare metric: %v", err)
}
// 创建 AgentEvaluator
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
// 执行评估
result, err := agentEvaluator.Evaluate(ctx, evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
```

#### 评估集 EvalSet 构建

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

#### 评估指标 Metric 构建

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

## 核心概念

![evaluation](../assets/img/evaluation/evaluation.png)

- 评估集 EvalSet 提供评估所需的数据集，包含用户输入及其对应的预期 Agent 输出。
- 评估指标 Metric 定义用于衡量模型表现的指标信息，包括指标名称及对应的分数阈值。
- 评估器 Evaluator 负责对比实际会话结果与预期会话结果，计算具体得分，并依据评估指标阈值判断评估状态。
- 评估器注册中心 Registry 维护评估指标名称与对应评估器的映射关系，支持动态注册与查找评估器。
- 评估服务 Service 作为核心组件，整合了待评估的 Agent、评估集 EvalSet、评估指标 Metric、评估器注册中心 Registry 以及评估结果 EvalResult Registry。评估流程分为两个阶段：
  - 推理阶段 Inference：默认模式下从评估集提取用户输入并调用 Agent 执行推理，将 Agent 的实际输出与预期输出组合形成推理结果；trace 模式下直接将评估集 `conversation` 作为实际 trace 输出，跳过 Runner 推理。
  - 结果评估阶段 Evaluate：根据评估指标名称 Metric Name 从注册中心获取相应的评估器，并使用多个评估器对推理结果进行多维度评估，最终生成评估结果  EvalResult。
- Agent Evaluator 为降低 Agent 输出的偶然性，评估服务会被调用 NumRuns 次，并聚合多次结果，以获得更稳定的评估结果。

### 评估集 -- EvalSet

EvalSet 是一组 EvalCase 的集合，通过唯一的 EvalSetID 进行标识，作为评估流程中的会话数据。

而 EvalCase 表示同一 Session 下的一组评估用例，包含唯一标识符 EvalID、对话内容、可选的 `contextMessages` 以及 Session 初始化信息。

对话数据包括四类内容：

- 用户输入
- Agent 最终响应
- 工具调用与结果
- 中间响应信息

EvalCase 支持通过 `evalMode` 配置评估模式：

- 默认模式（`evalMode` 省略或空字符串）：`conversation` 作为预期输出，评估过程会调用 Runner/Agent 生成实际输出。
- trace 模式（`evalMode` 为 `"trace"`）：`conversation` 作为实际输出 trace，评估过程不会调用 Runner/Agent 执行推理。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalMode 表示评估模式类型
type EvalMode string

const (
	EvalModeDefault EvalMode = ""      // EvalModeDefault 表示默认模式
	EvalModeTrace   EvalMode = "trace" // EvalModeTrace 表示 Trace 评估模式
)

// EvalSet 表示一个评估集
type EvalSet struct {
	EvalSetID         string               // 评估集唯一标识
	Name              string               // 评估集名称
	Description       string               // 评估集描述
	EvalCases         []*EvalCase          // 所有评估用例
	CreationTimestamp *epochtime.EpochTime // 创建时间
}

// EvalCase 表示单个评估用例
type EvalCase struct {
	EvalID            string               // 用例唯一标识
	EvalMode          EvalMode             // 评估模式
	ContextMessages   []*model.Message     // 用于在每次推理时注入上下文消息。
	Conversation      []*Invocation        // 对话序列
	SessionInput      *SessionInput        // Session 初始化数据
	CreationTimestamp *epochtime.EpochTime // 创建时间
}

// Invocation 表示一次用户与 Agent 的交互
type Invocation struct {
	InvocationID          string
	UserContent           *model.Message       // 用户输入
	FinalResponse         *model.Message       // Agent 最终响应
	Tools                 []*Tool              // 工具调用与工具执行结果
	IntermediateResponses []*model.Message     // Agent 中间响应数据
	CreationTimestamp     *epochtime.EpochTime // 创建时间
}

// Tool 表示一次工具调用和工具执行结果
type Tool struct {
	ID        string // 工具调用 ID
	Name      string // 工具名
	Arguments any    // 工具调用输入参数
	Result    any    // 工具执行结果
}

// SessionInput 表示 Session 初始化输入
type SessionInput struct {
	AppName string         // 应用名
	UserID  string         // 用户 ID
	State   map[string]any // 初始状态
}
```

EvalSet Manager 负责对评估集进行增删改查等操作，接口定义如下：

```go
type Manager interface {
	Get(ctx context.Context, appName, evalSetID string) (*EvalSet, error)                  // 获取指定 EvalSet
	Create(ctx context.Context, appName, evalSetID string) (*EvalSet, error)               // 创建新 EvalSet
	List(ctx context.Context, appName string) ([]string, error)                            // 列出所有 EvalSet ID
	Delete(ctx context.Context, appName, evalSetID string) error                           // 删除指定 EvalSet
	GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*EvalCase, error) // 获取指定用例
	AddCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error      // 向评估集添加用例
	UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *EvalCase) error   // 更新用例
	DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error           // 删除用例
}
```

框架为 EvalSet Manager 提供了两种实现：

- local：将评估集存储在本地文件系统中，文件命名格式为 `<EvalSetID>.evalset.json`。
- inmemory：将评估集存储在内存中，所有操作均保证深拷贝，适用于临时测试场景。

### 评估指标 -- Metric

Metric 表示一个评估指标，用于衡量 EvalSet 的某一方面表现，每个评估指标包含指标名、评估准则和评分阈值。

评估过程中，评估器会根据配置的评估准则对实际会话与预期会话进行比较，计算出该指标的评估得分，并与阈值进行对比：

- 当评估得分低于阈值时，指标判定为未通过。
- 当评估得分达到或超过阈值时，指标判定为通过。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// EvalMetric 表示用于评估 EvalCase 的单项指标
type EvalMetric struct {
	MetricName string               // 指标名称
	Threshold  float64              // 评分阈值
	Criterion  *criterion.Criterion // 评估准则
}

// Criterion 聚合各类评估准则
type Criterion struct {
	ToolTrajectory *tooltrajectory.ToolTrajectoryCriterion // 工具轨迹评估准则
	LLMJudge       *llm.LLMCriterion                       // LLM 评估准则
}
```

Metric Manager 负责管理评估指标。

每个 EvalSet 可以拥有多个评估指标，通过 `MetricName` 区分。

接口定义如下:

```go
type Manager interface {
	List(ctx context.Context, appName, evalSetID string) ([]string, error)               // 返回指定 EvalSet 下所有的 Metric Name
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error) // 获取指定 EvalSet 中的单个 Metric
	Add(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error        // 为指定 EvalSet 添加 Metric
	Delete(ctx context.Context, appName, evalSetID, metricName string) error             // 删除指定 Metric
	Update(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error     // 更新指定 Metric
}
```

框架为 Metric Manager 提供了两种实现:

- local: 将评估指标存储在本地文件系统中，文件命名格式为 `<EvalSetID>.metric.json`。
- inmemory: 将评估指标存储在内存中，所有操作均保证深拷贝，适用于临时测试或快速验证场景。

### 评估器 -- Evaluator

Evaluator 根据实际会话、预期会话 与评估指标计算最终评估结果。

评估器输出的结果包括：

- 总体评估得分
- 总体评估状态
- 逐会话评估结果列表

其中，单条会话评估结果包含：

- 实际会话
- 预期会话
- 评估得分
- 评估状态

评估状态通常由得分与指标阈值共同决定：

- 若评估得分 ≥ 评估指标阈值，则状态为通过
- 若评估得分 < 评估指标阈值，则状态为未通过

**注意**：评估器名称 `Evaluator.Name()` 需要与评估指标名 `metric.MetricName` 一致。

评估器 Evaluator 接口定义如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Evaluator 定义评估器的通用接口
type Evaluator interface {
	// Name 返回评估器名称
	Name() string
	// Description 返回评估器描述信息
	Description() string
	// Evaluate 执行评估逻辑，比较实际与预期会话并返回结果
	Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) (*EvaluateResult, error)
}

// EvaluateResult 表示评估器在多次会话上的汇总结果
type EvaluateResult struct {
	OverallScore         float64                // 总体得分
	OverallStatus        status.EvalStatus      // 总体状态，分为通过/未通过/未评估
	PerInvocationResults []*PerInvocationResult // 单次会话评估结果
}

// PerInvocationResult 表示单次会话的评估结果
type PerInvocationResult struct {
	ActualInvocation   *evalset.Invocation   // 实际会话
	ExpectedInvocation *evalset.Invocation   // 预期会话
	Score              float64               // 当前会话得分
	Status             status.EvalStatus     // 当前会话状态
	Details            *PerInvocationDetails // 额外信息，例如原因和评分
}

// PerInvocationDetails 表示单轮评估的额外信息
type PerInvocationDetails struct {
	Reason       string                    // 评分原因
	Score        float64                   // 评估得分
	RubricScores []*evalresult.RubricScore // 各项评估细则结果
}
```

### 评估器注册中心 -- Registry

Registry 用于统一管理和访问各类评估器。

方法包括：

- `Register(name string, e Evaluator)`：注册指定名称的评估器。
- `Get(name string)`：根据名称获取评估器实例。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"

// Registry 定义评估器注册中心接口
type Registry interface {
	// Register 将评估器注册到全局注册中心
	Register(name string, e evaluator.Evaluator) error
	// Get 根据评估器名称获取实例
	Get(name string) (evaluator.Evaluator, error)
}
```

框架默认注册了以下评估器：

- `tool_trajectory_avg_score` 工具轨迹一致性评估器，需要配置预期输出。
 - `final_response_avg_score` 最终响应评估器，不需要 LLM，需要配置预期输出。
- `llm_final_response` LLM 最终响应评估器，需要配置预期输出。
- `llm_rubric_response` LLM rubric 响应评估器，需要评估集提供会话输入并配置 LLMJudge/rubrics。
- `llm_rubric_knowledge_recall` LLM rubric 知识召回评估器，需要评估集提供会话输入并配置 LLMJudge/rubrics。

### 评估结果 -- EvalResult

EvalResult 模块用于记录并管理评估执行后的结果数据。

EvalSetResult 记录评估集 EvalSetID 的评估结果，包含多个 EvalCaseResult，用于展示每个评估用例的执行情况与得分明细。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"

// EvalSetResult 表示评估集的整体评估结果
type EvalSetResult struct {
	EvalSetResultID   string               // 评估结果唯一标识
	EvalSetResultName string               // 评估结果名称
	EvalSetID         string               // 对应的评估集 ID
	EvalCaseResults   []*EvalCaseResult    // 各评估用例的结果
	CreationTimestamp *epochtime.EpochTime // 结果创建时间
}
```

EvalCaseResult 表示单个评估用例的评估结果，包含总体评估状态、各项指标得分以及每轮对话的评估详情。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// EvalCaseResult 表示单个评估用例的评估结果
type EvalCaseResult struct {
	EvalSetID                     string                           // 所属评估集 ID
	EvalID                        string                           // 用例唯一标识
	FinalEvalStatus               status.EvalStatus                // 用例最终评估状态
	OverallEvalMetricResults      []*EvalMetricResult              // 各指标总体得分结果
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation // 按对话粒度的指标评估结果
	SessionID                     string                           // 推理阶段生成的 Session ID
	UserID                        string                           // 推理阶段使用的 User ID
}
```

EvalMetricResult 表示某一指标的评估结果，包括得分、状态、阈值及附加信息。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalMetricResult 表示单项指标的评估结果
type EvalMetricResult struct {
	MetricName string                   // 指标名称
	Score      float64                  // 实际得分
	EvalStatus status.EvalStatus        // 评测状态
	Threshold  float64                  // 阈值
	Criterion  *criterion.Criterion     // 评估准则
	Details    *EvalMetricResultDetails // 额外信息，如评分过程、错误描述等
}

// EvalMetricResultDetails 表示指标评估的附加信息
type EvalMetricResultDetails struct {
	Reason       string         // 评分原因
	Score        float64        // 评估得分
	RubricScores []*RubricScore // 各项评估细则结果
}

// RubricScore 表示单条评估细则结果
type RubricScore struct {
	ID     string  // 评估细则 ID
	Reason string  // 评分原因
	Score  float64 // 评估得分
}

// ScoreResult 表示单项指标的评分结果
type ScoreResult struct {
	Reason       string         // 评分原因
	Score        float64        // 评估得分
	RubricScores []*RubricScore // 各项评估细则结果
}
```

EvalMetricResultPerInvocation 表示单轮对话的逐指标评估结果，用于分析具体对话在不同指标下的表现差异。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"

// EvalMetricResultPerInvocation 表示单轮对话的逐指标评估结果
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation // 实际执行的对话
	ExpectedInvocation *evalset.Invocation // 预期的对话结果
	EvalMetricResults  []*EvalMetricResult // 各指标评估结果
}
```

EvalResult Manager 负责管理评估结果的存储、查询与列表操作，接口定义如下：

```go
// Manager 定义评估结果的管理接口
type Manager interface {
	// Save 保存评估结果，返回 EvalSetResultID
	Save(ctx context.Context, appName string, evalSetResult *EvalSetResult) (string, error)
	// Get 根据 evalSetResultID 获取指定评估结果
	Get(ctx context.Context, appName, evalSetResultID string) (*EvalSetResult, error)
	// List 返回指定应用下的所有评估结果 ID
	List(ctx context.Context, appName string) ([]string, error)
}
```

框架为 EvalResult Manager 提供两种实现方式：

- local：将评估结果存储在本地文件系统中，文件默认命名格式为 `<EvalSetResultID>.evalset_result.json`。其中，`EvalSetResultID` 默认命名规则为 `<appName>_<EvalSetID>_<UUID>`。
- inmemory：将评估结果存储在内存中，所有操作均保证深拷贝，适合调试与快速验证场景。

### 评估服务 -- Service

Service 是评估服务，用于整合以下模块：

- 评估集 EvalSet
- 评估指标 Metric
- 评估器注册中心 Registry
- 评估器 Evaluator
- 评估结果 EvalSetResult

Service 接口定义了完整的评测流程，包括推理（Inference）和评估（Evaluate）两个阶段，接口定义如下：

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"

// Service 定义了评估服务的核心接口。
type Service interface {
	// Inference 执行推理，调用 Agent 处理指定的评测用例，并返回推理结果。
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error)
	// Evaluate 对推理结果进行评估，生成并持久化评测结果。
	Evaluate(ctx context.Context, request *EvaluateRequest) (*evalresult.EvalSetResult, error)
}
```

框架为 Service 接口提供了默认本地评估服务 `local` 实现: 调用本地 Agent，在本地执行推理与评估。

#### 推理阶段 -- Inference

推理阶段负责运行 Agent，并捕获对评测用例的实际响应。
输入为 `InferenceRequest`，输出为 `InferenceResult` 列表。

```go
// InferenceRequest 表示一次推理请求
type InferenceRequest struct {
	AppName     string   // 应用名称
	EvalSetID   string   // 评估集 ID
	EvalCaseIDs []string // 需要推理的评估用例 ID 列表
}
```

说明：

- `AppName` 指定应用名称。
- `EvalSetID` 指定评估集。
- `EvalCaseIDs` 指定要评估的用例列表。若为空，则默认评估该评估集下的全部用例。

在推理阶段，系统会依次读取每个评估用例中的会话 `Invocation`，将其中的 `UserContent` 作为用户输入调用 Agent，并记录 Agent 的响应。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// InferenceResult 表示单个评测用例的推理结果
type InferenceResult struct {
	AppName      string                // 应用名称
	EvalSetID    string                // 所属评估集 ID
	EvalCaseID   string                // 评估用例 ID
	Inferences   []*evalset.Invocation // 实际推理得到的会话
	SessionID    string                // 推理阶段的 Session ID
	Status       status.EvalStatus     // 推理状态
	ErrorMessage string                // 推理失败时的错误信息
}
```

说明：

- 每个 `InferenceResult` 对应一个 `EvalCase`。
- 由于评估集可能包含多个评估用例，所以 `Inference` 将返回 `InferenceResult` 列表。

#### 评估阶段 -- Evaluate

评估阶段用于评估推理结果，输入为 `EvaluateRequest`，输出为评估结果 `EvalSetResult`。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"

// EvaluateRequest 表示一次评估请求
type EvaluateRequest struct {
	AppName          string             // 应用名称
	EvalSetID        string             // 评估集 ID
	InferenceResults []*InferenceResult // 推理阶段的结果
	EvaluateConfig   *EvaluateConfig    // 评估配置
}

// EvaluateConfig 表示评估阶段的配置
type EvaluateConfig struct {
	EvalMetrics []*metric.EvalMetric // 参与评估的指标集合
}
```

说明：

- 框架将根据配置的 `EvalMetrics` 调用对应的评估器 Evaluator 进行评估打分。
- 每个指标结果都会被汇总至最终的 `EvalSetResult` 中。

### Agent 评估 -- AgentEvaluator

`AgentEvaluator` 用于根据配置的评估集 EvalSetID 对 Agent 进行评估。

```go
// AgentEvaluator evaluates an agent based on an evaluation set.
type AgentEvaluator interface {
	// Evaluate evaluates the specified evaluation set.
	Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error)
	// Close closes the evaluator and releases owned resources.
	Close() error
}
```

`EvaluationResult` 表示一次完整评估任务的最终结果，包含整体评估状态、执行耗时以及所有评估用例的结果汇总。

```go
import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// EvaluationResult 包含多次执行评估的汇总结果
type EvaluationResult struct {
	AppName       string                  // 应用名称
	EvalSetID     string                  // 对应的评估集 ID
	OverallStatus status.EvalStatus       // 整体评估状态
	ExecutionTime time.Duration           // 执行耗时
	EvalCases     []*EvaluationCaseResult // 各评估用例的结果
}
```

`EvaluationCaseResult` 聚合单个评估用例多次运行的结果，包括整体评估状态、各次运行的详细结果以及指标级别的统计结果。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvaluationCaseResult 汇总了多次执行中单个评估用例的结果
type EvaluationCaseResult struct {
	EvalCaseID      string                         // 评估用例 ID
	OverallStatus   status.EvalStatus              // 综合评估状态
	EvalCaseResults []*evalresult.EvalCaseResult   // 各次运行的结果
	MetricResults   []*evalresult.EvalMetricResult // 指标级结果
}
```

可以通过 `evaluation.New` 创建 `AgentEvaluator` 实例。默认情况下，它会使用 `EvalSet Manager`、`Metric Manager` 与 `EvalResult Manager` 的 `local` 实现。

```go
agentEvaluator, err := evaluation.New(appName, runner, evaluation.WithNumRuns(numRuns))
if err != nil {
	panic(err)
}
defer agentEvaluator.Close()
```

由于 Agent 的运行过程可能存不确定性，`evaluation.WithNumRuns` 提供了多次评估运行的机制，用于降低单次运行带来的偶然性。

- 默认运行次数为 1 次；
- 通过指定 `evaluation.WithNumRuns(n)`，可对每个评估用例运行多次；
- 最终结果将基于多次运行的综合统计结果得出，默认统计方法是多次运行评估得分的平均值。

对于较大的评估集，为了加速 inference 阶段，可以开启 EvalCase 级别的并发推理：

- `evaluation.WithEvalCaseParallelInferenceEnabled(true)`：开启 eval case 并发推理，默认关闭。
- `evaluation.WithEvalCaseParallelism(n)`：设置最大并发数，默认值为 `runtime.GOMAXPROCS(0)`。

```go
agentEvaluator, err := evaluation.New(
	appName,
	runner,
	evaluation.WithEvalCaseParallelInferenceEnabled(true),
	evaluation.WithEvalCaseParallelism(runtime.GOMAXPROCS(0)),
)
defer agentEvaluator.Close()
```

## 使用指南

### 本地文件路径

本地文件有三种：

- 评估集文件
- 评估指标文件
- 评估结果文件

#### 评估集文件

评估集文件的默认路径为 `./<AppName>/<EvalSetID>.evalset.json`。

可以通过 `WithBaseDir` 设置自定义 `BaseDir`，即文件路径为 `<BaseDir>/<AppName>/<EvalSetID>.evalset.json`。

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

此外，若默认路径结构不满足需求，可通过实现 `Locator` 接口自定义文件路径规则，接口定义如下：

```go
// Locator 用于定义评估集文件的路径生成与枚举逻辑
type Locator interface {
	// Build 构建指定 appName 和 evalSetID 的评估集文件路径
	Build(baseDir, appName, evalSetID string) string
	// List 列出指定 appName 下的所有评估集 ID
	List(baseDir, appName string) ([]string, error)
}
```

例如将评估集文件格式设置为 `custom-<EvalSetID>.evalset.json`。

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

// Build 返回自定义文件路径格式：<BaseDir>/<AppName>/custom-<EvalSetID>.evalset.json
func (l *customLocator) Build(baseDir, appName, EvalSetID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetID+".evalset.json")
}

// List 列出指定 app 下的所有评估集 ID
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

#### 评估指标文件

评估指标文件的默认路径为 `./<AppName>/<EvalSetID>.metrics.json`。

可以通过 `WithBaseDir` 设置自定义 `BaseDir`，即文件路径为 `<BaseDir>/<AppName>/<EvalSetID>.metrics.json`。

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

此外，若默认路径结构不满足需求，可通过实现 `Locator` 接口自定义文件路径规则，接口定义如下：

```go
// Locator 用于定义评估指标文件的路径生成
type Locator interface {
	// Build 构建指定 appName 和 evalSetID 的评估指标文件路径
	Build(baseDir, appName, evalSetID string) string
}
```

例如将评估集文件格式设置为 `custom-<EvalSetID>.metrics.json`。

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

// Build 返回自定义文件路径格式：<BaseDir>/<AppName>/custom-<EvalSetID>.metrics.json
func (l *customLocator) Build(baseDir, appName, EvalSetID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetID+".metrics.json")
}
```

#### 评估结果文件

评估结果文件的默认路径为 `./<AppName>/<EvalSetResultID>.evalresult.json`。

可以通过 `WithBaseDir` 设置自定义 `BaseDir`，即文件路径为 `<BaseDir>/<AppName>/<EvalSetResultID>.evalresult.json`。其中，`EvalSetResultID` 默认命名规则为 `<appName>_<EvalSetID>_<UUID>`。

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

此外，若默认路径结构不满足需求，可通过实现 `Locator` 接口自定义文件路径规则，接口定义如下：

```go
// Locator 用于定义评估结果文件的路径生成与枚举逻辑
type Locator interface {
	// Build 构建指定 appName 和 evalSetResultID 的评估结果文件路径
	Build(baseDir, appName, evalSetResultID string) string
	// List 列出指定 appName 下的所有评估结果 ID
	List(baseDir, appName string) ([]string, error)
}
```

例如将评估结果文件格式设置为 `custom-<EvalSetResultID>.evalresult.json`。

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

// Build 返回自定义文件路径格式：<BaseDir>/<AppName>/custom-<EvalSetResultID>.evalresult.json
func (l *customLocator) Build(baseDir, appName, evalSetResultID string) string {
	return filepath.Join(baseDir, appName, "custom-"+evalSetResultID+".evalresult.json")
}

// List 列出指定 app 下的所有评估结果 ID
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

### Trace 评估模式

Trace 评估模式用于评估离线采集到的 Trace 执行轨迹，评估过程中不会调用 Runner 执行推理。

在 EvalSet 的 evalCase 中设置 `evalMode: "trace"`，并将 `conversation` 填写为实际输出的 invocation 序列，例如 `userContent`、`finalResponse`、`tools`、`intermediateResponses`。由于 trace 模式不提供预期输出，建议选择不依赖预期输出的 Metric，例如 `llm_rubric_response`。

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


完整示例参见 [examples/evaluation/trace](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/trace)。

### 评估准则

评估准则描述具体的评估方式，可按需组合使用。

框架内置了以下评估准则类型：

| 准则类型                | 适用对象                                |
|-------------------------|--------------------------------------|
| TextCriterion           | 文本字符串                             |
| JSONCriterion           | JSON 对象，通常用于比较 map[string]any  |
| ToolTrajectoryCriterion | 工具调用轨迹                           |
| LLMCriterion            | 基于 LLM 评估模型的评估                 |
| Criterion               | 多种准则的聚合                         |

#### TextCriterion

TextCriterion 用于字符串匹配，可配置是否忽略大小写和具体的匹配策略。

```go
// TextCriterion 定义字符串的匹配方式。
type TextCriterion struct {
	Ignore          bool              // 是否跳过匹配
	CaseInsensitive bool              // 是否大小写不敏感
	MatchStrategy   TextMatchStrategy // 匹配策略
	Compare         func(actual, expected string) (bool, error) // 自定义比较
}
```

TextMatchStrategy 取值说明：

| TextMatchStrategy 取值 | 说明                         |
|-----------------------|------------------------------|
| exact                 | 实际字符串与预期字符串完全一致（默认）。 |
| contains              | 实际字符串包含预期字符串。       |
| regex                 | 实际字符串满足预期字符串作为正则表达式。 |

#### JSONCriterion

JSONCriterion 用于对比结构化 JSON 数据，可配置是否忽略比较以及具体的匹配策略。

```go
// JSONCriterion 定义 JSON 对象的匹配方式。
type JSONCriterion struct {
	Ignore          bool                                                // 是否跳过匹配
	IgnoreTree      map[string]any                                      // 忽略的字段树，值为 true 时跳过该字段及其子树
	MatchStrategy   JSONMatchStrategy                                   // 匹配策略
	NumberTolerance *float64                                            // 数值容差，默认 1e-6，对叶子上的数字做近似比较
	Compare         func(actual, expected map[string]any) (bool, error) // 自定义比较
}
```

JSONMatchStrategy 取值说明：

| JSONMatchStrategy 取值 | 说明                         |
|-----------------------|------------------------------|
| exact                 | 实际 JSON 与预期 JSON 完全一致（默认）。 |

`IgnoreTree` 支持在比较时跳过特定字段以及其子树，只校验未被忽略的字段。

例如忽略 `metadata.updatedAt` 但校验其他字段：

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

配置文件示例如下：

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

ToolTrajectoryCriterion 用于配置工具调用与结果的评估准则，可设置默认策略、按工具名定制策略以及是否要求保持调用顺序。

```go
// ToolTrajectoryCriterion 定义工具调用与结果的评估准则。
type ToolTrajectoryCriterion struct {
	DefaultStrategy *ToolTrajectoryStrategy                                  // 默认策略
	ToolStrategy    map[string]*ToolTrajectoryStrategy                       // 按工具名定制策略
	OrderSensitive  bool                                                     // 是否要求按顺序严格匹配
	SubsetMatching  bool                                                     // 是否允许预期调用为实际调用的子集
	Compare         func(actual, expected *evalset.Invocation) (bool, error) // 自定义比较
}

// ToolTrajectoryStrategy 定义单个工具的匹配策略。
type ToolTrajectoryStrategy struct {
	Name      *TextCriterion // 工具名匹配
	Arguments *JSONCriterion // 调用参数匹配
	Result    *JSONCriterion // 工具结果匹配
}
```

DefaultStrategy 用于配置全局默认评估准则，适用于所有工具。

ToolStrategy 按工具名覆盖特定工具的评估准则，未设置 ToolStrategy 时所有工具调用都使用 DefaultStrategy。

若未设置任何评估准则，框架会使用默认评估准则：工具名按 TextCriterion 的 exact 策略比较，参数和结果按 JSONCriterion 的 exact 策略比较，保证工具轨迹评估始终有合理的兜底行为。

下面的示例展示了一个典型场景，大部分工具希望严格对齐工具调用和结果，但 current_time 这类时间相关工具的响应值本身不稳定，因此只需要检查是否按预期调用了正确的工具和参数，而不要求时间值本身完全一致。

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
						Ignore: true, // 忽略该工具结果的匹配
					},
				},
			}),
		),
	),
)
```

默认情况下，工具调用匹配对顺序不敏感，每个预期工具会与任意一个满足策略的实际工具尝试配对，同一个工具调用不会被重复复用，当所有预期工具都能找到匹配时视为通过。具体来说，此时会通过二分图最大匹配计算最大匹配数，将预期工具调用视为左节点，实际工具调用视为右节点，对于每对预期/实际工具调用，若两者满足工具匹配策略，则从预期工具节点向实际工具节点建一条边。建图完成之后，通过 Kuhn 算法求解二分图最大匹配，然后扫描未匹配的预期工具节点。若达成完美匹配，即所有预期工具节点都有匹配的实际工具节点，则认为工具匹配通过；否则，框架将返回未成功匹配的预期节点。

若希望严格按预期工具的出现顺序逐条比对，可开启 `WithOrderSensitive(true)`，此时评估器按预期/实际列表顺序扫描，若预期工具调用找不到对应的实际工具调用匹配，则判定为失败。

开启顺序严格匹配的代码示例如下：

```go
criterion := criterion.New(
	criterion.WithToolTrajectory(
		ctooltrajectory.New(
			ctooltrajectory.WithOrderSensitive(true), // 开启顺序敏感匹配.
		),
	),
)
```

开启顺序严格匹配的配置文件示例如下：

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

SubsetMatching 控制预期工具序列是否可以只是实际工具序列的子集，默认关闭。

- 关闭时，预期和实际的工具调用数量必须一致。
- 开启时，实际工具调用数量可以比预期更多，允许预期工具序列作为实际工具序列的子集。

开启子集匹配的代码示例如下：

```go
criterion := criterion.New(
	criterion.WithToolTrajectory(
		ctooltrajectory.New(
			ctooltrajectory.WithSubsetMatching(true),
		),
	),
)
```

开启子集匹配的配置文件如下：

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

假设 `A`、`B`、`C` 和 `D` 各自是一组工具调用，匹配情况示例如下。

| SubsetMatching | OrderSensitive | 预期序列 | 实际序列 | 结果 | 说明 |
| --- | --- | --- | --- | --- | --- |
| 关 | 关 | `[A]` | `[A, B]` | 不匹配 | 数量不等 |
| 开 | 关 | `[A]` | `[A, B]` | 匹配 | 预期是子集 |
| 开 | 关 | `[C, A]` | `[A, B, C]` | 匹配 | 预期是子集且无序匹配 |
| 开 | 开 | `[A, C]` | `[A, B, C]` | 匹配 | 预期是子集且顺序匹配 |
| 开 | 开 | `[C, A]` | `[A, B, C]` | 不匹配 | 顺序不满足 |
| 开 | 关 | `[C, D]` | `[A, B, C]` | 不匹配 | 实际工具序列缺少 D |
| 任意 | 任意 | `[A, A]` | `[A]` | 不匹配 | 实际调用不足，同一调用不能重复匹配 |


#### LLMCriterion

LLMCriterion 用于配置基于大模型的评估准则，适用于需要由模型给出评估结论的场景。

```go
// LLMCriterion 配置评估模型
type LLMCriterion struct {
	Rubrics    []*Rubric          // 评估细则配置
	JudgeModel *JudgeModelOptions // 评估模型配置
}

// Rubric 定义评估细则
type Rubric struct {
	ID          string         // 评估细则唯一标识
	Description string         // 评估细则描述，供人类阅读
	Type        string         // 评估细则类型
	Content     *RubricContent // 评估细则内容，供评估模型阅读
}

// RubricContent 定义评估细则内容
type RubricContent struct {
	Text string // 评估细则具体内容
}

// JudgeModelOptions 定义评估模型的详细参数
type JudgeModelOptions struct {
	ProviderName string                  // 模型供应商名称
	ModelName    string                  // 评估模型名称
	BaseURL      string                  // 模型 Base URL
	APIKey       string                  // 模型 API Key
	ExtraFields  map[string]any          // 模型请求的额外参数
	NumSamples   int                     // 评估采样次数
	Generation   *model.GenerationConfig // 评估模型的生成配置
}
```

- `Rubrics` 用于定义评估细则，仅在 rubric 类评估器中使用，无需配置预期输出，评估模型将根据评估细则逐项评估。
- `NumSamples` 控制评估模型调用次数，未配置时默认值为 1。
- `Generation` 默认使用 `MaxTokens=2000`、`Temperature=0.8`、`Stream=false`。

出于安全考虑，建议不要把 `judgeModel.apiKey` / `judgeModel.baseURL` 明文写入指标配置文件或者代码。

框架支持在 `.metrics.json` 中对 `judgeModel.providerName`、`judgeModel.modelName`、`judgeModel.apiKey` 和 `judgeModel.baseURL` 使用环境变量占位符，加载配置时会自动展开为对应的环境变量值。

例如：

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

可通过 `criterion.WithLLMJudge` 传入自定义配置，例如：

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

### 评估器

#### 工具轨迹评估器

工具轨迹评估器对应的指标名称为 `tool_trajectory_avg_score`，用于评估 Agent 在多次会话中对工具的使用是否符合预期。

在单次会话中，评估器会使用 `ToolTrajectoryCriterion` 对实际工具调用轨迹与预期轨迹进行比较：

- 若整条工具调用轨迹满足评估准则，则该会话在此指标上的得分为 1。  
- 若任意一步调用不满足评估准则，则该会话在此指标上的得分为 0。

在多次会话的场景下，评估器会对所有会话在该指标上的得分取平均值，作为最终的 `tool_trajectory_avg_score`，并与 `EvalMetric.Threshold` 比较，得到通过/未通过的判定结果。

工具轨迹评估器与 Metric、Criterion 的典型组合方式如下：

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
			// 使用默认评估准则，工具的名称、参数和执行结果需严格一致
			ctooltrajectory.New(),
		),
	),
}
```

对应的指标配置文件写法示例：

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

完整示例参见 [examples/evaluation/tooltrajectory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/tooltrajectory)。

#### 最终响应评估器

最终响应评估器对应的指标名称为 `final_response_avg_score`，不依赖 LLM，用于基于确定性规则对比 Agent 的最终回答与预期输出，适用于需要静态规则匹配文本或 JSON 输出的场景。

评估逻辑：

- 使用 `FinalResponseCriterion` 对每轮对话的 `Invocation.FinalResponse.Content` 进行对比；匹配得 1 分，不匹配得 0 分。
- 多次会话场景下对所有会话的得分取平均值，并与 `EvalMetric.Threshold` 比较得到通过/未通过判定。

`FinalResponseCriterion` 支持两类准则：

- `text`：使用 `TextCriterion` 按 `exact/contains/regex` 等策略比较文本，详细介绍可见 [TextCriterion](#textcriterion)。
- `json`：将 `FinalResponse.Content` 解析为 JSON 后使用 `JSONCriterion` 进行匹配。可配置 `ignoreTree`、`numberTolerance` 等参数，详细介绍可见 [JsonCriterion](#jsoncriterion)。

代码示例如下：

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

对应的指标配置文件写法示例

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

#### LLM 最终响应评估器

LLM 最终响应评估器对应的指标名称为 `llm_final_response`，通过评估模型判定 Agent 的最终回答是否有效。评估提示词会包含用户输入、参考答案与 Agent 的最终回答，适用于自动化校验最终文本输出。

评估逻辑：

- 使用 `LLMCriterion` 的 `JudgeModel` 调用评估模型，按配置的 `NumSamples` 采样多次。
- 评估模型需返回字段 `is_the_agent_response_valid`，取值为 `valid` 或 `invalid`（大小写不敏感）；`valid` 记 1 分，`invalid` 记 0 分，其他结果或解析失败会报错。
- 多次采样时按多数表决聚合，最终得分与 `EvalMetric.Threshold` 比较得到评估结论。

典型配置示例如下：

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

对应的指标配置文件写法示例：

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

完整示例参见 [examples/evaluation/llm/finalresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/finalresponse)。

#### LLM Rubric 响应评估器

LLM Rubric 响应评估器对应的指标名称为 `llm_rubric_response`，用于按评估细则判定 Agent 最终回答是否满足各项要求。

评估逻辑：

- 使用 `LLMCriterion` 的 `Rubrics` 构造提示，评估模型返回每个 rubric 的 `yes`/`no` 判定。
- 单次采样得分为所有 rubric 得分的平均值（`yes`=1，`no`=0）。
- 多次采样按多数表决选择代表结果，再与 `EvalMetric.Threshold` 比较得出通过/未通过。

典型配置示例：

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

对应的指标配置文件写法示例：

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

完整示例参见 [examples/evaluation/llm/rubricresponse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/rubricresponse)。

#### LLM Rubric 知识召回评估器

LLM Rubric 知识召回评估器对应的指标名称为 `llm_rubric_knowledge_recall`，用于判定检索到的知识是否支撑用户问题中的关键信息。

评估逻辑：

- 从 `IntermediateData.ToolResponses` 中提取 `knowledge_search`/`knowledge_search_with_agentic_filter` 工具的响应，作为检索结果。
- 结合 `Rubrics` 生成提示，评估模型对每个 rubric 返回 `yes`/`no`，单次采样得分为平均值。
- 多次采样使用多数表决确定代表结果，再与阈值比较得到最终结论。

典型配置示例：

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

对应的指标配置文件写法示例：

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

该评估器要求 Agent 的工具调用返回检索结果，完整示例参见 [examples/evaluation/llm/knowledgerecall](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/llm/knowledgerecall)。

### Callback 回调

Evaluation 支持在评估流程的关键节点注册回调，用于观测/埋点、上下文传递以及调整请求参数。

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

通过 `evaluation.WithEvalCaseParallelInferenceEnabled(true)` 开启并行推理后，case 级回调可能并发执行，由于 `args.Request` 指向同一份 `*InferenceRequest`，因此建议只读；如需改写请求，可以在 set 级回调中完成。

单个 EvalCase 的推理或评估失败通常不会通过 `error` 向上传递，而是写入 `Result.Status` 与 `Result.ErrorMessage`，因此 `After*CaseArgs.Error` 不用于承载单个用例失败原因，需要判断失败可以查看 `args.Result.Status` 与 `args.Result.ErrorMessage`。

## 最佳实践

### 上下文注入

`contextMessages` 用于为 EvalCase 提供一组额外上下文消息，常用于补充背景信息、角色设定或样本示例。它也适用于纯模型评估场景，将 system prompt 作为评估数据按用例配置，便于对比不同模型与提示词组合的能力。

上下文注入示例：

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

完整示例参见 [examples/evaluation/contextmessage](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/evaluation/contextmessage)。
