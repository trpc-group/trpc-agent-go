[English](README.md) | 中文

# tRPC-Agent-Go

[![Go Reference](https://pkg.go.dev/badge/trpc.group/trpc-go/trpc-agent-go.svg)](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/trpc-group/trpc-agent-go)](https://goreportcard.com/report/github.com/trpc-group/trpc-agent-go)
[![LICENSE](https://img.shields.io/badge/license-Apache--2.0-green.svg)](https://github.com/trpc-group/trpc-agent-go/blob/main/LICENSE)
[![Releases](https://img.shields.io/github/release/trpc-group/trpc-agent-go.svg?style=flat-square)](https://github.com/trpc-group/trpc-agent-go/releases)
[![Tests](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml/badge.svg)](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml)
[![Coverage](https://codecov.io/gh/trpc-group/trpc-agent-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/trpc-group/trpc-agent-go/tree/main)
[![Documentation](https://img.shields.io/badge/Docs-Website-blue.svg)](https://trpc-group.github.io/trpc-agent-go/)

一个用于构建智能**agent 系统**的强大 Go 框架，内置大语言模型（LLM）、分层规划器、memory、telemetry 以及丰富的 **tool** 生态。如果你希望创建能够进行推理、调用工具、与子 agent 协作并保持长期状态的自主或半自主 agent，`tRPC-Agent-Go` 都能满足需求。

## 目录

- [文档](#文档)
- [快速开始](#快速开始)
- [示例](#示例)
  - [Tool 用法](#1-tool-用法)
  - [仅 LLM 的 Agent](#2-仅-llm-的-agent)
  - [多 Agent Runner](#3-多-agent-runner)
  - [Graph Agent](#4-graph-agent)
  - [Memory](#5-memory)
  - [Knowledge](#6-knowledge)
  - [Telemetry 与 Tracing](#7-telemetry-与-tracing)
  - [MCP 集成](#8-mcp-集成)
  - [调试 Web Demo](#9-调试-web-demo)
- [架构概览](#架构概览)
- [使用内置 Agents](#使用内置-agents)
- [未来增强](#未来增强)
- [贡献](#贡献)
- [致谢](#致谢)

## 文档

准备好深入了解 tRPC-Agent-Go 了吗？我们的[文档](https://trpc-group.github.io/trpc-agent-go/)涵盖从基础概念到高级技巧的一切，帮助你自信地构建强大的 AI 应用。无论你是 AI agent 新手还是有经验的开发者，都能在其中找到详细指南、实用示例和最佳实践，加速你的开发旅程。

## 快速开始

### 前置条件

1. Go 1.24.1 或更高版本。
2. 一个 LLM 提供商的密钥（例如 `OPENAI_API_KEY`、`OPENAI_BASE_URL`）。

### 运行示例

使用以下命令配置环境并通过 Runner 启动一个带有 streaming 和 tool 调用的多轮对话会话。

```bash
# 克隆项目
git clone https://github.com/trpc-group/trpc-agent-go.git
cd trpc-agent-go

# 运行一个快速示例
export OPENAI_API_KEY="<your-api-key>"
export OPENAI_BASE_URL="<your-base-url>"
cd examples/runner
go run . -model="gpt-4o-mini" -streaming=true
```

[examples/runner](examples/runner) 展示了通过 **Runner** 实现的多轮对话，包括会话管理、流式输出以及 tool 调用。
它包含两个工具：计算器和当前时间工具。可以通过 `-streaming`、`-enable-parallel` 等标志切换行为。

### 基本用法

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

## 示例

`examples` 目录包含涵盖各主要功能的可运行 Demo。

### 1. Tool 用法

- [examples/agenttool](examples/agenttool) – 将 agent 封装为可调用的 tool。
- [examples/multitools](examples/multitools) – 多工具编排。
- [examples/duckduckgo](examples/duckduckgo) – Web 搜索工具集成。
- [examples/filetoolset](examples/filetoolset) – 文件操作作为工具。
- [examples/fileinput](examples/fileinput) – 以文件作为输入。
- [examples/agenttool](examples/agenttool) 展示了流式与非流式模式。

### 2. 仅 LLM 的 Agent（[examples/llmagent](examples/llmagent)）

- 将任意 chat-completion 模型封装为 `LLMAgent`。
- 配置 system 指令、temperature、max tokens 等。
- 在模型流式输出时接收增量 `event.Event` 更新。

### 3. 多 Agent Runner（[examples/multiagent](examples/multiagent)）

- **ChainAgent** – 子 agent 的线性流水线。
- **ParallelAgent** – 并发执行子 agent 并合并结果。
- **CycleAgent** – 迭代执行直到满足终止条件。

### 4. Graph Agent（[examples/graph](examples/graph)）

- **GraphAgent** – 展示如何使用 `graph` 与 `agent/graph` 包来构建并执行复杂的、带条件的工作流。展示了如何构建基于图的 agent、安全管理状态、实现条件路由，并通过 Runner 进行编排执行。

### 5. Memory（[examples/memory](examples/memory)）

- 提供内存与 Redis memory 服务，包含 CRUD、搜索与 tool 集成。
- 如何进行配置、调用工具以及自定义 prompts。

### 6. Knowledge（[examples/knowledge](examples/knowledge)）

- 基础 RAG 示例：加载数据源、向量化到 vector store，并进行搜索。
- 如何使用对话上下文以及调节加载/并发选项。

### 7. Telemetry 与 Tracing（[examples/telemetry](examples/telemetry)）

- 在 model、tool 与 runner 层面的 OpenTelemetry hooks。
- 将 traces 导出到 OTLP endpoint 进行实时分析。

### 8. MCP 集成（[examples/mcptool](examples/mcptool)）

- 围绕 **trpc-mcp-go** 的封装工具，这是 **Model Context Protocol (MCP)** 的一个实现。
- 提供遵循 MCP 规范的 structured prompts、tool 调用、resource 与 session 消息。
- 使 agent 与 LLM 之间能够进行动态工具执行与上下文丰富的交互。

### 9. 调试 Web Demo（[examples/debugserver](examples/debugserver)）

- 启动一个 **debug Server**，提供与 ADK 兼容的 HTTP endpoint。
- 前端：[google/adk-web](https://github.com/google/adk-web) 通过 `/run_sse` 连接，并实时流式展示 agent 的响应。
- 是搭建你自定义聊天 UI 的优秀起点。

其他值得关注的示例：

- [examples/humaninloop](examples/humaninloop) – Human-in-the-loop。
- [examples/codeexecution](examples/codeexecution) – 代码执行。

关于使用详情，请参阅各示例文件夹中的 `README.md`。

## 架构概览

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

关键包：

| Package     | 职责                                                                                                         |
| ----------- | ------------------------------------------------------------------------------------------------------------ |
| `agent`     | 核心执行单元，负责处理用户输入并生成响应。                                                                    |
| `runner`    | agent 执行器，负责管理执行流程并连接 Session/Memory Service 能力。                                           |
| `model`     | 支持多种 LLM 模型（OpenAI、DeepSeek 等）。                                                                     |
| `tool`      | 提供多种工具能力（Function、MCP、DuckDuckGo 等）。                                                            |
| `session`   | 管理用户会话状态与事件。                                                                                      |
| `memory`    | 记录用户长期记忆与个性化信息。                                                                                |
| `knowledge` | 实现 RAG 知识检索能力。                                                                                       |
| `planner`   | 提供 agent 的规划与推理能力。                                                                                 |

## 使用内置 Agents

对于大多数应用，你**不需要**自己实现 `agent.Agent` 接口。框架已经提供了若干可直接使用的 agent，你可以像搭积木一样组合：

| Agent           | 目的                                                |
| --------------- | --------------------------------------------------- |
| `LLMAgent`      | 将 LLM chat-completion 模型封装为一个 agent。      |
| `ChainAgent`    | 依次顺序执行子 agent。                              |
| `ParallelAgent` | 并发执行子 agent 并合并输出。                      |
| `CycleAgent`    | 围绕 planner + executor 循环，直到收到停止信号。   |

### 多 Agent 协作示例

```go
// 1. 创建一个基础的 LLM agent。
base := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
)

// 2. 创建第二个具有不同指令的 LLM agent。
translator := llmagent.New(
    "translator",
    llmagent.WithInstruction("Translate everything to French"),
    llmagent.WithModel(openai.New("gpt-3.5-turbo")),
)

// 3. 将它们组合成一个 chain。
pipeline := chainagent.New(
    "pipeline",
    chainagent.WithSubAgents([]agent.Agent{base, translator}),
)

// 4. 通过 runner 运行以获得会话与 telemetry。
run := runner.NewRunner("demo-app", pipeline)
events, _ := run.Run(ctx, "user-1", "sess-1",
    model.NewUserMessage("Hello!"))
for ev := range events { /* ... */ }
```

组合式 API 允许你将 chain、cycle 或 parallel 进行嵌套，从而在无需底层管线处理的情况下构建复杂工作流。

## 未来规划

- 持久化 memory 适配器（PostgreSQL、Redis）。
- 更多内置工具（web 搜索、计算器、文件 I/O）。
- 高级规划器（tree-of-thought、graph 执行）。
- 提供 gRPC 与 HTTP 服务器以进行远程 agent 调用。
- 完整的 benchmark 与测试套件。

## 贡献

欢迎提交 Pull Request、Issue 和建议！请阅读
[CONTRIBUTING.md](CONTRIBUTING.md) 并遵循 Go 编码约定。提交前请运行
`go test ./... && go vet ./...`。

## 致谢

感谢来自腾讯内部业务（如腾讯元宝、腾讯视频、腾讯新闻、IMA、QQ 音乐等）的支持。业务场景的打磨是对该框架最好的验证。

同时感谢优秀的开源框架，如 ADK、Agno、CrewAI、AutoGen 等，它们为 tRPC-Agent-Go 的发展提供了灵感与借鉴。

本项目遵循 Apache 2.0 许可证。


