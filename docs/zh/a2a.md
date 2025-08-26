# tRPC-Agent-Go A2A 集成指南

## 概述

tRPC-Agent-Go 提供了一键将 Agent 转换为 A2A (Agent-to-Agent) 服务的能力。通过简单的配置，开发者可以将任何基于 trpc-agent-go 框架开发的 Agent 快速暴露为符合 A2A 协议的服务，实现 Agent 间的标准化通信。

### 核心能力

- **零协议感知**: 开发者只需关注 Agent 的业务逻辑，无需了解 A2A 协议细节
- **自动适配**: 框架自动将 Agent 信息转换为 A2A AgentCard
- **消息转换**: 自动处理 A2A 协议消息与 Agent 消息格式的转换
- **tRPC 集成**: 与 tRPC 生态无缝集成，支持服务发现、负载均衡等企业级特性

## tRPC-Agent-Go 中的 A2A 集成

### Agent 到 A2A 的自动转换

tRPC-Agent-Go 通过 `server/a2a` 包实现了从 Agent 到 A2A 服务的无缝转换：

```go
func New(opts ...Option) (*a2a.A2AServer, error) {}
```

### AgentCard 自动生成

框架会自动提取 Agent 的元数据（名称、描述等），生成符合 A2A 协议的 AgentCard。

### 消息协议转换

框架内置 `messageProcessor` 实现 A2A 协议消息与 Agent 消息格式的双向转换，用户无需关心消息格式转换的细节。

## 快速开始

### 使用 tRPC-Agent-Go 创建 A2A 服务

只需几行代码，就可以将任意 Agent 转换为 A2A 服务：

#### 基础示例：从 Agent 到 A2A 服务

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

#### 客户端调用示例

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
