# Multi-Agent Tool Filtering Example

This example demonstrates the per-run tool filtering feature using the new `WithToolFilter` API, showing how to control which tools are available to agents during runtime.

## Features

- **Flexible filtering with `tool.FilterFunc`**: Custom filter functions for fine-grained control
- **Built-in helper functions**:
  - `tool.NewIncludeToolNamesFilter()`: Whitelist specific tools
  - `tool.NewExcludeToolNamesFilter()`: Blacklist specific tools
- **Custom per-agent filtering**: Example showing how to implement agent-specific logic
- **Multi-agent architecture**: Coordinator agent with specialized sub-agents
- **Per-run configuration**: Tool filtering only affects the current request
- **Request visibility**: OpenAI callback shows which tools are actually sent to the LLM
- **Token optimization**: Reduce API costs by limiting tool descriptions sent to the model
- **Framework tool protection**: Built-in tools (like `transfer_to_agent`) are never filtered

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

### Filtering Modes

```bash
# Demo 1: Exclude specific tools using NewExcludeToolNamesFilter
go run main.go -filter=exclude-demo

# Demo 2: Include only specific tools using NewIncludeToolNamesFilter
go run main.go -filter=include-demo

# Demo 3: Per-agent filtering with custom FilterFunc
go run main.go -filter=per-agent
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

### 3. Apply Tool Filtering with `WithToolFilter`

Use `agent.WithToolFilter()` with built-in helper functions or custom logic:

```go
// Demo 1: Exclude specific tools (exclude-demo)
filter := tool.NewExcludeToolNamesFilter("text_tool")
eventChan, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithToolFilter(filter),
)

// Demo 2: Include only specific tools (include-demo)
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
eventChan, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithToolFilter(filter),
)

// Demo 3: Custom per-agent filtering (per-agent)
filter := createPerAgentFilter()
eventChan, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithToolFilter(filter),
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

### 5. Custom Per-Agent Filtering

This example shows how to implement agent-specific filtering in user code:

```go
func createPerAgentFilter() tool.FilterFunc {
    // Define per-agent allowed tools
    agentAllowedTools := map[string]map[string]bool{
        "math-agent": {
            "calculator": true,
        },
        "time-agent": {
            "time_tool": true,
        },
    }

    return func(ctx context.Context, t tool.Tool) bool {
        declaration := t.Declaration()
        if declaration == nil {
            return false
        }
        toolName := declaration.Name

        // Get the current agent name from invocation context
        inv, ok := agent.InvocationFromContext(ctx)
        if !ok || inv == nil {
            return true // fallback: allow all tools
        }

        agentName := inv.AgentName

        // Check if this tool is allowed for the current agent
        allowedTools, exists := agentAllowedTools[agentName]
        if !exists {
            return true // fallback: allow all tools
        }

        // Return true only if the tool is in the agent's allowed list
        return allowedTools[toolName]
    }
}
```

**Key Insight**: The `FilterFunc` receives `context.Context`, which contains the invocation information. You can use `agent.InvocationFromContext(ctx)` to get the current agent name (`inv.AgentName`) and apply agent-specific filtering rules.

## API Reference

### WithToolFilter

```go
func WithToolFilter(filter tool.FilterFunc) RunOption
```

Applies a custom filter function to control which tools are available for a specific run.

- **Parameters**: `filter` - a function of type `tool.FilterFunc`
- **Returns**: `RunOption` - option to pass to `runner.Run()`
- **Behavior**: If nil, no filtering is applied (default)
- **Scope**: Applies to all agents, but framework tools are never filtered

**FilterFunc signature:**

```go
type FilterFunc func(ctx context.Context, tool Tool) bool
```

- Returns `true` to include the tool, `false` to filter it out
- Receives `context.Context` for accessing contextual information
- Framework tools (like `transfer_to_agent`, `knowledge_search`) are automatically preserved

Example:

```go
// Use built-in helper
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)

// Custom filter function
filter := func(ctx context.Context, t tool.Tool) bool {
    declaration := t.Declaration()
    if declaration == nil {
        return false
    }
    // Custom logic
    return strings.HasPrefix(declaration.Name, "safe_")
}
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)
```

### Helper Functions

#### NewIncludeToolNamesFilter

```go
func NewIncludeToolNamesFilter(names ...string) FilterFunc
```

Creates a filter that **includes only** the specified tool names (whitelist).

Example:

```go
// Only allow calculator and time_tool
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
runner.Run(ctx, userID, sessionID, message, agent.WithToolFilter(filter))
```

#### NewExcludeToolNamesFilter

```go
func NewExcludeToolNamesFilter(names ...string) FilterFunc
```

Creates a filter that **excludes** the specified tool names (blacklist).

Example:

```go
// Block text_tool, allow everything else
filter := tool.NewExcludeToolNamesFilter("text_tool")
runner.Run(ctx, userID, sessionID, message, agent.WithToolFilter(filter))
```

## Use Cases

### 1. Simple Whitelist Filtering

Use `NewIncludeToolNamesFilter` when you want to allow only specific tools:

```go
// Scenario: Trial mode with limited tools for all agents
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))

// All agents will only see calculator and time_tool
// text_tool and any other tools will be filtered out
// Framework tools (transfer_to_agent, knowledge_search) are auto-included
```

**When to use:**
- Trial/demo modes with limited functionality
- Simple filtering applied uniformly across all agents
- When you have a small set of allowed tools

### 2. Simple Blacklist Filtering

Use `NewExcludeToolNamesFilter` when you want to block specific tools:

```go
// Scenario: Block dangerous or expensive tools
filter := tool.NewExcludeToolNamesFilter("delete_file", "execute_code")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))

// All tools except delete_file and execute_code are available
```

**When to use:**
- Blocking specific dangerous or expensive tools
- When you have a small set of tools to exclude
- Simpler than maintaining a whitelist

### 3. Per-Agent Filtering (Custom Logic)

Implement custom filter functions for agent-specific control:

```go
// Define per-agent rules
agentRules := map[string]map[string]bool{
    "math-agent": {"calculator": true},
    "time-agent": {"time_tool": true},
}

filter := func(ctx context.Context, t tool.Tool) bool {
    declaration := t.Declaration()
    if declaration == nil {
        return false
    }

    // Get agent name from invocation context
    inv, ok := agent.InvocationFromContext(ctx)
    if !ok || inv == nil {
        return true // fallback: allow all tools
    }

    agentName := inv.AgentName

    // Apply agent-specific rules
    if allowedTools, ok := agentRules[agentName]; ok {
        return allowedTools[declaration.Name]
    }
    return true // default: allow
}

runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))
```

**When to use:**
- Multi-agent systems with different capabilities per agent
- Agent-specific permission control
- Different security levels per agent

### 4. Dynamic Filtering Based on User Permissions

```go
// Adjust capabilities based on user level
var filter tool.FilterFunc
userLevel := getUserLevel(ctx)

switch userLevel {
case "free":
    // Free users: only basic tools
    filter = tool.NewIncludeToolNamesFilter("calculator", "time_tool")
case "premium":
    // Premium users: exclude only dangerous tools
    filter = tool.NewExcludeToolNamesFilter("delete_file")
case "admin":
    // Admin users: no filtering
    filter = nil
}

runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))
```

**When to use:**
- Role-based access control
- Subscription-based feature gating
- Dynamic capability adjustment

### 5. Cost Optimization

```go
// Reduce token usage by filtering unnecessary tools
// Only include tools relevant to the current task
taskType := detectTaskType(userMessage)

var filter tool.FilterFunc
switch taskType {
case "math":
    filter = tool.NewIncludeToolNamesFilter("calculator")
case "time":
    filter = tool.NewIncludeToolNamesFilter("time_tool")
default:
    filter = nil // all tools
}

runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))
```

**When to use:**
- Reducing API costs by limiting tool descriptions
- Task-specific tool selection
- Optimizing token usage

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

# Demo 1: Exclude specific tools
go run main.go -filter=exclude-demo

# Demo 2: Include only specific tools
go run main.go -filter=include-demo

# Demo 3: Per-agent filtering
go run main.go -filter=per-agent
```

### One-Shot Testing

```bash
# Test without filtering
echo "Calculate 5+3" | go run main.go

# Test exclude-demo: excludes text_tool globally
echo "Calculate 5+3" | go run main.go -filter=exclude-demo

# Test include-demo: includes only calculator and time_tool
echo "What time is it?" | go run main.go -filter=include-demo

# Test per-agent: custom agent-specific filtering
echo "Calculate 5+3" | go run main.go -filter=per-agent
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

### Example 2: With exclude-demo (Exclude text_tool)

```bash
echo "Calculate 5+3" | go run main.go -filter=exclude-demo
```

**Expected output:**
```
🚀 Multi-Agent Tool Filtering Demo
Model: deepseek-chat
Filter Mode: exclude-demo
...
   Tool filtering is active:
   - Demo: tool.NewExcludeToolNamesFilter
   - Excludes: text_tool globally
   - Result: All agents can use calculator/time_tool, but NOT text_tool

👤 User: � Exclude filter: text_tool
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

**Key observation:** math-agent has **only calculator**: `[calculator]` ✅ text_tool excluded!

### Example 3: With include-demo (Include only calculator and time_tool)

```bash
echo "What time is it?" | go run main.go -filter=include-demo
```

**Expected output:**
```
🚀 Multi-Agent Tool Filtering Demo
Model: deepseek-chat
Filter Mode: include-demo
...
   Tool filtering is active:
   - Demo: tool.NewIncludeToolNamesFilter
   - Includes only: calculator, time_tool
   - Result: All agents can ONLY use calculator and time_tool

👤 User: ✅ Include filter: calculator, time_tool
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

**Key observation:** time-agent has **only time_tool**: `[time_tool]` ✅ Only whitelisted tools included!

### Example 4: With per-agent (Agent-specific filtering)

```bash
echo "Calculate 5+3" | go run main.go -filter=per-agent
```

**Expected output:**
```
🚀 Multi-Agent Tool Filtering Demo
Model: deepseek-chat
Filter Mode: per-agent
...
   Tool filtering is active:
   - Demo: Custom FilterFunc with agent.InvocationFromContext
   - math-agent: only calculator
   - time-agent: only time_tool
   - Shows how to implement agent-specific filtering

👤 User: 🎯 Per-agent filter:
   - math-agent: only calculator
   - time-agent: only time_tool
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

**Key observation:** Each agent has **different tools** based on custom logic! ✅ Agent-specific filtering works!

## Testing Guide

### Test Case 1: Exclude Filter

Test that `NewExcludeToolNamesFilter` works correctly:

```bash
# Without filtering - all tools available
echo "Calculate 10+5" | go run main.go

# With exclude-demo - text_tool excluded
echo "Calculate 10+5" | go run main.go -filter=exclude-demo
```

**What to verify:**
- Coordinator transfers to math-agent
- `📋 Tools in OpenAI request:` shows available tools
- Without filter: `[text_tool calculator]` or `[calculator text_tool]`
- With exclude-demo: `[calculator]` only (text_tool excluded)

### Test Case 2: Include Filter

Test that `NewIncludeToolNamesFilter` works correctly:

```bash
# Without filtering - all tools available
echo "What time is it?" | go run main.go

# With include-demo - only calculator and time_tool included
echo "What time is it?" | go run main.go -filter=include-demo
```

**What to verify:**
- Coordinator transfers to time-agent
- `📋 Tools in OpenAI request:` shows available tools
- Without filter: `[time_tool text_tool]` or `[text_tool time_tool]`
- With include-demo: `[time_tool]` only (only whitelisted tools)

### Test Case 3: Per-Agent Filter

Test that custom per-agent filtering works correctly:

```bash
# Test math-agent with per-agent filter
echo "Calculate 10+5" | go run main.go -filter=per-agent

# Test time-agent with per-agent filter
echo "What time is it?" | go run main.go -filter=per-agent
```

**What to verify:**
- math-agent: `📋 Tools in OpenAI request: [calculator]` (only calculator)
- time-agent: `📋 Tools in OpenAI request: [time_tool]` (only time_tool)
- Different agents have different tools based on custom logic

### Test Case 4: Interactive Mode

For comprehensive testing, run in interactive mode:

```bash
go run main.go -filter=exclude-demo
```

Then try multiple queries in sequence:
1. `Calculate 5+3` - should work (calculator available)
2. `Convert 'test' to uppercase` - should fail or delegate (text_tool excluded)
3. `What time is it?` - should work (time_tool available)

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
| API | `agent.WithModelName()` | `agent.WithToolFilter()` |
| Fallback | Agent's default model | Agent's registered tools |
| Use Case | Dynamic model selection | Dynamic tool selection |

## Advanced Usage

### Combining Multiple Filter Conditions

```go
// Create a composite filter that combines multiple conditions
filter := func(ctx context.Context, t tool.Tool) bool {
    declaration := t.Declaration()
    if declaration == nil {
        return false
    }

    // Condition 1: Exclude dangerous tools
    dangerousTools := map[string]bool{
        "delete_file": true,
        "execute_code": true,
    }
    if dangerousTools[declaration.Name] {
        return false
    }

    // Condition 2: Check user permission level
    userLevel := ctx.Value("user_level").(string)
    if userLevel == "free" {
        // Free users: only basic tools
        basicTools := map[string]bool{
            "calculator": true,
            "time_tool": true,
        }
        return basicTools[declaration.Name]
    }

    // Premium users: all non-dangerous tools
    return true
}

runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))
```

### Reusable Filter Factory

```go
// Create a factory function for common filter patterns
func createUserLevelFilter(userLevel string) tool.FilterFunc {
    switch userLevel {
    case "free":
        return tool.NewIncludeToolNamesFilter("calculator", "time_tool")
    case "premium":
        return tool.NewExcludeToolNamesFilter("admin_tool")
    case "admin":
        return nil // no filtering
    default:
        return tool.NewIncludeToolNamesFilter("calculator") // minimal access
    }
}

// Use it
filter := createUserLevelFilter(getUserLevel(ctx))
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))
```

## Summary

This example demonstrates:

✅ **Flexible Tool Filtering**: Use `tool.FilterFunc` for custom filtering logic

✅ **Built-in Helpers**: `NewIncludeToolNamesFilter` and `NewExcludeToolNamesFilter` for common patterns

✅ **Per-Agent Filtering**: Example showing how to implement agent-specific filtering in user code

✅ **Dynamic Per-Run Filtering**: Apply different tool restrictions based on runtime conditions

✅ **Request-Level Visibility**: Use OpenAI callbacks to verify which tools are sent to the LLM

✅ **Framework Tool Protection**: Built-in tools (like `transfer_to_agent`) are never filtered

✅ **Cost Optimization**: Reduce token usage by limiting tool descriptions in prompts

✅ **Permission Control**: Implement soft constraints on tool access (always combine with tool-level security)

### Key Takeaways

1. **Tool filtering is set per-run**: Use `agent.WithToolFilter()` when calling `runner.Run()`, not at agent creation time
2. **Three filtering approaches**:
   - `tool.NewIncludeToolNamesFilter()`: Whitelist specific tools (simple, recommended for most cases)
   - `tool.NewExcludeToolNamesFilter()`: Blacklist specific tools
   - Custom `FilterFunc`: Full control with custom logic (for per-agent filtering, etc.)
3. **Framework tools are protected**: Tools like `transfer_to_agent` and `knowledge_search` are never filtered
4. **Context-aware filtering**: `FilterFunc` receives `context.Context`, enabling per-agent or per-user filtering
5. **Debugging visibility**: The `📋 Tools in OpenAI request` output shows exactly which tools are sent to the LLM
6. **Security note**: Tool filtering is a **UX/cost optimization**, not a security feature—always implement tool-level authorization
7. **Token efficiency**: Filtering reduces the number of tool descriptions sent to the model, saving tokens and costs

### Common Patterns

```go
// Pattern 1: No filtering (default)
runner.Run(ctx, userID, sessionID, message)

// Pattern 2: Whitelist filtering (most common)
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))

// Pattern 3: Blacklist filtering
filter := tool.NewExcludeToolNamesFilter("dangerous_tool")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))

// Pattern 4: Custom per-agent filtering
filter := func(ctx context.Context, t tool.Tool) bool {
    // Get agent name from invocation context
    inv, ok := agent.InvocationFromContext(ctx)
    if !ok || inv == nil {
        return true
    }

    // Your custom logic here based on agent name
    // Example: only allow calculator for math-agent
    if inv.AgentName == "math-agent" {
        return t.Declaration().Name == "calculator"
    }

    return true // or false
}
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))
```

### Three Filtering Approaches

This example demonstrates three approaches to tool filtering:

```go
// Approach 1: Exclude specific tools (blacklist)
filter := tool.NewExcludeToolNamesFilter("text_tool", "dangerous_tool")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))

// Approach 2: Include only specific tools (whitelist)
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))

// Approach 3: Custom per-agent filtering
filter := func(ctx context.Context, t tool.Tool) bool {
    inv, ok := agent.InvocationFromContext(ctx)
    if !ok || inv == nil {
        return true
    }
    // Apply agent-specific rules
    // See the "Per-Agent Filtering" section for full example
    return true
}
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter))
```

