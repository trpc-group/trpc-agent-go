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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	// 1. 创建模型
	llmModel := openai.New("DeepSeek-V3-Online-64K")

	// 2. 创建 Agent
	a := llmagent.New("assistant",
		llmagent.WithModel(llmModel),
		llmagent.WithInstruction("你是一个有帮助的AI助手"),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}), // 启用流式输出
	)

	// 3. 创建 Runner
	r := runner.NewRunner("my-app", a)
	defer r.Close()  // 确保资源被清理 (trpc-agent-go >= v0.5.0)

	// 4. 运行对话
	ctx := context.Background()
	userMessage := model.NewUserMessage("你好！")

	eventChan, err := r.Run(ctx, "user1", "session1", userMessage, agent.WithRequestID("request-ID"))
	if err != nil {
		panic(err)
	}

	// 5. 处理响应
	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("错误: %s\n", event.Error.Message)
			continue
		}

		if len(event.Response.Choices) > 0 {
			fmt.Print(event.Response.Choices[0].Delta.Content)
		}
		// Recommended: stop when Runner emits its completion event.
		if event.IsRunnerCompletion() {
			break
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
```

#### 中断恢复（工具优先继续执行）

在真实业务里，用户可能在 Agent 还处于“工具调用阶段”时中断：

- 会话里的最后一条消息是带 `tool_calls` 的 assistant 消息；
- 但对应的工具结果（tool result）还没来得及写回 Session。

之后如果你想在同一个 `sessionID` 上“继续上次的任务”，可以开启
`WithResume(true)`，让 Runner 先把上次未完成的工具调用执行完，再进入
下一轮 LLM 调用：

```go
eventChan, err := r.Run(
    ctx,
    userID,
    sessionID,
    model.Message{},          // 没有新的用户输入
    agent.WithResume(true),   // 开启恢复模式
)
```

开启 `WithResume(true)` 时，Runner 会：

- 读取当前 Session 中最新的一条事件；
- 如果最后一条是「带 `tool_calls` 的 assistant 回复」，且之后没有对应的
  工具结果事件：
  - 使用当前 Agent 注册的工具集合和回调，执行这些“未完成的工具调用”；
  - 把工具执行结果写入 Session（作为 tool 消息事件）；
- 工具执行结束后，再按正常流程发起新一轮 LLM 调用，此时模型能看到
  “上一次的 tool_calls + 对应的工具结果”，不会重复要求调用同一工具。

如果最后一条事件是 user / tool 消息，或者是普通的 assistant 文本回复，
则 `WithResume(true)` 不会做任何额外处理，行为等同于普通的 `Run` 调用。

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

ch, err := runner.RunWithMessages(ctx, r, userID, sessionID, msgs, agent.WithRequestID("request-ID"))
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

## ✅ 图式流程的“优雅结束”与最终结果读取

很多同学在使用 GraphAgent（图式智能体）时，会误把 `Response.IsFinalResponse()` 当作“流程完成”的信号。请注意：`IsFinalResponse()` 只是“大模型本轮回复已结束”，但图上后续节点（例如 `output` 汇总节点）仍可能在继续执行。

最稳妥、统一的做法是：以 Runner 的“完成事件”作为运行结束的唯一判据：

```go
for e := range eventChan {
    // ... 处理流式分片、工具可视化等
    if e.IsRunnerCompletion() { // Runner 的终止事件
        break
    }
}
```

此外，Runner 会把图在完成时的最终快照传递到这条“最后事件”里，因此你可以直接从该事件的 `StateDelta` 里读取图的最终输出（例如 `graph.StateKeyLastResponse` 对应的文本）：

```go
import (
    "encoding/json"
    "fmt"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

for e := range eventChan {
    if e.IsRunnerCompletion() {
        if b, ok := e.StateDelta[graph.StateKeyLastResponse]; ok {
            var final string
            _ = json.Unmarshal(b, &final)
            fmt.Println("\nFINAL:", final)
        }
        break
    }
}
```

这样应用层可以始终“看最后一条事件”来判断流程结束并读取最终结果，避免因为提前退出而错过 `output` 等后续节点。

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

### 在请求级别切换 Agent

Runner 支持在构造时注册多个可选 Agent，并在单次 Run 时切换。

```go
reader := llmagent.New("agent1", llmagent.WithModel(model))
writer := llmagent.New("agent2", llmagent.WithModel(model))

r := runner.NewRunner("appName", reader, // 使用 reader agent 作为默认 agent
    runner.WithAgent("writer", writer),  // 按名称注册可选 Agent
)

// 使用 reader agent 作为默认 agent
ch, err := r.Run(ctx, userID, sessionID, msg)

// 通过 Agent Name 指定使用 writer agent
ch, err := r.Run(ctx, userID, sessionID, msg, agent.WithAgentByName("writer"))

// 直接传入实例，无需预注册。
custom := llmagent.New("custom", llmagent.WithModel(model))
ch, err := r.Run(ctx, userID, sessionID, msg, agent.WithAgent(custom))
```

- `runner.NewRunner("appName", agent)`：在创建 runner 时设置默认 Agent；
- `runner.WithAgent("agentName", agent)`: 在创建 Runner 时预注册一个 Agent，供后续请求按名称切换；
- `agent.WithAgentByName("agentName")`: 在单次请求里通过名称选用已注册的 Agent；
- `agent.WithAgent(agent)`: 在单次请求里直接传入一个 Agent 实例临时覆盖，无需预注册。

Agent 生效优先级：`agent.WithAgent` > `agent.WithAgentByName` > `runner.NewRunner` 设置的默认 Agent。

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
    if len(event.Response.Choices) > 0 {
        choice := event.Response.Choices[0]
        fmt.Print(choice.Delta.Content)
    }

    // 工具调用
    if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
        for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
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
        if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
            fmt.Println("🔧 工具调用:")
            for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
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
        if len(event.Response.Choices) > 0 {
            content := event.Response.Choices[0].Delta.Content
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

### 安全中断执行

- **取消上下文**：用 `context.WithCancel` 包裹 `runner.Run` 的 ctx，
  当轮次或 token 超限时调用 `cancel()`。`llmflow` 将
  `context.Canceled` 视为正常退出，会关闭 agent 事件通道，
  runner 的消费循环也会正常结束，避免阻塞。

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

eventCh, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    return err
}

turns := 0
for evt := range eventCh {
    if evt.Error != nil {
        log.Printf("事件错误: %s", evt.Error.Message)
        continue
    }
    // ... 处理事件 ...
    if evt.IsFinalResponse() {
        break
    }
    turns++
    if turns >= maxTurns {
        cancel() // 停止后续模型或工具调用
    }
}
```

- **发送停止事件**：在自定义处理器或工具内部返回 `agent.NewStopError("原因")`。`llmflow` 会把它转换为 `stop_agent_error` 事件并停止流程。
  仍建议配合 ctx cancel 进行硬截止。详见 [回调中的停止用法](https://trpc-group.github.io/trpc-agent-go/zh/callbacks/#stop-agent-via-callbacks)。

- **避免直接 break 事件循环**：直接在 runner 的事件消费循环里 break 会让 agent goroutine 继续运行并可能在写通道时阻塞。
  优先使用上下文取消或 `StopError`。

### 资源管理

#### 🔒 关闭 Runner（重要）

**Runner 在不使用时必须调用 `Close()` 方法，否则会导致 goroutine 泄漏（要求 `trpc-agent-go >= v0.5.0`）。**

**Runner 只关闭它自己创建的资源**

当 Runner 创建时如果未提供 Session Service，会自动创建默认的 inmemory Session Service。该 Service 内部会启动后台 goroutines（用于异步处理 summary、基于 TTL 的会话清理等任务）。**Runner 只管理这个自己创建的 inmemory Session Service 的生命周期。** 如果你通过 `WithSessionService()` 提供了自己的 Session Service，你需要自己管理它的生命周期——Runner 不会关闭它。

如果不调用拥有 inmemory Session Service 的 Runner 的 `Close()`，这些后台 goroutines 将永远运行，造成资源泄漏。

**推荐做法**：

```go
// ✅ 推荐：使用 defer 确保资源被清理
r := runner.NewRunner("my-app", agent)
defer r.Close()  // 确保在函数退出时关闭 (trpc-agent-go >= v0.5.0)

// 使用 runner
eventChan, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
	return err
}

for event := range eventChan {
	// 处理事件
	if event.IsRunnerCompletion() {
		break
	}
}
```

**当你提供自己的 Session Service 时**：

```go
// 你创建并管理 session service 的生命周期
sessionService := redis.NewService(redis.WithRedisClientURL("redis://localhost:6379"))
defer sessionService.Close()  // 你负责关闭它

// Runner 使用但不拥有这个 session service
r := runner.NewRunner("my-app", agent,
	runner.WithSessionService(sessionService))
defer r.Close()  // 这不会关闭 sessionService（因为是你提供的） (trpc-agent-go >= v0.5.0)

// ... 使用 runner
```

**长期运行的服务**：

```go
type Service struct {
	runner runner.Runner
	sessionService session.Service  // 如果你自己管理它
}

func NewService() *Service {
	r := runner.NewRunner("my-app", agent)
	return &Service{runner: r}
}

func (s *Service) Start() error {
	// 启动服务逻辑
	return nil
}

// 在服务关闭时调用 Close
func (s *Service) Stop() error {
	// 关闭 runner（它会关闭自己拥有的 inmemory session service）
    // 要求 trpc-agent-go >= v0.5.0
	if err := s.runner.Close(); err != nil {
		return err
	}

	// 如果你提供了自己的 session service，在这里关闭它
	if s.sessionService != nil {
		return s.sessionService.Close()
	}

	return nil
}
```

**注意事项**：

- ✅ `Close()` 是幂等的，多次调用是安全的
- ✅ **Runner 只关闭它默认创建的 inmemory Session Service**
- ✅ 如果你通过 `WithSessionService()` 提供了自己的 Session Service，Runner 不会关闭它（你需要自己管理）
- ❌ 如果 Runner 拥有 inmemory Session Service 但不调用 `Close()`，会导致 goroutine 泄漏

#### Context 生命周期控制

```go
// 使用 context 控制单次运行的生命周期
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
