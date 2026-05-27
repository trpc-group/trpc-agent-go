# Agent Task Run 运行时

`agent/taskrun` 包定义了一套后台任务运行的控制面 API。一次 task run
会通过 `runner.Run` 执行某个 agent，适合当前用户轮次需要尽快返回，
但委托出去的任务还要在另一个 session 中继续运行的场景。

这个能力把稳定 API 和单进程实现拆开：

- `agent/taskrun` 定义 `Run`、`Status`、`SpawnRequest`、`ListFilter`
  和 `Controller`。
- `agent/taskrun/inprocess` 提供基于 goroutine 的单进程实现，并带有
  `MemoryStore` 和 `FileStore`。
- `tool/taskrun` 提供可选的控制工具，让父 agent 自主创建和管理
  task run。

`inprocess.Service` 适合测试、本地运行时，以及单进程产品适配层。
多节点部署应当自己实现 `taskrun.Controller`，并基于外部存储、队列、
lease 和跨节点取消机制来管理状态。`SpawnRequest` 是 Go API，不是跨节点
wire protocol；其中 `RuntimeState map[string]any` 这类字段只适合直接调用
`runner.Run` 的实现。分布式 controller 应在产品适配层把这些值规范化，
不要直接序列化任意 Go 对象。

## 和 agenttool.NewTool、transfer_to_agent 的区别

tRPC-Agent-Go 里有三种常见的 agent 委托方式。它们都能让另一个 agent
参与工作，但语义不一样。

| 方式 | 适合场景 | 是否立即返回结果 | 是否有可管理的 run id |
| --- | --- | --- | --- |
| `agenttool.NewTool` / `tool/agent.Tool` | 把一个专家 agent 当成普通工具同步调用，父 agent 需要拿到返回值继续推理 | 是 | 否 |
| `transfer_to_agent` | 当前 invocation 的控制权交给某个 sub-agent，后续由目标 agent 接着处理这一轮 | 在同一轮内继续 | 否 |
| `tool/taskrun` | 另起一个子 session 执行任务，父 agent 可以继续当前轮，也可以查询、等待或取消子任务 | 可同步也可异步 | 是 |

可以把它们理解成三个不同的问题：

- “我现在需要一个专家给我一个结果吗？”用 `agenttool.NewTool`。
- “这轮对话应该交给另一个 agent 接管吗？”用 `transfer_to_agent`。
- “这个任务应该变成一个可跟踪、可取消、可等待的后台 run 吗？”用
  `taskrun`。

### 同步调用专家 agent：使用 agenttool.NewTool

`agenttool.NewTool` 会把一个已经构造好的 agent 包装成一个普通 tool。父 agent
调用这个 tool 时，会等待子 agent 完成。默认情况下，tool result 会聚合子
agent 产生的非空 assistant 消息。下面的示例配置了
`agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly)`，因此 tool 只返回
最后一条完整 assistant 消息。它适合“查资料、审查一段内容、生成一个小结果”
这类同步子任务。

```go
reviewer := llmagent.New(
	"reviewer",
	llmagent.WithModel(reviewModel),
	llmagent.WithInstruction(
		"Review the input carefully and return concise findings.",
	),
)

reviewTool := agenttool.NewTool(
	reviewer,
	agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
)

parent := llmagent.New(
	"parent",
	llmagent.WithModel(parentModel),
	llmagent.WithTools([]tool.Tool{reviewTool}),
)
```

这里没有后台 run，也没有 `run id`。父 agent 的模型看到的是一个名为
`reviewer` 的普通工具；工具调用结束后，父 agent 继续当前推理。

### 当前轮交给 sub-agent：使用 transfer_to_agent

当父 agent 不只是需要一个工具结果，而是判断“后续应该由另一个 agent
处理这一轮”时，用 sub-agent transfer 更合适。配置 sub-agent 后，LLMAgent
会为当前 agent 暴露框架工具 `transfer_to_agent`。

```go
support := llmagent.New(
	"support",
	llmagent.WithModel(supportModel),
	llmagent.WithInstruction(
		"Handle product support questions and ask for missing details.",
	),
)

router := llmagent.New(
	"router",
	llmagent.WithModel(routerModel),
	llmagent.WithInstruction(
		"Route product support requests to the support agent.",
	),
	llmagent.WithSubAgents([]agent.Agent{support}),
)
```

模型可以调用：

```json
{
  "agent_name": "support",
  "message": "The user is asking how to configure retries. Continue from here."
}
```

这不是后台任务。它表示当前 invocation 的控制流转到 `support`，事件仍然属于
这一次运行。

### 可管理的后台任务：使用 taskrun

`taskrun` 适合“任务可能比较久、父 agent 不一定要等、之后还要查状态或取消”
的场景。每次 `start_task_run` 都会创建一个新的 child session，并返回一个
run id。后续可以用 `get_task_run`、`wait_task_run`、`cancel_task_run`
管理它。

```json
{
  "task": "Read the design notes and summarize the migration risks.",
  "agent_name": "worker",
  "mode": "async",
  "timeout_seconds": 600
}
```

如果父 agent 必须等结果，可以用 `sync`：

```json
{
  "task": "Compare the two API designs and return a recommendation.",
  "agent_name": "worker",
  "mode": "sync",
  "timeout_seconds": 600,
  "wait_timeout_seconds": 120
}
```

`sync` 只表示工具会等待子 run 进入终态；它仍然会创建 run id，并走同一套
taskrun 生命周期。`wait_timeout_seconds` 到期时不会取消子 run，父 agent
仍然可以拿 run id 后续查询。

## 代码侧用法

先创建普通的 `runner.Runner`，再用 `inprocess.Service` 包装它。
具体由哪个 agent 执行子任务，仍由同一个 runner 决定。需要选择具名
runner agent 时，可以传入 `AgentName`。

```go
store, err := inprocess.NewFileStore("task-runs/runs.json")
if err != nil {
	return err
}

svc, err := inprocess.NewService(
	r,
	inprocess.WithStore(store),
	inprocess.WithObserver(taskrun.ObserverFunc(
		func(ctx context.Context, run taskrun.Run) {
			if run.Status.IsTerminal() {
				log.Printf("task run %s finished: %s", run.ID, run.Status)
			}
		},
	)),
)
if err != nil {
	return err
}
svc.Start(ctx)
defer svc.Close()

run, err := svc.Spawn(ctx, taskrun.SpawnRequest{
	OwnerUserID:     "user-123",
	ParentSessionID: "chat-456",
	AgentName:       "worker",
	Task:            "review the generated frontend and summarize issues",
	Timeout:         10 * time.Minute,
})
if err != nil {
	return err
}

final, err := svc.Wait(ctx, run.ID)
if err != nil {
	return err
}
log.Printf("result: %s", final.Result)
```

## 工具用法

当父 agent 需要自行判断是否委托后台任务时，可以接入 `tool/taskrun`。
这些工具会使用当前 invocation session 作为父 session。

如果父 agent 的工具列表里包含 taskrun 工具，而 `inprocess.Service` 又需要
持有同一个 runner，可以先创建一个没有 controller 的工具集合，再在 service
创建后调用 `SetController`。这样可以避免“parent agent 需要 service，
service 又需要 runner，runner 又需要 parent agent”的初始化循环。

```go
taskRunTools := taskruntool.NewTools(
	nil,
	taskruntool.WithDefaultAgentName("worker"),
)

worker := llmagent.New(
	"worker",
	llmagent.WithModel(workerModel),
	llmagent.WithInstruction(
		"Work on delegated tasks and return a clear final result.",
	),
)

parent := llmagent.New(
	"parent",
	llmagent.WithModel(parentModel),
	llmagent.WithTools(taskRunTools.All()),
	llmagent.WithInstruction(
		"Use taskrun tools only when the work can run in a separate task.",
	),
)

r := runner.NewRunner(
	"taskrun-example",
	parent,
	runner.WithAgent("worker", worker),
)

svc, err := inprocess.NewService(r)
if err != nil {
	return err
}
svc.Start(ctx)
defer svc.Close()

taskRunTools.SetController(svc)
```

如果你的应用已经先创建好了 `taskrun.Controller`，也可以直接传入：

```go
taskRunTools := taskruntool.NewTools(
	svc,
	taskruntool.WithDefaultAgentName("worker"),
)

parent := llmagent.New(
	"parent",
	llmagent.WithTools(taskRunTools.All()),
)
```

工具名包括：

- `start_task_run`
- `list_task_runs`
- `get_task_run`
- `cancel_task_run`
- `wait_task_run`

如果配置了 `taskruntool.WithSessionService`，`tool/taskrun` 还会额外
提供：

- `read_task_run_transcript`

`start_task_run` 支持 `mode` 字段：

- `async` 是默认模式。工具会启动子 run，并立即返回 run id。
- `sync` 会启动子 run，并等待它进入终态后再返回。

`timeout_seconds` 限制的是子 run 本身的执行时间。`wait_timeout_seconds`
只限制 `sync` 模式下工具等待的时间；等待超时不会取消子 run。

默认禁止嵌套创建 task run。只有当应用自己有并发和扇出限制时，才应通过
`taskruntool.WithNestedSpawns(true)` 显式开启。

## 给模型的使用建议

把 `tool/taskrun` 暴露给父 agent 后，建议在父 agent 的 instruction 中说明
什么时候应该使用它。不要只告诉模型“你可以启动任务”，而要说明边界。

```go
parent := llmagent.New(
	"parent",
	llmagent.WithModel(parentModel),
	llmagent.WithTools(taskRunTools.All()),
	llmagent.WithInstruction(`
You coordinate user requests.

Use start_task_run when a delegated task can continue in a separate child
session, especially when it may take a long time or the user does not need the
result immediately.

Use mode="async" when you can continue the current response after receiving the
run id. Use mode="sync" only when the delegated result is required before you
can answer.

After starting an async task, keep the returned run id. Use get_task_run or
wait_task_run when you need the latest status or final result. Use
cancel_task_run only when the user asks to stop the task or continuing it would
be wasteful.
`),
)
```

如果只是需要另一个 agent 同步返回一段分析，优先用 `agenttool.NewTool`
包装那个 agent。如果是路由到另一个 agent 继续处理当前 invocation，优先配置
sub-agent 并使用 `transfer_to_agent`。只有需要 run id、状态查询、等待或取消
时，才使用 `taskrun`。

## 选择具名 agent

`start_task_run` 的 `agent_name` 对应 runner 里的具名 agent。最简单的方式是
用 `runner.WithAgent` 注册一个已经构造好的 worker。

```go
r := runner.NewRunner(
	"taskrun-example",
	parent,
	runner.WithAgent("worker", worker),
)
```

如果 worker 需要按请求动态构造，比如不同租户使用不同 prompt、模型或沙箱，
可以使用 `runner.WithAgentFactory`。taskrun 会把 `agent_name` 传给 runner，
由 runner 在每个子 run 开始时解析对应 agent。

```go
r := runner.NewRunner(
	"taskrun-example",
	parent,
	runner.WithAgentFactory(
		"worker",
		func(ctx context.Context, ro agent.RunOptions) (agent.Agent, error) {
			return llmagent.New(
				"worker",
				llmagent.WithModel(workerModel),
				llmagent.WithInstruction(
					"Handle delegated background work for this request.",
				),
			), nil
		},
	),
)
```

推荐用不同的 `agent_name` 表达不同执行策略，例如 `fast_worker`、
`deep_reviewer`、`readonly_researcher`。这样模型只选择业务角色，具体模型、
工具和权限由应用侧配置，成本和安全边界更可控。

## 进度与子会话 transcript

`Run.Progress` 是从子 run 事件流里收集出来的轻量状态快照，包含事件数、
工具调用数、工具结果数、token 计数，以及最后一次观察到的事件时间。
完整 transcript 不会复制到 `Run` 里，而是继续存放在 `Run.ChildSessionID`
指向的子 session 中。

进程内 controller 会让运行中的进度可以通过轮询 API 读取。Observer 仍然
只接收已经持久化的生命周期更新；最终进度快照会和终态 run 一起持久化。

这样可以保持 task run 控制面足够小：

- `list_task_runs`、`get_task_run`、`wait_task_run` 返回轻量状态和进度。
- 子 session 保存详细 transcript。
- 业务侧仍然可以基于 observer 和 session events 自行实现 UI、通知、
  日志归档或 artifact 存储。

如果希望 agent 可以查看子 run transcript，把 runner 使用的同一个
session service 传给工具即可：

```go
taskRunTools := taskruntool.NewTools(
	svc,
	taskruntool.WithDefaultAgentName("worker"),
	taskruntool.WithSessionService(sessionService),
)
```

`read_task_run_transcript` 会读取子 session 最近的事件。它的 `limit`
字段可选，默认读取最近 40 条事件，最多 200 条。访问范围限制在当前用户
拥有、并且由当前父 session 创建的 run。当 run 记录了 app name，例如
来自 `agent.WithAppName` 时，transcript 读取会使用这个 app name 定位
子 session。

## 运行时状态

每个子 run 会收到这些 runtime-state key：

- `taskrun.RuntimeStateKeyRun`
- `taskrun.RuntimeStateKeyRunID`
- `taskrun.RuntimeStateKeyParentSessionID`

业务适配层可以通过 `SpawnRequest.RuntimeState` 合并更多运行时状态；如果
产品侧需要自己的运行时命名，也可以通过 `SpawnRequest.RuntimeStateKeys`
覆盖注入的 key 名称。如果子 run 需要使用特定的 session app 命名空间，
可以设置 `SpawnRequest.AppName`。直接调用 `runner.Run` 的本地适配层还
可以通过 `SpawnRequest.RunOptions` 传入每次运行的 `agent.RunOption`；
这属于进程内 runner 配置，不应作为跨节点序列化协议。
`SpawnRequest.RunContext` 可用于把本地 context value 注入子 runner
context，适用范围同样是进程内实现。
