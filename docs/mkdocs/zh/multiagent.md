# 多 Agent 系统 (Multi-Agent System)

多 Agent 系统是 trpc-agent-go 框架的核心功能之一，允许您创建由多个专门化 Agent 组成的复杂系统。这些 Agent 可以以不同的方式协作，实现从简单到复杂的各种应用场景。

## 概述

多 Agent 系统基于 SubAgent 概念构建，通过 `WithSubAgents` option 实现各种协作模式：

### 基础概念

- **SubAgent** - 通过 `WithSubAgents` option 配置的专门化 Agent，是构建复杂协作模式的基础

### 核心协作模式

1. **链式 Agent (ChainAgent)** - 使用 SubAgent 按顺序执行，形成处理流水线
2. **并行 Agent (ParallelAgent)** - 使用 SubAgent 同时处理同一输入的不同方面
3. **循环 Agent (CycleAgent)** - 使用 SubAgent 在循环中迭代，直到满足特定条件

### 辅助功能

- **Agent 工具 (AgentTool)** - 将 Agent 包装成工具，供其他 Agent 调用
- **Agent 委托 (Agent Transfer)** - 通过 `transfer_to_agent` 工具实现 Agent 间的任务委托
- **等待用户回复路由** - 让 Agent 在需要补充信息时显式接管下一条用户消息
- **Team** - 更高层的团队编排封装，支持协调者团队与 Swarm（见 `team` 包）

## SubAgent 基础

SubAgent 是多 Agent 系统的核心概念，通过 `WithSubAgents` option 实现。它允许您将多个专门化的 Agent 组合在一起，构建复杂的协作模式。

### SubAgent 的作用

- **专业化分工**：每个 SubAgent 专注于特定领域或任务类型
- **模块化设计**：将复杂系统分解为可管理的组件
- **灵活组合**：可以根据需要组合不同的 SubAgent
- **统一接口**：所有协作模式都基于相同的 `WithSubAgents` 机制

### 基本用法

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

// 创建 SubAgent
mathAgent := llmagent.New(
    "math-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("处理数学计算和数值问题"),
    llmagent.WithInstruction("你是数学专家，专注于数学运算和数值推理..."),
)

weatherAgent := llmagent.New(
    "weather-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("提供天气信息和建议"),
    llmagent.WithInstruction("你是天气专家，提供天气分析和活动建议..."),
)

// 使用 WithSubAgents option 配置 SubAgent
mainAgent := llmagent.New(
    "coordinator-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("协调者 Agent，负责任务委托"),
    llmagent.WithInstruction("你是协调者，分析用户请求并委托给合适的专家..."),
    llmagent.WithSubAgents([]agent.Agent{mathAgent, weatherAgent}),
)
```

## 核心协作模式

所有协作模式都基于 SubAgent 概念，通过不同的执行策略实现：

### 链式 Agent (ChainAgent)

链式 Agent 使用 SubAgent 按顺序连接，形成处理流水线。每个 SubAgent 专注于特定任务，并将结果传递给下一个 SubAgent。

#### 使用场景

- **内容创作流程**：规划 → 研究 → 写作
- **问题解决流程**：分析 → 设计 → 实现
- **数据处理流程**：收集 → 清洗 → 分析

#### 基本用法

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

// 创建 SubAgent
planningAgent := llmagent.New("planning-agent", ...)
researchAgent := llmagent.New("research-agent", ...)
writingAgent := llmagent.New("writing-agent", ...)

// 创建链式 Agent，使用 WithSubAgents 配置 SubAgent
chainAgent := chainagent.New(
    "multi-agent-chain",
    chainagent.WithSubAgents([]agent.Agent{
        planningAgent, 
        researchAgent, 
        writingAgent,
    }),
)
```

#### 示例会话

```
🔗 多 Agent 链式演示
链式流程：规划 → 研究 → 写作
==================================================

👤 用户：解释可再生能源的好处

📋 规划 Agent：我将创建一个结构化的分析计划...

🔍 研究 Agent：
🔧 使用工具：
   • web_search (ID: call_123)
🔄 执行中...
✅ 工具结果：最新的可再生能源数据...

✍️ 写作 Agent：基于规划和研究：
[结构化的综合回答]
```

### 并行 Agent (ParallelAgent)

并行 Agent 使用 SubAgent 同时处理同一输入的不同方面，提供多角度的分析。

#### 使用场景

- **商业决策分析**：市场分析、技术评估、风险评估、机会分析
- **多维度评估**：不同专家同时评估同一问题
- **快速并行处理**：需要同时获得多个视角的场景

#### 基本用法

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
)

// 创建 SubAgent
marketAgent := llmagent.New("market-analysis", ...)
technicalAgent := llmagent.New("technical-assessment", ...)
riskAgent := llmagent.New("risk-evaluation", ...)
opportunityAgent := llmagent.New("opportunity-analysis", ...)

// 创建并行 Agent，使用 WithSubAgents 配置 SubAgent
parallelAgent := parallelagent.New(
    "parallel-demo",
    parallelagent.WithSubAgents([]agent.Agent{
        marketAgent,
        technicalAgent, 
        riskAgent,
        opportunityAgent,
    }),
)
```

#### 示例会话

```
⚡ 并行多 Agent 演示
Agent：市场 📊 | 技术 ⚙️ | 风险 ⚠️ | 机会 🚀
==================================================

💬 用户：我们应该为供应链跟踪实施区块链吗？

🚀 开始并行分析："我们应该为供应链跟踪实施区块链吗？"
📊 Agent 正在分析不同角度...
────────────────────────────────────────────────────────────────────────────────

📊 [market-analysis] 开始分析...
⚙️ [technical-assessment] 开始分析...
⚠️ [risk-evaluation] 开始分析...
🚀 [opportunity-analysis] 开始分析...

📊 [market-analysis]: 区块链供应链市场正在经历强劲增长，年复合增长率为67%...

⚙️ [technical-assessment]: 实施需要分布式账本基础设施和共识机制...

⚠️ [risk-evaluation]: 主要风险包括40%目标市场的监管不确定性...

🚀 [opportunity-analysis]: 战略优势包括增强透明度，可带来15-20%的成本降低...

🎯 所有并行分析成功完成！
────────────────────────────────────────────────────────────────────────────────
✅ 多角度分析在4.1秒内完成
```

### 循环 Agent (CycleAgent)

循环 Agent 使用 SubAgent 在迭代循环中运行，直到满足特定条件（如质量阈值或最大迭代次数）。

#### 使用场景

- **内容优化**：生成 → 评估 → 改进 → 重复
- **问题解决**：提出 → 评估 → 增强 → 重复
- **质量保证**：草稿 → 审查 → 修订 → 重复

#### 基本用法

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
)

// 创建 SubAgent
generateAgent := llmagent.New("generate-agent", ...)
criticAgent := llmagent.New("critic-agent", ...)

// 创建循环 Agent，使用 WithSubAgents 配置 SubAgent
cycleAgent := cycleagent.New(
    "cycle-demo",
    cycleagent.WithSubAgents([]agent.Agent{
        generateAgent,
        criticAgent,
    }),
    cycleagent.WithMaxIterations(3),
    cycleagent.WithEscalationFunc(qualityEscalationFunc),
)
```

#### 退出函数（WithEscalationFunc）

你可以把 `WithEscalationFunc` 理解为“退出函数”：当回调返回 `true`，
`CycleAgent` 就会退出循环。代码里叫 `EscalationFunc`，这里的
Escalation 只是指控制流上抛 / 中止循环，并不是“版本升级”。

`CycleAgent` 会按顺序运行 SubAgent，然后重复整套流程。它会在以下任一
情况发生时停止：

1. 你的 `EscalationFunc` 对某个事件返回 `true`
2. 达到 `WithMaxIterations(n)` 设定的上限
3. `context.Context` 被取消（超时 / 手动取消）

##### `EscalationFunc` 会收到什么？

回调签名如下：

```go
type EscalationFunc func(*event.Event) bool
```

它会在子 Agent 产生并被转发出来的事件上执行。为了避免在流式输出的
“半截 token”上误判，`CycleAgent` 只会在更“关键”的事件上做检查，
例如：

- 错误事件（`evt.Error != nil`）
- 工具结果事件（`evt.Object == model.ObjectTypeToolResponse`）
- 最终完成事件（`evt.Done == true`，非 streaming chunk）

##### 默认行为

如果你不设置 `WithEscalationFunc`，`CycleAgent` 只会在遇到错误时停止。

##### 示例：基于质量阈值停止

一个常见的循环是：**生成 → 评审 → 达标就停**。

让评审 Agent 通过工具返回一个“机器可读”的信号（例如 `record_score`
工具返回 JSON，其中包含 `needs_improvement`）。当
`needs_improvement` 变为 `false` 时立即停止（需要引入
`encoding/json`）：

```go
type scoreResult struct {
	NeedsImprovement bool `json:"needs_improvement"`
}

func qualityEscalationFunc(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	if evt.Error != nil {
		return true
	}
	if evt.Object != model.ObjectTypeToolResponse {
		return false
	}

	for _, choice := range evt.Response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleTool {
			continue
		}

		var res scoreResult
		if err := json.Unmarshal([]byte(msg.Content), &res); err != nil {
			continue
		}
		return !res.NeedsImprovement
	}
	return false
}
```

提示：这个函数运行在事件循环里，建议保持轻量、无副作用，并做好
`nil` / 解析失败的防御处理。

#### 示例会话

```
🔄 多 Agent 循环演示
最大迭代次数：3
循环：生成 → 评估 → 改进 → 重复
==================================================

👤 用户：写一个短笑话

🤖 循环响应：

🤖 生成 Agent：为什么骷髅不互相打架？
因为它们没有胆量！

👀 评估 Agent：
🔧 使用工具：
   • record_score (ID: call_123)
🔄 执行中...
✅ 质量评分：75/100
⚠️ 需要改进 - 继续迭代

🔄 **第2次迭代**

🤖 生成 Agent：这是一个改进版本，有新的转折：
**为什么骷髅从不赢得争论？**
因为它们总是在中途失去脊梁！

👀 评估 Agent：
🔧 使用工具：
   • record_score (ID: call_456)
🔄 执行中...
✅ 质量评分：85/100
🎉 质量阈值达到 - 循环完成

🏁 循环在2次迭代后完成
```

## 辅助功能

### Agent 工具 (AgentTool)

Agent 工具是构建复杂多 Agent 系统的重要基础功能，它允许您将任何 Agent 包装成可调用的工具，供其他 Agent 或应用程序使用。

#### 使用场景

- **专业化委托**：不同 Agent 处理特定类型的任务
- **工具集成**：Agent 可以作为工具集成到更大的系统中
- **模块化设计**：可重用的 Agent 组件可以组合在一起
- **复杂工作流**：涉及多个专门化 Agent 的复杂工作流

#### 基本用法

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 创建专门的 Agent
mathAgent := llmagent.New(
    "math-specialist",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("专门处理数学运算的 Agent"),
    llmagent.WithInstruction("你是一个数学专家，专注于数学运算、计算和数值推理..."),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
)

// 将 Agent 包装成工具
agentTool := agenttool.NewTool(
    mathAgent,
    // 默认 skip summarization=false，当设置为 true 时会在 tool.response 后直接结束本轮
    agenttool.WithSkipSummarization(false),
    // 开启转发：把子 Agent 的流式事件内联到父流程
    agenttool.WithStreamInner(true),
)

// 在主 Agent 中使用 Agent 工具
mainAgent := llmagent.New(
    "chat-assistant",
    llmagent.WithTools([]tool.Tool{timeTool, agentTool}),
)
```

#### Agent工具架构

```
聊天助手 (主 Agent)
├── 时间工具 (函数)
└── 数学专家 Agent 工具 (Agent)
    └── 数学专家 Agent (专门化 Agent)
        └── 计算器工具 (函数)
```

#### 示例会话

```
🚀 Agent 工具示例
模型：deepseek-chat
可用工具：current_time, math-specialist
==================================================

👤 用户：计算 923476 * 273472354

🤖 助手：我将使用数学专家 Agent 来计算这个结果。

🔧 工具调用已启动：
   • math-specialist (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1)
     参数：{"request":"计算 923476 乘以 273472354"}

🔄 执行工具中...
✅ 工具响应 (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1)：
"计算 923,476 乘以 273,472,354 的结果是：

\[
923,\!476 \times 273,\!472,\!354 = 252,\!545,\!155,\!582,\!504
\]"

✅ 工具执行完成。
```

#### 流式内部转发（StreamInner）

当为 Agent 工具启用 `WithStreamInner(true)` 时：

- 子 Agent 的事件会以流式形式转发到父流程（`event.Event`），可直接显示 `choice.Delta.Content`
- 为避免重复打印，子 Agent 最终的整段文本默认不会作为转发事件再次输出；但会被聚合写入最终的 `tool.response`，用于满足模型“工具消息跟随”的要求
- 如果你想保留内部进度、但隐藏子 Agent 的 assistant 正文，可以再加上 `WithInnerTextMode(agenttool.InnerTextModeExclude)`
- 建议在 UI 层：
  - 展示子 Agent 转发的增量内容
  - 如非调试，不再额外打印最终聚合的工具响应内容

示例：在事件循环中区分外层助手/子 Agent/工具响应

```go
// 子 Agent 转发的增量（作者不是父 Agent）
if ev.Author != parentName && ev.Response != nil && len(ev.Response.Choices) > 0 {
    if delta := ev.Response.Choices[0].Delta.Content; delta != "" {
        fmt.Print(delta)
    }
    return
}

// 工具响应（包含聚合内容），默认不打印，避免重复
if ev.Response != nil && ev.Object == model.ObjectTypeToolResponse {
    // ...按需展示或忽略
    return
}
```

#### 选项对照

- `WithSkipSummarization(false)`：默认，工具返回后允许外层模型再总结一次
- `WithSkipSummarization(true)`：工具返回后跳过外层模型的总结调用
- `WithStreamInner(true)`：启用子 Agent 事件转发（父/子 Agent 建议都 `Stream: true`）
- `WithStreamInner(false)`：按普通可调用工具处理，不转发内部流
- `WithInnerTextMode(agenttool.InnerTextModeInclude)`：启用内部转发时，继续展示子 Agent 的 assistant 文本
- `WithInnerTextMode(agenttool.InnerTextModeExclude)`：保留内部进度事件，但不再转发子 Agent 的 assistant 正文

### Agent 委托 (Agent Transfer)

Agent 委托通过 `transfer_to_agent` 工具实现 Agent 间的任务委托，允许主 Agent 根据任务类型自动选择合适的 SubAgent。

#### 使用场景

- **任务分类**：根据用户请求自动选择合适的 SubAgent
- **智能路由**：将复杂任务路由到最合适的处理者
- **专业化处理**：每个 SubAgent 专注于特定领域
- **无缝切换**：在 SubAgent 之间无缝切换，保持对话连续性

#### 基本用法

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 创建 SubAgent
mathAgent := llmagent.New(
    "math-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("处理数学计算和数值问题"),
    llmagent.WithInstruction("你是数学专家，专注于数学运算和数值推理..."),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
)

weatherAgent := llmagent.New(
    "weather-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("提供天气信息和建议"),
    llmagent.WithInstruction("你是天气专家，提供天气分析和活动建议..."),
    llmagent.WithTools([]tool.Tool{weatherTool}),
)

// 创建协调者 Agent，使用 WithSubAgents 配置 SubAgent
coordinatorAgent := llmagent.New(
    "coordinator-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("协调者 Agent，负责任务委托"),
    llmagent.WithInstruction("你是协调者，分析用户请求并委托给合适的专家..."),
    llmagent.WithSubAgents([]agent.Agent{mathAgent, weatherAgent}),
)
```

#### 动态 SubAgent 发现（结合 A2A）

在真实系统中，SubAgent 往往是通过 A2A 协议暴露的远程 Agent，
它们的列表会随着时间变化（例如从注册中心动态上下线）。

为支持这一场景，`LLMAgent` 实现了 `agent.SubAgentSetter`
接口，可以在运行时刷新 SubAgent 列表，而无需重建协调者：

```go
import (
    "fmt"
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
)

func refreshSubAgents(ctx context.Context, ag agent.Agent) error {
    cfg, ok := ag.(agent.SubAgentSetter)
    if !ok {
        return fmt.Errorf("agent does not support dynamic SubAgents")
    }

    // 1. 从注册中心或配置源发现远程 Agent。
    urls := []string{
        "http://localhost:8087/",
        "http://localhost:8088/",
    }

    // 2. 为每个远程 Agent 构建 A2AAgent 代理。
    subAgents := make([]agent.Agent, 0, len(urls))
    for _, url := range urls {
        a2, err := a2aagent.New(a2aagent.WithAgentCardURL(url))
        if err != nil {
            // 生产环境中建议记录日志并跳过失败项。
            continue
        }
        subAgents = append(subAgents, a2)
    }

    // 3. 原子性地替换协调者上的 SubAgents。
    cfg.SetSubAgents(subAgents)
    return nil
}
```

这种模式可以让你：

- 与任意注册中心集成（服务发现、数据库、配置文件等）
- 动态增加或移除远程 SubAgent
- 保持 `Runner` 和会话管理逻辑不变，因为协调者始终是同一
  个 Agent 实例

#### Agent委托架构

```
协调者 Agent (主入口)
├── 分析用户请求
├── 选择合适的 SubAgent
└── 使用 transfer_to_agent 工具委托任务
    ├── 数学 SubAgent (数学计算)
    ├── 天气 SubAgent (天气信息)
    └── 研究 SubAgent (信息搜索)
```

#### 示例会话

```
🔄 Agent 委托演示
可用 SubAgent：math-agent, weather-agent, research-agent
==================================================

👤 用户：计算复利，本金5000美元，年利率6%，期限8年

🎯 协调者：我将把这个任务委托给我们的数学专家进行准确计算。
🔄 启动委托...
🔄 委托事件：将控制权转移给 Agent：math-agent

🧮 数学专家：我将帮助您逐步计算复利。
🔧 🧮 执行工具：
   • calculate ({"operation":"power","a":1.06,"b":8})
   ✅ 工具完成
🔧 🧮 执行工具：
   • calculate ({"operation":"multiply","a":5000,"b":1.593})
   ✅ 工具完成

复利计算结果：
- 本金：$5,000
- 年利率：6%
- 期限：8年
- 结果：$7,969.24（利息约$2,969.24）
```

#### 跨轮追问用户

`transfer_to_agent` 解决的是 **当前这一轮由谁处理**。它本身并不会告诉
`Runner`：**下一条用户消息** 应该继续交给谁。

当 SubAgent 发起追问时，这个问题就会暴露出来：

1. coordinator transfer 给某个 SubAgent
2. SubAgent 向用户追问缺失信息
3. 用户在下一次请求里补充信息
4. 默认情况下，`Runner` 还是会从常规入口 Agent 开始

更干净的做法是把这件事做成 **显式**、**一次性** 的路由：

- 在 Runner 上开启 `runner.WithAwaitUserReplyRouting(true)`
- 对可能追问用户的 Agent，开启
  `llmagent.WithAwaitUserReplyTool(true)`
- 在这些 Agent 的 instruction 里明确要求模型：如果要向用户补字段，
  先调用 `await_user_reply`，再发出追问

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

profileAgent := llmagent.New(
    "profile-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("负责采集并更新客户资料。"),
    llmagent.WithInstruction(`
你负责更新客户资料。

如果缺少必要字段：
1. 先调用 await_user_reply
2. 再向用户提出一个明确的问题
3. 然后等待下一条用户消息

字段齐全后，再调用 update_profile。
`),
    llmagent.WithAwaitUserReplyTool(true),
    llmagent.WithTools([]tool.Tool{updateProfileTool}),
)

coordinatorAgent := llmagent.New(
    "coordinator-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction(`
用户要更新资料时，直接 transfer 给 profile-agent。
不要自己收集资料字段。
`),
    llmagent.WithSubAgents([]agent.Agent{profileAgent}),
)

r := runner.NewRunner(
    "crm-app",
    coordinatorAgent,
    runner.WithAwaitUserReplyRouting(true),
)

// 第 1 轮：
// user: "帮我更新资料"
// coordinator-agent -> transfer_to_agent(profile-agent)
// profile-agent -> await_user_reply + "请问要保存的手机号是多少？"

// 第 2 轮（同一个 session）：
// user: "+1-555-0101"
// Runner 这一轮会直接从 profile-agent 开始。
events, err := r.Run(
    context.Background(),
    "user-1",
    "session-1",
    model.NewUserMessage("+1-555-0101"),
)
if err != nil {
    // handle error
}
for evt := range events {
    if evt.IsRunnerCompletion() {
        break
    }
}
```

这套能力的行为边界：

- 这条路由只消费一次；下一条用户消息开始时就会被清掉
- 显式指定的 `agent.WithAgent(...)` 或 `agent.WithAgentByName(...)`
  仍然优先
- 如果目标 Agent 路径已经失效，`Runner` 会回退到默认入口 Agent，
  并清理掉脏路由
- 对最常见的 “coordinator + WithSubAgents” 结构，`Runner`
  会按完整的 Agent 链路径恢复，不需要你把每个 SubAgent 再额外注册一遍

如果你不是用 `LLMAgent`，而是自己实现 `agent.Agent`，请看 `runner`
文档里的底层 API：`agent.MarkAwaitingUserReply(...)`。

## 环境变量配置

所有多 Agent 示例都需要以下环境变量：

| 变量名 | 必需 | 默认值 | 说明 |
|--------|------|--------|------|
| `OPENAI_API_KEY` | 是 | - | OpenAI API 密钥 |
| `OPENAI_BASE_URL` | 否 | `https://api.openai.com/v1` | OpenAI API 基础URL |

## 运行示例

所有示例代码位于 [examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples)

### 核心协作模式示例

#### 链式 Agent 示例

```bash
cd examples/multiagent/chain
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### 并行 Agent 示例

```bash
cd examples/multiagent/parallel
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### 循环 Agent 示例

```bash
cd examples/multiagent/cycle
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat -max-iterations 5
```

### 辅助功能示例

#### Agent 工具示例

```bash
cd examples/agenttool
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### Agent 委托示例

```bash
cd examples/transfer
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

## 自定义和扩展

### 添加新的 Agent

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// 创建自定义 Agent
customAgent := llmagent.New(
    "custom-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("自定义 Agent 描述"),
    llmagent.WithInstruction("自定义指令"),
    llmagent.WithTools([]tool.Tool{customTool}),
)

// 集成到多 Agent 系统中
chainAgent := chainagent.New(
    "custom-chain",
    chainagent.WithSubAgents([]agent.Agent{
        existingAgent,
        customAgent,  // 添加自定义 Agent
    }),
)
```

### 配置工具

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 创建自定义工具
customTool := function.NewFunctionTool(
    customFunction,
    function.WithName("custom_tool"),
    function.WithDescription("自定义工具描述"),
)

// 为 Agent 分配工具
agent := llmagent.New(
    "tool-agent",
    llmagent.WithTools([]tool.Tool{customTool}),
)
```

### 调整参数

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// 配置生成参数
genConfig := model.GenerationConfig{
    MaxTokens:   intPtr(500),
    Temperature: floatPtr(0.7),
    Stream:      true,
}

// 应用到 Agent
agent := llmagent.New(
    "configured-agent",
    llmagent.WithGenerationConfig(genConfig),
)
```
