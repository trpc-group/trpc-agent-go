# Agent 使用文档

`Agent` 是 tRPC-Agent-Go 框架的核心执行单元，负责处理用户输入并生成相应的响应。每个 `Agent` 都实现了统一的接口，支持流式输出和回调机制。

框架提供了多种类型的 `Agent`，包括 `LLMAgent`、`ChainAgent`、`ParallelAgent`、`CycleAgent` 和 `GraphAgent`。本文重点介绍 `LLMAgent`，其他 `Agent` 类型以及多 `Agent` 系统的详细介绍请参考 [multiagent](./multiagent.md)。

## 快速开始

本示例使用 OpenAI 的 GPT-4o-mini 模型。在开始之前，请确保您已准备好相应的 `OPENAI_API_KEY` 并通过环境变量导出：

```shell
export OPENAI_API_KEY="your_api_key"
```

此外，框架还支持兼容 OpenAI API 的模型，可通过环境变量进行配置：

```shell
export OPENAI_BASE_URL="your_api_base_url"
export OPENAI_API_KEY="your_api_key"
```

### 创建模型实例

首先需要创建一个模型实例，这里使用 OpenAI 的 GPT-4o-mini 模型：

```go
import "trpc.group/trpc-go/trpc-agent-go/model/openai"

modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
flag.Parse()
// 创建 OpenAI 模型实例
modelInstance := openai.New(*modelName, openai.Options{})
```

### 配置生成参数

设置模型的生成参数，包括最大 token 数、温度以及是否使用流式输出等：

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

maxTokens := 1000
temperature := 0.7
genConfig := model.GenerationConfig{
    MaxTokens:   &maxTokens,   // 最大生成 token 数
    Temperature: &temperature, // 温度参数，控制输出的随机性
    Stream:      true,         // 启用流式输出
}
```

### 创建 LLMAgent

使用模型实例和配置创建 `LLMAgent`，同时设置 `Agent` 的 `Description` 与 `Instruction`。

`Description` 用于描述 `Agent` 的基本功能和特性，Instruction 则定义了 `Agent` 在执行任务时应遵循的具体指令和行为准则。

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"

llmAgent := llmagent.New(
    "demo-agent",                      // Agent 名称
    llmagent.WithModel(modelInstance), // 设置模型
    llmagent.WithDescription("A helpful AI assistant for demonstrations"),              // 设置描述
    llmagent.WithInstruction("Be helpful, concise, and informative in your responses"), // 设置指令
    llmagent.WithGenerationConfig(genConfig),                                           // 设置生成参数
)
```

### 创建调用上下文

创建 `Invocation` 对象，包含执行所需的所有信息：

```go
import "trpc.group/trpc-go/trpc-agent-go/agent"

invocation := &agent.Invocation{
    AgentName:     "demo-agent",                                                   // Agent 名称
    InvocationID:  "demo-invocation-001",                                          // 调用 ID
    EndInvocation: false,                                                          // 是否结束调用
    Model:         modelInstance,                                                  // 使用的模型
    Message:       model.NewUserMessage("Hello! Can you tell me about yourself?"), // 用户消息
    Session:       &session.Session{ID: "session-001"},
}
```

### 执行 Agent

调用 `Agent.Run` 方法开始执行：

```go
import "context"

ctx := context.Background()
eventChan, err := llmAgent.Run(ctx, invocation)
if err != nil {
    log.Fatalf("执行 Agent 失败: %v", err)
}
```

### 处理事件流

通过事件通道接收执行结果：

```go
// 处理 Event
for event := range eventChan {
    // 检查错误
    if event.Error != nil {
        log.Printf("err: %s", event.Error.Message)
        continue
    }
    // 处理内容
    if len(event.Choices) > 0 {
        choice := event.Choices[0]
        if choice.Delta.Content != "" {
            // 流式输出
            fmt.Print(choice.Delta.Content)
        }
    }
    // 检查是否完成
    if event.Done {
        break
    }
}
```

该示例的完整代码可见 [examples/llmagent](http://github.com/trpc-group/trpc-agent-go/tree/main/examples/llmagent)

## 核心概念

### Invocation

`Invocation` 是 `Agent` 执行流程的上下文对象，包含了单次调用所需的所有信息：

```go
// Invocation 是 Agent 执行流程的上下文对象，包含了单次调用所需的所有信息
type Invocation struct {
	// Agent 指定要调用的 Agent 实例
	Agent Agent
	// AgentName 标识要调用的 Agent 实例名称
	AgentName string
	// InvocationID 为每次调用提供唯一标识
	InvocationID string
	// Branch 用于分层事件过滤的分支标识符
	Branch string
	// EndInvocation 标识是否结束调用的标志
	EndInvocation bool
	// Session 维护对话的上下文状态
	Session *session.Session
	// Model 指定要使用的模型实例
	Model model.Model
	// Message 是用户发送给 Agent 的具体内容
	Message model.Message
	// EventCompletionCh 用于在事件写入会话时发出信号
	EventCompletionCh <-chan string
	// RunOptions 是 Run 方法的选项配置
	RunOptions RunOptions
	// TransferInfo 支持 Agent 之间的控制权转移
	TransferInfo *TransferInfo
	// AgentCallbacks 允许在 Agent 执行的不同阶段插入自定义逻辑
	AgentCallbacks *AgentCallbacks
	// ModelCallbacks 允许在模型调用的不同阶段插入自定义逻辑
	ModelCallbacks *model.ModelCallbacks
	// ToolCallbacks 允许在工具调用的不同阶段插入自定义逻辑
	ToolCallbacks *tool.ToolCallbacks
}
```

### Event

`Event` 是 `Agent` 执行过程中产生的实时反馈，通过 `Event` 流实时报告执行进展。

Event 主要有以下类型：

- 模型对话事件
- 工具调用与响应事件
- Agent 转移事件
- 错误事件

```go
// Event 是 Agent 执行过程中产生的实时反馈，通过 Event 流实时报告执行进展
type Event struct {
	// Response 包含模型的响应内容、工具调用结果和统计信息
	*model.Response
	// InvocationID 关联到具体的调用
	InvocationID string `json:"invocationId"`
	// Author 是事件的来源，例如 Agent 或工具
	Author string `json:"author"`
	// ID 是事件的唯一标识
	ID string `json:"id"`
	// Timestamp 记录事件发生的时间
	Timestamp time.Time `json:"timestamp"`
	// Branch 用于分层事件过滤的分支标识符
	Branch string `json:"branch,omitempty"`
	// RequiresCompletion 标识此事件是否需要完成信号
	RequiresCompletion bool `json:"requiresCompletion,omitempty"`
	// CompletionID 用于此事件的完成信号
	CompletionID string `json:"completionId,omitempty"`
	// LongRunningToolIDs 是长时间运行函数调用的 ID 集合，Agent 客户端可以通过此字段了解哪个函数调用是长时间运行的，仅对函数调用事件有效
	LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`
}
```

`Event` 的流式特性让你能够实时看到 `Agent` 的工作过程，就像和一个真人对话一样自然。你只需要遍历 `Event` 流，检查每个 `Event` 的内容和状态，就能完整地处理 `Agent` 的执行结果。

### Agent 接口

`Agent` 接口定义了所有 `Agent` 必须实现的核心行为。这个接口让你能够统一使用不同类型的 `Agent`，同时支持工具调用和子 `Agent` 管理。

```go
type Agent interface {
    // Run 接收执行上下文和调用信息，返回一个事件通道。通过这个通道，你可以实时接收 Agent 的执行进展和结果
    Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)
    // Tools 返回此 Agent 可以访问和执行的工具列表
    Tools() []tool.Tool
    // Info 方法提供 Agent 的基本信息，包括名称和描述，便于识别和管理
    Info() Info
    // SubAgents 返回此 Agent 可用的子 Agent 列表
    // SubAgents 和 FindSubAgent 方法支持 Agent 之间的协作。一个 Agent 可以将任务委托给其他 Agent，构建复杂的多 Agent 系统
    SubAgents() []Agent
    // FindSubAgent 通过名称查找子 Agent
    FindSubAgent(name string) Agent
}
```

框架提供了多种类型的 Agent 实现，包括 `LLMAgent`、`ChainAgent`、`ParallelAgent`、`CycleAgent` 和 `GraphAgent`，不同类型 `Agent` 以及多 `Agent` 系统的详细介绍请参考 [multiagent](./multiagent.md)。

## Callbacks

Callbacks 提供了丰富的回调机制，让你能够在 `Agent` 执行的关键节点注入自定义逻辑。

### 回调类型

框架提供了三种类型的回调：

**Agent Callbacks**：在 `Agent` 执行前后触发
```go
type AgentCallbacks struct {
    BeforeAgent []BeforeAgentCallback  // Agent 运行前的回调
    AfterAgent  []AfterAgentCallback   // Agent 运行后的回调
}
```

**Model Callbacks**：在模型调用前后触发
```go
type ModelCallbacks struct {
    BeforeModel []BeforeModelCallback  // 模型调用前的回调
    AfterModel  []AfterModelCallback   // 模型调用后的回调
}
```

**Tool Callbacks**：在工具调用前后触发

```go
type ToolCallbacks struct {
	BeforeTool []BeforeToolCallback  // 工具调用前的回调
	AfterTool []AfterToolCallback    // 工具调用后的回调
}
```

### 使用示例

```go
// 创建 Agent 回调
callbacks := &agent.AgentCallbacks{
    BeforeAgent: []agent.BeforeAgentCallback{
        func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
            log.Printf("Agent %s 开始执行", invocation.AgentName)
            return nil, nil
        },
    },
    AfterAgent: []agent.AfterAgentCallback{
        func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
            if runErr != nil {
                log.Printf("Agent %s 执行出错: %v", invocation.AgentName, runErr)
            } else {
                log.Printf("Agent %s 执行完成", invocation.AgentName)
            }
            return nil, nil
        },
    },
}

// 在 Invocation 中使用回调
invocation := &agent.Invocation{
    AgentName:     "demo-agent",
    InvocationID:  "demo-001",
    AgentCallbacks: callbacks,
    Model:         modelInstance,
    Message:       model.NewUserMessage("用户输入"),
    Session: &session.Session{
        ID: "session-001",
    },
}
```

回调机制让你能够精确控制 `Agent` 的执行过程，实现更复杂的业务逻辑。

## 进阶使用

框架还提供了 `Runner`、`Session` 和 `Memory` 等高级功能，用于构建更复杂的 `Agent 系统。

`Runner` 是 `Agent` 的执行器，负责管理 `Agent` 的执行流程，串联了 `Session/Memory Service` 等能力。

`Session Service` 用于管理会话状态，支持对话历史记录和上下文维护。

`Memory Service` 用于记录用户的偏好信息，支持个性化体验。

详细使用方法请参考 [runner](runner.md)、[session](session.md) 和 [memory](memory.md) 文档。
