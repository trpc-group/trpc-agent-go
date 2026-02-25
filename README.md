English | [中文](README.zh_CN.md)

# tRPC-Agent-Go

[![Go Reference](https://pkg.go.dev/badge/trpc.group/trpc-go/trpc-agent-go.svg)](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/trpc-group/trpc-agent-go)](https://goreportcard.com/report/github.com/trpc-group/trpc-agent-go)
[![LICENSE](https://img.shields.io/badge/license-Apache--2.0-green.svg)](https://github.com/trpc-group/trpc-agent-go/blob/main/LICENSE)
[![Releases](https://img.shields.io/github/release/trpc-group/trpc-agent-go.svg?style=flat-square)](https://github.com/trpc-group/trpc-agent-go/releases)
[![Tests](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml/badge.svg)](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml)
[![Coverage](https://codecov.io/gh/trpc-group/trpc-agent-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/trpc-group/trpc-agent-go/tree/main)
[![Documentation](https://img.shields.io/badge/Docs-Website-blue.svg)](https://trpc-group.github.io/trpc-agent-go/)

**A powerful Go framework for building intelligent agent systems** that transforms how you create AI applications. Build autonomous agents that think, remember, collaborate, and act with unprecedented ease.

**Why tRPC-Agent-Go?**

- **Intelligent Reasoning**: Advanced hierarchical planners and multi-agent orchestration
- **Rich Tool Ecosystem**: Seamless integration with external APIs, databases, and services
- **Persistent Memory**: Long-term state management and contextual awareness
- **Multi-Agent Collaboration**: Chain, parallel, and graph-based agent workflows
- **GraphAgent**: Type-safe graph workflows with multi-conditional routing, functionally equivalent to LangGraph for Go
- **Agent Skills**: Reusable `SKILL.md` workflows with safe execution
- **Artifacts**: Versioned storage for files produced by agents and tools
- **Prompt Caching**: Automatic cost optimization with 90% savings on cached content
- **Evaluation & Benchmarks**: Eval sets + metrics to measure quality over time
- **UI & Server Integration**: AG-UI (Agent-User Interaction),
  and Agent-to-Agent (A2A) interoperability
- **Production Ready**: Built-in telemetry, tracing, and enterprise-grade reliability
- **High Performance**: Optimized for scalability and low latency

## Use Cases

**Perfect for building:**

- **Customer Support Bots** - Intelligent agents that understand context and solve complex queries
- **Data Analysis Assistants** - Agents that query databases, generate reports, and provide insights
- **DevOps Automation** - Smart deployment, monitoring, and incident response systems
- **Business Process Automation** - Multi-step workflows with human-in-the-loop capabilities
- **Research & Knowledge Management** - RAG-powered agents for document analysis and Q&A

## Key Features

<table>
<tr>
<td width="50%" valign="top">

### Multi-Agent Orchestration

```go
// Chain agents for complex workflows
pipeline := chainagent.New("pipeline",
    chainagent.WithSubAgents([]agent.Agent{
        analyzer, processor, reporter,
    }))

// Or run them in parallel
parallel := parallelagent.New("concurrent",
    parallelagent.WithSubAgents(tasks))
```

</td>
<td width="50%" valign="top">

### Advanced Memory System

```go
// Persistent memory with search
memory := memorysvc.NewInMemoryService()
agent := llmagent.New("assistant",
    llmagent.WithTools(memory.Tools()),
    llmagent.WithModel(model))

// Memory service managed at runner level
runner := runner.NewRunner("app", agent,
    runner.WithMemoryService(memory))

// Agents remember context across sessions
```

</td>
</tr>
<tr>
<td valign="top">

### Rich Tool Integration

```go
// Any function becomes a tool
calculator := function.NewFunctionTool(
    calculate,
    function.WithName("calculator"),
    function.WithDescription("Math operations"))

// MCP protocol support
mcpTool := mcptool.New(serverConn)
```

</td>
<td valign="top">

### Production Observability

```go
// Start Langfuse integration
clean, _ := langfuse.Start(ctx)
defer clean(ctx)

runner := runner.NewRunner("app", agent)
// Run with Langfuse attributes
events, _ := runner.Run(ctx, "user-1", "session-1", 
    model.NewUserMessage("Hello"),
    agent.WithSpanAttributes(
        attribute.String("langfuse.user.id", "user-1"),
        attribute.String("langfuse.session.id", "session-1"),
    ))
```

</td>
</tr>
<tr>
<td valign="top">

### Agent Skills

```go
// Skills are folders with a SKILL.md spec.
repo, _ := skill.NewFSRepository("./skills")

// Let the agent load and run skills on demand.
tools := []tool.Tool{
    skilltool.NewLoadTool(repo),
    skilltool.NewRunTool(repo, localexec.New()),
}
```

`NewFSRepository` also accepts an HTTP(S) URL (for example, a `.zip` or
`.tar.gz` archive). The payload is downloaded and cached locally (set
`SKILLS_CACHE_DIR` to override the cache location).

</td>
<td valign="top">

### Evaluation & Benchmarks

```go
evaluator, _ := evaluation.New("app", runner, evaluation.WithNumRuns(3))
defer evaluator.Close()
result, _ := evaluator.Evaluate(ctx, "math-basic")
_ = result.OverallStatus
```

</td>
</tr>
</table>

## Table of Contents

- [tRPC-Agent-Go](#trpc-agent-go)
  - [Use Cases](#use-cases)
  - [Key Features](#key-features)
    - [Multi-Agent Orchestration](#multi-agent-orchestration)
    - [Advanced Memory System](#advanced-memory-system)
    - [Rich Tool Integration](#rich-tool-integration)
    - [Production Observability](#production-observability)
    - [Agent Skills](#agent-skills)
    - [Evaluation \& Benchmarks](#evaluation--benchmarks)
  - [Table of Contents](#table-of-contents)
  - [Documentation](#documentation)
  - [Quick Start](#quick-start)
    - [Prerequisites](#prerequisites)
    - [Run the Example](#run-the-example)
    - [Basic Usage](#basic-usage)
    - [Stop / Cancel a Run](#stop--cancel-a-run)
  - [Examples](#examples)
    - [1. Tool Usage](#1-tool-usage)
    - [2. LLM-Only Agent](#2-llm-only-agent)
    - [3. Multi-Agent Runners](#3-multi-agent-runners)
    - [4. Graph Agent](#4-graph-agent)
    - [5. Memory](#5-memory)
    - [6. Knowledge](#6-knowledge)
    - [7. Telemetry \& Tracing](#7-telemetry--tracing)
    - [8. MCP Integration](#8-mcp-integration)
    - [9. AG-UI Demo](#9-ag-ui-demo)
    - [10. Evaluation](#10-evaluation)
    - [11. Agent Skills](#11-agent-skills)
    - [12. Artifacts](#12-artifacts)
    - [13. A2A Interop](#13-a2a-interop)
    - [14. Gateway Server](#14-gateway-server)
  - [Architecture Overview](#architecture-overview)
    - [**Execution Flow**](#execution-flow)
  - [Using Built-in Agents](#using-built-in-agents)
    - [Multi-Agent Collaboration Example](#multi-agent-collaboration-example)
  - [Contributing](#contributing)
    - [**Ways to Contribute**](#ways-to-contribute)
    - [**Quick Contribution Setup**](#quick-contribution-setup)
  - [Acknowledgements](#acknowledgements)
    - [**Enterprise Validation**](#enterprise-validation)
    - [**Open Source Inspiration**](#open-source-inspiration)
  - [Star History](#star-history)
  - [License](#license)
    - [**Star us on GitHub** • **Report Issues** • **Join Discussions**](#star-us-on-github--report-issues--join-discussions)

## Documentation

Ready to dive into tRPC-Agent-Go? Our [documentation](https://trpc-group.github.io/trpc-agent-go/) covers everything from basic concepts to advanced techniques, helping you build powerful AI applications with confidence. Whether you're new to AI agents or an experienced developer, you'll find detailed guides, practical examples, and best practices to accelerate your development journey.

## Quick Start

> **See it in Action**: _[Demo GIF placeholder - showing agent reasoning and tool usage]_

### Prerequisites

- Go 1.21 or later
- LLM provider API key (OpenAI, DeepSeek, etc.)
- 5 minutes to build your first intelligent agent

### Run the Example

**Get started in 3 simple steps:**

```bash
# 1. Clone and setup
git clone https://github.com/trpc-group/trpc-agent-go.git
cd trpc-agent-go

# 2. Configure your LLM
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="your-base-url-here"  # Optional

# 3. Run your first agent!
cd examples/runner
go run . -model="gpt-4o-mini" -streaming=true
```

**What you'll see:**

- **Interactive chat** with your AI agent
- **Real-time streaming** responses
- **Tool usage** (calculator + time tools)
- **Multi-turn conversations** with memory

Try asking: "What's the current time? Then calculate 15 \* 23 + 100"

### Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
    // Create model.
    modelInstance := openai.New("deepseek-chat")

    // Create tool.
    calculatorTool := function.NewFunctionTool(
        calculator,
        function.WithName("calculator"),
        function.WithDescription("Execute addition, subtraction, multiplication, and division. "+
            "Parameters: a, b are numeric values, op takes values add/sub/mul/div; "+
            "returns result as the calculation result."),
    )

    // Enable streaming output.
    genConfig := model.GenerationConfig{
        Stream: true,
    }

    // Create Agent.
    agent := llmagent.New("assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithTools([]tool.Tool{calculatorTool}),
        llmagent.WithGenerationConfig(genConfig),
    )

    // Create Runner.
    runner := runner.NewRunner("calculator-app", agent)

    // Execute conversation.
    ctx := context.Background()
    events, err := runner.Run(ctx,
        "user-001",
        "session-001",
        model.NewUserMessage("Calculate what 2+3 equals"),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Process event stream.
    for event := range events {
        if event.Object == "chat.completion.chunk" {
            fmt.Print(event.Response.Choices[0].Delta.Content)
        }
    }
    fmt.Println()
}

func calculator(ctx context.Context, req calculatorReq) (calculatorRsp, error) {
    var result float64
    switch req.Op {
    case "add", "+":
        result = req.A + req.B
    case "sub", "-":
        result = req.A - req.B
    case "mul", "*":
        result = req.A * req.B
    case "div", "/":
        result = req.A / req.B
	default:
		return calculatorRsp{}, fmt.Errorf("invalid operation: %s", req.Op)
    }
    return calculatorRsp{Result: result}, nil
}

type calculatorReq struct {
    A  float64 `json:"A"  jsonschema:"description=First integer operand,required"`
    B  float64 `json:"B"  jsonschema:"description=Second integer operand,required"`
    Op string  `json:"Op" jsonschema:"description=Operation type,enum=add,enum=sub,enum=mul,enum=div,required"`
}

type calculatorRsp struct {
    Result float64 `json:"result"`
}
```

### Dynamic Agent per Request

Sometimes your Agent must be created **per request** (for example: different
prompt, model, tools, sandbox instance). In that case, you can let Runner build
a fresh Agent for every `Run(...)`:

```go
r := runner.NewRunnerWithAgentFactory(
    "my-app",
    "assistant",
    func(ctx context.Context, ro agent.RunOptions) (agent.Agent, error) {
        // Use ro to build an Agent for this request.
        a := llmagent.New("assistant",
            llmagent.WithInstruction(ro.Instruction),
        )
        return a, nil
    },
)

events, err := r.Run(ctx,
    "user-001",
    "session-001",
    model.NewUserMessage("Hello"),
    agent.WithInstruction("You are a helpful assistant."),
)
_ = events
_ = err
```

### Stop / Cancel a Run

If you want to interrupt a running agent, **cancel the context** you passed to
`Runner.Run` (recommended). This stops model calls and tool calls safely and
lets the runner clean up.

Important: **do not** just “break” your event loop and walk away — the agent
goroutine may keep running and can block on channel writes. Always cancel, then
keep draining the event channel until it is closed.

#### Option A: Ctrl+C (terminal programs)

Convert Ctrl+C into context cancellation:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

events, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    return err
}
for range events {
    // Drain until the runner stops (ctx canceled or run completed).
}
```

#### Option B: Cancel from your code

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

events, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    return err
}

go func() {
    time.Sleep(2 * time.Second)
    cancel()
}()

for range events {
    // Keep draining until the channel is closed.
}
```

#### Option C: Cancel by `requestID` (for servers / background runs)

```go
requestID := "req-123"
events, err := r.Run(ctx, userID, sessionID, message,
    agent.WithRequestID(requestID),
)

mr := r.(runner.ManagedRunner)
_ = mr.Cancel(requestID)
```

For more details (including detached cancellation, resume, and server cancel
routes), see `docs/mkdocs/en/runner.md` and `docs/mkdocs/en/agui.md`.

## Examples

The `examples` directory contains runnable demos covering every major feature.

### 1. Tool Usage

- [examples/agenttool](examples/agenttool) – Wrap agents as callable tools.
- [examples/multitools](examples/multitools) – Multiple tools orchestration.
- [examples/duckduckgo](examples/duckduckgo) – Web search tool integration.
- [examples/filetoolset](examples/filetoolset) – File operations as tools.
- [examples/fileinput](examples/fileinput) – Provide files as inputs.
- [examples/agenttool](examples/agenttool) shows streaming and non-streaming
  patterns.

### 2. LLM-Only Agent

Example: [examples/llmagent](examples/llmagent)

- Wrap any chat-completion model as an `LLMAgent`.
- Configure system instructions, temperature, max tokens, etc.
- Receive incremental `event.Event` updates while the model streams.

### 3. Multi-Agent Runners

Example: [examples/multiagent](examples/multiagent)

- **ChainAgent** – linear pipeline of sub-agents.
- **ParallelAgent** – run sub-agents concurrently and merge results.
- **CycleAgent** – iterate until a termination condition is met.

### 4. Graph Agent

Example: [examples/graph](examples/graph)

- **GraphAgent** – demonstrates building and executing complex, conditional
  workflows using the `graph` and `agent/graph` packages. It shows
  how to construct a graph-based agent, manage state safely, implement
  conditional routing, and orchestrate execution with the Runner.

- Multi-conditional fan-out routing:

```go
// Return multiple branch keys and run targets in parallel.
sg := graph.NewStateGraph(schema)
sg.AddNode("router", func(ctx context.Context, s graph.State) (any, error) {
    return nil, nil
})
sg.AddNode("A", func(ctx context.Context, s graph.State) (any, error) {
    return graph.State{"a": 1}, nil
})
sg.AddNode("B", func(ctx context.Context, s graph.State) (any, error) {
    return graph.State{"b": 1}, nil
})
sg.SetEntryPoint("router")
sg.AddMultiConditionalEdges(
    "router",
    func(ctx context.Context, s graph.State) ([]string, error) {
        return []string{"goA", "goB"}, nil
    },
    map[string]string{"goA": "A", "goB": "B"}, // Path map or ends map
)
sg.SetFinishPoint("A").SetFinishPoint("B")
```

### 5. Memory

Example: [examples/memory](examples/memory)

- In‑memory and Redis memory services with CRUD, search and tool integration.
- How to configure, call tools and customize prompts.

### 6. Knowledge

Example: [examples/knowledge](examples/knowledge)

- Basic RAG example: load sources, embed to a vector store, and search.
- How to use conversation context and tune loading/concurrency options.

### 7. Telemetry & Tracing

Example: [examples/telemetry](examples/telemetry)

- OpenTelemetry hooks across model, tool and runner layers.
- Export traces to OTLP endpoint for real-time analysis.

### 8. MCP Integration

Example: [examples/mcptool](examples/mcptool)

- Wrapper utilities around **trpc-mcp-go**, an implementation of the
  **Model Context Protocol (MCP)**.
- Provides structured prompts, tool calls, resource and session messages that
  follow the MCP specification.
- Enables dynamic tool execution and context-rich interactions between agents
  and LLMs.

### 9. AG-UI Demo

Example: [examples/agui](examples/agui)

- Exposes a Runner through the AG-UI (Agent-User Interaction) protocol.
- Built-in Server-Sent Events (SSE) server, plus client samples (for example,
  CopilotKit and TDesign Chat).

### 10. Evaluation

Example: [examples/evaluation](examples/evaluation)

- Evaluate an agent with repeatable eval sets and pluggable metrics.
- Includes local file-backed runs and in-memory runs.

### 11. Agent Skills

Example: [examples/skillrun](examples/skillrun)

- Skills are folders with a `SKILL.md` spec + optional docs/scripts.
- Built-in tools: `skill_load`, `skill_list_docs`, `skill_select_docs`,
  `skill_run` (runs commands in an isolated workspace).
- Prefer using `skill_run` only for commands required by the selected skill
  docs, not for generic shell exploration.

### 12. Artifacts

Example: [examples/artifact](examples/artifact)

- Save and retrieve versioned files (images, text, reports) produced by tools.
- Supports multiple backends (in-memory, S3, COS).

### 13. A2A Interop

Example: [examples/a2aadk](examples/a2aadk)

- Agent-to-Agent (A2A) interop with an ADK Python A2A server.
- Demonstrates streaming, tool calls, and code execution across runtimes.

### 14. Gateway Server

Example: [openclaw](openclaw)

- A minimal OpenClaw-like gateway server.
- Stable session ids and per-session serialization.
- Basic safety controls: allowlist + mention gating.
- OpenClaw-like demo binary (Telegram + gateway): [openclaw](openclaw)

Other notable examples:

- [examples/humaninloop](examples/humaninloop) – Human in the loop.
- [examples/codeexecution](examples/codeexecution) – Secure code execution.

See individual `README.md` files in each example folder for usage details.

## Architecture Overview

Architecture

![architecture](docs/mkdocs/assets/img/component_architecture.svg)

### **Execution Flow**

1. **Runner** orchestrates the entire execution pipeline with session management
2. **Agent** processes requests using multiple specialized components
3. **Planner** determines the optimal strategy and tool selection
4. **Tools** execute specific tasks (API calls, calculations, web searches)
5. **Memory** maintains context and learns from interactions
6. **Knowledge** provides RAG capabilities for document understanding

Key packages:

| Package     | Responsibility                                                                                              |
| ----------- | ----------------------------------------------------------------------------------------------------------- |
| `agent`     | Core execution unit, responsible for processing user input and generating responses.                        |
| `runner`    | Agent executor, responsible for managing execution flow and connecting Session/Memory Service capabilities. |
| `model`     | Supports multiple LLM models (OpenAI, DeepSeek, etc.).                                                      |
| `tool`      | Provides various tool capabilities (Function, MCP, DuckDuckGo, etc.).                                       |
| `session`   | Manages user session state and events.                                                                      |
| `memory`    | Records user long-term memory and personalized information.                                                 |
| `knowledge` | Implements RAG knowledge retrieval capabilities.                                                            |
| `planner`   | Provides Agent planning and reasoning capabilities.                                                         |
| `artifact`  | Stores and retrieves versioned files produced by agents and tools (images, reports, etc.).                  |
| `skill`     | Loads and executes reusable Agent Skills defined by `SKILL.md`.                                             |
| `event`     | Defines event types and streaming payloads used across Runner and servers.                                  |
| `evaluation` | Evaluates agents on eval sets using pluggable metrics and stores results.                                  |
| `server`    | Exposes HTTP servers (Gateway, AG-UI, A2A) for integration and UIs.                                         |
| `telemetry` | OpenTelemetry tracing and metrics instrumentation.                                                          |


## Using Built-in Agents

For most applications you **do not** need to implement the `agent.Agent`
interface yourself. The framework already ships with several ready-to-use
agents that you can compose like Lego bricks:

| Agent           | Purpose                                             |
| --------------- | --------------------------------------------------- |
| `LLMAgent`      | Wraps an LLM chat-completion model as an agent.     |
| `ChainAgent`    | Executes sub-agents sequentially.                   |
| `ParallelAgent` | Executes sub-agents concurrently and merges output. |
| `CycleAgent`    | Loops over a planner + executor until stop signal.  |

### Multi-Agent Collaboration Example

```go
// 1. Create a base LLM agent.
base := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
)

// 2. Create a second LLM agent with a different instruction.
translator := llmagent.New(
    "translator",
    llmagent.WithInstruction("Translate everything to French"),
    llmagent.WithModel(openai.New("gpt-3.5-turbo")),
)

// 3. Combine them in a chain.
pipeline := chainagent.New(
    "pipeline",
    chainagent.WithSubAgents([]agent.Agent{base, translator}),
)

// 4. Run through the runner for sessions & telemetry.
run := runner.NewRunner("demo-app", pipeline)
events, _ := run.Run(ctx, "user-1", "sess-1",
    model.NewUserMessage("Hello!"))
for ev := range events { /* ... */ }
```

The composition API lets you nest chains, cycles, or parallels to build complex
workflows without low-level plumbing.

## Contributing

We love contributions! Join our growing community of developers building the future of AI agents.

### **Ways to Contribute**

- **Report bugs** or suggest features via [Issues](https://github.com/trpc-group/trpc-agent-go/issues)
- **Improve documentation** - help others learn faster
- **Submit PRs** - bug fixes, new features, or examples
- **Share your use cases** - inspire others with your agent applications

### **Quick Contribution Setup**

```bash
# Fork & clone the repo
git clone https://github.com/YOUR_USERNAME/trpc-agent-go.git
cd trpc-agent-go

# Run tests to ensure everything works
go test ./...
go vet ./...

# Make your changes and submit a PR!
```

**Please read** [CONTRIBUTING.md](CONTRIBUTING.md) for detailed guidelines and coding standards.

## Acknowledgements

### **Enterprise Validation**

Special thanks to Tencent's business units including **Tencent Yuanbao**, **Tencent Video**, **Tencent News**, **IMA**, and **QQ Music** for their invaluable support and real-world validation. Production usage drives framework excellence!

### **Open Source Inspiration**

Inspired by amazing frameworks like **ADK**, **Agno**, **CrewAI**, **AutoGen**, and many others. Standing on the shoulders of giants!

---

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=trpc-group/trpc-agent-go&type=Date)](https://star-history.com/#trpc-group/trpc-agent-go&Date)

---

## License

Licensed under the **Apache 2.0 License** - see [LICENSE](LICENSE) file for details.

---

<div align="center">

### **Star us on GitHub** • **Report Issues** • **Join Discussions**

**Built with love by the tRPC-Agent-Go team**

_Empowering developers to build the next generation of intelligent applications_

</div>
