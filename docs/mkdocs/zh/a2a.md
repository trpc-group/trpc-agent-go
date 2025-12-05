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
		a2aserver.WithAgent(agent), // 传入任意 Agent
	)

	// 3. 启动服务，即可接受 A2A 请求
	server.Start(":8080")
}
```

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
	a2aagent.WithCustomEventConverter(curtomEventConverter),

	a2aagent.WithCustomA2AConverter(cursomA2AConverter)

)
```

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

2. **分布式追踪**：添加请求/追踪 ID
   ```go
   agent.WithA2ARequestOptions(
       client.WithRequestHeader("X-Request-ID", requestID),
       client.WithRequestHeader("X-Trace-ID", traceID),
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

### 自定义转换器

对于特殊需求，可以自定义消息和事件转换器：

```go
// 自定义 A2A 消息转换器
type CustomA2AConverter struct{}

func (c *CustomA2AConverter) ConvertToA2AMessage(
	isStream bool, 
	agentName string, 
	invocation *agent.Invocation,
) (*protocol.Message, error) {
	// 自定义消息转换逻辑
	return &protocol.Message{
		MessageID: invocation.InvocationID,
		Role:      protocol.MessageRoleUser,
		Parts:     []protocol.Part{/* 自定义内容 */},
	}, nil
}

// 自定义事件转换器  
type CustomEventConverter struct{}

func (c *CustomEventConverter) ConvertToEvent(
	result protocol.MessageResult,
	agentName string,
	invocation *agent.Invocation,
) (*event.Event, error) {
	// 自定义事件转换逻辑
	return event.New(invocation.InvocationID, agentName), nil
}

// 使用自定义转换器
a2aAgent, _ := a2aagent.New(
	a2aagent.WithAgentCardURL("http://remote-agent:8888"),
	a2aagent.WithA2AMessageConverter(&CustomA2AConverter{}),
	a2aagent.WithEventConverter(&CustomEventConverter{}),
)
```


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
