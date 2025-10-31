# Multi-Agent Tool Filtering Example

This example demonstrates the per-run tool filtering feature, showing how to control which tools are available to agents during runtime. It supports both global filtering (`WithAllowedTools`) and agent-specific filtering (`WithAllowedAgentTools`).

## Features

- **Two filtering modes**: 
  - `WithAllowedTools`: Global filtering applied to all agents uniformly
  - `WithAllowedAgentTools`: Agent-specific filtering for fine-grained control
- **Multi-agent architecture**: Coordinator agent with specialized sub-agents
- **Per-run configuration**: Tool filtering only affects the current request
- **Request visibility**: OpenAI callback shows which tools are actually sent to the LLM
- **Token optimization**: Reduce API costs by limiting tool descriptions sent to the model
- **Priority system**: Agent-specific filters override global filters

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Architecture

This demo includes:

- **Coordinator Agent**: Main agent that delegates to sub-agents
- **Math Agent**: Has `calculator` and `text_tool`
- **Time Agent**: Has `time_tool` and `text_tool`

## Usage

### No Filtering

Run without filtering - all agents use all their tools:

```bash
cd examples/tool_filter
go run main.go
```

### Agent-Specific Filtering

Restrict tools for specific agents:

```bash
# Restrict math-agent to only calculator (text_tool filtered out)
go run main.go -filter=restrict-math

# Restrict time-agent to only time_tool (text_tool filtered out)
go run main.go -filter=restrict-time
```

## Implementation

### 1. Create Sub-Agents with Tools

Each sub-agent is created with its own tools:

```go
// Math agent with calculator and text tools
mathAgent := llmagent.New(
    "math-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{
        createCalculatorTool(),
        createTextTool(),
    }),
)

// Time agent with time and text tools
timeAgent := llmagent.New(
    "time-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{
        createTimeTool(),
        createTextTool(),
    }),
)
```

### 2. Create Coordinator with Sub-Agents

```go
coordinatorAgent := llmagent.New(
    "coordinator",
    llmagent.WithModel(modelInstance),
    llmagent.WithSubAgents([]agent.Agent{mathAgent, timeAgent}),
)
```

### 3. Apply Agent-Specific Tool Filtering

Use `agent.WithAllowedAgentTools()` to filter tools for specific agents:

```go
// Restrict math-agent to only calculator
eventChan, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithAllowedAgentTools(map[string][]string{
        "math-agent": {"calculator"},  // text_tool filtered out
        // time-agent not specified, so uses all its tools
    }),
)
```

### 4. Add OpenAI Request Callback for Debugging

This example includes a callback to show which tools are sent to the LLM:

```go
import openaigo "github.com/openai/openai-go"

modelInstance := openai.New(
    modelName,
    openai.WithChatRequestCallback(func(ctx context.Context, req *openaigo.ChatCompletionNewParams) {
        // Print tools in the request
        if len(req.Tools) > 0 {
            toolNames := make([]string, 0, len(req.Tools))
            for _, t := range req.Tools {
                toolNames = append(toolNames, t.Function.Name)
            }
            fmt.Printf("📋 Tools in OpenAI request: %v\n", toolNames)
        } else {
            fmt.Printf("📋 Tools in OpenAI request: [none]\n")
        }
    }),
)
```

This callback helps verify that tool filtering is working correctly by showing exactly which tools are included in each LLM request.

### 5. Dynamic Filtering Based on Mode

```go
func getAgentToolsFilter(filterMode string) map[string][]string {
    switch filterMode {
    case "restrict-math":
        return map[string][]string{
            "math-agent": {"calculator"},
        }
    case "restrict-time":
        return map[string][]string{
            "time-agent": {"time_tool"},
        }
    default:
        return nil // no filter
    }
}
```

## API Reference

### WithAllowedTools

```go
func WithAllowedTools(toolNames []string) RunOption
```

Filters the tools available to **all agents** for a specific run (global filtering).

- **Parameters**: `toolNames` - list of tool names to allow
- **Returns**: `RunOption` - option to pass to `runner.Run()`
- **Behavior**: If empty/nil, no filtering is applied (default)
- **Scope**: Applies to main agent and all sub-agents

Example:

```go
// Allow only calculator and time_tool for all agents
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedTools([]string{"calculator", "time_tool"}),
)
```

### WithAllowedAgentTools

```go
func WithAllowedAgentTools(agentTools map[string][]string) RunOption
```

Filters tools for specific agents (useful for multi-agent systems).

- **Parameters**: `agentTools` - map of agent name to allowed tool names
- **Returns**: `RunOption` - option to pass to `runner.Run()`
- **Priority**: `AllowedAgentTools` > `AllowedTools` > default

Example:

```go
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedAgentTools(map[string][]string{
        "agent1": {"tool_a", "tool_b"},
        "agent2": {"tool_c"},
    }),
)
```

## Use Cases

### 1. Global Tool Filtering (Single Agent or Simple Scenarios)

Use `WithAllowedTools` when you want to apply the same tool restrictions to all agents:

```go
// Scenario: Trial mode with limited tools for all agents
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedTools([]string{"calculator", "time_tool"}))

// All agents (including sub-agents) will only see these two tools
// text_tool and any other tools will be filtered out
```

**When to use:**
- Single agent systems
- Simple filtering applied uniformly across all agents
- Trial/demo modes with limited functionality

### 2. Agent-Specific Tool Filtering (Multi-Agent Systems)

Use `WithAllowedAgentTools` for fine-grained control in multi-agent architectures:

```go
// Restrict dangerous tools for certain agents
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedAgentTools(map[string][]string{
        "public-agent": {"search", "calculator"},  // Safe tools only
        "admin-agent": nil,                        // All tools (no restriction)
    }))
```

**When to use:**
- Multi-agent systems with different capabilities
- Agent-specific permission control
- Different security levels per agent

### 3. Cost Optimization per Agent

```go
// Reduce token usage by filtering unnecessary tools
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedAgentTools(map[string][]string{
        "math-agent": {"calculator"},              // Only math tools
        "time-agent": {"time_tool"},               // Only time tools
    }))
```

### 4. Dynamic Agent Capabilities

```go
// Adjust agent capabilities based on context
var agentTools map[string][]string
if isTrialMode {
    agentTools = map[string][]string{
        "assistant": {"calculator", "time_tool"},  // Limited
    }
} else {
    agentTools = nil  // Full capabilities
}

runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedAgentTools(agentTools))
```

### 5. Combined Global and Agent-Specific Filtering

```go
// Global baseline + agent-specific overrides
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedTools([]string{"calculator", "time_tool", "text_tool"}),
    agent.WithAllowedAgentTools(map[string][]string{
        "restricted-agent": {"calculator"},  // More restrictive than global
        // Other agents will use the global filter
    }))

// Priority: AllowedAgentTools > AllowedTools > agent's default tools
// - restricted-agent: only calculator
// - other agents: calculator, time_tool, text_tool
```

**When to use:**
- Set a baseline restriction for all agents, then override for specific ones
- Useful for role-based access with defaults

## Security Considerations

⚠️ **Important**: Tool filtering is a "soft" constraint for UX and cost optimization.

**Tool filtering should be used in conjunction with, not as a replacement for, tool-level security:**

```go
func sensitiveToolHandler(ctx context.Context, req Request) (Response, error) {
    // Always implement authorization in the tool itself
    if !hasPermission(ctx, req) {
        return Response{}, errors.New("permission denied")
    }
    // ... actual operation
}
```

**Why?**
- Models may attempt to use tools not in the list (e.g., common tools like `read_file`)
- Historical context may contain information about filtered tools
- Malicious users might try to manipulate the system

**Tool filtering provides:**
- Better UX (prevents unnecessary tool calls)
- Cost optimization (fewer tool descriptions in prompts)
- Reduced error messages (model won't try to use unavailable tools)

## Environment Variables

Set your OpenAI API key:

```bash
export OPENAI_API_KEY="your-api-key"
```

Or use a compatible endpoint (recommended for testing):

```bash
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-deepseek-key"
```

## How to Run

### Interactive Mode

```bash
cd examples/tool_filter

# No filtering - all agents use all their tools
go run main.go

# Restrict math-agent to only calculator
go run main.go -filter=restrict-math

# Restrict time-agent to only time_tool
go run main.go -filter=restrict-time
```

### One-Shot Testing

```bash
# Test without filtering
echo "Calculate 5+3" | go run main.go

# Test with math-agent restriction
echo "Calculate 5+3" | go run main.go -filter=restrict-math

# Test with time-agent restriction
echo "What time is it?" | go run main.go -filter=restrict-time
```

## Expected Output

The demo includes OpenAI request callbacks that show which tools are actually sent to the model. This helps verify that tool filtering is working correctly.

### Example 1: Without Filtering (All Tools Available)

```bash
echo "Calculate 5+3" | go run main.go
```

**Expected output:**
```
🚀 Multi-Agent Tool Filtering Demo
Model: deepseek-chat
...
👤 User: 🤖 Assistant: 📋 Tools in OpenAI request: [transfer_to_agent]
I'll transfer this to the math specialist agent.
🔧 Tool calls: → transfer_to_agent (ID: call_xxx)

⚡ Executing...

🤖 Assistant: 📋 Tools in OpenAI request: [text_tool calculator]
🔧 Tool calls: → calculator (ID: call_xxx)
   Arguments: {"expression": "5+3"}

⚡ Executing...

🤖 Assistant: 📋 Tools in OpenAI request: [calculator text_tool]
The result of 5+3 is **8**.
```

**Key observation:** math-agent has **both tools**: `[text_tool calculator]`

### Example 2: With restrict-math (Filter math-agent's text_tool)

```bash
echo "Calculate 5+3" | go run main.go -filter=restrict-math
```

**Expected output:**
```
🚀 Multi-Agent Tool Filtering Demo
Model: deepseek-chat
Filter Mode: restrict-math
...
   Tool filtering is active:
   - math-agent: only calculator (text_tool filtered out)
   - time-agent: all tools

👤 User: 🔒 Agent tool filter active: map[math-agent:[calculator]]
🤖 Assistant: 📋 Tools in OpenAI request: [transfer_to_agent]
I'll transfer this to the math specialist agent.
🔧 Tool calls: → transfer_to_agent (ID: call_xxx)

⚡ Executing...

🤖 Assistant: 📋 Tools in OpenAI request: [calculator]
🔧 Tool calls: → calculator (ID: call_xxx)
   Arguments: {"expression": "5+3"}

⚡ Executing...

🤖 Assistant: 📋 Tools in OpenAI request: [calculator]
The result of 5+3 is **8**.
```

**Key observation:** math-agent has **only one tool**: `[calculator]` ✅ text_tool filtered!

### Example 3: With restrict-time (Filter time-agent's text_tool)

```bash
echo "What time is it?" | go run main.go -filter=restrict-time
```

**Expected output:**
```
🚀 Multi-Agent Tool Filtering Demo
Model: deepseek-chat
Filter Mode: restrict-time
...
   Tool filtering is active:
   - math-agent: all tools
   - time-agent: only time_tool (text_tool filtered out)

👤 User: 🔒 Agent tool filter active: map[time-agent:[time_tool]]
🤖 Assistant: 📋 Tools in OpenAI request: [transfer_to_agent]
I'll transfer you to the time-agent.
🔧 Tool calls: → transfer_to_agent (ID: call_xxx)

⚡ Executing...

🤖 Assistant: 📋 Tools in OpenAI request: [time_tool]
🔧 Tool calls: → time_tool (ID: call_xxx)
   Arguments: {"operation": "current"}

⚡ Executing...

🤖 Assistant: 📋 Tools in OpenAI request: [time_tool]
The current time is 14:33:18 on October 31, 2025.
```

**Key observation:** time-agent has **only one tool**: `[time_tool]` ✅ text_tool filtered!

## Testing Guide

### Test Case 1: Math Operations

Test that math-agent can perform calculations:

```bash
# Without filtering - works with both tools
echo "Calculate 10+5" | go run main.go

# With restrict-math - works with calculator only
echo "Calculate 10+5" | go run main.go -filter=restrict-math
```

**What to verify:**
- Coordinator transfers to math-agent
- `📋 Tools in OpenAI request:` shows available tools
- Without filter: `[text_tool calculator]` or `[calculator text_tool]`
- With filter: `[calculator]` only

### Test Case 2: Time Queries

Test that time-agent can provide time information:

```bash
# Without filtering
echo "What time is it?" | go run main.go

# With restrict-time
echo "What time is it?" | go run main.go -filter=restrict-time
```

**What to verify:**
- Coordinator transfers to time-agent
- `📋 Tools in OpenAI request:` shows available tools
- Without filter: `[time_tool text_tool]` or `[text_tool time_tool]`
- With filter: `[time_tool]` only

### Test Case 3: Text Processing

Test that text processing is blocked when tool is filtered:

```bash
# Without filtering - should work
echo "Convert 'hello' to uppercase" | go run main.go

# With restrict-math - text_tool filtered, should fail or delegate
echo "Convert 'hello' to uppercase" | go run main.go -filter=restrict-math
```

**What to verify:**
- Without filter: Agent can use text_tool
- With filter: Agent either delegates or indicates inability to process

### Test Case 4: Interactive Mode

For comprehensive testing, run in interactive mode:

```bash
go run main.go -filter=restrict-math
```

Then try multiple queries in sequence:
1. `Calculate 5+3` - should work (calculator available)
2. `Convert 'test' to uppercase` - should fail or delegate (text_tool filtered)
3. `What time is it?` - should work (time-agent not restricted)

### Test Case 5: Global Tool Filtering (WithAllowedTools)

To test `WithAllowedTools` for global filtering, modify the code to use:

```go
// Instead of WithAllowedAgentTools
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedTools([]string{"calculator", "time_tool"}))
```

**Expected behavior:**
- Both math-agent and time-agent will have `text_tool` filtered out
- math-agent: `[calculator]` only
- time-agent: `[time_tool]` only
- Global filtering applies to all agents uniformly

**When to prefer:**
- Use `WithAllowedTools` for simple, uniform restrictions
- Use `WithAllowedAgentTools` for fine-grained, per-agent control

## Debugging Tips

### Understanding the Output

1. **Tool Filter Status Line:**
   ```
   🔒 Agent tool filter active: map[math-agent:[calculator]]
   ```
   Shows which agents have restrictions and what tools they can use.

2. **OpenAI Request Tools:**
   ```
   📋 Tools in OpenAI request: [calculator]
   ```
   Shows exactly which tools are sent to the LLM in each request.

3. **No Filter Message:**
   ```
   No filtering - all agents can use all their tools
   ```
   Indicates all tools are available without restrictions.

### Verification Checklist

- [ ] Coordinator always has `transfer_to_agent` tool
- [ ] Sub-agent tools match the filter configuration
- [ ] Tool filtering is consistent across multiple requests
- [ ] Filtered tools don't appear in OpenAI requests
- [ ] Unfiltered agents retain all their tools

## Comparison with Model Switching

Tool filtering follows the same pattern as model switching:

| Feature | Model Switching | Tool Filtering |
|---------|----------------|----------------|
| Scope | Per-run | Per-run |
| API | `agent.WithModelName()` | `agent.WithAllowedTools()` |
| Priority | `Model` > `ModelName` > default | `AllowedAgentTools` > `AllowedTools` > default |
| Fallback | Agent's default model | Agent's registered tools |
| Use Case | Dynamic model selection | Dynamic tool selection |

## Advanced Usage

### SubAgent Tool Filtering

```go
mainAgent := llmagent.New("main",
    llmagent.WithSubAgents([]agent.Agent{agent1, agent2}))

runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedAgentTools(map[string][]string{
        "agent1": {"tool_a", "tool_b"},
        "agent2": {"tool_c"},
    }))
```

### Combined Filtering

```go
// Global filter + agent-specific filter
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedTools([]string{"calculator", "time_tool"}),
    agent.WithAllowedAgentTools(map[string][]string{
        "subagent1": {"calculator"},
    }))
```

## Summary

This example demonstrates:

✅ **Multi-Agent Tool Filtering**: Restrict tools for specific sub-agents in a coordinator pattern

✅ **Dynamic Per-Run Filtering**: Apply different tool restrictions based on runtime conditions using command-line flags

✅ **Request-Level Visibility**: Use OpenAI callbacks to verify which tools are sent to the LLM

✅ **Agent-Specific vs Global Filtering**: Fine-grained control with `WithAllowedAgentTools` or broad control with `WithAllowedTools`

✅ **Cost Optimization**: Reduce token usage by limiting tool descriptions in prompts

✅ **Permission Control**: Implement soft constraints on tool access (always combine with tool-level security)

### Key Takeaways

1. **Tool filtering is set per-run**: Use `agent.WithAllowedTools()` or `agent.WithAllowedAgentTools()` when calling `runner.Run()`, not at agent creation time
2. **Two filtering approaches**:
   - `WithAllowedTools`: Global filtering applied uniformly to all agents
   - `WithAllowedAgentTools`: Agent-specific filtering for fine-grained control in multi-agent systems
3. **Priority order**: `AllowedAgentTools` > `AllowedTools` > agent's default tools
4. **Debugging visibility**: The `📋 Tools in OpenAI request` output shows exactly which tools are sent to the LLM, helping verify filtering
5. **Security note**: Tool filtering is a **UX/cost optimization**, not a security feature—always implement tool-level authorization
6. **Token efficiency**: Filtering reduces the number of tool descriptions sent to the model, saving tokens and costs

### Common Patterns

```go
// Pattern 1: No filtering (default)
runner.Run(ctx, userID, sessionID, message)

// Pattern 2: Global tool filtering
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedTools([]string{"calculator", "time_tool"}))

// Pattern 3: Agent-specific filtering (recommended for multi-agent)
runner.Run(ctx, userID, sessionID, message,
    agent.WithAllowedAgentTools(map[string][]string{
        "math-agent": {"calculator"},
        "time-agent": {"time_tool"},
    }))
```

