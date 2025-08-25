# Event Usage Documentation

`Event` is the core communication mechanism between `Agent` and users in tRPC-Agent-Go. It acts like a message envelope, carrying `Agent` response content, tool call results, error information, and more. Through `Event`, you can understand `Agent`'s working status in real-time, handle streaming responses, implement multi-agent collaboration, and track tool execution.

## Event Overview

`Event` is the carrier for communication between `Agent` and users.

Users obtain event streams through the `runner.Run()` method, then listen to event channels to process `Agent` responses.

### Event Structure

`Event` represents an event between `Agent` and users, with the following structure definition:

```go
type Event struct {
    // Response is the base response structure of Event, carrying LLM responses.
    *model.Response

    // InvocationID is the unique identifier for this call.
    InvocationID string `json:"invocationId"`

    // Author is the event initiator.
    Author string `json:"author"`

    // ID is the unique identifier for the event.
    ID string `json:"id"`

    // Timestamp is the event timestamp.
    Timestamp time.Time `json:"timestamp"`

    // Branch is the branch identifier for multi-agent collaboration.
    Branch string `json:"branch,omitempty"`

    // RequiresCompletion indicates whether this event requires a completion signal.
    RequiresCompletion bool `json:"requiresCompletion,omitempty"`

    // CompletionID is used for the completion signal of this event.
    CompletionID string `json:"completionId,omitempty"`

    // LongRunningToolIDs is a collection of IDs for long-running function calls.
    // Agent clients will understand which function calls are long-running from this field.
    // Only valid for function call events.
    LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`
}
```

`model.Response` is the base response structure of `Event`, carrying LLM responses, tool calls, errors, and other information. It is defined as follows:

```go
type Response struct {
    // Response unique identifier.
    ID string `json:"id"`
    
    // Object type (such as "chat.completion", "error", etc.), helping clients identify processing methods.
    Object string `json:"object"`
    
    // Creation timestamp.
    Created int64 `json:"created"`
    
    // Model name used.
    Model string `json:"model"`
    
    // Response choices, LLM may generate multiple candidate responses for user selection, default is only 1.
    Choices []Choice `json:"choices"`
    
    // Usage statistics, recording token usage.
    Usage *Usage `json:"usage,omitempty"`
    
    // System fingerprint.
    SystemFingerprint *string `json:"system_fingerprint,omitempty"`
    
    // Error information.
    Error *ResponseError `json:"error,omitempty"`
    
    // Timestamp.
    Timestamp time.Time `json:"timestamp"`
    
    // Indicates whether the entire conversation is complete.
    Done bool `json:"done"`
    
    // Whether it's a partial response.
    IsPartial bool `json:"is_partial"`
}

type Choice struct {
    // Choice index.
    Index int `json:"index"`
    
    // Complete message, containing the entire response.
    Message Message `json:"message,omitempty"`
    
    // Incremental message, used for streaming responses, only containing new content of the current chunk.
    // For example: complete response "Hello, how can I help you?" in streaming response:
    // First event: Delta.Content = "Hello"
    // Second event: Delta.Content = ", how"  
    // Third event: Delta.Content = " can I help you?"
    Delta Message `json:"delta,omitempty"`
    
    // Completion reason.
    FinishReason *string `json:"finish_reason,omitempty"`
}

type Message struct {
    // Role of the message initiator, such as "system", "user", "assistant", "tool".
    Role string `json:"role"`

    // Message content.
    Content string `json:"content"`

    // Content parts for multimodal messages.
    ContentParts []ContentPart `json:"content_parts,omitempty"`

    // ID of the tool used by the tool response.
    ToolID string `json:"tool_id,omitempty"`

    // Name of the tool used by the tool response.
    ToolName string `json:"tool_name,omitempty"`

    // Optional tool calls.
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Usage struct {
    // Number of tokens used in prompts.
    PromptTokens int `json:"prompt_tokens"`

    // Number of tokens used in completion.
    CompletionTokens int `json:"completion_tokens"`

    // Total number of tokens used in the response.
    TotalTokens int `json:"total_tokens"`
}
```

### Event Types

`Event` is created and sent in the following scenarios:

1. **User message events**: Automatically created when users send messages
2. **`Agent` response events**: Created when `Agent` generates responses
3. **Streaming response events**: Created for each response chunk in streaming mode
4. **Tool call events**: Created when `Agent` calls tools
5. **Error events**: Created when errors occur
6. **`Agent` transfer events**: Created when `Agent` transfers to other agents
7. **Completion events**: Created when Agent execution completes

Based on the `model.Response.Object` field, `Event` can be divided into the following types:

```go
const (
    // Error events.
    ObjectTypeError = "error"
    
    // Tool response events.
    ObjectTypeToolResponse = "tool.response"
    
    // Preprocessing events.
    ObjectTypePreprocessingBasic = "preprocessing.basic"
    ObjectTypePreprocessingContent = "preprocessing.content"
    ObjectTypePreprocessingIdentity = "preprocessing.identity"
    ObjectTypePreprocessingInstruction = "preprocessing.instruction"
    ObjectTypePreprocessingPlanning = "preprocessing.planning"
    
    // Postprocessing events.
    ObjectTypePostprocessingPlanning = "postprocessing.planning"
    ObjectTypePostprocessingCodeExecution = "postprocessing.code_execution"
    
    // Agent transfer events.
    ObjectTypeTransfer = "agent.transfer"
    
    // Runner completion events.
    ObjectTypeRunnerCompletion = "runner.completion"
)
```

### Event Creation

When developing custom `Agent` types or `Processor`, you need to create `Event`.

`Event` provides three creation methods, suitable for different scenarios.

```go
// Create new event.
func New(invocationID, author string, opts ...Option) *Event

// Create error event.
func NewErrorEvent(invocationID, author, errorType, errorMessage string) *Event

// Create event from response.
func NewResponseEvent(invocationID, author string, response *model.Response) *Event
```

**Parameter description:**

- `invocationID string`: Call unique identifier
- `author string`: Event initiator
- `opts ...Option`: Optional configuration options (only for `New` method)
- `errorType string`: Error type (only for `NewErrorEvent` method)
- `errorMessage string`: Error message (only for `NewErrorEvent` method)
- `response *model.Response`: Response object (only for `NewResponseEvent` method)

The framework supports the following `Option` for configuring `Event`:

- `WithBranch(branch string)`: Set the branch identifier for the event
- `WithResponse(response *model.Response)`: Set the response content for the event
- `WithObject(o string)`: Set the type for the event

**Examples:**
```go
// Create basic event.
evt := event.New("invoke-123", "agent")

// Create event with branch.
evt := event.New("invoke-123", "agent", event.WithBranch("main"))

// Create error event.
evt := event.NewErrorEvent("invoke-123", "agent", "api_error", "Request timeout")

// Create event from response.
response := &model.Response{
    Object: "chat.completion",
    Done:   true,
    Choices: []model.Choice{{Message: model.Message{Role: "assistant", Content: "Hello!"}}},
}
evt := event.NewResponseEvent("invoke-123", "agent", response)
```

### Event Methods

`Event` provides the `Clone` method for creating deep copies of `Event`.

```go
func (e *Event) Clone() *Event
```

## Event Usage Examples

This example demonstrates how to use `Event` in practical applications to handle `Agent` streaming responses, tool calls, and error handling.

### Core Process

1. **Send user message**: Start `Agent` processing through `runner.Run()`
2. **Receive event stream**: Process events returned by `Agent` in real-time
3. **Handle different event types**: Distinguish streaming content, tool calls, errors, etc.
4. **Visualize output**: Provide user-friendly interactive experience

### Code Examples

```go
// processMessage handles single message interaction.
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
    message := model.NewUserMessage(userMessage)

    // Run agent through runner.
eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
if err != nil {
    return fmt.Errorf("failed to run agent: %w", err)
}

// Process response.
return c.processResponse(eventChan)
}

// processResponse handles response, including streaming response and tool call visualization.
func (c *multiTurnChat) processResponse(eventChan <-chan *event.Event) error {
    fmt.Print("🤖 Assistant: ")

    var (
        fullContent       string        // Accumulated complete content.
        toolCallsDetected bool          // Whether tool calls are detected.
        assistantStarted  bool          // Whether Assistant has started replying.
    )

    for event := range eventChan {
        // Handle single event.
if err := c.handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
    return err
}

// Check if it's the final event.
if event.Done && !c.isToolEvent(event) {
    fmt.Printf("\n")
    break
}
    }

    return nil
}

// handleEvent handles single event.
func (c *multiTurnChat) handleEvent(
    event *event.Event,
    toolCallsDetected *bool,
    assistantStarted *bool,
    fullContent *string,
) error {
    // 1. Handle error events.
if event.Error != nil {
    fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
    return nil
}

// 2. Handle tool calls.
if c.handleToolCalls(event, toolCallsDetected, assistantStarted) {
    return nil
}

// 3. Handle tool responses.
if c.handleToolResponses(event) {
    return nil
}

// 4. Handle content.
c.handleContent(event, toolCallsDetected, assistantStarted, fullContent)

    return nil
}

// handleToolCalls detects and displays tool calls.
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

// handleToolResponses detects and displays tool responses.
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

// handleContent processes and displays content.
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

// extractContent extracts content based on streaming mode.
func (c *multiTurnChat) extractContent(choice model.Choice) string {
    if c.streaming {
        // Streaming mode: use incremental content.
return choice.Delta.Content
}
// Non-streaming mode: use complete message content.
return choice.Message.Content
}

// displayContent prints content to console.
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

// isToolEvent checks if the event is a tool response.
func (c *multiTurnChat) isToolEvent(event *event.Event) bool {
    if event.Response == nil {
        return false
    }
    
    // Check if there are tool calls.
if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
    return true
}

// Check if there's a tool ID.
if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
    return true
}

// Check if it's a tool role.
for _, choice := range event.Response.Choices {
    if choice.Message.Role == model.RoleTool {
        return true
    }
}

    return false
}
```
