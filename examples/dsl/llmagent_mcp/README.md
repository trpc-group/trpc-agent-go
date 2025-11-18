# LLMAgent with MCP Tools Example

This example demonstrates how to configure MCP (Model Context Protocol) tools directly in the DSL workflow configuration, without needing to create separate nodes or edges.

## 🎯 Key Features

### High-Level Tool Configuration
- **MCP Tools**: Configure MCP tools directly in the `llmagent` node config
- **Regular Tools**: Mix MCP tools with regular tools from ToolRegistry
- **No Extra Nodes**: No need to create separate tool nodes or connect edges
- **Frontend-Friendly**: All configuration is declarative in JSON

### Advantages Over LangFlow/Flowise
- ✅ **Higher Abstraction**: Tools are configured as part of the agent, not as separate nodes
- ✅ **Cleaner Workflows**: No need for complex edge connections for tool handling
- ✅ **Better UX**: Frontend users can simply add tools via dropdown/configuration panel
- ✅ **Flexible**: Supports both high-level (MCP config) and low-level (explicit tool nodes) approaches

## 📝 Current Example Configuration

This example uses **stdio transport** to connect to a local MCP server (`examples/mcptool/stdioserver/main.go`) that provides:

- **echo** (MCP tool): Echo messages with an optional prefix
- **add** (MCP tool): Add two numbers together
- **calculator** (Regular tool): Basic math operations (not MCP)

The MCP server is launched as a subprocess via stdio, demonstrating how to integrate local MCP tools without needing HTTP servers.

## 📋 Configuration Format

### MCP Tools in DSL (stdio transport)

```json
{
  "id": "mcp_agent",
  "component": {
    "type": "builtin",
    "ref": "builtin.llmagent"
  },
  "config": {
    "model_name": "deepseek-chat",
    "instruction": "You are a helpful assistant with access to echo and add tools via MCP...",
    "tools": ["calculator"],
    "mcp_tools": [
      {
        "name": "stdio_tools",
        "transport": "stdio",
        "command": "go",
        "args": ["run", "../../../examples/mcptool/stdioserver/main.go"],
        "timeout": 30,
        "tool_filter": ["echo", "add"]
      }
    ]
  }
}
```

### Alternative: HTTP Transport

```json
{
  "mcp_tools": [
    {
      "name": "weather_service",
      "transport": "streamable_http",
      "server_url": "http://localhost:3000/mcp",
      "timeout": 10,
      "tool_filter": ["get_weather", "get_news"],
      "headers": {
        "User-Agent": "trpc-agent-go-dsl/1.0.0"
      }
    }
  ]
}
```

### MCP Tool Configuration Options

#### Common Fields
- `name`: (Optional) Name for the toolset
- `transport`: Transport type - `"stdio"`, `"streamable_http"`, or `"sse"`
- `timeout`: Timeout in seconds (default: 10)
- `tool_filter`: Array of tool names to include

#### STDIO Transport
```json
{
  "transport": "stdio",
  "command": "go",
  "args": ["run", "./server/main.go"],
  "timeout": 10,
  "tool_filter": ["echo", "add"]
}
```

#### Streamable HTTP Transport
```json
{
  "transport": "streamable_http",
  "server_url": "http://localhost:3000/mcp",
  "timeout": 10,
  "tool_filter": ["get_weather", "get_news"],
  "headers": {
    "User-Agent": "custom-agent/1.0"
  }
}
```

#### SSE Transport
```json
{
  "transport": "sse",
  "server_url": "http://localhost:8080/sse",
  "timeout": 10,
  "tool_filter": ["sse_recipe", "sse_health_tip"],
  "headers": {
    "Authorization": "Bearer token"
  }
}
```

## 🚀 Running the Example

### Prerequisites

1. Set up environment variables:
```bash
# Copy the example env file
cp .env.example .env

# Edit .env and add your API key
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"
export MODEL_NAME="deepseek-chat"
```

2. **No need to start MCP server separately!**
   - This example uses **stdio transport**
   - The MCP server (`examples/mcptool/stdioserver/main.go`) is launched automatically as a subprocess
   - The server provides `echo` and `add` tools

### Run the Example

```bash
cd examples/dsl/llmagent_mcp

# Build
go build

# Run interactively
./llmagent_mcp

# Or run directly
go run main.go
```

### Example Interaction

```
🚀 LLMAgent with MCP Tools Example
============================================================

✅ Registered model: deepseek-chat
✅ Registered tool: calculator

✅ Loaded workflow: llmagent_mcp_example

✅ Workflow compiled successfully!

✅ Runner created successfully!

Type your message (or 'exit' to quit):
============================================================

👤 You: Use the echo tool to say Hello World with prefix MCP:
🤖 Assistant: MCP: Hello World

👤 You: Use the add tool to calculate 123 + 456
🤖 Assistant: The result is 579.

👤 You: Calculate 25 * 4 using the calculator
🤖 Assistant: 25 multiplied by 4 equals 100.

👤 You: exit
👋 Goodbye!
```

### Using the Test Script

```bash
chmod +x test_mcp.sh
./test_mcp.sh
```

This will run automated tests for:
- Echo tool (MCP via stdio)
- Add tool (MCP via stdio)
- Calculator tool (regular tool)

### Example Interactions

```
👤 You: What's the weather in Beijing?
🤖 Assistant: 
🔧 Calling tool: get_weather
✅ Tool result: {"temperature": 15, "condition": "sunny"}
The weather in Beijing is sunny with a temperature of 15°C.

👤 You: Calculate 25 * 4
🤖 Assistant:
🔧 Calling tool: calculator
✅ Tool result: {"result": 100}
25 multiplied by 4 equals 100.
```

## 🎨 Frontend Usage Scenario

In a visual workflow editor (like the one shown in your screenshot), users can:

1. **Drag an "LLM Agent" node** onto the canvas
2. **Configure in the right panel**:
   - Name: "Weather Agent"
   - Model: "gpt-4" (dropdown)
   - Instructions: "You are a weather assistant..."
   - **Tools section**:
     - Regular tools: Click "+" → Select "Web Search", "Calculator"
     - **MCP Tools**: Click "Add MCP Tool" → Configure:
       - Transport: "Streamable HTTP" (dropdown)
       - Server URL: "http://127.0.0.1:3000/mcp"
       - Tool Filter: "get_weather, get_news"
3. **Connect nodes** with simple edges (no tool-specific edges needed)

### Comparison with LangFlow

**LangFlow Approach** (Low-level):
```
[User Input] → [LLM Node] → [Tool Node 1] → [LLM Node]
                          → [Tool Node 2] → [LLM Node]
                          → [Tool Node 3] → [LLM Node]
```
- Requires multiple nodes and edges
- Tool handling is explicit in the graph
- More complex for simple use cases

**Our Approach** (High-level):
```
[User Input] → [LLM Agent (with tools configured)] → [Output]
```
- Single node with tools configured inside
- Cleaner visual representation
- Still supports low-level approach when needed

## 🔧 Implementation Details

### How It Works

1. **DSL Parsing**: The `mcp_tools` config is parsed from JSON
2. **Compilation**: `createLLMAgentNodeFunc` creates MCP ToolSets dynamically
3. **Graph Agent**: Compiled graph is wrapped in a GraphAgent
4. **Runner**: GraphAgent is executed via Runner for session management
5. **Tool Calls**: Framework automatically handles tool execution and response

### Architecture Flow

```
workflow.json → Parser → Compiler → Graph → GraphAgent → Runner → Events
                                      ↓
                                  MCP ToolSets
                                  Regular Tools
```

### Code Flow

```go
// Step 1: Parse and compile DSL
parser := dsl.NewParser()
workflow, _ := parser.ParseFile("workflow.json")

compiler := dsl.NewCompiler(registry.DefaultRegistry).
    WithModelRegistry(modelRegistry).
    WithToolRegistry(toolRegistry)

compiledGraph, _ := compiler.Compile(workflow)

// Step 2: Create GraphAgent
graphAgent, _ := graphagent.New("llmagent-mcp-workflow", compiledGraph,
    graphagent.WithDescription("LLMAgent with MCP tools configuration"),
)

// Step 3: Create Runner
appRunner := runner.NewRunner("llmagent-mcp-demo", graphAgent)
defer appRunner.Close()

// Step 4: Execute with streaming events
message := model.NewUserMessage(userInput)
eventChan, _ := appRunner.Run(ctx, userID, sessionID, message)

// Step 5: Process streaming events
for evt := range eventChan {
    if len(evt.Choices) > 0 && evt.Choices[0].Delta.Content != "" {
        fmt.Print(evt.Choices[0].Delta.Content)
    }
}
```

### Compiler Implementation

```go
// In compiler.go
func (c *Compiler) createLLMAgentNodeFunc(node Node) (graph.NodeFunc, error) {
    // ... parse regular tools from ToolRegistry

    // Parse MCP tools from config
    var mcpToolSets []tool.ToolSet
    if mcpToolsConfig, ok := node.Config["mcp_tools"]; ok {
        for _, mcpToolConfig := range mcpToolsConfig {
            toolSet := c.createMCPToolSet(mcpToolConfig)
            mcpToolSets = append(mcpToolSets, toolSet)
        }
    }

    // Create LLMAgent with both tool types
    return func(ctx context.Context, state graph.State) (interface{}, error) {
        opts := []llmagent.Option{
            llmagent.WithModel(model),
            llmagent.WithTools(tools),           // Regular tools
            llmagent.WithToolSets(mcpToolSets),  // MCP tools
        }
        agent := llmagent.New("agent", opts...)
        // ... execute agent
    }
}
```

## 📚 Related Examples

- `examples/dsl/llmagent/` - Basic LLMAgent with regular tools
- `examples/mcptool/` - Low-level MCP tool usage
- `examples/dsl/graph_custom/` - Custom registry DSL workflow

## 💡 Best Practices

1. **Use MCP tools for external services**: Weather APIs, database queries, etc.
2. **Use regular tools for simple functions**: Calculations, string manipulation
3. **Combine both**: Mix MCP and regular tools in the same agent
4. **Tool filtering**: Use `tool_filter` to limit which tools are exposed
5. **Error handling**: MCP connections may fail, ensure proper timeout configuration

## 🎓 Learning Points

This example demonstrates:
- ✅ High-level tool configuration in DSL
- ✅ Dynamic MCP ToolSet creation from JSON config
- ✅ Mixing different tool types (regular + MCP)
- ✅ Frontend-friendly declarative configuration
- ✅ Framework's advantage over visual-only platforms
