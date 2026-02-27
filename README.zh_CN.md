[English](README.md) | 中文

# tRPC-Agent-Go

[![Go Reference](https://pkg.go.dev/badge/trpc.group/trpc-go/trpc-agent-go.svg)](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/trpc-group/trpc-agent-go)](https://goreportcard.com/report/github.com/trpc-group/trpc-agent-go)
[![LICENSE](https://img.shields.io/badge/license-Apache--2.0-green.svg)](https://github.com/trpc-group/trpc-agent-go/blob/main/LICENSE)
[![Releases](https://img.shields.io/github/release/trpc-group/trpc-agent-go.svg?style=flat-square)](https://github.com/trpc-group/trpc-agent-go/releases)
[![Tests](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml/badge.svg)](https://github.com/trpc-group/trpc-agent-go/actions/workflows/prc.yml)
[![Coverage](https://codecov.io/gh/trpc-group/trpc-agent-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/trpc-group/trpc-agent-go/tree/main)
[![Documentation](https://img.shields.io/badge/Docs-Website-blue.svg)](https://trpc-group.github.io/trpc-agent-go/)

**一个用于构建智能 agent 系统的强大 Go 框架**，彻底改变您创建 AI 应用的方式。构建能够思考、记忆、协作和行动的自主 agent，前所未有地简单。

**为什么选择 tRPC-Agent-Go？**

- **智能推理**：先进的分层 planner 和多 agent 编排
- **丰富的 Tool 生态系统**：与外部 API、数据库和服务的无缝集成
- **持久化 Memory**：长期状态管理和上下文感知
- **多 Agent 协作**：Chain、Parallel 和基于 Graph 的 agent 工作流
- **GraphAgent**：类型安全的图工作流，支持多条件路由，功能对标 LangGraph 的 Go 实现
- **Agent Skills**：可复用的 `SKILL.md` 工作流，支持安全执行
- **Artifacts**：对 agent/tool 产出的文件进行版本化存储
- **评测与基准**：EvalSet + Metric 用于长期质量度量
- **UI 与服务集成**：AG-UI（Agent-User Interaction），
  以及 Agent-to-Agent（A2A）互通
- **生产就绪**：内置 telemetry、tracing 和企业级可靠性
- **高性能**：针对可扩展性和低延迟进行优化

## 使用场景

**非常适合构建：**

- **客户支持机器人** - 理解上下文并解决复杂查询的智能 agent
- **数据分析助手** - 查询数据库、生成报告并提供洞察的 agent
- **DevOps 自动化** - 智能部署、监控和事件响应系统
- **业务流程自动化** - 具有 human-in-the-loop 能力的多步骤工作流
- **研究与知识管理** - 基于 RAG 的文档分析和问答 agent

## 核心特性

<table>
<tr>
<td width="50%" valign="top">

### 多 Agent 编排

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
<td width="50%" valign="top">

### 先进的 Memory 系统

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
<td valign="top">

### 丰富的 Tool 集成

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
<td valign="top">

### 生产可观测性

```go
// 启动 Langfuse 集成
clean, _ := langfuse.Start(ctx)
defer clean(ctx)

runner := runner.NewRunner("app", agent)
// 运行并添加 Langfuse 属性
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
// Skills 是一个包含 SKILL.md 的文件夹。
repo, _ := skill.NewFSRepository("./skills")

// 让 agent 按需加载并执行 skills。
tools := []tool.Tool{
    skilltool.NewLoadTool(repo),
    skilltool.NewRunTool(repo, localexec.New()),
}
```

`NewFSRepository` 也支持传入 HTTP(S) URL（例如 `.zip` / `.tar.gz` 压缩包），
会自动下载并缓存到本地（可通过 `SKILLS_CACHE_DIR` 覆盖缓存目录）。

</td>
<td valign="top">

### 评测与基准

```go
evaluator, _ := evaluation.New("app", runner, evaluation.WithNumRuns(3))
defer evaluator.Close()
result, _ := evaluator.Evaluate(ctx, "math-basic")
_ = result.OverallStatus
```

</td>
</tr>
</table>

## 目录

- [tRPC-Agent-Go](#trpc-agent-go)
  - [使用场景](#使用场景)
  - [核心特性](#核心特性)
    - [**多 Agent 编排**](#多-agent-编排)
    - [**先进的 Memory 系统**](#先进的-memory-系统)
    - [**丰富的 Tool 集成**](#丰富的-tool-集成)
    - [**生产可观测性**](#生产可观测性)
    - [**Agent Skills**](#agent-skills)
    - [**评测与基准**](#评测与基准)
  - [目录](#目录)
  - [文档](#文档)
  - [快速开始](#快速开始)
    - [前置条件](#前置条件)
    - [运行示例](#运行示例)
    - [基本用法](#基本用法)
    - [中断 / 取消一次运行](#中断--取消一次运行)
  - [示例](#示例)
    - [1. Tool 用法](#1-tool-用法)
    - [2. 仅 LLM 的 Agent](#2-仅-llm-的-agent)
    - [3. 多 Agent Runner](#3-多-agent-runner)
    - [4. Graph Agent](#4-graph-agent)
    - [5. Memory](#5-memory)
    - [6. Knowledge](#6-knowledge)
    - [7. Telemetry 与 Tracing](#7-telemetry-与-tracing)
    - [8. MCP 集成](#8-mcp-集成)
    - [9. AG-UI Demo](#9-ag-ui-demo)
    - [10. 评测（Evaluation）](#10-评测evaluation)
    - [11. Agent Skills](#11-agent-skills)
    - [12. Artifacts](#12-artifacts)
    - [13. A2A 互通](#13-a2a-互通)
    - [14. Gateway 服务](#14-gateway-服务)
  - [架构概览](#架构概览)
    - [**执行流程**](#执行流程)
  - [使用内置 Agents](#使用内置-agents)
    - [多 Agent 协作示例](#多-agent-协作示例)
  - [贡献](#贡献)
    - [**贡献方式**](#贡献方式)
    - [**快速贡献设置**](#快速贡献设置)
  - [致谢](#致谢)
    - [**企业验证**](#企业验证)
    - [**开源灵感**](#开源灵感)
  - [Star 历史](#star-历史)
  - [许可证](#许可证)
    - [**在 GitHub 上为我们加星** • **报告问题** • **加入讨论**](#在-github-上为我们加星--报告问题--加入讨论)

## 文档

准备好深入了解 tRPC-Agent-Go 了吗？我们的[文档](https://trpc-group.github.io/trpc-agent-go/)涵盖从基础概念到高级技巧的一切，帮助你自信地构建强大的 AI 应用。无论你是 AI agent 新手还是有经验的开发者，都能在其中找到详细指南、实用示例和最佳实践，加速你的开发旅程。

## 快速开始

> **实际演示**：_[Demo GIF 占位符 - 展示 agent 推理和 tool 使用]_

### 前置条件

- Go 1.21 或更高版本
- LLM 提供商 API 密钥（OpenAI、DeepSeek 等）
- 5 分钟构建您的第一个智能 agent

### 运行示例

**3 个简单步骤开始：**

```bash
# 1. 克隆和设置
git clone https://github.com/trpc-group/trpc-agent-go.git
cd trpc-agent-go

# 2. 配置您的 LLM
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="your-base-url-here"  # 可选

# 3. 运行您的第一个 agent！
cd examples/runner
go run . -model="gpt-4o-mini" -streaming=true
```

**您将看到：**

- **与您的 AI agent 互动聊天**
- **实时流式**响应
- **Tool 使用**（计算器 + 时间工具）
- **带 memory 的多轮对话**

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

### 每次请求动态创建 Agent

有些场景下，你希望 Agent **按请求创建**（例如：不同的提示词、模型、工具集、沙箱实例）。
这时可以让 Runner 在每次 `Run(...)` 时动态构建一个新的 Agent：

```go
r := runner.NewRunnerWithAgentFactory(
    "my-app",
    "assistant",
    func(ctx context.Context, ro agent.RunOptions) (agent.Agent, error) {
        // 通过 ro 构建本次请求使用的 Agent。
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

### 中断 / 取消一次运行

如果你希望“中断正在运行的 agent”（停止本次模型调用 / 工具调用），推荐做法是：
**取消你传给 `Runner.Run` 的 `context.Context`**。

特别注意：**不要**只是在消费事件的 `for range` 里 `break` 然后直接返回。
如果你不再读取事件通道，但 agent 还在后台写事件，可能会阻塞并造成 goroutine
泄漏。正确姿势是：先 cancel，再把事件通道读到关闭为止。

#### 方式 A：Ctrl+C（命令行程序）

把 Ctrl+C 转成 ctx cancel：

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

events, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    return err
}
for range events {
    // 一直读到通道关闭：要么 ctx 被取消，要么 run 正常结束。
}
```

#### 方式 B：代码里主动 cancel

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
    // 一直读到通道关闭。
}
```

#### 方式 C：按 `requestID` 取消（适合服务端 / 后台任务）

```go
requestID := "req-123"
events, err := r.Run(ctx, userID, sessionID, message,
    agent.WithRequestID(requestID),
)

mr := r.(runner.ManagedRunner)
_ = mr.Cancel(requestID)
```

更完整的说明（包含 detached cancel、resume、以及 AG-UI 的取消路由）见
`docs/mkdocs/zh/runner.md` 与 `docs/mkdocs/zh/agui.md`。

## 示例

`examples` 目录包含涵盖各主要功能的可运行 Demo。

### 1. Tool 用法

- [examples/agenttool](examples/agenttool) – 将 agent 封装为可调用的 tool。
- [examples/multitools](examples/multitools) – 多工具编排。
- [examples/duckduckgo](examples/duckduckgo) – Web 搜索工具集成。
- [examples/filetoolset](examples/filetoolset) – 文件操作作为工具。
- [examples/fileinput](examples/fileinput) – 以文件作为输入。
- [examples/agenttool](examples/agenttool) 展示了流式与非流式模式。

### 2. 仅 LLM 的 Agent

示例：[examples/llmagent](examples/llmagent)

- 将任意 chat-completion 模型封装为 `LLMAgent`。
- 配置 system 指令、temperature、max tokens 等。
- 在模型流式输出时接收增量 `event.Event` 更新。

### 3. 多 Agent Runner

示例：[examples/multiagent](examples/multiagent)

- **ChainAgent** – 子 agent 的线性流水线。
- **ParallelAgent** – 并发执行子 agent 并合并结果。
- **CycleAgent** – 迭代执行直到满足终止条件。

### 4. Graph Agent

示例：[examples/graph](examples/graph)

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

### 5. Memory

示例：[examples/memory](examples/memory)

- 提供内存与 Redis memory 服务，包含 CRUD、搜索与 tool 集成。
- 如何进行配置、调用工具以及自定义 prompts。

### 6. Knowledge

示例：[examples/knowledge](examples/knowledge)

- 基础 RAG 示例：加载数据源、向量化到 vector store，并进行搜索。
- 如何使用对话上下文以及调节加载/并发选项。

### 7. Telemetry 与 Tracing

示例：[examples/telemetry](examples/telemetry)

- 在 model、tool 与 runner 层面的 OpenTelemetry hooks。
- 将 traces 导出到 OTLP endpoint 进行实时分析。

### 8. MCP 集成

示例：[examples/mcptool](examples/mcptool)

- 围绕 **trpc-mcp-go** 的封装工具，这是 **Model Context Protocol (MCP)** 的一个实现。
- 提供遵循 MCP 规范的 structured prompts、tool 调用、resource 与 session 消息。
- 使 agent 与 LLM 之间能够进行动态工具执行与上下文丰富的交互。

### 9. AG-UI Demo

示例：[examples/agui](examples/agui)

- 通过 AG-UI（Agent-User Interaction）协议对外暴露 Runner。
- 默认提供 Server-Sent Events（SSE）服务端实现，并提供客户端示例（例如 CopilotKit、TDesign Chat）。

### 10. 评测（Evaluation）

示例：[examples/evaluation](examples/evaluation)

- 通过可复用的 EvalSet 与可插拔的 Metric 对 agent 进行评测。
- 包含本地文件（local）与内存（inmemory）两种模式。

### 11. Agent Skills

示例：[examples/skillrun](examples/skillrun)

- Skill 是一个包含 `SKILL.md` 规范的文件夹，可附带 docs/scripts。
- 内置工具：`skill_load`、`skill_list_docs`、`skill_select_docs`、`skill_run`（在隔离工作空间里执行命令）。
- 建议 `skill_run` 尽量只用于执行所选 Skill 文档里要求的命令，而不是用于通用的 Shell 探查。

### 12. Artifacts

示例：[examples/artifact](examples/artifact)

- 保存并读取工具产出的版本化文件（图片、文本、报告等）。
- 支持多种后端（in-memory、S3、COS）。

### 13. A2A 互通

示例：[examples/a2aadk](examples/a2aadk)

- Agent-to-Agent（A2A）与 ADK Python A2A Server 的互通示例。
- 演示跨运行时的流式输出、工具调用与代码执行。

### 14. Gateway 服务

示例：[openclaw](openclaw)

- 一个最小的 OpenClaw-like gateway 服务。
- 稳定的 session id，以及同一 session 串行执行。
- 基础安全控制：allowlist + mention gating。
- OpenClaw-like demo binary（Telegram + gateway）：[openclaw](openclaw)

其他值得关注的示例：

- [examples/humaninloop](examples/humaninloop) – Human-in-the-loop。
- [examples/codeexecution](examples/codeexecution) – 代码执行。

关于使用详情，请参阅各示例文件夹中的 `README.md`。

## 架构概览

架构图

![architecture](docs/mkdocs/assets/img/component_architecture.svg)

### **执行流程**

1. **Runner** 通过会话管理编排整个执行管道
2. **Agent** 使用多个专门组件处理请求
3. **Planner** 确定最优策略和 tool 选择
4. **Tools** 执行特定任务（API 调用、计算、web 搜索）
5. **Memory** 维护上下文并从交互中学习
6. **Knowledge** 为文档理解提供 RAG 能力

关键包：

| Package     | 职责                                                               |
| ----------- | ------------------------------------------------------------------ |
| `agent`     | 核心执行单元，负责处理用户输入并生成响应。                                |
| `runner`    | agent 执行器，负责管理执行流程并连接 Session/Memory Service 能力。       |
| `model`     | 支持多种 LLM 模型（OpenAI、DeepSeek 等）。                             |
| `tool`      | 提供多种工具能力（Function、MCP、DuckDuckGo 等）。                      |
| `session`   | 管理用户会话状态与事件。                                              |
| `memory`    | 记录用户长期记忆与个性化信息。                                         |
| `knowledge` | 实现 RAG 知识检索能力。                                              |
| `planner`   | 提供 agent 的规划与推理能力。                                         |
| `artifact`  | 存储并读取工具/agent 产出的版本化文件（图片、报告等）。                    |
| `skill`     | 管理并执行以 `SKILL.md` 定义的可复用 Agent Skills。                   |
| `event`     | 定义 Runner 与各类服务使用的事件结构与流式载荷。                        |
| `evaluation`| 提供 EvalSet/Metric 驱动的评测框架并管理评测结果。                     |
| `server`    | 提供 Gateway、AG-UI、A2A 等 HTTP 服务端能力。                       |
| `telemetry` | OpenTelemetry 的 tracing/metrics 采集与接入。                      |

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

## 贡献

我们热爱贡献！加入我们不断壮大的开发者社区，共同构建 AI agent 的未来。

### **贡献方式**

- **报告 bug** 或通过 [Issues](https://github.com/trpc-group/trpc-agent-go/issues) 建议新功能
- **改进文档** - 帮助他人更快学习
- **提交 PR** - bug 修复、新功能或示例
- **分享您的用例** - 用您的 agent 应用启发他人

### **快速贡献设置**

```bash
# Fork 并克隆仓库
git clone https://github.com/YOUR_USERNAME/trpc-agent-go.git
cd trpc-agent-go

# 运行测试确保一切正常
go test ./...
go vet ./...

# 进行您的更改并提交 PR！
```

**请阅读** [CONTRIBUTING.md](CONTRIBUTING.md) 了解详细指南和编码标准。

## 致谢

### **企业验证**

特别感谢腾讯各业务单元，包括**腾讯元宝**、**腾讯视频**、**腾讯新闻**、**IMA** 和 **QQ 音乐**的宝贵支持和生产环境验证推动框架发展

### **开源灵感**

感谢优秀的开源框架如 **ADK**、**Agno**、**CrewAI**、**AutoGen** 等的启发。站在巨人的肩膀上！

---

## Star 历史

[![Star History Chart](https://api.star-history.com/svg?repos=trpc-group/trpc-agent-go&type=Date)](https://star-history.com/#trpc-group/trpc-agent-go&Date)

---

## 许可证

遵循 **Apache 2.0 许可证** - 详见 [LICENSE](LICENSE) 文件。

---

<div align="center">

### **在 GitHub 上为我们加星** • **报告问题** • **加入讨论**

**由 tRPC-Agent-Go 团队用爱构建**

_赋能开发者构建下一代智能应用_

</div>
