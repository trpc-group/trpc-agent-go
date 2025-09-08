# A2A Sub-Agent System Example

This is an A2A (Agent-to-Agent) protocol example based on trpc-agent-go, demonstrating how to create, deploy, and interact with multiple AI agents. The system adopts a coordinator pattern, managing multiple specialized sub-agents through a central coordinator agent.

## Project Structure

```
examples/a2asubagent/
├── agents/                    # AI agent servers
│   ├── calculator/           # Calculator agent (port 8087)
│   │   └── calculate_agent.go
│   └── codecheck/            # Code check agent (port 8088)
│       ├── codecc_agent.go
│       ├── codecc_tool.go
│       └── spec.txt
├── client/                   # A2A interactive client
│   └── client.go
├── README.md                 # This file
└── start.sh                  # Quick start script
```

## System Architecture

The system adopts a coordinator pattern and includes the following components:

- **Coordinator Agent**: Serves as the system entry point, responsible for receiving user requests and dispatching tasks to appropriate sub-agents
- **Calculator Agent**: Specialized in handling mathematical calculation tasks
- **CodeCheck Agent**: Specialized in analyzing Go code quality and standard compliance
- **A2A Client**: Provides user interaction interface with support for agent transfer and streaming responses

## Quick Start

### 1. Environment Configuration

First, set up the necessary environment variables:

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

### 2. One-Click Service and Client Startup

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
# Terminal 1: Start calculator agent
cd examples/a2asubagent/agents/calculator
go run calculate_agent.go -host 0.0.0.0:8087

# Terminal 2: Start code check agent
cd examples/a2asubagent/agents/codecheck
go run codecc_agent.go -host 0.0.0.0:8088
```

### 2. Connect Using Client

```bash
# Terminal 3: Start coordinator client
cd examples/a2asubagent/client
go run client.go
```

## Agent Descriptions

### Coordinator Agent

- **Function**: Serves as the system entry point, managing and coordinating multiple sub-agents
- **Features**: Intelligent task dispatch, agent transfer, streaming responses
- **Icon**: 🎯

### Calculator Agent

- **Port**: 8087
- **Function**: Handles mathematical calculation tasks
- **URL**: http://localhost:8087/
- **Icon**: 🧮
- **Agent Card**: http://localhost:8087/.well-known/agent.json

### CodeCheck Agent

- **Port**: 8088
- **Function**: Analyzes Go code quality and checks Go language standard compliance
- **URL**: http://localhost:8088/
- **Icon**: 🔍
- **Agent Card**: http://localhost:8088/.well-known/agent.json

## Usage Examples

### Chatting with the Coordinator Agent

The following are actual usage examples demonstrating how the coordinator intelligently dispatches tasks to appropriate sub-agents:

```bash
$ ./client
------- Coordinator Agent -------
agent_coordinator: You are a coordinator assistant that manages multiple sub-agents. You can handle user requests directly or delegate them to appropriate sub-agents.
--------------------------------

Chat with the coordinator agent. Type 'new' for a new session, or 'exit' to quit.

# Example 1: Mathematical calculation task
User: calculate 123
🎯 Coordinator: I'll transfer you to the calculator agent to help with this calculation.
🔄 Initiating transfer...
The
🔄 Transfer Event: Transferring control to agent: calculator
🧮 Calculator: 123 is a number, but I'm not sure what calculation you'd like to perform with it. Could you please specify what you'd like to do? For example, do you want to:

- Add, subtract, multiply, or divide it with another number?
- Find its square root or other mathematical operations?
- Convert it to a different numeral system (like binary or hexadecimal)?
- Or something else?

Let me know, and I'll be happy to help! calculator agent will now help you with your calculation request.

# Example 2: Go language standard query
User: query golang spec
🎯 Coordinator: I'll transfer you to the CodeCheckAgent which specializes in Go language standards and specifications.
🔄 Initiating transfer...
Transfer
🔄 Transfer Event: Transferring control to agent: CodeCheckAgent
🔍 Code Checker: I'll help you read the Go language standard specification to understand the language's standards and requirements. Let me access the specification document for you.

## Go Language Standard Specification

### 1.1 [REQUIRED] Formatting
- **All code must be formatted using `gofmt`** - This is a mandatory requirement for all Go code

### 1.2 [RECOMMENDED] Line Breaks
- It is recommended that a line of code should not exceed **120 columns**
- If a line exceeds this limit, use reasonable line breaking methods
- **Exceptions** to this recommendation include:
  - Function signatures (though if they have too many parameters, you should reconsider the design)
  - Long string literals (if they contain newline characters `\n`, consider using raw string literals `` `raw string literal` ``)
  - Import module statements
  - Tool-generated code
  - Struct tags

This specification emphasizes the importance of consistent formatting using `gofmt` and provides guidelines for maintaining readable code through proper line length management.

Would you like me to help you analyze specific Go code against these standards or do you have any questions about these specifications?
```

## Core Features

### 1. Intelligent Agent Transfer

- Coordinator automatically identifies task types and transfers to appropriate sub-agents
- Supports real-time transfer event display (🔄 Transfer Event)
- Each agent has unique icons and display names

### 2. Streaming Response Processing

- Supports real-time streaming content display
- Tool invocation process visualization (🔧 executing tools)
- Tool completion status indication (✅ Tool completed)

### 3. Multi-Agent Collaboration

- Calculator Agent: Handles mathematical operations and numerical calculations
- Code Check Agent: Analyzes Go code quality and standard compliance
- Coordinator Agent: Intelligent task dispatch and session management

### 4. User-Friendly Interface

- Clear agent identity identification
- Real-time transfer status display
- Support for new session creation (type 'new')
- Graceful exit mechanism (type 'exit')

## Advanced Configuration

### Custom Host and Port

```bash
# Start agents on custom ports
./calculate_agent -host 0.0.0.0:8087 -model deepseek-chat
./codecc_agent -host 0.0.0.0:8088 -model deepseek-chat
```

### Model Configuration

```bash
# Use different models
export OPENAI_MODEL="gpt-4"
export OPENAI_MODEL="deepseek-chat"
```

### Client Configuration

The client automatically connects to the following sub-agents:

- http://localhost:8087/ (Calculator Agent)
- http://localhost:8088/ (Code Check Agent)

You can configure different agent addresses by modifying the `agentURLS` variable in `client.go`.

## Troubleshooting

### Common Issues

1. **Connection Failure**

   ```bash
   # Check if agents are running
   curl http://localhost:8087/.well-known/agent.json
   curl http://localhost:8088/.well-known/agent.json
   ```

2. **API Key Error**

   ```bash
   # Verify environment variable settings
   echo $OPENAI_API_KEY
   echo $OPENAI_BASE_URL
   echo $OPENAI_MODEL
   ```

3. **Port Occupation**

   ```bash
   # Check port usage
   lsof -i :8087
   lsof -i :8088
   ```

4. **Agent Transfer Issues**
   - Ensure all sub-agents are started and accessible
   - Check coordinator agent's sub-agent configuration
   - Verify agent cards (/.well-known/agent.json) are accessible

## Technical Implementation

### Agent Transfer Mechanism

- Uses `transfer_to_agent` tool to implement inter-agent transfers
- Supports real-time transfer event handling and status tracking
- Each agent maintains independent session state

### Streaming Processing

- Event-driven streaming response processing
- Supports tool invocation visualization and status feedback
- Optimized content display logic to avoid duplicate output

## More Information

- [trpc-agent-go Documentation](https://github.com/trpc-group/trpc-agent-go)
- [A2A Protocol Specification](https://a2a-spec.org/)
- [OpenAI API Documentation](https://platform.openai.com/docs)
