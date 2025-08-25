# Planner 使用文档

`Planner` 是用于实现 `Agent` 规划能力的组件。它允许 `Agent` 在执行任务前制定计划，从而提高执行效率和准确性。

框架提供了两种 `Planner` 实现，分别适用于不同类型的模型：

- `BuiltinPlanner`：适用于支持原生思考功能的模型
- `ReActPlanner`：适用于不支持原生思考的模型，通过标签化指令引导模型按固定格式输出，提供结构化的思考过程

## Planner 接口

`Planner` 接口定义了所有规划器必须实现的方法：

```go
type Planner interface {
    // BuildPlanningInstruction 应用必要的配置到 LLM 请求，并构建要附加的系统指令用于规划
    // 如果不需要指令则返回空字符串
    BuildPlanningInstruction(
        ctx context.Context,
        invocation *agent.Invocation,
        llmRequest *model.Request,
    ) string

    // ProcessPlanningResponse 处理 LLM 的规划响应，返回处理后的响应
    // 如果不需要处理则返回 nil
    ProcessPlanningResponse(
        ctx context.Context,
        invocation *agent.Invocation,
        response *model.Response,
    ) *model.Response
}
```

`Planner` 的工作流程：

1. 请求处理阶段：`Planner` 在 LLM 请求发送前通过 `BuildPlanningInstruction` 添加规划指令或配置
2. 响应处理阶段：`Planner` 处理 LLM 响应，通过 `ProcessPlanningResponse` 组织内容结构

## BuiltinPlanner

`BuiltinPlanner` 适用于支持原生思考功能的模型。它不生成显式的规划指令，而是通过配置模型使用其内部的思考机制来实现规划功能。

模型配置如下：

```go
type Options struct {
    // ReasoningEffort 限制推理模型的推理程度
    // 支持的值："low", "medium", "high"
    // 仅对 OpenAI o-series 模型有效
    ReasoningEffort *string
    // ThinkingEnabled 为支持思考的模型启用思考模式
    // 仅对通过 OpenAI API 的 Claude 和 Gemini 模型有效
    ThinkingEnabled *bool
    // ThinkingTokens 控制思考的长度
    // 仅对通过 OpenAI API 的 Claude 和 Gemini 模型有效
    ThinkingTokens *int
}
```

在实现上，BuiltinPlanner 通过以下方式工作：

- `BuildPlanningInstruction`：将思考参数配置到 LLM 请求中；由于模型支持原生思考，不需要规划标签，因此直接返回空字符串
- `ProcessPlanningResponse`：由于模型在响应中已经包含了规划过程，因此直接返回 nil

示例如下：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/planner/builtin"
)

// 创建模型实例
modelInstance := openai.New("gpt-4o-mini")

// 创建 BuiltinPlanner
reasoningEffort := "high"
planner := builtin.New(builtin.Options{
    ReasoningEffort: &reasoningEffort,
})

// 创建 LLMAgent 并配置 Planner
llmAgent := llmagent.New(
    "demo-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A helpful AI assistant with built-in planning"),
    llmagent.WithInstruction("Be helpful and think through problems carefully"),
    llmagent.WithPlanner(planner), // 配置 Planner
)
```

## ReActPlanner

`ReActPlanner` 适用于不支持原生思考的模型。它通过引导 LLM 遵循特定的格式，使用特定标签来组织规划、推理、行动和最终答案，从而实现结构化的思考过程。

ReActPlanner 使用以下特定标签来组织响应内容：

1. 规划阶段（`/*PLANNING*/`）：创建明确的计划来回答用户问题
2. 推理阶段（`/*REASONING*/`）：在工具执行之间提供推理
3. 行动阶段（`/*ACTION*/`）：根据计划执行工具
4. 重新规划（`/*REPLANNING*/`）：根据结果需要时修订计划
5. 最终答案（`/*FINAL_ANSWER*/`）：提供综合答案

在实现上，`ReActPlanner` 通过以下方式工作：

- `BuildPlanningInstruction`：返回包含高层次指导、规划要求、推理要求等的综合指令，引导模型按标签格式输出
- `ProcessPlanningResponse`：过滤空名称的工具调用，如果内容包含 `/*FINAL_ANSWER*/` 标签则只保留最终答案部分，否则返回原内容，将规划内容与最终答案分离

使用示例：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/planner/react"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 创建模型实例
modelInstance := openai.New("gpt-4o-mini")

// 创建工具
searchTool := function.NewFunctionTool(
    searchFunction,
    function.WithName("search"),
    function.WithDescription("Search for information on a given topic"),
)

// 创建 ReActPlanner
planner := react.New()

// 创建 LLMAgent 并配置 Planner
llmAgent := llmagent.New(
    "react-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An AI assistant that uses structured planning"),
    llmagent.WithInstruction("Follow a structured approach to solve problems"),
    llmagent.WithPlanner(planner), // 配置 Planner
    llmagent.WithTools([]tool.Tool{searchTool}), // 配置工具
)
```

完整代码示例可参考 [examples/react](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/react)

## 自定义 Planner

除了框架提供的两种 `Planner` 实现，你还可以通过实现 `Planner` 接口来创建自定义的 `Planner`，以满足特定需求：

```go
type customPlanner struct {
    // 自定义配置
}

func (p *customPlanner) BuildPlanningInstruction(
    ctx context.Context,
    invocation *agent.Invocation,
    llmRequest *model.Request,
) string {
    // 返回自定义的规划指令
    return "你的自定义规划指令"
}

func (p *customPlanner) ProcessPlanningResponse(
    ctx context.Context,
    invocation *agent.Invocation,
    response *model.Response,
) *model.Response {
    // 处理响应
    return response
}

// 创建 LLMAgent 并配置自定义 Planner
llmAgent := llmagent.New(
    "react-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An AI assistant that uses structured planning"),
    llmagent.WithInstruction("Follow a structured approach to solve problems"),
    llmagent.WithPlanner(&customPlanner{}),      // 配置 Planner
    llmagent.WithTools([]tool.Tool{searchTool}), // 配置工具
)
```
