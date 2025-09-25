# Graph Debug Server Example

This example demonstrates both **LLM Agent** and **Graph Agent** working together in a debug server environment, connected to [ADK Web](https://github.com/google/adk-web). It showcases:

- Running both LLM Agent and Graph Agent in the same server
- Mathematical calculation workflows using Graph Agent
- Tool integration and execution patterns
- ADK Web UI integration for visual debugging
- Streaming responses and real-time event handling

## Features

### Simple Agent (LLM Agent)
- Direct tool calling for mathematical calculations
- Streaming responses with real-time updates
- Simple request-response pattern

### Workflow Agent (Graph Agent)
- Multi-stage mathematical processing pipeline:
  1. **Parse Input** - Extract and validate mathematical expressions
  2. **Format Result** - Present calculations in user-friendly format
- State management and conditional workflow execution
- Tool execution with detailed metadata tracking

## Prerequisites

- Go 1.21 or later
- NodeJS & npm (for running ADK Web UI)
- Valid OpenAI-compatible API key

## Running the Server

```bash
# From repository root
cd examples/graph_debugserver

# Build and start the server
go build -o graph_debugserver main.go
OPENAI_BASE_URL="https://api.deepseek.com/v1" OPENAI_API_KEY="your-api-key" ./graph_debugserver -addr :8080

# Or run directly
OPENAI_BASE_URL="https://api.deepseek.com/v1" OPENAI_API_KEY="your-api-key" go run main.go -addr :8080
```

### Command Line Options

- `-model`: Name of the model to use (default: "deepseek-chat")
- `-addr`: Listen address (default: ":8080")

## Running ADK Web UI

Clone and serve the front-end:

```bash
git clone https://github.com/google/adk-web.git
cd adk-web
npm install

# Point the UI to our Go backend
npm run serve --backend=http://localhost:8080 -- --port=4200 --host=localhost
```

Open <http://localhost:4200> in your browser. You'll see two available agents:

- **simple-agent**: Direct LLM agent for quick calculations
- **workflow-agent**: Graph-based agent with structured workflow

## Usage Examples

### Simple Agent
Ask mathematical questions like:
- "What is 15 + 23?"
- "Calculate 45 * 67"
- "Divide 100 by 4"

### Workflow Agent
The Graph Agent processes mathematical expressions through a structured workflow:
- Input parsing and validation
- Calculation execution via tools
- Result formatting and presentation

## Architecture

### Simple Agent Flow
```
User Input → LLM → Tool Call → Tool Response → Final Answer
```

### Graph Agent Flow
```
User Input → Parse Input Node → Calculator Tool → Format Result Node → Final Answer
```

## State Keys (Graph Agent)

The workflow uses these state keys:
- `original_expression`: The input mathematical expression
- `parsed_numbers`: Extracted numbers from input
- `operation`: Mathematical operation to perform
- `messages`: LLM conversation history
- `user_input`: Original user input
- `last_response`: Most recent response

## Tools Available

Both agents have access to:
- **Calculator Tool**: Performs basic arithmetic operations (add, subtract, multiply, divide)

## Development

To customize the example:

1. **Add new tools**: Extend the calculator or add new function tools
2. **Modify workflows**: Update the Graph Agent's node structure
3. **Adjust prompts**: Customize LLM instructions for different behaviors
4. **Add new agents**: Register additional agents in the server

## API Endpoints

When running, the server exposes:
- `POST /agent/simple-agent/chat` - LLM Agent endpoint
- `POST /agent/workflow-agent/chat` - Graph Agent endpoint
- Both support streaming via `"stream": true` in request body

## Requirements

- Valid OpenAI-compatible API endpoint
- API key for model access
- Network connectivity for LLM calls

Note: The example uses DeepSeek API by default. Adjust `OPENAI_BASE_URL` and `OPENAI_API_KEY` for other providers.
