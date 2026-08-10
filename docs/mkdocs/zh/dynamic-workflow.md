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
      子 Agent 事件继续进入同一个 event stream / Session Service
        ↓
      汇总结果并返回给根 Agent
```

适合动态工作流的任务通常需要临时拆分角色，例如：

```text
分析方案 → 让 reviewer 审核 → 按反馈修改 → 再次审核
```

如果流程稳定、确定、强业务约束，应直接写应用 Go 代码。如果只是普通工具之间的
循环、分支或 JSON 转换，应优先使用更轻量的 `execute_tool_code`。

workflow 语言是 Runtime 的选择，不是 Dynamic Workflow 的本质约束。当前内置
Runtime 使用 Python；对已注册 Agent 和工具的调用会通过显式 bridge/RPC 回到 Go
宿主，而不是在脚本中运行另一套 Agent SDK。

模型生成的 workflow 代码应保持为简短的编排胶水：只表达角色委派、数据流、分支、
并发和有界循环，具体的调研、写作、编码或工具操作交给子 Agent。不要把任务报告、
源码文件或大段 shell 脚本直接嵌入 workflow 代码。

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
    // 让每个临时角色只关注自己的分支；同一 workflow 请求内复用
    // instance_id 时仍会共享对应历史。
    llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
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
- `model`：宿主通过 `WithAgentModelProfile` 注册 profile 后可选的模型别名；
  省略则继承模板模型。
- `tools` / `skills`：省略表示继承基础 Agent；`[]` 表示这次禁用；非空列表表示在基础 Agent 已有能力上收窄。
- `structured_output` / `schema`：要求这次子 Agent 返回结构化 JSON。
- `instance_id`：同一个 workflow 内多次调用复用同一个子 Agent 历史。

### 宿主授权的模型 profile

默认情况下，每次子 Agent 调用使用模板 Agent 已注册的模型。若要让 workflow
在少数宿主持有模型之间选择，可注册 profile 别名：

```go
fast := openai.New("gpt-5-mini")
deep := openai.New("gpt-5")

workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.LocalRunner{},
    []agent.Agent{general},
    dynamicworkflow.WithAgentModelProfile(
        "fast",
        "Low-latency drafting and simple extraction.",
        fast,
    ),
    dynamicworkflow.WithAgentModelProfile(
        "deep",
        "Careful review and multi-step reasoning.",
        deep,
    ),
)
```

workflow 可在单次子 Agent 调用中选择 profile：

```python
draft = await agent(
    "Write a short draft.",
    instruction="Draft quickly.",
    model="fast",
)
review = await agent(
    {"draft": draft["text"]},
    instruction="Review carefully.",
    model="deep",
)
```

profile 是白名单。每个 `model.Model` 实例由宿主持有；workflow 代码不能传入
提供商模型标识符，也不能自行构造模型。省略 `model` 会完整保留模板模型。只有
在有明确任务原因时才应选择覆盖。选中的 profile 对 `LLMAgent` 模板，以及会遵守
invocation surface patch 的自定义 Agent 生效；其他 Agent 仍使用各自已配置的模型。

默认不传 `instance_id` 时，每次 `agent(...)` 调用都会创建独立的子 Agent
历史，适合并发分支。对于 `LLMAgent` 模板，如果临时角色不应继承根分支对话，
请像上例一样配置 `llmagent.IsolatedRequest`。显式传相同 `instance_id` 表示
复用同一条子历史；并发调用同一个 `instance_id` 会被串行执行，避免同时读写
同一段会话历史。

复用的历史包含子 Agent 的输入和产生的事件。动态 `instruction` 只配置当前这次
调用，不会作为对话消息持久化；后续调用必须记住的事实应放在 `input` 中。

这些选项只影响当前这次子 Agent 调用。workflow 不能借它改变权限策略、发明模型
接入点，也不能新增基础 Agent 本来没有的宿主能力。模型选择仅限于宿主通过
`WithAgentModelProfile` 注册的别名。

`agent(...)` 返回的 envelope 包含 `text`、可选的 `structured` 结果和执行元数据。
下游需要普通文本时应传 `result["text"]`，需要稳定字段时传
`result["structured"]`；除非确实需要元数据，否则不要继续传递完整 envelope。
后续分支或循环需要稳定字段时，应请求 `schema` 并使用 `structured`，不要要求子
Agent 返回 JSON 文本后再在 workflow 中解析。

如果一个角色必须明确调用工具，而后续代码又需要结构化判断，应拆成两次子 Agent
调用：先获取未结构化但有工具依据的文本，再把这份证据交给带 `schema`、且
`tools=[]` 的子 Agent。这样无需依赖模型服务在同一轮里同时支持工具调用和结构化
响应模式。

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

`parallel` 用于同时执行互不依赖的分支，并按输入顺序返回结果；失败的独立分支返回
`None`：

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

每个 stage 可以接收一、二或三个位置参数：

- `stage(previous)`
- `stage(previous, original)`
- `stage(previous, original, index)`

第一阶段的 `previous` 就是原始 item。简单阶段可以只写一个参数；后续阶段仍可按需
读取原始 item 和 index。所有 stage 的签名会在任何 item 启动前完成校验。如果某个
stage 失败或返回 `None`，该 item 的最终结果就是 `None`，后续 stage 不再执行。

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

`agent(..., tools=[...])` 只是授权子 Agent 使用这些工具，并不保证模型一定调用；
结构化输出约束的是子 Agent 最终回答的形状，而不是字段的数据来源。如果 Python
控制流必须使用宿主工具的精确结果，应先通过 `call_tool` 取得原始数据，再交给
Agent 解读。

不要把执行类工具、`run_workflow` 自身、`execute_tool_code`、`transfer_to_agent`、
`await_user_reply`、workspace 工具或 AgentTool 放进 `WithCodeCallableTools`。这些工具容易形成
递归或混合控制流边界；workflow 调用子 Agent 应使用 `agent(...)`。

## 事件、Session 与执行边界

Dynamic Workflow 采用前台、一次性执行。workflow 代码负责表达编排逻辑，已注册的
Agent 和工具仍在 Go 宿主中运行。省略 `instance_id` 时，每次子 Agent 调用都会获得
独立的会话分支；显式复用同一个 `instance_id` 的调用会共享对应子历史。所有子 Agent
调用仍属于当前运行；按上例配置 `IsolatedRequest` 后，子 Agent 只看到自己的分支。
如果基础 Agent 选择了更宽的历史模式，它也可能看到祖先上下文。因此：

- 前端可以从同一个 event stream 看到子 Agent 输出和工具调用进度。
- 配置的 Session Service 会持久化这些事件。
- `parallel` 分支的事件可能交错出现；这是实时流语义，不影响
  `parallel(...)` 返回值仍按输入顺序排列。

event stream 遵循框架统一的流式消费约定：持续消费直到运行结束；如果提前停止，应取消
本次运行的 context。

workflow 执行不具备事务性。如果子 Agent 或代码可调用工具已经修改了外部状态，而后续
步骤失败，这些副作用不会自动回滚。当根 Agent 或应用可能重试 workflow 时，应让有
副作用的操作保持串行，并尽量具备幂等性。

这也是 Dynamic Workflow 和“让模型写一个普通脚本自己跑完”的关键区别：临时
workflow 具备代码的灵活性，但 Agent 执行、工具边界、事件流和 Session 持久化仍由
Go 框架掌控。

`dynamicworkflow.LocalRunner` 会通过共享的 local Python runtime 启动本地 Python
进程。它不是安全 sandbox。它会为本地使用提供 defense-in-depth 防护，包括限制 Python 语法、限制 builtins、
限制源码大小、限制捕获输出、使用最小进程环境、默认使用空的临时工作目录、将 bootstrap
脚本放在私有目录、尽力终止 guest 进程（Unix-like 系统下会清理进程组），以及通过
`dynamicworkflow.NewLocalRunner(dynamicworkflow.LocalRunnerConfig{Timeout: ...})`
配置的可选全流程 timeout。
默认 timeout 会刻意保持未设置，LocalRunner 只继承调用方 context，避免意外截断
耗时较长的 Agent workflow。

推荐直接编写以 `return` 结束的 workflow body。为了兼容模型常见输出，如果整段
workflow 只有一个可选 docstring 和一个无参数的 `async def run()` 或
`async def main()`，LocalRunner 也会自动调用它；其他未调用的 helper 仍会校验失败。

相较之前的 LocalRunner，强化后的默认行为不再继承宿主环境，默认使用空的临时工作
目录，会拒绝超过 64 KiB 的生成源码（除非显式调整限制），并执行文档声明的受限
Python 子集。这些是有意的行为变化，但不会构成安全 sandbox 边界。

需要本地 OS 隔离时，可以直接使用内置 sandbox runner：

```go
workflow, err := dynamicworkflow.NewTool(
    dynamicworkflow.NewSandboxRunner(),
    childAgents,
)
```

使用上面不带 option 的构造方式时，每次 workflow 都会使用一次性 workspace 和
clean process environment。该 runner 复用 `codeexecutor/sandbox`，默认限制网络；
sandbox 初始化失败时会直接报错，不会 fallback 到本地执行。Linux 需要
`bubblewrap`，macOS 使用 `/usr/bin/sandbox-exec`，Windows 尚未实现 managed
backend。

`SandboxRunner.Timeout` 会为完整 workflow 设置 deadline，并把取消信号传导给 guest、
宿主 Tool 和子 Agent callback。Go context 采用协作式取消，call handler 必须在
context 结束后及时返回。零值不会额外添加 deadline，而是依赖调用方 context；生产
环境必须为调用方 context 设置 deadline，或者显式配置该 timeout。两者同时存在时，
以更早到期者为准。CPU、内存和进程数配额仍应由外层容器、microVM 或远端 runtime
提供。

`SandboxRunner.Python` 为空时，sandbox 会从自己的 clean PATH 解析 `python3`。任何
非空值（包括显式的 `"python3"`）都会先通过宿主 PATH 解析并转换成绝对路径。如果
解释器不在 backend 默认开放的 runtime 路径中，还需要通过
`sandbox.WorkspaceWriteProfile().WithReadPaths(...)` 扩展 managed permission
profile，并将其传给 `sandbox.WithPermissionProfile(...)`。

OS sandbox 仍然运行在宿主机上，并会开放启动 Python 所需的平台与 runtime 路径；
具体只读可见范围因 backend 而异。它不等价于 microVM 级租户边界。如果 guest 不能
看到任何宿主文件，或者需要更强的资源与租户隔离，应使用容器、microVM 或远端
`Runtime`。

可运行代码见
[Sandbox Dynamic Workflow 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/dynamicworkflow/sandbox)：
生成的 workflow 在 managed sandbox 中运行，子 Agent 工具与事件仍留在 Go 框架内。

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

完整可运行代码见 [Dynamic Workflow 基础示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/dynamicworkflow/basic)。

## 后续计划：文件化 workflow

后续的 source 选择扩展可以允许 `run_workflow` 在 inline code 与 workspace 相对路径的
脚本之间二选一，并接受可选 JSON 参数。它应复用配置的 workspace 抽象，并与脚本创作、
执行状态持久化、resume、checkpoint 和分发等能力保持独立。
