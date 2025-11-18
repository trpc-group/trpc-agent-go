# tRPC-Agent-Go Dify Integration Guide

## Overview

tRPC-Agent-Go provides complete integration with the [Dify](https://dify.ai/) platform through the `DifyAgent` component. Developers can easily invoke Dify workflows and chatflows, seamlessly integrating Dify's powerful AI capabilities into tRPC-Agent-Go applications.

### What is Dify?

Dify is an open-source LLM application development platform that provides visual AI workflow orchestration, prompt engineering, RAG (Retrieval-Augmented Generation), and other enterprise-grade features. With Dify, you can quickly build and deploy complex AI applications.

### Core Capabilities

- **Unified Interface**: Use Dify services like regular Agents without understanding Dify API details
- **Protocol Conversion**: Automatically handle conversion between tRPC-Agent-Go message formats and Dify API
- **Streaming Support**: Support both streaming and non-streaming response modes for real-time interaction
- **State Transfer**: Support transferring session state to Dify workflows
- **Custom Extensions**: Support custom request and response converters

## DifyAgent: Calling Dify Services

### Concept Introduction

`DifyAgent` is a special Agent implementation provided by tRPC-Agent-Go that forwards requests to Dify platform workflows or chatflow services. From the user's perspective, `DifyAgent` looks like a regular Agent, but it's actually a local proxy for Dify services.

**Simple Understanding**:
- I have a Dify workflow/chatflow → Call it in tRPC-Agent-Go through DifyAgent
- Use Dify's AI capabilities like a local Agent

### Core Features

- **Transparent Proxy**: Use Dify services like local Agents
- **Automatic Protocol Conversion**: Automatically handle message format conversion with Dify API
- **Streaming Support**: Support both streaming and non-streaming communication modes
- **State Transfer**: Support transferring session state to Dify workflows
- **Custom Processing**: Support custom streaming response handlers
- **Flexible Configuration**: Support custom Dify client configuration

### Use Cases

1. **Enterprise AI Applications**: Call internally deployed Dify services
2. **Workflow Orchestration**: Use Dify's visual workflow capabilities for complex business logic
3. **RAG Applications**: Build intelligent Q&A systems using Dify's knowledge base and RAG capabilities
4. **Multi-Model Integration**: Manage and call multiple LLM models uniformly through Dify

## Quick Start

### Prerequisites

1. **Get Dify Service Information**:
   - Dify service URL (e.g., `https://api.dify.ai/v1`)
   - API Secret (obtained from Dify application settings)

2. **Install Dependencies**:
   ```bash
   go get trpc.group/trpc-go/trpc-agent-go
   go get github.com/cloudernative/dify-sdk-go
   ```

### Basic Usage

#### Example 1: Non-Streaming Chat

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/difyagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// 1. Create DifyAgent
	difyAgent, err := difyagent.New(
		difyagent.WithBaseUrl("https://api.dify.ai/v1"),
		difyagent.WithName("dify-chat-assistant"),
		difyagent.WithDescription("Dify intelligent assistant"),
		difyagent.WithEnableStreaming(false), // Non-streaming mode
		difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
			return dify.NewClientWithConfig(&dify.ClientConfig{
				Host:             "https://api.dify.ai/v1",
				DefaultAPISecret: "your-api-secret",
				Timeout:          30 * time.Second,
			}), nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Create session service
	sessionService := inmemory.NewSessionService()

	// 3. Create Runner
	chatRunner := runner.NewRunner(
		"dify-runner",
		difyAgent,
		runner.WithSessionService(sessionService),
	)

	// 4. Send message
	events, err := chatRunner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("Hello, please introduce yourself"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Process response
	for event := range events {
		if event.Response != nil && len(event.Response.Choices) > 0 {
			if event.Response.Done {
				fmt.Println(event.Response.Choices[0].Message.Content)
			}
		}
	}
}
```

#### Example 2: Streaming Chat

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/difyagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// Custom streaming response handler
	streamingHandler := func(resp *model.Response) (string, error) {
		if len(resp.Choices) > 0 {
			content := resp.Choices[0].Delta.Content
			if content != "" {
				fmt.Print(content) // Real-time printing
			}
			return content, nil
		}
		return "", nil
	}

	// Create DifyAgent with streaming support
	difyAgent, err := difyagent.New(
		difyagent.WithBaseUrl("https://api.dify.ai/v1"),
		difyagent.WithName("dify-streaming-assistant"),
		difyagent.WithDescription("Dify streaming assistant"),
		difyagent.WithEnableStreaming(true), // Enable streaming
		difyagent.WithStreamingRespHandler(streamingHandler),
		difyagent.WithStreamingChannelBufSize(2048), // Streaming buffer size
		difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
			return dify.NewClientWithConfig(&dify.ClientConfig{
				Host:             "https://api.dify.ai/v1",
				DefaultAPISecret: "your-api-secret",
				Timeout:          60 * time.Second,
			}), nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Create session service and Runner
	sessionService := inmemory.NewSessionService()
	chatRunner := runner.NewRunner(
		"dify-streaming-runner",
		difyAgent,
		runner.WithSessionService(sessionService),
	)

	// Send message
	fmt.Print("🤖 Assistant: ")
	events, err := chatRunner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("Please write a short story about a robot learning to paint"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Process streaming response
	for event := range events {
		if event.Error != nil {
			log.Printf("Error: %s", event.Error.Message)
		}
		// streamingHandler will automatically handle and print content
	}
	fmt.Println() // New line
}
```

## Configuration Options

### Basic Configuration

| Option | Description | Required |
|--------|-------------|----------|
| `WithBaseUrl(url)` | Dify service URL | No |
| `WithName(name)` | Agent name | Yes |
| `WithDescription(desc)` | Agent description | No |

### Client Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithGetDifyClientFunc(fn)` | Custom Dify client creation function | Use default config |

### Streaming Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithEnableStreaming(bool)` | Enable streaming mode | false |
| `WithStreamingChannelBufSize(size)` | Streaming buffer size | 1024 |
| `WithStreamingRespHandler(handler)` | Custom streaming response handler | Default handler |

### Advanced Configuration

| Option | Description |
|--------|-------------|
| `WithCustomEventConverter(converter)` | Custom event converter |
| `WithCustomRequestConverter(converter)` | Custom request converter |
| `WithTransferStateKey(keys...)` | Set session state keys to transfer |

## Advanced Usage

### Custom State Transfer

Transfer session state to Dify workflows:

```go
difyAgent, _ := difyagent.New(
	difyagent.WithName("stateful-agent"),
	// Specify state keys to transfer
	difyagent.WithTransferStateKey("user_profile", "conversation_context"),
	// ... other configs
)

// Set state in Runner
runner.Run(
	ctx,
	userID,
	sessionID,
	model.NewUserMessage("Check my orders"),
	// These states will be transferred to Dify
	runner.WithRuntimeState(map[string]interface{}{
		"user_profile": map[string]string{
			"name": "John",
			"vip_level": "gold",
		},
		"conversation_context": "Discussing order issues",
	}),
)
```

### Custom Response Processing

Implement custom streaming response handler:

```go
// Custom handler: filter sensitive words, format output, etc.
customHandler := func(resp *model.Response) (string, error) {
	if len(resp.Choices) == 0 {
		return "", nil
	}
	
	content := resp.Choices[0].Delta.Content
	
	// 1. Filter sensitive words
	filtered := filterSensitiveWords(content)
	
	// 2. Real-time logging
	log.Printf("Received chunk: %s", filtered)
	
	// 3. Real-time display
	fmt.Print(filtered)
	
	// Return processed content (will be aggregated to final response)
	return filtered, nil
}

difyAgent, _ := difyagent.New(
	difyagent.WithStreamingRespHandler(customHandler),
	// ... other configs
)
```

### Custom Request Converter

Implement custom request conversion logic:

```go
type MyRequestConverter struct{}

func (c *MyRequestConverter) ConvertToDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*dify.ChatMessageRequest, error) {
	// Custom request building logic
	req := &dify.ChatMessageRequest{
		Query:           extractQuery(invocation),
		ResponseMode:    getResponseMode(isStream),
		ConversationID:  getConversationID(invocation),
		User:            invocation.RunOptions.UserID,
		Inputs:          customInputs(invocation),
	}
	return req, nil
}

difyAgent, _ := difyagent.New(
	difyagent.WithCustomRequestConverter(&MyRequestConverter{}),
	// ... other configs
)
```

### Custom Event Converter

Implement custom response conversion logic:

```go
type MyEventConverter struct{}

func (c *MyEventConverter) ConvertToEvent(
	resp *dify.ChatMessageResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	// Custom event conversion logic
	return event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: customFormat(resp.Answer),
				},
			}},
		}),
	)
}

func (c *MyEventConverter) ConvertStreamingToEvent(
	resp *dify.ChatMessageStreamResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	// Custom streaming event conversion logic
	// ...
}

difyAgent, _ := difyagent.New(
	difyagent.WithCustomEventConverter(&MyEventConverter{}),
	// ... other configs
)
```

## Environment Variable Configuration

Recommended to use environment variables for sensitive information:

```bash
export DIFY_BASE_URL="https://api.dify.ai/v1"
export DIFY_API_SECRET="app-xxxxxxxxxx"
```

Read in code:

```go
import "os"

difyAgent, _ := difyagent.New(
	difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
		return dify.NewClientWithConfig(&dify.ClientConfig{
			Host:             os.Getenv("DIFY_BASE_URL"),
			DefaultAPISecret: os.Getenv("DIFY_API_SECRET"),
			Timeout:          30 * time.Second,
		}), nil
	}),
	// ... other configs
)
```

## Error Handling

### Common Errors and Solutions

| Error | Cause | Solution |
|-------|-------|----------|
| `agent name is required` | Agent name not set | Use `WithName()` to set name |
| `request converter not set` | Request converter not set | Use default or custom converter |
| `Dify request failed` | Dify API call failed | Check network, API Secret, service URL |
| `event converter not set` | Event converter not set | Use default or custom converter |

### Error Event Processing

```go
for event := range events {
	// Check for errors
	if event.Error != nil {
		log.Printf("Error: %s", event.Error.Message)
		continue
	}
	
	// Check response errors
	if event.Response != nil && event.Response.Error != nil {
		log.Printf("Response Error: %s", event.Response.Error.Message)
		continue
	}
	
	// Normal processing
	// ...
}
```

## Best Practices

### 1. Timeout Configuration

Set reasonable timeout based on Dify workflow complexity:

```go
difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
	return dify.NewClientWithConfig(&dify.ClientConfig{
		Host:             baseURL,
		DefaultAPISecret: apiSecret,
		Timeout:          60 * time.Second, // Longer timeout for complex workflows
	}), nil
})
```

### 2. Streaming Buffer Size

Adjust buffer based on expected response length:

```go
// Short responses
difyagent.WithStreamingChannelBufSize(512)

// Long responses (e.g., article generation)
difyagent.WithStreamingChannelBufSize(4096)
```

### 3. Error Retry

Implement retry mechanism in production:

```go
func runWithRetry(runner *runner.Runner, maxRetries int) error {
	for i := 0; i < maxRetries; i++ {
		events, err := runner.Run(ctx, userID, sessionID, msg)
		if err == nil {
			// Process events
			return nil
		}
		log.Printf("Retry %d/%d: %v", i+1, maxRetries, err)
		time.Sleep(time.Second * time.Duration(i+1))
	}
	return fmt.Errorf("max retries exceeded")
}
```

### 4. Session Management

Properly manage session IDs to maintain conversation context:

```go
// Use unique session ID for each user conversation
sessionID := fmt.Sprintf("user-%s-conv-%d", userID, time.Now().Unix())

// Or use persistent session ID for long-term conversations
sessionID := getUserSessionID(userID)
```

## Example Code

Complete examples are located in the project's `examples/dify/` directory:

- `basic_chat/`: Basic non-streaming chat example
- `streaming_chat/`: Streaming chat example
- `advanced_usage/`: Advanced usage examples (state transfer, custom converters, etc.)

Run examples:

```bash
cd examples/dify/basic_chat
export DIFY_BASE_URL="https://api.dify.ai/v1"
export DIFY_API_SECRET="your-api-secret"
go run main.go
```

## FAQ

**Q: What's the difference between DifyAgent and A2AAgent?**

A: 
- `DifyAgent`: Specifically for calling Dify platform services using Dify API
- `A2AAgent`: For calling remote Agent services that comply with A2A protocol
- Choice depends on backend service type

**Q: How to use self-hosted Dify in enterprise intranet?**

A: Simply set `WithBaseUrl()` to the intranet Dify service address, e.g.:
```go
difyagent.WithBaseUrl("http://dify.internal.company.com/v1")
```

**Q: How to choose between streaming and non-streaming modes?**

A:
- **Streaming**: Suitable for long text generation, scenarios requiring real-time feedback
- **Non-streaming**: Suitable for short responses, batch processing scenarios

**Q: How to debug Dify requests?**

A: Implement a custom request converter with logging:
```go
func (c *MyConverter) ConvertToDifyRequest(...) (*dify.ChatMessageRequest, error) {
	req := &dify.ChatMessageRequest{...}
	log.Printf("Dify Request: %+v", req)
	return req, nil
}
```

## Related Resources

- [Dify Official Documentation](https://docs.dify.ai/)
- [Dify SDK Go](https://github.com/cloudernative/dify-sdk-go)
- [tRPC-Agent-Go Documentation](https://github.com/trpc-group/trpc-agent-go/blob/main/README.md)
- [A2A Integration Guide](./a2a.md)
