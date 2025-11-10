[English](README.md) | 中文

# tRPC-Agent-Go

[![Go Reference](https://pkg.go.dev/badge/trpc.group/trpc-go/trpc-agent-go.svg)](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/trpc-group/trpc-agent-go)](https://goreportcard.com/report/github.com/trpc-group/trpc-agent-go)
[![LICENSE](https://img.shields.io/badge/license-Apache--2.0-green.svg)](https://github.com/trpc-group/trpc-agent-go/blob/main/LICENSE)
[![Releases](https://img.shields.io/github/release/trpc-group/trpc-agent-go.svg?style=flat-square)](https://github.com/trpc-group/trpc-agent-go/releases)
[![Tests](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml/badge.svg)](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml)
[![Coverage](https://codecov.io/gh/trpc-group/trpc-agent-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/trpc-group/trpc-agent-go/tree/main)
[![Documentation](https://img.shields.io/badge/Docs-Website-blue.svg)](https://trpc-group.github.io/trpc-agent-go/)

🚀 **一个用于构建智能 agent 系统的强大 Go 框架**，彻底改变您创建 AI 应用的方式。构建能够思考、记忆、协作和行动的自主 agent，前所未有地简单。

✨ **为什么选择 tRPC-Agent-Go？**

- 🧠 **智能推理**：先进的分层 planner 和多 agent 编排
- 🧰 **丰富的 Tool 生态系统**：与外部 API、数据库和服务的无缝集成
- 💾 **持久化 Memory**：长期状态管理和上下文感知
- 🔗 **多 Agent 协作**：Chain、Parallel 和基于 Graph 的 agent 工作流
- 📊 **生产就绪**：内置 telemetry、tracing 和企业级可靠性
- ⚡ **高性能**：针对可扩展性和低延迟进行优化

## 🎯 使用场景

**非常适合构建：**

- 🤖 **客户支持机器人** - 理解上下文并解决复杂查询的智能 agent
- 📊 **数据分析助手** - 查询数据库、生成报告并提供洞察的 agent
- 🔧 **DevOps 自动化** - 智能部署、监控和事件响应系统
- 💼 **业务流程自动化** - 具有 human-in-the-loop 能力的多步骤工作流
- 🧠 **研究与知识管理** - 基于 RAG 的文档分析和问答 agent

## 🚀 核心特性

<table>
<tr>
<td width="50%">

### 🎪 **多 Agent 编排**

```go
// Chain agent 构建复杂工作流
pipeline := chainagent.New("pipeline",
    chainagent.WithSubAgents([]agent.Agent{
        analyzer, processor, reporter,
    }))

// 或者并行运行
parallel := parallelagent.New("concurrent",
    parallelagent.WithSubAgents(tasks))
```

</td>
<td width="50%">

### 🧠 **先进的 Memory 系统**

```go
// 带搜索的持久化 memory
memory := memorysvc.NewInMemoryService()
agent := llmagent.New("assistant",
    llmagent.WithTools(memory.Tools()),
    llmagent.WithModel(model))

// Memory service 在 runner 层管理
runner := runner.NewRunner("app", agent,
    runner.WithMemoryService(memory))

// Agent 在会话间记住上下文
```

</td>
</tr>
<tr>
<td>

### 🛠️ **丰富的 Tool 集成**

```go
// 任何函数都可以成为 tool
calculator := function.NewFunctionTool(
    calculate,
    function.WithName("calculator"),
    function.WithDescription("数学运算"))

// MCP 协议支持
mcpTool := mcptool.New(serverConn)
```

</td>
<td>

### 📈 **生产监控**

```go
// OpenTelemetry 集成
runner := runner.NewRunner("app", agent,
    runner.WithTelemetry(telemetry.Config{
        TracingEnabled: true,
        MetricsEnabled: true,
    }))
```

</td>
</tr>
</table>

## 目录

- [使用场景](#-使用场景)
- [核心特性](#-核心特性)
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

## ⚡ 快速开始

> 🎬 **实际演示**：_[Demo GIF 占位符 - 展示 agent 推理和 tool 使用]_

### 📋 前置条件

- ✅ Go 1.21 或更高版本
- 🔑 LLM 提供商 API 密钥（OpenAI、DeepSeek 等）
- 💡 5 分钟构建您的第一个智能 agent

### 🚀 运行示例

**3 个简单步骤开始：**

```bash
# 1️⃣ 克隆和设置
git clone https://github.com/trpc-group/trpc-agent-go.git
cd trpc-agent-go

# 2️⃣ 配置您的 LLM
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="your-base-url-here"  # 可选

# 3️⃣ 运行您的第一个 agent！🎉
cd examples/runner
go run . -model="gpt-4o-mini" -streaming=true
```

**您将看到：**

- 💬 **与您的 AI agent 互动聊天**
- ⚡ **实时流式**响应
- 🧮 **Tool 使用**（计算器 + 时间工具）
- 🔄 **带 memory 的多轮对话**

试着问问："现在几点了？然后计算 15 \* 23 + 100"

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

- 多条件扇出路由（一次返回多个分支并行执行）：

```go
// 返回多个分支键，分别触发目标节点并行执行。
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
    map[string]string{"goA": "A", "goB": "B"}, // 可用 PathMap 或 Ends
)
sg.SetFinishPoint("A").SetFinishPoint("B")
```

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

## 🏗️ 架构概览

架构图

![architecture](docs/mkdocs/assets/img/component_architecture.png)

### 🔄 **执行流程**

1. **🚀 Runner** 通过会话管理编排整个执行管道
2. **🤖 Agent** 使用多个专门组件处理请求
3. **🧠 Planner** 确定最优策略和 tool 选择
4. **🛠️ Tools** 执行特定任务（API 调用、计算、web 搜索）
5. **💾 Memory** 维护上下文并从交互中学习
6. **📚 Knowledge** 为文档理解提供 RAG 能力

关键包：

| Package     | 职责                                                               |
| ----------- | ------------------------------------------------------------------ |
| `agent`     | 核心执行单元，负责处理用户输入并生成响应。                         |
| `runner`    | agent 执行器，负责管理执行流程并连接 Session/Memory Service 能力。 |
| `model`     | 支持多种 LLM 模型（OpenAI、DeepSeek 等）。                         |
| `tool`      | 提供多种工具能力（Function、MCP、DuckDuckGo 等）。                 |
| `session`   | 管理用户会话状态与事件。                                           |
| `memory`    | 记录用户长期记忆与个性化信息。                                     |
| `knowledge` | 实现 RAG 知识检索能力。                                            |
| `planner`   | 提供 agent 的规划与推理能力。                                      |

- 时序图

![execution](docs/mkdocs/assets/img/timing_diagram.png)

## 使用内置 Agents

对于大多数应用，你**不需要**自己实现 `agent.Agent` 接口。框架已经提供了若干可直接使用的 agent，你可以像搭积木一样组合：

| Agent           | 目的                                             |
| --------------- | ------------------------------------------------ |
| `LLMAgent`      | 将 LLM chat-completion 模型封装为一个 agent。    |
| `ChainAgent`    | 依次顺序执行子 agent。                           |
| `ParallelAgent` | 并发执行子 agent 并合并输出。                    |
| `CycleAgent`    | 围绕 planner + executor 循环，直到收到停止信号。 |

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

## 🤝 贡献

我们 ❤️ 贡献！加入我们不断壮大的开发者社区，共同构建 AI agent 的未来。

### 🌟 **贡献方式**

- 🐛 **报告 bug** 或通过 [Issues](https://github.com/trpc-group/trpc-agent-go/issues) 建议新功能
- 📖 **改进文档** - 帮助他人更快学习
- 🔧 **提交 PR** - bug 修复、新功能或示例
- 💡 **分享您的用例** - 用您的 agent 应用启发他人

### 🚀 **快速贡献设置**

```bash
# Fork 并克隆仓库
git clone https://github.com/YOUR_USERNAME/trpc-agent-go.git
cd trpc-agent-go

# 运行测试确保一切正常
go test ./...
go vet ./...

# 进行您的更改并提交 PR！🎉
```

📋 **请阅读** [CONTRIBUTING.md](CONTRIBUTING.md) 了解详细指南和编码标准。

## 🏆 致谢

### 🏢 **企业验证**

特别感谢腾讯各业务单元，包括**腾讯元宝**、**腾讯视频**、**腾讯新闻**、**IMA** 和 **QQ 音乐**的宝贵支持和生产环境验证推动框架发展

### 🌟 **开源灵感**

感谢优秀的开源框架如 **ADK**、**Agno**、**CrewAI**、**AutoGen** 等的启发。站在巨人的肩膀上！🙏

---

## ⭐ Star 历史

[![Star History Chart](https://api.star-history.com/svg?repos=trpc-group/trpc-agent-go&type=Date)](https://star-history.com/#trpc-group/trpc-agent-go&Date)

---

## 📜 许可证

遵循 **Apache 2.0 许可证** - 详见 [LICENSE](LICENSE) 文件。

---

<div align="center">

### 🌟 **在 GitHub 上为我们加星** • 🐛 **报告问题** • 💬 **加入讨论**

**由 tRPC-Agent-Go 团队用 ❤️ 构建**

_赋能开发者构建下一代智能应用_

</div>
