# MCP Tool + LLM Integration Example

This example demonstrates how to integrate trpc-agent-go MCP tools with LLM, supporting intelligent tool calling and multi-turn conversations.

## Features

- **Streamable HTTP MCP Server Integration**: Connect to local HTTP MCP server
- **Enterprise-Grade MCP Configuration**: Advanced features like retry, authentication, logging, tool filtering
- **Hybrid Tool Environment**: Seamless integration of function tools + MCP tools
- **Intelligent Tool Calling**: LLM automatically selects and uses appropriate tools
- **Multi-turn Conversation Support**: Tool call results automatically passed to LLM
- **Complete Error Handling**: Handling of network timeouts, tool failures, and other scenarios
- **Tool Mapping Convenience Functions**: Automatically convert tool arrays to mapping format required by LLM

## Project Structure

```
mcp-tool-llm/
├── main.go                 # Main program with demonstration scenarios
├── tool_functions.go       # Tool function implementations (filesystem, weather, calculator, etc.)
├── mcpserver/             # Streamable HTTP MCP Server
│   └── main.go            # MCP server implementation
└── README.md              # This document
```

## Core Convenience Features

### `toolsToMap()` Function

To simplify tool usage, we provide a convenience function that automatically converts tool arrays to the mapping format required by LLM models:

```go
// toolsToMap converts tool array to map for easy use in model.Request
func toolsToMap(tools []tool.Tool) map[string]tool.Tool {
	toolsMap := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		decl := t.Declaration()
		toolsMap[decl.Name] = t
	}
	return toolsMap
}
```

**Usage Example:**
```go
// Get tool array
tools := mcpToolSet.Tools(ctx)

// 🎯 Auto-convert to mapping (no manual creation needed)
toolsMap := toolsToMap(tools)

// Use directly in LLM request
request := &model.Request{
	Messages: [...],
	Tools:    toolsMap, // ✅ Clean and easy to use
}
```

**Compared to Traditional Approach:**
```go
// ❌ Traditional manual approach (tedious)
toolsMap := make(map[string]tool.Tool)
for _, t := range tools {
	decl := t.Declaration()
	toolsMap[decl.Name] = t
}

// ✅ New convenience approach (clean)
toolsMap := toolsToMap(tools)
```

## Prerequisites

### 1. Set Environment Variables

```bash
# OpenAI API Configuration (supports any OpenAI-compatible API)
export OPENAI_API_KEY="sk-your-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # Default
export OPENAI_MODEL="gpt-4o-mini"  # Default

# Example: DeepSeek API Configuration
export OPENAI_API_KEY="sk-your-deepseek-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_MODEL="deepseek-chat"
```

### 2. Start MCP Server

```bash
# Enter MCP server directory
cd streamable_server

# Start server (default port 3000)
go run main.go

# Or specify port
PORT=3001 go run main.go
```

After server starts, you'll see:
```
Starting MCP server on http://localhost:3000
Available tools:
- echo: Echoes messages with optional prefix
- greet: Generates greetings in different languages
- current_time: Returns current time
- calculate: Performs basic math operations
- env_info: Returns environment information
```

### 3. Verify MCP Server

```bash
# Test tool list
curl -X POST http://localhost:3000/mcp \
  -H 'Content-Type: application/json' \
  -d '{"method":"tools/list","jsonrpc":"2.0","id":1}'

# Test greeting tool
curl -X POST http://localhost:3000/mcp \
  -H 'Content-Type: application/json' \
  -d '{"method":"tools/call","jsonrpc":"2.0","id":2,"params":{"name":"greet","arguments":{"name":"Alice","language":"en"}}}'
```

## Running Examples

### Demo 1: Streamable HTTP MCP Toolset

**Using only MCP server tools:**

```bash
go run . streamable
```

**Features:**
- Connect to `http://localhost:3000/mcp`
- Enterprise configuration: 3 retries, exponential backoff, tool filtering
- Automatic tool discovery and refresh
- Multiple test scenarios: multilingual greetings, time calculations, environment info, math operations
- **Automatic Tool Mapping**: Uses `toolsToMap()` function to simplify tool usage

**Sample Output:**
```
=== Demo 1: Streamable HTTP MCP Tools ===

MCP Toolset created successfully
Connected to streamable HTTP MCP server at http://localhost:3000/mcp
Discovered 5 MCP tools
   - echo: Echoes the input message with optional prefix
   - greet: Generates a greeting message
   - current_time: Returns the current time
   - calculate: Performs basic mathematical operations
   - env_info: Returns information about the environment

Test scenario 1: Multi-language greeting test
User: Please greet 'Alice' in Chinese, then greet 'Marie' in French
Assistant: I'll help you generate multilingual greetings...

Executing 2 tool calls:
- greet: {"name":"Alice","language":"zh"}
  Result: 你好，Alice！
- greet: {"name":"Marie","language":"fr"}  
  Result: Bonjour, Marie!

Final response: I have generated multilingual greetings for you:
1. Chinese greeting: 你好，Alice！
2. French greeting: Bonjour, Marie!

Token usage: input=156, output=89, total=245
```

### Demo 2: Mixed Tools Environment

**Function tools + MCP tools hybrid usage:**

```bash
go run . hybrid
```

**Features:**
- 2 local function tools: calculator, get_time
- 5 MCP server tools: echo, greet, current_time, calculate, env_info
- LLM intelligently selects the most appropriate tools
- Unified tool calling interface
- **Intelligent Tool Mapping**: Automatically handles mixed tool environments

**Sample Output:**
```
=== Demo 2: Mixed Tools Environment ===

MCP Toolset connected to HTTP server
Total tools count: 7 (2 function tools + 5 MCP tools)
Available tools list:
   - calculator: Execute basic mathematical operations
   - get_time: Get current time
   - echo: Echoes the input message with optional prefix
   - greet: Generates a greeting message
   - current_time: Returns the current time
   - calculate: Performs basic mathematical operations
   - env_info: Returns information about the environment

Comprehensive test scenarios:
User: Please tell me the current time, then calculate 15*23+7, and finally greet 'Alice' in English

Executing 3 tool calls:
- get_time: {"format":"2006-01-02 15:04:05"}
  Result: 2025-01-26 19:19:03
- calculator: {"expression":"15*23+7"}
  Result: 352.00
- greet: {"name":"Alice","language":"en"}
  Result: Hello, Alice!

Final response: I have completed all your requests:
1. Current time: 2025-01-26 19:19:03
2. Calculation result: 15*23+7 = 352.00
3. English greeting: Hello, Alice!

Token usage: input=201, output=134, total=335
```

### Demo 3: STDIO MCP Server

**Local STDIO MCP server tools:**

```bash
go run . stdio
```

### Demo 4: Multiple MCPToolset Management (ADK Python Style)

**Using multiple MCPToolsets to manage multiple independent MCP sessions:**

```bash
go run . multiple
```

**Features:**
- 🔗 **MCPToolset 1**: Streamable HTTP MCP Server (http://localhost:3000)  
- 🔗 **MCPToolset 2**: STDIO MCP Server (local process)
- 📦 **Unified Tool Management**: Automatically collect tools from all toolsets
- 🎯 **Intelligent Routing**: LLM automatically selects appropriate tools (regardless of which session they come from)
- 🎨 **ADK Python Alignment**: Each MCPToolset = one independent MCP session

**Design Philosophy Comparison:**

| Approach | ADK Python | Our Implementation |
|----------|------------|-------------------|
| Multi-session Management | Multiple MCPToolsets | Multiple MCPToolsets ✅ |
| Session Independence | Each toolset one session | Each toolset one session ✅ |
| Tool Unification | Agent layer auto-aggregation | Manual tool array aggregation |
| Resource Management | Framework auto-cleanup | defer Close() |

**Sample Output:**
```
=== Multiple MCPToolsets Demo (ADK Python Style) ===
🎯 Each MCPToolset = one independent MCP session

🌐 HTTP MCP toolset: Found 3 tools
   - echo (from HTTP MCP)
   - greet (from HTTP MCP) 
   - current_time (from HTTP MCP)

📡 STDIO MCP toolset: Found 3 tools
   - echo (from STDIO MCP)
   - text_transform (from STDIO MCP)
   - calculator (from STDIO MCP)

🎯 Total tools count: 6 (from multiple independent MCP sessions)

🎯 Test scenario 1: Cross-session tool usage
💬 User: Please use tools from different sources: first use HTTP MCP's greet tool to greet 'Alice', then use STDIO MCP's calculator to calculate 25*4

🔧 Executing 2 tool calls:
- greet: {"name":"Alice","language":"en"}
  ✅ Result: Hello, Alice!
- calculator: {"operation":"multiply","a":25,"b":4}
  ✅ Result: 100

🎯 Final response: I completed cross-session tool calls for you:
1. Using HTTP MCP server greeting: Hello, Alice!
2. Using STDIO MCP server calculation: 25 × 4 = 100
```

### Demo 5: Tool Filtering Showcase

**Demonstrate different tool filtering strategies:**

```bash
go run . filter
```

### Demo 6: Enhanced Error Diagnostics Showcase

**Demonstrate enhanced error handling and diagnostics:**

```bash
go run . diagnostics
```

## Code Architecture Highlights

### 1. Tool Mapping Automation

**Problem**: Traditional approach requires manual tool mapping creation
```go
// ❌ Tedious manual approach
toolsMap := make(map[string]tool.Tool)
for _, t := range tools {
	decl := t.Declaration()
	toolsMap[decl.Name] = t
}
```

**Solution**: Provide convenience function for automatic conversion
```go
// ✅ Clean automatic approach
toolsMap := toolsToMap(tools)
```

### 2. Unified Tool Interface

Whether local function tools or remote MCP tools, all use the same `tool.Tool` interface:

```go
type Tool interface {
	Call(ctx context.Context, jsonArgs []byte) (any, error)
	Declaration() *Declaration
}
```

This allows the `toolsToMap()` function to handle any type of tool.

### 3. Enterprise Feature Integration

```go
mcpToolSet := tool.NewMCPToolSet(mcpConfig,
	tool.WithRetry(...),           // Retry mechanism
	tool.WithLogger(...),          // Logging
	tool.WithToolFilter(...),      // Tool filtering
	tool.WithAutoRefresh(...),     // Auto-refresh
)

// 🎯 No matter how complex the configuration, tool usage is simple
toolsMap := toolsToMap(mcpToolSet.Tools(ctx))
```

## MCP Server Tool Descriptions

### 1. echo Tool
```json
{
  "name": "echo",
  "description": "Echoes the input message with optional prefix",
  "parameters": {
    "message": {"type": "string", "required": true},
    "prefix": {"type": "string", "default": "Echo: "}
  }
}
```

### 2. greet Tool
```json
{
  "name": "greet", 
  "description": "Generates a greeting message",
  "parameters": {
    "name": {"type": "string", "required": true},
    "language": {"type": "string", "enum": ["en", "zh", "es", "fr"], "default": "en"}
  }
}
```

### 3. current_time Tool
```json
{
  "name": "current_time",
  "description": "Returns the current time", 
  "parameters": {
    "timezone": {"type": "string", "default": "Local"},
    "format": {"type": "string", "default": "2006-01-02 15:04:05"}
  }
}
```

### 4. calculate Tool  
```json
{
  "name": "calculate",
  "description": "Performs basic mathematical operations",
  "parameters": {
    "a": {"type": "number", "required": true},
    "b": {"type": "number", "required": true}, 
    "operation": {"type": "string", "enum": ["add", "subtract", "multiply", "divide"], "required": true}
  }
}
```

### 5. env_info Tool
```json
{
  "name": "env_info",
  "description": "Returns information about the environment",
  "parameters": {
    "type": {"type": "string", "enum": ["all", "hostname", "user", "pwd"], "default": "all"}
  }
}
```

## Function Tool Descriptions

### 1. calculator Function Tool
- **Function**: Parse and calculate mathematical expressions
- **Support**: Basic arithmetic operations (add, subtract, multiply, divide)
- **Example**: `15*23+7` → `352.00`

### 2. get_time Function Tool  
- **Function**: Get current local time
- **Format**: Supports custom time formats
- **Default**: `2006-01-02 15:04:05`

## Architecture Features

### MCP Tool Integration
- **Transport Protocol**: Streamable HTTP (real-time bidirectional communication)
- **Connection Management**: Auto-reconnection, timeout handling
- **Tool Discovery**: Dynamic retrieval of available server tools
- **Enterprise Features**: Retry, authentication, logging, filtering

### Intelligent Tool Calling
- **LLM Decision Making**: Intelligently select tools based on user needs
- **Parameter Mapping**: Automatic JSON parameter conversion handling
- **Result Integration**: Tool results automatically passed to LLM for response generation
- **Error Handling**: Graceful degradation when tools fail

### Hybrid Tool Environment
- **Seamless Integration**: Unified interface for local function tools and remote MCP tools
- **Intelligent Selection**: LLM automatically chooses the most appropriate tool implementation
- **Tool Competition**: For similar functionality tools, LLM decides which to use
- **Convenience Functions**: `toolsToMap()` simplifies tool mapping creation

## Development Best Practices

### 1. Tool Mapping Simplification
```go
// ✅ Recommended approach: Use convenience function
tools := mcpToolSet.Tools(ctx)
toolsMap := toolsToMap(tools)

request := &model.Request{
	Tools: toolsMap,
	// ...
}
```

### 2. Hybrid Tool Environment
```go
// Merge tools from different sources
allTools := []tool.Tool{}
allTools = append(allTools, functionTools...)
allTools = append(allTools, mcpTools...)

// Unified handling
toolsMap := toolsToMap(allTools)
```

### 3. Enterprise Configuration
```go
mcpToolSet := tool.NewMCPToolSet(config,
	tool.WithRetry(...),      // Required for production
	tool.WithLogger(...),     // Debugging and monitoring
	tool.WithToolFilter(...), // Security control
)
```

## Error Handling

### Network Issues
- When MCP server connection fails, automatically fallback to function tools
- Support retry mechanism with exponential backoff to avoid server overload
- Timeout protection to avoid long waits

### Tool Call Failures
- Friendly prompts for parameter validation errors
- Error catching for tool execution exceptions
- Skip failed tools and continue processing

## Performance Optimization

- **Connection Reuse**: HTTP connection pooling reduces connection overhead
- **Caching Mechanism**: Tool list caching reduces redundant requests  
- **Concurrency Control**: Concurrent tool call processing improves efficiency
- **Resource Cleanup**: Automatic release of connections and resources
- **Mapping Cache**: Tool mapping creation optimization

## Development Recommendations

1. **Local Development**: Start MCP server first, then run client
2. **Network Debugging**: Use curl commands to verify MCP server API
3. **Log Levels**: Adjust log levels to observe detailed interaction processes
4. **Tool Extension**: Add new tools in respective server main.go files
5. **Error Recovery**: Implement appropriate error handling and retry logic
6. **Convenience Functions**: Use `toolsToMap()` to simplify tool mapping creation

This example demonstrates a complete MCP + LLM integration solution, providing clean and easy-to-use APIs with powerful enterprise-grade features, offering a solid foundation for building intelligent AI applications. 