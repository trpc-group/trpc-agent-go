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

`start_task_run` 支持 `mode` 字段：

- `async` 是默认模式。工具会启动子 run，并立即返回 run id。
- `sync` 会启动子 run，并等待它进入终态后再返回。

`timeout_seconds` 限制的是子 run 本身的执行时间。`wait_timeout_seconds`
只限制 `sync` 模式下工具等待的时间；等待超时不会取消子 run。

默认禁止嵌套创建 task run。只有当应用自己有并发和扇出限制时，才应通过
`taskruntool.WithNestedSpawns(true)` 显式开启。

## 运行时状态

每个子 run 会收到这些 runtime-state key：

- `taskrun.RuntimeStateKeyRun`
- `taskrun.RuntimeStateKeyRunID`
- `taskrun.RuntimeStateKeyParentSessionID`

业务适配层可以通过 `SpawnRequest.RuntimeState` 合并更多运行时状态；如果
产品侧需要自己的运行时命名，也可以通过 `SpawnRequest.RuntimeStateKeys`
覆盖注入的 key 名称。直接调用 `runner.Run` 的本地适配层还可以通过
`SpawnRequest.RunOptions` 传入每次运行的 `agent.RunOption`；这属于进程内
runner 配置，不应作为跨节点序列化协议。`SpawnRequest.RunContext` 可用于
把本地 context value 注入子 runner context，适用范围同样是进程内实现。
