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
  - [Tool Usage](#1-tool-usage-examplestool)
  - [LLM-only Agent](#2-llma-only-agent-examplesllmagent)
  - [Multi-Agent Runners](#3-multi-agent-runners-examplesmultiagent)
  - [Telemetry & Tracing](#4-telemetry--tracing-examplestelemetry)
- [Architecture Overview](#architecture-overview)
- [Using Built-in Agents](#using-built-in-agents)
- [Memory & Knowledge](#memory--knowledge)
- [Future Enhancements](#future-enhancements)
- [Contributing](#contributing)
- [Acknowledgements](#acknowledgements)


## Quick Start

### Prerequisites

1. Go 1.24 or later.
2. An LLM provider key (e.g. `OPENAI_API_KEY`).

```bash
# Install the module
go get trpc.group/trpc-go/trpc-agent-go@latest

# Run the tool example
export OPENAI_API_KEY="<your-key>"
cd examples/tool
go run . -model="gpt-4o-mini"
```

The example shows an agent that calls **function tools** to retrieve weather and
population data. Switch between streaming and non-streaming modes with
`streaming_input.go` and `non-streaming.go`.


## Examples

The `examples` directory contains runnable demos covering every major feature.

### 1. Tool Usage ([examples/tool](examples/tool))

- Create JSON-schema function tools.
- Let the LLM decide when to invoke a tool.
- Streaming vs. non-streaming interaction patterns.

### 2. LLMA-Only Agent ([examples/llmagent](examples/llmagent))

- Wrap any chat-completion model as an `LLMAgent`.
- Configure system instructions, temperature, max tokens, etc.
- Receive incremental `event.Event` updates while the model streams.

### 3. Multi-Agent Runners ([examples/multiagent](examples/multiagent))

- **ChainAgent** – linear pipeline of sub-agents.
- **ParallelAgent** – run sub-agents concurrently and merge results.
- **CycleAgent** – iterate until a termination condition is met.

### 4. Telemetry & Tracing ([examples/telemetry](examples/telemetry))

- OpenTelemetry hooks across model, tool and runner layers.
- Export traces to OTLP endpoint for real-time analysis.

### 5. MCP Integration ([tool/mcp](tool/mcp))

- Wrapper utilities around **trpc-mcp-go**, an implementation of the
  **Model Context Protocol (MCP)**.
- Provides structured prompts, tool calls, resource and session messages that
  follow the MCP specification.
- Enables dynamic tool execution and context-rich interactions between agents
  and LLMs.

### 6. ADK Web Demo ([examples/adkweb](examples/adkweb))

- Launches an **ADK Server** that speaks ADK-compatible HTTP endpoints.
- Front-end: [google/adk-web](https://github.com/google/adk-web) connects via
  `/run_sse`, streams agent responses in real-time.
- Great starting point for building your own chat UI.

See individual `README.md` files in each example folder for usage details.


## Architecture Overview

```text
┌─────────────────────┐
│       Runner        │  orchestrates sessions & events
└─────────┬───────────┘
          │ invokes
┌─────────▼───────────┐
│       Agent         │  implements business logic
└───────┬┴┬┬┬┬────────┘
        │ ││││ sub-agents / tools
┌───────▼─▼▼▼▼────────┐
│     Planner &       │  breakpoint reasoning / TODO planning
│   Generation Loop   │
└──────────┬──────────┘
           │ calls
┌──────────▼──────────┐
│      LLM Model      │  chat-completion, embedding, …
└─────────────────────┘
```

Key packages:

| Package | Responsibility |
|---------|----------------|
| `agent` | Core interfaces & built-in `ChainAgent`, `ParallelAgent`, `LLMAgent`, etc. |
| `tool` | Unified tool specification, JSON schema, execution helpers & built-ins (e.g. DuckDuckGo search). |
| `planner` | Next-step planners: built-in & ReAct-style reasoning. |
| `runner` | Session lifecycle, event persistence, OpenTelemetry tracing. |
| `memory` | Abstract memory interfaces (vector DB integrations coming soon). |


## Using Built-in Agents

For most applications you **do not** need to implement the `agent.Agent`
interface yourself. The framework already ships with several ready-to-use
agents that you can compose like Lego bricks:

| Agent            | Purpose                                            |
|------------------|----------------------------------------------------|
| `LLMAgent`       | Wraps an LLM chat-completion model as an agent.    |
| `ChainAgent`     | Executes sub-agents sequentially.                  |
| `ParallelAgent`  | Executes sub-agents concurrently and merges output.|
| `CycleAgent`     | Loops over a planner + executor until stop signal. |

### Quick composition example

```go
// 1. Create a base LLM agent.
base := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("gpt-4o-mini", nil)),
)

// 2. Create a second LLM agent with a different instruction.
translator := llmagent.New(
    "translator",
    llmagent.WithInstruction("Translate everything to French"),
    llmagent.WithModel(openai.New("gpt-3.5-turbo", nil)),
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


## Memory & Knowledge

`tRPC-Agent-Go` ships with an in-memory session store. Future releases will add
vector store integrations (Milvus, Pinecone, Qdrant) and long-term knowledge
bases under `knowledge/`.


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

Inspired by Google Adk.

Licensed under the Apache 2.0 License.
