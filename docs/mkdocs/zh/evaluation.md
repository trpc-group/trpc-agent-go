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
// 执行评估
result, err := agentEvaluator.Evaluate(context.Background(), evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
```

#### 评估集 EvalSet 文件示例

```json
{
  "eval_set_id": "math-basic",
  "name": "math-basic",
  "eval_cases": [
    {
      "eval_id": "calc_add",
      "conversation": [
        {
          "invocation_id": "calc_add-1",
          "user_content": {
            "parts": [
              {
                "text": "calc add 2 3"
              }
            ],
            "role": "user"
          },
          "final_response": {
            "parts": [
              {
                "text": "calc result: 5"
              }
            ],
            "role": "assistant"
          },
          "intermediate_data": {
            "tool_uses": [
              {
                "args": {
                  "a": 2,
                  "b": 3,
                  "operation": "add"
                },
                "name": "calculator"
              }
            ]
          },
          "creation_timestamp": 1761134484.981062
        }
      ],
      "session_input": {
        "app_name": "math-eval-app",
        "user_id": "user"
      },
      "creation_timestamp": 1761134484.981062
    },
  ],
  "creation_timestamp": 1761134484.9804401
}
```

#### 评估指标 Metric 文件示例

```json
[
  {
    "metric_name": "tool_trajectory_avg_score",
    "threshold": 1
  }
]
```

#### 评估结果 EvalResult 文件示例

```json
"{\"eval_set_result_id\":\"math-eval-app_math-basic_76798060-dcc3-41e9-b20e-06f23aa3cdbc\",\"eval_set_result_name\":\"math-eval-app_math-basic_76798060-dcc3-41e9-b20e-06f23aa3cdbc\",\"eval_set_id\":\"math-basic\",\"eval_case_results\":[{\"eval_set_id\":\"math-basic\",\"eval_id\":\"calc_add\",\"final_eval_status\":1,\"overall_eval_metric_results\":[{\"metric_name\":\"tool_trajectory_avg_score\",\"score\":1,\"eval_status\":1,\"threshold\":1}],\"eval_metric_result_per_invocation\":[{\"actual_invocation\":{\"invocation_id\":\"8b205b3f-682e-409a-b751-89ef805d0221\",\"user_content\":{\"parts\":[{\"text\":\"calc add 2 3\"}],\"role\":\"user\"},\"final_response\":{\"parts\":[{\"text\":\"The result of adding 2 and 3 is **5**.\"}],\"role\":\"assistant\"},\"intermediate_data\":{\"tool_uses\":[{\"id\":\"call_00_j75SIh8A9xSlG61OrC1ARIab\",\"args\":{\"a\":2,\"b\":3,\"operation\":\"add\"},\"name\":\"calculator\"}]}},\"expected_invocation\":{\"invocation_id\":\"calc_add-1\",\"user_content\":{\"parts\":[{\"text\":\"calc add 2 3\"}],\"role\":\"user\"},\"final_response\":{\"parts\":[{\"text\":\"calc result: 5\"}],\"role\":\"assistant\"},\"intermediate_data\":{\"tool_uses\":[{\"args\":{\"a\":2,\"b\":3,\"operation\":\"add\"},\"name\":\"calculator\"}]},\"creation_timestamp\":1761134484.981062},\"eval_metric_results\":[{\"metric_name\":\"tool_trajectory_avg_score\",\"score\":1,\"eval_status\":1,\"threshold\":1}]}],\"session_id\":\"74252944-b1a7-4c17-8f39-4a5809395d1d\",\"user_id\":\"user\"},{\"eval_set_id\":\"math-basic\",\"eval_id\":\"calc_multiply\",\"final_eval_status\":1,\"overall_eval_metric_results\":[{\"metric_name\":\"tool_trajectory_avg_score\",\"score\":1,\"eval_status\":1,\"threshold\":1}],\"eval_metric_result_per_invocation\":[{\"actual_invocation\":{\"invocation_id\":\"65226930-d45c-43ae-ab88-9c35f3abce70\",\"user_content\":{\"parts\":[{\"text\":\"calc multiply 6 7\"}],\"role\":\"user\"},\"final_response\":{\"parts\":[{\"text\":\"6 × 7 = 42\"}],\"role\":\"assistant\"},\"intermediate_data\":{\"tool_uses\":[{\"id\":\"call_00_b3Gj4Y3fJu9Blkbl6H0MLquO\",\"args\":{\"a\":6,\"b\":7,\"operation\":\"multiply\"},\"name\":\"calculator\"}]}},\"expected_invocation\":{\"invocation_id\":\"calc_multiply-1\",\"user_content\":{\"parts\":[{\"text\":\"calc multiply 6 7\"}],\"role\":\"user\"},\"final_response\":{\"parts\":[{\"text\":\"calc result: 42\"}],\"role\":\"assistant\"},\"intermediate_data\":{\"tool_uses\":[{\"args\":{\"a\":6,\"b\":7,\"operation\":\"multiply\"},\"name\":\"calculator\"}]},\"creation_timestamp\":1761134484.9812014},\"eval_metric_results\":[{\"metric_name\":\"tool_trajectory_avg_score\",\"score\":1,\"eval_status\":1,\"threshold\":1}]}],\"session_id\":\"6393fabd-ab50-49b7-8656-59fcb0a29758\",\"user_id\":\"user\"}],\"creation_timestamp\":1761134849.3572516}"
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
// 执行评估
result, err := agentEvaluator.Evaluate(ctx, evalSetID)
if err != nil {
	log.Fatalf("evaluate: %v", err)
}
```

#### 评估集 EvalSet 构建

```go
import (
	"google.golang.org/genai"
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
				UserContent: &genai.Content{
					Role: "user",
					Parts: []*genai.Part{
						{
							Text: "calc add 2 3",
						},
					},
				},
				FinalResponse: &genai.Content{
					Role: "assistant",
					Parts: []*genai.Part{
						{
							Text: "calc result: 5",
						},
					},
				},
				IntermediateData: &evalset.IntermediateData{
					ToolUses: []*genai.FunctionCall{
						{
							Name: "calculator",
							Args: map[string]interface{}{
								"operation": "add",
								"a":         2.0,
								"b":         3.0,
							},
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
import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"

evalMetric := &metric.EvalMetric{
	MetricName: "tool_trajectory_avg_score",
	Threshold:  1.0,
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
  - 推理阶段 Inference：从评估集提取用户输入，调用 Agent 执行推理，将 Agent 的实际输出与预期输出组合形成推理结果。
  - 结果评估阶段 Evaluate：根据评估指标名称 Metric Name 从注册中心获取相应的评估器，并使用多个评估器对推理结果进行多维度评估，最终生成评估结果  EvalResult。
- Agent Evaluator 为降低 Agent 输出的偶然性，评估服务会被调用 NumRuns 次，并聚合多次结果，以获得更稳定的评估结果。

### 评估集 -- EvalSet

EvalSet 是一组 EvalCase 的集合，通过唯一的 EvalSetID 进行标识，作为评估流程中的会话数据。

而 EvalCase 表示同一 Session 下的一组评估用例，包含唯一标识符 EvalID、对话内容以及 Session 初始化信息。

对话数据包括三类内容：

- 用户输入
- Agent 最终响应
- Agent 中间响应，包括:
  - 工具调用
  - 工具响应
  - 中间响应信息

```go
import (
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"
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
	Conversation      []*Invocation        // 对话序列
	SessionInput      *SessionInput        // Session 初始化数据
	CreationTimestamp *epochtime.EpochTime // 创建时间
}

// Invocation 表示一次用户与 Agent 的交互
type Invocation struct {
	InvocationID      string
	UserContent       *genai.Content       // 用户输入
	FinalResponse     *genai.Content       // Agent 最终响应
	IntermediateData  *IntermediateData    // Agent 中间响应数据
	CreationTimestamp *epochtime.EpochTime // 创建时间
}

// IntermediateData 表示执行过程中的中间数据
type IntermediateData struct {
	ToolUses              []*genai.FunctionCall     // 工具调用
	ToolResponses         []*genai.FunctionResponse // 工具响应
	IntermediateResponses [][]any                   // 中间响应，包含来源与内容
}

// SessionInput 表示 Session 初始化输入
type SessionInput struct {
	AppName string                 // 应用名
	UserID  string                 // 用户 ID
	State   map[string]interface{} // 初始状态
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

Metric 表示一个评估指标，用于衡量 EvalSet 的某一方面表现。

每个指标包含指标名和评分阈值:

- 当评估得分低于阈值时，指标判定为未通过。
- 当评估得分达到或超过阈值时，指标判定为通过。

```go
// EvalMetric 表示用于评估 EvalCase 的单项指标
type EvalMetric struct {
	MetricName string         // 指标名称
	Threshold  float64        // 评分阈值
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
	OverallScore         float64               // 总体得分
	OverallStatus        status.EvalStatus     // 总体状态，分为通过/未通过/未评估
	PerInvocationResults []PerInvocationResult // 单次会话评估结果
}

// PerInvocationResult 表示单次会话的评估结果
type PerInvocationResult struct {
	ActualInvocation   *evalset.Invocation // 实际会话
	ExpectedInvocation *evalset.Invocation // 预期会话
	Score              float64             // 当前会话得分
	Status             status.EvalStatus   // 当前会话状态
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

- `tool_trajectory_avg_score` 工具轨迹一致性评估器。
  - 对于单次会话：
    - 若实际工具调用序列与预期完全一致，则计 1 分；
    - 若不一致，则计 0 分。
  - 对于多次会话：计算各会话得分的平均值作为最终得分。

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
import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// EvalMetricResult 表示单项指标的评估结果
type EvalMetricResult struct {
	MetricName string            // 指标名称
	Score      float64           // 实际得分
	EvalStatus status.EvalStatus // 评测状态
	Threshold  float64           // 阈值
	Details    map[string]any    // 额外信息，如评分过程、错误描述等
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
// AgentEvaluator 根据评估集评估 Agent
type AgentEvaluator interface {
	// Evaluate 对指定的评估集执行评估
	Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error)
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
```

由于 Agent 的运行过程可能存不确定性，`evaluation.WithNumRuns` 提供了多次评估运行的机制，用于降低单次运行带来的偶然性。

- 默认运行次数为 1 次；
- 通过指定 `evaluation.WithNumRuns(n)`，可对每个评估用例运行多次；
- 最终结果将基于多次运行的综合统计结果得出，默认统计方法是多次运行评估得分的平均值。

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
