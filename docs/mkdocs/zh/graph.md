# Graph 包使用指南

## 概述

Graph 将可控的工作流编排与可扩展的 Agent 能力结合，适用于：

- 类型安全的状态管理与可预测路由；
- LLM 决策、工具调用循环、可选的 Human in the Loop（HITL）；
- 可复用的组件，既可独立运行，也可作为子 Agent 组合。

特点：

- Schema 驱动的 State 与 Reducer，避免并发分支写入同一字段时的数据竞争；
- BSP 风格（计划/执行/合并）的确定性并行；
- 内置节点类型封装 LLM、工具与 Agent，减少重复代码；
- 流式事件、检查点与中断，便于观测与恢复。
- 节点级重试/退避（指数退避与抖动），支持执行器默认重试策略与带重试元数据的事件观测。
- 节点主动外抛事件（EventEmitter），支持在 NodeFunc 中发射自定义事件、进度更新和流式文本。

## 快速开始

### 最小工作流

下面是一个经典的“prepare → ask LLM → 可能调用工具”的循环，使用 `graph.MessagesStateSchema()`（已定义 `graph.StateKeyMessages`、`graph.StateKeyUserInput`、`graph.StateKeyLastResponse` 等键）。

```mermaid
flowchart LR
    START([start]):::startNode --> P[prepare]:::processNode
    P --> A[ask LLM]:::llmNode
    A -. tool_calls .-> T[tools]:::toolNode
    A -- no tool_calls --> F[fallback]:::processNode
    T --> A
    F --> END([finish]):::endNode

    classDef startNode fill:#e1f5e1,stroke:#4caf50,stroke-width:2px
    classDef endNode fill:#ffe1e1,stroke:#f44336,stroke-width:2px
    classDef llmNode fill:#e3f2fd,stroke:#2196f3,stroke-width:2px
    classDef toolNode fill:#fff3e0,stroke:#ff9800,stroke-width:2px
    classDef processNode fill:#f3e5f5,stroke:#9c27b0,stroke-width:2px
```

Graph 包允许您将复杂的 AI 工作流建模为有向图，其中节点代表处理步骤，边代表数据流和控制流。它特别适合构建需要条件路由、状态管理和多步骤处理的 AI 应用。

### 使用模式

Graph 包的使用遵循以下模式：

1. **创建 Graph**：使用 `StateGraph` 构建器定义工作流结构
2. **创建 GraphAgent**：将编译后的 Graph 包装为 Agent
3. **创建 Runner**：使用 Runner 管理会话和执行环境
4. **执行工作流**：通过 Runner 执行工作流并处理结果

这种模式提供了：

- **类型安全**：通过状态模式确保数据一致性
- **会话管理**：支持多用户、多会话的并发执行
- **事件流**：实时监控工作流执行进度
- **错误处理**：统一的错误处理和恢复机制

### Agent 集成

GraphAgent 实现了 `agent.Agent` 接口，可以：

- **作为独立 Agent**：通过 Runner 直接执行
- **作为 SubAgent**：被其他 Agent（如 LLMAgent）作为子 Agent 使用
- **挂载 SubAgent**：通过 `graphagent.WithSubAgents` 配置子 Agent，并在图中使用 `AddAgentNode` 委托执行

这种设计使得 GraphAgent 既能接入其他 Agent，也能在自身工作流中灵活调度子 Agent。

### 主要特性

- **类型安全的状态管理**：使用 Schema 定义状态结构，支持自定义 Reducer
- **条件路由**：基于状态动态选择执行路径
- **LLM 节点集成**：内置对大型语言模型的支持
- **工具节点**：支持函数调用和外部工具集成
- **Agent 节点**：通过子 Agent 将其他 Agent 融入图中
- **流式执行**：支持实时事件流和进度跟踪
- **并发安全**：线程安全的图执行
- **基于检查点的时间旅行**：浏览执行历史并恢复之前的状态
- **人机协作 (HITL)**：支持带有中断和恢复功能的交互式工作流
- **原子检查点**：原子存储检查点和待写入数据，确保可靠的恢复
- **检查点谱系**：跟踪形成执行线程的相关检查点及其父子关系

## 核心概念

### 1. 图 (Graph)

图是工作流的核心结构，由节点和边组成：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 创建状态模式
schema := graph.NewStateSchema()

// 创建图
graph := graph.New(schema)
```

**虚拟节点**：

- `Start`：虚拟起始节点，通过 `SetEntryPoint()` 自动连接
- `End`：虚拟结束节点，通过 `SetFinishPoint()` 自动连接
- 这些节点不需要显式创建，系统会自动处理连接

### 2. 节点 (Node)

节点代表工作流中的一个处理步骤：

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 节点函数签名
type NodeFunc func(ctx context.Context, state graph.State) (any, error)

// 创建节点
node := &graph.Node{
    ID:          "process_data",
    Name:        "数据处理",
    Description: "处理输入数据",
    Function:    processDataFunc,
}
```

### 3. 状态 (State)

状态是在节点间传递的数据容器：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 状态是一个键值对映射
type State map[string]any

// 用户自定义的状态键
const (
    StateKeyInput         = "input"          // 输入数据
    StateKeyResult        = "result"         // 处理结果
    StateKeyProcessedData = "processed_data" // 处理后的数据
    StateKeyStatus        = "status"         // 处理状态
)
```

**内置状态键**：

Graph 包提供了一些内置状态键，主要用于系统内部通信：

**用户可访问的内置键**：

- `StateKeyUserInput`：用户输入（一次性，消费后清空，由 LLM 节点自动持久化）
- `StateKeyOneShotMessages`：一次性消息（完整覆盖本轮输入，消费后清空）
- `StateKeyLastResponse`：最后响应（用于设置最终输出，Executor 会读取此值作为结果）
- `StateKeyMessages`：消息历史（持久化，支持 append + MessageOp 补丁操作）
- `StateKeyNodeResponses`：按节点存储的响应映射。键为节点 ID，值为该
  节点的最终文本响应。`StateKeyLastResponse` 用于串行路径上的最终输
  出；当多个并行节点在某处汇合时，应从 `StateKeyNodeResponses` 中按节
  点读取各自的输出。
- `StateKeyMetadata`：元数据（用户可用的通用元数据存储）

**系统内部键**（用户不应直接使用）：

- `StateKeySession`：会话信息（由 GraphAgent 自动设置）
- `StateKeyExecContext`：执行上下文（由 Executor 自动设置）
- `StateKeyToolCallbacks`：工具回调（由 Executor 自动设置）
- `StateKeyModelCallbacks`：模型回调（由 Executor 自动设置）

用户应该使用自定义状态键来存储业务数据，只在必要时使用用户可访问的内置状态键。

### 4. 状态模式 (StateSchema)

状态模式定义状态的结构和行为：

```go
import (
    "reflect"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 创建状态模式
schema := graph.NewStateSchema()

// 添加字段定义
schema.AddField("counter", graph.StateField{
    Type:    reflect.TypeOf(0),
    Reducer: graph.DefaultReducer,
    Default: func() any { return 0 },
})
```

## 使用指南

### 节点 I/O 约定

节点之间仅通过共享状态 State 传递数据。每个节点返回一个 state delta，按 Schema 的 Reducer 合并到全局 State，下游节点从 State 读取上游产出。

- 常用内置键（对用户可见）

  - `user_input`：一次性用户输入，被下一个 LLM/Agent 节点消费后清空
  - `one_shot_messages`：一次性完整消息覆盖，用于下一次 LLM 调用，执行后清空
  - `messages`：持久化的消息历史（LLM/Tools 会追加），支持 MessageOp 补丁
  - `last_response`：最近一次助手文本回复
  - `node_responses`：map[nodeID]any，按节点保存最终文本回复。最近结果用 `last_response`

- 函数节点（Function node）

  - 输入：完整 State
  - 输出：返回 `graph.State` 增量，写入自定义键（需在 Schema 中声明），如 `{"parsed_time":"..."}`

- LLM 节点

  - 输入优先级：`one_shot_messages` → `user_input` → `messages`
  - 输出：
    - 向 `messages` 追加助手消息
    - 设置 `last_response`
    - 设置 `node_responses[<llm_node_id>]`

- Tools 节点

  - 输入：从 `messages` 中寻找最新的带 `tool_calls` 的助手消息
  - 输出：向 `messages` 追加工具返回消息

- Agent 节点（子代理）
  - 输入：Graph 的 State 通过 `Invocation.RunOptions.RuntimeState` 传入子代理
    - 子代理的 Model/Tool 回调可通过 `agent.InvocationFromContext(ctx)` 访问
  - 结束输出：
    - 设置 `last_response`
    - 设置 `node_responses[<agent_node_id>]`
    - 清空 `user_input`

推荐用法

- 在 Schema 中声明业务字段（如 `parsed_time`、`final_payload`），函数节点写入/读取。
- 需要给 LLM 节点注入结构化提示时，可在前置节点写入 `one_shot_messages`（例如加入包含解析信息的 system message）。
- 需要消费上游文本结果时：紧邻下游读取 `last_response`，或在任意后续节点读取 `node_responses[节点ID]`。

示例：

- `examples/graph/io_conventions`：函数 + LLM + Agent 的 I/O 演示
- `examples/graph/io_conventions_tools`：加入 Tools 节点，展示如何获取工具 JSON 并落入 State
- `examples/graph/retry`：节点级重试/退避演示

#### 状态键常量与来源（可直接引用）

- 导入包：`import "trpc.group/trpc-go/trpc-agent-go/graph"`
- 常量定义位置：`graph/state.go`

- 用户可见、常用键

  - `user_input` → 常量 `graph.StateKeyUserInput`
  - `one_shot_messages` → 常量 `graph.StateKeyOneShotMessages`
  - `messages` → 常量 `graph.StateKeyMessages`
  - `last_response` → 常量 `graph.StateKeyLastResponse`
  - `node_responses` → 常量 `graph.StateKeyNodeResponses`

- 其他常用键
  - `session` → `graph.StateKeySession`
  - `metadata` → `graph.StateKeyMetadata`
  - `current_node_id` → `graph.StateKeyCurrentNodeID`
  - `exec_context` → `graph.StateKeyExecContext`
  - `tool_callbacks` → `graph.StateKeyToolCallbacks`
  - `model_callbacks` → `graph.StateKeyModelCallbacks`
  - `agent_callbacks` → `graph.StateKeyAgentCallbacks`
  - `parent_agent` → `graph.StateKeyParentAgent`

使用示例：

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func myNode(ctx context.Context, state graph.State) (any, error) {
    // 读取上一节点文本输出
    last, _ := state[graph.StateKeyLastResponse].(string)
    // 写入自定义字段
    return graph.State{"my_key": last}, nil
}
```

#### 事件元数据键（StateDelta）

- 导入包：`import "trpc.group/trpc-go/trpc-agent-go/graph"`
- 常量定义位置：`graph/events.go`

- 模型元数据：`_model_metadata` → `graph.MetadataKeyModel`（结构体 `graph.ModelExecutionMetadata`）
- 工具元数据：`_tool_metadata` → `graph.MetadataKeyTool`（结构体 `graph.ToolExecutionMetadata`）
- 节点元数据：`_node_metadata` → `graph.MetadataKeyNode`（结构体 `graph.NodeExecutionMetadata`）。包含重试字段：`Attempt`、`MaxAttempts`、`NextDelay`、`Retrying` 及时间相关信息。

使用示例：

```go
if b, ok := event.StateDelta[graph.MetadataKeyModel]; ok {
    var md graph.ModelExecutionMetadata
    _ = json.Unmarshal(b, &md)
}
```

#### 节点主动外抛事件（EventEmitter）

在 NodeFunc 执行过程中，节点可以通过 `EventEmitter` 主动向外部发射自定义事件，用于实时传递进度、中间结果或自定义业务数据。

**获取 EventEmitter**

```go
func myNode(ctx context.Context, state graph.State) (any, error) {
    // 从 State 中获取 EventEmitter
    emitter := graph.GetEventEmitterWithContext(ctx, state)
    // 或直接调用 emitter := graph.GetEventEmitter(state)
    return state, nil
}
```

**EventEmitter 接口**

```go
type EventEmitter interface {
    // Emit 发射任意事件
    Emit(evt *event.Event) error
    // EmitCustom 发射自定义事件
    EmitCustom(eventType string, payload any) error
    // EmitProgress 发射进度事件（progress: 0-100）
    EmitProgress(progress float64, message string) error
    // EmitText 发射流式文本事件
    EmitText(text string) error
    // Context 返回关联的 context
    Context() context.Context
}
```

**使用示例**

```go
func dataProcessNode(ctx context.Context, state graph.State) (any, error) {
    emitter := graph.GetEventEmitter(state)
    
    // 1. 发射自定义事件
    emitter.EmitCustom("data.loaded", map[string]any{
        "recordCount": 1000,
        "source":      "database",
    })
    
    // 2. 发射进度事件
    total := 100
    for i := 0; i < total; i++ {
        processItem(i)
        progress := float64(i+1) / float64(total) * 100
        emitter.EmitProgress(progress, fmt.Sprintf("Processing %d/%d", i+1, total))
    }
    
    // 3. 发射流式文本
    emitter.EmitText("Processing complete.\n")
    emitter.EmitText("Results: 100 items processed successfully.")
    
    return state, nil
}
```

**事件流转流程**

```mermaid
sequenceDiagram
    autonumber
    participant NF as NodeFunc
    participant EE as EventEmitter
    participant EC as EventChan
    participant EX as Executor
    participant TR as AGUI Translator
    participant FE as 客户端

    Note over NF,FE: 事件发射阶段
    NF->>EE: GetEventEmitter(state)
    EE-->>NF: 返回 EventEmitter 实例
    
    alt 发射自定义事件
        NF->>EE: EmitCustom(eventType, payload)
    else 发射进度事件
        NF->>EE: EmitProgress(progress, message)
    else 发射文本事件
        NF->>EE: EmitText(text)
    end
    
    Note over EE: 构建 NodeCustomEventMetadata
    EE->>EE: 注入 NodeID, InvocationID, Timestamp
    EE->>EC: 发送 Event 到 Chan
    
    Note over EC,FE: 事件消费阶段
    EC->>EX: Executor 接收事件
    EX->>TR: 传递事件给 Translator
    
    alt Custom 类型事件
        TR->>TR: 转换为 AG-UI CustomEvent
    else Progress 类型事件
        TR->>TR: 转换为 AG-UI CustomEvent (含进度信息)
    else Text 类型事件 (消息上下文)
        TR->>TR: 转换为 TextMessageContentEvent
    else Text 类型事件 (非消息上下)
        TR->>TR: 转换为 AG-UI CustomEvent
    end
    
    TR->>FE: SSE 推送 AG-UI 事件
    FE->>FE: 处理并更新 UI
```

**AGUI 事件转换**

当使用 AGUI Server 时，节点发射的事件会自动转换为 AG-UI 协议事件：

| 节点事件类型 | AG-UI 事件类型 | 说明 |
|-------------|--------------|------|
| Custom | CustomEvent | 自定义事件，payload 在 `value` 字段 |
| Progress | CustomEvent | 进度事件，包含 `progress` 和 `message` |
| Text（消息上下文中）| TextMessageContentEvent | 流式文本追加到当前消息 |
| Text（非消息上下文）| CustomEvent | 包含 `nodeId` 和 `content` 字段 |

**注意事项**

- **线程安全**：EventEmitter 是线程安全的，可在并发环境中使用
- **优雅降级**：若 State 中无有效的 ExecutionContext 或 EventChan，`GetEventEmitter` 返回 no-op emitter，所有操作静默成功
- **错误处理**：业务实践中建议事件发射失败不要中断节点执行，仅记录警告日志，忽略异常
- **自定义事件元数据**：`_node_custom_metadata` → `graph.MetadataKeyNodeCustom`（结构体 `graph.NodeCustomEventMetadata`）

### 1. 创建 GraphAgent 和 Runner

用户主要通过创建 GraphAgent 然后通过 Runner 来使用 Graph 包。这是推荐的使用模式：

```go
package main

import (
    "context"
    "fmt"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    // 1. 创建状态模式
    schema := graph.MessagesStateSchema()

    // 2. 创建状态图构建器
    stateGraph := graph.NewStateGraph(schema)

    // 3. 添加节点
    stateGraph.AddNode("start", startNodeFunc).
        AddNode("process", processNodeFunc)

    // 4. 设置边
    stateGraph.AddEdge("start", "process")

    // 5. 设置入口点和结束点
    // SetEntryPoint 会自动创建虚拟 Start 节点到 "start" 节点的边
    // SetFinishPoint 会自动创建 "process" 节点到虚拟 End 节点的边
    stateGraph.SetEntryPoint("start").
        SetFinishPoint("process")

    // 6. 编译图
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        panic(err)
    }

    // 7. 创建 GraphAgent
    graphAgent, err := graphagent.New("simple-workflow", compiledGraph,
        graphagent.WithDescription("简单的工作流示例"),
        graphagent.WithInitialState(graph.State{}),
        // 设置传给模型的消息过滤模式，最终传给模型的消息需同时满足WithMessageTimelineFilterMode与WithMessageBranchFilterMode条件
        // 时间维度过滤条件
        // 默认值: graphagent.TimelineFilterAll
        // 可选值:
        //  - graphagent.TimelineFilterAll: 包含历史消息以及当前请求中所生成的消息
        //  - graphagent.TimelineFilterCurrentRequest: 仅包含当前请求中所生成的消息
        //  - graphagent.TimelineFilterCurrentInvocation: 仅包含当前invocation上下文中生成的消息
        graphagent.WithMessageTimelineFilterMode(graphagent.TimelineFilterAll),
        // 分支维度过滤条件
        // 默认值: graphagent.BranchFilterModePrefix
        // 可选值:
        //  - graphagent.BranchFilterModeAll: 包含所有agent的消息, 当前agent与模型交互时,如需将所有agent生成的有效内容消息同步给模型时可设置该值
        //  - graphagent.BranchFilterModePrefix: 通过Event.FilterKey与Invocation.eventFilterKey做前缀匹配过滤消息, 期望将与当前agent以及相关上下游agent生成的消息传递给模型时，可设置该值
        //  - graphagent.BranchFilterModeExact: 通过Event.FilterKey==Invocation.eventFilterKey过滤消息，当前agent与模型交互时,仅需使用当前agent生成的消息时可设置该值
        graphagent.WithMessageBranchFilterMode(graphagent.BranchFilterModeAll),
    )
    if err != nil {
        panic(err)
    }

    // 8. 创建会话服务
    sessionService := inmemory.NewSessionService()

    // 9. 创建 Runner
    appRunner := runner.NewRunner(
        "simple-app",
        graphAgent,
        runner.WithSessionService(sessionService),
    )

    // 10. 执行工作流
    ctx := context.Background()
    userID := "user"
    sessionID := fmt.Sprintf("session-%d", time.Now().Unix())

    // 创建用户消息（Runner 会自动将消息内容放入 StateKeyUserInput）
    message := model.NewUserMessage("Hello World")

    // 通过 Runner 执行
    eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
    if err != nil {
        panic(err)
    }

    // 处理事件流
    for event := range eventChan {
        if event.Error != nil {
            fmt.Printf("错误: %s\n", event.Error.Message)
            continue
        }

        if len(event.Response.Choices) > 0 {
            choice := event.Response.Choices[0]
            if choice.Delta.Content != "" {
                fmt.Print(choice.Delta.Content)
            }
        }

        // 推荐：使用 Runner 完成事件作为“流程结束”的信号。
        if event.IsRunnerCompletion() {
            break
        }
    }
}

const (
    stateKeyProcessedData = "processed_data"
    stateKeyResult        = "result"
)

// 起始节点函数实现
func startNodeFunc(ctx context.Context, state graph.State) (any, error) {
    // 从内置的 StateKeyUserInput 获取用户输入（由 Runner 自动设置）
    input := state[graph.StateKeyUserInput].(string)
    return graph.State{
        stateKeyProcessedData: fmt.Sprintf("处理后的: %s", input),
    }, nil
}

// 处理节点函数实现
func processNodeFunc(ctx context.Context, state graph.State) (any, error) {
    processed := state[stateKeyProcessedData].(string)
    result := fmt.Sprintf("结果: %s", processed)
    return graph.State{
        stateKeyResult:             result,
        graph.StateKeyLastResponse: fmt.Sprintf("最终结果: %s", result),
    }, nil
}

```

### 2. 使用 LLM 节点

LLM 节点实现了固定的三段式输入规则，无需配置：

1. **OneShot 优先**：若存在 `one_shot_messages`，以它为本轮输入。
2. **UserInput 其次**：否则若存在 `user_input`，自动持久化一次。
3. **历史默认**：否则以持久化历史作为输入。

```go
// 创建 LLM 模型
model := openai.New("gpt-4")

// 添加 LLM 节点
stateGraph.AddLLMNode("analyze", model,
    `你是一个文档分析专家。分析提供的文档并：
1. 分类文档类型和复杂度
2. 提取关键主题
3. 评估内容质量
请提供结构化的分析结果。`,
    nil) // 工具映射
```

**重要说明**：

- SystemPrompt 仅用于本次输入，不落持久化状态。
- 一次性键（`user_input`/`one_shot_messages`）在成功执行后自动清空。
- 所有状态更新都是原子性的，确保一致性。
- GraphAgent/Runner 仅设置 `user_input`，不再预先把用户消息写入
  `messages`。这样可以允许在 LLM 节点之前的任意节点对 `user_input`
  进行修改，并能在同一轮生效。

#### 三种输入范式

- OneShot（`StateKeyOneShotMessages`）：

  - 当该键存在时，本轮仅使用这里提供的 `[]model.Message` 调用模型，
    通常包含完整的 system prompt 与 user prompt。调用后自动清空。
  - 适用场景：前置节点专门构造 prompt 的工作流，需完全覆盖本轮输入。

- UserInput（`StateKeyUserInput`）：

  - 当 `user_input` 非空时，LLM 节点会取持久化历史 `messages`，并将
    本轮的用户输入合并后发起调用。结束后会把用户输入与助手回复通过
    `MessageOp`（例如 `AppendMessages`、`ReplaceLastUser`）原子性写入
    到 `messages`，并自动清空 `user_input` 以避免重复追加。
  - 适用场景：普通对话式工作流，允许在前置节点动态调整用户输入。

- Messages only（仅 `StateKeyMessages`）：
  - 多用于工具调用回路。当第一轮经由 `user_input` 发起后，路由到工具
    节点执行，再回到 LLM 节点时，因为 `user_input` 已被清空，LLM 将走
    “Messages only” 分支，以历史中的 tool 响应继续推理。

#### LLM 指令中的占位符

LLM 节点的 `instruction` 支持占位符注入（与 LLMAgent 规则一致）。支持原生 `{key}` 与 Mustache `{{key}}` 两种写法（Mustache 会自动规整为原生写法）：

- `{key}` / `{{key}}` → 替换为 `session.State["key"]`
- `{key?}` / `{{key?}}` → 可选，缺失时替换为空
- `{user:subkey}`、`{app:subkey}`、`{temp:subkey}`（以及其 Mustache 写法）→ 访问用户/应用/临时命名空间（SessionService 会将 app/user 作用域合并到 session，并带上前缀）

说明：

- GraphAgent 会把当前 `*session.Session` 写入图状态的 `StateKeySession`，LLM 节点据此读取注入值
- 无前缀键（如 `research_topics`）需要直接存在于 `session.State`

示例：

```go
mdl := openai.New(modelName)
stateGraph.AddLLMNode(
  "research",
  mdl,
  "You are a research assistant. Focus: {research_topics}. User: {user:topics?}. App: {app:banner?}.",
  nil,
)
```

可参考可运行示例：`examples/graph/placeholder`。

将检索结果与用户输入注入指令

- 在进入 LLM 节点前的任意节点，将临时值写入会话的 `temp:` 命名空间，LLM 指令即可用占位符读取。
- 示例模式：

```go
// 在 LLM 节点之前
stateGraph.AddNode("retrieve", func(ctx context.Context, s graph.State) (any, error) {
    // 假设你已经得到检索内容 retrieved，并希望连同当前用户输入一起注入
    retrieved := "• 文档A...\n• 文档B..."
    var input string
    if v, ok := s[graph.StateKeyUserInput].(string); ok { input = v }
    if sess, _ := s[graph.StateKeySession].(*session.Session); sess != nil {
        if sess.State == nil { sess.State = make(session.StateMap) }
        sess.State[session.StateTempPrefix+"retrieved_context"] = []byte(retrieved)
        sess.State[session.StateTempPrefix+"user_input"] = []byte(input)
    }
    return graph.State{}, nil
})

// LLM 节点的指令引用 {temp:retrieved_context} 和 {temp:user_input}
stateGraph.AddLLMNode("answer", mdl,
    "请结合上下文回答。\n\n上下文：\n{temp:retrieved_context}\n\n问题：{temp:user_input}",
    nil)
```

示例：`examples/graph/retrieval_placeholder`。

占位符与会话状态的最佳实践

- 短期 vs 持久：只用于本轮提示词组装的数据写到 `session.State` 的 `temp:*`；需要跨轮/跨会话保留的配置，请通过 SessionService（会话服务）更新 `user:*`/`app:*`。
- 为什么可以直接写：LLM 节点从图状态里的会话对象读取并展开占位符，见 [graph/state_graph.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/state_graph.go)；GraphAgent 在启动时把会话对象放入图状态，见 [agent/graphagent/graph_agent.go](https://github.com/trpc-group/trpc-agent-go/blob/main/agent/graphagent/graph_agent.go)。
- 服务侧护栏：内存实现禁止通过“更新用户态”的接口写 `temp:*`（以及 `app:*` via user updater），见 [session/inmemory/service.go](https://github.com/trpc-group/trpc-agent-go/blob/main/session/inmemory/service.go)。
- 并发建议：并行分支不要同时改同一批 `session.State` 键；建议汇总到单节点合并后一次写入，或先放图状态再一次写到 `temp:*`。
- 可观测性：若希望在完成事件中看到摘要，可额外把精简信息放入图状态（如 `metadata`）；最终事件会序列化非内部的最终状态，见 [graph/events.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/events.go)。

#### 通过 Reducer 与 MessageOp 实现的原子更新

Graph 包的消息状态支持 `MessageOp` 补丁操作（如 `ReplaceLastUser`、
`AppendMessages` 等），由 `MessageReducer` 实现原子合并。这带来两个
直接收益：

- 允许在 LLM 节点之前修改 `user_input`，LLM 节点会据此在一次返回中将
  需要的操作（例如替换最后一条用户消息、追加助手消息）以补丁形式返回，
  执行器一次性落库，避免竞态与重复。`
- 兼容传统的直接 `[]Message` 追加用法，同时为复杂更新提供更高的表达力。

示例：在前置节点修改 `user_input`，随后进入 LLM 节点。

```go
stateGraph.
    AddNode("prepare_input", func(ctx context.Context, s graph.State) (any, error) {
        // 清洗/改写用户输入，使其在本轮 LLM 中生效。
        cleaned := strings.TrimSpace(s[graph.StateKeyUserInput].(string))
        return graph.State{graph.StateKeyUserInput: cleaned}, nil
    }).
    AddLLMNode("ask", modelInstance,
        "你是一个有帮助的助手。请简洁回答。",
        nil).
    SetEntryPoint("prepare_input").
    SetFinishPoint("ask")
```

#### 使用 RemoveAllMessages 清除消息历史

当多个 LLM 节点串联时，`MessageReducer` 会累积消息。如果每个 LLM 节点
需要独立的消息上下文（不继承前序节点的对话历史），可以使用
`RemoveAllMessages` 操作清除之前的消息：

```go
// 在准备 prompt 的节点中，先清除消息再设置新的 UserInput.
func preparePromptNode(ctx context.Context, state graph.State) (any, error) {
    userMessage := buildUserMessage(...)
    return graph.State{
        // 关键：先清除之前的消息，避免累积.
        graph.StateKeyMessages:  graph.RemoveAllMessages{},
        graph.StateKeyUserInput: userMessage,
    }, nil
}
```

**适用场景**：

- 同一个 Graph 内有多个独立的 LLM 节点，每个节点不需要前序节点的对话历史
- 循环结构中，每次迭代需要全新的消息上下文
- 需要完全重建消息列表的场景

**注意**：

- `RemoveAllMessages{}` 是一个特殊的 `MessageOp`，`MessageReducer` 会
  识别它并清空消息列表
- 必须在设置 `StateKeyUserInput` **之前**先设置 `RemoveAllMessages{}`
- `WithSubgraphIsolatedMessages(true)` 只对 `AddSubgraphNode` 有效，
  对 `AddLLMNode` 无效；如需在 LLM 节点间隔离消息，请使用
  `RemoveAllMessages`

### 3. GraphAgent 配置选项

GraphAgent 支持多种配置选项：

```go
// 创建 GraphAgent 时可以使用多种选项
graphAgent, err := graphagent.New(
	"workflow-name",
	compiledGraph,
	graphagent.WithDescription("工作流描述"),
	graphagent.WithInitialState(graph.State{
		"initial_data": "初始数据",
	}),
	graphagent.WithChannelBufferSize(1024),            // 调整事件通道缓冲区
	graphagent.WithCheckpointSaver(memorySaver),       // 使用持久化检查点
	graphagent.WithSubAgents([]agent.Agent{subAgent}), // 配置子 Agent
	graphagent.WithAddSessionSummary(true),            // 将会话摘要注入 system 消息
	graphagent.WithMaxHistoryRuns(5),                  // 未开启摘要时截断历史轮次
	// 设置传给模型的消息过滤模式，最终传给模型的消息需同时满足WithMessageTimelineFilterMode与WithMessageBranchFilterMode条件
	// 时间维度过滤条件
	// 默认值: graphagent.TimelineFilterAll
	// 可选值:
	//  - graphagent.TimelineFilterAll: 包含历史消息以及当前请求中所生成的消息
	//  - graphagent.TimelineFilterCurrentRequest: 仅包含当前请求中所生成的消息
	//  - graphagent.TimelineFilterCurrentInvocation: 仅包含当前invocation上下文中生成的消息
	graphagent.WithMessageTimelineFilterMode(graphagent.BranchFilterModeAll),
	// 分支维度过滤条件
	// 默认值: graphagent.BranchFilterModePrefix
	// 可选值:
	//  - graphagent.BranchFilterModeAll: 包含所有agent的消息, 当前agent与模型交互时,如需将所有agent生成的有效内容消息同步给模型时可设置该值
	//  - graphagent.BranchFilterModePrefix: 通过Event.FilterKey与Invocation.eventFilterKey做前缀匹配过滤消息, 期望将与当前agent以及相关上下游agent生成的消息传递给模型时，可设置该值
	//  - graphagent.BranchFilterModeExact: 通过Event.FilterKey==Invocation.eventFilterKey过滤消息，当前agent与模型交互时,仅需使用当前agent生成的消息时可设置该值
	graphagent.WithMessageBranchFilterMode(graphagent.TimelineFilterAll),
	// 推理内容模式（DeepSeek 思考模式）
	// 默认值: graphagent.ReasoningContentModeDiscardPreviousTurns
	// 可选值:
	//  - graphagent.ReasoningContentModeDiscardPreviousTurns: 丢弃之前请求轮次的
	//    reasoning_content（默认，推荐）
	//  - graphagent.ReasoningContentModeKeepAll: 保留所有 reasoning_content
	//  - graphagent.ReasoningContentModeDiscardAll: 丢弃所有 reasoning_content
	graphagent.WithReasoningContentMode(graphagent.ReasoningContentModeDiscardPreviousTurns),
	graphagent.WithAgentCallbacks(&agent.Callbacks{
		// Agent 级回调配置
	}),
)
```

> 模型/工具回调需要在节点级配置，例如 `AddLLMNode(..., graph.WithModelCallbacks(...))`
> 或 `AddToolsNode(..., graph.WithToolCallbacks(...))`。

使用会话摘要的注意事项：

- `WithAddSessionSummary(true)` 仅在 `Session.Summaries` 中已有对应 `FilterKey` 的摘要时生效。摘要通常由 SessionService + SessionSummarizer 生成，Runner 在落库事件后会自动触发 `EnqueueSummaryJob`。
- GraphAgent 只读取摘要，不生成摘要。如果绕过 Runner，需在写入事件后自行调用 `sessionService.CreateSessionSummary` 或 `EnqueueSummaryJob`。
- 摘要仅在 `TimelineFilterAll` 下生效。

#### 并发使用注意事项

在服务端将 Graph + GraphAgent 部署为长期运行、可并发复用的组件时（例如单个进程内同时处理多路请求），需要注意以下几点：

- 自定义 CheckpointSaver / Cache 必须具备并发安全能力  
  `CheckpointSaver` 与 `Cache` 接口本身是存储无关、线程无状态的抽象，同一个 `Executor` / `GraphAgent` 实例在并发执行多次时，会从多个 goroutine 调用它们的方法。如果你提供自定义实现，需要确保：
  - `CheckpointSaver` 的 `Get` / `GetTuple` / `List` / `Put` / `PutWrites` / `PutFull` / `DeleteLineage` 等方法在并发场景下是安全的；
  - `Cache` 的 `Get` / `Set` / `Clear` 等方法在并发场景下是安全的；
  - 内部使用的 map、连接池、内存缓冲区等都经过适当加锁或使用其它并发原语保护。

- NodeFunc / 工具 / 回调应把传入状态视为“本次调用本节点的局部数据”  
  每个节点在执行时会拿到一份针对当前任务的状态副本，这份副本在当前 goroutine 内修改是安全的，但是不建议：
  - 将该副本（或其中的 map / slice）保存到全局变量，再由其它 goroutine 后续修改；
  - 从状态中取出 `StateKeyExecContext`（`*graph.ExecutionContext`）后，绕过内部锁直接读写 `execCtx.State` 或 `execCtx.pendingTasks` 等字段。
  如果确实需要跨节点或跨调用共享可变数据，请使用你自己的同步手段（例如 `sync.Mutex` / `sync.RWMutex`），或者使用外部服务（数据库、缓存等）承载共享状态。

- 不要在多个 goroutine 中复用同一个 *agent.Invocation  
  框架设计假定每次调用 `Run`（无论是 GraphAgent、Runner 还是其它 Agent 实现）都使用独立的 `*agent.Invocation` 实例。如果在多个 goroutine 中复用同一个 `*agent.Invocation` 并并行调用 `Run`，会在 `Branch`、`RunOptions`、回调状态等字段上产生数据竞争。推荐的做法是：
  - 每个请求构造一份新的 `*agent.Invocation`，或者
  - 在需要保持关联时使用 `invocation.Clone(...)` 从父调用上克隆一份新的 Invocation。

- 并行工具调用要求具体工具实现本身支持并发  
  当某个 Tools 节点使用 `WithEnableParallelTools(true)` 配置为“并行工具执行”时，在同一轮 step 内该节点会为每个工具调用起一个 goroutine 并行执行：
  - 框架保证 `tools` 映射在执行期间只读访问；
  - 框架也保证传入工具的图状态在工具内部只读使用，状态更新由节点返回的 `State` 合并完成。
  但每个具体的 `tool.Tool` 实现以及对应的 `tool.Callbacks` 可能会被多个 goroutine 同时调用，因此需要确保：
  - 工具实现不会在没有加锁的情况下修改共享的全局变量或共享数据结构；
  - 工具内部使用的缓存、HTTP 客户端、连接池等组件本身支持并发访问。

这些约束在单进程多请求的高并发场景下尤为重要，可以帮助你安全地复用同一个 `Graph` / `Executor` / `GraphAgent` 实例。

配置了子 Agent 后，可以在图中使用 Agent 节点委托执行：

```go
// 假设 subAgent.Info().Name == "assistant"
stateGraph.AddAgentNode("assistant",
    graph.WithName("子 Agent 调度"),
    graph.WithDescription("调用预先注册的 assistant Agent"),
)

// 执行时 GraphAgent 会在自身的 SubAgents 中查找同名 Agent 并发起调用
```

> Agent 节点会以节点 ID 作为查找键，因此需确保 `AddAgentNode("assistant")`
> 与 `subAgent.Info().Name == "assistant"` 一致。

### 4. 条件路由

```go
// 定义条件函数
func complexityCondition(ctx context.Context, state graph.State) (string, error) {
    complexity := state["complexity"].(string)
    if complexity == "simple" {
        return "simple_process", nil
    }
    return "complex_process", nil
}

// 添加条件边
stateGraph.AddConditionalEdges("analyze", complexityCondition, map[string]string{
    "simple_process":  "simple_node",
    "complex_process": "complex_node",
})
```

### 4.1 节点命名分支（Ends）

当一个节点需要把“业务结论”（例如 `approve`/`reject`/`manual_review`）路由到具体的下游节点时，建议在该节点上声明命名分支（Ends）。

作用与优势：

- 在节点本地集中声明“符号名 → 具体目标”的映射，更直观、更易重构；
- 编译期校验：`Compile()` 会检查映射中的目标是否存在（或为常量 `graph.End`）；
- 路由统一：命令式路由（`Command.GoTo`）与条件路由都可复用同一份映射；
- 解耦：节点返回业务语义（例如 `GoTo:"approve"`），映射负责落地到图结构。

API：

```go
// 在节点上声明 Ends（符号名到具体目标）
sg.AddNode("decision", decideNode,
    graph.WithEndsMap(map[string]string{
        "approve": "approved",
        "reject":  "rejected",
        // 也可以把某个符号映射到终点
        // "drop":  graph.End,
    }),
)

// 简写：符号名与目标节点名相同的情况
sg.AddNode("router", routerNode, graph.WithEnds("nodeB", "nodeC"))
```

命令式路由（Command.GoTo）：

```go
func decideNode(ctx context.Context, s graph.State) (any, error) {
    switch s["decision"].(string) {
    case "approve":
        return &graph.Command{GoTo: "approve"}, nil // 符号名
    case "reject":
        return &graph.Command{GoTo: "reject"}, nil
    default:
        return &graph.Command{GoTo: "reject"}, nil
    }
}
```

条件路由复用 Ends：当 `AddConditionalEdges(from, condition, pathMap)` 的 `pathMap` 为 `nil` 或未匹配到键时，执行器会继续使用该节点的 Ends 解析返回值；若仍未匹配，则把返回值当作具体节点 ID。

解析优先级：

1. 条件边 `pathMap` 的显式映射；
2. 当前节点的 Ends 映射（符号名 → 具体目标）；
3. 直接把返回值当作节点 ID。

编译期校验：

- `WithEndsMap/WithEnds` 中声明的目标会在 `Compile()` 阶段校验；
- 目标必须存在于图中或为特殊常量 `graph.End`。

注意：

- 请使用常量 `graph.End` 表示“终点”，不要使用字符串 "END"；
- 当通过 `Command.GoTo` 进行路由时，无需额外添加 `AddEdge(from, to)`；只需保证目标节点存在，若为终点需设置 `SetFinishPoint(target)`。

完整可运行示例：`examples/graph/multiends`。

### 4.2 多条件扇出（并行）

当一次决策需要“同时”走多个分支（例如同时做摘要与打标签），可使用
`AddMultiConditionalEdges`：

```go
// 返回多个分支键，每个分支键解析为一个具体目标节点。
sg.AddMultiConditionalEdges(
    "router",
    func(ctx context.Context, s graph.State) ([]string, error) {
        // 一次决策，同时走两个分支
        return []string{"toA", "toB"}, nil
    },
    map[string]string{
        "toA": "A", // 分支键 -> 目标节点
        "toB": "B",
    },
)
```

说明：

- 返回结果会先去重，同一分支键在同一步内只触发一次；
- 每个分支键的解析优先级与单条件路由一致：
  1. 条件边 `pathMap`；2) 节点 Ends；3) 直接当作节点 ID；
- 可视化：当未提供 `pathMap` 时，DOT 会回退使用该节点的 Ends 来渲染虚线
  条件边（仅用于可视化，方便理解流程）。

### 5. 工具节点集成

```go
// 创建工具
tools := map[string]tool.Tool{
    "calculator": calculatorTool,
    "search":     searchTool,
}

// 添加工具节点
stateGraph.AddToolsNode("tools", tools)

// 添加 LLM 到工具的条件路由
stateGraph.AddToolsConditionalEdges("llm_node", "tools", "fallback_node")
```

开启工具并行执行（与 LLMAgent 的选项对齐）：

```go
// 当同一条 assistant 消息包含多个 tool_calls 时，并行执行以加速整体耗时。
stateGraph.AddToolsNode(
    "tools",
    tools,
    graph.WithEnableParallelTools(true), // 可选；默认串行
)
```

**工具调用配对机制与二次进入 LLM：**

- 从 `messages` 尾部向前扫描最近的 `assistant(tool_calls)`；遇到 `user`
  则停止，确保配对正确。
- 当工具节点完成后返回到 LLM 节点时，`user_input` 已被清空，LLM 将走
  “Messages only” 分支，以历史中的 tool 响应继续推理。

### 6. 节点重试与退避

为节点配置指数退避的重试策略（可选抖动）。失败的尝试不会产生写入；只有成功的一次才会落库并触发路由。

- 节点级策略（`WithRetryPolicy`）：

```go
// 便捷策略（attempts 含首次尝试）
sg.AddNode("unstable", unstableFunc,
    graph.WithRetryPolicy(graph.WithSimpleRetry(3)))

// 完整策略
policy := graph.RetryPolicy{
    MaxAttempts:     3,                      // 1 次首试 + 最多 2 次重试
    InitialInterval: 200 * time.Millisecond, // 基础等待
    BackoffFactor:   2.0,                    // 指数增长
    MaxInterval:     2 * time.Second,        // 上限
    Jitter:          true,                   // 抖动
    RetryOn: []graph.RetryCondition{
        graph.DefaultTransientCondition(),   // 截止/网络超时等瞬时错误
        graph.RetryOnErrors(context.DeadlineExceeded),
        graph.RetryOnPredicate(func(error) bool { return true }),
    },
    MaxElapsedTime:  5 * time.Second,        // 总重试预算（可选）
    // PerAttemptTimeout: 0,                 // 预留；节点超时由执行器控制
}
sg.AddNode("unstable", unstableFunc, graph.WithRetryPolicy(policy))
```

- 执行器默认策略（当节点未配置时生效）：

```go
exec, _ := graph.NewExecutor(compiled,
    graph.WithDefaultRetryPolicy(graph.WithSimpleRetry(2)))
```

注意事项

- 中断（interrupt）不参与重试。
- 当设置了步骤超时（`WithStepTimeout`）时，退避时间会被当前步骤的截止时间钳制。
- 事件会携带重试元数据，便于 CLI/UI 展示进度：

```go
if b, ok := ev.StateDelta[graph.MetadataKeyNode]; ok {
    var md graph.NodeExecutionMetadata
    _ = json.Unmarshal(b, &md)
    if md.Phase == graph.ExecutionPhaseError && md.Retrying {
        // md.Attempt, md.MaxAttempts, md.NextDelay
    }
}
```

示例：`examples/graph/retry` 展示了一个会先失败后成功的节点，并在成功后进入下游 LLM 输出最终答案。

### 7. Runner 配置

Runner 提供了会话管理和执行环境：

```go
// 创建会话服务
sessionService := inmemory.NewSessionService()
// 或者使用 Redis 会话服务
// sessionService, err := redis.NewService(redis.WithRedisClientURL("redis://localhost:6379"))

// 创建 Runner
appRunner := runner.NewRunner(
    "app-name",
    graphAgent,
    runner.WithSessionService(sessionService),
    // 可以添加更多配置选项
)

// 使用 Runner 执行工作流
// Runner 仅设置 StateKeyUserInput，不再预先写入 StateKeyMessages。
message := model.NewUserMessage("用户输入")
eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
```

### 8. 消息状态模式

对于对话式应用，可以使用预定义的消息状态模式：

```go
// 使用消息状态模式
schema := graph.MessagesStateSchema()

// 这个模式包含：
// - messages: 对话历史（StateKeyMessages）
// - user_input: 用户输入（StateKeyUserInput）
// - last_response: 最后响应（StateKeyLastResponse）
// - node_responses: 节点响应映射（StateKeyNodeResponses）
// - metadata: 元数据（StateKeyMetadata）
```

### 9. 状态键使用场景

**用户自定义状态键**：用于存储业务逻辑数据

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 推荐：使用自定义状态键
const (
    StateKeyDocumentLength = "document_length"
    StateKeyComplexityLevel = "complexity_level"
    StateKeyProcessingStage = "processing_stage"
)

// 在节点中使用
return graph.State{
    StateKeyDocumentLength: len(input),
    StateKeyComplexityLevel: "simple",
    StateKeyProcessingStage: "completed",
}, nil
```

**内置状态键**：用于系统集成

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 获取用户输入（由系统自动设置）
userInput := state[graph.StateKeyUserInput].(string)

// 设置最终输出（系统会读取此值）
return graph.State{
    graph.StateKeyLastResponse: "处理完成",
}, nil

// 当多个节点（例如并行的 LLM 节点）同时产出结果时，使用按节点响应。
// 该值是 map[nodeID]any，会在执行过程中合并。串行路径使用
// LastResponse；并行节点汇合时使用 NodeResponses。
responses, _ := state[graph.StateKeyNodeResponses].(map[string]any)
news := responses["news"].(string)
dialog := responses["dialog"].(string)

// 分别使用或组合成最终输出。
return graph.State{
    "news_output":   news,
    "dialog_output": dialog,
    graph.StateKeyLastResponse: news + "\n" + dialog,
}, nil

// 存储元数据
return graph.State{
    graph.StateKeyMetadata: map[string]any{
        "timestamp": time.Now(),
        "version": "1.0",
    },
}, nil
```

## 高级功能

### 1. 中断和恢复（人机协作）

Graph 包通过中断和恢复功能支持人机协作 (HITL) 工作流。这使得工作流可以暂停执行，等待人工输入或审批，然后从中断的确切位置恢复。

#### 基本用法

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 创建一个可以中断执行等待人工输入的节点
b.AddNode("approval_node", func(ctx context.Context, s graph.State) (any, error) {
    // 使用 Interrupt 助手函数进行干净的中断/恢复处理
    prompt := map[string]any{
        "message": "请审批此操作 (yes/no):",
        "data":    s["some_data"],
    }
})
```

用代码把这个图变成可运行的工作流：

```go
package main

import (
    "context"
    "fmt"
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 非导出常量，避免魔法字符串
const (
    nodePrepare   = "prepare"
    nodeAsk       = "ask"
    nodeTools     = "tools"
    nodeFallback  = "fallback"
    nodeFinish    = "finish"

    modelName     = "gpt-4o-mini"
    systemPrompt  = "你是一个谨慎的助手。"
    outputKeyFinal = "final_output"

    toolNameCalculator = "calculator"

    demoUserID    = "user"
    demoSessionID = "session"
    demoQuestion  = "6 * 7 等于多少？"
)

func newCalculator() tool.Tool {
    type Input struct {
        Expression string `json:"expression"`
    }
    type Output struct {
        Result float64 `json:"result"`
    }
    return function.NewFunctionTool[Input, Output](
        func(ctx context.Context, in Input) (Output, error) {
            // 在此实现真实计算逻辑
            return Output{Result: 42}, nil
        },
        function.WithName(toolNameCalculator),
        function.WithDescription("计算数学表达式"),
    )
}

func buildWorkflow(m model.Model, tools map[string]tool.Tool) (*graph.Graph, error) {
    sg := graph.NewStateGraph(graph.MessagesStateSchema())

    sg.AddNode(nodePrepare, func(ctx context.Context, s graph.State) (any, error) {
        raw := fmt.Sprint(s[graph.StateKeyUserInput])
        cleaned := strings.TrimSpace(raw)
        return graph.State{graph.StateKeyUserInput: cleaned}, nil
    })

    sg.AddLLMNode(nodeAsk, m, systemPrompt, tools)
    sg.AddToolsNode(nodeTools, tools)

    sg.AddNode(nodeFallback, func(ctx context.Context, s graph.State) (any, error) {
        return graph.State{graph.StateKeyLastResponse: "无需工具，直接回答"}, nil
    })

    sg.AddNode(nodeFinish, func(ctx context.Context, s graph.State) (any, error) {
        return graph.State{outputKeyFinal: fmt.Sprint(s[graph.StateKeyLastResponse])}, nil
    })

    sg.SetEntryPoint(nodePrepare)
    sg.AddEdge(nodePrepare, nodeAsk)
    sg.AddToolsConditionalEdges(nodeAsk, nodeTools, nodeFallback)
    sg.AddEdge(nodeTools, nodeAsk)
    sg.AddEdge(nodeFallback, nodeFinish)
    sg.SetFinishPoint(nodeFinish)

    return sg.Compile()
}

func main() {
    mdl := openai.New(modelName)
    tools := map[string]tool.Tool{toolNameCalculator: newCalculator()}

    g, err := buildWorkflow(mdl, tools)
    if err != nil {
        panic(err)
    }

    // 使用 GraphAgent + Runner 运行
    ga, err := graphagent.New("demo", g)
    if err != nil {
        panic(err)
    }
    app := runner.NewRunner("app", ga)
    events, err := app.Run(context.Background(), demoUserID, demoSessionID,
        model.NewUserMessage(demoQuestion))
    if err != nil {
        panic(err)
    }
    for ev := range events {
        if ev.Response == nil {
            continue
        }
        if ev.Author == nodeAsk && !ev.Response.IsPartial && len(ev.Response.Choices) > 0 {
            fmt.Println("LLM:", ev.Response.Choices[0].Message.Content)
        }
    }
}
```

上面的例子展示了如何声明节点、连边并运行。接下来先介绍执行方式与会话管理，然后进入核心概念与常见用法。

### 执行方式

- 用 `graphagent.New` 包装成通用 `agent.Agent`，交给 `runner.Runner` 管理会话与事件流。

最小 GraphAgent + Runner 例子：

```go
compiled, _ := buildWorkflow(openai.New("gpt-4o-mini"), nil)
ga, _ := graphagent.New("demo", compiled)
app := runner.NewRunner("app", ga)

events, _ := app.Run(ctx, "user", "session", model.NewUserMessage("hi"))
for ev := range events { /* 处理事件 */ }
```

Runner 会话后端可选项：

- 内存：`session/inmemory`（默认示例使用）
- Redis：`session/redis`（生产更常用）

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

sess, _ := redis.NewService(redis.WithRedisClientURL("redis://localhost:6379"))
app := runner.NewRunner("app", ga, runner.WithSessionService(sess))
```

### GraphAgent 配置选项

```go
ga, err := graphagent.New(
    "workflow",
    compiledGraph,
    graphagent.WithDescription("工作流描述"),
    graphagent.WithInitialState(graph.State{"init": 1}),
    graphagent.WithChannelBufferSize(512),
    graphagent.WithCheckpointSaver(saver),
    graphagent.WithSubAgents([]agent.Agent{subAgent}), // 配置子 Agent
    graphagent.WithAgentCallbacks(agent.NewCallbacks()), // 注意：结构化回调 API 需要 trpc-agent-go >= 0.6.0
    // 设置传给模型的消息过滤模式，最终传给模型的消息需同时满足WithMessageTimelineFilterMode与WithMessageBranchFilterMode条件
    // 时间维度过滤条件
    // 默认值: graphagent.TimelineFilterAll
    // 可选值:
    //  - graphagent.TimelineFilterAll: 包含历史消息以及当前请求中所生成的消息
    //  - graphagent.TimelineFilterCurrentRequest: 仅包含当前请求中所生成的消息
    //  - graphagent.TimelineFilterCurrentInvocation: 仅包含当前invocation上下文中生成的消息
    graphagent.WithMessageTimelineFilterMode(graphagent.BranchFilterModeAll),
    // 分支维度过滤条件
    // 默认值: graphagent.BranchFilterModePrefix
    // 可选值:
    //  - graphagent.BranchFilterModeAll: 包含所有agent的消息, 当前agent与模型交互时,如需将所有agent生成的有效内容消息同步给模型时可设置该值
    //  - graphagent.BranchFilterModePrefix: 通过Event.FilterKey与Invocation.eventFilterKey做前缀匹配过滤消息, 期望将与当前agent以及相关上下游agent生成的消息传递给模型时，可设置该值
    //  - graphagent.BranchFilterModeExact: 通过Event.FilterKey==Invocation.eventFilterKey过滤消息，当前agent与模型交互时,仅需使用当前agent生成的消息时可设置该值
    graphagent.WithMessageBranchFilterMode(graphagent.TimelineFilterAll),
)
```

## 核心概念

### 状态管理

GraphAgent 采用 Schema + Reducer 模式管理状态。先明确状态结构与合并规则，后续节点输入/输出的 key 就有了清晰来源与生命周期约定。

#### 使用内置 Schema

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

schema := graph.MessagesStateSchema()

// 预定义字段（键名常量）与语义：
// - graph.StateKeyMessages       ("messages")        对话历史（[]model.Message；MessageReducer + MessageOp 原子合并）
// - graph.StateKeyUserInput      ("user_input")      用户输入（string；一次性，成功执行后清空）
// - graph.StateKeyLastResponse   ("last_response")   最后响应（string）
// - graph.StateKeyNodeResponses  ("node_responses")  各节点输出（map[string]any；并行汇总读取）
// - graph.StateKeyMetadata       ("metadata")        元数据（map[string]any；MergeReducer 合并）

// 其他一次性/系统键（按需使用）：
// - graph.StateKeyOneShotMessages ("one_shot_messages")  一次性覆盖本轮输入（[]model.Message）
// - graph.StateKeySession         ("session")            会话对象（系统使用）
// - graph.StateKeyExecContext     ("exec_context")       执行上下文（事件流等，系统使用）
```

#### 自定义 Schema

```go
import (
    "reflect"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

schema := graph.NewStateSchema()

// 添加自定义字段
schema.AddField("counter", graph.StateField{
    Type:    reflect.TypeOf(0),
    Default: func() any { return 0 },
    Reducer: func(old, new any) any {
        return old.(int) + new.(int)  // 累加
    },
})

// 字符串列表使用内置 Reducer
schema.AddField("items", graph.StateField{
    Type:    reflect.TypeOf([]string{}),
    Default: func() any { return []string{} },
    Reducer: graph.StringSliceReducer,
})
```

Reducer 机制确保状态字段按预定义规则安全合并，这在并发执行时尤其重要。

提示：建议为业务键定义常量，避免散落魔法字符串。

### 节点类型

GraphAgent 提供了四种内置节点类型：

#### Function 节点

最基础的节点，执行自定义逻辑：

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
    stateKeyInput  = "input"
    stateKeyOutput = "output"
    nodeProcess    = "process"
)

sg.AddNode(nodeProcess, func(ctx context.Context, state graph.State) (any, error) {
    data := state[stateKeyInput].(string)
    processed := transform(data)
    // Function 节点需显式指定输出 key
    return graph.State{stateKeyOutput: processed}, nil
})
```

#### LLM 节点

集成语言模型，自动管理对话历史：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

const (
    llmModelName     = "gpt-4o-mini"
    llmSystemPrompt  = "系统提示词"
    llmNodeAssistant = "assistant"
)

model := openai.New(llmModelName)
sg.AddLLMNode(llmNodeAssistant, model, llmSystemPrompt, tools)

// LLM 节点的输入输出规则：
// 输入优先级: graph.StateKeyOneShotMessages > graph.StateKeyUserInput > graph.StateKeyMessages
// 输出: graph.StateKeyLastResponse、graph.StateKeyMessages(原子更新)、graph.StateKeyNodeResponses（包含当前节点输出，便于并行汇总）
```

## 节点缓存（Cache）

为“纯函数型”节点开启缓存可以显著减少重复计算开销。Graph 支持图级与节点级缓存策略：

- 图级设置缓存实现与默认策略：
  - `WithCache(cache Cache)` 设置缓存后端（默认示例提供内存实现 InMemoryCache）
  - `WithCachePolicy(policy *CachePolicy)` 设置默认缓存策略（键函数 KeyFunc + 生存时间 Time To Live, TTL）
- 节点级覆盖策略：`WithNodeCachePolicy(policy *CachePolicy)`（优先于图级）
- 清理：`ClearCache(nodes ...string)` 按节点清理缓存命名空间

参考：

- Graph 接口（缓存与策略的访问/设置）：[graph/graph.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/graph.go)
- 默认策略与内存后端实现：
  - 接口/策略与默认键函数（规范化 JSON + SHA‑256）：[graph/cache.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/cache.go)
  - 内存缓存（InMemoryCache）并发安全实现（读写锁 + 深拷贝）：[graph/cache.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/cache.go)
- 执行器：
  - 节点执行前尝试 Get，命中则跳过节点函数执行，仅触发 after 回调与写出（Writes）：[graph/executor.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/executor.go)
  - 正常执行成功后写入缓存（Set）：[graph/executor.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/executor.go)
  - 节点完成事件中附带 `_cache_hit` 观察标记（命中时插入 `StateDelta["_cache_hit"]=true`）：[graph/executor.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/executor.go)

最小用法：

```go
schema := graph.NewStateSchema()
sg := graph.NewStateGraph(schema).
  WithCache(graph.NewInMemoryCache()).
  WithCachePolicy(graph.DefaultCachePolicy())

// 对某个节点单独设置 TTL 10 分钟
nodePolicy := &graph.CachePolicy{KeyFunc: graph.DefaultCachePolicy().KeyFunc, TTL: 10*time.Minute}
sg.AddNode("compute", computeFunc, graph.WithNodeCachePolicy(nodePolicy)).
  SetEntryPoint("compute").
  SetFinishPoint("compute")

compiled, _ := sg.Compile()
```

进阶用法：

- 仅使用部分字段作为键（推荐）

```go
package main

import (
    "context"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func build() (*graph.Graph, error) {
    schema := graph.NewStateSchema()
    sg := graph.NewStateGraph(schema).
        WithCache(graph.NewInMemoryCache()).
        WithCachePolicy(graph.DefaultCachePolicy())

    compute := func(ctx context.Context, s graph.State) (any, error) {
        // your logic here
        return graph.State{"out": s["n"].(int) * 2}, nil
    }

    // 仅使用 n 与 user_id 两个字段参与键计算（其余字段不会影响命中）
    sg.AddNode("compute", compute,
        graph.WithNodeCachePolicy(&graph.CachePolicy{KeyFunc: graph.DefaultCachePolicy().KeyFunc, TTL: 30*time.Minute}),
        graph.WithCacheKeyFields("n", "user_id"),
    )

    return sg.Compile()
}
```

- 自定义选择器（当字段映射更复杂时）

```go
package main

import (
    "context"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func build() (*graph.Graph, error) {
    schema := graph.NewStateSchema()
    sg := graph.NewStateGraph(schema).
        WithCache(graph.NewInMemoryCache()).
        WithCachePolicy(graph.DefaultCachePolicy())

    compute := func(ctx context.Context, s graph.State) (any, error) { return graph.State{"out": 42}, nil }

    sg.AddNode("compute", compute,
        graph.WithNodeCachePolicy(&graph.CachePolicy{KeyFunc: graph.DefaultCachePolicy().KeyFunc, TTL: 5*time.Minute}),
        graph.WithCacheKeySelector(func(m map[string]any) any {
            // 仅从净化后的输入中选择 n 与 uid 作为键来源
            return map[string]any{"n": m["n"], "uid": m["uid"]}
        }),
    )
    return sg.Compile()
}
```

- 版本化命名空间（跨版本防脏缓存）

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func build() (*graph.Graph, error) {
    schema := graph.NewStateSchema()
    sg := graph.NewStateGraph(schema).
        WithGraphVersion("v2025.03"). // 命名空间变为 __writes__:v2025.03:<node>
        WithCache(graph.NewInMemoryCache()).
        WithCachePolicy(graph.DefaultCachePolicy())

    // ... AddNode(...)
    return sg.Compile()
}
```

- 节点级 TTL（Time To Live）

```go
package main

import (
    "context"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func build() (*graph.Graph, error) {
    schema := graph.NewStateSchema()
    sg := graph.NewStateGraph(schema).
        WithCache(graph.NewInMemoryCache()).
        WithCachePolicy(graph.DefaultCachePolicy())

    compute := func(ctx context.Context, s graph.State) (any, error) { return graph.State{"out": 42}, nil }

    sg.AddNode("compute", compute,
        graph.WithNodeCachePolicy(&graph.CachePolicy{
            KeyFunc: graph.DefaultCachePolicy().KeyFunc,
            TTL:     10 * time.Minute,
        }),
    )
    return sg.Compile()
}
```

- 清理缓存（按节点）

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func clear(sg *graph.StateGraph) {
    // 清理指定节点
    sg.ClearCache("compute", "format")

    // 清理图内所有节点（不传参）
    sg.ClearCache()
}
```

- 读取缓存命中标记（\_cache_hit）

```go
package main

import (
    "context"
    "fmt"
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func runAndReadHits(executor *graph.Executor, initial graph.State) error {
    inv := &agent.Invocation{InvocationID: "demo"}
    ch, err := executor.Execute(context.Background(), initial, inv)
    if err != nil { return err }
    for e := range ch {
        if e.Response != nil && e.Response.Object == graph.ObjectTypeGraphNodeComplete {
            if e.StateDelta != nil {
                if _, ok := e.StateDelta["_cache_hit"]; ok {
                    fmt.Println("cache hit: skipped node function")
                }
            }
        }
        if e.Done { break }
    }
    return nil
}
```

注意事项：

- 仅对“纯函数（相同输入 → 相同输出，无外部副作用）”节点开启缓存，避免语义错误。
- TTL（Time To Live）为 0 表示不过期，需防止内存增长；生产建议使用持久化后端（如 Redis/SQLite）与定期清理。
- 键函数会“净化输入”后再规范化序列化，避免把会话、执行上下文等“易变/不可序列化”值纳入键，提升命中率、避免错误（见 [graph/cache_key.go](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/cache_key.go)）。
- 代码更新后可调用 `ClearCache("nodeID")` 清理旧缓存，或在键/命名空间中引入“函数标识符/版本”维度。

Runner + GraphAgent 环境使用示例：

```go
package main

import (
    "bufio"
    "context"
    "flag"
    "fmt"
    "os"
    "strings"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ttl := flag.Duration("ttl", 1*time.Minute, "cache ttl")
    flag.Parse()

    // Build graph with cache
    schema := graph.NewStateSchema()
    sg := graph.NewStateGraph(schema).
        WithCache(graph.NewInMemoryCache()).
        WithCachePolicy(graph.DefaultCachePolicy())

    compute := func(ctx context.Context, s graph.State) (any, error) {
        n, _ := s["n"].(int)
        time.Sleep(200 * time.Millisecond)
        return graph.State{"out": n * 2}, nil
    }
    sg.AddNode("compute", compute,
        graph.WithNodeCachePolicy(&graph.CachePolicy{KeyFunc: graph.DefaultCachePolicy().KeyFunc, TTL: *ttl}),
        graph.WithCacheKeyFields("n"),
    ).
        SetEntryPoint("compute").
        SetFinishPoint("compute")
    g, _ := sg.Compile()

    // GraphAgent + Runner
    ga, _ := graphagent.New("cache-demo", g, graphagent.WithInitialState(graph.State{}))
    app := runner.NewRunner("app", ga, runner.WithSessionService(inmemory.NewSessionService()))

    // Interactive
    sc := bufio.NewScanner(os.Stdin)
    fmt.Println("Enter integers; repeated inputs should hit cache. type 'exit' to quit")
    for {
        fmt.Print("> ")
        if !sc.Scan() { break }
        txt := strings.TrimSpace(sc.Text())
        if txt == "exit" { break }
        if txt == "" { continue }
        msg := model.NewUserMessage(fmt.Sprintf("compute %s", txt))
        evts, err := app.Run(context.Background(), "user", fmt.Sprintf("s-%d", time.Now().UnixNano()), msg, agent.WithRuntimeState(graph.State{"n": atoi(txt)}))
        if err != nil { fmt.Println("run error:", err); continue }
        for e := range evts {
            if e.Response != nil && e.Response.Object == graph.ObjectTypeGraphNodeComplete {
                if e.StateDelta != nil { if _, ok := e.StateDelta["_cache_hit"]; ok { fmt.Println("cache hit") } }
            }
            if e.Done { break }
        }
    }
}

func atoi(s string) int { var n int; fmt.Sscanf(s, "%d", &n); return n }
```

示例：

- 交互式（Interactive）+ Runner + GraphAgent 演示：`examples/graph/nodecache`，入口 [examples/graph/nodecache/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/graph/nodecache/main.go)

#### Tools 节点

执行工具调用，注意是**顺序执行**：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const nodeTools = "tools"

sg.AddToolsNode(nodeTools, tools)
// 多个工具会按 LLM 返回的顺序依次执行
// 如需并行，应该使用多个节点 + 并行边
// 配对规则：从 messages 尾部回溯定位最近的 assistant(tool_calls)
// 消息，遇到新的 user 即停止，确保与本轮工具调用配对。
```

#### 将工具结果写入 State

在 Tools 节点之后，添加一个函数节点，从 `graph.StateKeyMessages` 汇总工具结果并写入结构化 State：

```go
const stateKeyToolResults = "tool_results"

sg.AddNode("collect_tool_results", func(ctx context.Context, s graph.State) (any, error) {
    msgs, _ := s[graph.StateKeyMessages].([]model.Message)
    if len(msgs) == 0 { return nil, nil }

    // 定位本轮 assistant(tool_calls)
    i := len(msgs) - 1
    for i >= 0 && !(msgs[i].Role == model.RoleAssistant && len(msgs[i].ToolCalls) > 0) {
        if msgs[i].Role == model.RoleUser { // 新一轮，停止
            return nil, nil
        }
        i--
    }
    if i < 0 { return nil, nil }

    // 收集匹配的工具回复（按 ToolID 配对）
    idset := map[string]bool{}
    for _, tc := range msgs[i].ToolCalls { idset[tc.ID] = true }
    results := map[string]string{}
    for j := i + 1; j < len(msgs); j++ {
        m := msgs[j]
        if m.Role == model.RoleTool && idset[m.ToolID] {
            results[m.ToolName] = m.Content // 内容可能为 JSON/文本，依工具定义决定
        }
        if m.Role == model.RoleUser { break }
    }
    if len(results) == 0 { return nil, nil }
    return graph.State{stateKeyToolResults: results}, nil
})
```

参考示例：`examples/graph/io_conventions_tools`。

````

#### Agent 节点
嵌入子 Agent，实现多 Agent 协作：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
)

const (
    subAgentNameAnalyzer = "analyzer"
    graphAgentNameMain   = "main"
)

// 重要：节点 ID 必须与子 Agent 名称一致
sg.AddAgentNode(subAgentNameAnalyzer)

// Agent 实例在 GraphAgent 创建时注入
analyzer := createAnalyzer()  // 内部 Agent 名称必须是 "analyzer"
graphAgent, _ := graphagent.New(graphAgentNameMain, g,
    graphagent.WithSubAgents([]agent.Agent{analyzer}))
````

### 边与路由

边定义了节点间的执行流转：

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
    nodeA         = "nodeA"
    nodeB         = "nodeB"
    nodeDecision  = "decision"
    nodePathA     = "pathA"
    nodePathB     = "pathB"

    routeToPathA  = "route_to_pathA"
    routeToPathB  = "route_to_pathB"
    stateKeyFlag  = "flag"
)

// 普通边：顺序执行
sg.AddEdge(nodeA, nodeB)

// 条件边：动态路由（第三个参数为路径映射，建议显式提供以做静态校验）
// 先定义目标节点
sg.AddNode(nodePathA, handlerA)
sg.AddNode(nodePathB, handlerB)
// 再添加条件路由
sg.AddConditionalEdges(nodeDecision,
    func(ctx context.Context, s graph.State) (string, error) {
        if s[stateKeyFlag].(bool) {
            return routeToPathA, nil
        }
        return routeToPathB, nil
    }, map[string]string{
        routeToPathA: nodePathA,
        routeToPathB: nodePathB,
    })

// 工具条件边：处理 LLM 工具调用
const (
    nodeLLM      = "llm"
    nodeToolsUse = "tools"
    nodeFallback = "fallback"
)
sg.AddToolsConditionalEdges(nodeLLM, nodeToolsUse, nodeFallback)

// 并行边：自动并行执行
const (
    nodeSplit   = "split"
    nodeBranch1 = "branch1"
    nodeBranch2 = "branch2"
)
sg.AddEdge(nodeSplit, nodeBranch1)
sg.AddEdge(nodeSplit, nodeBranch2)  // branch1 和 branch2 会并行执行
```

提示：设置入口与结束点时，会隐式连接到虚拟的 Start/End 节点：

- `SetEntryPoint("first")` 等效于创建 `Start -> first` 的连边；
- `SetFinishPoint("last")` 等效于创建 `last -> End` 的连边。
  无需显式添加这两条边。

常量名：`graph.Start == "__start__"`，`graph.End == "__end__"`。

### 消息可见性选项
当前Agent可在需要时根据不同场景控制其对其他Agent生成的消息以及历史会话消息的可见性进行管理，可通过相关选项配置进行管理。
在与model交互时仅将可见的内容输入给模型。 

TIPS:
 - 不同sessionID的消息在任何场景下都是互不可见的，以下管控策略均针对同一个sessionID的消息
 - Invocation.Message在任何场景下均可见
 - 相关配置仅控制State[graph.StateKeyMessages]的初始值
 - Agent node生成的消息filterKey为subAgent name, 因此使用`IsolatedRequest`或`IsolatedInvocation`过滤时对当前graphAgent不可见
 - 未配置选项时，默认值为FullContext

配置:
- `graphagent.WithMessageFilterMode(MessageFilterMode)`:
  - `FullContext`: 所有能通过filterKey做前缀匹配的消息
  - `RequestContext`: 仅包含当前请求周期内通过filterKey前缀匹配的消息
  - `IsolatedRequest`: 仅包含当前请求周期内通过filterKey完全匹配的消息
  - `IsolatedInvocation`: 仅包含当前invocation周期内通过filterKey完全匹配的消息

推荐用法示例（该用法仅基于高阶用法基础之上做了配置简化）: 

案例1: 对graphAgent消息可见性控制
```go
subgraphAgentA := graphagent.New(
    "subgraphA", subgraph,
    // 对parentAgent、subgraphAgentA、subgraphAgentB(包含task1、task2分别生成的消息) 生成的所有消息可见（包含同一sessionID的历史会话消息）
    graphagent.WithMessageFilterMode(graphagent.FullContext),
    // 对parentAgent、subgraphAgentA、subgraphAgentB(包含task1、task2分别生成的消息) 当前runner.Run期间生成的所有消息可见（不包含历史会话消息）
    graphagent.WithMessageFilterMode(graphagent.RequestContext),
    // 仅对subgraphAgentA 当前runner.Run期间生成的消息可见（不包含自己的历史会话消息）
    graphagent.WithMessageFilterMode(graphagent.IsolatedRequest),
    // 仅对subgraphAgentA当前invocation期间生成的消息可见(此示例中subgraphAgentA仅执行一次， 效果等价于graphagent.IsolatedRequest)
    graphagent.WithMessageFilterMode(graphagent.IsolatedInvocation),
)

subgraphAgentB := graphagent.New(
    "subgraphB", subgraph,
    // 对parentAgent、subgraphAgentA、subgraphAgentB(包含task1、task2分别生成的消息) 生成的所有消息可见（包含同一sessionID的历史会话消息）
    graphagent.WithMessageFilterMode(graphagent.FullContext),
    // 对parentAgent、subgraphAgentA、subgraphAgentB(包含task1、task2分别生成的消息) 当前runner.Run期间生成的所有消息可见（不包含历史会话消息）
    graphagent.WithMessageFilterMode(graphagent.RequestContext),
    // 仅对subgraphAgentB（包含task1、task2分别生成的消息）当前runner.Run期间生成的消息可见（不包含自己的历史会话消息）
    graphagent.WithMessageFilterMode(graphagent.IsolatedRequest),
    // 仅对subgraphAgentB 当前Invocation中生成的消息可见，不包含历史消息（task1与task2执行期间生成的消息互不可见）。
    graphagent.WithMessageFilterMode(graphagent.IsolatedInvocation),
)

sg.AddAgentNode("subgraphA")
sg.AddNode("fanout", func(ctx context.Context, state graph.State) (any, error) {
    return []*graph.Command{
        {
            GoTo: "subgraph",
            Update: graph.State{
                "task": "task 1"
            },
        },
        {
            GoTo: "subgraph",
            Update: graph.State{
                "task": "task 2"
            },
        },
    }, nil
})
sg.AddAgentNode("subgraphB")
sg.AddEdge("subgraphA", "fanout")
sg.SetEntryPoint(subgraphA)
graph, err := sg.Compile()
if err != nil {
    log.Fatalf("Failed to Compile state graph, err: %w", err)
}
parentAgent := graphagent.New(
    "subgraph", graph,
    // subagent
    graphagent.WithSubAgents(subgraphAgent)
)
```

案例2: 对节点Agent消息可见性控制
```go
taskagentA := llmagent.New(
  "taskagentA",
  llmagent.WithModel(modelInstance),
  // 对taskagentA、taskagentB生成的所有消息可见（包含同一sessionID的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.FullContext),
  // 对taskagentA、taskagentB当前runner.Run期间生成的所有消息可见（不包含历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.RequestContext),
  // 仅对taskagentA当前runner.Run期间生成的消息可见（不包含自己的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
  // agent执性顺序：taskagentA-invocation1 -> taskagentB-invocation2 -> taskagentA-invocation3(当前执行阶段)
  // 仅对taskagentA当前taskagentA-invocation3期间生成的消息可见（不包含自己的历史会话消息以及taskagentA-invocation1期间生成的消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedInvocation),
)

taskagentB := llmagent.New(
  "taskagentB",
  llmagent.WithModel(modelInstance),
  // 对taskagentA、taskagentB生成的所有消息可见（包含同一sessionID的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.FullContext),
  // 对taskagentA、taskagentB当前runner.Run期间生成的所有消息可见（不包含历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.RequestContext),
  // 仅对taskagentB当前runner.Run期间生成的消息可见（不包含自己的历史会话消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
  // agent执性顺序：taskagentA-invocation1 -> taskagentB-invocation2 -> taskagentA-invocation3 -> taskagentB-invocation4(当前执行阶段)
  // 仅对taskagentB当前taskagentB-invocation4期间生成的消息可见（不包含自己的历史会话消息以及taskagentB-invocation2期间生成的消息）
  llmagent.WithMessageFilterMode(llmagent.IsolatedInvocation),
)

sg.AddAgentNode("taskagentA")
// 同一个并行执行多个任务
sg.AddNode("fanout", func(ctx context.Context, state graph.State) (any, error) {
    return []*graph.Command{
        {
            GoTo: "taskB",
            Update: graph.State{
                "task": "task 1"
            },
        },
        {
            GoTo: "taskB",
            Update: graph.State{
                "task": "task 2"
            },
        },
    }, nil
})
// 可将taskagentB的消息过滤模式设置为llmagent.IsolatedInvocation 以此来隔离多任务的上下文会话信息
sg.AddNode("taskB", func(ctx context.Context, state graph.State) (any, error){
    task := state["task-id"]
    inv, _ := agent.InvocationFromContext(ctx)
    inv.Message = model.NewUserMessage(task)
    chan, err := taskagentB.Run(ctx, inv)
    // do any thing
})
```

高阶用法示例：
可以单独通过 `WithMessageTimelineFilterMode`、`WithMessageBranchFilterMode`控制当前agent对历史消息与其他agent生成的消息可见性。
当前agent在与模型交互时，最终将同时满足两个条件的消息输入给模型。

`配置:`
- `WithMessageTimelineFilterMode`: 时间维度可见性控制
  - `TimelineFilterAll`: 包含历史消息以及当前请求中所生成的消息
  - `TimelineFilterCurrentRequest`: 仅包含当前请求(一次runner.Run为一次请求)中所生成的消息
  - `TimelineFilterCurrentInvocation`: 仅包含当前invocation上下文中生成的消息
- `WithMessageBranchFilterMode`: 分支维度可见性控制（用于控制对其他agent生成消息的可见性）
  - `BranchFilterModePrefix`: 通过Event.FilterKey与Invocation.eventFilterKey做前缀匹配
  - `BranchFilterModeAll`: 所有agent的均消息
  - `BranchFilterModeExact`: 仅自己生成的消息可见
  
```go
llmAgent := llmagent.New(
    "demo-agent",                      // Agent 名称
    llmagent.WithModel(modelInstance), // 设置模型
    llmagent.WithDescription("A helpful AI assistant for demonstrations"),              // 设置描述
    llmagent.WithInstruction("Be helpful, concise, and informative in your responses"), // 设置指令
    llmagent.WithGenerationConfig(genConfig),                                           // 设置生成参数

    // 设置传给模型的消息过滤模式，最终传给模型的消息需同时满足WithMessageTimelineFilterMode与WithMessageBranchFilterMode条件
    // 时间维度过滤条件
    // 默认值: llmagent.TimelineFilterAll
    // 可选值:
    //  - llmagent.TimelineFilterAll: 包含历史消息以及当前请求中所生成的消息
    //  - llmagent.TimelineFilterCurrentRequest: 仅包含当前请求中所生成的消息
    //  - llmagent.TimelineFilterCurrentInvocation: 仅包含当前invocation上下文中生成的消息
    llmagent.WithMessageTimelineFilterMode(llmagent.BranchFilterModeAll),
    // 分支维度过滤条件
    // 默认值: llmagent.BranchFilterModePrefix
    // 可选值:
    //  - llmagent.BranchFilterModeAll: 包含所有agent的消息, 当前agent与模型交互时,如需将所有agent生成的有效内容消息同步给模型时可设置该值
    //  - llmagent.BranchFilterModePrefix: 通过Event.FilterKey与Invocation.eventFilterKey做前缀匹配过滤消息, 期望将与当前agent以及相关上下游agent生成的消息传递给模型时，可设置该值
    //  - llmagent.BranchFilterModeExact: 通过Event.FilterKey==Invocation.eventFilterKey过滤消息，当前agent与模型交互时,仅需使用当前agent生成的消息时可设置该值
    llmagent.WithMessageBranchFilterMode(llmagent.TimelineFilterAll),
)
```

### 命令模式（动态路由 / Fan-out）

节点除返回 `graph.State` 外，也可以返回 `*graph.Command` 或 `[]*graph.Command`，以同时更新状态并指定下一跳：

```go
// 动态路由到 A 或 B，并写入状态
const (
    nodeDecide   = "decide"
    nodeA        = "A"
    nodeB        = "B"
    stateKeyFlag = "flag"
)

sg.AddNode(nodeDecide, func(ctx context.Context, s graph.State) (any, error) {
    if s[stateKeyFlag].(bool) {
        return &graph.Command{Update: graph.State{"routed": nodeA}, GoTo: nodeA}, nil
    }
    return &graph.Command{Update: graph.State{"routed": nodeB}, GoTo: nodeB}, nil
})

// Fan-out：并行派发多个任务到同一 worker
const (
    nodeFanout = "fanout"
    nodeWorker = "worker"
)
sg.AddNode(nodeFanout, func(ctx context.Context, s graph.State) (any, error) {
    cmds := []*graph.Command{
        {Update: graph.State{"param": "A"}, GoTo: nodeWorker},
        {Update: graph.State{"param": "B"}, GoTo: nodeWorker},
        {Update: graph.State{"param": "C"}, GoTo: nodeWorker},
    }
    return cmds, nil
})
```

使用命令模式进行路由时，无需为 `GoTo` 目标添加显式静态边；仅需保证目标节点存在，并在需要作为终点时设置 `SetFinishPoint`。

## 架构设计

### 整体架构

GraphAgent 的架构设计体现了我们对复杂系统的理解：通过清晰的分层来管理复杂性。每一层都有明确的职责，层与层之间通过标准接口通信。

```mermaid
flowchart TB
    subgraph "Runner Layer"
        R[Runner]:::runnerClass
        S[Session Service]:::sessionClass
    end

    subgraph "GraphAgent"
        GA[GraphAgent Wrapper]:::agentClass
        CB[Callbacks]:::callbackClass
    end

    subgraph "Graph Engine"
        SG[StateGraph Builder]:::builderClass
        G[Graph]:::graphClass
        E[Executor]:::executorClass
    end

    subgraph "Execution Components"
        P[Planning]:::phaseClass
        EX[Execution]:::phaseClass
        U[Update]:::phaseClass
    end

    subgraph "Storage"
        CP[Checkpoint]:::storageClass
        ST[State Store]:::storageClass
    end

    R --> GA
    GA --> G
    G --> E
    E --> P
    E --> EX
    E --> U
    E --> CP

    classDef runnerClass fill:#e8f5e9,stroke:#43a047,stroke-width:2px
    classDef sessionClass fill:#f3e5f5,stroke:#8e24aa,stroke-width:2px
    classDef agentClass fill:#e3f2fd,stroke:#1976d2,stroke-width:2px
    classDef callbackClass fill:#fce4ec,stroke:#c2185b,stroke-width:2px
    classDef builderClass fill:#fff8e1,stroke:#f57c00,stroke-width:2px
    classDef graphClass fill:#f1f8e9,stroke:#689f38,stroke-width:2px
    classDef executorClass fill:#e0f2f1,stroke:#00796b,stroke-width:2px
    classDef phaseClass fill:#ede7f6,stroke:#512da8,stroke-width:2px
    classDef storageClass fill:#efebe9,stroke:#5d4037,stroke-width:2px
```

### 核心模块解析

核心组件一览：

**`graph/state_graph.go`** - StateGraph 构建器  
提供链式声明式 Go API 来构建图结构，通过 fluent 方法链（AddNode → AddEdge → Compile）定义节点、边和条件路由。

**`graph/graph.go`** - 编译后的运行时  
实现基于通道（Channel）的事件触发式执行机制。节点执行结果合并入 State；通道仅用于触发路由，写入哨兵值（sentinel value）而非业务数据。

**`graph/executor.go`** - BSP 执行器  
这是系统心脏，借鉴了 [Google Pregel](https://research.google/pubs/pub37252/) 论文。实现 BSP（Bulk Synchronous Parallel）风格的三阶段循环：Planning → Execution → Update。

**`graph/checkpoint/*`** - 检查点和恢复机制  
提供可选的检查点持久化（如 sqlite），原子保存状态与待写入动作，支持按谱系/检查点恢复。

**`agent/graphagent/graph_agent.go`** - Graph 与 Agent 的桥梁  
将编译后的 Graph 适配为通用 Agent，可复用会话、回调与事件流。

### 执行模型

GraphAgent 借鉴了 Google Pregel 的 BSP（Bulk Synchronous Parallel）模型，但适配到了单进程环境；在此基础上还支持检查点、HITL 中断/恢复与时间旅行：

```mermaid
sequenceDiagram
    autonumber
    participant R as Runner
    participant GA as GraphAgent
    participant EX as Executor
    participant CK as Checkpoint Saver
    participant DB as Storage
    participant H as Human

    R->>GA: Run(invocation)
    GA->>EX: Execute(graph, state, options)
    GA-->>R: Stream node/tool/model events

    loop 每个超级步 (BSP)
        EX->>EX: Planning — 计算前沿(Frontier)
        par 并行执行节点
            EX->>EX: 执行节点 i（状态浅拷贝）
            EX-->>GA: 节点开始事件(author=nodeID)
        and
            EX->>EX: 执行节点 j（状态浅拷贝）
            EX-->>GA: 节点开始事件
        end

        alt 节点触发 Interrupt(key,prompt)
            EX->>CK: Save checkpoint(state,frontier,
            EX->>CK: pending_writes,versions_seen,reason=interrupt)
            CK->>DB: 原子提交
            EX-->>GA: interrupt 事件(checkpoint_id,prompt)
            GA-->>R: 转发中断事件并暂停
            R->>H: 请求人工输入/审批
            H-->>R: 提交决策/值
            R->>GA: Run(resume) runtime_state{
            R->>GA: checkpoint_id,resume_map}
            GA->>EX: ResumeFromCheckpoint(checkpoint_id,resume_map)
            EX->>CK: Load checkpoint
            CK->>EX: state/frontier/pending_writes/versions_seen
            EX->>EX: 重建前沿并应用恢复值
        else 正常执行
            EX-->>GA: 节点完成事件（含 tool/model 事件）
            EX->>EX: Update — Reducer 合并状态
            EX->>CK: Save checkpoint(state,frontier,
            EX->>CK: pending_writes,versions_seen)
            CK->>DB: 原子提交
        end
    end

    Note over EX,CK: versions_seen 避免重复执行；
    Note over EX,CK: pending_writes 重建通道；
    Note over EX,CK: parent_id 形成谱系以支持时间旅行

    opt 时间旅行（回溯/分支）
        R->>GA: Run(runtime_state{checkpoint_id})
        GA->>EX: ResumeFromCheckpoint(checkpoint_id)
        EX->>CK: Load checkpoint + lineage
        CK->>EX: 恢复状态并可创建新 lineage_id
    end

    EX-->>GA: done 事件（last_response）
    GA-->>R: 输出最终消息
```

```mermaid
flowchart TB
    %% 执行全景图（精简连线）
    subgraph Client
        R[Runner]:::runner --> GA[GraphAgent]:::agent
    end

    subgraph Engine[Graph Engine]
        GA --> EX[Executor]:::executor
        subgraph BSP["BSP Superstep"]
            P[Planning]:::phase --> X[Execution]:::phase --> U[Update]:::phase
        end
    end

    N[Nodes: LLM / Tools / Function / Agent]:::process
    CK[(Checkpoint)]:::storage
    H[Human]:::human

    EX --> BSP
    EX --> N
    EX -.-> CK
    GA <--> H
    GA --> R

    classDef runner fill:#e8f5e9,stroke:#43a047,stroke-width:2px
    classDef agent fill:#e3f2fd,stroke:#1976d2,stroke-width:2px
    classDef executor fill:#e0f2f1,stroke:#00796b,stroke-width:2px
    classDef phase fill:#ede7f6,stroke:#512da8,stroke-width:2px
    classDef process fill:#f3e5f5,stroke:#9c27b0,stroke-width:2px
    classDef storage fill:#efebe9,stroke:#6d4c41,stroke-width:2px
    classDef human fill:#e8f5e9,stroke:#43a047,stroke-width:2px
```

执行过程的关键点：

1. **Planning Phase**: 基于通道状态确定本步要执行的节点
2. **Execution Phase**: 每个节点获得状态的浅拷贝（maps.Copy），并行执行
3. **Update Phase**: 通过 Reducer 合并各节点的状态更新，保证并发安全

这种设计让每一步都能被清晰观测、安全中断和恢复。

#### 运行态隔离与事件快照

- 执行器（Executor）可复用且并发安全，单次运行态存放于 `ExecutionContext`，包括通道版本、待写（pending writes）、最近检查点等。
- 事件的 `StateDelta` 使用深拷贝快照，只包含可序列化且允许的键；内部键（如执行上下文、回调等）会被过滤，便于带外观测与持久化。

### 执行器配置

```go
exec, err := graph.NewExecutor(g,
    graph.WithChannelBufferSize(1024),              // 事件通道缓冲
    graph.WithMaxSteps(50),                          // 最大步数
    graph.WithStepTimeout(5*time.Minute),            // 步骤超时
    graph.WithNodeTimeout(2*time.Minute),            // 节点超时
    graph.WithCheckpointSaver(saver),                // 开启检查点（如 sqlite/inmemory）
    graph.WithCheckpointSaveTimeout(30*time.Second), // 检查点保存超时
)
```

## 与多 Agent 系统集成

GraphAgent 的设计初衷就是成为 tRPC-Agent-Go 多 Agent 生态的一部分，而不是独立存在。它实现了标准的 Agent 接口，可以和其他 Agent 类型无缝协作。

### GraphAgent 作为 Agent

GraphAgent 实现了标准 Agent 接口：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
)

// 可以直接在 ChainAgent, ParallelAgent, CycleAgent 中使用
chain := chainagent.New("chain",
    chainagent.WithSubAgents([]agent.Agent{
        graphAgent1,  // 结构化流程1
        graphAgent2,  // 结构化流程2
    }))
```

### 高级编排

下图展示复杂业务编排：入口清洗 → 智能路由 → 多子编队（Email、Weather、Research）→ 并行 fanout/聚合 → 最终合成与发布。

```mermaid
flowchart LR
    %% Layout
    subgraph UE["User & Entry"]
        U((User)):::human --> IN["entry<br/>normalize"]:::process
    end

    subgraph FAB["Graph Orchestration"]
        Rtr["where_to_go<br/>router"]:::router
        Compose["compose<br/>LLM"]:::llm
    end

    IN --> Rtr

    %% Email Agent (expanded)
    subgraph EC["Email Agent"]
        direction LR
        CE["classifier<br/>LLM"]:::llm --> WE["writer<br/>LLM"]:::llm
    end

    %% Weather Agent (expanded)
    subgraph WA["Weather Agent"]
        direction LR
        LE["locate<br/>LLM"]:::llm --> WT["weather tool"]:::tool
    end

    %% Routing from router to pods
    Rtr -- email --> CE
    Rtr -- weather --> LE
    Rtr -- other --> REPLY["reply<br/>LLM"]:::llm

    %% Fanout Pipeline (fanout → workers → aggregate)
    subgraph FP["Fanout Pipeline"]
        direction LR
        Fan["plan_fanout"]:::process --> W1["worker A"]:::process
        Fan --> W2["worker B"]:::process
        Fan --> W3["worker C"]:::process
        W1 --> Agg["aggregate"]:::process
        W2 --> Agg
        W3 --> Agg
    end
    Rtr -- research --> Fan

    %% Human-in-the-loop (optional)
    Compose -. review .- HG["human<br/>review"]:::human

    %% Compose final (minimal wiring)
    Agg --> Compose
    WE --> Compose
    WT --> Compose
    REPLY --> Compose
    Compose --> END([END]):::terminal

    %% Styles
    classDef router fill:#fff7e0,stroke:#f5a623,stroke-width:2px
    classDef llm fill:#e3f2fd,stroke:#1e88e5,stroke-width:2px
    classDef tool fill:#fff3e0,stroke:#fb8c00,stroke-width:2px
    classDef process fill:#f3e5f5,stroke:#8e24aa,stroke-width:2px
    classDef human fill:#e8f5e9,stroke:#43a047,stroke-width:2px
    classDef terminal fill:#ffebee,stroke:#e53935,stroke-width:2px
```

要点：

- 智能路由 where_to_go 可由 LLM 决策或函数节点实现（条件边）。
- Fanout Pipeline 使用 Command GoTo 进行运行时 fanout，三路并行后在 aggregate 节点聚合。
- 可选的人机把关位于聚合之后，确保关键输出经人工确认。
- 仅在 Compose 处展示一次保存检查点，既不喧宾夺主，又能体现可恢复能力。

### 在图中嵌入 Agent

在图内部，我们也可以把已有的子 Agent 作为一个节点来调用。下面的示例展示了如何创建子 Agent、声明对应节点，并在 GraphAgent 构造时注入。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
)

// 创建子 Agent
const (
    subAgentAnalyzer = "analyzer"
    subAgentReviewer = "reviewer"
)
analyzer := createAnalyzer()  // 名称必须是 "analyzer"
reviewer := createReviewer()  // 名称必须是 "reviewer"

// 在图中声明 Agent 节点
sg.AddAgentNode(subAgentAnalyzer)
sg.AddAgentNode(subAgentReviewer)

// 创建 GraphAgent 时注入子 Agent
graphAgent, _ := graphagent.New("workflow", g,
    graphagent.WithSubAgents([]agent.Agent{
        analyzer,
        reviewer,
    }))

// I/O：子 Agent 既会把 graph.StateKeyUserInput 作为消息传入，也能通过
// inv.RunOptions.RuntimeState 读取完整图状态；完成后会更新
// graph.StateKeyLastResponse 以及 graph.StateKeyNodeResponses[nodeID]
```

#### 只传结果：将 last_response 映射为下游 user_input

> 场景：A → B → C 互为黑盒，不希望下游读取完整会话，只需要上游的“结果文本”作为本轮输入。

- 方式一（无需依赖，推荐通用）：在目标 Agent 节点添加前置回调，把父态 `last_response` 写入 `user_input`，必要时开启“消息隔离”。

```go
sg.AddAgentNode("orchestrator",
    // 将上游 last_response 设置为本轮 user_input
    graph.WithPreNodeCallback(func(ctx context.Context, cb *graph.NodeCallbackContext, s graph.State) (any, error) {
        if v, ok := s[graph.StateKeyLastResponse].(string); ok && v != "" {
            s[graph.StateKeyUserInput] = v
        }
        return nil, nil
    }),
    // 可选：隔离子 Agent 的会话注入，仅消费本轮 user_input
    graph.WithSubgraphIsolatedMessages(true),
)
```

- 方式二（使用增强选项，更简洁）：

```go
// 框架提供声明式 Option，自动执行 last_response → user_input 的映射
sg.AddAgentNode("orchestrator",
    graph.WithSubgraphInputFromLastResponse(),
    graph.WithSubgraphIsolatedMessages(true), // 可选，配合隔离达到“只传结果”
)
```

说明：以上两种方式都能让 B 仅看到 A 的结果、C 仅看到 B 的结果；方式二更简洁，方式一零依赖、到处可用。

### 混合模式示例

结构化流程中嵌入动态决策：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

sg := graph.NewStateGraph(schema)

const (
    nodePrepare  = "prepare"
    nodeAnalyzer = "analyzer"
    nodeFinalize = "finalize"
)

// 结构化的数据准备
sg.AddNode(nodePrepare, prepareData)

// 动态决策点 - 使用 ChainAgent
dynamicAgent := chainagent.New(nodeAnalyzer,
    chainagent.WithSubAgents([]agent.Agent{...}))
sg.AddAgentNode(nodeAnalyzer)

// 继续结构化流程
sg.AddNode(nodeFinalize, finalizeResults)

// 连接流程
sg.SetEntryPoint(nodePrepare)
sg.AddEdge(nodePrepare, nodeAnalyzer)     // 交给动态 Agent
sg.AddEdge(nodeAnalyzer, nodeFinalize)    // 回到结构化流程
sg.SetFinishPoint(nodeFinalize)

// 创建时注入
graphAgent, _ := graphagent.New("hybrid", g,
    graphagent.WithSubAgents([]agent.Agent{dynamicAgent}))
```

## 核心机制详解

### 状态管理：Schema + Reducer 模式

状态管理是图工作流的核心挑战之一。我们设计了一套基于 Schema + Reducer 的状态管理机制，既保证了类型安全，又支持高并发的原子更新。

```mermaid
flowchart LR
    subgraph "State Schema"
        MS[messages: MessageList]:::schemaClass
        UI[user_input: string]:::schemaClass
        LR[last_response: string]:::schemaClass
        NR[node_responses: Map]:::schemaClass
    end

    subgraph "State Operations"
        R1[MessageReducer]:::reducerClass
        R2[AppendReducer]:::reducerClass
        R3[DefaultReducer]:::reducerClass
    end

    subgraph "Concurrent Updates"
        N1[Node 1 Output]:::nodeOutputClass
        N2[Node 2 Output]:::nodeOutputClass
        N3[Node 3 Output]:::nodeOutputClass
    end

    N1 --> R1
    N2 --> R2
    N3 --> R3
    R1 --> MS
    R2 --> NR
    R3 --> LR

    classDef schemaClass fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
    classDef reducerClass fill:#f3e5f5,stroke:#7b1fa2,stroke-width:2px
    classDef nodeOutputClass fill:#fff8e1,stroke:#f57f17,stroke-width:2px
```

Graph 的状态底层是 `map[string]any`，通过 `StateSchema` 提供运行时类型校验和字段验证。Reducer 机制确保状态字段按预定义规则安全合并，避免并发更新冲突。

#### 常用键常量参考

- 用户可见：`graph.StateKeyUserInput`、`graph.StateKeyOneShotMessages`、`graph.StateKeyMessages`、`graph.StateKeyLastResponse`、`graph.StateKeyNodeResponses`、`graph.StateKeyMetadata`
- 系统内部：`session`、`exec_context`、`tool_callbacks`、`model_callbacks`、`agent_callbacks`、`current_node_id`、`parent_agent`
- 命令/恢复：`__command__`、`__resume_map__`

常量均定义在 `graph/state.go` 与 `graph/keys.go`，建议通过常量引用，避免硬编码。

#### 节点级回调、工具与生成参数

节点可通过可选项注册回调或参数（见 `graph/state_graph.go`）：

- `graph.WithPreNodeCallback` / `graph.WithPostNodeCallback` / `graph.WithNodeErrorCallback`
- LLM 节点可用 `graph.WithGenerationConfig`、`graph.WithModelCallbacks`
- 工具相关：`graph.WithToolCallbacks`、`graph.WithToolSets`（除 `tools []tool.Tool` 外，再额外提供 ToolSet`）、
  `graph.WithRefreshToolSetsOnRun`（在每次运行时从 ToolSet 重新构造工具列表，适合 MCP 等动态工具源）
- Agent 节点可用 `graph.WithAgentNodeEventCallback`

#### Graph 中的 ToolSet 与 Agent 的区别

`graph.WithToolSets` 是**节点级、构图期**配置：在构建图时，把一个或多个 `tool.ToolSet` 绑定到特定的 LLM 节点，例如：

```go
sg.AddLLMNode("llm",
    model,
    "inst",
    tools,
    graph.WithToolSets([]tool.ToolSet{mcpToolSet, fileToolSet}),
)
```

需要注意：

- Graph 一旦 `Compile()`，结构（包括节点绑定的 ToolSet）就是**不可变的**。如果要调整某个节点的 ToolSet，需要重新构图或创建新的 `GraphAgent`。
- 运行时希望动态增删 ToolSet，应该在 Agent 层处理（例如使用 `llmagent.AddToolSet`、`llmagent.RemoveToolSet`、`llmagent.SetToolSets`），或者替换 Graph 中 Agent 节点所使用的下游 Agent。

此外，`graph.WithName`/`graph.WithDescription` 可为节点添加友好的名称与描述；`graph.WithDestinations` 可声明潜在动态路由目标（仅用于静态校验/可视化）。

### LLM 输入规则：三段式设计

LLM 节点的输入处理是我们花了很多时间打磨的功能。看起来简单的三段式规则，实际上解决了 AI 应用中最常见的上下文管理问题。

LLM 节点内置了一套固定的输入选择逻辑（无需额外配置）：

1. **优先用 `graph.StateKeyOneShotMessages`**：完全覆盖本轮输入（含 system/user），执行后清空
2. **其次用 `graph.StateKeyUserInput`**：在 `graph.StateKeyMessages` 基础上追加本轮 user，再把 assistant 回答一起原子写回，随后清空 `graph.StateKeyUserInput`
3. **否则仅用 `graph.StateKeyMessages`**：常见于工具回路二次进 LLM（`graph.StateKeyUserInput` 已被清空）

这套规则的精妙之处在于，它既保证了"预处理节点可以改写 `graph.StateKeyUserInput` 并在同一轮生效"，又与工具循环（tool_calls → tools → LLM）自然衔接。

示例（技术解析级别的小片段，演示三种输入路径）：

```go
// OneShot（graph.StateKeyOneShotMessages）：完全覆盖本轮输入（包含 system/user），适合“前置节点构造完整 prompt”
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

const (
    systemPrompt = "你是审慎可靠的助手"
    userPrompt   = "请用要点总结这段文本"
)

sg.AddNode("prepare_prompt", func(ctx context.Context, s graph.State) (any, error) {
    oneShot := []model.Message{
        model.NewSystemMessage(systemPrompt),
        model.NewUserMessage(userPrompt),
    }
    return graph.State{graph.StateKeyOneShotMessages: oneShot}, nil
})
// 后续进入 LLM 节点时将仅使用 graph.StateKeyOneShotMessages，并在执行后清空
```

```go
// UserInput（graph.StateKeyUserInput）：在历史 graph.StateKeyMessages 基础上附加本轮用户输入
import (
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
    stateKeyCleanedInput = "cleaned_input"
)

sg.AddNode("clean_input", func(ctx context.Context, s graph.State) (any, error) {
    in := strings.TrimSpace(s[graph.StateKeyUserInput].(string))
    return graph.State{
        graph.StateKeyUserInput: in,                // 将清洗后的输入写回，LLM 节点会把 user+assistant 原子写入 messages
        stateKeyCleanedInput:    in,                // 同时保留业务自定义键
    }, nil
})
```

```go
// Messages-only（graph.StateKeyMessages）：工具回路返回后，graph.StateKeyUserInput 已清空；LLM 仅基于 graph.StateKeyMessages（含 tool 响应）继续推理
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
    nodeAsk       = "ask"
    nodeExecTools = "exec_tools"
    nodeFallback  = "fallback"
)

sg.AddToolsNode(nodeExecTools, tools)
sg.AddToolsConditionalEdges(nodeAsk, nodeExecTools, nodeFallback)
// 再次回到 nodeAsk（或下游 LLM 节点）时，由于 graph.StateKeyUserInput 已清空，将走 messages-only 分支
```

#### 指令占位符注入

`AddLLMNode` 的 `instruction` 支持占位符，语法与 `llmagent` 一致：

- `{key}` / `{key?}`：从会话 `session.State` 读取键值，可选后缀 `?` 缺失时为空；
- `{user:subkey}`、`{app:subkey}`、`{temp:subkey}`：按命名空间读取。

GraphAgent 会把当前 `*session.Session` 放入状态（`graph.StateKeySession` 键），LLM 节点会在执行前对指令进行占位符展开。

提示：GraphAgent 会从会话事件播种 `graph.StateKeyMessages` 以保证多轮连贯；从检查点恢复时，若用户消息仅为 "resume"，不会注入到 `graph.StateKeyUserInput`，以避免干扰已恢复的状态。

### 并发执行和状态安全

当一个节点有多条出边时，会自动触发并行执行：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 这样的图结构会自动并行执行
stateGraph.
    AddNode("analyze", analyzeData).
    AddNode("generate_report", generateReport).
    AddNode("call_external_api", callAPI).
    AddEdge("analyze", "generate_report").    // 这两个会并行执行
    AddEdge("analyze", "call_external_api")   //
```

内部实现保证了并发安全：执行器为每个任务构造浅拷贝（maps.Copy）并在合并时加锁，同时通过 Reducer 机制来安全地合并并发更新。

### 节点 I/O 约定与常用键

节点之间仅通过共享 `State` 传递数据，节点函数返回的增量由 Schema 的 Reducer 合并。

- 函数节点（Function）

  - 输入：完整 `State`（按 Schema 声明读取）
  - 输出：只写业务键（例如 `{"parsed_time":"..."}`），不要写内部键

- LLM 节点

  - 输入优先级：`graph.StateKeyOneShotMessages` → `graph.StateKeyUserInput` → `graph.StateKeyMessages`
  - 输出：原子写回 `graph.StateKeyMessages`、设置 `graph.StateKeyLastResponse`、设置 `graph.StateKeyNodeResponses[<llm_node_id>]`

- 工具节点（Tools）

  - 自 `graph.StateKeyMessages` 尾部配对当前轮的 `assistant(tool_calls)`，按顺序追加工具返回到 `graph.StateKeyMessages`
  - 多个工具按 LLM 返回顺序顺序执行

- Agent 节点
  - 通过 `Invocation.RunOptions.RuntimeState` 接收 Graph 的 `State`
  - 输出：设置 `graph.StateKeyLastResponse` 与 `graph.StateKeyNodeResponses[<agent_node_id>]`；执行成功后会清空 `graph.StateKeyUserInput`

实践建议：

- 串行读取：紧邻下游直接读取 `graph.StateKeyLastResponse`；
- 并行/汇合读取：从 `graph.StateKeyNodeResponses[<nodeID>]` 读取指定节点输出；
- 为业务键在 Schema 中声明合适的 Reducer，避免并发写入冲突。

### API 速查表

- 构图

  - `graph.NewStateGraph(schema)` → 构建器
  - `AddNode(id, func, ...opts)` / `AddLLMNode(id, model, instruction, tools, ...opts)`
  - `AddToolsNode(id, tools, ...opts)` / `AddAgentNode(id, ...opts)`
  - `AddEdge(from, to)` / `AddConditionalEdges(from, condition, pathMap)`
  - `AddToolsConditionalEdges(llmNode, toolsNode, fallback)`
  - `SetEntryPoint(nodeID)` / `SetFinishPoint(nodeID)` / `Compile()`

- 常用 State 键（用户可见）

  - `graph.StateKeyUserInput`、`graph.StateKeyOneShotMessages`、`graph.StateKeyMessages`、`graph.StateKeyLastResponse`、`graph.StateKeyNodeResponses`、`graph.StateKeyMetadata`

- 节点级可选项

  - LLM / 工具相关：
    - `graph.WithGenerationConfig`、`graph.WithModelCallbacks`
    - `graph.WithToolCallbacks`、`graph.WithToolSets`
  - 回调相关：
    - `graph.WithPreNodeCallback`、`graph.WithPostNodeCallback`、`graph.WithNodeErrorCallback`

- 执行
  - `graphagent.New(name, compiledGraph, ...opts)` → `runner.NewRunner(app, agent)` → `Run(...)`

更多端到端用法见 `examples/graph`（基础/并行/多轮/中断/工具/占位符）。

## 可视化导出（DOT/图片）

Graph 支持直接导出 Graphviz（图形可视化软件，Graph Visualization）`DOT`（Graphviz 的描述语言，Directed Graph Language）文本，以及通过系统安装的 `dot`（Graphviz 命令行工具 `dot` 是 Graphviz 的布局引擎之一，用于渲染 DOT 文件）渲染 `PNG`（Portable Network Graphics，便携式网络图形格式）/`SVG`（Scalable Vector Graphics，可缩放矢量图形）。

- `WithDestinations`（在节点上声明潜在动态去向）会以虚线（dotted、灰色）显示，仅用于静态检查与可视化，不影响运行时路由。
- 条件边（Conditional edges）会以虚线（dashed、灰色）并标注分支键值。
- 常规边（AddEdge）为实线。
- 虚拟 `Start`/`End` 节点可通过选项打开/隐藏。

示例：

```go
g := sg.MustCompile()

// 生成 DOT 文本
dot := g.DOT(
    graph.WithRankDir(graph.RankDirLR),     // 左→右布局（或 graph.RankDirTB）
    graph.WithIncludeDestinations(true),    // 显示 WithDestinations 声明
    graph.WithGraphLabel("My Workflow"),   // 图标题
)

// 渲染 PNG （需要已安装 Graphviz 的 dot）
if err := g.RenderImage(context.Background(), graph.ImageFormatPNG, "workflow.png",
    graph.WithRankDir(graph.RankDirLR),
    graph.WithIncludeDestinations(true),
); err != nil {
    // 未安装 Graphviz 时这里会返回错误，可忽略或提示安装
}
```

API 参考：

- `g.DOT(...)` / `g.WriteDOT(w, ...)`：导出 DOT 文本
- `g.RenderImage(ctx, format, outputPath, ...)`：调用 `dot` 渲染图片（`png`/`svg` 等）
- 选项：`WithRankDir(graph.RankDirLR|graph.RankDirTB)`、`WithIncludeDestinations(bool)`、`WithIncludeStartEnd(bool)`、`WithGraphLabel(string)`

完整示例见：`examples/graph/visualization`

## 高级特性

### 检查点与恢复

为了支持时间旅行与可靠恢复，可以为执行器或 GraphAgent 配置检查点保存器。下面演示使用 SQLite/Redis Saver 持久化检查点并从特定检查点恢复。

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
    "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/redis"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// 配置检查点
db, _ := sql.Open("sqlite3", "./checkpoints.db")
saver, _ := sqlite.NewSaver(db)

graphAgent, _ := graphagent.New("workflow", g,
    graphagent.WithCheckpointSaver(saver))

// 执行时自动保存检查点（默认每步保存）

// 从检查点恢复
eventCh, err := r.Run(ctx, userID, sessionID,
    model.NewUserMessage("resume"),
    agent.WithRuntimeState(map[string]any{
        graph.CfgKeyCheckpointID: "ckpt-123",
    }),
)

// 配置redis检查点
redisSaver, _ := redis.NewSaver(redis.WithRedisClientURL("redis://[username:password@]host:port[/database]"))

graphAgent, _ := graphagent.New("workflow", g,
    graphagent.WithCheckpointSaver(redisSaver))

// 执行时自动保存检查点（默认每步保存）

// 从检查点恢复
eventCh, err := r.Run(ctx, userID, sessionID,
    model.NewUserMessage("resume"),
    agent.WithRuntimeState(map[string]any{
        graph.CfgKeyCheckpointID: "ckpt-123",
    }),
)
```

#### 检查点管理

使用管理器可以便捷地浏览、查询与删除检查点：

```go
cm := graph.NewCheckpointManager(saver)

// 最新检查点（可按 namespace 过滤；空字符串代表跨命名空间）
latest, _ := cm.Latest(ctx, lineageID, "")

// 列表（按时间倒序）
tuples, _ := cm.ListCheckpoints(ctx, graph.NewCheckpointConfig(lineageID).ToMap(), &graph.CheckpointFilter{Limit: 10})

// 获取具体检查点元组（含 pending writes）
tuple, _ := cm.GetTuple(ctx, graph.CreateCheckpointConfig(lineageID, checkpointID, namespace))

// 删除谱系
_ = cm.DeleteLineage(ctx, lineageID)
```

建议在生产中为 `namespace` 使用稳定的业务标识（如 `svc:prod:flowX`），便于审计与对账。

### 默认值与注意事项

- 默认值（Executor）

  - `ChannelBufferSize = 256`、`MaxSteps = 100`、`CheckpointSaveTimeout = 10s`
  - 步/节点超时可通过 `Executor` 的 `WithStepTimeout` / `WithNodeTimeout` 配置（目前 GraphAgent 选项未直接暴露）

- 会话
  - 生产环境优先使用 Redis Session；设置合理 TTL 与清理策略
- Runner 会自动从会话事件播种多轮 `graph.StateKeyMessages`

- 检查点

  - 采用稳定的 `namespace` 命名（如 `svc:prod:flowX`）；使用 `CheckpointManager` 按谱系审计与清理

- 事件与背压

  - 调整 `WithChannelBufferSize`；按 `author`/`object` 过滤事件降低噪音

- 命名与键

  - 节点/路由标签/状态键使用常量；为需要合并的键声明 Reducer

- 治理与合规
- 关键路径引入 HITL；敏感信息优先落到 `graph.StateKeyMetadata`，避免混入 `graph.StateKeyMessages`

### 事件速览

- Author 约定

  - 节点级：节点 ID（无法获取时为 `graph.AuthorGraphNode`）
  - Pregel 阶段：`graph.AuthorGraphPregel`
  - 执行器/系统：`graph.AuthorGraphExecutor`
  - 用户输入：`user`（未导出常量）

- 对象类型（子集）
  - 节点：`graph.ObjectTypeGraphNodeStart | graph.ObjectTypeGraphNodeComplete | graph.ObjectTypeGraphNodeError`
  - Pregel：`graph.ObjectTypeGraphPregelPlanning | graph.ObjectTypeGraphPregelExecution | graph.ObjectTypeGraphPregelUpdate`
  - 通道/状态：`graph.ObjectTypeGraphChannelUpdate` / `graph.ObjectTypeGraphStateUpdate`
  - 检查点：`graph.ObjectTypeGraphCheckpoint`、`graph.ObjectTypeGraphCheckpointCreated`、`graph.ObjectTypeGraphCheckpointCommitted`、`graph.ObjectTypeGraphCheckpointInterrupt`

更多示例见下文“事件监控”。

### Human-in-the-Loop

在关键路径上引入人工确认（HITL）能够显著提升可控性。下面的示例展示一个“中断—恢复”的基本流程：

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

const (
    stateKeyContent    = "content"
    stateKeyDecision   = "decision"
    interruptKeyReview = "review_key"
    nodeReview         = "review"
)

sg.AddNode(nodeReview, func(ctx context.Context, s graph.State) (any, error) {
    content := s[stateKeyContent].(string)

    // 中断并等待人工输入
    result, err := graph.Interrupt(ctx, s, interruptKeyReview,
        fmt.Sprintf("请审核: %s", content))
    if err != nil {
        return nil, err
    }

    return graph.State{stateKeyDecision: result}, nil
})

// 恢复执行（需要 import agent 包）
eventCh, err := r.Run(ctx, userID, sessionID,
    model.NewUserMessage("resume"),
    agent.WithRuntimeState(map[string]any{
        graph.CfgKeyCheckpointID: checkpointID,
        graph.StateKeyResumeMap: map[string]any{
            "review_key": "approved",
        },
    }),
)
```

恢复辅助函数：

```go
// 带类型的恢复值读取
if v, ok := graph.ResumeValue[string](ctx, state, "approval"); ok { /* 使用 v */ }

// 带默认值
v := graph.ResumeValueOrDefault(ctx, state, "approval", "no")

// 判断/清理
_ = graph.HasResumeValue(state, "approval")
graph.ClearResumeValue(state, "approval")
graph.ClearAllResumeValues(state)
```

也可以在执行入口通过命令注入恢复值（无需提前到特定节点）。使用 Runner 传入 `RuntimeState` 即可：

```go
cmd := graph.NewResumeCommand().
    WithResumeMap(map[string]any{"approval": "yes"})

// 通过 RuntimeState 注入 __command__ 到初始状态
events, err := r.Run(ctx, userID, sessionID,
    model.NewUserMessage("resume"),
    agent.WithRuntimeState(map[string]any{
        graph.StateKeyCommand: cmd,
    }),
)
```

### 事件监控

事件流承载了整个图的执行过程与增量输出。下面的示例展示了如何遍历事件并区分图事件与模型增量：

```go
import (
    "fmt"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

for ev := range eventCh {
    if ev.Response == nil {
        continue
    }
    // 按对象类型分流（Graph 扩展事件类型见 graph/events.go）
    switch ev.Response.Object {
    case graph.ObjectTypeGraphNodeStart:
        fmt.Println("节点开始")
    case graph.ObjectTypeGraphNodeComplete:
        fmt.Println("节点完成")
    case graph.ObjectTypeGraphChannelUpdate:
        fmt.Println("通道更新")
    case graph.ObjectTypeGraphCheckpoint, graph.ObjectTypeGraphCheckpointCommitted:
        fmt.Println("检查点事件")
    }
    // 同时处理模型增量/最终输出
    if len(ev.Response.Choices) > 0 {
        ch := ev.Response.Choices[0]
        if ev.Response.IsPartial && ch.Delta.Content != "" {
            fmt.Print(ch.Delta.Content)
        } else if !ev.Response.IsPartial && ch.Message.Content != "" {
            fmt.Println("\n输出:", ch.Message.Content)
        }
    }
}
```

在实际使用中，建议结合 Event 的 `Author` 字段进行过滤：

- 节点级事件（模型、工具、节点起止）：`Author = <nodeID>`（若无法获取 nodeID，则为 `graph-node`）
- Pregel（规划/执行/更新/错误）：`Author = graph.AuthorGraphPregel`
- 执行器级别事件（状态更新/检查点等）：`Author = graph.AuthorGraphExecutor`
- 用户输入事件（Runner 写入）：`Author = user`

利用这一约定，你可以精准订阅某个节点的流式输出，而无需在节点之间传递流式上下文（流式由事件通道统一承载，状态仍按 LangGraph 风格以结构化 State 传递）。

示例：仅消费节点 `ask` 的流式输出，并在完成时打印最终消息。

```go
import (
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const nodeIDWatch = "ask"

for ev := range eventCh {
    // 仅关注来自指定节点的事件
    if ev.Author != nodeIDWatch {
        continue
    }
    if ev.Response == nil || len(ev.Response.Choices) == 0 {
        continue
    }
    choice := ev.Response.Choices[0]

    // 节点的流式增量（Delta）
    if ev.Response.IsPartial && choice.Delta.Content != "" {
        fmt.Print(choice.Delta.Content)
        continue
    }

    // 节点的最终完整消息
    if !ev.Response.IsPartial && choice.Message.Content != "" {
        fmt.Println("\n[ask] 最终输出:", choice.Message.Content)
    }
}
```

#### 事件元数据（StateDelta）

每个事件还携带 `StateDelta`，可读取模型/工具等执行元数据：

```go
import (
    "encoding/json"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

for ev := range events {
    if ev.StateDelta == nil { continue }
    if b, ok := ev.StateDelta[graph.MetadataKeyModel]; ok {
        var md graph.ModelExecutionMetadata
        _ = json.Unmarshal(b, &md)
        // 使用 md.Input / md.Output / md.Duration 等
    }
    if b, ok := ev.StateDelta[graph.MetadataKeyTool]; ok {
        var td graph.ToolExecutionMetadata
        _ = json.Unmarshal(b, &td)
    }
}
```

#### 在节点回调中携带业务值

默认情况下，中途事件（如 `graph.state.update`）只会上报“更新了哪些键”，不携带值；最终完成事件（`graph.execution`）的 `StateDelta` 才包含“最终状态快照”。如果仅需在“某个节点”完成后，将其返回的部分键值即时发给上游服务，可在该节点的 After 回调中构造并发送一条自定义事件：

实现步骤：

- 在节点上注册 `WithPostNodeCallback`；
- 在回调的 `result any` 中读取该节点返回的 `graph.State`（state delta）；
- 选择需要的键值，序列化后放入自定义事件的 `StateDelta`；
- 通过 `agent.EmitEvent` 发送到事件通道。

示例：

```go
import (
    "context"
    "encoding/json"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
    nodeParse   = "parse"
    stateKeyOut = "parsed_value" // 仅输出所需键
)

func parseNode(ctx context.Context, s graph.State) (any, error) {
    // ...业务处理...
    val := map[string]any{"ok": true, "score": 0.97}
    return graph.State{stateKeyOut: val}, nil
}

func buildGraph() (*graph.Graph, error) {
    sg := graph.NewStateGraph(graph.MessagesStateSchema())
    sg.AddNode(nodeParse, parseNode,
        graph.WithPostNodeCallback(func(
            ctx context.Context,
            cb *graph.NodeCallbackContext,
            st graph.State,
            result any,
            nodeErr error,
        ) (any, error) {
            if nodeErr != nil { return nil, nil }
            inv, _ := agent.InvocationFromContext(ctx)
            execCtx, _ := st[graph.StateKeyExecContext].(*graph.ExecutionContext)
            if delta, ok := result.(graph.State); ok {
                if v, exists := delta[stateKeyOut]; exists && execCtx != nil {
                    evt := graph.NewGraphEvent(
                        inv.InvocationID,
                        cb.NodeID,
                        graph.ObjectTypeGraphNodeExecution,
                    )
                    if evt.StateDelta == nil { evt.StateDelta = make(map[string][]byte) }
                    if b, err := json.Marshal(v); err == nil {
                        evt.StateDelta[stateKeyOut] = b
                        _ = agent.EmitEvent(ctx, inv, execCtx.EventChan, evt)
                    }
                }
            }
            return nil, nil
        }),
    ).
        SetEntryPoint(nodeParse).
        SetFinishPoint(nodeParse)
    return sg.Compile()
}
```

建议：

- 仅输出必要键，控制负载与敏感信息；
- 内部/易变键不会被序列化到最终快照，亦不建议外发（参考 [graph/internal_keys.go:16](https://github.com/trpc-group/trpc-agent-go/blob/main/graph/internal_keys.go#L16)）；
- 文本类中间结果优先复用模型流式事件（`choice.Delta.Content`）。

也可以在 Agent 级别配置回调：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// 方式一：构造回调并注册（推荐）
// 注意：结构化回调 API 需要 trpc-agent-go >= 0.6.0
cb := agent.NewCallbacks().
    RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
        // 返回非空 CustomResponse 可直接短路此轮执行
        return nil, nil
    }).
    RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
        // 可对最终响应做统一修改/替换
        return nil, nil
    })

graphAgent, _ := graphagent.New("workflow", g,
    graphagent.WithAgentCallbacks(cb),
)
```

## 常见问题排查

**Q1: 报错 "graph must have an entry point"**

- 未设置入口点。调用 `SetEntryPoint()`，并确保目标节点已定义。

**Q2: 报错目标/源节点不存在**

- 在连边/条件路由前先定义节点；条件路由的 `pathMap` 目标也需存在。

**Q3: 工具未执行**

- 确认 LLM 返回了 `tool_calls`，并使用了 `AddToolsConditionalEdges(ask, tools, fallback)`；
- 工具名需与模型声明一致；
- 配对规则是从最近一次 `assistant(tool_calls)` 回溯到下一个 `user`，检查消息顺序。

**Q4: LLM 返回 tool_calls 但工具节点未执行（永远路由到 fallback）**

- **根本原因**：使用了 `NewStateSchema()` 而非 `MessagesStateSchema()`。
- `AddToolsConditionalEdges` 内部检查 `state[StateKeyMessages].([]model.Message)` 来判断是否有工具调用。
- 如果 Schema 中没有 `StateKeyMessages` 字段，`MessageReducer` 不会被应用，LLM 节点返回的是 `[]graph.MessageOp` 类型而非 `[]model.Message`，导致类型断言失败，永远路由到 `fallbackNode`。
- **解决方案**：将 `graph.NewStateSchema()` 改为 `graph.MessagesStateSchema()`，然后在其基础上添加业务字段：
  ```go
  // 错误写法
  schema := graph.NewStateSchema()

  // 正确写法
  schema := graph.MessagesStateSchema()
  schema.AddField("my_field", graph.StateField{...})
  ```

**Q5: 没有观察到流式事件**

- 调大 `WithChannelBufferSize` 并按 `Author`/对象类型过滤；
- 确认从 `Runner.Run(...)` 消费事件。

**Q6: 从检查点恢复未按预期继续**

- 通过 `agent.WithRuntimeState(map[string]any{ graph.CfgKeyCheckpointID: "..." })` 传入；
- HITL 恢复时提供 `ResumeMap`；纯 "resume" 文本不会注入到 `graph.StateKeyUserInput`。

**Q7: 并行下状态冲突**

- 为列表/映射等声明合并型 Reducer（如 `StringSliceReducer`、`MergeReducer`），避免多个分支覆盖同一键。

**Q8: AddLLMNode 的 tools 参数起什么作用？工具何时被调用？**

- **tools 参数是声明性的**：传给 `AddLLMNode` 的 tools 会被放入 `model.Request.Tools` 字段，告诉 LLM 有哪些工具可用。
- **LLM 决定是否调用工具**：LLM 根据 tools 声明和用户输入，决定是否在响应中返回 `tool_calls`。
- **tools 不会自动执行**：`AddLLMNode` 只负责声明工具、发送请求给 LLM，不会执行工具。
- **工具执行需要配合 AddToolsNode + AddToolsConditionalEdges**：
  ```go
  // 1. LLM 节点声明工具（告诉 LLM 有哪些工具可用）
  sg.AddLLMNode("ask", model, systemPrompt, tools)

  // 2. 工具节点负责执行工具（根据 LLM 返回的 tool_calls）
  sg.AddToolsNode("tools", tools)

  // 3. 条件边根据 LLM 响应路由（有 tool_calls 则路由到工具节点）
  sg.AddToolsConditionalEdges("ask", "tools", "fallback")
  ```
- **调用时机**：当 `AddToolsConditionalEdges` 检测到 LLM 响应中包含 `tool_calls` 时，会路由到 `AddToolsNode`，由工具节点执行实际调用。

**Q9: 多个 LLM 节点串联时，req.Messages 中消息被重复追加**

- **问题现象**：同一个用户输入在 `req.Messages` 中出现多次。
- **根本原因**：使用 `MessagesStateSchema()` 时，`MessageReducer` 会累积消息。每个 LLM 节点执行时，如果 `StateKeyUserInput` 不为空，会追加新的 user message。当存在循环（如工具调用循环）或多个 LLM 节点串联时，消息会不断累积。
- **解决方案**：在设置新的 `StateKeyUserInput` 之前，使用 `RemoveAllMessages` 清除之前的消息：
  ```go
  // 在准备 prompt 的节点中
  func preparePromptNode(ctx context.Context, state graph.State) (any, error) {
      userMessage := buildUserMessage(...)
      return graph.State{
          // 关键：先清除之前的消息，避免累积
          graph.StateKeyMessages:  graph.RemoveAllMessages{},
          graph.StateKeyUserInput: userMessage,
      }, nil
  }
  ```
- **注意**：`WithSubgraphIsolatedMessages(true)` 只对 `AddSubgraphNode` 有效，对 `AddLLMNode` 无效。

## 实际案例

### 审批工作流

```go
import (
    "context"
    "fmt"
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func buildApprovalWorkflow() (*graph.Graph, error) {
    sg := graph.NewStateGraph(graph.MessagesStateSchema())

    // AI 初审（定义 LLM 模型）
    const (
        modelNameApprove      = "gpt-4o-mini"
        promptApproveDecision = "判断申请是否符合要求，回复 approve 或 reject"

        nodeAIReview    = "ai_review"
        nodeHumanReview = "human_review"
        nodeApprove     = "approve"
        nodeReject      = "reject"

        routeHumanReview = "route_human_review"
        routeReject      = "route_reject"
        routeApprove     = "route_approve"

        stateKeyApplication = "application"
        stateKeyDecision    = "decision"
    )

    llm := openai.New(modelNameApprove)
    sg.AddLLMNode(nodeAIReview, llm, promptApproveDecision, nil)

    // 条件路由到人工审核或拒绝
    sg.AddConditionalEdges(nodeAIReview,
        func(ctx context.Context, s graph.State) (string, error) {
            resp := s[graph.StateKeyLastResponse].(string)
            if strings.Contains(resp, "approve") {
                return routeHumanReview, nil
            }
            return routeReject, nil
        }, map[string]string{
            routeHumanReview: nodeHumanReview,
            routeReject:      nodeReject,
        })

    // 人工审核节点
    sg.AddNode(nodeHumanReview, func(ctx context.Context, s graph.State) (any, error) {
        app := s[stateKeyApplication].(string)
        decision, err := graph.Interrupt(ctx, s, "approval",
            fmt.Sprintf("请审批: %s", app))
        if err != nil {
            return nil, err
        }
        return graph.State{stateKeyDecision: decision}, nil
    })

    // 结果处理
    sg.AddNode(nodeApprove, func(ctx context.Context, s graph.State) (any, error) {
        // 执行批准逻辑
        return graph.State{"status": "approved"}, nil
    })
    sg.AddNode(nodeReject, func(ctx context.Context, s graph.State) (any, error) {
        return graph.State{"status": "rejected"}, nil
    })

    // 配置流程
    sg.SetEntryPoint(nodeAIReview)
    sg.AddConditionalEdges(nodeHumanReview,
        func(ctx context.Context, s graph.State) (string, error) {
            if s[stateKeyDecision] == "approve" {
                return routeApprove, nil
            }
            return routeReject, nil
        }, map[string]string{
            routeApprove: nodeApprove,
            routeReject:  nodeReject,
        })

    return sg.Compile()
}
```

## 总结

本文介绍了 `graph` 包与 GraphAgent 的核心用法：如何声明节点与路由、如何通过 Schema 与 Reducer 安全合并状态、以及如何利用事件、检查点与中断实现可观测与可恢复。对于结构化流程（审批、内容审核、分步数据处理等），Graph 提供稳定、可审计的执行路径；对于需要智能决策的环节，可通过 LLM 节点与子 Agent 灵活扩展。

## 参考与示例

- 代码仓库: https://github.com/trpc-group/trpc-agent-go
- Graph 示例: `examples/graph` 目录（基础/并行/多轮/中断与恢复等）
  - I/O 约定：`io_conventions`、`io_conventions_tools`
  - 并行 / 扇出：`parallel`、`fanout`、`diamond`
  - 占位符：`placeholder`
  - 检查点 / 中断：`checkpoint`、`interrupt`
- 进一步阅读：`graph/state_graph.go`、`graph/executor.go`、`agent/graphagent`
