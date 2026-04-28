# Tool Usage Guide

The Tool system is a core component of the tRPC-Agent-Go framework, enabling Agents to interact with external services and functions. The framework supports multiple tool types, including Function Tools and external tools integrated via the MCP (Model Context Protocol) standard.

## Overview

### 🎯 Key Features

- **🔧 Multiple Tool Types**: Supports Function Tools and MCP standard tools.
- **🌊 Streaming Responses**: Supports both real-time streaming responses and normal responses.
- **⚡ Parallel Execution**: Tool invocations support parallel execution to improve performance.
- **🔄 MCP Protocol**: Full support for STDIO, SSE, and Streamable HTTP transports.
- **🔁 Tool Call Retry**: Supports retrying callable tool calls in LLMAgent and Graph ToolsNode.
- **🛠️ Configuration Support**: Provides configuration options and filter support.
- **🧹 Arguments Repair**: Optionally enable `agent.WithToolCallArgumentsJSONRepairEnabled(true)` to best-effort repair `tool_calls` `arguments`, improving robustness for tool execution and external parsing.

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

**Recommendation (always configure `name` and `description`)**

- **name (required)**: Used by the model to precisely select and invoke the tool. Keep it **stable, unique, and descriptive** (prefer `snake_case`), and avoid name collisions across Tools/ToolSets.
- **description (required)**: Used by the model to understand what the tool does, when to use it, and any constraints. Missing or vague descriptions will noticeably reduce tool-call accuracy and stability.

> For Function Tools, set these via `function.WithName(...)` / `function.WithDescription(...)`. For custom Tools, set `Name` / `Description` on the `tool.Declaration` returned by `Declaration()`.

#### 📦 ToolSet

A ToolSet is a collection of related tools that implements the `tool.ToolSet` interface. A ToolSet manages the lifecycle of tools, connections, and resource cleanup.

```go
type ToolSet interface {
    // Tools returns the current tools in this set.
    Tools(context.Context) []tool.Tool

    // Close releases any resources held by the ToolSet.
    Close() error

    // Name returns the identifier of the ToolSet, used for
    // identification and conflict resolution.
    Name() string
}
```

**Relationship between Tool and ToolSet:**

- One "Tool" = one concrete capability (e.g., calculator).
- One "ToolSet" = a group of related Tools (e.g., all tools provided by an MCP server).
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

| Tool Type                  | Definition                                         | Integration Method                                    |
| -------------------------- | -------------------------------------------------- | ----------------------------------------------------- |
| **Function Tools**         | Tools implemented by directly calling Go functions | `Tool` interface, in-process calls                    |
| **Agent Tool (AgentTool)** | Wrap any Agent as a callable tool                  | `Tool` interface, supports streaming inner forwarding |
| **DuckDuckGo Tool**        | Search tool based on DuckDuckGo API                | `Tool` interface, HTTP API                            |
| **MCP ToolSet**            | External toolset based on MCP protocol             | `ToolSet` interface, multiple transports              |

> **📖 Related docs**: For Agent Tool and Transfer Tool used in multi-Agent collaboration, see the Multi-Agent System document.

## Function Tools

Function Tools implement tool logic directly via Go functions and are the simplest tool type.

### Basic Usage

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/function"

// 1. Define a tool function.
func calculator(ctx context.Context, req struct {
    Operation string  `json:"operation" jsonschema:"description=Operation type e.g. add/multiply"`
    A         float64 `json:"a" jsonschema:"description=First operand"`
    B         float64 `json:"b" jsonschema:"description=Second operand"`
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

### Input Schema and field descriptions

For Function Tools, the input `req` is automatically converted into a JSON Schema (so the model can understand the expected arguments). Add field descriptions via struct tags:

- **Field name**: use `json:"..."` as the schema property name.
- **Field description (recommended)**: use `jsonschema:"description=..."` to populate `properties.<field>.description`.
- **Note**: the `jsonschema` tag uses comma `,` as the separator, so **the description value must not contain `,`**; otherwise it will be parsed as multiple tag items.
- **Compatibility**: `description:"..."` is also supported for legacy code. If both `jsonschema:"description=..."` and `description:"..."` are present, the `jsonschema` description wins.
- **More flexible schema**: if you need full control over the input schema (e.g. complex JSON Schema constraints), use `function.WithInputSchema(customInputSchema)` to bypass auto-generation.

### Streaming Tool Example

```go
// 1. Define input and output structures.
type weatherInput struct {
    Location string `json:"location" jsonschema:"description=Location to query e.g. city name or coordinates"`
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

### Access Tool Call ID inside Tool implementations

When the model emits a `tool_call`, the framework injects that call's
`tool_call_id` into the tool execution `context.Context` before your tool
starts running.

This means the framework **does support** reading the current tool call ID
directly inside your own Tool implementation.

This is especially useful when you want to:

- create unique state keys for concurrent calls to the same tool
- attach a stable identifier to logs, metrics, or traces
- launch a child Agent inside the tool and tell your UI which
  `tool_call` that child Agent belongs to

Today this mechanism applies to:

- regular function tools in LLMAgent
- streamable tools in LLMAgent
- tool execution in GraphAgent tools nodes
- Tool callbacks and plugins as well

#### The simplest form

Call `tool.ToolCallIDFromContext(ctx)` inside the tool:

```go
const defaultToolCallID = "default"

type searchArgs struct {
    Query string `json:"query"`
}

func searchDocs(
    ctx context.Context,
    args searchArgs,
) (map[string]any, error) {
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
        toolCallID = defaultToolCallID
    }

    log.Printf(
        "tool_call_id=%s query=%s",
        toolCallID,
        args.Query,
    )

    return map[string]any{
        "tool_call_id": toolCallID,
        "query":        args.Query,
    }, nil
}
```

If all you need is per-call logging, metrics, or Invocation State, this is
usually enough.

For a runnable end-to-end example, see `examples/toolcallid`.

#### When the Tool also launches a child Agent

There are two different kinds of identifiers here:

- `tool_call_id`: identifies one model-issued tool call
- `InvocationID` / `ParentInvocationID`: identify parent-child Agent runs

If your goal is "show the child Agent output under the specific tool card in
the UI", keep both layers:

1. Read the current `tool_call_id` with
   `tool.ToolCallIDFromContext(ctx)`
2. Read the parent Invocation with `agent.InvocationFromContext(ctx)`
3. Create the child Invocation with `parentInv.Clone(...)`
4. Put the `tool_call_id` into the child Invocation
   `RunOptions.RuntimeState`
5. In the renderer, use:
   - `InvocationID` / `ParentInvocationID` for the execution tree
   - the propagated `tool_call_id` for the originating tool card

Example (assuming `childAgent` is an existing runnable child Agent):

```go
const runtimeStateParentToolCallID = "display.parent_tool_call_id"
const defaultToolCallID = "default"

type delegateArgs struct {
    Message string `json:"message"`
}

func runChildAgentInsideTool(
    ctx context.Context,
    args delegateArgs,
) (string, error) {
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
        toolCallID = defaultToolCallID
    }

    parentInv, ok := agent.InvocationFromContext(ctx)
    if !ok || parentInv == nil {
        return "", errors.New("missing parent invocation")
    }

    childRunOptions := parentInv.RunOptions
    childRunOptions.RuntimeState = make(
        map[string]any,
        len(parentInv.RunOptions.RuntimeState)+1,
    )
    for key, value := range parentInv.RunOptions.RuntimeState {
        childRunOptions.RuntimeState[key] = value
    }
    childRunOptions.RuntimeState[
        runtimeStateParentToolCallID
    ] = toolCallID

    childInv := parentInv.Clone(
        agent.WithInvocationAgent(childAgent),
        agent.WithInvocationMessage(
            model.NewUserMessage(args.Message),
        ),
        agent.WithInvocationRunOptions(childRunOptions),
    )

    childCtx := agent.NewInvocationContext(ctx, childInv)
    eventCh, err := agent.RunWithPlugins(
        childCtx,
        childInv,
        childAgent,
    )
    if err != nil {
        return "", err
    }

    var final string
    for ev := range eventCh {
        if ev.Response != nil && len(ev.Response.Choices) > 0 {
            msg := ev.Response.Choices[0].Message
            if msg.Content != "" {
                final = msg.Content
            }
        }
        // Child events naturally carry:
        // - ev.InvocationID       == childInv.InvocationID
        // - ev.ParentInvocationID == parentInv.InvocationID
        //
        // Your renderer can build the invocation tree from these two
        // fields, and read runtimeStateParentToolCallID from the child
        // invocation path to attach that subtree back to the original
        // tool-call card.
    }

    return final, nil
}
```

Copy `RuntimeState` before writing to it. `Invocation.Clone(...)`
does not deep-copy the `map`, so mutating a reused map would also
mutate the parent Invocation.

Inside the child Agent, if you need to read the originating tool call ID
again, read it from runtime state:

```go
toolCallID, ok := agent.GetRuntimeStateValueFromContext[string](
    ctx,
    runtimeStateParentToolCallID,
)
```

#### Recommended pattern

- If you only need the identifier for the current tool call, use
  `tool.ToolCallIDFromContext(ctx)`
- If you need to represent "which child Agent belongs to which parent
  execution", rely on `InvocationID` / `ParentInvocationID`
- If the UI must also attach that child subtree back to a specific tool
  card, propagate `tool_call_id` explicitly through `RuntimeState` or
  custom event metadata
- If you are using `AgentTool`, the framework already maintains parent-child
  Invocation linkage via `Invocation.Clone(...)`. In many UIs that is
  already enough. Propagate `tool_call_id` only when you need the extra
  tool-card association

#### One subtle caveat

The framework injects `tool_call_id` before tool execution.
However, if a `BeforeTool` callback replaces the context with a brand-new
bare context, downstream tool code will no longer see that ID.

So if you replace the context inside callbacks, make sure you preserve the
existing context values you still need.

## Built-in Tools

### Tool Call Retry

When a tool call may fail because of a transient issue, you can configure retry for it, for example:

- a temporary network issue;
- a short timeout;
- an intermittent failure from an external service.

This feature is disabled by default. It currently applies only to `CallableTool`, and `StreamableTool` is not retried yet. When enabled, the framework retries only the current tool call. It does not rerun the whole Agent or the whole Graph workflow.

### Basic Configuration

```go
policy := &tool.RetryPolicy{
    MaxAttempts:     3,
    InitialInterval: 200 * time.Millisecond,
    BackoffFactor:   2.0,
    MaxInterval:     2 * time.Second,
    Jitter:          true,
}
```

Common fields:

- `MaxAttempts`: Total attempt count, including the first call.
- `InitialInterval`: Delay before the second attempt.
- `BackoffFactor`: Multiplier for backoff growth.
- `MaxInterval`: Upper bound for the delay.
- `Jitter`: Whether to enable jitter.

### Default Retry Rules

If you do not provide `RetryOn`, the framework uses `tool.DefaultRetryOn(...)`.

The default rule is conservative and retries only common transient errors, such as:

- `io.EOF`
- `io.ErrUnexpectedEOF`
- timeout / temporary errors reported through `net.Error`

It does not retry `context.Canceled`, `context.DeadlineExceeded`, or result-level failures by default.

### Custom Retry Rules

If the default rule is not enough, you can customize the decision with `RetryOn`. A common pattern is to reuse `tool.DefaultRetryOn(...)` first, then add your own conditions:

```go
policy := &tool.RetryPolicy{
    MaxAttempts:     2,
    InitialInterval: 200 * time.Millisecond,
    BackoffFactor:   2.0,
    MaxInterval:     time.Second,
    RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
        retry, err := tool.DefaultRetryOn(ctx, info)
        if err != nil || retry {
            return retry, err
        }
        if info.ResultError {
            return true, nil
        }
        return false, nil
    },
}
```

`tool.RetryInfo` carries the current call information, such as tool name, attempt number, raw error, and result-level failure flag, so you can make your retry decision in one place.

### Enable It in LLMAgent

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{myTool}),
    llmagent.WithToolCallRetryPolicy(policy),
)
```

Runnable example:

- [examples/llmagent_tool_call_retry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/llmagent_tool_call_retry)

### Enable It in Graph

```go
sg.AddToolsNode(
    "tools",
    tools,
    graph.WithToolCallRetryPolicy(policy),
)
```

Runnable example:

- [examples/graph/tool_call_retry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/graph/tool_call_retry)

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

### Claude Code ToolSet

`tool/claudecode` provides a code-oriented ToolSet that exposes a Claude Code-style tool surface inside the framework. It covers file editing, repository search, command execution, and web retrieval, and can be attached directly to `LLMAgent` or other runtimes. If your goal is to invoke the local Claude Code CLI and consume its execution trace and tool events, see the [Claude Code Agent guide](claudecode.md).

By default, `claudecode` exposes a core set of workflow tools: `Bash`, `TaskStop`, `TaskOutput`, `Read`, `Glob`, `Grep`, `WebFetch`, and `WebSearch`. When read-only mode is disabled, it also exposes `Write`, `Edit`, and `NotebookEdit`.

The following table lists the tools currently exposed by `claudecode`:

| Tool | Description |
| --- | --- |
| `Bash` | Executes local shell commands. |
| `TaskStop` | Stops a background task started by `Bash`. |
| `TaskOutput` | Reads the current or final output of a background task. |
| `Read` | Reads file contents. |
| `Glob` | Finds files by path pattern. |
| `Grep` | Searches repository content. |
| `WebFetch` | Fetches the content of a specific URL. |
| `WebSearch` | Performs an open web search. |
| `Write` | Creates a file or overwrites a file with complete contents. Only exposed when read-only mode is disabled. |
| `Edit` | Performs targeted replacement in an existing text file. Only exposed when read-only mode is disabled. |
| `NotebookEdit` | Edits `.ipynb` files at the cell level. Only exposed when read-only mode is disabled. |

#### Basic Usage

```go
import (
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/claudecode"
)

toolSet, err := claudecode.NewToolSet(
	claudecode.WithBaseDir("."),
)
if err != nil {
	log.Fatal(err)
}
defer toolSet.Close()

agent := llmagent.New(
	"claude-style-agent",
	llmagent.WithToolSets([]tool.ToolSet{toolSet}),
)
```

`llmagent.WithToolSets(...)` attaches these tools as a ToolSet. Calling `Tools()` returns the flattened list of individual tools.

#### Common Options

The main `tool/claudecode` options focus on working directory, read-only mode, and web behavior:

| Option | Description |
| --- | --- |
| `WithName(name)` | Overrides the ToolSet name. The default name is `claudecode`. |
| `WithBaseDir(dir)` | Sets the base directory used by file, search, and command execution tools. |
| `WithReadOnly(readOnly)` | Removes `Write`, `Edit`, and `NotebookEdit` when enabled. |
| `WithMaxFileSize(size)` | Limits the maximum readable file size. |
| `WithWebFetchOptions(opts)` | Configures domain policy, timeout, and content handling for `WebFetch`. |
| `WithWebSearchOptions(opts)` | Configures backend, paging, and request options for `WebSearch`. |

`WithBaseDir` defines the working scope for `Read`, `Write`, `Edit`, `Glob`, and `Grep`, and also determines the default working directory for `Bash`. When read-only mode is enabled, the toolset keeps only read, search, command, and web capabilities. When read-only mode is disabled, it also exposes `Write`, `Edit`, and `NotebookEdit`.

## MCP Tools

MCP (Model Context Protocol) is an open protocol that standardizes how applications provide context to LLMs. MCP tools are based on JSON-RPC 2.0 and provide standardized integration with external services for Agents.

**MCP ToolSet Features:**

- 🔗 Unified interface: All MCP tools are created via `mcp.NewMCPToolSet()`.
- ✅ Explicit initialization: `(*mcp.ToolSet).Init(ctx)` lets you fail fast on MCP connection / tool loading errors during startup.
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
    mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("echo", "add")), // Optional: tool filter.
)

// (Optional but recommended) Explicitly initialize MCP: connect + initialize + list tools.
if err := mcpToolSet.Init(ctx); err != nil {
    log.Fatalf("failed to initialize MCP toolset: %v", err)
}

// Integrate into an Agent.
agent := llmagent.New("mcp-assistant",
    llmagent.WithModel(model),
    llmagent.WithToolSets([]tool.ToolSet{mcpToolSet}))
```

### ToolSet Lifecycle and Ownership

The `ToolSet` interface explicitly includes `Close()`. That means the
party that creates a ToolSet is also responsible for releasing the
connections, sessions, caches, and other resources it owns.

Important ownership boundaries:

- `llmagent.WithToolSets(...)` only attaches a `ToolSet` to an Agent for
  use. It does **not** transfer ownership.
- `LLMAgent.AddToolSet(...)`, `LLMAgent.RemoveToolSet(...)`, and
  `LLMAgent.SetToolSets(...)` only change which tools the Agent exposes.
  They do **not** automatically call `Close()` on replaced or removed
  ToolSets.
- `runner.NewRunner(...)` and `runner.NewRunnerWithAgentFactory(...)`
  likewise do not automatically reclaim a ToolSet just because an Agent
  uses it.

Recommended patterns:

- **Long-lived ToolSet**: create it at startup, optionally call
  `Init(ctx)`, reuse it across many runs, and close it during
  application shutdown.
- **Per-request ToolSet**: create it only for the current run, then
  clean it up explicitly when that run finishes.

If your goal is only to let a ToolSet fetch the **latest** tool list on
each run, prefer `llmagent.WithRefreshToolSetsOnRun(true)`. That refreshes
`ToolSet.Tools(ctx)` per run, but it does **not** recreate or close the
ToolSet instance itself.

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

### Session Reconnection Support

MCP ToolSet supports automatic session reconnection to recover from server restarts or session expiration.

```go
// SSE/Streamable HTTP transports support session reconnection
sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
    },
    mcp.WithSessionReconnect(3), // Enable session reconnection with max 3 attempts
)
```

**Reconnection Features:**

- 🔄 **Auto Reconnect**: Automatically recreates session when connection loss or expiration is detected
- 🎯 **Independent Retries**: Each tool call gets independent reconnection attempts
- 🛡️ **Conservative Strategy**: Only triggers reconnection for clear connection/session errors to avoid infinite loops

### Dynamic MCP Tool Discovery (LLMAgent Option)

For MCP ToolSets, the list of tools on the server side can change over
time (for example, when a new MCP tool is registered). To let an
LLMAgent automatically see the **latest** tools from a ToolSet on each
run, use `llmagent.WithRefreshToolSetsOnRun(true)` together with
`WithToolSets`.

#### LLMAgent configuration example

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// 1. Create an MCP ToolSet (can be STDIO, SSE, or Streamable HTTP).
mcpToolSet := mcp.NewMCPToolSet(connectionConfig)

// 2. Create an LLMAgent and enable dynamic ToolSets refresh.
agent := llmagent.New(
    "mcp-assistant",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
    llmagent.WithToolSets([]tool.ToolSet{mcpToolSet}),
    llmagent.WithRefreshToolSetsOnRun(true),
)
```

When `WithRefreshToolSetsOnRun(true)` is enabled:

- Each time the LLMAgent builds its tool list for a run, it calls
  `ToolSet.Tools(ctx)` again, using the current run context.
- If the MCP server adds or removes tools, the **next run** of this
  LLMAgent will use the updated tool list automatically.
- If you query tools outside a run (for example, by calling
  `agent.Tools()` directly), the LLMAgent uses `context.Background()`.

This option focuses on **dynamic discovery** of tools. If you also need
ToolSets to honor a specific context for initialization or tool
discovery without refreshing the tool list on every run, keep using the
pattern shown in the `examples/mcptool/http_headers` example, where you
manually call `toolSet.Tools(ctx)` and pass the tools via `WithTools`.

Common pitfalls:

- `WithRefreshToolSetsOnRun(true)` refreshes the **tool list**, not the
  ToolSet instance itself. It does not automatically create, replace, or
  close ToolSets for you.
- `tools/call` uses the current run context, but if you call
  `agent.Tools()` outside a run, the ToolSet will see
  `context.Background()`.
- If `initialize/tools/list` must strictly use a custom context
  (for example, per-request auth headers or tracing values), the safer
  pattern is usually to call `toolSet.Tools(ctx)` yourself and inject the
  resulting tools via `WithTools(...)`.

### MCP Broker (On-Demand MCP Discovery)

In addition to expanding remote MCP tools into first-class Tools, the
framework also provides another integration style:
`tool/mcpbroker`.

The core idea of `mcpbroker` is:

- do not expose every remote MCP tool up front
- expose only a very small broker surface first
- let the model discover and call remote MCP tools only when needed

This is a better fit when the remote MCP surface is large, but a single
request usually needs only a small subset of that surface.

#### When to Use MCP Broker

Use `mcpbroker` when:

- an MCP server exposes many tools and you do not want to send the full
  tool surface to the model on every turn
- some tools are long-tail or backup capabilities rather than hot-path tools
- a Skill, System Prompt, or User Prompt reveals an MCP endpoint that
  should be connected dynamically
- you want a smaller and more stable initial tool surface

Keep using `mcp.NewMCPToolSet()` when:

- the capability is high-frequency, stable, and already known
- you want remote MCP tools to become first-class Tools directly
- you care more about shorter execution paths and stronger schema-level
  constraints

The two patterns can be mixed:

- use `MCP ToolSet` for hot-path capabilities
- use `mcpbroker` for long-tail or dynamically discovered capabilities

#### How It Differs from MCP ToolSet

The main difference is **when** remote MCP tools become visible:

- `MCP ToolSet`
  - performs `initialize + tools/list`
  - expands remote MCP tools into model-visible Tools
- `mcpbroker`
  - initially exposes only 4 broker tools
  - the model discovers servers, then tools, then inspects selected schemas, then calls a concrete tool

You can think of them as:

- `MCP ToolSet`: directly mount remote tools
- `mcpbroker`: discover remote tools on demand

Typical trade-offs:

- `MCP ToolSet`: faster and more strongly constrained, but larger tool surface
- `mcpbroker`: lighter on context and better for long-tail / dynamic capabilities, but usually slower because discovery adds extra steps

#### Basic Integration

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcpbroker"
)

broker := mcpbroker.New(
    mcpbroker.WithServers(map[string]mcp.ConnectionConfig{
        "local_stdio_code": {
            Transport:   "stdio",
            Command:     "go",
            Args:        []string{"run", "./stdio_server/main.go"},
            Timeout:     10 * time.Second,
            Description: "Project management, documentation, and calendar tools.",
        },
    }),
    mcpbroker.WithAllowAdHocHTTP(true),
)

agent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(broker.Tools()),
)
```

#### Server Description

The `Description` field on `ConnectionConfig` provides a capability
summary for the MCP server, helping the model decide which server to
explore at the `mcp_list_servers` stage. The output includes the
description:

```json
{
  "servers": [
    {
      "name": "local_stdio_code",
      "transport": "stdio",
      "description": "Project management, documentation, and calendar tools."
    }
  ]
}
```

This is analogous to the `description` field on an OpenAI tool namespace:
the model can read it at the `mcp_list_servers` step and decide which
server to explore next, without needing to `mcp_list_tools` every server
one by one. The more servers you have, or the less self-explanatory the
server names are, the more valuable this description becomes.

The field is optional. When omitted, the output simply does not include
a `description` property, and existing behavior is unchanged.

Today `mcpbroker` is a **tool-layer integration**. Unlike `Skill`, it
does not automatically inject routing guidance into the system prompt.
If you want the model to behave more predictably, it is still useful to
add a small amount of business-level instruction such as:

- when to list named servers first
- when to use `mcp_list_tools` first
- when to expand selected tools with `mcp_inspect_tools`
- when to prefer broker over direct tools

#### The 4 Model-Visible Broker Tools

The model only sees these 4 tools:

- `mcp_list_servers`
- `mcp_list_tools`
- `mcp_inspect_tools`
- `mcp_call`

The recommended flow is usually:

1. `mcp_list_servers()` to inspect named servers already known to the broker
2. `mcp_list_tools(selector)` to inspect a server or ad-hoc MCP endpoint with lightweight summaries
3. `mcp_inspect_tools(selector, tools[])` to expand schema for only the selected tools
4. `mcp_call(selector, arguments)` to call one concrete MCP tool

So instead of seeing the entire remote tool surface immediately, the
model explores it progressively through the broker.

#### Selector Mental Model

`mcpbroker` intentionally avoids a mixed input model like
`server_name + tool_name + url`. Instead it uses a unified `selector`:

- In `mcp_list_tools`:
  - named server: `local_stdio_code`
  - ad-hoc URL: `https://example.com/mcp`
- In `mcp_call`:
  - named tool: `local_stdio_code.add`
  - ad-hoc URL tool: `https://example.com/mcp.add`

If an ad-hoc HTTP endpoint would make dot-based selectors ambiguous, `mcpbroker`
also supports:

- `https://example.com/mcp#tool=add`

Remote MCP tool parameters always go into:

- `mcp_call(..., arguments={...})`

and not into top-level wrapper fields.

#### Progressive Discovery Flow

- start with `mcp_list_tools` for lightweight summaries
- only use `mcp_inspect_tools` when preparing to call a specific tool

Compared with sending every full schema up front, this is usually more
friendly to tight context budgets.

#### Dynamic URL and Skill Scenarios

`mcpbroker` supports ad-hoc HTTP MCP targets:

```go
broker := mcpbroker.New(
    mcpbroker.WithAllowAdHocHTTP(true),
)
```

`WithAllowAdHocHTTP(true)` makes `selector` model-controlled input for
HTTP(S) MCP targets. In production, validate or allowlist the URL, domain, and
path before treating ad-hoc HTTP as a trusted integration path.

In practice, dynamic connection still requires some **information
source** to tell the model:

- that this MCP endpoint exists
- what it roughly does
- which URL should be used

That source can be:

- a System Prompt
- a User Prompt
- a Skill
- a knowledge source

In other words, `mcpbroker` solves “how to connect / inspect / call”.
It does not solve “why would the model think of connecting to this MCP
in the first place”.

This makes `mcpbroker` a natural companion to Skills. Some Skills only
need a dedicated MCP capability while that Skill is relevant, so those
MCP tools do not need to stay exposed as global tools for the whole
conversation. Other Skills may reveal an incremental MCP endpoint that
is only known after the Skill is loaded. In both cases, the Skill can
act as the information source, while `mcpbroker` handles the dynamic
connection and progressive tool/schema disclosure.

See:

- `examples/mcpbroker/basic`

That example includes:

- a named local MCP server
- a Skill that reveals a remote streamable HTTP MCP endpoint
- a model flow like `skill_load -> mcp_list_tools -> mcp_inspect_tools -> mcp_call`

#### Auth Hooks (Per-Run Header Injection)

For HTTP MCP targets, `mcpbroker` also provides runtime auth hooks:

- `WithHTTPHeaderInjector(...)`
- `WithErrorInterceptor(...)`

These hooks are useful when:

- you do not want the model to provide `Authorization` itself
- you need to inject a user-specific token on every request
- you want business code to wrap 401/403 responses into clearer errors

Example:

```go
broker := mcpbroker.New(
    mcpbroker.WithAllowAdHocHTTP(true),
    mcpbroker.WithHTTPHeaderInjector(func(ctx context.Context, req *mcpbroker.HeaderInjectRequest) (map[string]string, error) {
        token, _ := resolveUserTokenFromContext(ctx, req.BaseURL)
        if token == "" {
            return nil, nil
        }
        return map[string]string{
            "Authorization": "Bearer " + token,
        }, nil
    }),
    mcpbroker.WithErrorInterceptor(func(ctx context.Context, req *mcpbroker.BrokerErrorRequest) (*mcpbroker.BrokerErrorDecision, error) {
        if isUnauthorized(req.Err) {
            return &mcpbroker.BrokerErrorDecision{
                Handled:   true,
                WrapError: fmt.Errorf("the current user must authorize this provider in the host application before retrying"),
            }, nil
        }
        return nil, nil
    }),
)
```

The key design point is:

- the model chooses the `selector`
- business code resolves headers from `ctx`
- `mcpbroker` does not manage a full OAuth session state machine

When `WithAllowAdHocHTTP(true)` is enabled, URL selectors may come from
model-visible context. In production, validate `req.IsAdHoc` and `req.BaseURL`
inside your `HTTPHeaderInjector` before returning sensitive headers.

See:

- `examples/mcpbroker/authhooks`

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
    // Optional, defaults to false. When set to true, the outer model summary will be skipped, 
    // and the current round will end directly after tool.response.
    agenttool.WithSkipSummarization(false),
    // forward child Agent streaming events to parent flow.
    agenttool.WithStreamInner(true),
    // hide child assistant prose while still forwarding inner tool progress.
    agenttool.WithInnerTextMode(agenttool.InnerTextModeExclude),
    // return only the child Agent's final assistant message as the tool result.
    agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
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

When `WithStreamInner(true)` is enabled, AgentTool forwards child Agent events
to the parent flow as they happen:

- Forwarded items are actual `event.Event` instances, carrying incremental text in `choice.Delta.Content`
- To avoid duplication, the child Agent’s final full message is not forwarded again; it is aggregated into the final `tool.response` content for the next LLM turn (to satisfy providers requiring tool messages)
- UI guidance: show forwarded child deltas; avoid printing the aggregated final `tool.response` content unless debugging
- `WithInnerTextMode(agenttool.InnerTextModeExclude)` lets you keep inner tool
  progress visible while suppressing forwarded child assistant text. This is
  useful when an outer coordinator will produce the final user-facing answer.

Example: Only show tool fragments when needed to avoid duplicates

```go
if ev.Response != nil && ev.Object == model.ObjectTypeToolResponse {
    // Tool response contains aggregated content; skip printing by default to avoid duplicates
}

// Child Agent forwarded deltas (author != parent)
if ev.Author != parentName && ev.Response != nil &&
    len(ev.Response.Choices) > 0 {
    if delta := ev.Response.Choices[0].Delta.Content; delta != "" {
        fmt.Print(delta)
    }
}
```

### Tool Result Response Modes

AgentTool always executes the child Agent as an event stream. The response mode
only controls the value returned to the parent Agent as the tool result. It does
not change child session storage, event filter keys, or inner streaming.

By default, AgentTool preserves its legacy behavior: every non-empty assistant
message emitted by the child Agent is appended into one tool-result string.
This is useful for compatibility, but it can be surprising for long-running
child Agents that emit progress, drafts, or intermediate assistant messages.
Those intermediate assistant messages can become part of the parent Agent's
`tool.response`.

Use `WithResponseMode(agenttool.ResponseModeFinalOnly)` when the child Agent is
used as an isolated worker and the parent Agent should only see the child's
final answer:

```go
childTool := agenttool.NewTool(
    childAgent,
    agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
)
```

In final-only mode, AgentTool:

- ignores partial assistant chunks
- ignores non-assistant messages, empty assistant messages, and tool messages
- returns the last complete child assistant message
- returns an empty string when the child Agent completes without a complete
  assistant message
- still returns child Agent errors as `agent error: ...`

This is different from `WithSkipSummarization(true)`. `WithSkipSummarization`
controls whether the parent flow performs an extra outer summarization call
after the tool response. `WithResponseMode` controls what the tool response
contains in the first place.

Example with both settings:

```go
childTool := agenttool.NewTool(
    childAgent,
    // Keep the child Agent's chain-of-work out of the tool result.
    agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
    // End the parent turn after tool.response instead of asking the parent
    // model to summarize the result again.
    agenttool.WithSkipSummarization(true),
)
```

### Options

- WithSkipSummarization(bool):

  - false (default): Allow an additional summarization/answer call after the tool result
  - true: Skip the outer summarization LLM call once the tool returns

- WithStreamInner(bool):

  - true: Forward child Agent events to the parent flow (recommended: enable `GenerationConfig{Stream: true}` for both parent and child Agents)
  - false: Treat as a callable-only tool, without inner event forwarding

- WithInnerTextMode(InnerTextMode):

  - `InnerTextModeInclude`: (effective default) forward child assistant text
    when inner streaming is enabled
  - `InnerTextModeExclude`: suppress forwarded child assistant text while
    keeping inner tool calls, tool completions, and the aggregated final tool
    response

- WithResponseMode(ResponseMode):

  - `ResponseModeDefault` (default): keep legacy concatenation of child
    assistant messages into the tool result
  - `ResponseModeFinalOnly`: return only the last complete child assistant
    message as the tool result

- WithHistoryScope(HistoryScope):
  - `HistoryScopeIsolated` (default): Keep the child Agent fully isolated; it only sees the current tool arguments (no inherited history).
  - `HistoryScopeParentBranch`: Inherit parent conversation history by using a hierarchical filter key `parent/child-uuid`. This allows the content processor to include parent events via prefix matching while keeping child events isolated under a sub-branch. Typical use cases: “edit/optimize/continue previous output”.

Example:

```go
child := agenttool.NewTool(
    childAgent,
    agenttool.WithSkipSummarization(false),
    agenttool.WithStreamInner(true),
    agenttool.WithInnerTextMode(agenttool.InnerTextModeExclude),
    agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
    agenttool.WithHistoryScope(agenttool.HistoryScopeParentBranch),
)
```

### Notes

- Completion signaling: Tool response events are marked `RequiresCompletion=true`; Runner sends completion automatically
- De-duplication: When inner deltas are forwarded, avoid printing the aggregated final `tool.response` text again by default
- Progress-only UX: combine `WithStreamInner(true)` with
  `WithInnerTextMode(agenttool.InnerTextModeExclude)` when users should see
  inner progress without seeing duplicated child prose
- Model compatibility: Some providers require a tool message after tool_calls; AgentTool automatically supplies the aggregated content
- Child context isolation and tool-result shaping are separate concerns.
  `WithHistoryScope(agenttool.HistoryScopeIsolated)` controls what the child
  can read. `WithResponseMode(agenttool.ResponseModeFinalOnly)` controls what
  the parent receives as the tool result.
- `WithSkipSummarization(true)` only skips the extra outer summarization LLM call. It does not make `tool.response` a final assistant response; keep consuming until `runner.completion` if you need the real terminal signal

## Tool Integration and Usage

### Create an Agent and Integrate Tools

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
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
if err := stdioToolSet.Init(ctx); err != nil {
    return fmt.Errorf("failed to initialize stdio MCP toolset: %w", err)
}

sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
    },
)
if err := sseToolSet.Init(ctx); err != nil {
    return fmt.Errorf("failed to initialize sse MCP toolset: %w", err)
}

streamableToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "streamable_http",
        ServerURL: "http://localhost:3000/mcp",
        Timeout:   10 * time.Second,
    },
)
if err := streamableToolSet.Init(ctx); err != nil {
    return fmt.Errorf("failed to initialize streamable MCP toolset: %w", err)
}

// Create an Agent and integrate all tools.
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("You are a helpful AI assistant that can use various tools to help users."),
    // Add single tools (Tool interface).
    llmagent.WithTools([]tool.Tool{
        calculatorTool, timeTool, searchTool,
    }),
    // Add ToolSets (ToolSet interface).
    llmagent.WithToolSets([]tool.ToolSet{
        stdioToolSet, sseToolSet, streamableToolSet,
    }),
)
```

### MCP Tool Filters

MCP ToolSets support filtering tools at creation time. It's recommended to use the unified `tool.FilterFunc` interface:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// ✅ Recommended: Use the unified filter interface
includeFilter := tool.NewIncludeToolNamesFilter("get_weather", "get_news", "calculator")
excludeFilter := tool.NewExcludeToolNamesFilter("deprecated_tool", "slow_tool")

// Apply filter
toolSet := mcp.NewMCPToolSet(
    connectionConfig,
    mcp.WithToolFilterFunc(includeFilter),
)

// Optional: initialize once at startup to catch MCP connection / tool loading errors early.
if err := toolSet.Init(ctx); err != nil {
    return fmt.Errorf("failed to initialize MCP toolset: %w", err)
}
```

### Per-Run Tool Filtering

- Option one: Per-run tool filtering enables dynamic control of tool availability for each `runner.Run` invocation without modifying Agent configuration. This is a "soft constraint" mechanism for optimizing token consumption and implementing role-based tool access control.
apply to all agents
- Option two: Configure the runtime filtering function through 'llmagent. WhatToolFilter' to only apply to the current agent
**Key Features:**

- 🎯 **Per-Run Control**: Independent configuration per invocation, no Agent modification needed
- 💰 **Cost Optimization**: Reduce tool descriptions sent to LLM, lowering token costs
- 🛡️ **Smart Protection**: Framework tools (`transfer_to_agent`, `knowledge_search`, optional `await_user_reply`) automatically preserved, never filtered
- 🔧 **Flexible Customization**: Support for built-in filters and custom FilterFunc

#### Tool Search (Automatic Tool Selection)

In addition to rule-based filtering (Tool Filter), the framework provides **Tool Search**: before each main model call, it runs a lightweight “tool selection” step to shrink the available tool set to **TopK** (for example, 3 tools), then sends only those tools to the main model. This typically reduces token usage (especially **PromptTokens**) when the full tool list is large.

Trade-offs to keep in mind:

- **Latency**: Tool Search adds extra work (another LLM call and/or embedding + vector search), so end-to-end latency may increase.
- **Prompt caching**: the tool list can change every turn, which may reduce prompt caching hit rate on some providers.

How it differs from Tool Filter:

- **Tool Filter**: you (or your business logic) decide which tools are allowed/blocked (access control / cost control).
- **Tool Search**: the framework picks tools automatically based on the current user query (automation / cost optimization).

They can be combined: use Tool Filter for permissions/allow-lists first, then use Tool Search to select TopK from the remaining tools.

**Two strategies:**

- **LLM Search**: put the candidate tool list (name + description) into the prompt and ask an LLM to output the selected tool names.
  - Pros: no vector store needed; simple to adopt.
  - Cons: the prompt cost grows roughly linearly with the number/length of tool descriptions, and repeats every turn.
- **Knowledge Search**: rewrite the query with an LLM, then use embeddings + vector search to find relevant tools.
  - Pros: you don’t need to send the full tool list to the selection LLM every turn; and **tool embeddings are cached within the same `ToolKnowledge` instance** (so tools are not re-embedded repeatedly).
  - Note: the query is still embedded each turn (a fixed per-turn cost).

##### Basic Usage (LLM Search)

Tool Search can be used either as a Runner plugin or as a per-agent callback.

**Option A: Runner Plugin**

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

ts, err := toolsearch.New(modelInstance,
    toolsearch.WithMaxTools(3),
    toolsearch.WithFailOpen(), // optional: fallback to full tool set on failure
)
if err != nil { /* handle */ }

ag := llmagent.New("assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(allTools), // register full tools; Tool Search picks TopK
)

r := runner.NewRunner("app", ag,
    runner.WithPlugins(ts),
)
```

**Option B: Per-Agent BeforeModel Callback**

Register Tool Search as a `BeforeModel` callback. It will mutate `req.Tools`
before the main model call:

```go
	import (
	    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	    "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	    "trpc.group/trpc-go/trpc-agent-go/model"
	)

modelCallbacks := model.NewCallbacks()
tc, err := toolsearch.New(modelInstance,
    toolsearch.WithMaxTools(3),
    toolsearch.WithFailOpen(), // optional: fallback to full tool set on failure
)
if err != nil { /* handle */ }
modelCallbacks.RegisterBeforeModel(tc.Callback())

agent := llmagent.New("assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(allTools), // register full tools; Tool Search will pick TopK per run
    llmagent.WithModelCallbacks(modelCallbacks),
)
```

##### Basic Usage (Knowledge Search)

Create a `ToolKnowledge` (embedder + vector store) and enable it via `toolsearch.WithToolKnowledge(...)`:

```go
	import (
	    "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	)

toolKnowledge, err := toolsearch.NewToolKnowledge(
    openaiembedder.New(openaiembedder.WithModel(openaiembedder.ModelTextEmbedding3Small)),
    toolsearch.WithVectorStore(vectorinmemory.New()),
)
if err != nil { /* handle */ }

tc, err := toolsearch.New(modelInstance,
    toolsearch.WithMaxTools(3),
    toolsearch.WithToolKnowledge(toolKnowledge),
    toolsearch.WithFailOpen(),
)
if err != nil { /* handle */ }
modelCallbacks.RegisterBeforeModel(tc.Callback())
```

##### Token Usage (Optional)

Tool Search stores usage in `context.Context`, which you can use for metrics/cost analysis:

```go
	import "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"

if usage, ok := toolsearch.ToolSearchUsageFromContext(ctx); ok && usage != nil {
    // usage.PromptTokens / usage.CompletionTokens / usage.TotalTokens
}
```

#### Basic Usage

**1. Exclude Specific Tools (Exclude Filter)**

Use blacklist approach to exclude unwanted tools:

```go
import "trpc.group/trpc-go/trpc-agent-go/tool"

// Option 1:
// Exclude text_tool and dangerous_tool, all other tools available
filter := tool.NewExcludeToolNamesFilter("text_tool", "dangerous_tool")
eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)

// Option 2:
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("You are a helpful AI assistant that can use various tools to help users."),
    llmagent.WithTools([]tool.Tool{
        calculatorTool, timeTool, searchTool,
    }),
    llmagent.WithToolSets([]tool.ToolSet{
        stdioToolSet, sseToolSet, streamableToolSet,
    }),
    llmagent.WithToolFilter(filter),
)
```

**2. Include Only Specific Tools (Include Filter)**

Use whitelist approach to allow only specified tools:

```go
// Only allow calculator and time tool
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)
```

**3. Custom Filtering Logic (Custom FilterFunc)**

Implement custom filter function for complex filtering logic:

```go
// Option 1:
// Custom filter: only allow tools with names starting with "safe_"
filter := func(ctx context.Context, t tool.Tool) bool {
    declaration := t.Declaration()
    if declaration == nil {
        return false
    }
    return strings.HasPrefix(declaration.Name, "safe_")
}

eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)

// Option 2:
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("You are a helpful AI assistant that can use various tools to help users."),
    llmagent.WithTools([]tool.Tool{
        calculatorTool, timeTool, searchTool,
    }),
    llmagent.WithToolSets([]tool.ToolSet{
        stdioToolSet, sseToolSet, streamableToolSet,
    }),
    llmagent.WithToolFilter(filter),
```

**4. Per-Agent Filtering**

Use `agent.InvocationFromContext` to implement different tool sets for different Agents:

```go
// Define allowed tools for each Agent
agentAllowedTools := map[string]map[string]bool{
    "math-agent": {
        "calculator": true,
    },
    "time-agent": {
        "time_tool": true,
    },
}

// Custom filter function: filter based on current Agent name
filter := func(ctx context.Context, t tool.Tool) bool {
    declaration := t.Declaration()
    if declaration == nil {
        return false
    }
    toolName := declaration.Name

    // Get current Agent information from context
    inv, ok := agent.InvocationFromContext(ctx)
    if !ok || inv == nil {
        return true // fallback: allow all tools
    }

    agentName := inv.AgentName

    // Check if this tool is in the current Agent's allowed list
    allowedTools, exists := agentAllowedTools[agentName]
    if !exists {
        return true // fallback: allow all tools
    }

    return allowedTools[toolName]
}

eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)
```

**Complete Example:** See `examples/toolfilter/` directory

#### Smart Filtering Mechanism

The framework automatically distinguishes **user tools** from **framework tools**, filtering only user tools:

| Tool Category       | Includes                                                                                                                      | Filtered?                         |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------- | --------------------------------- |
| **User Tools**      | Tools registered via `WithTools`<br>Tools registered via `WithToolSets`                                                       | ✅ Subject to filtering           |
| **Framework Tools** | `transfer_to_agent` (multi-Agent coordination)<br>`knowledge_search` (knowledge base retrieval)<br>`agentic_knowledge_search`<br>`await_user_reply` (one-shot follow-up routing, when enabled) | ❌ Never filtered, auto-preserved |

**Example:**

```go
// Agent registers multiple tools
agent := llmagent.New("assistant",
    llmagent.WithTools([]tool.Tool{
        calculatorTool,  // User tool
        textTool,        // User tool
    }),
    llmagent.WithSubAgents([]agent.Agent{subAgent1, subAgent2}), // Auto-adds transfer_to_agent
    llmagent.WithKnowledge(kb),                                   // Auto-adds knowledge_search
    llmagent.WithAwaitUserReplyTool(true),                        // Auto-adds await_user_reply
)

// Runtime filtering: only allow calculator
filter := tool.NewIncludeToolNamesFilter("calculator")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)

// Tools actually sent to LLM:
// ✅ calculator        - User tool, in allowed list
// ❌ textTool          - User tool, filtered out
// ✅ transfer_to_agent - Framework tool, auto-preserved
// ✅ knowledge_search  - Framework tool, auto-preserved
// ✅ await_user_reply  - Framework tool, auto-preserved
```

#### `await_user_reply` for Follow-Up Turns

`await_user_reply` is an optional framework tool. Enable it with
`llmagent.WithAwaitUserReplyTool(true)` when an Agent may ask the user for
missing information and you want the next user message to resume at that same
Agent.

Use it together with `runner.WithAwaitUserReplyRouting(true)`:

```go
profileAgent := llmagent.New("profile-agent",
    llmagent.WithAwaitUserReplyTool(true),
    llmagent.WithInstruction(`
If you must ask the user for a missing field, call await_user_reply
immediately before your question.
`),
)

r := runner.NewRunner(
    "crm-app",
    profileAgent,
    runner.WithAwaitUserReplyRouting(true),
)
```

The route is one-shot: Runner consumes it on the next user turn and then clears
it automatically.

#### Important Notes

⚠️ **Security Notice:** Per-run tool filtering is a "soft constraint" primarily for optimization and user experience. Tools must still implement their own authorization logic:

### Manual Tool Execution (Interrupt Tool Calls)

By default, when the model returns `tool_calls`, the framework executes those
tools automatically, then sends the tool results back to the model.

In some systems, you may want the caller (for example, a client, an upstream
service, or an external tool runtime such as Model Context Protocol (MCP)) to
execute tools instead. You can interrupt tool execution with
`agent.WithToolExecutionFilter(...)`.

**Key idea:**

- `agent.WithToolFilter(...)` controls **tool visibility** (what the model can
  see and call).
- `agent.WithToolExecutionFilter(...)` controls **tool execution** (what the
  framework will auto-run after the model requests it).

#### Basic Flow

1. Run the agent with `WithToolExecutionFilter` so the framework does **not**
   execute selected tools.
2. Read `tool_calls` from the model response.
3. Execute the tool externally.
4. Send a `role=tool` message back so the model can continue.

```go
execFilter := tool.NewExcludeToolNamesFilter("external_search")

// Step 1: model returns tool_calls, but the tool is NOT executed.
ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("search ..."),
    agent.WithToolExecutionFilter(execFilter),
)

// Step 2: extract tool_call_id + arguments from events (omitted).
toolCallID := "call_123"
toolResultJSON := `{"status":"ok","data":"..."}`

// Step 3/4: send tool result as role=tool, then model continues.
toolMsg := model.NewToolMessage(toolCallID, "external_search", toolResultJSON)
ch, err = r.Run(ctx, userID, sessionID, toolMsg,
    agent.WithToolExecutionFilter(execFilter),
)
```

**Complete example:** `examples/toolinterrupt/`

```go
func sensitiveOperation(ctx context.Context, req Request) (Result, error) {
    // ✅ Required: internal tool authorization
    if !hasPermission(ctx, req.UserID, "sensitive_operation") {
        return nil, fmt.Errorf("permission denied")
    }

    // Execute operation
    return performOperation(req)
}
```

**Reason:** LLMs may know about tool existence and usage from context or memory and attempt to call them. Tool filtering reduces this possibility but cannot completely prevent it.

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

Graph workflows can also enable parallelism for a Tools node:

```go
stateGraph.AddToolsNode("tools", tools, graph.WithEnableParallelTools(true))
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

### Dynamic ToolSet Management (Runtime)

`WithToolSets` is a **static** configuration: it wires ToolSets when constructing the Agent. In many real‑world scenarios you also need to **add, remove, or replace ToolSets at runtime** without recreating the Agent.

LLMAgent exposes three methods for this:

- `AddToolSet(toolSet tool.ToolSet)` — add or replace a ToolSet by `ToolSet.Name()`.
- `RemoveToolSet(name string) bool` — remove all ToolSets whose `Name()` matches `name`.
- `SetToolSets(toolSets []tool.ToolSet)` — replace all ToolSets with the provided slice.

These methods are concurrency‑safe and automatically recompute:

- Aggregated tools (direct tools + ToolSets + knowledge tools + skill tools)
- User tool tracking (used by the smart filtering logic above)

One important lifecycle detail:

- `AddToolSet` does **not** automatically close a replaced ToolSet.
- `RemoveToolSet` does **not** automatically close a removed ToolSet.
- `SetToolSets` does **not** automatically close ToolSets from the
  previous slice.

If you created those ToolSet instances, you still need to close them
explicitly at the right time.

**Typical usage pattern:**

```go
// 1. Create Agent with base tools only.
agent := llmagent.New("dynamic-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
)

// 2. Later, attach an MCP ToolSet at runtime.
mcpToolSet := mcp.NewMCPToolSet(connectionConfig)
if err := mcpToolSet.Init(ctx); err != nil {
    return fmt.Errorf("failed to init MCP ToolSet: %w", err)
}
agent.AddToolSet(mcpToolSet)

// 3. Replace all ToolSets from configuration (declarative control plane).
toolSetsFromConfig := []tool.ToolSet{mcpToolSet, fileToolSet}
agent.SetToolSets(toolSetsFromConfig)

// 4. Remove a ToolSet by name (e.g., feature rollback).
removed := agent.RemoveToolSet(mcpToolSet.Name())
if !removed {
    log.Printf("ToolSet %q not found", mcpToolSet.Name())
}
```

Runtime ToolSet updates integrate seamlessly with the **tool filtering** logic described earlier:

- Tools coming from `WithTools` or any ToolSet (including dynamically added ones) are treated as **user tools** and are subject to `WithToolFilter` and per‑run filters.
- Framework tools such as `transfer_to_agent`, `knowledge_search`,
  `agentic_knowledge_search`, and optional `await_user_reply` remain
  **never filtered** and are always available.

#### Tool Call Arguments Auto Repair

Some models may emit non-strict JSON arguments for `tool_calls` (for example, unquoted object keys or trailing commas), which can break tool execution or external parsing.

Tool call arguments auto repair is useful when the caller needs to parse `toolCall.Function.Arguments` outside the framework, or when tools require strictly valid JSON input.

When `agent.WithToolCallArgumentsJSONRepairEnabled(true)` is enabled in `runner.Run`, the framework will attempt to repair `toolCall.Function.Arguments` on a best-effort basis.

```go
ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("..."),
    agent.WithToolCallArgumentsJSONRepairEnabled(true),
)
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
            Operation string  `json:"operation" jsonschema:"description=Operation type e.g. add/multiply"`
            A         float64 `json:"a" jsonschema:"description=First operand"`
            B         float64 `json:"b" jsonschema:"description=Second operand"`
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
