# GenAIWorkflow 上报实现计划

## 目标

新增 `GenAIWorkflow` 监控项下的 graph node 耗时指标，用于统计每个 workflow/node 从 `nodeStart` 到最终 complete/error 的耗时。具体指标名使用 `gen_ai.client.operation.duration`，通过 `gen_ai.workflow.*` 维度区分 graph node 口径；不包含 stream、cache hit、retry attempt 维度，`gen_ai.system` 允许为空。

## 改动范围

- `[telemetry/semconv/metrics/metrics.go](telemetry/semconv/metrics/metrics.go)`: 复用/确认 metric name `gen_ai.client.operation.duration`，并为 `GenAIWorkflow` 监控项增加合适的 meter name。
- `[telemetry/semconv/trace/trace.go](telemetry/semconv/trace/trace.go)`: 增加 `gen_ai.app.name`、`gen_ai.user.id` 属性 key；`gen_ai.agent.*` 和 `gen_ai.workflow.*` 继续复用现有定义。
- `[internal/telemetry/metric_workflow.go](internal/telemetry/metric_workflow.go)`: 新增内部上报模块，封装 histogram、attributes 构造和 no-op 行为。
- `[telemetry/metric/metric.go](telemetry/metric/metric.go)`: 初始化新 meter/histogram，并接入 `SetHistogramBuckets`。
- `[graph/executor.go](graph/executor.go)`: 在 graph node 最终成功/失败路径接入上报。
- 测试文件：新增内部 metric 单测，并扩展 `telemetry/metric` 与 `graph` 相关测试。

## 上报设计

新增 `WorkflowAttributes`，记录 `gen_ai.client.operation.duration` 时包含：

- `gen_ai.system`: 有模型信息时取模型 system/name；无模型节点允许为空。
- `gen_ai.app.name`: 优先从 `invocation.Session.AppName` 取，必要时 fallback 到 graph state session。
- `gen_ai.user.id`: 优先从 `invocation.Session.UserID` 取，必要时 fallback 到 graph state session。
- `gen_ai.agent.id`: 复用现有 fallback 逻辑，当前可用 `invocation.AgentName`。
- `gen_ai.agent.name`: 非空时上报。
- `gen_ai.workflow.id`: `Task.NodeID`。
- `gen_ai.workflow.name`: `Executor.getNodeName(nodeID)`，为空时 fallback 到 nodeID。
- `gen_ai.workflow.type`: 复用 `workflowTypeFromNodeType(nodeType)`。
- `error.type`: 仅最终失败时上报。

## Executor 接入点

核心入口是 `[graph/executor.go](graph/executor.go)` 中的 `executeSingleTask`：

```2727:2774:graph/executor.go
func (e *Executor) executeSingleTask(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
	t *Task,
	step int,
	report *stepExecutionReport,
) error {
	// Initialize node execution context with retry policies and metadata.
	nodeCtx := e.initializeNodeContext(ctx, invocation, execCtx, t, step)
	// ...
	return e.executeTaskWithRetry(ctx, invocation, execCtx, t, step, nodeCtx)
}
```

计划在 `nodeCtx` 初始化后构造一个轻量 recorder，把 `nodeStart`、node id/type、invocation/session 信息固定下来。不要直接在 `emitNodeErrorEvent` 中记录，因为 retry 过程中也会发 error event：

```3809:3845:graph/executor.go
func (e *Executor) emitNodeErrorEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	execCtx *ExecutionContext,
	nodeID string,
	nodeType NodeType,
	step int,
	err error,
	extra ...NodeEventOption,
) {
	// emits graph.node.error events
}
```

推荐做法是在节点终态路径显式记录：

- success：`finalizeSuccessfulExecution`、`finalizeRecoveredExecution`、cache hit、before callback custom result。
- final failure：before callback error、after callback error、最终不再 retry 的 node error、handle result/conditional edge error。
- recorder 内部用 `sync.Once` 防止重复记录。

## Metric 初始化

在 `telemetry/metric/metric.go` 中仿照 `initInvokeAgentMetrics` 增加 `initWorkflowMetrics`：

- meter: `metrics.MeterNameWorkflow`
- histogram: `metrics.MetricGenAIClientOperationDuration`
- unit: `s`
- description: `Duration of graph workflow/node execution`

同时在 `SetHistogramBuckets` 增加新 meter 分支，支持用户调整 bucket。

## 测试计划

- `internal/telemetry/metric_workflow_test.go`: 覆盖 attributes、空 system、错误时 `error.type`、nil histogram no-op。
- `telemetry/metric/metric_test.go`: 覆盖新 metric 初始化、bucket 设置、未知 metric 错误。
- `graph/executor` 测试：覆盖成功节点上报一次、失败节点上报一次并带 `error.type`、retry 多次只上报最终一次、cache hit 上报但没有 cache hit 维度、before callback custom result 按成功上报。

## 风险与注意事项

- 不要让 `DisableGraphExecutorEvents` 影响 metric；隐藏事件不等于关闭监控。
- 不要上报 request/response payload 到 metric attributes，避免高基数和大属性。
- retry 中间 error event 不应产生 metric，否则会重复计数并污染失败率。
- 如果节点完成后 barrier 发送失败，metric 应反映节点执行结果，而不是 barrier 后处理结果。
