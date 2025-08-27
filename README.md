# tRPC-Agent-Go

[![Go Reference](https://pkg.go.dev/badge/trpc.group/trpc-go/trpc-agent-go.svg)](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/trpc-group/trpc-agent-go)](https://goreportcard.com/report/github.com/trpc-group/trpc-agent-go)
[![LICENSE](https://img.shields.io/badge/license-Apache--2.0-green.svg)](https://github.com/trpc-group/trpc-agent-go/blob/main/LICENSE)
[![Releases](https://img.shields.io/github/release/trpc-group/trpc-agent-go.svg?style=flat-square)](https://github.com/trpc-group/trpc-agent-go/releases)
[![Tests](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml/badge.svg)](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml)
[![Coverage](https://codecov.io/gh/trpc-group/trpc-agent-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/trpc-group/trpc-agent-go/tree/main)

A powerful Go framework for building intelligent **agent systems** with large
language models (LLMs), hierarchical planners, memory, telemetry and a rich
**tool** ecosystem. If you want to create autonomous or semi-autonomous agents
that reason, call tools, collaborate with sub-agents and keep long-term state,
`tRPC-Agent-Go` has you covered.

## Table of Contents

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

## Quick Start

### Prerequisites

1. Go 1.24.1 or later.
2. An LLM provider key (e.g. `OPENAI_API_KEY`, `OPENAI_BASE_URL`).

### Run the example

Use the commands below to configure your environment and start a multi-turn
chat session with streaming and tool calls via the Runner.

```bash
# Clone the project
git clone https://github.com/trpc-group/trpc-agent-go.git
cd trpc-agent-go

# Run a quick example
export OPENAI_API_KEY="<your-api-key>"
export OPENAI_BASE_URL="<your-base-url>"
cd examples/runner
go run . -model="gpt-4o-mini" -streaming=true
```

[examples/runner](examples/runner) demonstrates multi‑turn chat via the **Runner** with session management, streaming output, and tool calling.
It includes two tools: a calculator and a current time tool. Toggle behavior with flags like `-streaming` and `-enable-parallel`.

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
            fmt.Print(event.Choices[0].Delta.Content)
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

## Architecture Overview

```text
┌─────────────────────┐
│       Runner        │  orchestrates session, memory, ...
└─────────┬───────────┘
          │ invokes
┌─────────▼───────────┐
│       Agent         │  implements business logic, integrated with knowledge, code executor, ...
└─────────┬───────────┘
          │ sub-agents(LLM, Graph, Multi, A2A agents)
┌─────────▼───────────┐
│       Planner       │  decide next action / tool use
└────────┬────────────┘
         │ LLM flow
┌────────▼──────────┐   ┌──────────────┐
│     Model Call    │──►│    Tools     │ function / agent / MCP
└────────┬──────────┘   └──────────────┘
         │ calls
┌────────▼──────────┐
│     LLM Model     │  chat-completion, batch, embedding, …
└───────────────────┘
```

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

## Future Enhancements

- Persistent memory adapters (PostgreSQL, Redis).
- More built-in tools (web search, calculators, file I/O).
- Advanced planners (tree-of-thought, graph execution).
- gRPC & HTTP servers for remote agent invocation.
- Comprehensive benchmark & test suite.

## Contributing

Pull requests, issues and suggestions are very welcome! Please read
[CONTRIBUTING.md](CONTRIBUTING.md) and follow Go coding conventions. Run
`go test ./... && go vet ./...` before submitting.

## Acknowledgements

Thanks to Tencent internal businesses such as Tencent Yuanbao, Tencent Video, Tencent News, IMA, QQ Music,
and other businesses for their support. Business scenario refinement is the best validation for the framework.

Thanks to excellent open-source frameworks like ADK, Agno, CrewAI, AutoGen, etc., for their inspiration,
providing ideas for tRPC-Agent-Go development.

Licensed under the Apache 2.0 License.
