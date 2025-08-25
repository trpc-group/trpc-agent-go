# Event 使用文档

`Event` 是 tRPC-Agent-Go 中 `Agent` 与用户之间通信的核心机制。它就像一个消息信封，承载着 `Agent` 的响应内容、工具调用结果、错误信息等。通过 `Event`，你可以实时了解 `Agent` 的工作状态，处理流式响应，实现多 `Agent` 协作，以及追踪工具执行。

## Event 概述

`Event` 是 `Agent` 与用户之间通信的载体。

用户通过 `runner.Run()` 方法获取事件流，然后监听事件通道来处理 `Agent` 的响应。

### Event 结构

`Event` 表示 `Agent` 与用户之间的一次事件，结构定义如下：

```go
type Event struct {
    // Response 是 Event 的基础响应结构，承载 LLM 的响应
    *model.Response

    // InvocationID 是本次调用的唯一标识
    InvocationID string `json:"invocationId"`

    // Author 是事件的发起者
    Author string `json:"author"`

    // ID 是事件的唯一标识符
    ID string `json:"id"`

    // Timestamp 是事件的时间戳
    Timestamp time.Time `json:"timestamp"`

    // Branch 是分支标识符，用于多 Agent 协作
    Branch string `json:"branch,omitempty"`

    // RequiresCompletion 表示此事件是否需要完成信号
    RequiresCompletion bool `json:"requiresCompletion,omitempty"`

    // CompletionID 用于此事件的完成信号
    CompletionID string `json:"completionId,omitempty"`

    // LongRunningToolIDs 是长运行函数调用的 ID 集合
    // Agent 客户端将从此字段了解哪些函数调用是长时间运行的
    // 仅对函数调用事件有效
    LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`
}
```

`model.Response` 是 `Event` 的基础响应结构，承载了 LLM 的响应、工具调用以及错误等信息，定义如下：

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
    
    // 表示整个对话是否完成
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
}
```

### Event 类型

`Event` 在以下场景中会被创建和发送：

1. **用户消息事件**：用户发送消息时自动创建
2. **`Agent` 响应事件**：`Agent` 生成响应时创建
3. **流式响应事件**：流式模式下每个响应块都会创建
4. **工具调用事件**：`Agent` 调用工具时创建
5. **错误事件**：发生错误时创建
6. **`Agent` 转移事件**：`Agent` 转移给其他 `Agent` 时创建
7. **完成事件**：Agent 执行完成时创建

根据 `model.Response.Object` 字段，`Event` 可以分为以下类型：

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

### Event 创建

在开发自定义 `Agent` 类型或 `Processor` 时，需要创建 `Event`。

`Event` 提供了三种创建方法，适用于不同场景。

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
- `opts ...Option`：可选的配置选项（仅 `New` 方法）
- `errorType string`：错误类型（仅 `NewErrorEvent` 方法）
- `errorMessage string`：错误消息（仅 `NewErrorEvent` 方法）
- `response *model.Response`：响应对象（仅 `NewResponseEvent` 方法）

框架支持以下 `Option` 用以配置 `Event`：

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

### Event 方法

`Event` 提供了 `Clone` 方法，用于创建 `Event` 的深拷贝。

```go
func (e *Event) Clone() *Event
```

## Event 使用示例

这个示例展示了如何在实际应用中使用 `Event` 处理 `Agent` 的流式响应、工具调用和错误处理。

### 核心流程

1. **发送用户消息**：通过 `runner.Run()` 启动 `Agent` 处理
2. **接收事件流**：实时处理 `Agent` 返回的事件
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

        // 检查是否为最终事件
        if event.Done && !c.isToolEvent(event) {
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
    if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
        *toolCallsDetected = true
        if *assistantStarted {
            fmt.Printf("\n")
        }
        fmt.Printf("🔧 Tool calls initiated:\n")
        for _, toolCall := range event.Choices[0].Message.ToolCalls {
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
    if len(event.Choices) > 0 {
        choice := event.Choices[0]
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

// isToolEvent 检查事件是否为工具响应
func (c *multiTurnChat) isToolEvent(event *event.Event) bool {
    if event.Response == nil {
        return false
    }
    
    // 检查是否有工具调用
    if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
        return true
    }
    
    // 检查是否有工具 ID
    if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
        return true
    }

    // 检查是否为工具角色
    for _, choice := range event.Response.Choices {
        if choice.Message.Role == model.RoleTool {
            return true
        }
    }

    return false
}
```
