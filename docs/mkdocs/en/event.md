# Event Usage Documentation

Event is the core communication mechanism between Agent and users in trpc-agent-go. It's like a message envelope that carries Agent response content, tool call results, error information, etc. Through Event, you can understand Agent's working status in real-time, handle streaming responses, implement multi-Agent collaboration, and track tool execution.

## Event Overview

Event is the carrier for communication between Agent and users.

Users obtain event streams through the `runner.Run()` method, then listen to event channels to handle Agent responses.

### Event Structure

`Event` represents an event between Agent and users, with the following structure definition:

```go
type Event struct {
    // Response is the basic response structure of Event, carrying LLM responses.
    *model.Response
    // RequestID The unique identifier for this request.
    // It can be passed via runner.Run using agent.WithRequestID.
	RequestID string `json:"requestID,omitempty"`

	// ParentInvocationID is the parent invocation ID of the event.
	ParentInvocationID string `json:"parentInvocationId,omitempty"`

    // InvocationID is current invocation ID of the event.
    InvocationID string `json:"invocationId"`

    // Author is the initiator of the event.
    Author string `json:"author"`

    // ID is the unique identifier of the event.
    ID string `json:"id"`

    // Timestamp is the timestamp of the event.
    Timestamp time.Time `json:"timestamp"`

    // Branch is a branch identifier for multi-Agent collaboration.
    Branch string `json:"branch,omitempty"`

    // RequiresCompletion indicates whether this event requires a completion signal.
    RequiresCompletion bool `json:"requiresCompletion,omitempty"`

    // LongRunningToolIDs is a set of IDs for long-running function calls.
    // Agent clients will understand which function calls are long-running from this field.
    // Only valid for function call events.
    LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`

    // StateDelta contains state changes to be written to the session.
    StateDelta map[string][]byte `json:"stateDelta,omitempty"`

    // StructuredOutput carries a typed, in-memory structured payload (not serialized).
    StructuredOutput any `json:"-"`

    // Actions carry flow-level hints (e.g., skip post-tool summarization).
    Actions *EventActions `json:"actions,omitempty"`
}

// EventActions provides optional behavior hints attached to the event.
type EventActions struct {
    // SkipSummarization indicates the flow should not run a summarization LLM call
    // after a tool.response event.
    SkipSummarization bool `json:"skipSummarization,omitempty"`
}
```

`model.Response` is the basic response structure of Event, carrying LLM responses, tool calls, and error information, defined as follows:

```go
type Response struct {
    // Response unique identifier.
    ID string `json:"id"`
    
    // Object type (such as "chat.completion", "error", etc.), helps clients identify processing methods.
    Object string `json:"object"`
    
    // Creation timestamp.
    Created int64 `json:"created"`
    
    // Model name used.
    Model string `json:"model"`
    
    // Response options, LLM may generate multiple candidate responses for user selection, default is 1.
    Choices []Choice `json:"choices"`
    
    // Usage statistics, records token usage.
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
    
    // Complete message, contains the entire response.
    Message Message `json:"message,omitempty"`
    
    // Incremental message, used for streaming responses, only contains new content of current chunk.
    // For example: complete response "Hello, how can I help you?" in streaming response:
    // First event: Delta.Content = "Hello"
    // Second event: Delta.Content = ", how"  
    // Third event: Delta.Content = " can I help you?"
    Delta Message `json:"delta,omitempty"`
    
    // Completion reason.
    FinishReason *string `json:"finish_reason,omitempty"`
}

type Message struct {
    // Role of message initiator, such as "system", "user", "assistant", "tool".
    Role string `json:"role"`

    // Message content.
    Content string `json:"content"`

    // Content fragments for multimodal messages.
    ContentParts []ContentPart `json:"content_parts,omitempty"`

    // ID of the tool used by tool response.
    ToolID string `json:"tool_id,omitempty"`

    // Name of the tool used by tool response.
    ToolName string `json:"tool_name,omitempty"`

    // Optional tool calls.
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type Usage struct {
    // Number of tokens used in prompts.
    PromptTokens int `json:"prompt_tokens"`

    // Number of tokens used in completion.
    CompletionTokens int `json:"completion_tokens"`

    // Total number of tokens used in response.
    TotalTokens int `json:"total_tokens"`

    // Timing statistics (optional).
    TimingInfo *TimingInfo `json:"timing_info,omitempty"`
}

type TimingInfo struct {
    // FirstTokenDuration is the accumulated duration from request start to the first meaningful token.
    // A "meaningful token" is defined as the first chunk containing reasoning content, regular content, or tool calls.
    //
    // Return timing:
    // - Streaming requests: Calculated and returned immediately when the first meaningful chunk is received
    // - Non-streaming requests: Calculated and returned when the complete response is received
    FirstTokenDuration time.Duration `json:"time_to_first_token,omitempty"`

    // ReasoningDuration is the accumulated duration of reasoning phases (streaming mode only).
    // Measured from the first reasoning chunk to the last reasoning chunk in each LLM call.
    //
    // Measurement details:
    // - Starts timing when the first chunk with reasoning content is received
    // - Continues timing for all subsequent reasoning chunks
    // - Stops timing when the first non-reasoning chunk (regular content or tool call) is received
    //
    // Return timing:
    // - Streaming requests: Calculated and returned immediately when reasoning ends (i.e., when the first
    //   non-reasoning content/tool call chunk is received)
    // - Non-streaming requests: Cannot be measured precisely, this field will remain 0
    ReasoningDuration time.Duration `json:"reasoning_duration,omitempty"`
}
```

### Event Types

Events are created and sent in the following scenarios:

1. **User Message Events**: Automatically created when users send messages
2. **Agent Response Events**: Created when Agent generates responses
3. **Streaming Response Events**: Created for each response chunk in streaming mode
4. **Tool Call Events**: Created when Agent calls tools
5. **Error Events**: Created when errors occur
6. **Agent Transfer Events**: Created when Agent transfers to other Agents
7. **Completion Events**: Created when Agent execution completes

Based on the `model.Response.Object` field, Events can be divided into the following types:

```go
const (
    // Error event.
    ObjectTypeError = "error"
    
    // Tool response event.
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
    
    // Agent transfer event.
    ObjectTypeTransfer = "agent.transfer"
    
    // Runner completion event.
    ObjectTypeRunnerCompletion = "runner.completion"
)
```

### Filtering Transfer Announcements

Transfer announcements (Agent delegation notices) are emitted as Events with `Response.Object == "agent.transfer"`.

This typically appears as a handoff notice: "Transferring control to agent: <name>".

 If your UI should not display these system-level notices, you have two compatible strategies:
 - Filter by `Object`: hide events where `Response.Object == "agent.transfer"`.
 - Filter by `Tag`: hide events whose `Event.Tag` contains the `transfer` tag. The framework adds this tag to delegation-related events (including transfer tool results), so filtering by tag avoids breaking ToolCall/ToolResult alignment.

 Tags are appended using a semicolon delimiter (`;`). Use `event.WithTag(tag)` when creating custom events; multiple tags are stored as `tag1;tag2;...`.

### Code Execution Event Tags

For code execution related events, use `Event.Tag` to distinguish between code and execution results:

- **Code Execution Event**: `Response.Object == "postprocessing.code_execution"` and `Event.ContainsTag(event.TagCodeExecution)`
- **Execution Result Event**: `Response.Object == "postprocessing.code_execution"` and `Event.ContainsTag(event.TagCodeExecutionResult)`

The related constants are defined in the `trpc.group/trpc-go/trpc-agent-go/event` package.

#### Helper: Detect Runner Completion

Use the convenience method to detect when the whole run has finished regardless of Agent type:

```go
// e.IsRunnerCompletion() returns true for the terminal runner-completion event.
if e.IsRunnerCompletion() {
    // Safe point to stop reading the channel
}
```

### Event Creation

When developing custom Agent types or Processors, you need to create Events.

Event provides three creation methods, suitable for different scenarios. Prefer these helpers instead of constructing `&event.Event{}` directly.

```go
// Create new event.
func New(invocationID, author string, opts ...Option) *Event

// Create error event.
func NewErrorEvent(invocationID, author, errorType, errorMessage string) *Event

// Create event from response.
func NewResponseEvent(invocationID, author string, response *model.Response) *Event
```

**Parameter Description:**

- `invocationID string`: Invocation unique identifier
- `author string`: Event initiator
- `opts ...Option`: Optional configuration options (New method only)
- `errorType string`: Error type (NewErrorEvent method only)
- `errorMessage string`: Error message (NewErrorEvent method only)
- `response *model.Response`: Response object (NewResponseEvent method only)

The framework supports the following Options for configuring Event:

- `WithBranch(branch string)`: Set event branch identifier
- `WithResponse(response *model.Response)`: Set event response content
- `WithObject(o string)`: Set event type

**Example:**
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


### Tool Response Streaming (including AgentTool forwarding)

When a Streamable tool is invoked (including AgentTool), the framework emits `tool.response` events. In streaming mode:

- Each partial chunk appears in `choice.Delta.Content`, `Done=false`, `IsPartial=true`.
- Final tool messages arrive with `choice.Message.Role=tool` and `choice.Message.Content`.

When AgentTool enables `WithStreamInner(true)`, it also forwards the child Agent’s events inline to the parent flow:

- Forwarded child events are standard `event.Event` items; incremental text appears in `choice.Delta.Content`.
- To avoid duplicate display, the child’s final full message is not forwarded; it is aggregated into the final `tool.response` content so the next LLM turn has tool messages as required by some providers.

Runner automatically sends completion signals for events requiring them (`RequiresCompletion=true`), so manual handling is not needed.

Example handling in an event loop:

```go
if evt.Response != nil && evt.Object == model.ObjectTypeToolResponse && len(evt.Response.Choices) > 0 {
    for _, ch := range evt.Response.Choices {
        if ch.Delta.Content != "" { // partial
            fmt.Print(ch.Delta.Content)
            continue
        }
        if ch.Message.Role == model.RoleTool && ch.Message.Content != "" { // final
            fmt.Println(strings.TrimSpace(ch.Message.Content))
        }
    }
    // Continue to next event; don't treat as assistant content
    continue
}
```

Tip: For custom events, always use `event.New(...)` with `WithResponse`, `WithBranch`, etc., to ensure IDs and timestamps are set consistently.

### Tags

Events support simple tagging via `Event.Tag` to annotate business labels for filtering and analytics:

- Delimiter: `;` (semicolon). Multiple tags concatenate as `tag1;tag2`.
- Helper: `event.WithTag("<tag>")` to append a tag without losing existing ones.
- Built-in usage: delegation-related events are tagged with `transfer`. UIs can hide these internal messages while preserving the complete event stream for debugging and processing.

### Event Methods

Event provides the `Clone` method for creating deep copies of Events.

```go
func (e *Event) Clone() *Event
```

## Event Usage Examples

This example demonstrates how to use Event in real applications to handle Agent streaming responses, tool calls, and error handling.

### Core Flow

1. **Send User Message**: Start Agent processing through `runner.Run()`
2. **Receive Event Stream**: Handle events returned by Agent in real-time
3. **Handle Different Event Types**: Distinguish streaming content, tool calls, errors, etc.
4. **Visual Output**: Provide user-friendly interactive experience

### Code Example

```go
// processMessage handles single message interaction.
func (c *multiTurnChat) processMessage(ctx context.Context, userMessage string) error {
    message := model.NewUserMessage(userMessage)

    // Run agent through runner.
    eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
    if err != nil {
        return fmt.Errorf("failed to run agent: %w", err)
    }

    // Handle response.
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
        if event.IsFinalResponse() {
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

// handleContent handles and displays content.
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
```

### Relationship and Usage Scenarios of RequestID, ParentInvocationID, and InvocationID
- `RequestID string`​​: Used to identify and distinguish multiple user interaction requests within the same session. It can be bound to the business layer's own request ID via runner.Runu agent.WithRequestID. This ensures unique identification for each request cycle, similar to how request IDs are employed to guarantee idempotency and de-duplication in API interactions.
- `​​ParentInvocationID string`​​: Used to associate the parent execution context. This ID can link to related events in the parent execution, enabling hierarchical tracking of nested operations. This mirrors concepts where a parent request ID groups multiple sub-requests, each with distinct identifiers but shared parent context for cohesive management.
- `​​InvocationID string`​​: The current execution context ID. This ID associates related events within the same execution context, allowing precise correlation of actions and outcomes for a specific invocation. It functions similarly to child request IDs in systems where individual operations are tracked under a parent scope.

Using these three IDs, the event flow can be organized in a hierarchical structure as follows:
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
