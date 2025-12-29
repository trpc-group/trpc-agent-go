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
- `{invocation:subkey}` ：替换为fmt.Sprintf("%+v",`invocation.state["subkey"]`)的值，（可以通过invocation.SetState(k,v)来设置）。

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
    "User interests: {user:topics?}. App banner: {app:banner?}." +
    "Invocation case: {invocation:case}",
  ),
)

inv := agent.NewInvoction()
inv.SetState("case", "case-1")

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

### 消息可见性选项
当前Agent可在需要时根据不同场景控制其对其他Agent生成的消息以及历史会话消息的可见性进行管理，可通过相关选项配置进行管理。
在与model交互时仅将可见的内容输入给模型。 

TIPS:
 - 不同sessionID的消息在任何场景下都是互不可见的，以下管控策略均针对同一个sessionID的消息
 - invocation.Message在任何场景下均可见
 - 未配置选项时，默认值为FullContext

配置:
- `llmagent.WithMessageFilterMode(MessageFilterMode)`:
  - `FullContext`: 所有能通过filterKey做前缀匹配的消息
  - `RequestContext`: 仅包含当前请求周期内通过filterKey前缀匹配的消息
  - `IsolatedRequest`: 仅包含当前请求周期内通过filterKey完全匹配的消息
  - `IsolatedInvocation`: 仅包含当前invocation周期内通过filterKey完全匹配的消息

推荐用法示例（该用法仅基于高级用法基础之上做了简化配置）:

```go
taskagentA := llmagent.New(
  "coordinator",
  llmagent.WithModel(modelInstance),
  // 对taskagentA、taskagentB生成的所有消息可见（包含同一sessionID的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.FullContext)
  // 对taskagentA、taskagentB当前runner.Run期间生成的所有消息可见（不包含历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.RequestContext)
  // 仅对taskagentA当前runner.Run期间生成的消息可见（不包含自己的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedRequest)
  // agent执性顺序：taskagentA-invocation1 -> taskagentB-invocation2 -> taskagentA-invocation3(当前执行阶段)
  // 仅对taskagentA当前taskagentA-invocation3期间生成的消息可见（不包含自己的历史会话消息以及taskagentA-invocation1期间生成的消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedInvocation)
)

taskagentB := llmagent.New(
  "coordinator",
  llmagent.WithModel(modelInstance),
  // 对taskagentA、taskagentB生成的所有消息可见（包含同一sessionID的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.FullContext),
  // 对taskagentA、taskagentB当前runner.Run期间生成的所有消息可见（不包含历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.RequestContext),
  // 仅对taskagentB当前runner.Run期间生成的消息可见（不包含自己的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
  // agent执性顺序：taskagentA-invocation1 -> taskagentB-invocation2 -> taskagentA-invocation3 -> taskagentB-invocation4(当前执行阶段)
  // 仅对taskagentB当前taskagentB-invocation4期间生成的消息可见（不包含自己的历史会话消息以及taskagentB-invocation2期间生成的消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedInvocation),
)

// 循环执行taskagentA、taskagentB
cycleAgent := cycleagent.New(
  "coordinator",
  llmagent.WithModel(modelInstance),
  llmagent.WithSubAgents([]agent.Agent{taskagentA, taskagentB}),
  llmagent.WithMessageFilterMode(llmagent.FullContext)
)

// 创建 Runner
runner := runner.NewRunner("demo-app", cycleAgent)

// 直接发送消息，无需创建复杂的 Invocation
message := model.NewUserMessage("Hello! Can you tell me about yourself?")
eventChan, err := runner.Run(ctx, "user-001", "session-001", message)
if err != nil {
    log.Fatalf("执行 Agent 失败: %v", err)
}
```

高阶用法示例：
可以单独通过 `WithMessageTimelineFilterMode`、`WithMessageBranchFilterMode`控制当前agent对历史消息与其他agent生成的消息可见性。
当前agent在与模型交互时，最终将同时满足两个条件的消息输入给模型。

`配置:`
- `WithMessageTimelineFilterMode`: 时间维度可见性控制
  - `TimelineFilterAll`: 包含历史消息以及当前请求中所生成的消息
  - `TimelineFilterCurrentRequest`: 仅包含当前请求(一次runner.Run为一次请求)中所生成的消息
  - `TimelineFilterCurrentInvocation`: 仅包含当前invocation上下文中生成的消息
- `WithMessageBranchFilterMode`: 分支维度可见性控制（用于控制对其他agent生成消息的可见性）
  - `BranchFilterModePrefix`: 通过Event.FilterKey与Invocation.eventFilterKey做前缀匹配
  - `BranchFilterModeAll`: 所有agent的均消息
  - `BranchFilterModeExact`: 仅自己生成的消息可见
  
```go
llmAgent := llmagent.New(
    "demo-agent",                      // Agent 名称
    llmagent.WithModel(modelInstance), // 设置模型
    llmagent.WithDescription("A helpful AI assistant for demonstrations"),              // 设置描述
    llmagent.WithInstruction("Be helpful, concise, and informative in your responses"), // 设置指令
    llmagent.WithGenerationConfig(genConfig),                                           // 设置生成参数

    // 设置传给模型的消息过滤模式，最终传给模型的消息需同时满足WithMessageTimelineFilterMode与WithMessageBranchFilterMode条件
    // 时间维度过滤条件
    // 默认值: llmagent.TimelineFilterAll
    // 可选值:
    //  - llmagent.TimelineFilterAll: 包含历史消息以及当前请求中所生成的消息
    //  - llmagent.TimelineFilterCurrentRequest: 仅包含当前请求中所生成的消息
    //  - llmagent.TimelineFilterCurrentInvocation: 仅包含当前invocation上下文中生成的消息
    llmagent.WithMessageTimelineFilterMode(llmagent.TimelineFilterAll),
    // 分支维度过滤条件
    // 默认值: llmagent.BranchFilterModePrefix
    // 可选值:
    //  - llmagent.BranchFilterModeAll: 包含所有agent的消息, 当前agent与模型交互时,如需将所有agent生成的有效内容消息同步给模型时可设置该值
    //  - llmagent.BranchFilterModePrefix: 通过Event.FilterKey与Invocation.eventFilterKey做前缀匹配过滤消息, 期望将与当前agent以及相关上下游agent生成的消息传递给模型时，可设置该值
    //  - llmagent.BranchFilterModeExact: 通过Event.FilterKey==Invocation.eventFilterKey过滤消息，当前agent与模型交互时,仅需使用当前agent生成的消息时可设置该值
    llmagent.WithMessageBranchFilterMode(llmagent.BranchFilterModePrefix),
)
```

### 推理内容模式（DeepSeek 思考模式）

当使用具有思考/推理能力的模型（如 DeepSeek）时，模型会同时输出 `reasoning_content`（思维链）和 `content`（最终回答）。根据 [DeepSeek API 文档](https://api-docs.deepseek.com/zh-cn/guides/thinking_mode)，在多轮对话中，不应将上一轮的 `reasoning_content` 发送给模型。

LLMAgent 提供 `WithReasoningContentMode` 来控制对话历史中 `reasoning_content` 的处理方式：

**可用模式：**

| 模式 | 常量 | 描述 |
|------|------|------|
| 丢弃之前轮次 | `ReasoningContentModeDiscardPreviousTurns` | 丢弃之前请求轮次的 `reasoning_content`，保留当前请求的。**（默认，推荐）** |
| 保留全部 | `ReasoningContentModeKeepAll` | 保留历史中的所有 `reasoning_content`（用于调试）。 |
| 全部丢弃 | `ReasoningContentModeDiscardAll` | 丢弃历史中的所有 `reasoning_content`，以最大化节省带宽。 |

**使用示例：**

```go
// DeepSeek 思考模式的推荐配置。
agent := llmagent.New(
    "deepseek-agent",
    llmagent.WithModel(deepseekModel),
    llmagent.WithInstruction("You are a helpful assistant."),
    // 丢弃之前轮次的 reasoning_content（推荐用于 DeepSeek）。
    llmagent.WithReasoningContentMode(llmagent.ReasoningContentModeDiscardPreviousTurns),
)
```

**工作原理：**

- **`keep_all`**：所有 `reasoning_content` 都保留在会话历史中。如果需要保留思维链用于调试或分析，请使用此模式。
- **`discard_previous_turns`**：在构建新请求的消息列表时，属于之前请求的消息的 `reasoning_content` 会被清除。当前请求内的消息（例如在工具调用循环期间）保留其 `reasoning_content`。这遵循 DeepSeek 的建议。
- **`discard_all`**：在发送给模型之前，所有历史消息的 `reasoning_content` 都会被清除。

**注意：** 此选项仅影响发送给模型之前对历史消息的处理方式。当前响应的 `reasoning_content` 始终会被捕获并存储在会话事件中。

### 委托可见性选项

在构建多 Agent（智能体）系统（Agent 之间的任务委托）时，LLMAgent 提供“默认占位消息”的统一配置。转移（transfer）事件始终包含提示文本，并统一打上 `transfer` 标签，前端（UI, User Interface）可按标签过滤。

- `llmagent.WithDefaultTransferMessage(string)`
  - 配置当模型未提供 `message` 时的“转移默认消息”。
  - 传入空字符串表示“禁用默认消息注入”；传入非空字符串表示“启用并使用该字符串作为默认消息”。

用法示例：

```go
coordinator := llmagent.New(
  "coordinator",
  llmagent.WithModel(modelInstance),
  llmagent.WithSubAgents([]agent.Agent{mathAgent, weatherAgent}),
  // 转移提示事件总是会输出（带有 `transfer` 标签），如需隐藏可在 UI 层按标签过滤
  // 当模型未传 message 时，自定义默认消息（传空字符串可禁用）
  llmagent.WithDefaultTransferMessage("Handing off to the specialist"),
)
```

说明：

- 这些选项不会改变真实的委托/切换逻辑，只影响“对外可见的提示文本”或“是否注入默认占位消息”。
- 转移提示事件统一以 `Response.Object == "agent.transfer"` 输出；如需在 UI 层隐藏系统级提示，可直接过滤该对象类型的事件。

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
// Invocation 是 Agent 执行流程的上下文对象，包含单次调用所需的全部信息
type Invocation struct {
    // Agent 指定要调用的 Agent 实例
    Agent Agent
    // AgentName 标识要调用的 Agent 实例名称
    AgentName string
    // InvocationID 为每次调用提供唯一标识
    InvocationID string
    // Branch 用于分层事件过滤的分支标识符
    Branch string
    // EndInvocation 标识是否结束调用
    EndInvocation bool

    // Session 维护对话上下文状态
    Session *session.Session
    // Model 指定要使用的模型实例
    Model model.Model
    // Message 是用户发送给 Agent 的具体内容
    Message model.Message
    // RunOptions 是 Run 方法的选项配置
    RunOptions RunOptions
    // TransferInfo 支持 Agent 间的控制权转移
    TransferInfo *TransferInfo

    // 结构化输出配置（可选）
    StructuredOutput     *model.StructuredOutput
    StructuredOutputType reflect.Type

    // 为本次调用注入的服务
    MemoryService   memory.Service
    ArtifactService artifact.Service

    // 内部通知：当事件写入会话时发出通知
    noticeChanMap map[string]chan any
    noticeMu      *sync.Mutex

    // 内部：事件过滤键与父调用（用于嵌套流程）
    eventFilterKey string
    parent         *Invocation

    // 调用级状态（延迟初始化，通过 stateMu 保护并发）
    state   map[string]any
    stateMu sync.RWMutex

    // 可选的调用级安全限制（通常由 LLMAgent 在 setupInvocation 中设置）。
    MaxLLMCalls      int
    MaxToolIterations int

    // 与 MaxLLMCalls / MaxToolIterations 配套使用的内部计数器。
    llmCallCount       int
    toolIterationCount int
}
```

#### Invocation State

`Invocation` 提供了通用的状态存储机制，用于在单次调用的生命周期内共享数据。这对于 callbacks、middleware 或任何需要在 invocation 级别存储临时数据的场景都很有用。

**核心方法：**

```go
// 设置状态值
inv.SetState(key string, value any)

// 获取状态值
value, ok := inv.GetState(key string)

// 删除状态值
inv.DeleteState(key string)
```

**特点：**

- **Invocation 级作用域**：状态自动限定在单次 Invocation 内
- **线程安全**：内置 RWMutex 保护，支持并发访问
- **懒初始化**：首次使用时才分配内存
- **通用性强**：可用于 callbacks、middleware、自定义逻辑等多种场景

**使用示例：**

> **版本要求**  
> 结构化回调 API（推荐）需要 **trpc-agent-go >= 0.6.0**。

```go
// 在 BeforeAgentCallback 中存储数据
// 注意：结构化回调 API 需要 trpc-agent-go >= 0.6.0
callbacks := agent.NewCallbacks()
callbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    args.Invocation.SetState("agent:start_time", time.Now())
    args.Invocation.SetState("custom:request_id", "req-123")
    return nil, nil
})

// 在 AfterAgentCallback 中读取数据
callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if startTime, ok := args.Invocation.GetState("agent:start_time"); ok {
        duration := time.Since(startTime.(time.Time))
        log.Printf("Execution took: %v", duration)
        args.Invocation.DeleteState("agent:start_time")
    }
    return nil, nil
})
```

**推荐的键名约定：**

- Agent 回调：`"agent:xxx"`
- Model 回调：`"model:xxx"`
- Tool 回调：`"tool:toolName:xxx"`
- 中间件：`"middleware:xxx"`
- 自定义逻辑：`"custom:xxx"`

详细的使用说明和更多示例请参考 [Callbacks](./callbacks.md#invocation-state)。

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

> **版本要求**  
> 结构化回调 API（推荐）需要 **trpc-agent-go >= 0.6.0**。

### 回调类型

框架提供了三种类型的回调：

**Agent Callbacks**：在 Agent 执行前后触发

```go
// 使用 agent.NewCallbacks() 创建回调
callbacks := agent.NewCallbacks()
```

**Model Callbacks**：在模型调用前后触发

```go
// 使用 model.NewCallbacks() 创建回调
callbacks := model.NewCallbacks()
```

**Tool Callbacks**：在工具调用前后触发

```go
// 使用 tool.NewCallbacks() 创建回调
callbacks := tool.NewCallbacks()
```

### 使用示例

```go
// 创建 Agent 回调（使用结构化 API）
// 注意：结构化回调 API 需要 trpc-agent-go >= 0.6.0
callbacks := agent.NewCallbacks()
callbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    log.Printf("Agent %s 开始执行", args.Invocation.AgentName)
    return nil, nil
})
callbacks.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if args.Error != nil {
        log.Printf("Agent %s 执行出错: %v", args.Invocation.AgentName, args.Error)
    } else {
        log.Printf("Agent %s 执行完成", args.Invocation.AgentName)
    }
    return nil, nil
})

// 在 llmAgent 中使用回调
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

## 运行时动态更新 Instruction

你可以在 Agent 已经创建并被 Runner 使用的情况下，动态更新其行为文案：

- Instruction：用于约束 Agent 行为的说明文本（追加到系统消息中）。
- Global Instruction（系统提示词）：系统级前言（作为系统消息的前缀）。

两者都可以在已有的 `LLMAgent` 实例上动态设置，新值会作用于后续的模型请求。

示例

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 1）服务启动时只构建一次模型与 Agent
mdl := openai.New("gpt-4o-mini", openai.Options{})
llm := llmagent.New(
    "support-bot",
    llmagent.WithModel(mdl),
    llmagent.WithInstruction("Be helpful and concise."),
)
run := runner.NewRunner("my-app", llm)

// 2）运行中根据用户在后台修改的提示词，动态更新
llm.SetInstruction("Translate all user inputs to French.")
llm.SetGlobalInstruction("System: Safety first. No PII leakage.")

// 3）之后的对话轮次将使用最新的提示词
msg := model.NewUserMessage("Where is the nearest museum?")
ch, err := run.Run(context.Background(), "u1", "s1", msg)
_ = ch; _ = err
```

注意

- 线程安全：上述设置方法是并发安全的，可在服务处理请求时调用。
- 同一轮次内的效果：若一次调用过程中会触发多次模型请求（例如工具调用后再次提问），更新可能会对同一轮后续的请求生效。若需要“每次调用内保持稳定”，可在调用开始时确定或冻结提示词。
- 个性化上下文：若需按用户/会话动态注入内容，优先使用指令中的占位符加会话状态注入（见上文“占位符变量”一节）。

### 另一种方式：用占位符驱动动态 System Prompt

如果不想在运行时调用 setter，也可以把 Instruction 写成模板，然后用会话状态（Session/App/User/Temp）来“喂”值。指令处理器会在每次请求时注入占位符。

模式

- 持久化“按用户”：写到 `user:*`，在模板里用 `{user:key}` 引用
- 持久化“按应用”：写到 `app:*`，在模板里用 `{app:key}` 引用
- 每轮一次（临时）：写入会话的 `temp:*` 命名空间，模板用 `{temp:key}`（不会持久化）

示例：按用户动态提示词

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

svc := inmemory.NewSessionService()
app, user, sid := "my-app", "u1", "s1"

// 1）在指令模板里引用用户态 key
llm := llmagent.New(
  "dyn-agent",
  llmagent.WithInstruction("{user:system_prompt}"),
)
run := runner.NewRunner(app, llm, runner.WithSessionService(svc))

// 2）当用户在后台改设置时，更新用户态状态
_ = svc.UpdateUserState(context.Background(), session.UserKey{AppName: app, UserID: user}, session.StateMap{
  "system_prompt": []byte("You are a helpful assistant. Always answer in English."),
})

// 3）后续运行会通过占位符读取最新值
_, _ = run.Run(context.Background(), user, sid, model.NewUserMessage("Hi!"))
```

示例：通过前置回调注入本轮临时值（temp）

> **版本要求**  
> 结构化回调 API（推荐）需要 **trpc-agent-go >= 0.6.0**。

```go
// 注意：结构化回调 API 需要 trpc-agent-go >= 0.6.0
callbacks := agent.NewCallbacks()
callbacks.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
  if args.Invocation != nil && args.Invocation.Session != nil {
    if args.Invocation.Session.State == nil {
      args.Invocation.Session.State = make(map[string][]byte)
    }
    // 为"本轮"临时指定指令
    args.Invocation.Session.State["temp:sys"] = []byte("Translate to French.")
  }
  return nil, nil
})

llm := llmagent.New(
  "temp-agent",
  llmagent.WithInstruction("{temp:sys}"),
  llmagent.WithAgentCallbacks(callbacks), // 需要 trpc-agent-go >= 0.6.0
)
```

注意事项

- 内存版 `UpdateUserState` 出于安全设计禁止写 `temp:*`；需要临时值时，直接往 `invocation.Session.State` 写（例如通过回调）。
- 占位符是在“请求时”解析；只要你换了存储的值，下一次模型请求就会用新值，无需重建 Agent。
