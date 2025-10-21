# A2A Agent Example

This example demonstrates the Agent-to-Agent (A2A) communication functionality in trpc-agent-go, showing how to create and communicate with remote agents using the A2A protocol.

## Overview

The A2A system enables distributed agent communication across network boundaries:

1. **A2A Server**: Hosts a remote agent and exposes it via A2A protocol
2. **A2A Client**: Connects to remote agents and communicates seamlessly
3. **Agent Discovery**: Automatic agent card resolution from well-known endpoints
4. **Protocol Translation**: Converts between local agent events and A2A protocol messages

## Architecture

```
┌─────────────────────┐    A2A Protocol     ┌─────────────────────┐
│   Local Client      │ ◄─────────────────► │   Remote Server     │
│   ️                  │    HTTP/JSON        │                     │
│                     │                     │                     │
│ ┌─────────────────┐ │                     │ ┌─────────────────┐ │
│ │   A2A Agent     │ │                     │ │   LLM Agent     │ │
│ │   (Proxy)       │ │                     │ │   (Actual)      │ │
│ └─────────────────┘ │                     │ └─────────────────┘ │
└─────────────────────┘                     └─────────────────────┘
```

## Components

### A2A Server
- **Purpose**: Hosts and exposes local agents via A2A protocol
- **Features**: 
  - Agent card publishing at `/.well-known/agent.json`
  - HTTP endpoint for A2A message handling
  - Protocol message conversion
- **Configuration**: Host binding and agent registration

### A2A Agent (Client)
- **Purpose**: Proxy agent that communicates with remote A2A servers
- **Features**:
  - Automatic agent discovery via well-known paths
  - HTTP client configuration with timeout support
  - Message translation between local events and A2A protocol
- **Configuration**: Agent card URL or direct agent card object

### Remote LLM Agent
- **Purpose**: The actual agent running on the remote server
- **Features**: 
  - OpenAI-compatible model integration
  - Configurable generation parameters
  - Standard agent interface implementation
- **Models**: Supports various models (DeepSeek, GPT, etc.)

## Building and Running

```bash
# Build the example
cd examples/a2aagent
go build -o a2a-demo .

# Run with default settings (deepseek-chat model, port 8888)
./a2a-demo

# Run with custom model and port
./a2a-demo -model gpt-4o-mini -host 0.0.0.0:9999
```

## Environment Setup

Set the required API keys for your chosen model:

```bash
# For DeepSeek
export OPENAI_API_KEY="your-deepseek-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"

# For OpenAI
export OPENAI_API_KEY="your-openai-api-key"
# OPENAI_BASE_URL not needed for OpenAI
```

## Example Interaction

```
$ ./a2a-demo

------- Agent Card -------
Name: agent_joker
Description: i am a remote agent, i can tell a joke
URL: http://0.0.0.0:8888
------------------------
Chat with the agent. Type 'new' for a new session, or 'exit' to quit.
User: tell me a joke
======== remote agent ========
🤖 Assistant: Why don't scientists trust atoms?

Because they make up everything! 😄

======== local agent ========
🤖 Assistant: Here's a joke for you:

Why did the programmer quit his job?

Because he didn't get arrays! (a raise) 😄
```

## Key Features

### Dual Agent Comparison
- Runs both remote A2A agent and local agent for comparison
- Shows responses from both agents side by side
- Demonstrates the transparency of A2A protocol

### Automatic Agent Discovery
- Fetches agent cards from `/.well-known/agent.json` endpoints
- Validates agent metadata and capabilities
- Configures client based on discovered information

### Interactive Chat Interface
- Real-time conversation with agents
- Session management with 'new' command
- Graceful exit with 'exit' command
- Visual separation between remote and local agent responses

### Protocol Translation
- Converts local `model.Message` to A2A `protocol.Message`
- Handles different message types (text, tasks, etc.)
- Maintains conversation context across protocol boundaries

### Error Handling
- Network timeout configuration (60 seconds for agent requests)
- Connection failure recovery
- Protocol error reporting

### Flexible Configuration
- Custom HTTP client support
- Configurable timeouts
- Multiple agent card resolution methods
- Streaming and non-streaming mode support

## Code Structure

### Main Components

1. **Server Setup** (`runRemoteAgent`)
   ```go
   // Create LLM agent
   remoteAgent := buildRemoteAgent(*modelName)
   
   // Start A2A server
   server, err := a2a.New(
       a2a.WithHost(*host),
       a2a.WithAgent(remoteAgent),
   )
   server.Start(*host)
   ```

2. **Client Setup** (`startChat`)
   ```go
   // Create A2A agent client
   a2aAgent, err := a2aagent.New(
       a2aagent.WithAgentCardURL(httpURL)
   )
   
   // Use with runner
   remoteRunner := runner.NewRunner("test", a2aAgent)
   localRunner := runner.NewRunner("test", localAgent)
   ```

3. **Agent Configuration** (`buildRemoteAgent`)
   ```go
   llmAgent := llmagent.New(
       agentName,
       llmagent.WithModel(modelInstance),
       llmagent.WithDescription(desc),
       llmagent.WithInstruction(desc),
       llmagent.WithGenerationConfig(genConfig),
   )
   ```

## Configuration Options

### Command Line Flags
- `-model`: Model name (default: "deepseek-chat")
- `-host`: Server host and port (default: "0.0.0.0:8888")
- `-streaming`: Enable streaming mode (default: true)
- `-remote-only`: Only output remote agent responses (default: false)

### A2A Agent Options
- `WithAgentCard()`: Use pre-configured agent card
- `WithAgentCardURL()`: Auto-discover from URL
- `WithHTTPClient()`: Custom HTTP client
- `WithTimeout()`: Request timeout
- `WithUserIDHeader()`: Custom HTTP header name for sending UserID to server (default: "X-User-ID")

### A2A Server Options
- `WithHost()`: Server host and port binding
- `WithAgent()`: The agent to expose and streaming mode
- `WithUserIDHeader()`: Custom HTTP header name for reading UserID from client (default: "X-User-ID")
- `WithDebugLogging()`: Enable debug logging
- `WithErrorHandler()`: Custom error handler

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
        client.WithRequestHeader("X-Request-ID", "req-12345"),
        client.WithRequestHeader("Authorization", "Bearer token"),
    ),
)
```

**Use Cases:**
- **Authentication**: Pass authentication tokens via `Authorization` header
- **Tracing**: Add request IDs for distributed tracing

**Configuring UserID Header:**

Both A2A Agent (client) and A2A Server support configuring which HTTP header to use for UserID. The default is `X-User-ID`.

```go
// Client side: Configure which header to send UserID in
a2aAgent, err := a2aagent.New(
    a2aagent.WithAgentCardURL("http://localhost:8888"),
    // Default is "X-User-ID", can be customized
    a2aagent.WithUserIDHeader("X-Custom-User-ID"),
)

// Server side: Configure which header to read UserID from
server, err := a2a.New(
    a2a.WithHost("localhost:8888"),
    a2a.WithAgent(agent, true),
    // Default is "X-User-ID", can be customized
    a2a.WithUserIDHeader("X-Custom-User-ID"),
)
```

The UserID from `invocation.Session.UserID` will be automatically sent via the configured header to the A2A server.

## Future Enhancements

The following features are planned for the next version:

- **Streaming Protocol Support**: Enhanced streaming capabilities for real-time agent communication
- **tRPC Ecosystem Integration**: Native integration with tRPC framework for improved performance and compatibility

## Troubleshooting

### Common Issues

1. **API Key Issues**
   - Verify environment variables are set
   - Check API key validity and permissions
   - Confirm base URL for non-OpenAI providers
