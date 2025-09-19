# Runner 组件使用手册

## 概述

Runner 提供了运行 Agent 的接口，负责会话管理和事件流处理。Runner 的核心职责是：获取或创建会话、生成 Invocation ID、调用 Agent.Run 方法、处理返回的事件流并将非 partial 响应事件追加到会话中。

### 🎯 核心特性

- **💾 会话管理**：通过 sessionService 获取/创建会话，默认使用 inmemory.NewSessionService()
- **🔄 事件处理**：接收 Agent 事件流，将非 partial 响应事件追加到会话中
- **🆔 ID 生成**：自动生成 Invocation ID 和事件 ID
- **📊 可观测集成**：集成 telemetry/trace，自动记录 span
- **✅ 完成事件**：在 Agent 事件流结束后生成 runner-completion 事件

## 架构设计

```text
┌─────────────────────┐
│       Runner        │  - 会话管理
└─────────┬───────────┘  - 事件流处理
          │
          │ r.agent.Run(ctx, invocation)
          │
┌─────────▼───────────┐
│       Agent         │  - 接收 Invocation
└─────────┬───────────┘  - 返回 <-chan *event.Event
          │
          │ 具体实现由 Agent 决定
          │
┌─────────▼───────────┐
│     Agent 实现      │  如 LLMAgent, ChainAgent 等
└─────────────────────┘
```

## 🚀 快速开始

### 📋 环境要求

- Go 1.21 或更高版本
- 有效的 LLM API 密钥（OpenAI 兼容接口）
- Redis（可选，用于分布式会话管理）

### 💡 最简示例

```go
package main

import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

func main() {
    // 1. 创建模型
    llmModel := openai.New("DeepSeek-V3-Online-64K")

    // 2. 创建 Agent
    agent := llmagent.New("assistant",
        llmagent.WithModel(llmModel),
        llmagent.WithInstruction("你是一个有帮助的AI助手"),
        llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}), // 启用流式输出
    )

    // 3. 创建 Runner
    r := runner.NewRunner("my-app", agent)

    // 4. 运行对话
    ctx := context.Background()
    userMessage := model.NewUserMessage("你好！")

    eventChan, err := r.Run(ctx, "user1", "session1", userMessage)
    if err != nil {
        panic(err)
    }

    // 5. 处理响应
    for event := range eventChan {
        if event.Error != nil {
            fmt.Printf("错误: %s\n", event.Error.Message)
            continue
        }

        if len(event.Choices) > 0 {
            fmt.Print(event.Choices[0].Delta.Content)
        }
    }
}
```

### 🚀 运行示例

```bash
# 进入示例目录
cd examples/runner

# 设置API密钥
export OPENAI_API_KEY="your-api-key"

# 基础运行
go run main.go

# 使用Redis会话
docker run -d -p 6379:6379 redis:alpine
go run main.go -session redis

# 自定义模型
go run main.go -model "gpt-4o-mini"
```

### 💬 交互式功能

运行示例后，支持以下特殊命令：

- `/history` - 请求 AI 显示对话历史
- `/new` - 开始新的会话（重置对话上下文）
- `/exit` - 结束对话

当 AI 使用工具时，会显示详细的调用过程：

```text
🔧 工具调用:
   • calculator (ID: call_abc123)
     参数: {"operation":"multiply","a":25,"b":4}

🔄 执行中...
✅ 工具响应 (ID: call_abc123): {"operation":"multiply","a":25,"b":4,"result":100}

🤖 助手: 我为您计算了 25 × 4 = 100。
```

## 🔧 核心 API

### Runner 创建

```go
// 基础创建
r := runner.NewRunner(appName, agent, options...)

// 常用选项
r := runner.NewRunner("my-app", agent,
    runner.WithSessionService(sessionService),  // 会话服务
)
```

### 运行对话

```go
// 执行单次对话
eventChan, err := r.Run(ctx, userID, sessionID, message, options...)

// 带运行选项（当前 RunOptions 为空结构体，留作未来扩展）
eventChan, err := r.Run(ctx, userID, sessionID, message)
```

#### 传入对话历史（auto-seed + 复用 Session）

如果上游服务已经维护了会话历史，并希望让 Agent 看见这些上下文，可以直接传入整段
`[]model.Message`。Runner 会在 Session 为空时自动将其写入 Session，并在随后的轮次将
新事件（工具调用、后续回复等）继续写入。

方式 A：使用便捷函数 `runner.RunWithMessages`

```go
msgs := []model.Message{
    model.NewSystemMessage("你是一个有帮助的助手"),
    model.NewUserMessage("第一条用户输入"),
    model.NewAssistantMessage("上一轮助手回复"),
    model.NewUserMessage("新的问题是什么？"),
}

ch, err := runner.RunWithMessages(ctx, r, userID, sessionID, msgs)
```

示例：`examples/runwithmessages`（使用 `RunWithMessages`；Runner 会 auto-seed 并复用 Session）

方式 B：通过 RunOption 显式传入（与 Python ADK 一致的理念）

```go
msgs := []model.Message{ /* 同上 */ }
ch, err := r.Run(ctx, userID, sessionID, model.Message{}, agent.WithMessages(msgs))
```

注意：当显式传入 `[]model.Message` 时，Runner 会在 Session 为空时自动把这段历史写入
Session。内容处理器不会读取这个选项，它只会从 Session 事件中派生消息（或在 Session
没有事件时回退到单条 `invocation.Message`）。`RunWithMessages` 仍会把最新的用户消息写入
`invocation.Message`。

## 💾 会话管理

### 内存会话（默认）

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

sessionService := inmemory.NewSessionService()
r := runner.NewRunner("app", agent,
    runner.WithSessionService(sessionService))
```

### Redis 会话（分布式）

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// 创建 Redis 会话服务
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"))

r := runner.NewRunner("app", agent,
    runner.WithSessionService(sessionService))
```

### 会话配置

```go
// Redis 支持的配置选项
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSessionEventLimit(1000),         // 限制会话事件数量
    // redis.WithRedisInstance("redis-instance"), // 或使用实例名
)
```

## 🤖 Agent 配置

Runner 的核心职责是管理 Agent 的执行流程。创建好的 Agent 需要通过 Runner 执行。

### 基础 Agent 创建

```go
// 创建基础 Agent（详细配置参见 agent.md）
agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("你是一个有帮助的AI助手"))

// 使用 Runner 执行 Agent
r := runner.NewRunner("my-app", agent)
```

### 生成配置

Runner 会将生成配置传递给 Agent：

```go
// 辅助函数
func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }

genConfig := model.GenerationConfig{
    MaxTokens:   intPtr(2000),
    Temperature: floatPtr(0.7),
    Stream:      true,  // 启用流式输出
}

agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithGenerationConfig(genConfig))
```

### 工具集成

工具配置在 Agent 中完成，Runner 负责运行包含工具的 Agent：

```go
// 创建工具（详细配置参见 tool.md）
tools := []tool.Tool{
    function.NewFunctionTool(myFunction, function.WithName("my_tool")),
    // 更多工具...
}

// 将工具添加到 Agent
agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(tools))

// Runner 运行配置了工具的 Agent
r := runner.NewRunner("my-app", agent)
```

**工具调用流程**：Runner 本身不直接处理工具调用，具体流程如下：

1. **传递工具**：Runner 通过 Invocation 将上下文传递给 Agent
2. **Agent 处理**：Agent.Run 方法负责具体的工具调用逻辑
3. **事件转发**：Runner 接收 Agent 返回的事件流并转发
4. **会话记录**：将非 partial 响应事件追加到会话中

### 多 Agent 支持

Runner 可以执行复杂的多 Agent 结构（详细配置参见 multiagent.md）：

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"

// 创建多 Agent 组合
multiAgent := chainagent.New("pipeline",
    chainagent.WithSubAgents([]agent.Agent{agent1, agent2}))

// 使用同一个 Runner 执行
r := runner.NewRunner("multi-app", multiAgent)
```

## 📊 事件处理

### 事件类型

```go
import "trpc.group/trpc-go/trpc-agent-go/event"

for event := range eventChan {
    // 错误事件
    if event.Error != nil {
        fmt.Printf("错误: %s\n", event.Error.Message)
        continue
    }

    // 流式内容
    if len(event.Choices) > 0 {
        choice := event.Choices[0]
        fmt.Print(choice.Delta.Content)
    }

    // 工具调用
    if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
        for _, toolCall := range event.Choices[0].Message.ToolCalls {
            fmt.Printf("调用工具: %s\n", toolCall.Function.Name)
        }
    }

    // 完成事件
    if event.Done {
        break
    }
}
```

### 完整事件处理示例

```go
import (
    "fmt"
    "strings"
)

func processEvents(eventChan <-chan *event.Event) error {
    var fullResponse strings.Builder

    for event := range eventChan {
        // 处理错误
        if event.Error != nil {
            return fmt.Errorf("事件错误: %w", event.Error)
        }

        // 处理工具调用
        if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
            fmt.Println("🔧 工具调用:")
            for _, toolCall := range event.Choices[0].Message.ToolCalls {
                fmt.Printf("  • %s (ID: %s)\n",
                    toolCall.Function.Name, toolCall.ID)
                fmt.Printf("    参数: %s\n",
                    string(toolCall.Function.Arguments))
            }
        }

        // 处理工具响应
        if event.Response != nil {
            for _, choice := range event.Response.Choices {
                if choice.Message.Role == model.RoleTool {
                    fmt.Printf("✅ 工具响应 (ID: %s): %s\n",
                        choice.Message.ToolID, choice.Message.Content)
                }
            }
        }

        // 处理流式内容
        if len(event.Choices) > 0 {
            content := event.Choices[0].Delta.Content
            if content != "" {
                fmt.Print(content)
                fullResponse.WriteString(content)
            }
        }

        if event.Done {
            fmt.Println() // 换行
            break
        }
    }

    return nil
}
```

## 🔮 执行上下文管理

Runner 创建并管理 Invocation 结构：

```go
// Runner 创建的 Invocation 包含以下字段：
invocation := agent.NewInvocation(
    agent.WithInvocationAgent(r.agent),        // Agent 实例
    agent.WithInvocationSession(Session),      // 会话对象
    agent.WithInvocationEndInvocation(false),  // 结束标志
    agent.WithInvocationMessage(message),      // 用户消息
    agent.WithInvocationRunOptions(ro),        // 运行选项
)
// 注：Invocation 还包含其他字段如 AgentName、Branch、Model、
// TransferInfo、AgentCallbacks、ModelCallbacks、ToolCallbacks 等，
// 但这些字段由 Agent 内部使用和管理
```

## ✅ 使用注意事项

### 错误处理

```go
// 处理 Runner.Run 的错误
eventChan, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    log.Printf("Runner 执行失败: %v", err)
    return err
}

// 处理事件流中的错误
for event := range eventChan {
    if event.Error != nil {
        log.Printf("事件错误: %s", event.Error.Message)
        continue
    }
    // 处理正常事件
}
```

### 资源管理

```go
// 使用 context 控制生命周期
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// 确保消费完所有事件
eventChan, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    return err
}

for event := range eventChan {
    // 处理事件
    if event.Done {
        break
    }
}
```

### 状态检查

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 检查 Runner 是否能正常工作
func checkRunner(r runner.Runner, ctx context.Context) error {
    testMessage := model.NewUserMessage("测试")
    eventChan, err := r.Run(ctx, "test-user", "test-session", testMessage)
    if err != nil {
        return fmt.Errorf("Runner.Run 失败: %v", err)
    }

    // 检查事件流
    for event := range eventChan {
        if event.Error != nil {
            return fmt.Errorf("收到错误事件: %s", event.Error.Message)
        }
        if event.Done {
            break
        }
    }

    return nil
}
```

## 📝 总结

Runner 组件是 tRPC-Agent-Go 框架的核心，提供了完整的对话管理和 Agent 编排能力。通过合理使用会话管理、工具集成和事件处理，可以构建强大的智能对话应用。
