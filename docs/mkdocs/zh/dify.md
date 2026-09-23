# tRPC-Agent-Go Dify 集成指南

## 概述

tRPC-Agent-Go 提供了与 [Dify](https://dify.ai/) 平台的完整集成方案，通过 `DifyAgent` 组件，开发者可以轻松调用 Dify 平台上的工作流（Workflow）和聊天流（Chatflow），将 Dify 强大的 AI 能力无缝集成到 tRPC-Agent-Go 应用中。

### 什么是 Dify？

Dify 是一个开源的 LLM 应用开发平台，提供了可视化的 AI 工作流编排、提示词工程、RAG（检索增强生成）等企业级功能。通过 Dify，您可以快速构建和部署复杂的 AI 应用。

### 核心能力

- **统一接口**: 像使用普通 Agent 一样使用 Dify 服务，无需了解 Dify API 细节
- **协议转换**: 自动处理 tRPC-Agent-Go 消息格式与 Dify API 的转换
- **流式支持**: 支持流式和非流式两种响应模式，实现实时交互
- **状态传递**: 支持将会话状态传递给 Dify 工作流
- **自定义扩展**: 支持自定义请求和响应转换器

## DifyAgent：调用 Dify 服务

### 概念介绍

`DifyAgent` 是 tRPC-Agent-Go 提供的特殊 Agent 实现，它将请求转发给 Dify 平台的工作流或聊天流服务。从使用者角度看，`DifyAgent` 就像一个普通的 Agent，但实际上它是 Dify 服务的本地代理。

**简单理解**：
- 我有一个 Dify 工作流/聊天流 → 通过 DifyAgent 在 tRPC-Agent-Go 中调用
- 像使用本地 Agent 一样使用 Dify 的 AI 能力

### 核心特性

- **透明代理**: 像使用本地 Agent 一样使用 Dify 服务
- **自动协议转换**: 自动处理消息格式与 Dify API 的转换
- **流式支持**: 支持流式和非流式两种通信模式
- **状态传递**: 支持将会话状态传递给 Dify 工作流
- **自定义处理**: 支持自定义流式响应处理器
- **灵活配置**: 支持自定义 Dify 客户端配置

### 使用场景

1. **企业 AI 应用**: 调用企业内部部署的 Dify 服务
2. **工作流编排**: 使用 Dify 的可视化工作流能力处理复杂业务逻辑
3. **RAG 应用**: 利用 Dify 的知识库和 RAG 能力构建智能问答系统
4. **多模型集成**: 通过 Dify 统一管理和调用多个 LLM 模型

## 快速开始

### 前置准备

1. **获取 Dify 服务信息**:
   - Dify 服务地址（如 `https://api.dify.ai/v1`）
   - API Secret（从 Dify 应用设置中获取）

2. **安装依赖**:
   ```bash
   go get trpc.group/trpc-go/trpc-agent-go
   go get github.com/cloudernative/dify-sdk-go
   ```

### 基本用法

#### 示例 1：非流式对话

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/difyagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// 1. 创建 DifyAgent
	difyAgent, err := difyagent.New(
		difyagent.WithBaseUrl("https://api.dify.ai/v1"),
		difyagent.WithName("dify-chat-assistant"),
		difyagent.WithDescription("Dify 智能助手"),
		difyagent.WithEnableStreaming(false), // 非流式模式
		difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
			return dify.NewClientWithConfig(&dify.ClientConfig{
				Host:             "https://api.dify.ai/v1",
				DefaultAPISecret: "your-api-secret",
				Timeout:          30 * time.Second,
			}), nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 2. 创建会话服务
	sessionService := inmemory.NewSessionService()

	// 3. 创建 Runner
	chatRunner := runner.NewRunner(
		"dify-runner",
		difyAgent,
		runner.WithSessionService(sessionService),
	)

	// 4. 发送消息
	events, err := chatRunner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("你好，请介绍一下自己"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 5. 处理响应
	for event := range events {
		if event.Response != nil && len(event.Response.Choices) > 0 {
			if event.Response.Done {
				fmt.Println(event.Response.Choices[0].Message.Content)
			}
		}
	}
}
```

#### 示例 2：流式对话

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/difyagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	// 自定义流式响应处理器
	streamingHandler := func(resp *model.Response) (string, error) {
		if len(resp.Choices) > 0 {
			content := resp.Choices[0].Delta.Content
			if content != "" {
				fmt.Print(content) // 实时打印
			}
			return content, nil
		}
		return "", nil
	}

	// 创建支持流式的 DifyAgent
	difyAgent, err := difyagent.New(
		difyagent.WithBaseUrl("https://api.dify.ai/v1"),
		difyagent.WithName("dify-streaming-assistant"),
		difyagent.WithDescription("Dify 流式助手"),
		difyagent.WithEnableStreaming(true), // 启用流式
		difyagent.WithStreamingRespHandler(streamingHandler),
		difyagent.WithStreamingChannelBufSize(2048), // 流式缓冲区大小
		difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
			return dify.NewClientWithConfig(&dify.ClientConfig{
				Host:             "https://api.dify.ai/v1",
				DefaultAPISecret: "your-api-secret",
				Timeout:          60 * time.Second,
			}), nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 创建会话服务和 Runner
	sessionService := inmemory.NewSessionService()
	chatRunner := runner.NewRunner(
		"dify-streaming-runner",
		difyAgent,
		runner.WithSessionService(sessionService),
	)

	// 发送消息
	fmt.Print("🤖 助手: ")
	events, err := chatRunner.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("请写一个关于机器人学习绘画的短故事"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 处理流式响应
	for event := range events {
		if event.Error != nil {
			log.Printf("Error: %s", event.Error.Message)
		}
		// streamingHandler 会自动处理并打印内容
	}
	fmt.Println() // 换行
}
```

## 配置选项详解

### 基础配置

| 配置项 | 说明 | 必填 |
|-------|------|------|
| `WithBaseUrl(url)` | Dify 服务地址 | 否 |
| `WithName(name)` | Agent 名称 | 是 |
| `WithDescription(desc)` | Agent 描述 | 否 |

### 客户端配置

| 配置项 | 说明 | 默认值 |
|-------|------|--------|
| `WithGetDifyClientFunc(fn)` | 自定义 Dify 客户端创建函数 | 使用默认配置 |

### 流式配置

| 配置项 | 说明 | 默认值 |
|-------|------|--------|
| `WithEnableStreaming(bool)` | 是否启用流式模式 | false |
| `WithStreamingChannelBufSize(size)` | 流式缓冲区大小 | 1024 |
| `WithStreamingRespHandler(handler)` | 自定义流式响应处理器 | 默认处理器 |

### 高级配置

| 配置项 | 说明 |
|-------|------|
| `WithCustomEventConverter(converter)` | 自定义事件转换器 |
| `WithCustomRequestConverter(converter)` | 自定义请求转换器 |
| `WithTransferStateKey(keys...)` | 设置需要传递的会话状态键 |

## 高级用法

### 自定义状态传递

将会话状态传递给 Dify 工作流：

```go
difyAgent, _ := difyagent.New(
	difyagent.WithName("stateful-agent"),
	// 指定需要传递的状态键
	difyagent.WithTransferStateKey("user_profile", "conversation_context"),
	// ... 其他配置
)

// 在 Runner 中设置状态
runner.Run(
	ctx,
	userID,
	sessionID,
	model.NewUserMessage("查询我的订单"),
	// 这些状态会被传递到 Dify
	runner.WithRuntimeState(map[string]interface{}{
		"user_profile": map[string]string{
			"name": "张三",
			"vip_level": "gold",
		},
		"conversation_context": "正在讨论订单问题",
	}),
)
```

### 自定义响应处理

实现自定义流式响应处理器：

```go
// 自定义处理器：过滤敏感词、格式化输出等
customHandler := func(resp *model.Response) (string, error) {
	if len(resp.Choices) == 0 {
		return "", nil
	}
	
	content := resp.Choices[0].Delta.Content
	
	// 1. 过滤敏感词
	filtered := filterSensitiveWords(content)
	
	// 2. 实时日志记录
	log.Printf("Received chunk: %s", filtered)
	
	// 3. 实时显示
	fmt.Print(filtered)
	
	// 返回处理后的内容（会被聚合到最终响应）
	return filtered, nil
}

difyAgent, _ := difyagent.New(
	difyagent.WithStreamingRespHandler(customHandler),
	// ... 其他配置
)
```

### 自定义请求转换器

实现自定义的请求转换逻辑：

```go
type MyRequestConverter struct{}

func (c *MyRequestConverter) ConvertToDifyRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	isStream bool,
) (*dify.ChatMessageRequest, error) {
	// 自定义请求构建逻辑
	req := &dify.ChatMessageRequest{
		Query:           extractQuery(invocation),
		ResponseMode:    getResponseMode(isStream),
		ConversationID:  getConversationID(invocation),
		User:            invocation.RunOptions.UserID,
		Inputs:          customInputs(invocation),
	}
	return req, nil
}

difyAgent, _ := difyagent.New(
	difyagent.WithCustomRequestConverter(&MyRequestConverter{}),
	// ... 其他配置
)
```

### 自定义事件转换器

实现自定义的响应转换逻辑：

```go
type MyEventConverter struct{}

func (c *MyEventConverter) ConvertToEvent(
	resp *dify.ChatMessageResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	// 自定义事件转换逻辑
	return event.New(
		invocation.InvocationID,
		agentName,
		event.WithResponse(&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: customFormat(resp.Answer),
				},
			}},
		}),
	)
}

func (c *MyEventConverter) ConvertStreamingToEvent(
	resp *dify.ChatMessageStreamResponse,
	agentName string,
	invocation *agent.Invocation,
) *event.Event {
	// 自定义流式事件转换逻辑
	// ...
}

difyAgent, _ := difyagent.New(
	difyagent.WithCustomEventConverter(&MyEventConverter{}),
	// ... 其他配置
)
```

## 环境变量配置

推荐使用环境变量管理敏感信息：

```bash
export DIFY_BASE_URL="https://api.dify.ai/v1"
export DIFY_API_SECRET="app-xxxxxxxxxx"
```

代码中读取：

```go
import "os"

difyAgent, _ := difyagent.New(
	difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
		return dify.NewClientWithConfig(&dify.ClientConfig{
			Host:             os.Getenv("DIFY_BASE_URL"),
			DefaultAPISecret: os.Getenv("DIFY_API_SECRET"),
			Timeout:          30 * time.Second,
		}), nil
	}),
	// ... 其他配置
)
```

## 错误处理

### 常见错误及解决方案

| 错误 | 原因 | 解决方案 |
|------|------|----------|
| `agent name is required` | 未设置 Agent 名称 | 使用 `WithName()` 设置名称 |
| `request converter not set` | 未设置请求转换器 | 使用默认转换器或自定义 |
| `Dify request failed` | Dify API 调用失败 | 检查网络、API Secret、服务地址 |
| `event converter not set` | 未设置事件转换器 | 使用默认转换器或自定义 |

### 错误事件处理

```go
for event := range events {
	// 检查错误
	if event.Error != nil {
		log.Printf("Error: %s", event.Error.Message)
		continue
	}
	
	// 检查响应错误
	if event.Response != nil && event.Response.Error != nil {
		log.Printf("Response Error: %s", event.Response.Error.Message)
		continue
	}
	
	// 正常处理
	// ...
}
```

## 最佳实践

### 1. 超时配置

根据 Dify 工作流的复杂度设置合理的超时时间：

```go
difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
	return dify.NewClientWithConfig(&dify.ClientConfig{
		Host:             baseURL,
		DefaultAPISecret: apiSecret,
		Timeout:          60 * time.Second, // 复杂工作流使用更长超时
	}), nil
})
```

### 2. 流式缓冲区大小

根据预期响应长度调整缓冲区：

```go
// 短响应
difyagent.WithStreamingChannelBufSize(512)

// 长响应（如生成文章）
difyagent.WithStreamingChannelBufSize(4096)
```

### 3. 错误重试

在生产环境中实现重试机制：

```go
func runWithRetry(runner *runner.Runner, maxRetries int) error {
	for i := 0; i < maxRetries; i++ {
		events, err := runner.Run(ctx, userID, sessionID, msg)
		if err == nil {
			// 处理 events
			return nil
		}
		log.Printf("Retry %d/%d: %v", i+1, maxRetries, err)
		time.Sleep(time.Second * time.Duration(i+1))
	}
	return fmt.Errorf("max retries exceeded")
}
```

### 4. 会话管理

正确管理会话 ID 以维持对话上下文：

```go
// 每个用户每次对话使用唯一的 session ID
sessionID := fmt.Sprintf("user-%s-conv-%d", userID, time.Now().Unix())

// 或使用持久化的 session ID 维持长期对话
sessionID := getUserSessionID(userID)
```

## 示例代码

完整示例代码位于项目的 `examples/dify/` 目录：

- `basic_chat/`: 基础非流式对话示例
- `streaming_chat/`: 流式对话示例
- `advanced_usage/`: 高级用法示例（状态传递、自定义转换器等）

运行示例：

```bash
cd examples/dify/basic_chat
export DIFY_BASE_URL="https://api.dify.ai/v1"
export DIFY_API_SECRET="your-api-secret"
go run main.go
```

## 常见问题

**Q: DifyAgent 和 A2AAgent 有什么区别？**

A: 
- `DifyAgent`: 专门用于调用 Dify 平台服务，使用 Dify API
- `A2AAgent`: 用于调用符合 A2A 协议的远程 Agent 服务
- 选择取决于后端服务类型

**Q: 如何在企业内网使用自部署的 Dify？**

A: 只需将 `WithBaseUrl()` 设置为内网 Dify 服务地址即可，例如：
```go
difyagent.WithBaseUrl("http://dify.internal.company.com/v1")
```

**Q: 流式和非流式模式如何选择？**

A:
- **流式**: 适合长文本生成、需要实时反馈的场景
- **非流式**: 适合短响应、批量处理的场景

**Q: 如何调试 Dify 请求？**

A: 可以实现自定义请求转换器，在其中添加日志：
```go
func (c *MyConverter) ConvertToDifyRequest(...) (*dify.ChatMessageRequest, error) {
	req := &dify.ChatMessageRequest{...}
	log.Printf("Dify Request: %+v", req)
	return req, nil
}
```

## 相关资源

- [Dify 官方文档](https://docs.dify.ai/)
- [Dify SDK Go](https://github.com/cloudernative/dify-sdk-go)
- [tRPC-Agent-Go 文档](https://github.com/trpc-group/trpc-agent-go/blob/main/README.zh_CN.md)
- [A2A v1.0 集成指南](./a2a_v1.md)
