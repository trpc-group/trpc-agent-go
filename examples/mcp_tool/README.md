# MCP Tool Example

This example demonstrates how trpc-agent-go supports MCP (Model-Client-Protocol) tools, showcasing both STDIO and Streamable HTTP implementations for building intelligent AI assistants.

## What are MCP Tools?

The trpc-agent-go framework provides built-in support for MCP tools with these key capabilities:

- **🔄 Multiple Tool Types**: Native support for Function tools, STDIO MCP tools, and Streamable HTTP tools
- **🌊 Streaming Responses**: Real-time character-by-character response generation
- **💾 Tool State Management**: Proper handling of tool calls and responses
- **🔧 Simple Tool Implementations**: Focused examples with minimal complexity
- **🚀 LLM Integration**: Seamless use of tools with language models

### Key Features

- **STDIO MCP Server**: Local server with echo and add tools
- **Streamable HTTP Server**: HTTP server with weather and news tools
- **Direct Tool Testing**: Test tools directly without LLM
- **LLM Integration**: Use tools with an LLM agent for intelligent conversations
- **Multi-turn Chat**: Support for conversational tool usage
- **Tool Visualization**: Clear indication of tool calls, arguments, and responses

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `deepseek-chat` |

## Usage

### Start the HTTP Server

```bash
cd streamalbe_server
go run main.go
```

### Run the Example

```bash
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

## Project Structure

```
mcp-tool/
├── main.go                # Main runner with interactive chat and direct tool testing
├── stdio_server/
│   └── main.go            # Simple STDIO MCP server with echo and add tools
├── streamalbe_server/     # Note: Directory name has a typo, maintained for compatibility
│   └── main.go            # Simple HTTP MCP server with weather and news tools
└── README.md              # This document
```

## Tool Types

This example demonstrates three types of tools:

1. **Function Tools**: Direct Go function implementations
   - `calculator`: Perform basic math operations
   - `current_time`: Get current time in different formats

2. **STDIO MCP Tools**: Tools provided via standard I/O
   - `echo`: Echo back a message with optional prefix
   - `add`: Add two numbers together

3. **Streamable HTTP MCP Tools**: Tools provided via HTTP
   - `get_weather`: Get weather information for a location (simulated)
   - `get_news`: Get news headlines by category (simulated)

## Configuring MCP Tools in Your Agent

### STDIO MCP Tool Configuration

To integrate STDIO MCP tools into your agent, use the following code:

```go
// Configure STDIO MCP to connect to our local server.
mcpConfig := mcp.MCPConnectionConfig{
    Transport: "stdio",
    Command:   "go",
    Args:      []string{"run", "./stdio_server/main.go"},
    Timeout:   10 * time.Second,
}

// Create MCP toolset
stdioToolSet := mcp.NewMCPToolSet(mcpConfig,
    mcp.WithToolFilter(mcp.NewIncludeFilter("echo", "add")),
)

// Get tools from the STDIO server
stdioTools := stdioToolSet.Tools(ctx)
```

### Streamable HTTP Tool Configuration

To integrate Streamable HTTP tools into your agent, use the following code:

```go
// Configure Streamable HTTP MCP connection.
streamableConfig := mcp.MCPConnectionConfig{
    Transport: "streamable_http",
    ServerURL: "http://localhost:3000/mcp",
    Timeout:   10 * time.Second,
}

// Create MCP toolset with enterprise-level configuration.
streamableToolSet := mcp.NewMCPToolSet(streamableConfig,
    mcp.WithRetry(mcp.RetryConfig{
        Enabled:       true,
        MaxAttempts:   3,
        InitialDelay:  time.Second,
        BackoffFactor: 2.0,
        MaxDelay:      30 * time.Second,
    }),
    mcp.WithToolFilter(mcp.NewIncludeFilter("get_weather", "get_news")),
)

// Get tools from the HTTP server
streamableTools := streamableToolSet.Tools(ctx)
```

### Combining Tools for Use in LLM Agent

```go
// Combine all tools
allTools := make([]tool.Tool, 0, 2+len(stdioTools)+len(streamableTools))
allTools = append(allTools, calculatorTool, timeTool) // Function tools
allTools = append(allTools, stdioTools...)            // STDIO MCP tools
allTools = append(allTools, streamableTools...)       // Streamable HTTP tools

// Create LLM agent with tools
llmAgent := llmagent.New(
    agentName,
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A helpful AI assistant"),
    // ... other configurations
    llmagent.WithTools(allTools), // Pass all tools to the agent
)
```

## Running the Example

### Prerequisites

Ensure you have Go installed and set up properly.

### Starting the Servers

1. **Start the Streamable HTTP Server**:
   ```bash
   cd streamalbe_server
   go run main.go
   ```

2. **Run the Main Example**:
   ```bash
   go run main.go
   ```

## Example Conversation

Here's an example of interacting with the assistant:

```
✅ Chat ready! Session: chat-session-1751367391

👤 You: Hello!
🤖 Assistant: Hi there! How can I assist you today? 😊

👤 You: Do you have any tools?
🤖 Assistant: Yes! I have a few handy tools to help with calculations, time queries, and more. Here's what I can do:

1. **Calculator**: Perform basic math operations like addition, subtraction, multiplication, and division.
2. **Time Tools**: Get the current time and date for any timezone.
3. **Weather**: Fetch the current weather for a specific location.
4. **News**: Retrieve the latest news headlines (optional: by category).
5. **Echo**: A simple tool that repeats back your message (for fun or testing).

Let me know what you'd like to use, and I'll assist! 😊

👤 You: I want to know the weather of Shenzhen.        
🤖 Assistant: 🔧 CallableTool calls initiated:
   • get_weather (ID: call_0_d2b56dbb-ba74-47f8-9e2a-e868db0952ac)
     Args: {"location":"Shenzhen"}

🔄 Executing tools...
✅ CallableTool response (ID: call_0_d2b56dbb-ba74-47f8-9e2a-e868db0952ac): {"text":"Weather for Shenzhen: 22°C, Sunny, Humidity: 45%, Wind: 10 km/h","type":"text"}

🤖 Assistant: The current weather in Shenzhen is **22°C** and **Sunny**. Here are the details:  
- **Humidity**: 45%  
- **Wind Speed**: 10 km/h  

Enjoy the pleasant weather! 😊
```

## Tool Descriptions

### Function Tools

1. **calculator**
   - **Description**: Perform basic mathematical calculations
   - **Parameters**:
     - `operation`: The operation to perform (add, subtract, multiply, divide)
     - `a`: First number
     - `b`: Second number

2. **current_time**
   - **Description**: Get the current time and date
   - **Parameters**:
     - `timezone`: Timezone (UTC, EST, PST, CST) or leave empty for local

### STDIO MCP Tools

1. **echo**
   - **Description**: Simple echo tool that returns the input message with an optional prefix
   - **Parameters**:
     - `message`: The message to echo
     - `prefix`: Optional prefix, default is 'Echo: '

2. **add**
   - **Description**: Simple addition tool that adds two numbers
   - **Parameters**:
     - `a`: First number
     - `b`: Second number

### Streamable HTTP MCP Tools

1. **get_weather**
   - **Description**: Get current weather for a location
   - **Parameters**:
     - `location`: City name or location

2. **get_news**
   - **Description**: Get latest news headlines
   - **Parameters**:
     - `category`: News category (default: "general")

## Implementation Details

The example demonstrates:

1. **Tool Integration**: How to integrate different types of tools (function, STDIO, HTTP)
2. **Direct Testing**: How to test tools directly without going through an LLM
3. **Interactive Chat**: How to use tools in an interactive chat session with an LLM

This serves as a practical reference for building your own tool-enabled AI assistants. 