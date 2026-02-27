# MCP HTTP Headers Example

This example demonstrates how to dynamically set HTTP headers for MCP tool calls using the `HTTPBeforeRequest` feature.

## Overview

The `HTTPBeforeRequest` feature allows you to modify HTTP requests before they are sent to MCP servers. This is useful for:

- Adding authentication tokens
- Setting request IDs for tracing
- Adding user/session context headers
- Logging outgoing requests

## Quick Summary

**Two integration approaches:**

1. **`WithTools(tools)` (used in this example)** ⭐ **Recommended for dynamic headers**
    - ✅ Full control: manually call `toolSet.Tools(ctx)` with your context
    - ✅ Dynamic headers for **all** MCP requests: `initialize`, `tools/list`, `tools/call`, GET SSE
    - 📌 Use when: per-request authentication, tracing, user context

2. **`WithToolSets([]tool.ToolSet{toolSet})`**
    - ⚠️ By default, tool discovery uses `context.Background()`
    - ✅ Dynamic headers for: `tools/call`
    - ❌ No dynamic headers for: `initialize`, `tools/list`, GET SSE (by default)
    - 💡 With `WithRefreshToolSetsOnRun(true)`, tool discovery uses the run context
      (but refreshes the tool list on every run)
    - 📌 Use when: static API keys, simple scenarios
    - 💡 Can combine with `WithRequestHeader` for static headers

## Key Concepts

### HTTPBeforeRequest Function

The `HTTPBeforeRequestFunc` is called before each HTTP request to the MCP server:

```go
type HTTPBeforeRequestFunc func(ctx context.Context, req *http.Request) error
```

- **Input**: Context and HTTP request
- **Output**: Error (returning an error aborts the request)
- **Timing**: Called for ALL HTTP requests (tool calls, notifications, SSE connections)

### Context Propagation

The key to dynamic headers is using `context.Context` to pass data from `runner.Run()` to the HTTP layer:

```
runner.Run(ctx, ...) 
    ↓ (context flows through)
MCP tool call
    ↓ (context flows through)
HTTPBeforeRequest function
    ↓ (extract data from context)
Set HTTP headers
```

## How It Works

### 1. Define Context Keys

```go
type contextKey string

const (
    requestIDKey contextKey = "request-id"
    userIDKey    contextKey = "user-id"
)
```

### 2. Create HTTPBeforeRequest Function

```go
beforeRequest := func(ctx context.Context, req *http.Request) error {
    // Extract values from context
    if requestID, ok := ctx.Value(requestIDKey).(string); ok {
        req.Header.Set("X-Request-ID", requestID)
    }
    if userID, ok := ctx.Value(userIDKey).(string); ok {
        req.Header.Set("X-User-ID", userID)
    }
    return nil
}
```

### 3. Configure MCP ToolSet

```go
toolSet := mcp.NewMCPToolSet(
    config,
    mcp.WithMCPOptions(
        tmcp.WithHTTPBeforeRequest(beforeRequest),
    ),
)
```

### 4. Choose Your Integration Approach

There are **two ways** to integrate MCP tools with the agent, depending on whether you need dynamic headers for all requests:

#### **Approach A: WithTools (Recommended for Dynamic Headers)**

Use this when you need **full control** over the context for ALL MCP requests (initialize, tools/list, tools/call).

```go
// Step 1: Create context with values for setup phase
setupCtx := context.WithValue(ctx, requestIDKey, "req-setup-123")
setupCtx = context.WithValue(setupCtx, userIDKey, userID)
setupCtx = context.WithValue(setupCtx, sessionIDKey, sessionID)

// Step 2: Get tools manually with your context
// This triggers: initialize, tools/list (with your headers)
tools := toolSet.Tools(setupCtx)

// Step 3: Pass tools directly to agent
agent := llmagent.New(
    agentName,
    llmagent.WithTools(tools),  // ✅ Use WithTools
)

// Step 4: During runtime, create context for each request
ctx = context.WithValue(ctx, requestIDKey, "req-12345")
ctx = context.WithValue(ctx, userIDKey, userID)

// This triggers: tools/call (with your headers)
eventChan, err := runner.Run(ctx, userID, sessionID, message)
```

**Pros:**
- ✅ Full control over context for all MCP requests
- ✅ All requests (POST and GET SSE) have dynamic headers
- ✅ Suitable for per-request authentication tokens

**Cons:**
- ⚠️ Requires manual `Tools(ctx)` call (slightly more code)

---

#### **Approach B: WithToolSets (Simpler, Static Headers)**

Use this when you only need **static headers** or don't care about headers during initialization.

```go
// Configure static headers (optional)
toolSet := mcp.NewMCPToolSet(
    config,
    mcp.WithMCPOptions(
        tmcp.WithHTTPBeforeRequest(beforeRequest),
        tmcp.WithRequestHeader("Authorization", "Bearer static-token"),
    ),
)

// Pass toolset directly to agent
agent := llmagent.New(
    agentName,
    llmagent.WithToolSets([]tool.ToolSet{toolSet}),  // ✅ Use WithToolSets
)

// During runtime, create context for each request
ctx = context.WithValue(ctx, requestIDKey, "req-12345")
eventChan, err := runner.Run(ctx, userID, sessionID, message)
```

**Pros:**
- ✅ Simpler code (no manual Tools() call)
- ✅ Suitable for static headers (API keys, service names)

**Cons:**
- ❌ Without `WithRefreshToolSetsOnRun(true)`, initialize/tools/list won't see ctx values
  because tool discovery uses `context.Background()`
- ⚠️ With `WithRefreshToolSetsOnRun(true)`, initialize/tools/list can see ctx values,
  but tool discovery runs on every invocation
- ⚠️ Can combine with static headers via `WithRequestHeader`

---

#### **Summary**

| Aspect | Approach A (WithTools) | Approach B (WithToolSets) |
|--------|------------------------|---------------------------|
| **Setup Complexity** | Medium (manual Tools() call) | Low (automatic) |
| **initialize headers** | ✅ Dynamic | ❌ Static only |
| **tools/list headers** | ✅ Dynamic | ❌ Static only |
| **tools/call headers** | ✅ Dynamic | ✅ Dynamic |
| **GET SSE headers** | ✅ Dynamic | ❌ Static only |
| **Use Case** | Per-request auth tokens | Static API keys |

> Note: If you enable `WithRefreshToolSetsOnRun(true)`, Approach B will also propagate
> the run context during tool discovery (initialize/tools/list), at the cost of
> refreshing the tool list on every invocation.

**This example uses Approach A** to demonstrate full dynamic header control.

## Running the Example

### Option 1: Using go run

```bash
# Terminal 1: Start the SSE server
cd sseserver
go run main.go

# Terminal 2: Run the client
cd ..
go run main.go
```

### Option 2: Using compiled binaries

```bash
# Terminal 1: Start the SSE server
cd sseserver
./server

# Terminal 2: Run the client
cd ..
./client
```

## Example Output

```
🚀 MCP HTTP Headers Example
Type 'exit' to end the conversation
==================================================
👤 You: What's the weather?

🔧 Tool calls initiated:
   • get_weather (ID: call_123)
     Args: {"location":"current"}

📤 HTTP Request Headers:
   X-Request-ID: req-1699564234567
   X-User-ID: user-123
   X-Session-ID: session-456

✅ Tool response received: Weather is sunny, 72°F

🤖 Assistant: The weather is sunny with a temperature of 72°F.
```

## Advanced: Composing Multiple Functions

If you need multiple before-request functions, compose them yourself:

```go
compose := func(fns ...tmcp.HTTPBeforeRequestFunc) tmcp.HTTPBeforeRequestFunc {
    return func(ctx context.Context, req *http.Request) error {
        for _, fn := range fns {
            if err := fn(ctx, req); err != nil {
                return err
            }
        }
        return nil
    }
}

beforeRequest := compose(
    addAuthHeaders,
    addTracingHeaders,
    logRequest,
)
```

## Notes

- The `HTTPBeforeRequest` function is set once during MCP client initialization
- It's called for **every** HTTP request (tool calls, notifications, SSE connections)
- Use context to pass dynamic, per-request data
- Returning an error from the function will abort the HTTP request
