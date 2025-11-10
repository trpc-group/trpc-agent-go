English | [中文](README.zh_CN.md)

# tRPC-Agent-Go

[![Go Reference](https://pkg.go.dev/badge/trpc.group/trpc-go/trpc-agent-go.svg)](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/trpc-group/trpc-agent-go)](https://goreportcard.com/report/github.com/trpc-group/trpc-agent-go)
[![LICENSE](https://img.shields.io/badge/license-Apache--2.0-green.svg)](https://github.com/trpc-group/trpc-agent-go/blob/main/LICENSE)
[![Releases](https://img.shields.io/github/release/trpc-group/trpc-agent-go.svg?style=flat-square)](https://github.com/trpc-group/trpc-agent-go/releases)
[![Tests](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml/badge.svg)](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml)
[![Coverage](https://codecov.io/gh/trpc-group/trpc-agent-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/trpc-group/trpc-agent-go/tree/main)
[![Documentation](https://img.shields.io/badge/Docs-Website-blue.svg)](https://trpc-group.github.io/trpc-agent-go/)

🚀 **A powerful Go framework for building intelligent agent systems** that transforms how you create AI applications. Build autonomous agents that think, remember, collaborate, and act with unprecedented ease.

✨ **Why tRPC-Agent-Go?**

- 🧠 **Intelligent Reasoning**: Advanced hierarchical planners and multi-agent orchestration
- 🧰 **Rich Tool Ecosystem**: Seamless integration with external APIs, databases, and services
- 💾 **Persistent Memory**: Long-term state management and contextual awareness
- 🔗 **Multi-Agent Collaboration**: Chain, parallel, and graph-based agent workflows
- 📊 **Production Ready**: Built-in telemetry, tracing, and enterprise-grade reliability
- ⚡ **High Performance**: Optimized for scalability and low latency

## 🎯 Use Cases

**Perfect for building:**

- 🤖 **Customer Support Bots** - Intelligent agents that understand context and solve complex queries
- 📊 **Data Analysis Assistants** - Agents that query databases, generate reports, and provide insights
- 🔧 **DevOps Automation** - Smart deployment, monitoring, and incident response systems
- 💼 **Business Process Automation** - Multi-step workflows with human-in-the-loop capabilities
- 🧠 **Research & Knowledge Management** - RAG-powered agents for document analysis and Q&A

## 🚀 Key Features

<table>
<tr>
<td width="50%">

### 🎪 **Multi-Agent Orchestration**

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
<td width="50%">

### 🧠 **Advanced Memory System**

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
<td>

### 🛠️ **Rich Tool Integration**

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
<td>

### 📈 **Production Monitoring**

```go
// OpenTelemetry integration
runner := runner.NewRunner("app", agent,
    runner.WithTelemetry(telemetry.Config{
        TracingEnabled: true,
        MetricsEnabled: true,
    }))
```

</td>
</tr>
</table>

## Table of Contents

- [Use Cases](#-use-cases)
- [Key Features](#-key-features)
- [Documentation](#documentation)
- [Quick Start](#quick-start)
- [Examples](#examples)
  - [Tool Usage](#1-tool-usage-examples)
  - [LLM-only Agent](#2-llm-only-agent)
  - [Multi-Agent Runners](#3-multi-agent-runners)
  - [Graph Agent](#4-graph-agent)
  - [Memory](#5-memory)
  - [Knowledge](#6-knowledge)
  - [Telemetry & Tracing](#7-telemetry--tracing)
  - [MCP Integration](#8-mcp-integration)
  - [Debug Web Demo](#9-debug-web-demo)
- [Architecture Overview](#architecture-overview)
- [Using Built-in Agents](#using-built-in-agents)
- [Future Enhancements](#future-enhancements)
- [Contributing](#contributing)
- [Acknowledgements](#acknowledgements)

## Documentation

Ready to dive into tRPC-Agent-Go? Our [documentation](https://trpc-group.github.io/trpc-agent-go/) covers everything from basic concepts to advanced techniques, helping you build powerful AI applications with confidence. Whether you're new to AI agents or an experienced developer, you'll find detailed guides, practical examples, and best practices to accelerate your development journey.

## ⚡ Quick Start

> 🎬 **See it in Action**: _[Demo GIF placeholder - showing agent reasoning and tool usage]_

### 📋 Prerequisites

- ✅ Go 1.21 or later
- 🔑 LLM provider API key (OpenAI, DeepSeek, etc.)
- 💡 5 minutes to build your first intelligent agent

### 🚀 Run the Example

**Get started in 3 simple steps:**

```bash
# 1️⃣ Clone and setup
git clone https://github.com/trpc-group/trpc-agent-go.git
cd trpc-agent-go

# 2️⃣ Configure your LLM
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="your-base-url-here"  # Optional

# 3️⃣ Run your first agent! 🎉
cd examples/runner
go run . -model="gpt-4o-mini" -streaming=true
```

**What you'll see:**

- 💬 **Interactive chat** with your AI agent
- ⚡ **Real-time streaming** responses
- 🧮 **Tool usage** (calculator + time tools)
- 🔄 **Multi-turn conversations** with memory

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

### 2. LLM-Only Agent ([examples/llmagent](examples/llmagent))

- Wrap any chat-completion model as an `LLMAgent`.
- Configure system instructions, temperature, max tokens, etc.
- Receive incremental `event.Event` updates while the model streams.

### 3. Multi-Agent Runners ([examples/multiagent](examples/multiagent))

- **ChainAgent** – linear pipeline of sub-agents.
- **ParallelAgent** – run sub-agents concurrently and merge results.
- **CycleAgent** – iterate until a termination condition is met.

### 4. Graph Agent ([examples/graph](examples/graph))

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

### 5. Memory ([examples/memory](examples/memory))

- In‑memory and Redis memory services with CRUD, search and tool integration.
- How to configure, call tools and customize prompts.

### 6. Knowledge ([examples/knowledge](examples/knowledge))

- Basic RAG example: load sources, embed to a vector store, and search.
- How to use conversation context and tune loading/concurrency options.

### 7. Telemetry & Tracing ([examples/telemetry](examples/telemetry))

- OpenTelemetry hooks across model, tool and runner layers.
- Export traces to OTLP endpoint for real-time analysis.

### 8. MCP Integration ([examples/mcptool](examples/mcptool))

- Wrapper utilities around **trpc-mcp-go**, an implementation of the
  **Model Context Protocol (MCP)**.
- Provides structured prompts, tool calls, resource and session messages that
  follow the MCP specification.
- Enables dynamic tool execution and context-rich interactions between agents
  and LLMs.

### 9. Debug Web Demo ([examples/debugserver](examples/debugserver))

- Launches a **debug Server** that speaks ADK-compatible HTTP endpoints.
- Front-end: [google/adk-web](https://github.com/google/adk-web) connects via
  `/run_sse`, streams agent responses in real-time.
- Great starting point for building your own chat UI.

Other notable examples:

- [examples/humaninloop](examples/humaninloop) – Human in the loop.
- [examples/codeexecution](examples/codeexecution) – Secure code execution.

See individual `README.md` files in each example folder for usage details.

## 🏗️ Architecture Overview

Architecture

![architecture](docs/mkdocs/assets/img/component_architecture.png)

### 🔄 **Execution Flow**

1. **🚀 Runner** orchestrates the entire execution pipeline with session management
2. **🤖 Agent** processes requests using multiple specialized components
3. **🧠 Planner** determines the optimal strategy and tool selection
4. **🛠️ Tools** execute specific tasks (API calls, calculations, web searches)
5. **💾 Memory** maintains context and learns from interactions
6. **📚 Knowledge** provides RAG capabilities for document understanding

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

Execution flow
![execution](docs/mkdocs/assets/img/timing_diagram.png)

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

## 🤝 Contributing

We ❤️ contributions! Join our growing community of developers building the future of AI agents.

### 🌟 **Ways to Contribute**

- 🐛 **Report bugs** or suggest features via [Issues](https://github.com/trpc-group/trpc-agent-go/issues)
- 📖 **Improve documentation** - help others learn faster
- 🔧 **Submit PRs** - bug fixes, new features, or examples
- 💡 **Share your use cases** - inspire others with your agent applications

### 🚀 **Quick Contribution Setup**

```bash
# Fork & clone the repo
git clone https://github.com/YOUR_USERNAME/trpc-agent-go.git
cd trpc-agent-go

# Run tests to ensure everything works
go test ./...
go vet ./...

# Make your changes and submit a PR! 🎉
```

📋 **Please read** [CONTRIBUTING.md](CONTRIBUTING.md) for detailed guidelines and coding standards.

## 🏆 Acknowledgements

### 🏢 **Enterprise Validation**

Special thanks to Tencent's business units including **Tencent Yuanbao**, **Tencent Video**, **Tencent News**, **IMA**, and **QQ Music** for their invaluable support and real-world validation. Production usage drives framework excellence! 🚀

### 🌟 **Open Source Inspiration**

Inspired by amazing frameworks like **ADK**, **Agno**, **CrewAI**, **AutoGen**, and many others. Standing on the shoulders of giants! 🙏

---

## ⭐ Star History

[![Star History Chart](https://api.star-history.com/svg?repos=trpc-group/trpc-agent-go&type=Date)](https://star-history.com/#trpc-group/trpc-agent-go&Date)

---

## 📜 License

Licensed under the **Apache 2.0 License** - see [LICENSE](LICENSE) file for details.

---

<div align="center">

### 🌟 **Star us on GitHub** • 🐛 **Report Issues** • 💬 **Join Discussions**

**Built with ❤️ by the tRPC-Agent-Go team**

_Empowering developers to build the next generation of intelligent applications_

</div>
