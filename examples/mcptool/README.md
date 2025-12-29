# MCP Tool Example

This example demonstrates how trpc-agent-go supports MCP (Model-Client-Protocol) tools, showcasing both STDIO, SSE, and Streamable HTTP implementations for building intelligent AI assistants.

## What are MCP Tools?

The trpc-agent-go framework provides built-in support for MCP tools with these key capabilities:

- **🔄 Multiple Tool Types**: Native support for Function tools, STDIO MCP tools, SSE MCP tools, and Streamable HTTP tools
- **🌊 Streaming Responses**: Real-time character-by-character response generation
- **💾 Tool State Management**: Proper handling of tool calls and responses
- **🔧 Simple Tool Implementations**: Focused examples with minimal complexity
- **🚀 LLM Integration**: Seamless use of tools with language models

### Key Features

- **STDIO MCP Server**: Local server with echo and add tools
- **SSE MCP Server**: HTTP-based server with recipe and health tip tools
- **Streamable HTTP Server**: HTTP server with weather and news tools
- **Direct Tool Testing**: Test tools directly without LLM
- **LLM Integration**: Use tools with an LLM agent for intelligent conversations
- **Multi-turn Chat**: Support for conversational tool usage
- **Tool Visualization**: Clear indication of tool calls, arguments, and responses

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description              | Default Value   |
| -------- | ------------------------ | --------------- |
| `-model` | Name of the model to use | `deepseek-chat` |

## Usage

### Start the Servers

```bash
# Start the Streamable HTTP Server
cd streamalbeserver
go run main.go

# Start the SSE Server
cd sseserver
go run main.go
```

### Run the Example

```bash
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

## Project Structure

```
mcptool/
├── main.go                # Main runner with interactive chat and direct tool testing
├── stdioserver/
│   └── main.go            # Simple STDIO MCP server with echo and add tools
├── sseserver/
│   └── main.go            # Simple SSE MCP server with recipe and health tip tools
├── streamalbeserver/      # Note: Directory name has a typo, maintained for compatibility
│   └── main.go            # Simple HTTP MCP server with weather and news tools
└── README.md              # This document
```

## Tool Types

This example demonstrates four types of tools:

1. **Function Tools**: Direct Go function implementations

   - `calculator`: Perform basic math operations
   - `current_time`: Get current time in different formats

2. **STDIO MCP Tools**: Tools provided via standard I/O

   - `echo`: Echo back a message with optional prefix
   - `add`: Add two numbers together

3. **SSE MCP Tools**: Tools provided via Server-Sent Events

   - `sse_recipe`: Get recipe information for a dish
   - `sse_health_tip`: Get health tips by category

4. **Streamable HTTP MCP Tools**: Tools provided via HTTP
   - `get_weather`: Get weather information for a location (simulated)
   - `get_news`: Get news headlines by category (simulated)

## Configuring MCP Tools in Your Agent

### STDIO MCP Tool Configuration

To integrate STDIO MCP tools into your agent, use the following code:

```go
// Configure STDIO MCP to connect to our local server.
stdioToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",
        Command:   "go",
        Args:      []string{"run", "./stdioserver/main.go"},
        Timeout:   10 * time.Second,
    },
    mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("echo", "add")),
)
```

### SSE MCP Tool Configuration

To integrate SSE MCP tools into your agent, use the following code:

```go
// Create SSE MCP tools.
sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse", // SSE server URL
        Timeout:   10 * time.Second,
        Headers: map[string]string{             // Optional headers
            "User-Agent": "trpc-agent-go/1.0.0",
        },
    },
    mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("sse_recipe", "sse_health_tip")),
    mcp.WithMCPOptions(
        // WithRetry: Custom retry configuration for fine-tuned control
        // Retry sequence: 1s -> 1.5s -> 2.25s -> 3.375s -> 5.0625s (capped at 15s)
        tmcp.WithRetry(tmcp.RetryConfig{
            MaxRetries:     5,                      // Maximum retry attempts (range: 0-10, default: 2)
            InitialBackoff: 1 * time.Second,       // Initial delay before first retry (range: 1ms-30s, default: 500ms)
            BackoffFactor:  1.5,                   // Exponential backoff multiplier (range: 1.0-10.0, default: 2.0)
            MaxBackoff:     15 * time.Second,      // Maximum delay cap (range: up to 5 minutes, default: 8s)
        }),
        // other mcp options.
        // tmcp.WithHTTPHeaders(http.Header{
        //     "User-Agent": []string{"trpc-agent-go/1.0.0"},
        // }),
    ),
)
```

### Streamable HTTP Tool Configuration

To integrate Streamable HTTP tools into your agent, use the following code:

```go
// Configure Streamable HTTP MCP connection.
streamableToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "streamable_http",
        ServerURL: "http://localhost:3000/mcp",
        Timeout:   10 * time.Second,
    },
    mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("get_weather", "get_news")),
    mcp.WithMCPOptions(
        // WithSimpleRetry(3): Uses default settings with 3 retry attempts
        // - MaxRetries: 3 (range: 0-10)
        // - InitialBackoff: 500ms (default, range: 1ms-30s)
        // - BackoffFactor: 2.0 (default, range: 1.0-10.0)
        // - MaxBackoff: 8s (default, range: up to 5 minutes)
        // Retry sequence: 500ms -> 1s -> 2s (total max delay: ~3.5s)
        tmcp.WithSimpleRetry(3),
        // other mcp options.
        // tmcp.WithHTTPHeaders(http.Header{
        //     "User-Agent": []string{"trpc-agent-go/1.0.0"},
        // }),
    ),
)
```

### Using ToolSets with LLM Agent

```go
// Create function tools
calculatorTool := function.NewFunctionTool(calculate,
    function.WithName("calculator"),
    function.WithDescription("Perform basic mathematical calculations"))
timeTool := function.NewFunctionTool(getCurrentTime,
    function.WithName("current_time"),
    function.WithDescription("Get the current time and date"))

// Create LLM agent with both function tools and toolsets
llmAgent := llmagent.New(
    agentName,
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A helpful AI assistant"),
    // ... other configurations
    llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),        // Function tools
    llmagent.WithToolSets([]tool.ToolSet{stdioToolSet, sseToolSet, streamableToolSet}), // MCP ToolSets
)
```

## Retry Mechanism for Network Tools

The trpc-agent-go framework provides automatic retry functionality for MCP tools to handle temporary network failures. This feature is available for network-based transports (Streamable HTTP and SSE), but not for STDIO transport due to its process-based nature.

### Simple Retry Configuration

For most use cases, use the simple retry configuration with a specified number of retry attempts:

```go
mcp.WithMCPOptions(
    // WithSimpleRetry(3): Uses default settings with 3 retry attempts
    // - MaxRetries: 3 (range: 0-10)
    // - InitialBackoff: 500ms (default, range: 1ms-30s)
    // - BackoffFactor: 2.0 (default, range: 1.0-10.0)
    // - MaxBackoff: 8s (default, range: up to 5 minutes)
    // Retry sequence: 500ms -> 1s -> 2s (total max delay: ~3.5s)
    tmcp.WithSimpleRetry(3),
)
```

### Advanced Retry Configuration

For specific scenarios requiring fine-tuned retry behavior:

```go
mcp.WithMCPOptions(
    // WithRetry: Custom retry configuration for fine-tuned control
    // Retry sequence: 1s -> 1.5s -> 2.25s -> 3.375s -> 5.0625s (capped at 15s)
    tmcp.WithRetry(tmcp.RetryConfig{
        MaxRetries:     5,                      // Maximum retry attempts (range: 0-10, default: 2)
        InitialBackoff: 1 * time.Second,       // Initial delay before first retry (range: 1ms-30s, default: 500ms)
        BackoffFactor:  1.5,                   // Exponential backoff multiplier (range: 1.0-10.0, default: 2.0)
        MaxBackoff:     15 * time.Second,      // Maximum delay cap (range: up to 5 minutes, default: 8s)
    }),
)
```

### What Errors Are Retried?

Automatic retry handles temporary failures:

- **Network issues**: Connection refused/reset/timeout, I/O timeout, EOF, broken pipe
- **HTTP server errors**: 408, 409, 429, and all 5xx status codes

### Key Features

- **Transport Layer**: Works across all MCP operations (tools, resources, prompts)
- **Exponential Backoff**: Intelligent delay strategy to avoid server overload
- **Error Classification**: Only retries temporary failures, not permanent errors
- **Silent Operation**: Transparent retry with no logging noise by default

### Import Requirements

To use retry functionality, import the trpc-mcp-go package:

```go
import (
    tmcp "trpc.group/trpc-go/trpc-mcp-go"
)
```

## Session Reconnection for MCP Tools

The trpc-agent-go framework provides automatic session reconnection to handle server restarts and session expiration. When enabled, the session manager automatically recreates the MCP session when detecting connection failures.

**Per-Operation Strategy**: Each tool call gets independent reconnection attempts. If one call exhausts its attempts, the next call starts fresh with full attempts again.

### Enabling Session Reconnection

For most use cases, simply enable session reconnection with default settings:

```go
sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
    },
    mcp.WithSessionReconnect(3), // Enable automatic session reconnection (max 3 attempts per operation, recommended)
    mcp.WithMCPOptions(
        tmcp.WithRetry(...), // Can be combined with retry
    ),
)
```

### Custom Reconnection Configuration

For scenarios requiring different reconnection behavior:

```go
sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
    },
    mcp.WithSessionReconnect(5), // Custom max attempts (valid range: 1-10)
)

// Or use advanced configuration for future extensions
sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{...},
    mcp.WithSessionReconnectConfig(mcp.SessionReconnectConfig{
        MaxReconnectAttempts: 5,
        // Future extension fields will be available here
    }),
)
```

### What Errors Trigger Reconnection?

Automatic reconnection handles connection/session failures:

- **Connection failures**: Transport closed, connection refused/reset, broken pipe, EOF
- **Session expiration**: HTTP 404, session not found, session_expired errors

**Not reconnected**: DNS resolution failures (configuration errors), I/O timeouts (potential performance issues)

### How It Works

```
User Request → Connection Error Detected → Session Reconnection → Retry Request → Success
```

**Example Flow (Per-Operation)**:
1. Server restarts (connection lost)
2. Tool call 1 detects `transport is closed` error
3. Automatically attempts reconnection up to 3 times for this call
4. If all 3 attempts fail, returns error for this call
5. Tool call 2 gets a fresh set of 3 reconnection attempts
6. Server is back online, reconnection succeeds
7. Returns result successfully

## Running the Example

### Prerequisites

Ensure you have Go installed and set up properly.

### Starting the Servers

1. **Start the Streamable HTTP Server**:

   ```bash
   cd streamalbeserver
   go run main.go
   ```

2. **Start the SSE Server**:

   ```bash
   cd sseserver
   go run main.go -port 8080  # Default port is 8080
   ```

3. **Run the Main Example**:
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
6. **Recipe**: Get Chinese recipe information for various dishes.
7. **Health Tips**: Get health tips by category.

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

### SSE MCP Tools

1. **sse_recipe**

   - **Description**: Chinese recipe query tool
   - **Parameters**:
     - `dish`: Dish name

2. **sse_health_tip**
   - **Description**: Health tip tool
   - **Parameters**:
     - `category`: Category (general, diet, exercise, etc.)

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

1. **Tool Integration**: How to integrate different types of tools (function, STDIO, SSE, Streamable-HTTP)
2. **Direct Testing**: How to test tools directly without going through an LLM
3. **Interactive Chat**: How to use tools in an interactive chat session with an LLM

This serves as a practical reference for building your own tool-enabled AI assistants.
