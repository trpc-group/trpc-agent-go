# tRPC-Agent-Go A2A Integration Guide

## Overview

tRPC-Agent-Go provides the capability to convert any Agent into an A2A (Agent-to-Agent) service with a single click. Through simple configuration, developers can quickly expose any Agent developed based on the trpc-agent-go framework as a service that complies with the A2A protocol, enabling standardized communication between Agents.

### Core Capabilities

- **Zero Protocol Awareness**: Developers only need to focus on Agent business logic without understanding A2A protocol details
- **Automatic Adaptation**: The framework automatically converts Agent information to A2A AgentCard
- **Message Conversion**: Automatically handles conversion between A2A protocol messages and Agent message formats
- **tRPC Integration**: Seamless integration with the tRPC ecosystem, supporting enterprise-level features like service discovery and load balancing

## A2A Integration in tRPC-Agent-Go

### Automatic Conversion from Agent to A2A

tRPC-Agent-Go implements seamless conversion from Agent to A2A service through the `server/a2a` package:

```go
func New(opts ...Option) (*a2a.A2AServer, error) {}
```

### Automatic AgentCard Generation

The framework automatically extracts Agent metadata (name, description, etc.) to generate an AgentCard that complies with the A2A protocol.

### Message Protocol Conversion

The framework includes a built-in `messageProcessor` that implements bidirectional conversion between A2A protocol messages and Agent message formats, so users don't need to worry about message format conversion details.

## Quick Start

### Creating A2A Services with tRPC-Agent-Go

With just a few lines of code, you can convert any Agent into an A2A service:

#### Basic Example: From Agent to A2A Service

```go
package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

func main() {
	// 1. Create a regular Agent.
	model := openai.New("gpt-4o-mini")
	agent := llmagent.New("MyAgent",
		llmagent.WithModel(model),
		llmagent.WithDescription("An intelligent assistant"),
	)

	// 2. Convert to A2A service with one click.
	server, _ := a2aserver.New(
		a2aserver.WithHost("localhost:8080"),
		a2aserver.WithAgent(agent), // Pass in any Agent
	)

	// 3. Start the service to accept A2A requests.
	server.Start(":8080")
}
```

#### Client Call Example

```go
import (
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func main() {
	// Connect to A2A service.
	client, _ := client.NewA2AClient("http://localhost:8080/")

	// Send message to Agent.
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("Hello, please help me analyze this code")},
	)

	// Agent will automatically process and return results.
	response, _ := client.SendMessage(context.Background(),
		protocol.SendMessageParams{Message: message})
}
```
