# tRPC-Agent-Go Evaluation 设计文档

tRPC-Agent-Go 评估功能用于测试和评估 Agent 性能，支持多种评估器，适用于 Agent 单元测试/集成测试场景。

## 目标

- **标准化评估流程**：提供标准的评估接口、评估集和评估结果定义
- **多样化评估指标**：支持工具轨迹、响应质量、性能等多维度评估
- **可扩展性**：支持自定义评估器、评估集管理器、评估结果管理器、评估服务

## 业界实现

### ADK

ADK 评估主要关注以下指标：

- **最终响应的质量**：支持 ROUGE 文本相似度和 LLM Judge 两种评估方式
- **工具调用轨迹**：衡量工具轨迹匹配度，支持多种匹配策略
- **安全性**：基于 VertexAI 的安全性评估

ADK 支持三种评估方式来适应不同的开发场景：

1. 通过 Web UI 进行交互式评估和调试
2. 通过 pytest 集成到现有测试流程
3. 通过 CLI 命令实现自动化评估。

数据格式方面，ADK 使用 `.test.json` 文件存储单个测试用例，`.evalset.json` 文件管理批量评估集，并通过 `test_config.json` 配置评估标准，测试结果存储在 `.evalset_result.json`。

ADK-Web 支持将对话导出为测试用例，支持测试用例编辑、评估运行以及评估结果查看的可视化，极大降低了评估的复杂性。

**示例：**

```python
from google.adk.evaluation.agent_evaluator import AgentEvaluator
import pytest

@pytest.mark.asyncio
async def test_with_single_test_file():
    """Test the agent's basic ability via a session file."""
    await AgentEvaluator.evaluate(
        agent_module="home_automation_agent",
        eval_dataset_file_path_or_dir="tests/integration/fixture/home_automation_agent/simple_test.test.json",
    )
```

### Agno

Agno 评估主要关注以下指标：

- **准确性（Accuracy）**：使用 LLM-as-a-judge 模式评估响应的完整性、正确性和准确性
- **性能（Performance）**：测量 Agent 的响应延迟和内存占用
- **可靠性（Reliability）**：验证 Agent 是否执行了预期的工具调用

**示例：**

```python
from typing import Optional

from agno.agent import Agent
from agno.eval.reliability import ReliabilityEval, ReliabilityResult
from agno.tools.calculator import CalculatorTools
from agno.models.openai import OpenAIChat
from agno.run.response import RunResponse

def multiply_and_exponentiate():
    agent = Agent(
        model=OpenAIChat(id="gpt-4o-mini"),
        tools=[CalculatorTools(add=True, multiply=True, exponentiate=True)],
    )
    response: RunResponse = agent.run("What is 10*5 then to the power of 2? do it step by step")
    evaluation = ReliabilityEval(
        agent_response=response,
        expected_tool_calls=["multiply", "exponentiate"],
    )
    result: Optional[ReliabilityResult] = evaluation.run(print_results=True)
    result.assert_passed()


if __name__ == "__main__":
    multiply_and_exponentiate()
```

## 设计方案

### 组织结构

```
evaluation/
├── evalset/              # 评估数据集管理
│   ├── evalcase.go       # 评估用例定义
│   ├── evalset.go        # 评估集定义、评估集管理器接口定义
│   ├── local/            # 评估集管理器的本地文件实现
│   └── inmemory/         # 评估集管理器的内存实现
├── evalresult/           # 评估结果
│   ├── evalresult.go     # 评估结果定义、评估结果管理器接口定义
│   ├── local/            # 评估结果管理器的本地文件实现
│   └── inmemory/         # 评估结果管理器的内存实现
├── evaluator/            # 评估器
│   ├── evaluator.go      # 评估器接口定义
│   ├── registry.go       # 评估器注册
│   ├── response/         # 响应质量评估器
│   └── tooltrajectory/   # 工具轨迹评估器
├── metric/               # 评估指标
│   ├── metric.go         # 指标类型和配置
├── service/              # 评估服务
│   ├── service.go        # 评估服务接口定义
│   └── local/            # 本地评估服务实现
├── evaluation.go         # Agent 评估器 - 用户入口
```

### 架构设计

评估功能采用分层架构，从下到上分为：

```
┌─────────────────────────────────────┐
│           AgentEvaluator            │  ← 用户入口层
├─────────────────────────────────────┤
│         EvaluationService           │  ← 评估服务层
├─────────────────────────────────────┤
│    Evaluator Registry & Metrics     │  ← 评估器层
├─────────────────────────────────────┤
│   EvalSet Manager & Result Manager  │  ← 数据管理层
└─────────────────────────────────────┘
```

**数据流向：**
1. **输入**：Agent + EvalSet → **推理阶段** → InferenceResult
2. **评估**：InferenceResult + Metrics → **评估阶段** → EvalCaseResult  
3. **输出**：EvalCaseResult → **汇总阶段** → EvaluationResult

#### evalset - 评估数据集管理

负责管理评估用例和评估集的存储、读取。

注：为使用 ADK Web 可视化编辑测试集的能力，gotag 需要与 ADK 字段对齐。

**核心类型:**

```go
// EvalCase 表示单个评估用例
type EvalCase struct {
    EvalID            string        `json:"eval_id"`
    Conversation      []Invocation  `json:"conversation"`
    SessionInput      *SessionInput `json:"session_input,omitempty"`
    CreationTimestamp time.Time     `json:"creation_timestamp"`
}

// Invocation 表示对话中的单次交互
type Invocation struct {
    InvocationID      string            `json:"invocation_id"`
    UserContent       string            `json:"user_content"`
    FinalResponse     string            `json:"final_response,omitempty"`
    IntermediateData  *IntermediateData `json:"intermediate_data,omitempty"`
    CreationTimestamp time.Time         `json:"creation_timestamp"`
}

// EvalSet 表示评估集
type EvalSet struct {
    EvalSetID         string      `json:"eval_set_id"`
    Name              string      `json:"name"`
    Description       string      `json:"description,omitempty"`
    EvalCases         []EvalCase  `json:"eval_cases"`
    CreationTimestamp time.Time   `json:"creation_timestamp"`
}
```

**管理器接口:**

```go
type Manager interface {
    Save(ctx context.Context, evalSet *EvalSet) error
    Get(ctx context.Context, evalSetID string) (*EvalSet, error)
    List(ctx context.Context) ([]*EvalSet, error)
    Delete(ctx context.Context, evalSetID string) error
}
```

**实现方式:**

- `inmemory` - 内存存储，适用于测试和小规模数据
- `local` - 本地文件存储，便于维护与分发

#### evalresult - 评估结果管理

负责管理评估结果的存储、读取和查询。

**核心类型:**

```go
// EvalCaseResult 表示单个评估用例的结果
type EvalCaseResult struct {
    EvalSetID                     string                              `json:"eval_set_id"`
    EvalCaseID                    string                              `json:"eval_id"`
    FinalEvalStatus               evaluation.EvalStatus               `json:"final_eval_status"`
    OverallEvalMetricResults      []metric.EvalMetricResult           `json:"overall_eval_metric_results"`
    EvalMetricResultPerInvocation []metric.EvalMetricResultPerInvocation `json:"eval_metric_result_per_invocation"`
    SessionID                     string                              `json:"session_id"`
    UserID                        string                              `json:"user_id,omitempty"`
}

// EvalSetResult 表示整个评估集的结果
type EvalSetResult struct {
    EvalSetResultID   string           `json:"eval_set_result_id"`
    EvalSetResultName string           `json:"eval_set_result_name,omitempty"`
    EvalSetID         string           `json:"eval_set_id"`
    EvalCaseResults   []EvalCaseResult `json:"eval_case_results"`
    CreationTimestamp float64          `json:"creation_timestamp"`
}
```

**管理器接口:**

```go
type Manager interface {
    Save(ctx context.Context, result *EvalSetResult) error
    Get(ctx context.Context, evalSetResultID string) (*EvalSetResult, error)
    List(ctx context.Context) ([]*EvalSetResult, error)
}
```

**实现方式:**

- `inmemory.Manager` - 内存存储，适用于测试和快速查询
- `local.Manager` - 本地文件存储，适用于结果持久化和历史记录

#### evaluator - 评估器

负责具体的评估逻辑实现，比较实际结果与期望结果。

**判定机制说明：**评估器通常会为每个指标计算一个分数（score），再与预先配置的阈值（threshold）比较；当指标达到阈值时，判定为通过（Passed），否则为不通过（Failed）。

**评估器接口:**

```go
type Evaluator interface {
    Evaluate(ctx context.Context, actual, expected []evalset.Invocation) (*EvaluationResult, error)
    Name() string
    Description() string
}

// EvaluationResult 评估器返回的结果
type EvaluationResult struct {
    OverallScore         float64                 `json:"overall_score"`
    OverallStatus        evaluation.EvalStatus   `json:"overall_status"`
    PerInvocationResults []PerInvocationResult   `json:"per_invocation_results"`
}
```

**常用的评估器实现:**

- `tooltrajectory` - 工具轨迹评估器，评估 Agent 工具调用的准确性
- `LLMJudgeEvaluator` - LLM评 判器，使用大语言模型评估响应质量
- `RougeEvaluator` - ROUGE 评估器，计算文本相似度分数

**注册器:**

```go
type Registry struct {
    evaluators map[string]Evaluator
}

func (r *Registry) Register(name string, evaluator Evaluator) error
func (r *Registry) Get(name string) (Evaluator, error)
func (r *Registry) List() []string
```

#### metric - 评估指标

定义各种评估指标的配置和结果类型。

**指标配置:**
```go
type EvalMetric struct {
    MetricName        string             `json:"metric_name"`
    Threshold         float64            `json:"threshold"`
    JudgeModelOptions *JudgeModelOptions `json:"judge_model_options,omitempty"`
}

// 指标结果
type EvalMetricResult struct {
    MetricName string                 `json:"metric_name"`
    Threshold  float64                `json:"threshold"`
    Score      *float64               `json:"score,omitempty"`
    Status     evaluation.EvalStatus  `json:"status"`
}
```

### service - 评估服务

提供高级的评估服务接口，协调各个组件完成完整的评估流程。

**服务接口:**

```go
type EvaluationService interface {
    // 执行推理，返回流式结果
    PerformInference(ctx context.Context, request *InferenceRequest) (<-chan *InferenceResult, error)
    
    // 执行评估，返回流式结果
    Evaluate(ctx context.Context, request *EvaluateRequest) (<-chan *evalresult.EvalCaseResult, error)
}
```

**请求类型:**

```go
type InferenceRequest struct {
    AppName         string          `json:"app_name"`
    EvalSetID       string          `json:"eval_set_id"`
    EvalCaseIDs     []string        `json:"eval_case_ids,omitempty"`
    InferenceConfig InferenceConfig `json:"inference_config"`
}

type EvaluateRequest struct {
    InferenceResults []InferenceResult `json:"inference_results"`
    EvaluateConfig   EvaluateConfig    `json:"evaluate_config"`
}
```

#### AgentEvaluator - 用户入口

**AgentEvaluator** 是用户入口

**核心接口:**

```go
type AgentEvaluator struct {
}

func (a *AgentEvaluator) Evaluate(ctx context.Context, runner runner.Runner) (*EvaluationResult, error)
```

**评估结果:**

```go
type EvaluationResult struct {
    OverallStatus EvalStatus                `json:"overall_status"`
    MetricResults map[string]MetricSummary  `json:"metric_results"`
    TotalCases    int                       `json:"total_cases"`
    ExecutionTime time.Duration             `json:"execution_time"`
}
```

### 使用示例

```go
package main

import (
    "context"
    "fmt"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/evaluation"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent"
)

func main() {
    ctx := context.Background()
    
    // 1. 创建 Agent 实例
    myAgent := &MyAgent{} // 实现 agent.Agent 接口
    
    // 2. 创建 Runner
    appRunner := runner.NewRunner("my-app", myAgent)
    
    // 3. 创建 AgentEvaluator
    evaluator := evaluation.NewAgentEvaluator()
    
    // 4. 运行评估
    result, err := evaluator.Evaluate(ctx, appRunner)
    if err != nil {
        log.Fatal("评估失败:", err)
    }
    
    // 5. 检查结果
    if result.OverallStatus == evaluation.EvalStatusPassed {
        fmt.Printf("🎉 评估通过! 处理了 %d 个用例，耗时 %v\n", 
            result.TotalCases, result.ExecutionTime)
    } else {
        fmt.Printf("❌ 评估失败! 请查看详细结果\n")
        
        // 也可以在测试中使用断言
        // assert.Equal(t, evaluation.EvalStatusPassed, result.OverallStatus, "Agent evaluation failed")
    }
}

// MyAgent 是 Agent 实现示例
type MyAgent struct {
    // 你的 Agent 字段
}
```
