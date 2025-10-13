# Agent Usage Documentation

Agent is the core execution unit of the tRPC-Agent-Go framework, responsible for processing user input and generating corresponding responses. Each Agent implements a unified interface, supporting streaming output and callback mechanisms.

The framework provides multiple types of Agents, including LLMAgent, ChainAgent, ParallelAgent, CycleAgent, and GraphAgent. This document focuses on LLMAgent. For detailed information about other Agent types and multi-Agent systems, please refer to [Multi-Agent](./multiagent.md).

## Quick Start

**Recommended Usage: Runner**

We strongly recommend using Runner to execute Agents instead of directly calling Agent interfaces. Runner provides a more user-friendly interface, integrating services like Session and Memory, making usage much simpler.

**📖 Learn More:** For detailed usage methods, please refer to [Runner](./runner.md)

This example uses OpenAI's GPT-4o-mini model. Before starting, please ensure you have prepared the corresponding `OPENAI_API_KEY` and exported it through environment variables:

```shell
export OPENAI_API_KEY="your_api_key"
```

Additionally, the framework supports OpenAI API-compatible models, which can be configured through environment variables:

```shell
export OPENAI_BASE_URL="your_api_base_url"
export OPENAI_API_KEY="your_api_key"
```

### Creating Model Instance

First, you need to create a model instance. Here we use OpenAI's GPT-4o-mini model:

```go
import "trpc.group/trpc-go/trpc-agent-go/model/openai"

modelName := flag.String("model", "gpt-4o-mini", "Name of the model to use")
flag.Parse()
// Create OpenAI model instance.
modelInstance := openai.New(*modelName, openai.Options{})
```

### Configuring Generation Parameters

Set the model's generation parameters, including maximum tokens, temperature, and whether to use streaming output:

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

maxTokens := 1000
temperature := 0.7
genConfig := model.GenerationConfig{
    MaxTokens:   &maxTokens,   // Maximum number of tokens to generate.
    Temperature: &temperature, // Temperature parameter, controls output randomness.
    Stream:      true,         // Enable streaming output.
}
```

### Creating LLMAgent

Use the model instance and configuration to create an LLMAgent, while setting the Agent's Description and Instruction.

Description is used to describe the basic functionality and characteristics of the Agent, while Instruction defines the specific instructions and behavioral guidelines that the Agent should follow when executing tasks.

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"

llmAgent := llmagent.New(
    "demo-agent",                      // Agent name.
    llmagent.WithModel(modelInstance), // Set model.
    llmagent.WithDescription("A helpful AI assistant for demonstrations"),              // Set description.
    llmagent.WithInstruction("Be helpful, concise, and informative in your responses"), // Set instruction.
    llmagent.WithGenerationConfig(genConfig),                                           // Set generation parameters.
)
```

### Placeholder Variables (Session State Injection)

LLMAgent automatically injects session state into `Instruction` and the optional `SystemPrompt` via placeholder variables. Supported patterns:

- `{key}`: Replace with the string value of `session.State["key"]`
- `{key?}`: Optional; if missing, replaced with an empty string
- `{user:subkey}` / `{app:subkey}` / `{temp:subkey}`: Use user/app/temp scoped keys (session services merge app/user state into session with these prefixes)

Notes:

- If a non-optional key is not found, the original `{key}` is preserved (helps the LLM notice missing context)
- Values are read from `invocation.Session.State` (Runner + SessionService set/merge this automatically)

Example:

```go
llm := llmagent.New(
  "research-agent",
  llmagent.WithModel(modelInstance),
  llmagent.WithInstruction(
    "You are a research assistant. Focus: {research_topics}. " +
    "User interests: {user:topics?}. App banner: {app:banner?}.",
  ),
)

// Initialize session state (Runner + SessionService)
_ = sessionService.UpdateUserState(ctx, session.UserKey{AppName: app, UserID: user}, session.StateMap{
  "topics": []byte("quantum computing, cryptography"),
})
_ = sessionService.UpdateAppState(ctx, app, session.StateMap{
  "banner": []byte("Research Mode"),
})
// Unprefixed keys live directly in session.State
_, _ = sessionService.CreateSession(ctx, session.Key{AppName: app, UserID: user, SessionID: sid}, session.StateMap{
  "research_topics": []byte("AI, ML, DL"),
})
```

See also:

- Examples: `examples/placeholder`, `examples/outputkey`
- Session API: `docs/mkdocs/en/session.md`

### Using Runner to Execute Agent

Use Runner to execute the Agent, which is the recommended usage:

```go
import "trpc.group/trpc-go/trpc-agent-go/runner"

// Create Runner.
runner := runner.NewRunner("demo-app", llmAgent)

// Send message directly without creating complex Invocation.
message := model.NewUserMessage("Hello! Can you tell me about yourself?")
eventChan, err := runner.Run(ctx, "user-001", "session-001", message)
if err != nil {
    log.Fatalf("Failed to execute Agent: %v", err)
}
```

### Handling Event Stream

The `eventChan` returned by `runner.Run()` is an event channel. The Agent continuously sends Event objects to this channel during execution.

Each Event contains execution state information at a specific moment: LLM-generated content, tool call requests and results, error messages, etc. By iterating through the event channel, you can get real-time execution progress (see [Event](#event) section below for details).

Receive execution results through the event channel:

```go
// 1. Get event channel (returns immediately, starts async execution)
eventChan, err := runner.Run(ctx, userID, sessionID, message)
if err != nil {
    log.Fatalf("Failed to start: %v", err)
}

// 2. Handle event stream (receive execution results in real-time)
for event := range eventChan {
    // Check for errors
    if event.Error != nil {
        log.Printf("Execution error: %s", event.Error.Message)
        continue
    }

    // Handle response content
    if len(event.Response.Choices) > 0 {
        choice := event.Response.Choices[0]

        // Streaming content (real-time display)
        if choice.Delta.Content != "" {
            fmt.Print(choice.Delta.Content)
        }

        // Tool call information
        for _, toolCall := range choice.Message.ToolCalls {
            fmt.Printf("Calling tool: %s\n", toolCall.Function.Name)
        }
    }

    // Check if completed (note: should not break on tool call completion)
    if event.IsFinalResponse() {
        fmt.Println()
        break
    }
}
```

The complete code for this example can be found at [examples/runner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)

**Why is Runner recommended?**

1. **Simpler Interface**: No need to create complex Invocation objects
2. **Integrated Services**: Automatically integrates Session, Memory and other services
3. **Better Management**: Unified management of Agent execution flow
4. **Production Ready**: Suitable for production environment use

**💡 Tip:** Want to learn more about Runner's detailed usage and advanced features? Please check [Runner](./runner.md)

**Advanced Usage: Direct Agent Usage**

If you need more fine-grained control, you can also use the Agent interface directly, but this requires creating Invocation objects:

## Core Concepts

### Invocation (Advanced Usage)

Invocation is the context object for Agent execution flow, containing all information needed for a single call. **Note: This is advanced usage, we recommend using Runner to simplify operations.**

```go
import "trpc.group/trpc-go/trpc-agent-go/agent"

// Create Invocation object (advanced usage).
invocation := agent.NewInvocation(
    agent.WithAgentName(agent),                                                                  // Agent.
    agent.WithInvocationMessage(model.NewUserMessage("Hello! Can you tell me about yourself?")), // User message.
    agent.WithInvocationSession(&session.Session{ID: "session-001"}),                            // session object.
    agent.WithInvocationEndInvocation(false),                                                    // Whether to end invocation.
    agent.WithInvocationModel(modelInstance),                                                    // Model to use.
)

// Call Agent directly (advanced usage).
ctx := context.Background()
eventChan, err := llmAgent.Run(ctx, invocation)
if err != nil {
    log.Fatalf("Failed to execute Agent: %v", err)
}
```

**When to use direct calls?**

- Need complete control over execution flow
- Custom Session and Memory management
- Implement special invocation logic
- Debugging and testing scenarios

```go
// Invocation is the context object for Agent execution flow, containing all information needed for a single call.
type Invocation struct {
	// Agent specifies the Agent instance to call.
	Agent Agent
	// AgentName identifies the name of the Agent instance to call.
	AgentName string
	// InvocationID provides a unique identifier for each call.
	InvocationID string
	// Branch is a branch identifier for hierarchical event filtering.
	Branch string
	// EndInvocation is a flag indicating whether to end the invocation.
	EndInvocation bool
	// Session maintains the context state of the conversation.
	Session *session.Session
	// Model specifies the model instance to use.
	Model model.Model
	// Message is the specific content sent by the user to the Agent.
	Message model.Message
	// RunOptions are option configurations for the Run method.
	RunOptions RunOptions
	// TransferInfo supports control transfer between Agents.
	TransferInfo *TransferInfo

    // notice
	noticeChanMap map[string]chan any
	noticeMu      *sync.Mutex
}
```

### Event

Event is the real-time feedback generated during Agent execution, reporting execution progress in real-time through Event streams.

Events mainly include the following types:

- Model conversation events
- Tool call and response events
- Agent transfer events
- Error events

```go
// Event is the real-time feedback generated during Agent execution, reporting execution progress in real-time through Event streams.
type Event struct {
	// Response contains model response content, tool call results and statistics.
	*model.Response
	// InvocationID is associated with a specific invocation.
	InvocationID string `json:"invocationId"`
	// Author is the source of the event, such as Agent or tool.
	Author string `json:"author"`
	// ID is the unique identifier of the event.
	ID string `json:"id"`
	// Timestamp records the time when the event occurred.
	Timestamp time.Time `json:"timestamp"`
	// Branch is a branch identifier for hierarchical event filtering.
	Branch string `json:"branch,omitempty"`
	// RequiresCompletion identifies whether this event requires a completion signal.
	RequiresCompletion bool `json:"requiresCompletion,omitempty"`
	// LongRunningToolIDs is a set of IDs for long-running function calls. Agent clients can understand which function calls are long-running through this field, only valid for function call events.
	LongRunningToolIDs map[string]struct{} `json:"longRunningToolIDs,omitempty"`
}
```

The streaming nature of Events allows you to see the Agent's working process in real-time, just like having a natural conversation with a real person. You only need to iterate through the Event stream, check the content and status of each Event, and you can completely handle the Agent's execution results.

### Agent Interface

The Agent interface defines the core behaviors that all Agents must implement. This interface allows you to uniformly use different types of Agents while supporting tool calls and sub-Agent management.

```go
type Agent interface {
    // Run receives execution context and invocation information, returns an event channel. Through this channel, you can receive Agent execution progress and results in real-time.
    Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)
    // Tools returns the list of tools that this Agent can access and execute.
    Tools() []tool.Tool
    // Info method provides basic information about the Agent, including name and description, for easy identification and management.
    Info() Info
    // SubAgents returns the list of sub-Agents available to this Agent.
    // SubAgents and FindSubAgent methods support collaboration between Agents. An Agent can delegate tasks to other Agents, building complex multi-Agent systems.
    SubAgents() []Agent
    // FindSubAgent finds sub-Agent by name.
    FindSubAgent(name string) Agent
}
```

The framework provides multiple types of Agent implementations, including LLMAgent, ChainAgent, ParallelAgent, CycleAgent, and GraphAgent. For detailed information about different types of Agents and multi-Agent systems, please refer to [Multi-Agent](./multiagent.md).

## Callbacks

Callbacks provide a rich callback mechanism that allows you to inject custom logic at key points during Agent execution.

### Callback Types

The framework provides three types of callbacks:

**Agent Callbacks**: Triggered before and after Agent execution

```go
type AgentCallbacks struct {
    BeforeAgent []BeforeAgentCallback  // Callbacks before Agent runs.
    AfterAgent  []AfterAgentCallback   // Callbacks after Agent runs.
}
```

**Model Callbacks**: Triggered before and after model calls

```go
type ModelCallbacks struct {
    BeforeModel []BeforeModelCallback  // Callbacks before model calls.
    AfterModel  []AfterModelCallback   // Callbacks after model calls.
}
```

**Tool Callbacks**: Triggered before and after tool calls

```go
type ToolCallbacks struct {
	BeforeTool []BeforeToolCallback  // Callbacks before tool calls.
	AfterTool []AfterToolCallback    // Callbacks after tool calls.
}
```

### Usage Example

```go
// Create Agent callbacks.
callbacks := &agent.AgentCallbacks{
    BeforeAgent: []agent.BeforeAgentCallback{
        func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
            log.Printf("Agent %s started execution", invocation.AgentName)
            return nil, nil
        },
    },
    AfterAgent: []agent.AfterAgentCallback{
        func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
            if runErr != nil {
                log.Printf("Agent %s execution error: %v", invocation.AgentName, runErr)
            } else {
                log.Printf("Agent %s execution completed", invocation.AgentName)
            }
            return nil, nil
        },
    },
}

// Use callbacks in Invocation.
llmagent := llmagent.New("llmagent", llmagent.WithAgentCallbacks(callbacks))
```

The callback mechanism allows you to precisely control the Agent's execution process and implement more complex business logic.

## Advanced Usage

The framework provides advanced features like Runner, Session, and Memory for building more complex Agent systems.

**Runner is the recommended usage**, responsible for managing Agent execution flow, connecting Session/Memory Service capabilities, and providing a more user-friendly interface.

Session Service is used to manage session state, supporting conversation history and context maintenance.

Memory Service is used to record user preference information, supporting personalized experiences.

**Recommended Reading Order:**

1. [Runner](runner.md) - Learn the recommended usage
2. [Session](session.md) - Understand session management
3. [Multi-Agent](multiagent.md) - Learn multi-Agent systems
