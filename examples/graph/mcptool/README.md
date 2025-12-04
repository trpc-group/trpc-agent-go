# Graph + MCP Example

This example shows how to call MCP tools from a graph workflow. It combines:

- A small graph built with `StateGraph`
- An LLM node that can emit tool calls
- A Tools node that executes MCP tools
- A local MCP server started via STDIO (`go run ./stdioserver/main.go`)

The main scenario is querying weather information via an MCP tool (`get_weather`) and letting the graph orchestrate tool calls and final answers.

## What This Example Demonstrates

- How to:
  - Use `graph.StateGraph` to define LLM + Tools nodes
  - Wire `AddToolsConditionalEdges` so the graph only runs tools when the LLM emits `tool_calls`
  - Configure an MCP `ToolSet` that talks to a local STDIO MCP server
  - Stream events (LLM tokens, tool calls, tool responses) via `Runner`
- How the OpenAI‑compatible model sees MCP tools:
  - Tools are passed via the `tools` field (name, description, JSON Schema) in the request
  - The LLM can decide when to call `get_weather` without the prompt re-describing the tool

## Files

- `main.go`  
  Graph example that:
  - Builds a messages‑based graph (`MessagesStateSchema()`)
  - Adds a single LLM node (`assistant`) with tools plus a minimal `finish` node used only to terminate the graph
  - Configures an MCP `ToolSet` (STDIO transport) and registers tools into the graph
  - Streams:
    - MCP tool calls (`🔧 MCP tool calls:`)
    - Tool responses (`✅ Tool response ...`)
    - LLM streaming tokens (`🤖 Assistant: ...`)

- `stdioserver/main.go`  
  A local STDIO MCP server providing:
  - `echo`: echo a message with optional prefix
  - `add`: add two numbers
  - `get_weather`: return structured weather data using the struct‑first + `OutputSchema` APIs

## How the Graph Is Wired

The graph built in `main.go` roughly looks like:

```text
assistant (LLM node with MCP tools)
   │
   ├─(if last assistant message has tool_calls)─► tools (Tools node executes MCP calls)
   │                                             │
   │                                             └──► back to assistant (LLM reads tool responses)
   │
   └─► finish (sink node)
```

- `assistant`: LLM node created with `AddLLMNode`. It receives:
  - The normal conversation messages from state
  - A tools map that includes MCP tools discovered from the STDIO server
- `tools`: Tools node created with `AddToolsNode`. It:
  - Reads tool calls from the last assistant message
  - Calls `get_weather` on the MCP stdio client
  - Appends tool responses as `role=tool` messages
- `finish`: No-op node used only as a graph sink; the user-visible answer is the last response from `assistant`.

The routing uses `AddToolsConditionalEdges("assistant", "tools", "finish")` so the graph only runs the tools node when the LLM actually emits `tool_calls`. After tools complete, control returns to `assistant`, which can either call tools again (if needed) or answer the user directly; when it no longer emits `tool_calls`, the graph routes to `finish`.

## Running the Example

From the repository root:

```bash
cd examples/graph/mcptool
```

Set your OpenAI‑compatible model endpoint (for example, DeepSeek):

```bash
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="sk-..."
export MODEL_NAME="deepseek-chat"
```

Then run the graph:

```bash
go run . -model "${MODEL_NAME:-deepseek-chat}"
```

What happens on startup:

- The example starts the STDIO MCP server by running:
  - `go run ./stdioserver/main.go`
- It discovers MCP tools from the server and registers them into the graph:
  - You should see: `🔧 MCP tools registered in graph: get_weather`
- It creates a `GraphAgent` and `Runner`, then enters interactive mode.

## Interactive Usage

When you see:

```text
📄 Request:
```

You can type prompts like:

- `Use get_weather to check the weather in Beijing`
- `What is the weather in London today? Use get_weather.`

On a successful run you will see:

- A line printing MCP tool calls, for example:

  ```text
  🔧 MCP tool calls:
     • get_weather (ID: call_abc123)
       Args: {"location":"Beijing","units":"celsius"}
  ```

- Tool responses coming back from the MCP server:

  ```text
  ✅ Tool response (ID: call_abc123): {"location":"Beijing","temperature":22,...}
  ```

- The assistant’s final streamed reply:

  ```text
  🤖 Assistant: The current weather in Beijing is ...
  ```

Type `exit` or `quit` to leave the interactive loop.

## Adapting This Example

To adapt this to your own MCP tools:

- Replace `stdioserver/main.go` with your own MCP STDIO server implementation:
  - Keep the same transport config in `main.go` (or update `Command`/`Args` accordingly).
  - Register additional tools (e.g. `get_news`) and expose them via MCP.
- Adjust the tools filter in `main.go`:

  ```go
  mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("get_weather"))
  ```

  for example:

  ```go
  mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("get_weather", "get_news"))
  ```

- Tweak the LLM node prompt if you want to:
  - Encourage more or fewer tool calls
  - Prefer specific tools for specific kinds of requests
