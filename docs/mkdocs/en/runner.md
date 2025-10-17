# Runner Component User Guide

## Overview

Runner provides the interface to run Agents, responsible for session management and event stream processing. The core responsibilities of Runner are: obtain or create sessions, generate an Invocation ID, call Agent.Run, process the returned event stream, and append non-partial response events to the session.

### 🎯 Key Features

- **💾 Session Management**: Obtain/create sessions via sessionService, using inmemory.NewSessionService() by default.
- **🔄 Event Handling**: Receive Agent event streams and append non-partial response events to the session.
- **🆔 ID Generation**: Automatically generate Invocation IDs and event IDs.
- **📊 Observability Integration**: Integrates telemetry/trace to automatically record spans.
- **✅ Completion Event**: Generates a runner-completion event after the Agent event stream ends.

## Architecture

```text
┌─────────────────────┐
│       Runner        │  - Session management.
└─────────┬───────────┘  - Event stream processing.
          │
          │ r.agent.Run(ctx, invocation)
          │
┌─────────▼───────────┐
│       Agent         │  - Receives Invocation.
└─────────┬───────────┘  - Returns <-chan *event.Event.
          │
          │ Implementation is determined by the Agent.
          │
┌─────────▼───────────┐
│    Agent Impl       │  e.g., LLMAgent, ChainAgent.
└─────────────────────┘
```

## 🚀 Quick Start

### 📋 Requirements

- Go 1.21 or later.
- Valid LLM API key (OpenAI-compatible interface).
- Redis (optional, for distributed session management).

### 💡 Minimal Example

```go
package main

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	// 1. Create model.
	llmModel := openai.New("DeepSeek-V3-Online-64K")

	// 2. Create Agent.
	a := llmagent.New("assistant",
		llmagent.WithModel(llmModel),
		llmagent.WithInstruction("You are a helpful AI assistant."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}), // Enable streaming output.
	)

	// 3. Create Runner.
	r := runner.NewRunner("my-app", a)

	// 4. Run conversation.
	ctx := context.Background()
	userMessage := model.NewUserMessage("Hello!")

	eventChan, err := r.Run(ctx, "user1", "session1", userMessage, agent.WithRequestID("request-ID"))
	if err != nil {
		panic(err)
	}

	// 5. Handle responses.
	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("Error: %s\n", event.Error.Message)
			continue
		}

		if len(event.Response.Choices) > 0 {
			fmt.Print(event.Response.Choices[0].Delta.Content)
		}
	}
}
```

### 🚀 Run the Example

```bash
# Enter the example directory.
cd examples/runner

# Set API key.
export OPENAI_API_KEY="your-api-key"

# Basic run.
go run main.go

# Use Redis session.
docker run -d -p 6379:6379 redis:alpine
go run main.go -session redis

# Custom model.
go run main.go -model "gpt-4o-mini"
```

### 💬 Interactive Features

After running the example, the following special commands are supported:

- `/history` - Ask AI to show conversation history.
- `/new` - Start a new session (reset conversation context).
- `/exit` - End the conversation.

When the AI uses tools, detailed invocation processes will be displayed:

```text
🔧 Tool Call:
   • calculator (ID: call_abc123)
     Params: {"operation":"multiply","a":25,"b":4}

🔄 Executing...
✅ Tool Response (ID: call_abc123): {"operation":"multiply","a":25,"b":4,"result":100}

🤖 Assistant: I calculated 25 × 4 = 100 for you.
```

## 🔧 Core API

### Create Runner

```go
// Basic creation.
r := runner.NewRunner(appName, agent, options...)

// Common options.
r := runner.NewRunner("my-app", agent,
    runner.WithSessionService(sessionService),  // Session service.
)
```

### Run Conversation

```go
// Execute a single conversation.
eventChan, err := r.Run(ctx, userID, sessionID, message, options...)

// With run options (currently RunOptions is an empty struct, reserved for future use).
eventChan, err := r.Run(ctx, userID, sessionID, message)
```

#### Provide Conversation History (auto-seed + session reuse)

If your upstream service maintains the conversation and you want the agent to
see that context, you can pass a full history (`[]model.Message`) directly. The
runner will seed an empty session with that history automatically and then
merge in new session events.

Option A: Use the convenience helper `runner.RunWithMessages`

```go
msgs := []model.Message{
    model.NewSystemMessage("You are a helpful assistant."),
    model.NewUserMessage("First user input"),
    model.NewAssistantMessage("Previous assistant reply"),
    model.NewUserMessage("What’s the next step?"),
}

ch, err := runner.RunWithMessages(ctx, r, userID, sessionID, msgs, agent.WithRequestID("request-ID"))
```

Example: `examples/runwithmessages` (uses `RunWithMessages`; runner auto-seeds and
continues reusing the session)

Option B: Pass via RunOption explicitly (same philosophy as ADK Python)

```go
msgs := []model.Message{ /* as above */ }
ch, err := r.Run(ctx, userID, sessionID, model.Message{}, agent.WithMessages(msgs))
```

When `[]model.Message` is provided, the runner persists that history into the
session on first use (if empty). The content processor does not read this
option; it only derives messages from session events (or falls back to the
single `invocation.Message` if the session has no events). `RunWithMessages`
still sets `invocation.Message` to the latest user turn so graph/flow agents
that inspect it continue to work.

## 💾 Session Management

### In-memory Session (Default)

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

sessionService := inmemory.NewSessionService()
r := runner.NewRunner("app", agent,
    runner.WithSessionService(sessionService))
```

### Redis Session (Distributed)

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// Create Redis session service.
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"))

r := runner.NewRunner("app", agent,
    runner.WithSessionService(sessionService))
```

### Session Configuration

```go
// Configuration options supported by Redis.
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSessionEventLimit(1000),         // Limit number of session events.
    // redis.WithRedisInstance("redis-instance"), // Or use an instance name.
)
```

## 🤖 Agent Configuration

Runner's core responsibility is to manage the Agent execution flow. A created Agent needs to be executed via Runner.

### Basic Agent Creation

```go
// Create a basic Agent (see agent.md for detailed configuration).
agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("You are a helpful AI assistant."))

// Execute Agent with Runner.
r := runner.NewRunner("my-app", agent)
```

### Generation Configuration

Runner passes generation configuration to the Agent:

```go
// Helper functions.
func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }

genConfig := model.GenerationConfig{
    MaxTokens:   intPtr(2000),
    Temperature: floatPtr(0.7),
    Stream:      true,  // Enable streaming output.
}

agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithGenerationConfig(genConfig))
```

### Tool Integration

Tool configuration is done inside the Agent, while Runner is responsible for running the Agent with tools:

```go
// Create tools (see tool.md for detailed configuration).
tools := []tool.Tool{
    function.NewFunctionTool(myFunction, function.WithName("my_tool")),
    // More tools...
}

// Add tools to the Agent.
agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(tools))

// Runner runs the Agent configured with tools.
r := runner.NewRunner("my-app", agent)
```

**Tool invocation flow**: Runner itself does not directly handle tool invocation. The flow is as follows:

1. **Pass tools**: Runner passes context to the Agent via Invocation.
2. **Agent processing**: Agent.Run handles the tool invocation logic.
3. **Event forwarding**: Runner receives the event stream returned by the Agent and forwards it.
4. **Session recording**: Append non-partial response events to the session.

### Multi-Agent Support

Runner can execute complex multi-Agent structures (see multiagent.md for details):

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"

// Create a multi-Agent pipeline.
multiAgent := chainagent.New("pipeline",
    chainagent.WithSubAgents([]agent.Agent{agent1, agent2}))

// Execute with the same Runner.
r := runner.NewRunner("multi-app", multiAgent)
```

## 📊 Event Processing

### Event Types

```go
import "trpc.group/trpc-go/trpc-agent-go/event"

for event := range eventChan {
    // Error event.
    if event.Error != nil {
        fmt.Printf("Error: %s\n", event.Error.Message)
        continue
    }

    // Streaming content.
    if len(event.Response.Choices) > 0 {
        choice := event.Response.Choices[0]
        fmt.Print(choice.Delta.Content)
    }

    // Tool invocation.
    if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
        for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
            fmt.Printf("Call tool: %s\n", toolCall.Function.Name)
        }
    }

    // Completion event.
    if event.Done {
        break
    }
}
```

### Complete Event Handling Example

```go
import (
    "fmt"
    "strings"
)

func processEvents(eventChan <-chan *event.Event) error {
    var fullResponse strings.Builder

    for event := range eventChan {
        // Handle errors.
        if event.Error != nil {
            return fmt.Errorf("Event error: %w", event.Error)
        }

        // Handle tool calls.
        if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
            fmt.Println("🔧 Tool Call:")
            for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
                fmt.Printf("  • %s (ID: %s)\n",
                    toolCall.Function.Name, toolCall.ID)
                fmt.Printf("    Params: %s\n",
                    string(toolCall.Function.Arguments))
            }
        }

        // Handle tool responses.
        if event.Response != nil {
            for _, choice := range event.Response.Choices {
                if choice.Message.Role == model.RoleTool {
                    fmt.Printf("✅ Tool Response (ID: %s): %s\n",
                        choice.Message.ToolID, choice.Message.Content)
                }
            }
        }

        // Handle streaming content.
        if len(event.Response.Choices) > 0 {
            content := event.Response.Choices[0].Delta.Content
            if content != "" {
                fmt.Print(content)
                fullResponse.WriteString(content)
            }
        }

        if event.Done {
            fmt.Println() // New line.
            break
        }
    }

    return nil
}
```

## 🔮 Execution Context Management

Runner creates and manages the Invocation structure:

```go
// The Invocation created by Runner contains the following fields.
invocation := agent.NewInvocation(
    agent.WithInvocationAgent(r.agent),                               // Agent instance.
    agent.WithInvocationSession(&session.Session{ID: "session-001"}), // Session object.
    agent.WithInvocationEndInvocation(false),                         // End flag.
    agent.WithInvocationMessage(model.NewUserMessage("User input")),  // User message.
    agent.WithInvocationRunOptions(ro),                               // Run options.
)
// Note: Invocation also includes other fields such as AgentName, Branch, Model,
// TransferInfo, AgentCallbacks, ModelCallbacks, ToolCallbacks, etc.,
// but these fields are used and managed internally by the Agent.
```

## ✅ Best Practices

### Error Handling

```go
// Handle errors from Runner.Run.
eventChan, err := r.Run(ctx, userID, sessionID, message, agent.WithRequestID("request-ID"))
if err != nil {
    log.Printf("Runner execution failed: %v", err)
    return err
}

// Handle errors in the event stream.
for event := range eventChan {
    if event.Error != nil {
        log.Printf("Event error: %s", event.Error.Message)
        continue
    }
    // Handle normal events.
}
```

### Resource Management

```go
// Use context to control lifecycle.
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Ensure all events are consumed.
eventChan, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    return err
}

for event := range eventChan {
    // Process events.
    if event.Done {
        break
    }
}
```

### Health Check

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Check whether Runner works properly.
func checkRunner(r runner.Runner, ctx context.Context) error {
    testMessage := model.NewUserMessage("test")
    eventChan, err := r.Run(ctx, "test-user", "test-session", testMessage)
    if err != nil {
        return fmt.Errorf("Runner.Run failed: %v", err)
    }

    // Check the event stream.
    for event := range eventChan {
        if event.Error != nil {
            return fmt.Errorf("Received error event: %s", event.Error.Message)
        }
        if event.Done {
            break
        }
    }

    return nil
}
```

## 📝 Summary

The Runner component is a core part of the tRPC-Agent-Go framework, providing complete conversation management and Agent orchestration capabilities. By properly using session management, tool integration, and event handling, you can build powerful intelligent conversational applications.
