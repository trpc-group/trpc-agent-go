# trpc_agent_go.internal.workflow 监控项协议草案

## 背景

`GenAIInvokeAgent` 当前用于统计一次 agent 调用的整体耗时。Graph 场景下，用户还需要按 graph node 维度观察执行耗时，用于定位首包耗时、慢节点和异常节点。

`trpc_agent_go.internal.workflow` 拟作为 graph workflow/node 执行过程监控项，语义上借鉴 trace 中已有的 workflow 标记：

- `gen_ai.workflow.id`
- `gen_ai.workflow.name`
- `gen_ai.workflow.type`

该监控项当前包含两类耗时：

- `gen_ai.client.operation.duration`：当前 workflow 观测对象自身的执行耗时。
- `gen_ai.workflow.elapsed_time`：当前 workflow 观测对象相对某两个生命周期点的累计耗时。

trpc-agent-go 当前把 graph node 适配为 workflow 观测对象，因此 `gen_ai.workflow.id/name/type` 标识当前 graph node，而不是额外引入 `workflow -> node` 层级。

## 监控项

| 字段 | 内容 |
| --- | --- |
| 监控项名称 | `trpc_agent_go.internal.workflow` |
| 监控项语义 | graph workflow/node 执行过程监控 |

### 指标定义

| 指标名 | 指标语义 | 指标类型 | 单位 | 上报时机 |
| --- | --- | --- | --- | --- |
| `gen_ai.client.operation.duration` | graph workflow/node 执行耗时 | histogram | 秒 | graph node 执行完成或执行失败时上报一次 |
| `gen_ai.workflow.elapsed_time` | 当前 workflow 观测对象的相对累计耗时 | histogram | 秒 | graph node 执行完成或执行失败时上报一次 |

`histogram` 用于支持平均值、p95、p99 等聚合统计。

### 维度定义

| 维度名称 | 类型 | 描述 | 示例 | 要求级别 |
| --- | --- | --- | --- | --- |
| `gen_ai.operation.name` | string | GenAI 操作名称，固定为 `workflow`，与 trace workflow span operation 保持一致 | `workflow` | 必填 |
| `gen_ai.system` | string | GenAI 产品标识，如 `openai`、`azure.ai.openai` 等；无模型节点允许为空 | `openai` | 允许为空 |
| `gen_ai.app.name` | string | 应用名称 | `my_app` | 必填 |
| `gen_ai.user.id` | string | user id | `user1` | 必填 |
| `gen_ai.agent.id` | string | agent id | `my_agent` | 必填 |
| `gen_ai.workflow.id` | string | workflow/node id | `retrieve` | 必填 |
| `gen_ai.workflow.name` | string | workflow/node 名称 | `execute_function_node retrieve` | 必填 |
| `gen_ai.workflow.type` | string | workflow/node 类型 | `function` | 必填 |
| `gen_ai.workflow.elapsed.from` | string | elapsed time 计算起点 | `root_workflow.start` | 仅上报 `gen_ai.workflow.elapsed_time` 时填写 |
| `gen_ai.workflow.elapsed.to` | string | elapsed time 计算终点 | `current_workflow.end` | 仅上报 `gen_ai.workflow.elapsed_time` 时填写 |
| `error.type` | string | 错误类型 | `timeout` | 错误时必填 |
| `gen_ai.agent.name` | string | agent name | `agent_1` | 选填 |

### workflow 类型取值

`gen_ai.workflow.type` 建议复用框架现有 graph node 类型：

| 类型 | 说明 |
| --- | --- |
| `function` | 普通函数节点 |
| `llm` | LLM 节点 |
| `tool` | Tool 节点 |
| `agent` | Agent/Subgraph 节点 |
| `join` | Join 节点 |
| `router` | Router 节点 |
| `graph` | 整体 graph workflow，预留 |

## 上报口径

`gen_ai.client.operation.duration` 从 graph executor 记录的 node start time 开始，到节点最终 complete 或 error 为止。

`gen_ai.workflow.elapsed_time` 是可选的相对耗时指标；只有上报该指标时，才需要同时填写 `gen_ai.workflow.elapsed.from` 和 `gen_ai.workflow.elapsed.to` 描述计算口径。当前 trpc-agent-go 初始映射为：

- `gen_ai.workflow.elapsed.from=root_workflow.start`：本次 root workflow 执行窗口开始。当前实现对应本次 graph `Execute()` start。
- `gen_ai.workflow.elapsed.to=current_workflow.end`：当前 workflow 观测对象结束。当前实现对应当前 graph node 最终 complete 或 error。

`gen_ai.workflow.elapsed.from/to` 只描述耗时计算点；当前观测对象仍由 `gen_ai.workflow.id/name/type` 标识。

当前先不扩展额外拓扑维度，避免在 DAG 分支、join、resume 等场景下引入未定义的聚合口径。

需要覆盖以下场景：

- 正常执行成功：上报耗时，不带 `error.type`。
- 执行失败：上报耗时，并带 `error.type`。
- before callback 返回 custom result 跳过节点函数：仍视为节点完成，上报从 node start 到 custom result 完成的耗时。
- cache hit：仍视为节点完成，上报 cache hit 节点耗时和 elapsed time；不增加 cache hit 维度。
- retry：只对最终结果上报一次完整节点耗时；不增加 retry attempt 维度。

## 与现有指标的关系

`GenAIInvokeAgent` 统计一次 agent 调用的整体耗时，适合看单次请求总耗时。

`trpc_agent_go.internal.workflow` 统计 graph workflow/node 维度耗时，适合定位具体慢节点。两者是父子关系，指标名同为 `gen_ai.client.operation.duration` 时，需要通过 `gen_ai.workflow.*` 等维度区分 graph node 口径。

`gen_ai.workflow.elapsed_time` 补充当前 workflow 观测对象相对 root workflow start 的累计耗时，适合观察“从本次 workflow 开始到当前对象完成”的用户视角到达时间；它不表示各节点耗时求和。

## 与现有代码的差异

当前代码中已有部分属性 key 与本草案命名不同，落地时统一切换为 `gen_ai.*` 命名或做映射：

| 草案维度 | 当前代码中已有 key | 说明 |
| --- | --- | --- |
| `gen_ai.app.name` | `trpc_go_agent.app.name` | 按平台协议切换到 GenAI 命名 |
| `gen_ai.user.id` | `trpc_go_agent.user.id` | 按平台协议切换到 GenAI 命名 |
| `gen_ai.agent.id` | `gen_ai.agent.id` | 已有，类型应为 string |
| `gen_ai.agent.name` | `gen_ai.agent.name` | 已有 |
| `gen_ai.workflow.id` | `gen_ai.workflow.id` | trace 中已有 |
| `gen_ai.workflow.name` | `gen_ai.workflow.name` | trace 中已有 |
| `gen_ai.workflow.type` | `gen_ai.workflow.type` | trace 中已有 |

## 已确认事项

- 监控项名称统一为实际上报名称 `trpc_agent_go.internal.workflow`。
- 操作名称维度固定为 `gen_ai.operation.name=workflow`，与现有 trace 的 `workflow` span operation 保持一致。
- app/user 维度统一使用 `gen_ai.*` 命名，即 `gen_ai.app.name`、`gen_ai.user.id`。
- cache hit、retry attempt 不作为维度。
- `gen_ai.system` 允许为空；有模型信息时优先使用模型 system。
- elapsed time 为可选指标；仅上报 `gen_ai.workflow.elapsed_time` 时填写 `gen_ai.workflow.elapsed.from/to`，初始取值为 `root_workflow.start` 到 `current_workflow.end`。
- 暂不增加额外拓扑维度。
