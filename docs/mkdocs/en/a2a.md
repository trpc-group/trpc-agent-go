# tRPC-Agent-Go A2A Integration Guide

## Overview

tRPC-Agent-Go provides a complete A2A (Agent-to-Agent) solution with two core components:

- **A2A Server**: Exposes local Agents as A2A services for other Agents to call
- **A2AAgent**: A client proxy for calling remote A2A services, allowing you to use remote Agents as if they were local

### Core Capabilities

- **Zero Protocol Awareness**: Developers only need to focus on Agent business logic without understanding A2A protocol details
- **Automatic Adaptation**: The framework automatically converts Agent information to A2A AgentCard
- **Message Conversion**: Automatically handles conversion between A2A protocol messages and Agent message formats

## A2A Server: Exposing Agents as Services

### Concept Introduction

A2A Server is a server-side component provided by tRPC-Agent-Go for quickly converting any local Agent into a network service that complies with the A2A protocol.

### Core Features

- **One-Click Conversion**: Expose Agents as A2A services through simple configuration
- **Automatic Protocol Adaptation**: Automatically handles conversion between A2A protocol and Agent interfaces
- **AgentCard Generation**: Automatically generates AgentCards required for service discovery
- **Streaming Support**: Supports both streaming and non-streaming response modes

### Automatic Conversion from Agent to A2A

tRPC-Agent-Go implements seamless conversion from Agent to A2A service through the `server/a2a` package:

```go
func New(opts ...Option) (*a2a.A2AServer, error) {}
```

### Automatic AgentCard Generation

The framework automatically extracts Agent metadata (name, description, tools, etc.) to generate an AgentCard that complies with the A2A protocol, including:
- Basic Agent information (name, description, URL)
- Capability declarations (streaming support)
- Skill lists (automatically generated based on Agent tools)

### Message Protocol Conversion

The framework includes a built-in `messageProcessor` that implements bidirectional conversion between A2A protocol messages and Agent message formats, so users don't need to worry about message format conversion details.

## A2A Server Quick Start

### Exposing Agent Services with A2A Server

With just a few lines of code, you can convert any Agent into an A2A service:

#### Basic Example: Creating A2A Server

```go
package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

func main() {
	// 1. Create a regular Agent
	model := openai.New("gpt-4o-mini")
	agent := llmagent.New("MyAgent",
		llmagent.WithModel(model),
		llmagent.WithDescription("An intelligent assistant"),
	)

	// 2. Convert to A2A service with one click
	server, _ := a2aserver.New(
		a2aserver.WithHost("localhost:8080"),
		a2aserver.WithAgent(agent), // Pass in any Agent
	)

	// 3. Start the service to accept A2A requests
	server.Start(":8080")
}
```

#### Direct A2A Protocol Client Call

```go
import (
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func main() {
	// Connect to A2A service
	client, _ := client.NewA2AClient("http://localhost:8080/")

	// Send message to Agent
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("Hello, please help me analyze this code")},
	)

	// Agent will automatically process and return results
	response, _ := client.SendMessage(context.Background(),
		protocol.SendMessageParams{Message: message})
}
```

## A2AAgent: Calling Remote A2A Services

Corresponding to A2A Server, tRPC-Agent-Go also provides `A2AAgent` for calling remote A2A services, enabling communication between Agents.

### Concept Introduction

`A2AAgent` is a special Agent implementation that doesn't directly handle user requests but forwards them to remote A2A services. From the user's perspective, `A2AAgent` looks like a regular Agent, but it's actually a local proxy for a remote Agent.

**Simple Understanding**:
- **A2A Server**: I have an Agent and want others to call it → Expose as A2A service
- **A2AAgent**: I want to call someone else's Agent → Call through A2AAgent proxy

### Core Features

- **Transparent Proxy**: Use remote Agents as if they were local Agents
- **Automatic Discovery**: Automatically discover remote Agent capabilities through AgentCard
- **Protocol Conversion**: Automatically handle conversion between local message formats and A2A protocol
- **Streaming Support**: Support both streaming and non-streaming communication modes
- **State Transfer**: Support transferring local state to remote Agents
- **Error Handling**: Comprehensive error handling and retry mechanisms

### Use Cases

1. **Distributed Agent Systems**: Call Agents from other services in microservice architectures
2. **Agent Orchestration**: Combine multiple specialized Agents into complex workflows
3. **Cross-Team Collaboration**: Call Agent services provided by other teams

### A2AAgent Quick Start

#### Basic Usage

```go
package main

import (
	"context"
	"fmt"
	
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// 1. Create A2AAgent pointing to remote A2A service
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL("http://localhost:8888"),
	)
	if err != nil {
		panic(err)
	}

	// 2. Use it like a regular Agent
	sessionService := inmemory.NewSessionService()
	runner := runner.NewRunner("test", a2aAgent, 
		runner.WithSessionService(sessionService))

	// 3. Send message
	events, err := runner.Run(
		context.Background(),
		"user1",
		"session1", 
		model.NewUserMessage("Please tell me a joke"),
	)
	if err != nil {
		panic(err)
	}

	// 4. Handle response
	for event := range events {
		if event.Response != nil && len(event.Response.Choices) > 0 {
			fmt.Print(event.Response.Choices[0].Message.Content)
		}
	}
}
```

In multi-agent systems, `A2AAgent` is often used as a SubAgent of a
local coordinator Agent (for example an `LLMAgent`). You can combine
`A2AAgent` with `LLMAgent.SetSubAgents` to dynamically load and refresh
remote SubAgents from a registry without recreating the coordinator.

#### Advanced Configuration

```go
// Create A2AAgent with advanced configuration
a2aAgent, err := a2aagent.New(
	// Specify remote service address
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	
	// Set streaming buffer size
	a2aagent.WithStreamingChannelBufSize(2048),

	// Custom protocol conversion
	a2aagent.WithCustomEventConverter(customEventConverter),

	a2aagent.WithCustomA2AConverter(customA2AConverter),
)
```

### Complete Example: A2A Server + A2AAgent Combined Usage

Here's a complete example showing how to run both A2A Server (exposing local Agent) and A2AAgent (calling remote service) in the same program:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// 1. Create and start remote Agent service
	remoteAgent := createRemoteAgent()
	startA2AServer(remoteAgent, "localhost:8888")
	
	time.Sleep(1 * time.Second) // Wait for service to start

	// 2. Create A2AAgent connecting to remote service
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL("http://localhost:8888"),
		a2aagent.WithTransferStateKey("user_context"),
	)
	if err != nil {
		panic(err)
	}

	// 3. Create local Agent
	localAgent := createLocalAgent()

	// 4. Compare local and remote Agent responses
	compareAgents(localAgent, a2aAgent)
}

func createRemoteAgent() agent.Agent {
	model := openai.New("gpt-4o-mini")
	return llmagent.New("JokeAgent",
		llmagent.WithModel(model),
		llmagent.WithDescription("I am a joke-telling agent"),
		llmagent.WithInstruction("Always respond with a funny joke"),
	)
}

func createLocalAgent() agent.Agent {
	model := openai.New("gpt-4o-mini") 
	return llmagent.New("LocalAgent",
		llmagent.WithModel(model),
		llmagent.WithDescription("I am a local assistant"),
	)
}

func startA2AServer(agent agent.Agent, host string) {
	server, err := a2a.New(
		a2a.WithHost(host),
		a2a.WithAgent(agent, true), // Enable streaming
	)
	if err != nil {
		panic(err)
	}
	
	go func() {
		server.Start(host)
	}()
}

func compareAgents(localAgent, remoteAgent agent.Agent) {
	sessionService := inmemory.NewSessionService()
	
	localRunner := runner.NewRunner("local", localAgent,
		runner.WithSessionService(sessionService))
	remoteRunner := runner.NewRunner("remote", remoteAgent,
		runner.WithSessionService(sessionService))

	userMessage := "Please tell me a joke"
	
	// Call local Agent
	fmt.Println("=== Local Agent Response ===")
	processAgent(localRunner, userMessage)
	
	// Call remote Agent (via A2AAgent)
	fmt.Println("\n=== Remote Agent Response (via A2AAgent) ===")
	processAgent(remoteRunner, userMessage)
}

func processAgent(runner runner.Runner, message string) {
	events, err := runner.Run(
		context.Background(),
		"user1",
		"session1",
		model.NewUserMessage(message),
		agent.WithRuntimeState(map[string]any{
			"user_context": "test_context",
		}),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	for event := range events {
		if event.Response != nil && len(event.Response.Choices) > 0 {
			content := event.Response.Choices[0].Message.Content
			if content == "" {
				content = event.Response.Choices[0].Delta.Content
			}
			if content != "" {
				fmt.Print(content)
			}
		}
	}
	fmt.Println()
}
```

### AgentCard Automatic Discovery

`A2AAgent` supports automatically obtaining remote Agent information through the standard AgentCard discovery mechanism:

```go
// A2AAgent automatically retrieves AgentCard from the following path
// http://remote-agent:8888/.well-known/agent.json

type AgentCard struct {
    Name         string                 `json:"name"`
    Description  string                 `json:"description"`
    URL          string                 `json:"url"`
    Capabilities AgentCardCapabilities  `json:"capabilities"`
}

type AgentCardCapabilities struct {
    Streaming *bool `json:"streaming,omitempty"`
}
```

### State Transfer

`A2AAgent` supports transferring local runtime state to remote Agents:

```go
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	// Specify state keys to transfer
	a2aagent.WithTransferStateKey("user_id", "session_context", "preferences"),
)

// Runtime state is passed to remote Agent through A2A protocol metadata field
events, _ := runner.Run(ctx, userID, sessionID, message,
	agent.WithRuntimeState(map[string]any{
		"user_id":         "12345",
		"session_context": "shopping_cart",
		"preferences":     map[string]string{"language": "en"},
	}),
)
```

### Custom HTTP Headers

You can pass custom HTTP headers to A2A agent for each request using `WithA2ARequestOptions`:

```go
import "trpc.group/trpc-go/trpc-a2a-go/client"

events, err := runner.Run(
	context.Background(),
	userID,
	sessionID,
	model.NewUserMessage("your question"),
	// Pass custom HTTP headers for this request
	agent.WithA2ARequestOptions(
		client.WithRequestHeader("X-Custom-Header", "custom-value"),
		client.WithRequestHeader("X-Request-ID", fmt.Sprintf("req-%d", time.Now().UnixNano())),
		client.WithRequestHeader("Authorization", "Bearer your-token"),
	),
)
```

**Common Use Cases:**

1. **Authentication**: Pass authentication tokens
   ```go
   agent.WithA2ARequestOptions(
       client.WithRequestHeader("Authorization", "Bearer "+token),
   )
   ```

2. **Distributed Tracing**: Add request/trace IDs
   ```go
   agent.WithA2ARequestOptions(
       client.WithRequestHeader("X-Request-ID", requestID),
       client.WithRequestHeader("X-Trace-ID", traceID),
   )
   ```


**Configuring UserID Header:**

Both client and server support configuring which HTTP header to use for UserID, default is X-User-ID:

```go
// Client side: Configure which header to send UserID in
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	// Default is "X-User-ID", can be customized
	a2aagent.WithUserIDHeader("X-Custom-User-ID"),
)

// Server side: Configure which header to read UserID from
server, _ := a2a.New(
	a2a.WithHost("localhost:8888"),
	a2a.WithAgent(agent, true),
	// Default is "X-User-ID", can be customized
	a2a.WithUserIDHeader("X-Custom-User-ID"),
)
```

The UserID from `invocation.Session.UserID` will be automatically sent via the configured header to the A2A server.

### Custom Converters

For special requirements, you can customize message and event converters:

```go
// Custom A2A message converter
type CustomA2AConverter struct{}

func (c *CustomA2AConverter) ConvertToA2AMessage(
	isStream bool, 
	agentName string, 
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	// Custom message conversion logic
	return &protocol.Message{
		MessageID: invocation.InvocationID,
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{/* custom content */},
	}, nil
}

// Custom event converter  
type CustomEventConverter struct{}

func (c *CustomEventConverter) ConvertToEvent(
	result protocol.MessageResult,
	agentName string,
	invocation *agent.Invocation,
) (*event.Event, error) {
	// Custom event conversion logic
	return event.New(invocation.InvocationID, agentName), nil
}

// Use custom converters
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	a2aagent.WithA2AMessageConverter(&CustomA2AConverter{}),
	a2aagent.WithEventConverter(&CustomEventConverter{}),
)
```

## Summary: A2A Server vs A2AAgent

| Component | Role | Use Case | Core Functions |
|-----------|------|----------|----------------|
| **A2A Server** | Service Provider | Expose local Agent for other systems to call | • Protocol conversion<br>• AgentCard generation<br>• Message routing<br>• Streaming support |
| **A2AAgent** | Service Consumer | Call remote A2A services | • Transparent proxy<br>• Automatic discovery<br>• State transfer<br>• Protocol adaptation |

### Typical Architecture Pattern

```
┌─────────────┐ A2A protocol  ┌───────────────┐
│   Client    │──────────────→│ A2A Server    │
│ (A2AAgent)  │               │ (local Agent) │
└─────────────┘               └───────────────┘
      ↑                              ↑
      │                              │
   Call remote                   Expose local
   Agent service                 Agent service
```

Through the combined use of A2A Server and A2AAgent, you can easily build distributed Agent systems.
