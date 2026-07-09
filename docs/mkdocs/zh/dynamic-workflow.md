# 动态工作流

动态工作流让一个普通 `LLMAgent` 在遇到复杂任务时，临时运行一段 workflow
代码去编排多个子 Agent。当前内置 `LocalRunner` 执行的是 Python workflow。

业务开发者通常不需要提前写这段 workflow 代码。你要做的是：

1. 准备一个或多个可被 workflow 调用的基础 Agent。
2. 创建 `run_workflow` 工具。
3. 把 `run_workflow` 挂到根 Agent 上。

如果只想先跑起来，读完“最小接入”和“一个完整例子”就够了。后面的章节主要解释
工具调用、并发、事件流和安全边界。

运行时大致是这样：

```text
用户请求
  ↓
根 Agent
  ├─ 简单任务：直接回答
  └─ 复杂任务：调用 run_workflow
        ↓
      模型生成临时 workflow 代码
        ↓
      workflow 通过 bridge/RPC 发起 agent(...) 调用
        ↓
      Go 进程内运行已注册的基础 Agent
        ↓
      子 Agent 事件继续进入 Runner event stream / Session Service
        ↓
      汇总结果并返回给根 Agent
```

适合动态工作流的任务通常需要临时拆分角色，例如：

```text
分析方案 → 让 reviewer 审核 → 按反馈修改 → 再次审核
```

如果流程稳定、确定、强业务约束，应直接写应用 Go 代码。如果只是普通工具之间的
循环、分支或 JSON 转换，应优先使用更轻量的 `execute_tool_code`。

Python 是当前内置运行时的工程选择，不是动态工作流的本质约束。workflow 代码不是
一段脱离框架的普通脚本，也不是在脚本里直接调用某个 Agent SDK；它始终通过显式
bridge/RPC 回到 Go 进程，由 Go 侧继续运行已注册的 Agent 和工具。因此使用 Go 作为
workflow 语言也不会获得直接访问宿主进程对象的优势。

## 最小接入

下面是最小接入方式：注册一个中性的基础 Agent，然后把 `run_workflow`
挂到根 Agent 上。

只注册一个基础 Agent 是常见做法。因为很多临时角色只是 instruction 不同，
模型、工具和权限边界都可以共用。只有这些边界真的不同时，才需要注册多个基础
Agent。

把下面片段放进应用自己的 Agent setup 代码里：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/dynamicworkflow"
)

// 根 Agent 和 workflow 内的子 Agent 可以共用同一个模型实例。
modelInstance := openai.New("gpt-5")

// 注册一个基础 Agent。workflow 代码后续会通过 agent(...) 调用它。
// 这个基础 Agent 固定模型、工具、权限等边界；临时角色由每次调用的 instruction 决定。
general := llmagent.New(
    "general_agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Base agent for workflow-local roles."),
    llmagent.WithInstruction(
        "Follow the dynamic instruction supplied for this workflow-local role.",
    ),
)

// 创建 run_workflow 工具。
// LocalRunner 会启动本地 Python 进程，只适合开发或已隔离的环境。
workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.LocalRunner{},
    []agent.Agent{general},
)
if err != nil {
    panic(err) // 生产代码中应按需处理错误
}

// 把 run_workflow 挂到根 Agent 上。
root := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction(
        "Answer simple requests directly. Use run_workflow for tasks that " +
            "need temporary child-agent collaboration.",
    ),
    llmagent.WithTools([]tool.Tool{workflow}),
)
```

这段代码只把 `run_workflow` 暴露给根 Agent。根 Agent 的其他工具不会自动进入
workflow。这样可以避免 workflow 意外获得写操作、凭证、shell 执行或控制面工具。

## 当前 Python workflow 里的 `agent(...)`

`agent(...)` 可以理解成：运行一次 Go 侧已注册的基础 Agent。

如果 `NewTool` 只注册了一个基础 Agent，workflow 可以直接调用：

```python
result = await agent(
    "Review this production change.",
    instruction="You are a strict production reviewer.",
)
return result["text"]
```

如果注册了多个基础 Agent，workflow 需要指定名字：

```python
result = await agent(
    "Review this production change.",
    template="reviewer",
)
```

这里的 `template` 只是“选择哪个基础 Agent”的字段名，不是一套额外的模板系统。

一次 `agent(...)` 调用可以临时指定角色：

```python
review = await agent(
    {"draft": draft},
    instruction="Review the draft and return approval plus feedback.",
    tools=[],
    structured_output={
        "type": "object",
        "properties": {
            "approved": {"type": "boolean"},
            "feedback": {"type": "string"},
        },
    },
)
```

常用选项只有几个：

- `instruction`：这次子 Agent 的临时角色说明。
- `tools` / `skills`：省略表示继承基础 Agent；`[]` 表示这次禁用；非空列表表示在基础 Agent 已有能力上收窄。
- `structured_output` / `schema`：要求这次子 Agent 返回结构化 JSON。
- `instance_id`：同一个 workflow 内多次调用复用同一个子 Agent 历史。

默认不传 `instance_id` 时，每次 `agent(...)` 调用都会创建独立的子 Agent
历史，适合并发分支。显式传相同 `instance_id` 表示复用同一条子历史；
并发调用同一个 `instance_id` 会被串行执行，避免同时读写同一段会话历史。

这些选项只影响当前这次子 Agent 调用。workflow 不能借它改变模型、权限策略，
也不能新增基础 Agent 本来没有的宿主能力。

## 一个完整例子

假设用户要求：

> Review the production change “Enable a new cache for the product catalog”:
> first analyze risk and rationale, then make an approval decision.

根 Agent 可以调用 `run_workflow`。模型可能生成并执行下面这段 workflow 代码：

```python
analysis = await agent(
    "Analyze the production change: Enable a new cache for the product catalog.",
    instruction="You are a technical analyst reviewing a production change.",
    structured_output={
        "type": "object",
        "properties": {
            "risks": {"type": "array", "items": {"type": "string"}},
            "rationale": {"type": "string"},
        },
    },
)

review = await agent(
    {
        "change": "Enable a new cache for the product catalog",
        "analysis": analysis["structured"],
    },
    instruction="You are a senior engineering reviewer for production changes.",
    structured_output={
        "type": "object",
        "properties": {
            "approved": {"type": "boolean"},
            "next_steps": {"type": "array", "items": {"type": "string"}},
        },
    },
)

return {
    "analysis": analysis["structured"],
    "decision": review["structured"],
}
```

注意：这段 workflow 代码通常是模型临时生成的；当前示例使用 Python。它不是业务预先
写死在 Go 里的代码。

第一次 `agent(...)` 让基础 Agent 临时扮演“技术分析员”，返回结构化风险分析。
第二次 `agent(...)` 把第一步的结构化结果作为输入，让同一个基础 Agent 临时扮演
“资深 reviewer”。最终返回值类似：

```json
{
  "analysis": {
    "risks": [
      "Cache invalidation can expose stale product information.",
      "Concurrent updates can introduce data-consistency issues."
    ],
    "rationale": "Caching reduces database load for a read-heavy catalog."
  },
  "decision": {
    "approved": true,
    "next_steps": [
      "Define cache invalidation and TTL policies.",
      "Add cache metrics and run a phased rollout."
    ]
  }
}
```

如果后续代码要稳定读取字段，应优先使用 `result["structured"]`。框架不会从自然语言
里猜字段、单位或业务含义。如果模型服务不支持 JSON Schema 响应格式，这次结构化
调用可能失败；不需要稳定字段时，可以不传 `structured_output`。

## 并发与批处理

`parallel` 用于同时执行互不依赖的分支，并按输入顺序返回结果：

```python
reviews = await parallel([
    lambda: agent({"plan": plan}, instruction="Review security risk."),
    lambda: agent({"plan": plan}, instruction="Review operational risk."),
])
```

注意，`parallel` 的返回值按输入顺序排列，但 event stream 是实时完成顺序。
两个并发子 Agent 的 partial、tool call 和 final 事件可能交错出现。前端或
消费者应通过 `InvocationID`、`ParentMetadata`、`FilterKey` 等字段把事件
归到对应分支，而不是依赖全局事件顺序。

`pipeline(items, stage1, stage2, ...)` 用于对一批对象执行重复的多阶段处理。
每个 item 会按 stage 顺序前进；一个 item 完成前一阶段后，就可以进入下一阶段，
不需要等待整批 item。

```python
async def analyze(previous, original, index):
    return await agent({"file": original}, instruction="Analyze this file.")

async def verify(analysis, original, index):
    return await agent(
        {"file": original, "analysis": analysis["structured"]},
        instruction="Verify the analysis.",
    )

results = await pipeline(files, analyze, verify)
```

## 在 workflow 代码里调用工具：`WithCodeCallableTools` 与 `call_tool`

最小接入不需要 `dynamicworkflow.WithCodeCallableTools`。此时 workflow 代码主要通过
`agent(...)` 编排子 Agent。

如果确实需要让 workflow 代码直接调用普通业务工具，可以在创建工具时显式传入：

```go
workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.LocalRunner{},
    []agent.Agent{general},
    dynamicworkflow.WithCodeCallableTools(searchCatalog, createQuote),
)
```

然后 workflow 代码可以调用：

```python
facts = await call_tool("search_catalog", query="trail backpack")
```

`call_tool` 只能调用 `WithCodeCallableTools` 显式传入的工具。它不会自动看到根 Agent 的工具。

不要把执行类工具、`run_workflow` 自身、`execute_tool_code`、`transfer_to_agent`、
`await_user_reply`、workspace 工具或 AgentTool 放进 `WithCodeCallableTools`。这些工具容易形成
递归或混合控制流边界；workflow 调用子 Agent 应使用 `agent(...)`。

## 事件、Session 与执行边界

Dynamic Workflow 第一版是前台、一次性执行。workflow 代码只负责表达编排逻辑；
真正的子 Agent 执行仍发生在 Go 进程里。`agent(...)` 不是脚本里断开框架联系的
一次普通 SDK 调用，而是一次通过 bridge/RPC 回到宿主侧的 Agent 调用。

实现上，子 Agent 会从父 invocation 派生出新的 invocation：它复用父执行里的
Session、SessionService、Plugin 和事件转发通道，但拥有新的 InvocationID、输入
Message、ParentMetadata 和独立的 event filter key。因此，子 Agent 的 LLM 上下文和
事件分支仍然是隔离的，不会简单混进根 Agent 的当前提示词上下文。

因此，子 Agent 的输出事件会继续回到当前 Runner 的事件流里：

- 前端可以从同一个 event stream 看到子 Agent 输出和工具调用进度。
- 配置的 Session Service 会持久化这些事件。
- `parallel` 分支的事件可能交错出现；这是实时流语义，不影响
  `parallel(...)` 返回值仍按输入顺序排列。

这也是 Dynamic Workflow 和“让模型写一个普通脚本自己跑完”的关键区别：临时
workflow 具备代码的灵活性，但 Agent 执行、工具边界、事件流和 Session 持久化仍由
Go 框架掌控。

`dynamicworkflow.LocalRunner` 会启动本地 Python 进程。它不是安全 sandbox。
生产环境应提供自己的 `dynamicworkflow.Runtime`，例如容器、microVM 或远端 sandbox，
并在里面落实文件系统、网络、进程、依赖和资源限制。

生成的 workflow 代码应该调用宿主工具，而不是直接调用 HTTP API。认证、授权、
重试、幂等、审计、限流和 API 版本适配仍应由业务工具在 Go 侧掌控。

## 如何选择能力

| 需求 | 推荐方式 |
| --- | --- |
| 稳定、已知、强业务约束的流程 | 应用 Go 代码 |
| 普通工具之间的循环、分支、JSON 转换 | `execute_tool_code` |
| 临时子 Agent 分工、审核、并发分析、反复修改 | `run_workflow` |

默认不要向同一个根 Agent 同时暴露 `execute_tool_code` 和 `run_workflow`。
两者都是代码编排路径，同时暴露会增加模型选择难度。

完整可运行代码见 [Dynamic Workflow Agent 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/dynamicworkflow)。

## 后续计划：文件化 workflow

这一节是后续设计备忘，普通使用者可以跳过。

当前版本是一次性的：模型把 inline workflow 代码传给 `run_workflow` 并立即执行。

后续可以扩展文件化执行，让 workflow 脚本像普通 workspace 文件一样被写入、
编辑、review 和重复运行。这个方向应作为 `run_workflow` 的 source 选择扩展，
而不是在本包里引入完整的 workflow 管理系统。

关键约束：

- 未来入参应在 inline `code` 与 workspace `file` 中二选一，并可加 JSON `args`。
- 文件路径应是 workspace 相对路径，不是任意宿主机路径。
- file resolver 应复用父 invocation 的 `codeexecutor` workspace。
- workflow 工具只负责运行已有脚本，不负责提供脚本创作 API。
- 文件化 workflow 持久化的是脚本源码，不是执行状态；resume、checkpoint、发布、
  跨节点存储都需要单独设计。
- 文件化版本仍应保留当前 Runtime 边界：只能编排已注册 Agent 和显式授权的宿主工具，
  不能因此获得无边界文件系统或 shell 访问。
