# Evaluation Invocation 增加 Agents 字段设计方案

## 1. 背景

当前评测数据结构 `evaluation/evalset.Invocation` 用于描述一轮对话中的一次调用（一次 user input → agent final response），包含：

- `UserContent`：用户输入
- `FinalResponse`：最终回复
- `Tools`：工具调用与返回（`evaluation/service/internal/inference/inference.go` 通过消费 `runner.Run()` 的事件流采集）

但在多智能体/子智能体场景下，一次 invocation 可能实际执行多个 Agent（及其子 Agent），典型包括：

- MultiAgent：`agent/chainagent`、`agent/parallelagent`
- 任务委托：`transfer_to_agent`（`internal/flow/processor/transfer.go`）
- Team：
  - `ModeSwarm`：成员间 transfer
  - `ModeCoordinator`：协调者通过 AgentTool 调用成员（`team/team.go`）
- GraphAgent：图中 `AgentNode`/`SubgraphNode` 触发子 Agent（`graph/state_graph.go`）
- AgentTool：把 Agent 作为 Tool 调用（`tool/agent/agent_tool.go`），默认 `StreamInner=false` 时子 Agent 的 inner events 不会转发到父流程事件流中

评测侧希望在 `evalset.Invocation` 中新增 `Agents` 字段，记录本轮 invocation 内“实际执行过哪些 Agent（含子 Agent）”，并包含 Agent 的 name 与调用关系信息，用于：

- Debug：定位具体由哪个子 Agent 产出问题结果
- 评测：后续可支持 “agent trajectory” 类指标
- 可观测：离线分析多 Agent 调用链路

## 2. 现状关键链路梳理

### 2.1 invocation 事件维度的可识别信息

框架在 `agent.InjectIntoEvent` 中把 invocation 关键信息写入事件：

- `Event.InvocationID`
- `Event.ParentInvocationID`（当 invocation 由 `Invocation.Clone()` 产生时会设置 parent，见 `agent/invocation.go`）
- `Event.Branch`（默认是 `AgentName`，clone 后是 `parentBranch/childAgentName`）
- `Event.FilterKey`（用于事件过滤，本方案阶段 1 不强依赖）

因此，只要某个子 Agent 的事件能进入事件流或进入 Session（被持久化），就可以通过 `InvocationID + ParentInvocationID + Branch` 重建调用树。

### 2.2 评测侧当前的采集方式与缺口

`evaluation/service/internal/inference/inference.go` 当前逻辑：

- 通过 `runner.Run()` 获取事件流
- 解析事件中的 tool_calls / tool_results 组装 `Invocation.Tools`
- 通过 `IsFinalResponse()` 捕获最终回复

缺口：

- 没有对事件中出现的“多个 invocationID（子 invocation）”做归并与结构化存储
- AgentTool 默认 `StreamInner=false` 时，子 Agent 的 events 不会出现在父 invocation 的事件流中（只会以 `tool.response` 的聚合结果表现），导致无法仅靠事件流复原“实际执行了哪些 Agent / 子 Agent”

## 3. 目标与原则

### 3.1 目标

1. 在 `evaluation/evalset.Invocation` 新增 `Agents` 字段，记录本轮 invocation 执行的所有 Agent（含子 Agent）。
2. 每个 Agent 记录至少包含：
   - `name`
   - 调用关系（父子关系）
   - 用于定位的一次执行信息（例如 `InvocationID/Branch`）

### 3.2 设计原则

- **可回放**：能从记录中恢复 agent 调用树
- **面向多形态**：Chain/Parallel/Transfer/Graph/Team/AgentTool 统一表示
- **不破坏兼容性**：新增字段 `omitempty`，旧数据可无损读写
- **可渐进增强**：先做到“能完整列出执行过哪些 Agent”，再逐步增强更细粒度的轨迹信息（如上下文/消息序列等）

## 4. 数据结构设计（evalset.Invocation.Agents）

在 `evaluation/evalset/evalcase.go` 中新增：

```go
type Invocation struct {
    // ...
    Tools   []*Tool            `json:"tools,omitempty"`
    Agents  []*AgentInvocation `json:"agents,omitempty"`
    // ...
}
```

推荐新增结构：

```go
// AgentInvocation captures one executed agent (including sub-agents) within a single evaluation invocation.
type AgentInvocation struct {
    // InvocationID is the runtime invocation id for this agent execution.
    InvocationID string `json:"invocationId,omitempty"`

    // ParentInvocationID links to the parent invocation id (if any).
    ParentInvocationID string `json:"parentInvocationId,omitempty"`

    // Name is the agent name (agent.Info().Name).
    Name string `json:"name,omitempty"`

    // Branch is the branch chain injected into events (e.g., "root/child/grandchild").
    Branch string `json:"branch,omitempty"`
}
```

说明：

- `InvocationID/ParentInvocationID/Branch` 用于构建树与定位具体一次执行（同名 Agent 多次执行也能区分）。

## 5. 采集方案设计

采集的核心在 `evaluation/service/internal/inference/inference.go` 的 `inferenceInvocation()`：当前已逐事件解析 Tools/FinalResponse，扩展为同时聚合 Agents。

### 5.1 统一的“agent 执行”识别策略

定义“一个 agent 执行” = “一次 invocation（`agent.Invocation`）的生命周期”，其标识是 `Event.InvocationID`。

#### 5.1.1 最小可行提取算法（推荐先落地这个）

1. **消费事件流**：评测侧从 `runner.Run()` 返回的事件 channel 持续读取，直到遇到 `evt.IsRunnerCompletion()`；并记录该 completion event 的 `InvocationID` 作为本轮 runner root invocation（建议也用它作为 `evalset.Invocation.InvocationID`）。
2. **按 `InvocationID` 聚合**：对每条 `evt`，以 `evt.InvocationID` 为 key 累积到一个桶里（忽略 `InvocationID` 为空的事件，并记录 `ParentInvocationID/Branch`）；同时记录该 `InvocationID` 在事件流中“首次出现的顺序 index”（用于稳定输出顺序）。
3. **识别 roots（允许森林）**：把 `ParentInvocationID` 为空或 parent 不存在于聚合结果中的 invocation 视为 root；其中 runner completion 的 `InvocationID`（若存在）视为“主 root”并优先输出。
4. **生成 `AgentInvocation`**（每个桶一条，best-effort）：
   - `InvocationID`：桶 key。
   - `ParentInvocationID`：取该桶内首个非空 `evt.ParentInvocationID`。
   - `Branch`：取该桶内首个非空 `evt.Branch`。
   - `Name`：优先取 `Branch` 最后一段（按 `/` 分割），否则 fallback 为 `evt.Author`（排除 `"user"`, `"graph-node"`, `"graph-pregel"` 等非 agent author）。
5. **组织调用树（可选）**：根据 `ParentInvocationID` 把这些条目组织成树；也可以保持扁平数组，仅依赖 `ParentInvocationID` 在消费侧重建树。
6. **稳定输出顺序（必须）**：建议按“forest 的前序遍历”输出 `Agents`：roots 按（主 root 优先 → 首次出现顺序 → `InvocationID`）排序；每个 parent 的 children 也按（首次出现顺序 → `InvocationID`）排序。

这个算法能覆盖：Runner root 调用、Chain/Parallel、Transfer、GraphAgent 的 AgentNode 等“子 invocation events 会进入外层事件流”的场景（AgentTool 仅在 `StreamInner=true` 时满足该条件）。

#### 5.1.2 关键假设与边界

- `InvocationID` 是一次 invocation 的唯一标识：框架在 `agent.NewInvocation()` 与 `Invocation.Clone()` 时生成新 ID，并通过 `agent.InjectIntoEvent()` 注入到事件中，因此按 `InvocationID` 聚合可稳定区分同名 Agent 的多次执行。
- `Branch` 不是唯一标识：同一个 parent 下重复执行同名 Agent 时，`Branch` 可能相同，因此不能用 `Branch` 作为聚合 key。
- 若某些实现“复用同一个 invocation 执行多个不同 Agent”（不通过 `Clone()` 生成新 `InvocationID`），则这些执行会在评测侧被合并；该行为不在本方案阶段 1 的支持范围内。

#### 5.1.3 覆盖场景（阶段 1）

以下场景满足“子 invocation events 会进入外层事件流”，因此可通过 `InvocationID` 聚合得到本轮实际执行过的 Agent 列表（含子 Agent）：

- Runner root 调用（默认 agent）。
- MultiAgent：`ChainAgent` / `ParallelAgent` / `CycleAgent` 等通过 `Invocation.Clone()` 派生子 invocation 并转发事件的实现。
- 任务委托：`transfer_to_agent` 触发的 target agent（`TransferResponseProcessor` clone 子 invocation 并转发事件）。
- Team：`ModeSwarm`（成员间 transfer）可覆盖；`ModeCoordinator` 仅当成员是以“可转发 inner events”的方式调用时可覆盖（例如 AgentTool `StreamInner=true`）。
- GraphAgent：`AgentNode` 触发的子 agent invocation（clone 子 invocation 并转发事件）。

### 5.2 暂不覆盖：AgentTool 且 `StreamInner=false`

本期先不支持 AgentTool 在 `StreamInner=false` 时的子 Agent 提取，因为该模式下子 Agent 的 invocation events 不会进入外层事件流，仅靠消费 `runner.Run()` 的事件无法完整恢复“执行了哪些子 agent”。

当前约束与建议：

- 若业务确实需要采集 AgentTool 的子 agent，本期建议开启 `WithStreamInner(true)`，使子 agent events 进入外层事件流，从而可直接复用 §5.1 的聚合算法。
- 若必须支持 `StreamInner=false`，可作为后续阶段能力：通过回放 session events 做补齐（不在本期范围内）。

### 5.3 GraphAgent 的 AgentNode 场景

GraphAgent 的 AgentNode 会通过 `buildAgentInvocationWithStateAndScope()` clone 出子 invocation 并运行子 Agent（`graph/state_graph.go`），子 Agent 的 events 会被转发到 graph 的 eventChan，因此：

- 事件流/Session 都能看到子 invocation events
- `ParentInvocationID` 与 `Branch` 可用于构建树

## 6. 序列化、兼容与存储影响

- `evalset.Invocation` 新增 `Agents` 字段，使用 `json:"agents,omitempty"`：
  - 旧版 evalset JSON 不包含该字段时反序列化不受影响
  - 新版写入时只会增加字段，不影响既有字段解析
- `evalresult` 中 `ActualInvocation/ExpectedInvocation` 都引用 `evalset.Invocation`：
  - expected invocation 可按需补充 Agents（用于未来 agent trajectory 评测）
  - 现阶段 metrics 未依赖 Agents，不会影响现有评测

## 7. 性能与规模控制

建议提供可配置的裁剪策略（评测默认可开启）：

- `MaxAgents`：最多记录多少个 agent invocation（超出可截断或只保留树的前 N 个）
- `MaxToolArgsBytes/MaxToolResultBytes`：Tool args/result 的大小限制（必要时保留摘要）

## 8. 实施建议（分阶段）

### 阶段 1：最小可用（先把 Agents 列出来）

- `evalset.Invocation` 增加 `Agents` 字段与结构体定义
- `evaluation/service/internal/inference/inference.go` 基于事件流聚合 Agents

覆盖：Chain/Parallel/Transfer/GraphAgent 等“子 invocation events 会进入外层事件流”的场景（AgentTool 仅当 `StreamInner=true`）。

### 阶段 2（可选）：支持 AgentTool 且 `StreamInner=false`

- 通过回放 session events 补齐子 agent（具体做法后续再设计与落地）。
