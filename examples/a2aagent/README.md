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
User: tell me a joke 
Agent: Why don't scientists trust atoms?

Because they make up everything! 😄

Here's the setup: Atoms are the basic building blocks of matter, and when we say they "make up" everything, it has a double meaning - they literally compose all matter, but "make up" can also mean "fabricate" or "lie." So the joke plays on this wordplay to create a silly pun about atoms being untrustworthy because they "make up" (fabricate) everything!
```

## Key Features

### Automatic Agent Discovery
- Fetches agent cards from `/.well-known/agent.json` endpoints
- Validates agent metadata and capabilities
- Configures client based on discovered information

### Protocol Translation
- Converts local `model.Message` to A2A `protocol.Message`
- Handles different message types (text, tasks, etc.)
- Maintains conversation context across protocol boundaries

### Error Handling
- Network timeout configuration
- Connection failure recovery
- Protocol error reporting

### Flexible Configuration
- Custom HTTP client support
- Configurable timeouts (default: 120s)
- Multiple agent card resolution methods

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

2. **Client Setup** (`callRemoteAgent`)
   ```go
   // Create A2A agent client
   a2aAgent, err := a2aagent.New(
       a2aagent.WithAgentCardURL(a2aURL)
   )
   
   // Use with runner
   runner := runner.NewRunner(agentCard.Name, a2aAgent, 
       runner.WithSessionService(sessionService))
   ```

3. **Agent Configuration** (`buildRemoteAgent`)
   ```go
   llmAgent := llmagent.New(
       "remoteAgent",
       llmagent.WithModel(modelInstance),
       llmagent.WithDescription(desc),
       llmagent.WithGenerationConfig(genConfig),
   )
   ```

## Configuration Options

### Command Line Flags
- `-model`: Model name (default: "deepseek-chat")
- `-host`: Server host and port (default: "0.0.0.0:8888")

### A2A Agent Options
- `WithAgentCard()`: Use pre-configured agent card
- `WithAgentCardURL()`: Auto-discover from URL
- `WithHTTPClient()`: Custom HTTP client
- `WithTimeout()`: Request timeout

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
