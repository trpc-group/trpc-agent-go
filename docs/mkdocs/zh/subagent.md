# 动态 Subagent 运行时

`subagent` 包用于从业务代码里动态创建后台 agent 任务。它适合
“先回复用户，再让委托任务继续在另一个 session 中执行”的场景。

运行时提供：

- `Spawn` 创建后台任务。
- `List`、`Get`、`Cancel`、`Wait` 管理任务。
- `MemoryStore` 和 `FileStore` 保存任务状态。
- 通过 `Observer` 观察生命周期变化。
- `tool/subagent` 工具，让父 agent 自主触发后台任务。

## 代码侧用法

先创建普通的 `runner.Runner`，再用 `subagent.Service` 包装它。
同一个 runner 决定子任务实际由哪个 agent 执行。需要选择具名 agent
时，在 `SpawnRequest.AgentName` 中传入对应名称。

```go
store, err := subagent.NewFileStore("subagents/runs.json")
if err != nil {
	return err
}

svc, err := subagent.NewService(
	r,
	subagent.WithStore(store),
	subagent.WithObserver(subagent.ObserverFunc(
		func(ctx context.Context, run subagent.Run) {
			if run.Status.IsTerminal() {
				log.Printf("subagent %s finished: %s", run.ID, run.Status)
			}
		},
	)),
)
if err != nil {
	return err
}
svc.Start(ctx)
defer svc.Close()

run, err := svc.Spawn(ctx, subagent.SpawnRequest{
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

当父 agent 需要自行判断是否委托后台任务时，可以接入 `tool/subagent`。
这些工具会把当前 invocation 的 session 作为父 session。

```go
subagentTools := subagenttool.NewTools(
	svc,
	subagenttool.WithDefaultAgentName("worker"),
)

parent := llmagent.New(
	"parent",
	llmagent.WithTools(subagentTools.All()),
)
```

工具名称如下：

- `subagents_spawn`
- `subagents_list`
- `subagents_get`
- `subagents_cancel`
- `subagents_wait`

默认禁止嵌套创建 subagent。只有当应用自己有并发和扇出限制时，才应通过
`subagenttool.WithNestedSpawns(true)` 显式开启。

## 运行时状态

每个子任务都会收到这些 runtime state：

- `subagent.RuntimeStateKeyRun`
- `subagent.RuntimeStateKeyRunID`
- `subagent.RuntimeStateKeyParentSessionID`

业务适配层可以通过 `SpawnRequest.RuntimeState` 合并更多运行时状态。
