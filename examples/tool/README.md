# Tool Example

This example demonstrates how to use the Tool system with AI models, showing how to create function-based tools that can be called by language models during conversations. The example includes both streaming and non-streaming implementations with weather and population tools.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `MODEL_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work. The model will use the tools to provide more accurate and contextual responses.


## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `gpt-4o-mini` |

## Directory Structure

```
examples/tool/
├── README.md           # This file
├── main.go            # Main entry point and configuration
├── functool.go        # Tool function implementations
├── non-streaming.go   # Non-streaming example with tool usage
├── streaming.go       # Streaming example with multiple tools
└── run.sh            # Script to run with predefined configuration
```

## Available Tools

### 1. Weather Tool (`get_weather`)

Returns weather information for a given location.

**Input:**
```json
{
  "location": "string"
}
```

**Output:**
```json
{
  "weather": "string"
}
```

### 2. Population Tool (`get_population`)

Returns population information for a given city.

**Input:**
```json
{
  "city": "string"
}
```

**Output:**
```json
{
  "population": "number"
}
```

## Parallel Tool Execution

The framework supports **parallel tool execution** for improved performance when the LLM makes multiple independent tool calls simultaneously.

### Default Behavior (Parallel Execution)

When the LLM generates multiple tool calls that can be executed independently, the framework automatically executes them in parallel:

```go
// Multiple tool calls from LLM
toolCalls := []ToolCall{
    {Function: {Name: "get_weather", Arguments: `{"location": "New York"}`}},
    {Function: {Name: "get_population", Arguments: `{"city": "London"}`}},
    {Function: {Name: "get_current_time", Arguments: `{"timezone": "PST"}`}},
}

// All tools execute concurrently using goroutines
// Execution time: ~max(individual_tool_time) instead of sum(all_tool_times)
```

### Performance Benefits

- **🚀 Performance Improvement**: Parallel execution can be faster than serial execution
- **⚡ Reduced Latency**: Multiple tools execute simultaneously instead of sequentially  
- **🔄 Independent Operations**: Each tool call runs in its own goroutine
- **🛡️ Error Isolation**: Failure in one tool doesn't block others

### Example: Parallel vs Serial Execution

```bash
# Parallel execution (default)
Tool 1: get_weather     [====] 50ms
Tool 2: get_population  [====] 50ms  
Tool 3: get_time       [====] 50ms
Total time: ~50ms (all execute simultaneously)

# Serial execution (if enabled)
Tool 1: get_weather     [====] 50ms
Tool 2: get_population       [====] 50ms
Tool 3: get_time                  [====] 50ms  
Total time: ~150ms (sequential execution)
```

### Configuration Options

You can control parallel execution behavior using the following options:

#### 1. Enable Parallel Tools (Opt-in for Performance)

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"

// Default: Serial execution (safe and compatible)
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(model),
    llmagent.WithTools(tools),
    // Serial execution by default - no configuration needed
)

// Opt-in to parallel execution for performance
agentWithParallel := llmagent.New(
    "my-agent-parallel",
    llmagent.WithModel(model),
    llmagent.WithTools(tools),
    llmagent.WithEnableParallelTools(true), // Enable parallel execution
)
```

### When to Use Serial vs Parallel

**Use Serial (Default)** when:
- 🛡️ **Safe and compatible** default behavior
- 🔄 Tools have dependencies (output of Tool A needed for Tool B)
- 🐛 Debugging tool execution issues
- 📊 Precise ordering is required

**Use Parallel (Opt-in)** when:
- ✅ Tool calls are independent
- ✅ No dependencies between tools
- ⚡ **performance improvement** is desired
- ✅ Tools have similar execution times

## Running the Examples

### Using custom environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"

cd examples/tool
go run . -model="gpt-4o-mini"
```


## Example Output

The example demonstrates two scenarios:

### 1. Non-streaming Example

```
=== Non-streaming Example ===
Response: Based on the weather tool, the current weather in New York City is Sunny with a temperature of 25°C. It's a beautiful day!

Tool Calls:
- get_weather called with: {"location": "New York City"}
- Tool result: {"weather": "Sunny, 25°C"}

Usage: 150 tokens (prompt: 50, completion: 100)
Finish Reason: stop
```

### 2. Streaming Input Example

```
=== Streaming Example ===
Streaming response: 
Let me check the weather and population for London...

[Tool Call: get_weather]
Arguments: {"location": "London City"}
Result: {"weather": "Sunny, 25°C"}

[Tool Call: get_population]  
Arguments: {"city": "London City"}
Result: {"population": 8000000}

Based on the information I retrieved:
- Weather in London: Sunny, 25°C
- Population of London: approximately 8,000,000 people

Usage: 180 tokens (prompt: 60, completion: 120)
```

### 2. Streaming Output Example

The data results of Streaming Output are similar to Non-streaming, Streaming Output merges all the stream data

```
=== Non-streaming Example ===
Response: Based on the weather tool, the current weather in New York City is Sunny with a temperature of 25°C. It's a beautiful day!

Tool Calls:
- get_weather called with: {"location": "New York City"}
- Tool result: {"weather": "Sunny, 25°C"}

Usage: 150 tokens (prompt: 50, completion: 100)
Finish Reason: stop
```

