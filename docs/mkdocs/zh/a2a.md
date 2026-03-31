# tRPC-Agent-Go A2A 集成指南

## 概述

tRPC-Agent-Go 提供了完整的 A2A (Agent-to-Agent) 解决方案，包含两个核心组件：

- **A2A Server**: 将本地 Agent 暴露为 A2A 服务，供其他 Agent 调用
- **A2A Agent**: 调用远程 A2A 服务的客户端代理，像使用本地 Agent 一样使用远程 Agent

### 核心能力

- **零协议感知**: 开发者只需关注 Agent 的业务逻辑，无需了解 A2A 协议细节
- **自动适配**: 框架自动将 Agent 信息转换为 A2A AgentCard
- **消息转换**: 自动处理 A2A 协议消息与 Agent 消息格式的转换

## A2A Server：暴露 Agent 为服务

### 概念介绍

A2A Server 是 tRPC-Agent-Go 提供的服务端组件，用于将任何本地 Agent 快速转换为符合 A2A 协议的网络服务。

### 核心特性

- **一键转换**: 通过简单配置将 Agent 暴露为 A2A 服务
- **自动协议适配**: 自动处理 A2A 协议与 Agent 接口的转换
- **AgentCard 生成**: 自动生成服务发现所需的 AgentCard
- **流式支持**: 支持流式和非流式两种响应模式

### Agent 到 A2A 的自动转换

tRPC-Agent-Go 通过 `server/a2a` 包实现了从 Agent 到 A2A 服务的无缝转换：

```go
func New(opts ...Option) (*a2a.A2AServer, error) {}
```

### AgentCard 自动生成

框架会自动提取 Agent 的元数据（名称、描述、工具等），生成符合 A2A 协议的 AgentCard，包括：
- Agent 基本信息（名称、描述、URL）
- 能力声明（是否支持流式）
- 技能列表（基于 Agent 的工具自动生成）

这里要特别区分两层语义：

- `WithAgent(agent, streaming)` 里的 `streaming`，本质上是用来声明生成出来的 `AgentCard.Capabilities.Streaming`。
- 它不是 `runner` 的执行开关，也不是服务端内部消息处理链路的总开关。
- 如果你走的是 `WithRunner(...) + WithAgentCard(...)` 模式，那么是否支持流式应该直接写在 `AgentCard` 上，例如通过 `NewAgentCard(..., streaming)` 来设置。
- `WithRunner(...) + WithAgentCard(...)` 模式下，`skills` 由调用方自己维护；`NewAgentCard(...)` 只帮你生成默认结构和默认 skill，不会自动从自定义 `runner` 里推导完整工具列表。

### 消息协议转换

框架内置 `messageProcessor` 实现 A2A 协议消息与 Agent 消息格式的双向转换，用户无需关心消息格式转换的细节。

## A2A Server 快速开始

### 使用 A2A Server 暴露 Agent 服务

只需几行代码，就可以将任意 Agent 转换为 A2A 服务：

#### 基础示例：创建 A2A Server

```go
package main

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

func main() {
	// 1. 创建一个普通的 Agent
	model := openai.New("gpt-4o-mini")
	agent := llmagent.New("MyAgent",
		llmagent.WithModel(model),
		llmagent.WithDescription("一个智能助手"),
	)

	// 2. 一键转换为 A2A 服务
	server, _ := a2aserver.New(
		a2aserver.WithHost("localhost:8080"),
		a2aserver.WithAgent(agent, true), // 开启 streaming
	)

	// 3. 启动服务，即可接受 A2A 请求
	server.Start(":8080")
}
```

#### 流式输出事件类型（Message vs Artifact）

当开启 streaming 时，A2A 允许服务端用不同的方式下发增量输出：

- **TaskArtifactUpdateEvent（默认）**：ADK 风格。把增量内容作为任务的
  artifact 更新事件（`artifact-update`）下发。
- **Message**：轻量模式。把增量内容作为 `message` 下发，客户端可以直接渲染
  `Message.parts`，无需把输出当作“可持久化 artifact”来处理。

如果你的业务更希望把流式内容直接当作 `message` 来消费，可以这样配置：

```go
server, _ := a2aserver.New(
	a2aserver.WithHost("localhost:8080"),
	a2aserver.WithAgent(agent, true),
	a2aserver.WithStreamingEventType(
		a2aserver.StreamingEventTypeMessage,
	),
)
```

任务状态更新（`submitted`、`completed`）仍然会以 `TaskStatusUpdateEvent` 的形式
下发。如果开启 `WithStructuredTaskErrors(true)`，终态失败也会通过 failed
`TaskStatusUpdateEvent` 下发：机器可读字段优先位于外层 metadata，并为了 `0.1`
兼容继续镜像到 `status.message.metadata`，展示文本位于
`status.message.parts`。

#### 直接使用 A2A 协议客户端调用

```go
import (
	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func main() {
	// 连接到 A2A 服务
	client, _ := client.NewA2AClient("http://localhost:8080/")

	// 发送消息给 Agent
	message := protocol.NewMessage(
		protocol.MessageRoleUser,
		[]protocol.Part{protocol.NewTextPart("你好，请帮我分析这段代码")},
	)

	// Agent 会自动处理并返回结果
	response, _ := client.SendMessage(context.Background(),
		protocol.SendMessageParams{Message: message})
}
```

### 高级配置

#### 自定义 Runner（WithRunner）

默认情况下，A2A Server 会自动为你创建一个 Runner。如果你需要更精细地控制，例如注入 MemoryService、自定义 SessionService，可以使用 `WithRunner`。

注意：`WithRunner` 与 `WithAgent` 互斥。使用 `WithRunner` 时，需要显式通过 `WithAgentCard` 提供对外暴露的 Agent 身份：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessionmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

memoryService := inmemory.NewMemoryService()
sessionService := sessionmemory.NewSessionService()
streaming := true

r := runner.NewRunner(
	agent.Info().Name,
	agent,
	runner.WithSessionService(sessionService),
	runner.WithMemoryService(memoryService),
)

card, _ := a2a.NewAgentCard(agent.Info().Name, agent.Info().Description, "localhost:8080", streaming)

server, _ := a2a.New(
	a2a.WithRunner(r),
	a2a.WithAgentCard(card),
)
```

在这种 `runner-only` 模式下，流式能力不再通过 `WithAgent(...)` 传入，而是直接由 `WithAgentCard(...)` 提供的 `AgentCard.Capabilities.Streaming` 决定。

`examples/a2aagent` 里也提供了显式示例。使用 `-server-mode runner-card` 可以切换到 `WithRunner(...) + WithAgentCard(...)` 的建服方式：

```bash
cd examples/a2aagent
go run . -server-mode runner-card
```

如果你只想快速得到一张符合默认约定的基础卡片，推荐直接使用 `NewAgentCard(...)`，而不是手写 `Name`、`Description`、`Capabilities` 和默认 `Skill`。如果你的 `runner` 需要暴露更准确的 `skills`，请在业务侧自己补齐和维护。

#### 动态更新 AgentCard

如果你希望在运行时热更新对外暴露的 `AgentCard`，可以直接通过 `WithExtraA2AOptions(...)` 接入底层 `a2a.WithAgentCardHandler(...)`，并配合 `NewAgentCardHandler(...)` 输出当前快照：

```go
import (
	"sync"

	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
)

card, _ := a2a.NewAgentCard(agent.Info().Name, agent.Info().Description, "localhost:8080", true)
var (
	cardMu      sync.RWMutex
	currentCard = card
)

server, _ := a2a.New(
	a2a.WithRunner(runner.NewRunner(agent.Info().Name, agent)),
	a2a.WithAgentCard(currentCard),
	a2a.WithExtraA2AOptions(
		a2aprotocolserver.WithAgentCardHandler(
			a2a.NewAgentCardHandler(func() a2aprotocolserver.AgentCard {
				cardMu.RLock()
				defer cardMu.RUnlock()
				return currentCard
			}),
		),
	),
)

cardMu.Lock()
updated := currentCard
updated.Description = "new description"
currentCard = updated
cardMu.Unlock()
```

这种方式只会更新对外暴露的 metadata，不会影响底层 `runner`、`taskManager` 和消息处理链路。至于 `currentCard` 存在内存、配置中心还是数据库里，由业务自己决定。

同时建议把 `Name`、`URL` 这类启动期就参与路由、身份或发现语义的字段视为不可变字段；如果确实要改，优先重建 server，而不是只热更新 card endpoint。

#### 服务端消息处理 Hook（WithProcessMessageHook）

`WithProcessMessageHook` 允许你在 A2A Server 处理消息之前/之后插入自定义逻辑。它采用中间件模式，包装底层的 `MessageProcessor`：

```go
import "trpc.group/trpc-go/trpc-a2a-go/taskmanager"

// 自定义 Hook 处理器
type hookProcessor struct {
	next taskmanager.MessageProcessor
}

func (h *hookProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	// 在处理之前：读取客户端注入的自定义 metadata
	if traceID, ok := message.Metadata["trace_id"]; ok {
		fmt.Printf("received trace_id: %v\n", traceID)
	}
	// 委托给下一个处理器
	return h.next.ProcessMessage(ctx, message, options, handler)
}

server, _ := a2a.New(
	a2a.WithHost("localhost:8080"),
	a2a.WithAgent(agent, true),
	a2a.WithProcessMessageHook(
		func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
			return &hookProcessor{next: next}
		},
	),
)
```

**典型使用场景**：
- 读取客户端通过 `BuildMessageHook` 注入的自定义 metadata
- 在消息处理前后添加日志、监控、审计
- 修改或验证入站消息

#### 客户端消息构建 Hook（WithBuildMessageHook）

`WithBuildMessageHook` 是 A2AAgent（客户端）侧的 Hook，允许在将消息发送到远程 A2A Server 之前注入自定义数据。它同样采用中间件模式：

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"

a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	a2aagent.WithBuildMessageHook(
		func(next a2aagent.ConvertToA2AMessageFunc) a2aagent.ConvertToA2AMessageFunc {
			return func(isStream bool, agentName string, inv *agent.Invocation) (*protocol.Message, error) {
				// 调用默认转换器
				msg, err := next(isStream, agentName, inv)
				if err != nil {
					return nil, err
				}
				// 注入自定义 metadata
				if msg.Metadata == nil {
					msg.Metadata = make(map[string]any)
				}
				msg.Metadata["trace_id"] = "my-trace-123"
				msg.Metadata["business_tag"] = "order-service"
				return msg, nil
			}
		},
	),
)
```

**BuildMessageHook + ProcessMessageHook 联动**：

```text
┌──────────────────┐                    ┌───────────────────┐
│    A2AAgent      │   A2A protocol     │    A2A Server     │
│                  │                    │                   │
│ BuildMessageHook │── metadata ──────→ │ProcessMessageHook │
│ (inject data)    │                    │ (read data)       │
└──────────────────┘                    └───────────────────┘
```

客户端通过 `BuildMessageHook` 将自定义数据（如 trace_id、业务标签）注入到 A2A 消息的 `metadata` 字段中，服务端通过 `ProcessMessageHook` 读取并处理这些数据。

#### 追加 RunOption（WithRunOptions）

`WithRunOptions` 允许为 A2A Server 的每次 Agent 调用追加额外的 `RunOption`：

```go
server, _ := a2a.New(
	a2a.WithHost("localhost:8080"),
	a2a.WithAgent(agent, true),
	a2a.WithRunOptions(
		agent.WithRequestID("custom-req-id"),
	),
)
```

#### Graph 内部事件透传

从默认行为看，A2A Server 会过滤大部分 `graph.*` 运行时内部事件（例如
`graph.node.start`、`graph.node.complete`、`graph.pregel.*`、`graph.checkpoint.*`），
避免把执行细节全部暴露给下游。

默认仍会保留终态 `graph.execution`（以及普通消息/错误事件），用于恢复最终状态与
`state_delta`。

如果你需要做链路调试或节点级 trace，可以扩展 graph 事件白名单：

```go
server, _ := a2aserver.New(
	a2aserver.WithHost("localhost:8080"),
	a2aserver.WithAgent(agent, true),
	a2aserver.WithGraphEventObjectAllowlist(
		"graph.execution", // 保留终态事件
		"graph.node.*",    // 透传节点生命周期事件
	),
)
```

说明：

- 不设置该选项时，默认白名单是 `["graph.execution"]`。
- 如果显式调用 `WithGraphEventObjectAllowlist()` 且不传参数，
  那么所有 `graph.*` 事件都会被过滤（包括 `graph.execution`）。

建议仅在调试场景开启，生产环境默认关闭可以减少噪音与带宽开销。

### 在一个端口上暴露多个 A2A Agent（base path）

有时你希望 **一个服务（一个端口）** 同时对外提供多个 A2A Agent。
在 A2A 的惯用做法里，每个 Agent 都应该有自己的 **base URL**，客户端通过选择 URL
来选择要调用的 Agent，而不是额外传一个 `agent_name` 参数做路由。

在 tRPC-Agent-Go 中，`a2a.WithHost(...)` 支持带 path 的 URL。
当 host URL 包含 path（例如 `http://localhost:8888/agents/math`）时，
A2A server 会自动把这段 path 作为自己的 **base path** 用于路由。

核心思路：

- 每个 Agent 创建一个独立的 A2A server（每个 server 使用不同的 base path）
- 不要对每个 server 都调用 `Start`（否则会争抢同一个端口）
- 通过 `server.Handler()` 把多个 A2A server 挂到一个共享的 `http.Server` 上

示例：

```go
mathServer, err := a2a.New(
	a2a.WithHost("http://localhost:8888/agents/math"),
	a2a.WithAgent(mathAgent, false),
)
if err != nil {
	panic(err)
}

weatherServer, err := a2a.New(
	a2a.WithHost("http://localhost:8888/agents/weather"),
	a2a.WithAgent(weatherAgent, false),
)
if err != nil {
	panic(err)
}

mux := http.NewServeMux()
mux.Handle("/agents/math/", mathServer.Handler())
mux.Handle("/agents/weather/", weatherServer.Handler())

if err := http.ListenAndServe(":8888", mux); err != nil {
	panic(err)
}
```

服务启动后，每个 Agent 都有自己独立的 AgentCard：

- `http://localhost:8888/agents/math/.well-known/agent-card.json`
- `http://localhost:8888/agents/weather/.well-known/agent-card.json`

完整可运行示例：`examples/a2amultipath`。

## A2AAgent：调用远程 A2A 服务

与 A2A Server 相对应，tRPC-Agent-Go 还提供了 `A2AAgent`，用于调用远程的 A2A 服务，实现 Agent 间的通信。

### 概念介绍

`A2AAgent` 是一个特殊的 Agent 实现，它不直接处理用户请求，而是将请求转发给远程的 A2A 服务。从使用者角度看，`A2AAgent` 就像一个普通的 Agent，但实际上它是远程 Agent 的本地代理。

**简单理解**：
- **A2A Server**: 我有一个 Agent，想让别人调用 → 暴露为 A2A 服务
- **A2AAgent**: 我想调用别人的 Agent → 通过 A2AAgent 代理调用

### 核心特性

- **透明代理**: 像使用本地 Agent 一样使用远程 Agent
- **自动发现**: 通过 AgentCard 自动发现远程 Agent 的能力
- **协议转换**: 自动处理本地消息格式与 A2A 协议的转换
- **流式支持**: 支持流式和非流式两种通信模式
- **状态传递**: 支持将本地状态传递给远程 Agent
- **错误处理**: 完善的错误处理和重试机制

如果你想看 A2A server 和 `A2AAgent` 之间推荐的结构化 task error 约定，见
[Error Handling](error-handling.md)。

### 使用场景

1. **分布式 Agent 系统**: 在微服务架构中调用其他服务的 Agent
2. **Agent 编排**: 将多个专业 Agent 组合成复杂的工作流
3. **跨团队协作**: 调用其他团队提供的 Agent 服务

### A2AAgent 快速开始

#### 基本用法

```go
package main

import (
	"context"
	"fmt"
	
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// 1. 创建 A2AAgent，指向远程 A2A 服务
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL("http://localhost:8888"),
	)
	if err != nil {
		panic(err)
	}

	// 2. 像使用普通 Agent 一样使用
	sessionService := inmemory.NewSessionService()
	runner := runner.NewRunner("test", a2aAgent, 
		runner.WithSessionService(sessionService))

	// 3. 发送消息
	events, err := runner.Run(
		context.Background(),
		"user1",
		"session1", 
		model.NewUserMessage("请帮我讲个笑话"),
	)
	if err != nil {
		panic(err)
	}

	// 4. 处理响应
	for event := range events {
		if event.Response != nil && len(event.Response.Choices) > 0 {
			fmt.Print(event.Response.Choices[0].Message.Content)
		}
	}
}
```

在多 Agent 系统中，`A2AAgent` 通常作为本地协调者 Agent
（例如 `LLMAgent`）的 SubAgent 使用。你可以将 `A2AAgent`
与 `LLMAgent.SetSubAgents` 结合，从注册中心动态加载并更新
远程 SubAgent，而无需重建协调者实例。

#### 高级配置

```go
// 创建带有高级配置的 A2AAgent
a2aAgent, err := a2aagent.New(
	// 指定远程服务地址
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	
	// 设置流式缓冲区大小
	a2aagent.WithStreamingChannelBufSize(2048),

	// 自定义协议转换
	a2aagent.WithCustomEventConverter(customEventConverter),
	a2aagent.WithCustomA2AConverter(customA2AConverter),

	// 显式控制流式模式（覆盖 AgentCard 中的 capability 声明）
	a2aagent.WithEnableStreaming(true),
)
```

客户端是否发起流式请求，遵循下面的优先级：

1. 单次调用显式指定的 `agent.WithStream(...)`
2. `a2aagent.WithEnableStreaming(...)`
3. 远端 `AgentCard.Capabilities.Streaming`
4. 默认关闭

也就是说，服务端这边的 `streaming` 声明主要是告诉客户端“我是否支持流式 A2A 请求”；客户端再基于这份 capability 决定默认发送流式还是非流式请求。  
如果客户端显式指定了 `agent.WithStream(...)` 或 `a2aagent.WithEnableStreaming(...)`，就会覆盖 `AgentCard` 中的声明。

### 完整示例：A2A Server + A2AAgent 综合使用

以下是一个完整的示例，展示了如何在同一个程序中同时运行 A2A Server（暴露本地 Agent）和 A2AAgent（调用远程服务）：

```go
package main

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// 1. 创建并启动远程 Agent 服务
	remoteAgent := createRemoteAgent()
	startA2AServer(remoteAgent, "localhost:8888")
	
	time.Sleep(1 * time.Second) // 等待服务启动

	// 2. 创建 A2AAgent 连接到远程服务
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL("http://localhost:8888"),
		a2aagent.WithTransferStateKey("user_context"),
	)
	if err != nil {
		panic(err)
	}

	// 3. 创建本地 Agent
	localAgent := createLocalAgent()

	// 4. 对比本地和远程 Agent 的响应
	compareAgents(localAgent, a2aAgent)
}

func createRemoteAgent() agent.Agent {
	model := openai.New("gpt-4o-mini")
	return llmagent.New("JokeAgent",
		llmagent.WithModel(model),
		llmagent.WithDescription("I am a joke-telling agent"),
		llmagent.WithInstruction("Always respond with a funny joke"),
	)
}

func createLocalAgent() agent.Agent {
	model := openai.New("gpt-4o-mini") 
	return llmagent.New("LocalAgent",
		llmagent.WithModel(model),
		llmagent.WithDescription("I am a local assistant"),
	)
}

func startA2AServer(agent agent.Agent, host string) {
	server, err := a2a.New(
		a2a.WithHost(host),
		a2a.WithAgent(agent, true), // 启用流式
	)
	if err != nil {
		panic(err)
	}
	
	go func() {
		server.Start(host)
	}()
}

func compareAgents(localAgent, remoteAgent agent.Agent) {
	sessionService := inmemory.NewSessionService()
	
	localRunner := runner.NewRunner("local", localAgent,
		runner.WithSessionService(sessionService))
	remoteRunner := runner.NewRunner("remote", remoteAgent,
		runner.WithSessionService(sessionService))

	userMessage := "请帮我讲个笑话"
	
	// 调用本地 Agent
	fmt.Println("=== Local Agent Response ===")
	processAgent(localRunner, userMessage)
	
	// 调用远程 Agent (通过 A2AAgent)
	fmt.Println("\n=== Remote Agent Response (via A2AAgent) ===")
	processAgent(remoteRunner, userMessage)
}

func processAgent(runner runner.Runner, message string) {
	events, err := runner.Run(
		context.Background(),
		"user1",
		"session1",
		model.NewUserMessage(message),
		agent.WithRuntimeState(map[string]any{
			"user_context": "test_context",
		}),
	)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	for event := range events {
		if event.Response != nil && len(event.Response.Choices) > 0 {
			content := event.Response.Choices[0].Message.Content
			if content == "" {
				content = event.Response.Choices[0].Delta.Content
			}
			if content != "" {
				fmt.Print(content)
			}
		}
	}
	fmt.Println()
}
```

### AgentCard 自动发现

`A2AAgent` 支持通过标准的 AgentCard 发现机制自动获取远程 Agent 的信息：

```go
// A2AAgent 会自动从以下路径获取 AgentCard
// http://remote-agent:8888/.well-known/agent.json

type AgentCard struct {
    Name         string                 `json:"name"`
    Description  string                 `json:"description"`
    URL          string                 `json:"url"`
    Capabilities AgentCardCapabilities  `json:"capabilities"`
}

type AgentCardCapabilities struct {
    Streaming *bool `json:"streaming,omitempty"`
}
```

### 状态传递

`A2AAgent` 支持将本地运行时状态传递给远程 Agent：

```go
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	// 指定要传递的状态键
	a2aagent.WithTransferStateKey("user_id", "session_context", "preferences"),
)

// 运行时状态会通过 A2A 协议的 metadata 字段传递给远程 Agent
events, _ := runner.Run(ctx, userID, sessionID, message,
	agent.WithRuntimeState(map[string]any{
		"user_id":         "12345",
		"session_context": "shopping_cart",
		"preferences":     map[string]string{"language": "zh"},
	}),
)
```

### 自定义 HTTP Headers

你可以使用 `WithA2ARequestOptions` 为每次请求传递自定义的 HTTP headers：

```go
import "trpc.group/trpc-go/trpc-a2a-go/client"

events, err := runner.Run(
	context.Background(),
	userID,
	sessionID,
	model.NewUserMessage("你的问题"),
	// 为本次请求传递自定义 HTTP headers
	agent.WithA2ARequestOptions(
		client.WithRequestHeader("X-Custom-Header", "custom-value"),
		client.WithRequestHeader("X-Request-ID", fmt.Sprintf("req-%d", time.Now().UnixNano())),
		client.WithRequestHeader("Authorization", "Bearer your-token"),
	),
)
```

**常见使用场景：**

1. **身份认证**：传递认证 token
   ```go
   agent.WithA2ARequestOptions(
       client.WithRequestHeader("Authorization", "Bearer "+token),
   )
   ```

2. **分布式追踪**：添加请求 ID（OpenTelemetry trace context 会自动通过 HTTP header 传播）
   ```go
   agent.WithA2ARequestOptions(
       client.WithRequestHeader("X-Request-ID", requestID),
   )
   ```

**配置 UserID Header：**

客户端和服务端都支持配置使用哪个 HTTP header 来传递 UserID，默认使用 X-User-ID：

```go
// 客户端：配置通过哪个 header 发送 UserID
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	// 默认是 "X-User-ID"，可以自定义
	a2aagent.WithUserIDHeader("X-Custom-User-ID"),
)

// 服务端：配置从哪个 header 读取 UserID
server, _ := a2a.New(
	a2a.WithHost("localhost:8888"),
	a2a.WithAgent(agent, true),
	// 默认是 "X-User-ID"，可以自定义
	a2a.WithUserIDHeader("X-Custom-User-ID"),
)
```

来自 `invocation.Session.UserID` 的 UserID 会自动通过配置的 header 发送给 A2A server。

### ADK 兼容模式

如果需要与 Google ADK (Agent Development Kit) Python 客户端互通，可以启用 ADK 兼容模式。
启用后，Server 会在 metadata 中额外写入 `adk_` 前缀的 key（如 `adk_type`、`adk_thought`），
以兼容 ADK 的 part converter 解析逻辑：

```go
server, _ := a2a.New(
	a2a.WithHost("localhost:8888"),
	a2a.WithAgent(agent, true),
	a2a.WithADKCompatibility(true), // 默认开启
)
```

### 自定义转换器

对于特殊需求，可以自定义消息和事件转换器：

```go
// 自定义 A2A 消息转换器（Invocation → A2A Message）
// 实现 a2aagent.InvocationA2AConverter 接口
type CustomA2AConverter struct{}

func (c *CustomA2AConverter) ConvertToA2AMessage(
	isStream bool, 
	agentName string, 
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	// 自定义消息转换逻辑
	msg := protocol.NewMessage(protocol.MessageRoleUser, []protocol.Part{
		protocol.NewTextPart(invocation.Message.Content),
	})
	return &msg, nil
}

// 自定义事件转换器（A2A Response → Event）
// 实现 a2aagent.A2AEventConverter 接口
type CustomEventConverter struct{}

func (c *CustomEventConverter) ConvertToEvents(
	result protocol.MessageResult,
	agentName string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	// 自定义非流式事件转换逻辑
	return []*event.Event{event.New(invocation.InvocationID, agentName)}, nil
}

func (c *CustomEventConverter) ConvertStreamingToEvents(
	result protocol.StreamingMessageEvent,
	agentName string,
	invocation *agent.Invocation,
) ([]*event.Event, error) {
	// 自定义流式事件转换逻辑
	return []*event.Event{event.New(invocation.InvocationID, agentName)}, nil
}

// 使用自定义转换器
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	a2aagent.WithCustomA2AConverter(&CustomA2AConverter{}),
	a2aagent.WithCustomEventConverter(&CustomEventConverter{}),
)
```


## 协议交互规范

关于 A2A 协议中工具调用、代码执行、思考内容等事件的传递规范，以及 Metadata 字段定义、ADK 兼容模式、分布式追踪等详细说明，请参考独立文档：

**[A2A 协议交互规范](a2a-interaction.md)**

该文档定义了 trpc-agent-go 在 A2A 协议之上的扩展规范，是 Client 和 Server 实现的标准参考。

## 总结：A2A Server vs A2AAgent

| 组件 | 职责 | 使用场景 | 核心功能 |
|------|------|----------|----------|
| **A2A Server** | 服务提供者 | 将本地 Agent 暴露给其他系统调用 | • 协议转换<br>• AgentCard 生成<br>• 消息路由<br>• 流式支持 |
| **A2AAgent** | 服务消费者 | 调用远程 A2A 服务 | • 透明代理<br>• 自动发现<br>• 状态传递<br>• 协议适配 |

### 典型架构模式

```
┌─────────────┐ A2A protocol  ┌───────────────┐
│   Client    │──────────────→│ A2A Server    |
│ (A2AAgent)  │               │ (local Agent) │
└─────────────┘               └───────────────┘
      ↑                              ↑
      │                              │
   调用远程                       暴露本地
   Agent服务                     Agent服务
```

通过 A2A Server 和 A2AAgent 的配合使用，可以比较方便的构建的远程的 Agent 系统。

### A2A Server 常用配置项一览

| 配置项 | 说明 |
|--------|------|
| `WithAgent(agent, streaming)` | 设置 Agent，并声明生成的 AgentCard 是否支持流式；与 `WithRunner` 互斥 |
| `WithHost(host)` | 设置服务地址，支持带 path 的 URL |
| `WithAgentCard(card)` | 自定义 AgentCard（覆盖自动生成） |
| `WithRunner(runner)` | 自定义 Runner（注入 Memory、Session 等）；需配合 `WithAgentCard` 使用 |
| `WithSessionService(service)` | 为默认 Runner 设置 SessionService |
| `WithProcessMessageHook(hook)` | 服务端消息处理 Hook（中间件模式） |
| `WithProcessorBuilder(builder)` | 完全自定义消息处理器 |
| `WithTaskManagerBuilder(builder)` | 自定义任务管理器 |
| `WithGraphEventObjectAllowlist(types...)` | 限制 Event 转换器允许输出的 graph object 类型 |
| `WithRunOptions(opts...)` | 为每次调用追加 RunOption |
| `WithStreamingEventType(type)` | 流式输出事件类型（Artifact/Message） |
| `WithUserIDHeader(header)` | 自定义 UserID HTTP Header |
| `WithADKCompatibility(enabled)` | ADK 兼容模式（默认：开启） |
| `WithErrorHandler(handler)` | 自定义错误处理 |
| `WithA2AToAgentConverter(conv)` | 自定义 A2A→Agent 消息转换 |
| `WithEventToA2AConverter(conv)` | 自定义 Event→A2A 消息转换 |
| `WithExtraA2AOptions(opts...)` | 透传底层 A2A Server 选项 |
| `WithDebugLogging(enabled)` | 开启调试日志 |

### A2AAgent 完整配置项一览

| 配置项 | 说明 |
|--------|------|
| `WithAgentCardURL(url)` | 远程 A2A 服务地址 |
| `WithBuildMessageHook(hook)` | 客户端消息构建 Hook（中间件模式） |
| `WithTransferStateKey(keys...)` | 指定要传递的 RuntimeState 键 |
| `WithEnableStreaming(enabled)` | 显式控制流式模式 |
| `WithStreamingChannelBufSize(size)` | 流式缓冲区大小 |
| `WithUserIDHeader(header)` | 自定义 UserID HTTP Header |
| `WithCustomA2AConverter(conv)` | 自定义 Invocation→A2A 消息转换 |
| `WithCustomEventConverter(conv)` | 自定义 A2A Response→Event 转换 |
