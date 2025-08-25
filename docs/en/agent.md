## Agent User Guide

`Agent` is the core execution unit of the tRPC-Agent-Go framework. It handles user input and produces responses. Every `Agent` implements a unified interface and supports streaming output and callback mechanisms.

The framework provides multiple `Agent` types, including `LLMAgent`, `ChainAgent`, `ParallelAgent`, `CycleAgent`, and `GraphAgent`. This document focuses on `LLMAgent`. For other `Agent` types and multi-agent systems, see [multiagent](./multiagent.md).

### Quick Start

This example uses OpenAI's `GPT-4o-mini` model. Before you begin, make sure you have an `OPENAI_API_KEY` and export it via an environment variable:

```shell
export OPENAI_API_KEY="your_api_key"
```

In addition, the framework supports OpenAI-compatible APIs, configurable via environment variables:

```shell
export OPENAI_BASE_URL="your_api_base_url"
export OPENAI_API_KEY="your_api_key"
```

#### Create a Model Instance

First, create a model instance using OpenAI's `GPT-4o-mini`:

```go
import "trpc.group/trpc-go/trpc-agent-go/model/openai"

modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
flag.Parse()
// Create an OpenAI model instance.
modelInstance := openai.New(*modelName, openai.Options{})
```

#### Configure Generation Parameters

Set generation parameters including max tokens, temperature, and whether to use streaming output:

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

maxTokens := 1000
temperature := 0.7
genConfig := model.GenerationConfig{
    MaxTokens:   &maxTokens,   // Maximum number of tokens to generate.
    Temperature: &temperature, // Temperature controls randomness.
    Stream:      true,         // Enable streaming output.
}
```

#### Create an LLMAgent

Create an `LLMAgent` with the model instance and configuration. Also set the `Agent`'s Description and Instruction.

`Description` describes the `Agent`'s basic functionality and characteristics, while `Instruction` defines the specific guidelines and behavioral rules the `Agent` should follow during execution.

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"

llmAgent := llmagent.New(
    "demo-agent",                      // Agent name.
    llmagent.WithModel(modelInstance), // Set model.
    llmagent.WithDescription("A helpful AI assistant for demonstrations"),              // Set description.
    llmagent.WithInstruction("Be helpful, concise, and informative in your responses"), // Set instruction.
    llmagent.WithGenerationConfig(genConfig),                                           // Set generation params.
)
```

#### Create an Invocation Context

Create an `Invocation` object that contains everything required for the execution:

```go
import "trpc.group/trpc-go/trpc-agent-go/agent"

invocation := &agent.Invocation{
    AgentName:     "demo-agent",                                                   // Agent name.
    InvocationID:  "demo-invocation-001",                                          // Invocation ID.
    EndInvocation: false,                                                          // Whether to end invocation.
    Model:         modelInstance,                                                  // Model used.
    Message:       model.NewUserMessage("Hello! Can you tell me about yourself?"), // User message.
    Session:       &session.Session{ID: "session-001"},
}
```

#### Run the Agent

Call the `Agent.Run` method to start execution:

```go
import "context"

ctx := context.Background()
eventChan, err := llmAgent.Run(ctx, invocation)
if err != nil {
    log.Fatalf("failed to run Agent: %v", err)
}
```

#### Consume the Event Stream

Receive execution results via the event channel:

```go
// Handle events.
for event := range eventChan {
    // Check for errors.
    if event.Error != nil {
        log.Printf("err: %s", event.Error.Message)
        continue
    }
    // Handle content.
    if len(event.Choices) > 0 {
        choice := event.Choices[0]
        if choice.Delta.Content != "" {
            // Streaming output.
            fmt.Print(choice.Delta.Content)
        }
    }
    // Check completion.
    if event.Done {
        break
    }
}
```

You can find the complete example at [examples/llmagent](http://github.com/trpc-group/trpc-agent-go/tree/main/examples/llmagent).

## Core Concepts

### Invocation

`Invocation` is the context object for the `Agent` execution flow. It includes all information required for a single run:

```go
// Invocation is the context object for the Agent execution flow. It contains all information required for a single run.
type Invocation struct {
    // Agent specifies the Agent instance to invoke.
    Agent Agent
    // AgentName identifies the Agent instance to invoke.
    AgentName string
    // InvocationID provides a unique identifier for each invocation.
    InvocationID string
    // Branch is a branch identifier used for hierarchical event filtering.
    Branch string
    // EndInvocation indicates whether to end the invocation.
    EndInvocation bool
    // Session maintains the conversational context state.
    Session *session.Session
    // Model specifies the model instance to use.
    Model model.Model
    // Message is the user content sent to the Agent.
    Message model.Message
    // EventCompletionCh signals when events are written to the session.
    EventCompletionCh <-chan string
    // RunOptions contains options for the Run method.
    RunOptions RunOptions
    // TransferInfo supports control transfer between Agents.
    TransferInfo *TransferInfo
    // AgentCallbacks allows injecting custom logic at different stages of Agent execution.
    AgentCallbacks *AgentCallbacks
    // ModelCallbacks allows injecting custom logic at different stages of model invocation.
    ModelCallbacks *model.ModelCallbacks
    // ToolCallbacks allows injecting custom logic at different stages of tool execution.
    ToolCallbacks *tool.ToolCallbacks
}
```

### Event

`Event` represents real-time feedback generated during `Agent` execution. It reports progress through an event stream.

Main event types include:

- Model conversation events
- Tool call and response events
- Agent transfer events
- Error events

```go
// Event represents real-time feedback generated during Agent execution and reports progress through an event stream.
type Event struct {
    // Response contains model response content, tool call results, and statistics.
    *model.Response
    // InvocationID associates the event to a specific invocation.
    InvocationID string `json:"invocationId"`
    // Author identifies the source of the event, such as Agent or tool.
    Author string `json:"author"`
    // ID is the unique identifier of the event.
    ID string `json:"id"`
    // Timestamp records when the event occurred.
    Timestamp time.Time `json:"timestamp"`
    // Branch is a branch identifier used for hierarchical event filtering.
    Branch string `json:"branch,omitempty"`
    // RequiresCompletion indicates whether this event requires a completion signal.
    RequiresCompletion bool `json:"requiresCompletion,omitempty"`
    // CompletionID is used to complete this event.
    CompletionID string `json:"completionId,omitempty"`
    // LongRunningToolIDs contains IDs of long-running function calls so clients can track them.
    // Only valid for function-call events.
    LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`
}
```

The streaming nature of `Event` lets you observe the `Agent`'s work in real time, making the interaction feel natural—like a conversation. Simply iterate over the event stream, check the content and status of each event, and you can fully process the `Agent`'s execution results.

### Agent Interface

The `Agent` interface defines the core behaviors that all `Agents` must implement. It provides a unified way to use different `Agent` types and supports tool calls and sub-Agent management.

```go
type Agent interface {
    // Run accepts the execution context and invocation information and returns an event channel.
    // You can receive the Agent's progress and results in real time via this channel.
    Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)
    // Tools returns the list of tools accessible to the Agent.
    Tools() []tool.Tool
    // Info returns the Agent's basic information, including name and description.
    Info() Info
    // SubAgents returns the list of available sub-Agents.
    // SubAgents and FindSubAgent support cooperation between Agents,
    // enabling an Agent to delegate tasks to others and build complex multi-agent systems.
    SubAgents() []Agent
    // FindSubAgent looks up a sub-Agent by name.
    FindSubAgent(name string) Agent
}
```

## Callbacks

Callbacks provide hooks at key stages of `Agent` execution so you can inject custom logic.

### Callback Types

The framework provides three kinds of callbacks:

**Agent Callbacks**: Triggered before and after Agent execution

```go
type AgentCallbacks struct {
    BeforeAgent []BeforeAgentCallback  // Callback before Agent runs.
    AfterAgent  []AfterAgentCallback   // Callback after Agent finishes.
}
```

**Model Callbacks**: Triggered before and after model invocation

```go
type ModelCallbacks struct {
    BeforeModel []BeforeModelCallback  // Callback before model call.
    AfterModel  []AfterModelCallback   // Callback after model call.
}
```

**Tool Callbacks**: Triggered before and after tool invocation

```go
type ToolCallbacks struct {
    BeforeTool []BeforeToolCallback  // Callback before tool call.
    AfterTool  []AfterToolCallback   // Callback after tool call.
}
```

### Usage Example

```go
// Create Agent callbacks.
callbacks := &agent.AgentCallbacks{
    BeforeAgent: []agent.BeforeAgentCallback{
        func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
            log.Printf("Agent %s is about to run", invocation.AgentName)
            return nil, nil
        },
    },
    AfterAgent: []agent.AfterAgentCallback{
        func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
            if runErr != nil {
                log.Printf("Agent %s failed: %v", invocation.AgentName, runErr)
            } else {
                log.Printf("Agent %s finished successfully", invocation.AgentName)
            }
            return nil, nil
        },
    },
}

// Use callbacks in Invocation.
invocation := &agent.Invocation{
    AgentName:      "demo-agent",
    InvocationID:   "demo-001",
    AgentCallbacks: callbacks,
    Model:          modelInstance,
    Message:        model.NewUserMessage("User input"),
    Session: &session.Session{
        ID: "session-001",
    },
}
```

Callbacks let you precisely control the `Agent`'s lifecycle and implement more sophisticated business logic.

## Advanced Usage

The framework also provides advanced capabilities such as `Runner`, `Session`, and `Memory` for constructing more complex `Agent` systems.

- `Runner` is the `Agent` executor that orchestrates the `Agent` execution flow and connects to `Session` or `Memory` services.
- `Session` Service manages conversational state, including history and context.
- `Memory` Service stores user preference information to enable personalization.

For details, see [runner](runner.md), [session](session.md), and [memory](memory.md).
