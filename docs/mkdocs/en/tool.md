# Tool Usage Guide

The Tool system is a core component of the tRPC-Agent-Go framework, enabling Agents to interact with external services and functions. The framework supports multiple tool types, including Function Tools and external tools integrated via the MCP (Model Context Protocol) standard.

## Overview

### 🎯 Key Features

- **🔧 Multiple Tool Types**: Supports Function Tools and MCP standard tools.
- **🌊 Streaming Responses**: Supports both real-time streaming responses and normal responses.
- **⚡ Parallel Execution**: Tool invocations support parallel execution to improve performance.
- **🔄 MCP Protocol**: Full support for STDIO, SSE, and Streamable HTTP transports.
- **🛠️ Configuration Support**: Provides configuration options and filter support.

### Core Concepts

#### 🔧 Tool

A Tool is an abstraction of a single capability that implements the `tool.Tool` interface. Each Tool provides specific functionality such as mathematical calculation, search, time query, etc.

```go
type Tool interface {
    Declaration() *Declaration  // Return tool metadata.
}

type CallableTool interface {
    Call(ctx context.Context, jsonArgs []byte) (any, error)
    Tool
}
```

#### 📦 ToolSet

A ToolSet is a collection of related tools that implements the `tool.ToolSet` interface. A ToolSet manages the lifecycle of tools, connections, and resource cleanup.

```go
type ToolSet interface {
    Tools(context.Context) []CallableTool  // Return the list of tools.
    Close() error                          // Resource cleanup.
}
```

**Relationship between Tool and ToolSet:**

- One **Tool** = one concrete capability (e.g., calculator).
- One **ToolSet** = a group of related Tools (e.g., all tools provided by an MCP server).
- An Agent can use multiple Tools and multiple ToolSets simultaneously.

#### 🌊 Streaming Tool Support

The framework supports streaming tools to provide real-time responses:

```go
// Streaming tool interface.
type StreamableTool interface {
    StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error)
    Tool
}

// Streaming data unit.
type StreamChunk struct {
    Content  any      `json:"content"`
    Metadata Metadata `json:"metadata,omitempty"`
}
```

**Streaming tool characteristics:**

- 🚀 Real-time responses: Data is returned progressively without waiting for the complete result.
- 📊 Large data handling: Suitable for scenarios such as log queries and data analysis.
- ⚡ User experience: Provides instant feedback and progress display.

### Tool Types

| Tool Type | Definition | Integration Method |
|----------|------------|--------------------|
| **Function Tools** | Tools implemented by directly calling Go functions | `Tool` interface, in-process calls |
| **Agent Tool (AgentTool)** | Wrap any Agent as a callable tool | `Tool` interface, supports streaming inner forwarding |
| **DuckDuckGo Tool** | Search tool based on DuckDuckGo API | `Tool` interface, HTTP API |
| **MCP ToolSet** | External toolset based on MCP protocol | `ToolSet` interface, multiple transports |

> **📖 Related docs**: For Agent Tool and Transfer Tool used in multi-Agent collaboration, see the Multi-Agent System document.

## Function Tools

Function Tools implement tool logic directly via Go functions and are the simplest tool type.

### Basic Usage

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/function"

// 1. Define a tool function.
func calculator(ctx context.Context, req struct {
    Operation string  `json:"operation"`
    A         float64 `json:"a"`
    B         float64 `json:"b"`
}) (map[string]interface{}, error) {
    switch req.Operation {
    case "add":
        return map[string]interface{}{"result": req.A + req.B}, nil
    case "multiply":
        return map[string]interface{}{"result": req.A * req.B}, nil
    default:
        return nil, fmt.Errorf("unsupported operation: %s", req.Operation)
    }
}

// 2. Create the tool.
calculatorTool := function.NewFunctionTool(
    calculator,
    function.WithName("calculator"),
    function.WithDescription("Perform mathematical operations."),
)

// 3. Integrate into an Agent.
agent := llmagent.New("math-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{calculatorTool}))
```

### Streaming Tool Example

```go
// 1. Define input and output structures.
type weatherInput struct {
    Location string `json:"location"`
}

type weatherOutput struct {
    Weather string `json:"weather"`
}

// 2. Implement the streaming tool function.
func getStreamableWeather(input weatherInput) *tool.StreamReader {
    stream := tool.NewStream(10)
    go func() {
        defer stream.Writer.Close()
        
        // Simulate progressively returning weather data.
        result := "Sunny, 25°C in " + input.Location
        for i := 0; i < len(result); i++ {
            chunk := tool.StreamChunk{
                Content: weatherOutput{
                    Weather: result[i : i+1],
                },
                Metadata: tool.Metadata{CreatedAt: time.Now()},
            }
            
            if closed := stream.Writer.Send(chunk, nil); closed {
                break
            }
            time.Sleep(10 * time.Millisecond) // Simulate latency.
        }
    }()
    
    return stream.Reader
}

// 3. Create the streaming tool.
weatherStreamTool := function.NewStreamableFunctionTool[weatherInput, weatherOutput](
    getStreamableWeather,
    function.WithName("get_weather_stream"),
    function.WithDescription("Get weather information as a stream."),
)

// 4. Use the streaming tool.
reader, err := weatherStreamTool.StreamableCall(ctx, jsonArgs)
if err != nil {
    return err
}

// Receive streaming data.
for {
    chunk, err := reader.Recv()
    if err == io.EOF {
        break // End of stream.
    }
    if err != nil {
        return err
    }
    
    // Process each chunk.
    fmt.Printf("Received: %v\n", chunk.Content)
}
reader.Close()
```

## Built-in Tools

### DuckDuckGo Search Tool

The DuckDuckGo tool is based on the DuckDuckGo Instant Answer API and provides factual and encyclopedia-style information search capabilities.

#### Basic Usage

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"

// Create a DuckDuckGo search tool.
searchTool := duckduckgo.NewTool()

// Integrate into an Agent.
searchAgent := llmagent.New("search-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{searchTool}))
```

#### Advanced Configuration

```go
import (
    "net/http"
    "time"
    "trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

// Custom configuration.
searchTool := duckduckgo.NewTool(
    duckduckgo.WithBaseURL("https://api.duckduckgo.com"),
    duckduckgo.WithUserAgent("my-app/1.0"),
    duckduckgo.WithHTTPClient(&http.Client{
        Timeout: 15 * time.Second,
    }),
)
```

## MCP Tools

MCP (Model Context Protocol) is an open protocol that standardizes how applications provide context to LLMs. MCP tools are based on JSON-RPC 2.0 and provide standardized integration with external services for Agents.

**MCP ToolSet Features:**

- 🔗 Unified interface: All MCP tools are created via `mcp.NewMCPToolSet()`.
- 🚀 Multiple transports: Supports STDIO, SSE, and Streamable HTTP.
- 🔧 Tool filters: Supports including/excluding specific tools.

### Basic Usage

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/mcp"

// Create an MCP ToolSet (STDIO example).
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",           // Transport method.
        Command:   "go",              // Command to execute.
        Args:      []string{"run", "./stdio_server/main.go"},
        Timeout:   10 * time.Second,
    },
    mcp.WithToolFilter(mcp.NewIncludeFilter("echo", "add")), // Optional: tool filter.
)

// Integrate into an Agent.
agent := llmagent.New("mcp-assistant",
    llmagent.WithModel(model),
    llmagent.WithToolSets([]tool.ToolSet{mcpToolSet}))
```

### Transport Configuration

MCP ToolSet supports three transports via the `Transport` field:

#### 1. STDIO Transport

Communicates with external processes via standard input/output. Suitable for local scripts and CLI tools.

```go
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",
        Command:   "python",
        Args:      []string{"-m", "my_mcp_server"},
        Timeout:   10 * time.Second,
    },
)
```

#### 2. SSE Transport

Uses Server-Sent Events for communication, supporting real-time data push and streaming responses.

```go
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
        Headers: map[string]string{
            "Authorization": "Bearer your-token",
        },
    },
)
```

#### 3. Streamable HTTP Transport

Uses standard HTTP for communication, supporting both regular HTTP and streaming responses.

```go
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "streamable_http",  // Use the full name.
        ServerURL: "http://localhost:3000/mcp",
        Timeout:   10 * time.Second,
    },
)
```

## Agent Tool (AgentTool)

AgentTool lets you expose an existing Agent as a tool to be used by a parent Agent. Compared with a plain function tool, AgentTool provides:

- ✅ Reuse: Wrap complex Agent capabilities as a standard tool
- 🌊 Streaming: Optionally forward the child Agent’s streaming events inline to the parent flow
- 🧭 Control: Options to skip post-tool summarization and to enable/disable inner forwarding

### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// 1) Define a reusable child Agent (streaming recommended)
mathAgent := llmagent.New(
    "math-specialist",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction("You are a math specialist..."),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
)

// 2) Wrap as an Agent tool
mathTool := agenttool.NewTool(
    mathAgent,
    agenttool.WithSkipSummarization(true), // opt-in: skip the outer summarization after tool.response
    agenttool.WithStreamInner(true),       // forward child Agent streaming events to parent flow
)

// 3) Use in parent Agent
parent := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
    llmagent.WithTools([]tool.Tool{mathTool}),
)
```

### Streaming Inner Forwarding

When `WithStreamInner(true)` is enabled, AgentTool forwards child Agent events to the parent flow as they happen:

- Forwarded items are actual `event.Event` instances, carrying incremental text in `choice.Delta.Content`
- To avoid duplication, the child Agent’s final full message is not forwarded again; it is aggregated into the final `tool.response` content for the next LLM turn (to satisfy providers requiring tool messages)
- UI guidance: show forwarded child deltas; avoid printing the aggregated final `tool.response` content unless debugging

Example: Only show tool fragments when needed to avoid duplicates

```go
if ev.Response != nil && ev.Object == model.ObjectTypeToolResponse {
    // Tool response contains aggregated content; skip printing by default to avoid duplicates
}

// Child Agent forwarded deltas (author != parent)
if ev.Author != parentName && len(ev.Choices) > 0 {
    if delta := ev.Choices[0].Delta.Content; delta != "" {
        fmt.Print(delta)
    }
}
```

### Options

- WithSkipSummarization(bool):
  - false (default): Allow an additional summarization/answer call after the tool result
  - true: Skip the outer summarization LLM call once the tool returns

- WithStreamInner(bool):
  - true: Forward child Agent events to the parent flow (recommended: enable `GenerationConfig{Stream: true}` for both parent and child Agents)
  - false: Treat as a callable-only tool, without inner event forwarding

### Notes

- Completion signaling: Tool response events are marked `RequiresCompletion=true`; Runner sends completion automatically
- De-duplication: When inner deltas are forwarded, avoid printing the aggregated final `tool.response` text again by default
- Model compatibility: Some providers require a tool message after tool_calls; AgentTool automatically supplies the aggregated content

## Tool Integration and Usage

### Create an Agent and Integrate Tools

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
    "trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// Create function tools.
calculatorTool := function.NewFunctionTool(calculator,
    function.WithName("calculator"),
    function.WithDescription("Perform basic mathematical operations."))

timeTool := function.NewFunctionTool(getCurrentTime,
    function.WithName("current_time"), 
    function.WithDescription("Get the current time."))

// Create a built-in tool.
searchTool := duckduckgo.NewTool()

// Create MCP ToolSets (examples for different transports).
stdioToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",
        Command:   "python",
        Args:      []string{"-m", "my_mcp_server"},
        Timeout:   10 * time.Second,
    },
)

sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
    },
)

streamableToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "streamable_http",
        ServerURL: "http://localhost:3000/mcp",
        Timeout:   10 * time.Second,
    },
)

// Create an Agent and integrate all tools.
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("You are a helpful AI assistant that can use various tools to help users."),
    // Add single tools (Tool interface).
    llmagent.WithTools([]tool.Tool{
        calculatorTool, timeTool, searchTool,
    }),
    // Add ToolSets (ToolSet interface).
    llmagent.WithToolSets([]tool.ToolSet{stdioToolSet, sseToolSet, streamableToolSet}),
)
```

### Tool Filters

```go
// Include filter: only allow specified tools.
includeFilter := mcp.NewIncludeFilter("get_weather", "get_news", "calculator")

// Exclude filter: exclude specified tools.
excludeFilter := mcp.NewExcludeFilter("deprecated_tool", "slow_tool")

// Combined filters.
combinedToolSet := mcp.NewMCPToolSet(
    connectionConfig,
    mcp.WithToolFilter(includeFilter),
)
```

### Parallel Tool Execution

```go
// Enable parallel tool execution (optional, for performance optimization).
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(tools),
    llmagent.WithToolSets(toolSets),
    llmagent.WithEnableParallelTools(true), // Enable parallel execution.
)
```

**Parallel execution effect:**

```bash
# Parallel execution (enabled).
Tool 1: get_weather     [====] 50ms
Tool 2: get_population  [====] 50ms  
Tool 3: get_time       [====] 50ms
Total time: ~50ms (executed simultaneously)

# Serial execution (default).
Tool 1: get_weather     [====] 50ms
Tool 2: get_population       [====] 50ms
Tool 3: get_time                  [====] 50ms  
Total time: ~150ms (executed sequentially)
```

## Quick Start

### Environment Setup

```bash
# Set API key.
export OPENAI_API_KEY="your-api-key"
```

### Simple Example

```go
package main

import (
    "context"
    "fmt"
    
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
    // 1. Create a simple tool.
    calculatorTool := function.NewFunctionTool(
        func(ctx context.Context, req struct {
            Operation string  `json:"operation"`
            A         float64 `json:"a"`
            B         float64 `json:"b"`
        }) (map[string]interface{}, error) {
            var result float64
            switch req.Operation {
            case "add":
                result = req.A + req.B
            case "multiply":
                result = req.A * req.B
            default:
                return nil, fmt.Errorf("unsupported operation")
            }
            return map[string]interface{}{"result": result}, nil
        },
        function.WithName("calculator"),
        function.WithDescription("Simple calculator."),
    )
    
    // 2. Create model and Agent.
    llmModel := openai.New("DeepSeek-V3-Online-64K")
    agent := llmagent.New("calculator-assistant",
        llmagent.WithModel(llmModel),
        llmagent.WithInstruction("You are a math assistant."),
        llmagent.WithTools([]tool.Tool{calculatorTool}),
        llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}), // Enable streaming output.
    )
    
    // 3. Create Runner and execute.
    r := runner.NewRunner("math-app", agent)
    
    ctx := context.Background()
    userMessage := model.NewUserMessage("Please calculate 25 times 4.")
    
    eventChan, err := r.Run(ctx, "user1", "session1", userMessage)
    if err != nil {
        panic(err)
    }
    
    // 4. Handle responses.
    for event := range eventChan {
        if event.Error != nil {
            fmt.Printf("Error: %s\n", event.Error.Message)
            continue
        }
        
        // Display tool calls.
        if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
            for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
                fmt.Printf("🔧 Call tool: %s\n", toolCall.Function.Name)
                fmt.Printf("   Params: %s\n", string(toolCall.Function.Arguments))
            }
        }
        
        // Display streaming content.
        if len(event.Response.Choices) > 0 {
            fmt.Print(event.Response.Choices[0].Delta.Content)
        }
        
        if event.Done {
            break
        }
    }
}
```

### Run the Examples

```bash
# Enter the tool example directory.
cd examples/tool
go run .

# Enter the MCP tool example directory.  
cd examples/mcp_tool

# Start the external server.
cd streamalbe_server && go run main.go &

# Run the main program.
go run main.go -model="deepseek-chat"
```

## Summary

The Tool system provides rich extensibility for tRPC-Agent-Go, supporting Function Tools, the DuckDuckGo Search Tool, and MCP protocol tools.
