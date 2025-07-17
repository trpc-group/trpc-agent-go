# A2A (Agent-to-Agent) Example

This is an A2A protocol example for trpc-agent-go, demonstrating how to create, deploy, and interact with multiple AI agents.

## Project Structure

```
examples/a2a/
├── agents/                    # AI agent servers
│   ├── entrance/             # Entrance agent (port 8081)
│   │   └── entrance_agent.go
│   ├── codecheck/            # Code check agent (port 8082)  
│   │   ├── codecc_agent.go
│   │   ├── codecc_tool.go
│   │   └── spec.txt
│   └── agent_utils.go        # Agent utility functions
├── client/                   # A2A interactive client
│   └── client.go
├── registry/                 # Agent registration service
│   └── registry.go
├── README.md                 # This file
└── start.sh                  # Quick start script
```

## Quick Start

### 1. Environment Configuration

First, set the necessary environment variables:

```bash
# OpenAI API configuration (required)
export OPENAI_API_KEY="your-openai-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # Optional, default value
export OPENAI_MODEL="gpt-4o-mini"                   # Optional, default value

# Or use other compatible API services
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_MODEL="deepseek-chat"
```

### 2. One-click Service and Client Launch

```bash
# Use the provided startup script
chmod +x start.sh
./start.sh
```

## Manual Startup

### 1. Start Agent Servers

Start agents in different terminal windows:

```bash
# Note the startup order
# Terminal 1: Start CodeCheck Agent
cd examples/a2a/agents/codecheck
./codecc_agent

# Terminal 2: Start Entrance Agent
cd examples/a2a/agents/entrance
./entrance_agent

```

### 2. Connect Using the Client

```bash
# Terminal 3: Connect to the entrance agent
cd examples/a2a/client
./client -url http://localhost:8081

# Or connect to the code check agent
./client -url http://localhost:8082
```

## Agent Descriptions

### Entrance Agent
- **Port**: 8081  
- **Function**: Acts as the system entry point, can call other agents
- **URL**: http://localhost:8081
- **Agent Card**: http://localhost:8081/.well-known/agent.json

### Code Check Agent
- **Port**: 8082
- **Function**: Analyzes Go code quality, checks compliance with Go language standards
- **URL**: http://localhost:8082  
- **Agent Card**: http://localhost:8082/.well-known/agent.json



## Usage Examples

### Conversing with the Entrance Agent

```bash
$ ./client -url http://localhost:8081
🚀 A2A Interactive Client
Agent URL: http://localhost:8081
Type 'exit' to quit
==================================================
🔗 Connecting to agent...
✅ Connected to agent: EntranceAgent
📝 Description: A entrance agent, it will delegate the task to the sub-agent by a2a protocol, or try to solve the task by itself
🏷️  Version: 1.0.0
🛠️  Skills:
   • non_streaming_CodeCheckAgent: Send non-streaming message to CodeCheckAgent agent: Check code quality by Go Language Standard; Query the golang standard/spec that user needed

💬 Start chatting (type 'exit' to quit):

👤 You: query golang standard
📤 Sending message to agent...
🤖 Agent: The Go Language Standard includes the following guidelines:

### 1.1 [REQUIRED] Formatting
- All code must be formatted using `gofmt`.

### 1.2 [RECOMMENDED] Line Breaks
- A line of code should not exceed `120 columns`. If it does, use reasonable line-breaking methods.
- Exceptions:
  - Function signatures (though this might indicate too many parameters).
  - Long string literals (if they contain newline characters `\n`, consider using raw string literals `` `raw string literal` ``).
  - Import module statements.
  - Tool-generated code.
  - Struct tags.

Would you like me to analyze or check any specific code against these standards?

conversation finished ctx id: ctx-342a8319-1650-4bd8-a899-72c4dff09c0b
👤 You: exit
👋 Goodbye!
```


## Using A2A Inspector to Access A2A Services (Optional)

A2A Inspector is a web interface tool for monitoring and debugging A2A communications.

### 1. Start A2A Inspector

```bash
# Run A2A Inspector using Docker
sudo docker run -d -p 8080:8080 a2a-inspector   


### 2. Access the Inspector Interface

Open your browser and visit: http://localhost:8080

### 3. Configure Agent Monitoring

Chat with the Agent in the web page

```

## Advanced Configuration

### Custom HOST

```bash
# Start agents on custom ports
./entrance_agent -host 0.0.0.0
./codecc_agent -host 0.0.0.0
```

### Model Configuration

```bash
# Use different models
export OPENAI_MODEL="gpt-4"
export OPENAI_MODEL="claude-3-sonnet"
export OPENAI_MODEL="deepseek-chat"
```


## Troubleshooting

### Common Issues

1. **Connection Failure**
   ```bash
   # Check if agents are running
   curl http://localhost:8081/.well-known/agent.json
   curl http://localhost:8082/.well-known/agent.json
   ```

2. **API Key Error**
   ```bash
   # Verify environment variable settings
   echo $OPENAI_API_KEY
   echo $OPENAI_BASE_URL
   ```

3. **Port Occupation**
   ```bash
   # Check port usage
   lsof -i :8081
   lsof -i :8082
   ```

## More Information

- [trpc-agent-go Documentation](https://github.com/trpc-group/trpc-agent-go)
- [A2A Protocol Specification](https://a2a-spec.org/)
- [OpenAI API Documentation](https://platform.openai.com/docs)