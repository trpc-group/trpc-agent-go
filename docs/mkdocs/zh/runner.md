# Runner 组件使用手册

## 概述

Runner 提供了运行 Agent 的接口，负责会话管理和事件流处理。Runner 的核心职责是：获取或创建会话、生成 Invocation ID、通过 `agent.RunWithPlugins` 调用 Agent、处理返回的事件流并将非 partial 响应事件追加到会话中。

### 🎯 核心特性

- **💾 会话管理**：通过 sessionService 获取/创建会话，默认使用 inmemory.NewSessionService()
- **🔄 事件处理**：接收 Agent 事件流，将非 partial 响应事件追加到会话中
- **🆔 ID 生成**：自动生成 Invocation ID 和事件 ID
- **📊 可观测集成**：集成 telemetry/trace，自动记录 span
- **✅ 完成事件**：在 Agent 事件流结束后生成 `runner.completion` 事件
- **🔌 插件**：在 Runner 上注册一次，全局作用于该 Runner 管理的 Agent、Tool 和模型调用。

## 架构设计

```text
┌─────────────────────┐
│       Runner        │  - 会话管理
└─────────┬───────────┘  - 事件流处理
          │
          │ agent.RunWithPlugins(ctx, invocation, r.agent)
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

### 🧩 按请求动态创建 Agent（Agent Factory）

默认情况下，`runner.NewRunner(...)` 需要你先把 `agent.Agent` 完整构建好，然后
Runner 会在每次请求里复用同一个 Agent 实例。

如果你的 Agent 配置需要 **跟当前请求绑定**（例如：提示词、模型、沙箱实例、工具集），
可以用 “Agent Factory” 在每次 `Runner.Run(...)` 时动态创建一个新的 Agent。

#### 方式 A：默认 Agent 按需创建

```go
r := runner.NewRunnerWithAgentFactory(
    "my-app",
    "assistant",
    func(ctx context.Context, ro agent.RunOptions) (agent.Agent, error) {
        // 你可以从 ro（或 ro.RuntimeState / ro.CustomAgentConfigs）读取
        // 本次请求的参数，然后据此构建 Agent。
        a := llmagent.New("assistant",
            llmagent.WithInstruction(ro.Instruction),
        )
        return a, nil
    },
)
```

#### 方式 B：注册多个命名工厂，并通过名字选择

```go
r := runner.NewRunner("my-app", defaultAgent,
    runner.WithAgentFactory("sandboxed", func(
        ctx context.Context,
        ro agent.RunOptions,
    ) (agent.Agent, error) {
        return llmagent.New("sandboxed"), nil
    }),
)

events, err := r.Run(ctx, userID, sessionID, message,
    agent.WithAgentByName("sandboxed"),
)
_ = events
_ = err
```

说明：

- 每次调用 `Runner.Run(...)`，Factory 会被调用一次。
- `agent.WithAgent(...)` 依然优先生效（测试时很方便）。

#### Agent Factory 中的资源边界

`AgentFactory` 适合按请求拼装 Agent 配置，但它**不会改变资源所有权**。

- `Runner` 只负责调用 factory 获取一个 `agent.Agent`。
- `Runner.Close()` 只会关闭 Runner 自己创建或持有的资源；它**不会**
  自动关闭 factory 内部新建的 `tool.ToolSet`、临时 MCP 连接、沙箱会话
  等请求级资源。
- 原因是 `agent.Agent` 接口本身没有 `Close()`，因此 Runner 无法统一接管
  这类资源的释放。

实践建议：

- 如果某个 `ToolSet` 或外部连接适合跨请求复用，优先在 factory 外创建一次，
  然后在 factory 中复用；应用退出时再统一 `Close()`。
- 如果资源必须按请求创建，调用方需要在本次 run 结束后自行清理。
  常见做法是包装一个带清理逻辑的 Agent，或者在 Agent 的
  after callback 中执行清理。

这类边界在使用 MCP ToolSet 时尤其常见，详细说明可继续参考
`tool` 文档中的 ToolSet 生命周期章节。

### 🔌 插件

Runner 插件是一类全局、Runner 作用域的 Hook（钩子）。只需要在创建 Runner 时
注册一次，后续该 Runner 执行的所有 Agent、Tool 和模型调用都会自动生效。

```go
import "trpc.group/trpc-go/trpc-agent-go/plugin"

r := runner.NewRunner("my-app", a,
    runner.WithPlugins(
        plugin.NewLogging(),
        plugin.NewGlobalInstruction("You must follow security policies."),
    ),
)
defer r.Close()
```

说明：

- 插件名在同一个 Runner 内必须唯一。
- 插件按注册顺序执行。
- 如果插件实现了 `plugin.Closer`，Runner 会在 `Close()` 时调用它。

### 🔄 Ralph Loop Mode

Ralph Loop 是一种“外部循环（outer loop）”模式：不依赖 LLM 主观判断“我已经完成了”，
而是用可验证的完成条件来决定是否继续迭代执行。

常见完成条件：

- Assistant 输出包含完成承诺（completion promise），例如
  `<promise>DONE</promise>`。
- 校验命令退出码为 0（例如 `go test ./...`）。
- 通过 `runner.Verifier` 扩展自定义校验。
- 强烈建议设置 `MaxIterations` 作为安全阀。

```go
r := runner.NewRunner("my-app", a,
    runner.WithRalphLoop(runner.RalphLoopConfig{
        MaxIterations:     20,
        CompletionPromise: "DONE",
        VerifyCommand:     "go test ./... -count=1",
        VerifyTimeout:     2 * time.Minute,
    }),
)
```

当达到 `MaxIterations` 仍未满足完成条件时，Runner 会发出一个 error event，其错误类型为
`stop_agent_error`。

### 运行对话

```go
// 执行单次对话
eventChan, err := r.Run(ctx, userID, sessionID, message, options...)
```

#### RequestID（request identifier，请求标识）与运行控制

每次调用 `Runner.Run` 都是一轮 **run**。如果你需要取消某次 run，或者查询它的
运行状态，就需要一个 request identifier（requestID，请求标识）。

推荐由调用方自己生成 requestID，并通过 `agent.WithRequestID` 传入（比如用
Universally Unique Identifier（UUID，通用唯一标识）生成一个唯一字符串）。
Runner 会把它保存到 `RunOptions.RequestID`，并注入到每个事件 `event.Event` 的
`event.RequestID` 字段里。

```go
requestID := "req-123"

eventChan, err := r.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithRequestID(requestID),
)
if err != nil {
    panic(err)
}

managed := r.(runner.ManagedRunner)
status, ok := managed.RunStatus(requestID)
_ = status
_ = ok

// 用 requestID 取消本次 run。
managed.Cancel(requestID)
```

#### 在同一轮 run 中排队插入新的用户消息

有些场景下，你并不想启动第二轮 run，而是希望继续使用当前的
`requestID`，把新的 `role=user` 消息排队，等当前这一轮 assistant 处理完后，
再插入到同一轮 run 里。

可以使用 `runner.EnqueueUserMessage(...)`：

```go
requestID := "req-123"

eventChan, err := r.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage("Draft a launch note."),
    agent.WithRequestID(requestID),
)
if err != nil {
    panic(err)
}

go func() {
    time.Sleep(time.Second)
    err := runner.EnqueueUserMessage(
        r,
        requestID,
        model.NewUserMessage("Also make the tone warmer."),
    )
    if err != nil {
        log.Printf("enqueue steer failed: %v", err)
    }
}()

_ = eventChan
```

可以把一次 assistant 输出看成一轮：

- 如果这次 assistant 只是普通回复，那么这一轮到 assistant 回复结束为止
- 如果这次 assistant 发起了 `tool_call`，那么这一轮要等这批 tool 全部执行完

新的用户消息只能插在两轮之间，不会插到一轮中间。

最直观的理解是：

```text
user(Q1)
assistant(tool_call A)
tool(result A)
user(Q2, queued steer)
assistant(...)
```

如果一条 assistant 消息里一次发起了多个 tool，那么也要等这一整轮结束：

```text
user(Q1)
assistant(tool_calls A, B)
tool(result A)
tool(result B)
user(Q2, queued steer)
assistant(...)
```

不会出现下面这种插法：

```text
user(Q1)
assistant(tool_calls A, B)
tool(result A)
user(Q2, queued steer)
tool(result B)
```

因为这会把同一轮里的 `tool_call -> tool_response` 结构拆开。

所以它的行为可以简单理解成：

- 这**不会**启动第二轮 run
- 新消息会先进入队列，不会立刻写 Session
- 只有上一轮 assistant 及其附属 tool 全部完成后，才会把消息追加进去
- 这能保证 `tool_call -> tool_response` 结构保持完整
- 如果 run 已经结束，enqueue 会返回错误

如果你想和实现对应起来看，它实际发生在一次 `runOneStep()` 结束之后、下一次
`runOneStep()` 开始之前。

可运行示例：`examples/steer/`

#### 按请求覆盖 AppName（多租户隔离）

默认情况下，Runner 使用构造时传入的 `appName` 作为 session key 和事件过滤 key。
如果一个 Runner 实例需要同时服务多个项目或租户，可以在每次 `Run` 调用时通过
`agent.WithAppName` 覆盖 app name：

```go
// 一个 Runner，两个项目。
r := runner.NewRunner("default-app", myAgent)

// 项目 A — session 数据存储在 "project-a" 下。
evA, _ := r.Run(ctx, userID, sessionID, msg,
    agent.WithAppName("project-a"),
)

// 项目 B — session 数据存储在 "project-b" 下，与 A 完全隔离。
evB, _ := r.Run(ctx, userID, sessionID, msg,
    agent.WithAppName("project-b"),
)
```

当 **未传入** `WithAppName`（或值为空字符串）时，Runner 会回退到构造函数提供的
默认 app name。此覆盖影响的维度如下：

| 维度 | 默认（无覆盖） | 使用 `WithAppName("X")` |
|---|---|---|
| `session.Key.AppName` | 构造时 `appName` | `"X"` |
| 默认 `EventFilterKey` | 构造时 `appName` | `"X"` |

Runner 级别的其他注册（可观测性 `appid`、agent 注册表）仍然绑定到构造时的原始
`appName`。

!!! note
    `appName` 不能为空。如果构造函数和 `WithAppName` 都没有提供非空值，
    session 服务会返回 `session.ErrAppNameRequired`。

#### DetachedCancel（忽略父 ctx cancel）

在 Go 里，`context.Context`（通常命名为 `ctx`）同时承载“取消信号”和“截止时间”。
默认情况下，父 `ctx` 被取消（cancel）时，Runner 会停止这次 run。

如果你希望父 `ctx` 的 cancel 不影响 run，但仍然要用超时来限制总运行时长，可以：

```go
eventChan, err := r.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithRequestID(requestID),
    agent.WithDetachedCancel(true),
    agent.WithMaxRunDuration(30*time.Second),
)
```

Runner 会取以下两者中较早的时间作为真正的超时上限：

- 父 `ctx` 的 deadline（如果存在）
- `MaxRunDuration`（如果设置了）

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

#### Tool Call 参数自动修复

部分模型在生成 `tool_calls` 时，可能产出非严格 JSON 的参数（例如对象 key 未加引号、尾逗号等），从而导致工具执行或外部解析失败。

在 `runner.Run` 中启用 `agent.WithToolCallArgumentsJSONRepairEnabled(true)` 后，框架会对 `toolCall.Function.Arguments` 做一次尽力修复，详细使用方法可参照 [ToolCall参数自动修复](./runner.md#tool-call)。

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

#### 按 `nodeID` 覆盖指定节点的运行时 surface

如果你需要在一次 `runner.Run(...)` 中只修改某个节点，而不是修改整个 Agent，可以传入
`agent.WithSurfacePatchForNode(nodeID, patch)`。

```go
var patch agent.SurfacePatch
patch.SetInstruction("Answer in one short paragraph.")

events, err := r.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage("Summarize this report."),
    agent.WithSurfacePatchForNode(nodeID, patch),
)
```

推荐先通过 `structure.Export(...)` 获取稳定 `nodeID`，再把它传给
`WithSurfacePatchForNode(...)`。同一次运行中如果要覆盖多个节点，可以重复传多个
`WithSurfacePatchForNode(...)`。完整说明与更多示例见
[Agent 使用文档：按 `nodeID` 覆盖运行时 surface](./agent.md#nodeid-surface)。

#### 按运行临时覆盖 `code executor`

如果需要为会从 `RunOptions.CodeExecutor` 解析执行器的 Agent 在不同请求中指定不同的执行环境，例如 `LLMAgent`，可以在 `runner.Run(...)` 中直接传入 `agent.WithCodeExecutor(exec)`。

```go
events, err := r.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage("Run the release checklist skill."),
    agent.WithCodeExecutor(containerExec),
)
```

说明：

- 该选项仅对当前这一次 `runner.Run(...)` 调用生效，不会修改 Agent 的默认配置。
- 该选项仅对会读取 `RunOptions.CodeExecutor` 的 Agent 生效；如果使用自定义 Agent，请确认其实现会处理该运行参数。
- 如果创建 Agent 时已经设置 `llmagent.WithCodeExecutor(...)`，则此处传入的执行器会在本次运行中临时覆盖默认值。
- 本次运行中所有依赖代码执行器的能力，均会使用此处传入的执行器，例如 `workspace_exec`、`skill_run` 和交互式 skill 会话工具。
- 如果仅需为 `skill_run` 提供运行环境，而不希望模型自动执行回复中的 Markdown 围栏代码，可在创建 Agent 时设置 `llmagent.WithEnableCodeExecutionResponseProcessor(false)`。更多说明见 [Skill 文档](./skill.md)。

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

#### 图完成前就致命退出时，如何只看最后一条事件拿到错误信息

有些运行不会走到最终的 `graph.execution` 完成事件，就已经因为致命错误提前结束。一个很常见的场景是：

如果你想看 graph、Runner、subgraph、A2A 串起来的完整推荐方案，见
[Error Handling](error-handling.md)。

- 某个节点回调先发出一条自定义 `StateDelta`，里面带了致命错误详情
- 随后流程直接中止，图本身来不及产出正常的最终快照

这时 Runner 仍然会发出最后那条 `runner.completion` 事件。对于这种真正的致命错误
（不包括 `stop_agent_error` 这种受控停止），Runner 现在会把“可安全兜底”的业务状态
带到这条最后事件上：

- `StateDelta`：错误路径上累计出来的状态增量

这里有两个细节要注意：

- `Response.Error` 仍然只保留在原始致命错误事件上，这样下游翻译层依然可以把
  `runner.completion` 当作正常的结束信号处理。
- `graph.MetadataKeyNode`、`graph.MetadataKeyTool` 这类图元数据键会在兜底复制时被过滤掉，
  避免 AGUI 这类消费者把节点/工具生命周期事件重复翻译一遍。

这样业务代码就可以继续保持同一个规则：优先看最后一条事件里的业务错误详情，而不是为了拿错误信息去遍历整条事件流。

如果 graph 侧用了 `graph.NewExecutionErrorCollector()`，那么这条
`StateDelta` 里的 `execution_errors` 也可能来自默认 recoverable 约定，
例如错误实现了 `Recoverable() bool`，或者通过
`graph.MarkRecoverable(err)` 做了显式标记。

示例：

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

const stateKeyNodeFatal = "node_fatal_error"

type RunSummary struct {
    TransportError  *model.ResponseError
    FatalDetail     map[string]any
    ExecutionErrors []graph.ExecutionError
}

func ConsumeRun(
    ctx context.Context,
    eventChan <-chan *event.Event,
) (*RunSummary, error) {
    summary := &RunSummary{}

    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case evt, ok := <-eventChan:
            if !ok {
                return summary, nil
            }
            if evt.Response != nil && evt.Response.Error != nil {
                summary.TransportError = evt.Response.Error
            }
            if !evt.IsRunnerCompletion() {
                continue
            }

            if b, ok := evt.StateDelta[stateKeyNodeFatal]; ok {
                var detail map[string]any
                if err := json.Unmarshal(b, &detail); err != nil {
                    return nil, err
                }
                summary.FatalDetail = detail
            }

            executionErrors, err := graph.ExecutionErrorsFromStateDelta(
                evt.StateDelta,
                graph.StateKeyExecutionErrors,
            )
            if err != nil {
                return nil, err
            }
            summary.ExecutionErrors = executionErrors
            return summary, nil
        }
    }
}

func PrintSummary(summary *RunSummary) {
    if summary.TransportError != nil {
        fmt.Printf(
            "transport error: type=%s code=%s message=%s\n",
            summary.TransportError.Type,
            ptrValue(summary.TransportError.Code),
            summary.TransportError.Message,
        )
    }
    if summary.FatalDetail != nil {
        fmt.Printf("fatal detail: %+v\n", summary.FatalDetail)
    }
    for _, record := range summary.ExecutionErrors {
        if record.Error == nil {
            continue
        }
        fmt.Printf(
            "execution error: severity=%s node=%s code=%s message=%s\n",
            record.Severity,
            record.NodeName,
            ptrValue(record.Error.Code),
            record.Error.Message,
        )
    }
}

func ptrValue(value *string) string {
    if value == nil {
        return ""
    }
    return *value
}
```

建议按下面这个心智模型理解：

- 正常成功且图产出了完成事件：从 completion event 的 `StateDelta` 里读取最终输出
  （例如 `graph.StateKeyLastResponse`）
- 图完成前就致命退出：从同一条 completion event 的自定义 fatal key 里读取错误信息；
  如果还需要结构化的 `Response.Error`，它仍然保留在原始致命错误事件上
- `stop_agent_error`：仍然被视为“受控停止”信号，不会再重复镜像到 completion event

#### 🔁 开关：让 Graph 的 LLM 节点输出最终响应事件

在 GraphAgent 里，一次 `Run` 可能会在多个节点里多次调用 LLM。当开启流式输出时，
一次模型调用通常会产生一串事件：

- 分片（`partial`）事件：`IsPartial=true`、`Done=false`，增量文本在
  `choice.Delta.Content`
- 模型调用的最终事件：`IsPartial=false`、`Done=true`，完整文本在
  `choice.Message.Content`

默认情况下，Graph 的 LLM 节点只输出分片事件，不输出最终 `Done=true` 的 assistant 消息
事件。这样可以避免“中间节点的输出”被当作普通助手回复（例如被 Runner 写进会话，或被
上层用户界面直接展示）。

如果你希望 Graph 的 LLM 节点也输出最终 `Done=true` 的 assistant 消息事件，可以开启这个
RunOption：

```go
eventChan, err := r.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithGraphEmitFinalModelResponses(true),
)
```

行为总结：

先讲清楚一句话：这个开关控制的是“图里每个 LLM 节点是否要额外输出最终 `Done=true`
的消息事件”，它不等价于“Runner 的完成事件一定会带（或一定不会带）
`Response.Choices`”。

假设你的图是：`llm1 -> llm2 -> llm3`，最后由 `llm3` 产出最终答案：

- 情况 1：`agent.WithGraphEmitFinalModelResponses(false)`（默认）
  - `llm1/llm2/llm3`：只输出分片事件（`Done=false`），不输出最终 `Done=true` 的
    assistant 消息事件。
  - Runner 完成事件：为了让“只看最后一条事件也能拿到最终答案”，Runner 会把 `llm3`
    的最终结果回显到完成事件的 `Response.Choices`（前提是图的完成事件里带了
    `Response.Choices`）。同时，最终文本也始终能从
    `StateDelta[graph.StateKeyLastResponse]` 读取。
- 情况 2：`agent.WithGraphEmitFinalModelResponses(true)`
  - `llm1/llm2/llm3`：除了分片事件外，还会各自输出最终 `Done=true` 的 assistant
    消息事件（因此中间节点也可能出现完整 assistant 消息，Runner 也可能把这些非分片事件
    写入会话）。
  - Runner 完成事件：为了避免和 `llm3` 的最终消息重复展示，Runner 会用响应 ID 做去重；
    当它确认“最终消息已在前面的事件里出现过”时，就会省略回显，因此完成事件的
    `Response.Choices` 可能为空，这是预期行为。最终文本仍然以
    `StateDelta[graph.StateKeyLastResponse]` 为准。

建议：在 GraphAgent 场景里，请始终以 Runner “完成事件”的 `StateDelta` 作为最终输出的
唯一来源（例如 `graph.StateKeyLastResponse`）。当开启该选项时，请把“完成事件”里的
`Response.Choices` 当作可选字段，不要作为唯一依赖。

#### 开关：只保留 terminal Graph 消息事件

当一个图里有多个 LLM 节点或多个子 Agent 节点时，业务侧拿到的消息流里可能会出现前面
节点的中间草稿。为了保持 100% 向后兼容，这仍然是默认行为。

如果你希望“对调用方可见”的消息流只保留 terminal 节点的消息事件，可以开启：

```go
eventChan, err := r.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithGraphTerminalMessagesOnly(true),
)
```

行为总结：

- 默认值（`false`）：完全不变，仍然会转发中间图节点的消息事件。
- 开启后（`true`）：对调用方可见的消息事件会被限制为 terminal LLM 节点和 terminal
  子 Agent 节点。
- 如果图的最后一步是并行的，所有 terminal 节点都会保留，不会强行只留最快或最后一条。
- 图内部执行不会变。state 传递、历史聚合、tracing、token 统计仍然基于完整原始事件流。

这个开关适合“产品只想流式展示最后一个用户可见步骤”的场景，同时又不影响图内部多个
节点之间的协作。

如果你的图里用到真实的 LLM 节点，并且你还希望看到 terminal 节点的最终
`Done=true` assistant 消息事件，请配合
`agent.WithGraphEmitFinalModelResponses(true)` 使用。可参考
`examples/graph/terminal_messages_only`。

#### 🎛️ 开关：StreamMode

Runner 支持在事件到达业务代码之前先做一次过滤：你可以用一个 RunOption 来选择
“本次运行”向 `eventChan` 转发哪些类别的事件。

使用 `agent.WithStreamMode(...)`：

```go
eventChan, err := r.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithStreamMode(agent.StreamModeMessages),
)
```

支持的模式（图式工作流）：

- `messages`：模型输出事件（例如 `chat.completion.chunk`）
- `updates`：`graph.state.update` / `graph.channel.update` / `graph.execution`
- `checkpoints`：`graph.checkpoint.*`
- `tasks`：任务生命周期事件（`graph.node.*`、`graph.pregel.*`）
- `debug`：等价于 `checkpoints` + `tasks`
- `custom`：节点主动发出的自定义事件（`graph.node.custom`）

注意事项：

- 当选择 `agent.StreamModeMessages` 时，Runner 会为本次运行自动开启 Graph 的最终响应事件
  输出。若你需要关闭该行为，请在 `agent.WithStreamMode(...)` 之后调用
  `agent.WithGraphEmitFinalModelResponses(false)` 覆盖。
- StreamMode 只影响 Runner 向你的 `eventChan` 转发哪些事件；Runner 内部仍会处理并持久化
  所有事件。
- 对于图式工作流，部分事件类型（例如 `graph.checkpoint.*`）只会在选择对应模式时才会产生。
- Runner 总会额外发出一条 `runner.completion` 完成事件。

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

### 结束语义

Runner 相关文档里有两个容易混淆的“结束”概念，这里统一说明：

- `Done=true`：表示**当前这条事件本身已经完整**。它可以出现在 assistant 最终消息、
  tool response、graph 事件以及 runner completion 事件上。
- `runner.completion` / `event.IsRunnerCompletion()`：表示**整次 Runner.Run 已经结束**。
  这是业务代码停止消费 `eventChan` 的唯一推荐判据。

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

    // 整次 Runner 结束
    if event.IsRunnerCompletion() {
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

        if event.IsRunnerCompletion() {
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

当你调用 `Runner.Run` 时，框架会启动 goroutines 来持续产出事件，直到本次 run
结束。

这里有两种“停止”，非常容易混淆：

1. **停止读取事件**（你的代码不读 eventChan 了）
2. **停止本次 run**（agent 停止模型/工具调用并退出）

如果你只是停止读取，但 run 还在继续，agent goroutine 可能会在写事件通道时阻塞，
从而引发 goroutine 泄漏或“卡住的 run”。

安全姿势永远是：

1. **触发取消**（ctx cancel / requestID cancel / StopError）
2. **把事件通道读到关闭为止**

#### 方式 A：Ctrl+C（命令行程序）

在命令行程序里，常见做法是把 Ctrl+C 转换为 ctx cancel：

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

eventCh, err := r.Run(ctx, userID, sessionID, message)
if err != nil {
    return err
}

for range eventCh {
    // 一直读到通道关闭：要么 ctx 被取消，要么 run 正常结束。
}
```

#### 方式 B：取消上下文（推荐默认做法）

用 `context.WithCancel` 包裹 `runner.Run` 的 ctx，在你希望中断时调用
`cancel()`（例如：轮次上限、token 预算超限、用户点击“停止”等）。

`llmflow` 将 `context.Canceled` 视为正常退出，会关闭 agent 事件通道，
runner 的消费循环也会正常结束，避免写端阻塞。

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
    if evt.IsRunnerCompletion() {
        break
    }
    turns++
    if turns >= maxTurns {
        cancel() // 停止后续模型或工具调用
    }
}
```

如果你需要“尽快返回”（例如 HTTP handler 超时），但仍想避免写端阻塞，可以用单独
的 goroutine 去 drain：

```go
// eventCh 是 Runner.Run 返回的事件通道。
// cancel 是 context.WithCancel 返回的取消函数。
go func() {
    for range eventCh {
    }
}()
cancel()
return nil
```

#### 方式 C：按 `requestID` 取消（ManagedRunner）

在服务端场景里，你经常需要在“另一个 goroutine / 另一个请求”里取消某次 run。
这时可以用 request identifier（requestID）来定位并取消。

1. 生成 requestID，并通过 `agent.WithRequestID` 传入 `Run`。
2. 将 runner 转换为 `runner.ManagedRunner`。
3. 调用 `Cancel(requestID)`。

```go
requestID := "req-123"

eventCh, err := r.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithRequestID(requestID),
)
if err != nil {
    return err
}

mr := r.(runner.ManagedRunner)
_ = mr.Cancel(requestID)

for range eventCh {
}
```

#### 方式 D：在 run 内部触发停止（StopError）

有时最适合决定“现在就停止”的位置是在工具、回调或处理器内部（例如：策略校验、
预算限制、业务规则）。

你可以返回 `agent.NewStopError("原因")`（也可以与其他错误 join / wrap）。
`llmflow` 会把它转换为 `stop_agent_error` 事件并停止流程。

但“硬截止”（强制时间上限）仍建议用 **ctx deadline**（`context.WithTimeout` /
`agent.WithMaxRunDuration`）来实现。

#### 常见误区

- **只 break 事件循环**：run 可能还在后台继续，并在写通道时阻塞。
- 全部使用 `context.Background()`：你没有办法取消，就无法中断 run。
- 工具实现忽略 `ctx`：取消是协作式的；长耗时工具应检查 `ctx.Done()`，
  或把 `ctx` 传入网络/DB 请求。

可运行示例：

- `examples/cancelrun`（Enter/Ctrl+C 取消、drain 事件通道）
- `examples/managedrunner`（requestID cancel、detached cancel、最长运行时长）

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
	if event.IsRunnerCompletion() {
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
        if event.IsRunnerCompletion() {
            break
        }
    }

    return nil
}
```

## 📝 总结

Runner 组件是 tRPC-Agent-Go 框架的核心，提供了完整的对话管理和 Agent 编排能力。通过合理使用会话管理、工具集成和事件处理，可以构建强大的智能对话应用。
