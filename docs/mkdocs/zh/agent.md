# Agent 使用文档

Agent 是 tRPC-Agent-Go 框架的核心执行单元，负责处理用户输入并生成相应的响应。每个 Agent 都实现了统一的接口，支持流式输出和回调机制。

框架提供了多种类型的 Agent，包括 LLMAgent、ChainAgent、ParallelAgent、CycleAgent 和 GraphAgent。本文重点介绍 LLMAgent，其他 Agent 类型以及多 Agent 系统的详细介绍请参考 [Multi-Agent](./multiagent.md)。

## 快速开始

**推荐使用方式：Runner**

我们强烈推荐使用 Runner 来执行 Agent，而不是直接调用 Agent 接口。Runner 提供了更友好的接口，集成了 Session、Memory 等服务，让使用更加简单。

**📖 了解更多：** 详细的使用方法请参考 [Runner](./runner.md)

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

使用模型实例和配置创建 LLMAgent，同时设置 Agent 的 Description 与 Instruction。

Description 用于描述 Agent 的基本功能和特性，Instruction 则定义了 Agent 在执行任务时应遵循的具体指令和行为准则。

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

### 占位符变量（会话状态注入）

LLMAgent 会自动在 `Instruction` 和可选的 `SystemPrompt` 中注入会话状态。支持的占位符语法：

- `{key}`：替换为 `session.State["key"]` 的字符串值
- `{key?}`：可选；如果不存在，替换为空字符串
- `{user:subkey}` / `{app:subkey}` / `{temp:subkey}`：访问用户/应用/临时命名空间（SessionService 会把 app/user 作用域的状态合并进 session，并带上前缀）

注意：

- 对于非可选的 `{key}`，若找不到则保留原样（便于 LLM 感知缺失上下文）
- 值读取自 `invocation.Session.State`（Runner + SessionService 会自动设置/合并）

示例：

```go
llm := llmagent.New(
  "research-agent",
  llmagent.WithModel(modelInstance),
  llmagent.WithInstruction(
    "You are a research assistant. Focus: {research_topics}. " +
    "User interests: {user:topics?}. App banner: {app:banner?}.",
  ),
)

// 通过 SessionService 初始化状态（用户态/应用态 + 会话本地键）
_ = sessionService.UpdateUserState(ctx, session.UserKey{AppName: app, UserID: user}, session.StateMap{
  "topics": []byte("quantum computing, cryptography"),
})
_ = sessionService.UpdateAppState(ctx, app, session.StateMap{
  "banner": []byte("Research Mode"),
})
// 无前缀键直接存到 session.State
_, _ = sessionService.CreateSession(ctx, session.Key{AppName: app, UserID: user, SessionID: sid}, session.StateMap{
  "research_topics": []byte("AI, ML, DL"),
})
```

进一步阅读：

- 示例：`examples/placeholder`、`examples/outputkey`
- Session API：`docs/mkdocs/zh/session.md`

### 使用 Runner 执行 Agent

使用 Runner 来执行 Agent，这是推荐的使用方式：

```go
import "trpc.group/trpc-go/trpc-agent-go/runner"

// 创建 Runner
runner := runner.NewRunner("demo-app", llmAgent)

// 直接发送消息，无需创建复杂的 Invocation
message := model.NewUserMessage("Hello! Can you tell me about yourself?")
eventChan, err := runner.Run(ctx, "user-001", "session-001", message)
if err != nil {
    log.Fatalf("执行 Agent 失败: %v", err)
}
```

### 处理事件流

`runner.Run()` 返回的 `eventChan` 是一个事件通道，Agent 执行过程中会持续向这个通道发送 Event 对象。

每个 Event 包含了某个时刻的执行状态信息：LLM 生成的内容、工具调用的请求和结果、错误信息等。通过遍历事件通道，你可以实时获取 Agent 的执行进展（详见下方 [Event](#event) 章节）。

通过事件通道接收执行结果：

```go
// 1. 获取事件通道（立即返回，开始异步执行）
eventChan, err := runner.Run(ctx, userID, sessionID, message)
if err != nil {
    log.Fatalf("failed to run agent: %v", err)
}

// 2. 处理事件流（实时接收执行结果）
for event := range eventChan {
    // 检查错误
    if event.Error != nil {
        log.Printf("error: %s", event.Error.Message)
        continue
    }

    // 处理响应内容
    if len(event.Response.Choices) > 0 {
        choice := event.Response.Choices[0]

        // 流式内容（实时显示）
        if choice.Delta.Content != "" {
            fmt.Print(choice.Delta.Content)
        }

        // 工具调用信息
        for _, toolCall := range choice.Message.ToolCalls {
            fmt.Printf("calling tool: %s\n", toolCall.Function.Name)
        }
    }

    // 检查是否完成（注意：工具调用完成时不应该 break）
    if event.IsFinalResponse() {
        fmt.Println()
        break
    }
}
```

该示例的完整代码可见 [examples/runner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)

**为什么推荐使用 Runner？**

1. **更简单的接口**：无需创建复杂的 Invocation 对象
2. **集成服务**：自动集成 Session、Memory 等服务
3. **更好的管理**：统一管理 Agent 的执行流程
4. **生产就绪**：适合生产环境使用

**💡 提示：** 想了解更多 Runner 的详细用法和高级功能？请查看 [Runner](./runner.md)

**高级用法：直接使用 Agent**

如果你需要更细粒度的控制，也可以直接使用 Agent 接口，但这需要创建 Invocation 对象：

## 核心概念

### Invocation（高级用法）

Invocation 是 Agent 执行流程的上下文对象，包含了单次调用所需的所有信息。**注意：这是高级用法，推荐使用 Runner 来简化操作。**

```go
import "trpc.group/trpc-go/trpc-agent-go/agent"

// 创建 Invocation 对象（高级用法）
invocation := agent.NewInvocation(
    agent.WithInvocationAgent(r.agent),                               // Agent 实例
    agent.WithInvocationSession(&session.Session{ID: "session-001"}), // Session
    agent.WithInvocationEndInvocation(false),                         // 是否结束调用
    agent.WithInvocationMessage(model.NewUserMessage("User input")),  // 用户消息
    agent.WithInvocationModel(modelInstance),                         // 使用的模型
)

// 直接调用 Agent（高级用法）
ctx := context.Background()
eventChan, err := llmAgent.Run(ctx, invocation)
if err != nil {
    log.Fatalf("执行 Agent 失败: %v", err)
}
```

**什么时候使用直接调用？**

- 需要完全控制执行流程
- 自定义 Session 和 Memory 管理
- 实现特殊的调用逻辑
- 调试和测试场景

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
	// RunOptions 是 Run 方法的选项配置
	RunOptions RunOptions
	// TransferInfo 支持 Agent 之间的控制权转移
	TransferInfo *TransferInfo
	// ModelCallbacks 允许在模型调用的不同阶段插入自定义逻辑
	ModelCallbacks *model.ModelCallbacks
	// ToolCallbacks 允许在工具调用的不同阶段插入自定义逻辑
	ToolCallbacks *tool.ToolCallbacks

    // notice
	noticeChanMap map[string]chan any
	noticeMu      *sync.Mutex
}
```

### Event

Event 是 Agent 执行过程中产生的实时反馈，通过 Event 流实时报告执行进展。

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
	// LongRunningToolIDs 是长时间运行函数调用的 ID 集合，Agent 客户端可以通过此字段了解哪个函数调用是长时间运行的，仅对函数调用事件有效
	LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`
}
```

Event 的流式特性让你能够实时看到 Agent 的工作过程，就像和一个真人对话一样自然。你只需要遍历 Event 流，检查每个 Event 的内容和状态，就能完整地处理 Agent 的执行结果。

### Agent 接口

Agent 接口定义了所有 Agent 必须实现的核心行为。这个接口让你能够统一使用不同类型的 Agent，同时支持工具调用和子 Agent 管理。

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

框架提供了多种类型的 Agent 实现，包括 LLMAgent、ChainAgent、ParallelAgent、CycleAgent 和 GraphAgent，不同类型 Agent 以及多 Agent 系统的详细介绍请参考 [Multi-Agent](./multiagent.md)。

## Callbacks

Callbacks 提供了丰富的回调机制，让你能够在 Agent 执行的关键节点注入自定义逻辑。

### 回调类型

框架提供了三种类型的回调：

**Agent Callbacks**：在 Agent 执行前后触发

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

// 在 llmAgent中使用回掉
llmagent := llmagent.New("llmagent", llmagent.WithAgentCallbacks(callbacks))
```

回调机制让你能够精确控制 Agent 的执行过程，实现更复杂的业务逻辑。

## 进阶使用

框架提供了 Runner、Session 和 Memory 等高级功能，用于构建更复杂的 Agent 系统。

**Runner 是推荐的使用方式**，它负责管理 Agent 的执行流程，串联了 Session/Memory Service 等能力，提供了更友好的接口。

Session Service 用于管理会话状态，支持对话历史记录和上下文维护。

Memory Service 用于记录用户的偏好信息，支持个性化体验。

**推荐阅读顺序：**

1. [Runner](runner.md) - 学习推荐的使用方式
2. [Session](session.md) - 了解会话管理
3. [Multi-Agent](multiagent.md) - 学习多 Agent 系统
