# Event 使用文档

Event 是 trpc-agent-go 中 Agent 与用户之间通信的核心机制。它就像一个消息信封，承载着 Agent 的响应内容、工具调用结果、错误信息等。通过 Event，你可以实时了解 Agent 的工作状态，处理流式响应，实现多 Agent 协作，以及追踪工具执行。

## Event 概述

Event 是 Agent 与用户之间通信的载体。

用户通过 `runner.Run()` 方法获取事件流，然后监听事件通道来处理 Agent 的响应。

### Event 结构

`Event` 表示 Agent 与用户之间的一次事件，结构定义如下：

```go
type Event struct {
    // Response 是 Event 的基础响应结构，承载 LLM 的响应
    *model.Response

    // RequestID 记录关联本次请求的ID，可由runner.Run通过agent.WithRequestID("request-ID")传递.
	RequestID string `json:"requestID,omitempty"`

	// InvocationID 当前执行上下文的ID.
	InvocationID string `json:"invocationId"`

	// ParentInvocationID 上一级执行上下文ID.
	ParentInvocationID string `json:"parentInvocationId,omitempty"`

    // Author 是事件的发起者
    Author string `json:"author"`

    // ID 是事件的唯一标识符
    ID string `json:"id"`

    // Timestamp 是事件的时间戳
    Timestamp time.Time `json:"timestamp"`

    // Branch 是分支标识符，用于多 Agent 协作
    Branch string `json:"branch,omitempty"`

    // Tag 使用 tags 为事件打业务标签
    Tag string `json:"tag,omitempty"`

    // RequiresCompletion 表示此事件是否需要完成信号
    RequiresCompletion bool `json:"requiresCompletion,omitempty"`

    // LongRunningToolIDs 是长运行函数调用的 ID 集合
    // Agent 客户端将从此字段了解哪些函数调用是长时间运行的
    // 仅对函数调用事件有效
    LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`

    // StateDelta 是需要写入会话状态的增量（例如 Processor 产出的状态变更）
    StateDelta map[string][]byte `json:"stateDelta,omitempty"`

    // StructuredOutput 携带类型化的内存内结构化输出，不参与序列化
    StructuredOutput any `json:"-"`

    // Actions 携带对 Flow 的行为提示（例如：跳过工具后的总结）
    Actions *EventActions `json:"actions,omitempty"`

    // FilterKey 是用于事件层级过滤的标识
    FilterKey string `json:"filterKey,omitempty"`
}

// EventActions 为事件附带的可选行为提示
type EventActions struct {
    // SkipSummarization 表示 Flow 在 tool.response 后不再进行总结型 LLM 调用
    SkipSummarization bool `json:"skipSummarization,omitempty"`
}
```

`SkipSummarization` 是一个流程控制提示，不表示当前这条
`tool.response` 已经变成 assistant final response。若你需要整次运行真正的
终止事件，仍应持续消费直到 `runner.completion`。

#### FilterKey（层级作用域 key）

`FilterKey` 是每条事件上的可选字段。你可以把它理解成“像路径一样的标签”，主要用在：

- 构建下一次 Prompt 时，筛选哪些历史事件允许进入上下文（`WithMessageBranchFilterMode`）。
- 生成/读取按作用域拆分的会话摘要（`WithSummaryFilterKey`）。

FilterKey 通过 `/` 做层级分隔，例如：

- `my-app/user-messages`
- `my-app/auth/role_admin`

在 `prefix` 模式下，匹配规则是**层级匹配**：只要两者存在祖先/后代关系就算匹配
（例如 `my-app` 会匹配 `my-app/auth/...`）。

如果你需要严格隔离（不希望继承父级内容），请使用 `BranchFilterModeSubtree`。
更详细的入门说明见 Session 文档：
`FilterKey、EventFilterKey 与 BranchFilterMode`。

`model.Response` 是 Event 的基础响应结构，承载了 LLM 的响应、工具调用以及错误等信息，定义如下：

```go
type Response struct {
    // 响应唯一标识
    ID string `json:"id"`
    
    // 对象类型（如 "chat.completion", "error" 等），帮助客户端识别处理方式
    Object string `json:"object"`
    
    // 创建时间戳
    Created int64 `json:"created"`
    
    // 使用的模型名称
    Model string `json:"model"`
    
    // 响应可选项，LLM 可能生成多个候选响应供用户选择，默认只有 1 个
    Choices []Choice `json:"choices"`
    
    // 使用统计信息，记录 token 使用情况
    Usage *Usage `json:"usage,omitempty"`
    
    // 系统指纹
    SystemFingerprint *string `json:"system_fingerprint,omitempty"`
    
    // 错误信息
    Error *ResponseError `json:"error,omitempty"`
    
    // 时间戳
    Timestamp time.Time `json:"timestamp"`
    
    // 表示当前响应流是否结束。
    //
    // 注意：Done=true 不一定代表整个流程已结束。
    // 对于图式流程，请以 Runner 完成事件作为结束信号。
    Done bool `json:"done"`
    
    // 是否为部分响应
    IsPartial bool `json:"is_partial"`
}

type Choice struct {
    // 选择索引
    Index int `json:"index"`
    
    // 完整消息，包含整个响应
    Message Message `json:"message,omitempty"`
    
    // 增量消息，用于流式响应，只包含当前块的新内容
    // 例如：完整响应 "Hello, how can I help you?" 在流式响应中：
    // 第一个事件：Delta.Content = "Hello"
    // 第二个事件：Delta.Content = ", how"  
    // 第三个事件：Delta.Content = " can I help you?"
    Delta Message `json:"delta,omitempty"`
    
    // 完成原因
    FinishReason *string `json:"finish_reason,omitempty"`
}

type Message struct {
    // 消息发起人的角色，例如 "system", "user", "assistant", "tool"
    Role string `json:"role"`

    // 消息内容
    Content string `json:"content"`

    // 多模式消息的内容片段
    ContentParts []ContentPart `json:"content_parts,omitempty"`

    // 工具响应所使用的工具的 ID
    ToolID string `json:"tool_id,omitempty"`

    // 工具响应所使用的工具的名称
    ToolName string `json:"tool_name,omitempty"`

    // 可选的工具调用
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Usage struct {
    // 提示词使用的 Token 数量.
    PromptTokens int `json:"prompt_tokens"`

    // 补全使用的 Token 数量.
    CompletionTokens int `json:"completion_tokens"`

    // 响应中使用的总 Token 数量.
    TotalTokens int `json:"total_tokens"`

    // 时间统计信息（可选）
    TimingInfo *TimingInfo `json:"timing_info,omitempty"`
}

type TimingInfo struct {
    // FirstTokenDuration 是从请求开始到第一个有意义 token 的累积时长
    // "有意义的 token" 定义为：包含 reasoning 内容、常规内容或工具调用的第一个 chunk
    // 
    // 返回时机：
    // - 流式请求：在收到第一个有意义的 chunk 时立即计算并返回
    // - 非流式请求：在收到完整响应时计算并返回
    FirstTokenDuration time.Duration `json:"time_to_first_token,omitempty"`

    // ReasoningDuration 是 reasoning 阶段的累积时长（仅流式模式）
    // 从每次 LLM 调用的第一个 reasoning chunk 到最后一个 reasoning chunk 的时间
    //
    // 测量细节：
    // - 收到第一个包含 reasoning 内容的 chunk 时开始计时
    // - 持续计时所有后续的 reasoning chunks
    // - 收到第一个非 reasoning chunk（常规内容或工具调用）时停止计时
    //
    // 返回时机：
    // - 流式请求：在检测到 reasoning 结束时（即收到第一个非 reasoning 的 content/tool call chunk）
    //   立即计算并返回
    // - 非流式请求：无法精确测量，此字段将保持为 0
    ReasoningDuration time.Duration `json:"reasoning_duration,omitempty"`
}
```

### Event 类型

Event 在以下场景中会被创建和发送：

1. **用户消息事件**：用户发送消息时自动创建
2. **Agent 响应事件**：Agent 生成响应时创建
3. **流式响应事件**：流式模式下每个响应块都会创建
4. **工具调用事件**：Agent 调用工具时创建
5. **错误事件**：发生错误时创建
6. **Agent 转移事件**：Agent 转移给其他 Agent 时创建
7. **完成事件**：Agent 执行完成时创建

根据 `model.Response.Object` 字段，Event 可以分为以下类型：

```go
const (
    // 错误事件
    ObjectTypeError = "error"
    
    // 工具响应事件
    ObjectTypeToolResponse = "tool.response"
    
    // 预处理事件
    ObjectTypePreprocessingBasic = "preprocessing.basic"
    ObjectTypePreprocessingContent = "preprocessing.content"
    ObjectTypePreprocessingIdentity = "preprocessing.identity"
    ObjectTypePreprocessingInstruction = "preprocessing.instruction"
    ObjectTypePreprocessingPlanning = "preprocessing.planning"
    
    // 后处理事件
    ObjectTypePostprocessingPlanning = "postprocessing.planning"
    ObjectTypePostprocessingCodeExecution = "postprocessing.code_execution"
    
    // Agent 转移事件
    ObjectTypeTransfer = "agent.transfer"
    
    // Runner 完成事件
    ObjectTypeRunnerCompletion = "runner.completion"
)
```

### 过滤委托提示（Transfer Announcements）

委托提示（Agent 委托/转移说明）统一以 `Response.Object == "agent.transfer"` 的事件输出。

常见形式为：
- 交接提示：`Transferring control to agent: <name>`

 如需在 UI 层隐藏这些系统级提示，可以采用两种兼容方式：
 - 按对象类型过滤：隐藏 `Response.Object == "agent.transfer"` 的事件。
 - 按标签（Tag）过滤：隐藏 `Event.Tag` 中包含 `transfer` 的事件。框架会为与委托相关的事件（包括 transfer 工具结果）统一打上 `transfer` 标签，按标签过滤不会破坏 ToolCall/ToolResult 的配对关系。

 标签以分号（`;`）分隔。自定义事件可使用 `event.WithTag(tag)` 追加标签，多标签格式为 `tag1;tag2;...`。

### 代码执行事件标签

对于代码执行（Code Execution）相关的事件，可通过 `Event.Tag` 区分代码和执行结果：

- **代码执行事件**：`Response.Object == "postprocessing.code_execution"` 且 `Event.ContainsTag(event.TagCodeExecution)`
- **执行结果事件**：`Response.Object == "postprocessing.code_execution"` 且 `Event.ContainsTag(event.TagCodeExecutionResult)`

相关常量定义在 `trpc.group/trpc-go/trpc-agent-go/event` 包中。

#### 辅助方法：检测 Runner 完成

使用便捷方法来判断整次运行是否已完成，无论 Agent 类型如何：

```go
// e.IsRunnerCompletion() 会在终止的 runner-completion 事件上返回 true。
if e.IsRunnerCompletion() {
    // 可安全停止读取事件通道的时机
}
```

不要混淆 `event.IsFinalResponse()` 与 `event.IsRunnerCompletion()`：

- `event.IsFinalResponse()` 复用的是嵌入 `Response` 的判断逻辑。它只说明
  当前这条响应已经结束：不是 partial、不是 tool-call response，且
  `Response.Done == true`。这可能对应 assistant 文本、`tool.response`，
  也可能是终止错误响应。
- `event.IsRunnerCompletion()` 判断的是 Runner 是否发出了终止
  `runner.completion` 事件。只有它返回 true，才表示整次 `Runner.Run`
  已真正结束，后续不会再有新的运行事件。

经验上：

- 想判断“当前这条输出是否已经完整”，可使用 `IsFinalResponse()`
- 想停止消费事件流、读取最终状态或把本次运行视为结束，应使用
  `IsRunnerCompletion()`

### Event 创建

在开发自定义 Agent 类型或 Processor 时，需要创建 Event。

Event 提供了三种创建方法，适用于不同场景。

```go
// 创建新事件
func New(invocationID, author string, opts ...Option) *Event

// 创建错误事件
func NewErrorEvent(invocationID, author, errorType, errorMessage string) *Event

// 从响应创建事件
func NewResponseEvent(invocationID, author string, response *model.Response) *Event
```

**参数说明：**

- `invocationID string`：调用唯一标识
- `author string`：事件发起者
- `opts ...Option`：可选的配置选项（仅 New 方法）
- `errorType string`：错误类型（仅 NewErrorEvent 方法）
- `errorMessage string`：错误消息（仅 NewErrorEvent 方法）
- `response *model.Response`：响应对象（仅 NewResponseEvent 方法）

框架支持以下 Option 用以配置 Event：

- `WithBranch(branch string)`：设置事件的分支标识
- `WithResponse(response *model.Response)`：设置事件的响应内容
- `WithObject(o string)`：设置事件的类型

**示例：**
```go
// 创建基本事件
evt := event.New("invoke-123", "agent")

// 创建带分支的事件
evt := event.New("invoke-123", "agent", event.WithBranch("main"))

// 创建错误事件
evt := event.NewErrorEvent("invoke-123", "agent", "api_error", "请求超时")

// 从响应创建事件
response := &model.Response{
    Object: "chat.completion",
    Done:   true,
    Choices: []model.Choice{{Message: model.Message{Role: "assistant", Content: "Hello!"}}},
}
evt := event.NewResponseEvent("invoke-123", "agent", response)
```

### 工具响应流式输出（含 AgentTool 转发）

当调用支持流式的工具（包括 AgentTool）时，框架会发送 `tool.response` 事件：

- 流式分片：内容在 `choice.Delta.Content`，并且 `Done=false`、`IsPartial=true`
- 最终消息：`choice.Message.Role=tool`，内容在 `choice.Message.Content`

当 AgentTool 开启 `WithStreamInner(true)` 时，还会把子 Agent 的事件直接转发到父流程：

- 子 Agent 转发事件依然是 `event.Event`，其中增量内容同样在 `choice.Delta.Content`
- 为避免重复打印，子 Agent 最终整段文本不会再次作为转发事件出现，但会被聚合到最终的 `tool.response` 内容中，供下一轮 LLM 使用
- 如果你只想保留内部进度、不想转发子 Agent 的 assistant 正文，可以把
  `WithStreamInner(true)` 和
  `WithInnerTextMode(agenttool.InnerTextModeExclude)` 搭配使用

Runner 会自动针对需要完成信号的事件（`RequiresCompletion=true`）发送完成信号，使用者无需额外处理。

事件循环中的处理示例：

```go
if evt.Response != nil && evt.Object == model.ObjectTypeToolResponse && len(evt.Response.Choices) > 0 {
    for _, ch := range evt.Response.Choices {
        if ch.Delta.Content != "" { // 部分片段
            fmt.Print(ch.Delta.Content)
            continue
        }
        if ch.Message.Role == model.RoleTool && ch.Message.Content != "" { // 最终内容
            fmt.Println(strings.TrimSpace(ch.Message.Content))
        }
    }
    continue // 不要把工具响应当成助手内容打印
}
```

提示：自定义事件时，优先使用 `event.New(...)` 搭配 `WithResponse`、`WithBranch` 等，以保证 ID 和时间戳等元数据一致。

### GraphAgent 节点自定义事件（非 LLM 输出）

在 GraphAgent 工作流里，Function/Tool/Agent 等非 LLM 节点经常会计算出一些
“中间结果”（例如 `processed`）。

把结果写进 `graph.State{...}` 只会更新图内部状态，供后续节点使用；它不会
自动变成助手文本出现在事件流里。

如果你希望把这些中间结果实时发送给用户，可以在 NodeFunc 里主动发出节点自定义
事件（`graph.node.custom`），并在消费事件流时解析出来。

#### 在 NodeFunc 中发出事件

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
    stateKeyInput  = "input"
    stateKeyOutput = "output"

    eventTypeProcessed = "process.processed"
)

func processNode(ctx context.Context, state graph.State) (any, error) {
    input, _ := state[stateKeyInput].(string)
    processed := transform(input)

    emitter := graph.GetEventEmitterWithContext(ctx, state)
    if err := emitter.EmitCustom(eventTypeProcessed, processed); err != nil {
        // Optional: log and keep going.
    }

    return graph.State{stateKeyOutput: processed}, nil
}
```

#### 在事件循环中读取

事件 payload 会以 JSON 的形式存放在
`Event.StateDelta[graph.MetadataKeyNodeCustom]` 中。
payload 需要能被 JSON 序列化（string/map/struct 等）。

```go
import (
    "encoding/json"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func handleEvents(eventChan <-chan *event.Event) {
    for evt := range eventChan {
        if evt == nil || evt.Response == nil {
            continue
        }
        if evt.Object != graph.ObjectTypeGraphNodeCustom {
            continue
        }

        b, ok := evt.StateDelta[graph.MetadataKeyNodeCustom]
        if !ok {
            continue
        }

        var meta graph.NodeCustomEventMetadata
        if err := json.Unmarshal(b, &meta); err != nil {
            continue
        }

        switch meta.Category {
        case graph.NodeCustomEventCategoryCustom:
            // Your payload is in meta.Payload.
        case graph.NodeCustomEventCategoryProgress:
            // Progress is meta.Progress (0-100), text is meta.Message.
        case graph.NodeCustomEventCategoryText:
            // Streamed text is meta.Message.
        }
    }
}
```

### 标签（Tags）

Event 支持通过 `Event.Tag` 添加简单标签，便于过滤与统计：

- 分隔符：`;`（分号）。多标签拼接为 `tag1;tag2`。
- 辅助函数：`event.WithTag("<tag>")` 在不覆盖已有标签的情况下追加新标签。
- 内置用法：与委托相关的事件会统一打上 `transfer` 标签。UI 可据此隐藏内部委托类消息，同时保留完整事件流，便于调试与处理。

### Event 方法

Event 提供了 `Clone` 方法，用于创建 Event 的深拷贝。

```go
func (e *Event) Clone() *Event
```

## Event 使用示例

这个示例展示了如何在实际应用中使用 Event 处理 Agent 的流式响应、工具调用和错误处理。

### 核心流程

1. **发送用户消息**：通过 `runner.Run()` 启动 Agent 处理
2. **接收事件流**：实时处理 Agent 返回的事件
3. **处理不同类型事件**：区分流式内容、工具调用、错误等
4. **可视化输出**：为用户提供友好的交互体验

### 代码示例

```go
// processMessage 处理单次消息交互
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
    message := model.NewUserMessage(userMessage)

    // 通过 runner 运行 agent
    eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
    if err != nil {
        return fmt.Errorf("failed to run agent: %w", err)
    }

    // 处理响应
    return c.processResponse(eventChan)
}

// processResponse 处理响应，包括流式响应和工具调用可视化
func (c *multiTurnChat) processResponse(eventChan <-chan *event.Event) error {
    fmt.Print("🤖 Assistant: ")

    var (
        fullContent       string        // 累积的完整内容
        toolCallsDetected bool          // 是否检测到工具调用
        assistantStarted  bool          // Assistant 是否已开始回复
    )

    for event := range eventChan {
        // 处理单个事件
        if err := c.handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
            return err
        }

        // 检查是否为整次运行完成事件
        if event.IsRunnerCompletion() {
            fmt.Printf("\n")
            break
        }
    }

    return nil
}

// handleEvent 处理单个事件
func (c *multiTurnChat) handleEvent(
    event *event.Event,
    toolCallsDetected *bool,
    assistantStarted *bool,
    fullContent *string,
) error {
    // 1. 处理错误事件
    if event.Error != nil {
        fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
        return nil
    }

    // 2. 处理工具调用
    if c.handleToolCalls(event, toolCallsDetected, assistantStarted) {
        return nil
    }

    // 3. 处理工具响应
    if c.handleToolResponses(event) {
        return nil
    }

    // 4. 处理内容
    c.handleContent(event, toolCallsDetected, assistantStarted, fullContent)

    return nil
}

// handleToolCalls 检测并显示工具调用
func (c *multiTurnChat) handleToolCalls(
    event *event.Event,
    toolCallsDetected *bool,
    assistantStarted *bool,
) bool {
    if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
        *toolCallsDetected = true
        if *assistantStarted {
            fmt.Printf("\n")
        }
        fmt.Printf("🔧 Tool calls initiated:\n")
        for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
            fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
            if len(toolCall.Function.Arguments) > 0 {
                fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
            }
        }
        fmt.Printf("\n🔄 Executing tools...\n")
        return true
    }
    return false
}

// handleToolResponses 检测并显示工具响应
func (c *multiTurnChat) handleToolResponses(event *event.Event) bool {
    if event.Response != nil && len(event.Response.Choices) > 0 {
        for _, choice := range event.Response.Choices {
            if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
                fmt.Printf("✅ Tool response (ID: %s): %s\n",
                    choice.Message.ToolID,
                    strings.TrimSpace(choice.Message.Content))
                return true
            }
        }
    }
    return false
}

// handleContent 处理并显示内容
func (c *multiTurnChat) handleContent(
    event *event.Event,
    toolCallsDetected *bool,
    assistantStarted *bool,
    fullContent *string,
) {
    if len(event.Response.Choices) > 0 {
        choice := event.Response.Choices[0]
        content := c.extractContent(choice)

        if content != "" {
            c.displayContent(content, toolCallsDetected, assistantStarted, fullContent)
        }
    }
}

// extractContent 根据流式模式提取内容
func (c *multiTurnChat) extractContent(choice model.Choice) string {
    if c.streaming {
        // 流式模式：使用增量内容
        return choice.Delta.Content
    }
    // 非流式模式：使用完整消息内容
    return choice.Message.Content
}

// displayContent 将内容打印到控制台
func (c *multiTurnChat) displayContent(
    content string,
    toolCallsDetected *bool,
    assistantStarted *bool,
    fullContent *string,
) {
    if !*assistantStarted {
        if *toolCallsDetected {
            fmt.Printf("\n🤖 Assistant: ")
        }
        *assistantStarted = true
    }
    fmt.Print(content)
    *fullContent += content
}
```

### RequestID,ParentInvocationID,InvocationID三者的关系与使用场景
- `RequestID string`：用于标识区分同一session会话下的多次用户交互请求，可由runner.Run通过agent.WithRequestID绑定业务层自己的请求ID。
- `ParentInvocationID string`：用于关联父级执行上下文，可通过此ID关联到父级执行中的相关事件
- `InvocationID string`：当前执行上下文ID。可通过此ID关联同一个执行上下文中的相关事件

可通过以上三个ID，将事件流按照层级结构组织，如下：
- requestID-1:
  - invocationID-1:
    - invocationID-2
    - invocationID-3
  - invocationID-1
  - invocationID-4
  - invocationID-5
- requestID-2:
  - invocationID-6
    - invocationID-7
  - invocationID-8
  - invocationID-9
