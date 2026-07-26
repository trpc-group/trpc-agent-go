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

It is important to distinguish these two layers:

- The `streaming` flag in `WithAgent(agent, streaming)` is primarily used to declare `AgentCard.Capabilities.Streaming`.
- It is not the global execution switch for the `runner`, and it does not directly control the internal message processing pipeline.
- If you use `WithRunner(...) + WithAgentCard(...)`, the streaming capability should be declared directly on the `AgentCard`, for example via `NewAgentCard(..., streaming)`.
- In `WithRunner(...) + WithAgentCard(...)` mode, `skills` are owned by the caller. `NewAgentCard(...)` only gives you a default structure and default skill; it does not infer the full tool list from a custom `runner`.

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
		a2aserver.WithAgent(agent, true), // Enable streaming
	)

	// 3. Start the service to accept A2A requests
	server.Start(":8080")
}
```

#### Streaming output event type (Message vs Artifact)

When streaming is enabled, A2A allows the server to emit incremental output in
different ways:

- **TaskArtifactUpdateEvent (default)**: ADK-style streaming. Chunks are sent
  as task artifact updates (`artifact-update`).
- **Message**: Lightweight streaming. Chunks are sent as `message`, so clients
  can render `Message.parts` directly without treating output as a persisted
  artifact.

To stream agent output as `message` instead of `artifact-update`, configure the
server with:

```go
server, _ := a2aserver.New(
	a2aserver.WithHost("localhost:8080"),
	a2aserver.WithAgent(agent, true),
	a2aserver.WithStreamingEventType(
		a2aserver.StreamingEventTypeMessage,
	),
)
```

Task state updates (`submitted`, `completed`) are still emitted as
`TaskStatusUpdateEvent`. If `WithStructuredTaskErrors(true)` is enabled,
terminal failures are also emitted as failed task status updates, with
machine-readable fields preferred on the outer metadata, mirrored into
`status.message.metadata` for `0.1` compatibility, and display text in
`status.message.parts`.

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

### Advanced Configuration

#### Custom Runner (WithRunner)

By default, A2A Server automatically creates a Runner for you. If you need finer control, such as injecting a MemoryService or customizing SessionService, use `WithRunner`.

Note: `WithRunner` is mutually exclusive with `WithAgent`. When you provide `WithRunner`, you must also provide the public agent identity explicitly via `WithAgentCard`:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessionmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

memoryService := inmemory.NewMemoryService()
sessionService := sessionmemory.NewSessionService()
streaming := true

r := runner.NewRunner(
	agent.Info().Name,
	agent,
	runner.WithSessionService(sessionService),
	runner.WithMemoryService(memoryService),
)

card, _ := a2a.NewAgentCard(agent.Info().Name, agent.Info().Description, "localhost:8080", streaming)

server, _ := a2a.New(
	a2a.WithRunner(r),
	a2a.WithAgentCard(card),
)
```

In this `runner-only` mode, streaming capability is no longer passed through `WithAgent(...)`; it is declared directly by the `AgentCard.Capabilities.Streaming` field provided via `WithAgentCard(...)`.

`examples/a2aagent` also includes an explicit example. Use `-server-mode runner-card` to switch the server construction path to `WithRunner(...) + WithAgentCard(...)`:

```bash
cd examples/a2aagent
go run . -server-mode runner-card
```

If you only need a default-compliant card quickly, prefer `NewAgentCard(...)` instead of manually filling in `Name`, `Description`, `Capabilities`, and the default skill. If your `runner` needs a more accurate `skills` list, populate and maintain it in your own code.

#### Dynamically Updating AgentCard

If you need to update the exposed `AgentCard` at runtime, wire the underlying `a2aprotocolserver.WithAgentCardHandler(...)` through `WithExtraA2AOptions(...)`, and use `NewAgentCardHandler(...)` to serve the current snapshot:

```go
import (
	"sync"

	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

card, _ := a2a.NewAgentCard(agent.Info().Name, agent.Info().Description, "localhost:8080", true)
var (
	cardMu      sync.RWMutex
	currentCard = card
)

server, _ := a2a.New(
	a2a.WithRunner(runner.NewRunner(agent.Info().Name, agent)),
	a2a.WithAgentCard(currentCard),
	a2a.WithExtraA2AOptions(
		a2aprotocolserver.WithAgentCardHandler(
			a2a.NewAgentCardHandler(func() a2aprotocolserver.AgentCard {
				cardMu.RLock()
				defer cardMu.RUnlock()
				return currentCard
			}),
		),
	),
)

cardMu.Lock()
updated := currentCard
updated.Description = "new description"
currentCard = updated
cardMu.Unlock()
```

This only updates the exposed metadata. It does not modify the underlying `runner`, `taskManager`, or message processing pipeline. The caller remains responsible for where `currentCard` is stored and how it is updated.

Treat fields such as `Name` and `URL` as startup-time invariants, because they also participate in identity, routing, or discovery semantics. If those fields must change, rebuilding the server is safer than only updating the card endpoint.

#### Server-Side Message Processing Hook (WithProcessMessageHook)

`WithProcessMessageHook` allows you to insert custom logic before/after the A2A Server processes messages. It uses a middleware pattern, wrapping the underlying `MessageProcessor`:

```go
import "trpc.group/trpc-go/trpc-a2a-go/taskmanager"

// Custom hook processor
type hookProcessor struct {
	next taskmanager.MessageProcessor
}

func (h *hookProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	// Before processing: read custom metadata injected by the client
	if traceID, ok := message.Metadata["trace_id"]; ok {
		fmt.Printf("received trace_id: %v\n", traceID)
	}
	// Delegate to the next processor
	return h.next.ProcessMessage(ctx, message, options, handler)
}

server, _ := a2a.New(
	a2a.WithHost("localhost:8080"),
	a2a.WithAgent(agent, true),
	a2a.WithProcessMessageHook(
		func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
			return &hookProcessor{next: next}
		},
	),
)
```

**Typical use cases**:
- Read custom metadata injected by the client via `BuildMessageHook`
- Add logging, monitoring, or auditing before/after message processing
- Modify or validate inbound messages

#### Client-Side Message Build Hook (WithBuildMessageHook)

`WithBuildMessageHook` is a Hook on the A2AAgent (client) side that allows injecting custom data before sending messages to a remote A2A Server. It also uses a middleware pattern:

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"

a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	a2aagent.WithBuildMessageHook(
		func(next a2aagent.ConvertToA2AMessageFunc) a2aagent.ConvertToA2AMessageFunc {
			return func(isStream bool, agentName string, inv *agent.Invocation) (*protocol.Message, error) {
				// Call the default converter
				msg, err := next(isStream, agentName, inv)
				if err != nil {
					return nil, err
				}
				// Inject custom metadata
				if msg.Metadata == nil {
					msg.Metadata = make(map[string]any)
				}
				msg.Metadata["trace_id"] = "my-trace-123"
				msg.Metadata["business_tag"] = "order-service"
				return msg, nil
			}
		},
	),
)
```

**BuildMessageHook + ProcessMessageHook interaction**:

```text
┌──────────────────┐                    ┌───────────────────┐
│    A2AAgent      │   A2A protocol     │    A2A Server     │
│                  │                    │                   │
│ BuildMessageHook │── metadata ──────→ │ProcessMessageHook │
│ (inject data)    │                    │ (read data)       │
└──────────────────┘                    └───────────────────┘
```

The client injects custom data (such as trace_id, business tags) into the A2A message's `metadata` field via `BuildMessageHook`, and the server reads and processes this data via `ProcessMessageHook`.

#### Append RunOptions (WithRunOptions)

`WithRunOptions` allows appending additional `RunOption` to every Agent invocation in the A2A Server:

```go
server, _ := a2a.New(
	a2a.WithHost("localhost:8080"),
	a2a.WithAgent(agent, true),
	a2a.WithRunOptions(
		agent.WithRequestID("custom-req-id"),
	),
)
```

#### Graph internal event forwarding

By default, A2A Server filters most internal `graph.*` runtime events (for
example `graph.node.start`, `graph.node.complete`, `graph.pregel.*`, and
`graph.checkpoint.*`) to avoid exposing low-level execution details to
downstream consumers.

The terminal `graph.execution` event is still preserved by default (together
with normal message/error events), so final state reconstruction and
`state_delta` handoff continue to work.

If you need node-level traces for debugging, extend the graph object allowlist:

```go
server, _ := a2aserver.New(
	a2aserver.WithHost("localhost:8080"),
	a2aserver.WithAgent(agent, true),
	a2aserver.WithGraphEventObjectAllowlist(
		"graph.execution", // keep terminal event
		"graph.node.*",    // include node lifecycle events
	),
)
```

Notes:

- If this option is not set, the default allowlist is `["graph.execution"]`.
- If you explicitly call `WithGraphEventObjectAllowlist()` with no arguments,
  all `graph.*` events will be filtered out (including `graph.execution`).

Use this in debug/diagnostic scenarios. Keeping it off by default reduces noise
and transport overhead in production.

#### Rewrite outbound A2A responses

`WithResponseRewriter` lets the server rewrite outbound A2A results immediately
before they are returned to the caller or sent to the streaming subscriber. It is
useful when you want to hide internal metadata, redact debug fields, or drop
server-generated events that are not part of your public A2A contract.

The rewriter sees the final unary result after server-side aggregation. In
streaming mode, it sees every outbound streaming result, including converted
agent events, task status updates, final artifact updates, structured task
errors, and messages returned by the error handler. The request context is
passed to each rewrite call so you can use request-scoped values in logs.

Returning `nil` from the rewriter drops that outbound result.

```go
import (
	"context"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

server, _ := a2aserver.New(
	a2aserver.WithHost("localhost:8080"),
	a2aserver.WithAgent(agent, true),
	a2aserver.WithResponseRewriter(a2aserver.ResponseRewriterFuncs{
		Unary: func(ctx context.Context, result protocol.UnaryMessageResult) protocol.UnaryMessageResult {
			if msg, ok := result.(*protocol.Message); ok {
				delete(msg.Metadata, "debug_trace")
			}
			return result
		},
		Streaming: func(ctx context.Context, result protocol.StreamingMessageResult) protocol.StreamingMessageResult {
			if msg, ok := result.(*protocol.Message); ok {
				delete(msg.Metadata, "debug_trace")
			}
			return result
		},
	}),
)
```

### Hosting multiple A2A agents on one HTTP port (base paths)

Sometimes you want **one service (one port)** to expose multiple A2A Agents.
The idiomatic A2A approach is to give each Agent its own **base URL**, and let
the client select the Agent by choosing the URL (not by passing an `agent_name`
parameter).

In tRPC-Agent-Go, `a2a.WithHost(...)` supports URLs with a path segment.
When the host URL contains a path (for example `http://localhost:8888/agents/math`),
the A2A server will automatically use that path as its **base path** for routing.

Key idea:

- Create **one** A2A server per Agent (each with a different base path)
- Mount all A2A servers onto **one** shared `http.Server` via `server.Handler()`

Example:

```go
mathServer, err := a2a.New(
	a2a.WithHost("http://localhost:8888/agents/math"),
	a2a.WithAgent(mathAgent, false),
)
if err != nil {
	panic(err)
}

weatherServer, err := a2a.New(
	a2a.WithHost("http://localhost:8888/agents/weather"),
	a2a.WithAgent(weatherAgent, false),
)
if err != nil {
	panic(err)
}

mux := http.NewServeMux()
mux.Handle("/agents/math/", mathServer.Handler())
mux.Handle("/agents/weather/", weatherServer.Handler())

if err := http.ListenAndServe(":8888", mux); err != nil {
	panic(err)
}
```

After the server starts, each Agent has its own AgentCard endpoint:

- `http://localhost:8888/agents/math/.well-known/agent-card.json`
- `http://localhost:8888/agents/weather/.well-known/agent-card.json`

Full runnable example: `examples/a2amultipath`.

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

For the recommended structured task-error convention between A2A server and
`A2AAgent`, see [Error Handling](error-handling.md).

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

	// Explicitly control streaming mode (overrides AgentCard capability declaration)
	a2aagent.WithEnableStreaming(true),
)
```

Whether the client sends a streaming request follows this priority:

1. Per-call override via `agent.WithStream(...)`
2. `a2aagent.WithEnableStreaming(...)`
3. Remote `AgentCard.Capabilities.Streaming`
4. Default false

In other words, the server-side streaming declaration mainly tells the client whether the remote A2A service supports streaming requests. The client then decides whether to send streaming or non-streaming requests based on that capability.  
If the client explicitly sets `agent.WithStream(...)` or `a2aagent.WithEnableStreaming(...)`, that explicit choice overrides the `AgentCard` declaration.

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

### ADK Compatibility Mode

If you need to interoperate with Google ADK (Agent Development Kit) Python clients, you can enable ADK compatibility mode. When enabled, the Server will write additional `adk_`-prefixed keys (such as `adk_type`, `adk_thought`) in metadata to be compatible with ADK's part converter parsing logic:

```go
server, _ := a2a.New(
	a2a.WithHost("localhost:8888"),
	a2a.WithAgent(agent, true),
	a2a.WithADKCompatibility(true), // Enabled by default
)
```

### Custom Converters

For special requirements, you can customize message and event converters:

```go
// Custom A2A message converter (Invocation -> A2A Message)
// Implements the a2aagent.InvocationA2AConverter interface
type CustomA2AConverter struct{}

func (c *CustomA2AConverter) ConvertToA2AMessage(
	isStream bool, 
	agentName string, 
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	// Custom message conversion logic
	msg := protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{
		protocol.NewTextPart(invocation.Message.Content),
	})
	return &msg, nil
}

// Custom event converter (A2A Response -> Event)
// Implements the a2aagent.A2AEventConverter interface
type CustomEventConverter struct{}

func (c *CustomEventConverter) ConvertToEvents(
	result protocol.MessageResult,
	agentName string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	// Custom non-streaming event conversion logic
	return []*event.Event{event.New(invocation.InvocationID, agentName)}, nil
}

func (c *CustomEventConverter) ConvertStreamingToEvents(
	result protocol.StreamingMessageEvent,
	agentName string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	// Custom streaming event conversion logic
	return []*event.Event{event.New(invocation.InvocationID, agentName)}, nil
}

// Use custom converters
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	a2aagent.WithCustomA2AConverter(&CustomA2AConverter{}),
	a2aagent.WithCustomEventConverter(&CustomEventConverter{}),
)
```

### Graph Interrupt and Resume via A2A

When the remote A2A Server runs a Graph Agent that uses `graph.Interrupt`, extra configuration is needed:

- **Server-side**: Allow at least `graph.execution` and `graph.pregel.step`; the former preserves the graph's terminal completion event, while the latter lets interrupt events carry resume metadata such as `state_delta` and `pregel_metadata` back through A2A. If you do not want to maintain a narrow allowlist, use `WithGraphEventObjectAllowlist("*")` instead.
- **Client-side**: Forward at least `graph.CfgKeyLineageID`, `graph.CfgKeyCheckpointID`, `graph.CfgKeyCheckpointNS`, and `graph.StateKeyCommand`, so resume requests can send lineage/checkpoint/command data back to the remote graph. If you want to forward the full RuntimeState, use `WithTransferStateKey("*")` instead.

```go
// Server
server, _ := a2aserver.New(
    a2aserver.WithAgent(graphAgent, true),
    a2aserver.WithGraphEventObjectAllowlist(
        "graph.execution",
        "graph.pregel.step",
    ),
)

// Client
subAgent, _ := a2aagent.New(
    a2aagent.WithAgentCardURL("http://remote:8888"),
    a2aagent.WithEnableStreaming(true),
    a2aagent.WithTransferStateKey(
        graph.CfgKeyLineageID,
        graph.CfgKeyCheckpointID,
        graph.CfgKeyCheckpointNS,
        graph.StateKeyCommand,
    ),
)
```

Notes:

- `graph.execution` preserves the graph's terminal completion event.
- `graph.pregel.step` is the key graph event object used to carry interrupt/resume metadata.
- `graph.StateKeyCommand` carries `Resume` / `ResumeMap`, which the remote graph uses to continue from the interrupted node.
- `"*"` remains a convenient catch-all option for debugging or when full state forwarding is acceptable.

> Full example: [examples/graph/a2a_interrupt](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/graph/a2a_interrupt)

## Protocol Interaction Specification

For detailed specifications on how tool calls, code execution, reasoning content, and other events are transmitted through the A2A protocol, as well as Metadata field definitions, ADK compatibility mode, and distributed tracing, please refer to the dedicated document:

**[A2A Protocol Interaction Specification](a2a-interaction.md)**

This document defines the extension specification of trpc-agent-go on top of the A2A protocol, serving as the standard reference for Client and Server implementations.

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

### A2A Server Configuration Reference

| Option | Description |
|--------|-------------|
| `WithAgent(agent, streaming)` | Set the Agent and declare whether the generated AgentCard supports streaming; mutually exclusive with `WithRunner` |
| `WithHost(host)` | Set the service address, supports URLs with path |
| `WithAgentCard(card)` | Custom AgentCard (overrides auto-generation) |
| `WithRunner(runner)` | Custom Runner (inject Memory, Session, etc.); requires `WithAgentCard` |
| `WithSessionService(service)` | Set the session service used by the default Runner |
| `WithProcessMessageHook(hook)` | Server-side message processing Hook (middleware pattern) |
| `WithProcessorBuilder(builder)` | Fully custom message processor |
| `WithTaskManagerBuilder(builder)` | Custom task manager |
| `WithGraphEventObjectAllowlist(types...)` | Limit graph object types emitted by Event converters |
| `WithResponseRewriter(rewriter)` | Rewrite or drop outbound A2A unary/streaming results |
| `WithRunOptions(opts...)` | Append RunOptions to every invocation |
| `WithStreamingEventType(type)` | Streaming output event type (Artifact/Message) |
| `WithUserIDHeader(header)` | Custom UserID HTTP Header |
| `WithADKCompatibility(enabled)` | ADK compatibility mode (default: enabled) |
| `WithErrorHandler(handler)` | Custom error handler |
| `WithA2AToAgentConverter(conv)` | Custom A2A→Agent message converter |
| `WithEventToA2AConverter(conv)` | Custom Event→A2A message converter |
| `WithExtraA2AOptions(opts...)` | Pass-through options for underlying A2A Server |
| `WithDebugLogging(enabled)` | Enable debug logging |

### A2AAgent Configuration Reference

| Option | Description |
|--------|-------------|
| `WithAgentCardURL(url)` | Remote A2A service address |
| `WithBuildMessageHook(hook)` | Client-side message build Hook (middleware pattern) |
| `WithTransferStateKey(keys...)` | Specify RuntimeState keys to transfer |
| `WithEnableStreaming(enabled)` | Explicitly control streaming mode |
| `WithStreamingChannelBufSize(size)` | Streaming buffer size |
| `WithUserIDHeader(header)` | Custom UserID HTTP Header |
| `WithCustomA2AConverter(conv)` | Custom Invocation→A2A message converter |
| `WithCustomEventConverter(conv)` | Custom A2A Response→Event converter |
