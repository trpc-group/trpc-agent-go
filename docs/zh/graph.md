# Graph 包使用指南

Graph 包是 trpc-agent-go 中用于构建和执行工作流的核心组件。它提供了一个类型安全、可扩展的图执行引擎，支持复杂的 AI 工作流编排。

## 概述

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
- **不支持 SubAgent**：GraphAgent 本身不支持子 Agent，专注于工作流执行

这种设计使得 GraphAgent 可以灵活地集成到复杂的多 Agent 系统中。

### 主要特性

- **类型安全的状态管理**：使用 Schema 定义状态结构，支持自定义 Reducer
- **条件路由**：基于状态动态选择执行路径
- **LLM 节点集成**：内置对大型语言模型的支持
- **工具节点**：支持函数调用和外部工具集成
- **流式执行**：支持实时事件流和进度跟踪
- **并发安全**：线程安全的图执行

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

- `StateKeyUserInput`：用户输入（由 GraphAgent 自动设置，来自 Runner 的消息）
- `StateKeyLastResponse`：最后响应（用于设置最终输出，Executor 会读取此值作为结果）
- `StateKeyMessages`：消息历史（用于 LLM 节点，由 LLM 节点自动更新）
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

### 1. 创建 GraphAgent 和 Runner

用户主要通过创建 GraphAgent 然后通过 Runner 来使用 Graph 包。这是推荐的使用模式：

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/event"
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
        
        if len(event.Choices) > 0 {
            choice := event.Choices[0]
            if choice.Delta.Content != "" {
                fmt.Print(choice.Delta.Content)
            }
        }
        
        if event.Done {
            break
        }
    }
}

// 节点函数实现
func startNodeFunc(ctx context.Context, state graph.State) (any, error) {
    // 从内置的 StateKeyUserInput 获取用户输入（由 Runner 自动设置）
    input := state[graph.StateKeyUserInput].(string)
    return graph.State{
        StateKeyProcessedData: fmt.Sprintf("处理后的: %s", input),
    }, nil
}

func processNodeFunc(ctx context.Context, state graph.State) (any, error) {
    processed := state[StateKeyProcessedData].(string)
    result := fmt.Sprintf("结果: %s", processed)
    return graph.State{
        StateKeyResult: result,
        // 使用内置的 StateKeyLastResponse 来设置最终输出
        graph.StateKeyLastResponse: fmt.Sprintf("最终结果: %s", result),
    }, nil
}
```

### 2. 使用 LLM 节点

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

### 3. GraphAgent 配置选项

GraphAgent 支持多种配置选项：

```go
// 创建 GraphAgent 时可以使用多种选项
graphAgent, err := graphagent.New("workflow-name", compiledGraph,
    graphagent.WithDescription("工作流描述"),
    graphagent.WithInitialState(graph.State{
        "initial_data": "初始数据",
    }),
    graphagent.WithChannelBufferSize(1024),
    graphagent.WithTools([]tool.Tool{
        calculatorTool,
        searchTool,
    }),
    graphagent.WithModelCallbacks(&model.Callbacks{
        // 模型回调配置
    }),
    graphagent.WithToolCallbacks(&tool.Callbacks{
        // 工具回调配置
    }),
)
```

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

### 6. Runner 配置

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
message := model.NewUserMessage("用户输入")
eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
```

### 7. 消息状态模式

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

### 8. 状态键使用场景

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

### 1. 自定义 Reducer

Reducer 定义如何合并状态更新：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 默认 Reducer：覆盖现有值
graph.DefaultReducer(existing, update) any

// 合并 Reducer：合并映射
graph.MergeReducer(existing, update) any

// 追加 Reducer：追加到切片
graph.AppendReducer(existing, update) any

// 消息 Reducer：处理消息数组
graph.MessageReducer(existing, update) any
```

### 2. 命令模式

节点可以返回命令来同时更新状态和指定路由：

```go
import (
    "context"
    
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func routingNodeFunc(ctx context.Context, state graph.State) (any, error) {
    // 根据条件决定下一步
    if shouldGoToA(state) {
        return &graph.Command{
            Update: graph.State{"status": "going_to_a"},
            GoTo:   "node_a",
        }, nil
    }
    
    return &graph.Command{
        Update: graph.State{"status": "going_to_b"},
        GoTo:   "node_b",
    }, nil
}
```

### 3. 执行器配置

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 创建带配置的执行器
executor, err := graph.NewExecutor(compiledGraph,
    graph.WithChannelBufferSize(1024),
    graph.WithMaxSteps(50),
)
```

### 4. 虚拟节点和路由

Graph 包使用虚拟节点来简化工作流的入口和出口：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 特殊节点标识符
const (
    Start = "__start__"  // 虚拟起始节点
    End   = "__end__"    // 虚拟结束节点
)

// 设置入口点（自动创建 Start -> nodeID 的边）
stateGraph.SetEntryPoint("first_node")

// 设置结束点（自动创建 nodeID -> End 的边）
stateGraph.SetFinishPoint("last_node")

// 不需要显式添加这些边：
// stateGraph.AddEdge(Start, "first_node")  // 不需要
// stateGraph.AddEdge("last_node", End)     // 不需要
```

这种设计使得工作流定义更加简洁，开发者只需要关注实际的业务节点和它们之间的连接。

## 最佳实践

### 1. 状态管理

- 使用常量定义状态键，避免硬编码字符串
- 为复杂状态创建 Helper 函数
- 使用 Schema 验证状态结构
- 区分内置状态键和用户自定义状态键

```go
import (
    "errors"
    
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// 定义用户自定义状态键常量
const (
    StateKeyInput        = "input"          // 用户业务数据
    StateKeyResult       = "result"         // 处理结果
    StateKeyProcessedData = "processed_data" // 处理后的数据
    StateKeyStatus       = "status"         // 处理状态
)

// 用户可访问的内置状态键（谨慎使用）
// StateKeyUserInput    - 用户输入（GraphAgent 自动设置）
// StateKeyLastResponse - 最后响应（Executor 读取作为最终结果）
// StateKeyMessages     - 消息历史（LLM 节点自动更新）
// StateKeyMetadata     - 元数据（用户可用的通用存储）

// 系统内部状态键（用户不应直接使用）
// StateKeySession      - 会话信息（GraphAgent 自动设置）
// StateKeyExecContext  - 执行上下文（Executor 自动设置）
// StateKeyToolCallbacks - 工具回调（Executor 自动设置）
// StateKeyModelCallbacks - 模型回调（Executor 自动设置）

// 创建状态 Helper
type StateHelper struct {
    state graph.State
}

func (h *StateHelper) GetInput() (string, error) {
    if input, ok := h.state[StateKeyInput].(string); ok {
        return input, nil
    }
    return "", errors.New("input not found")
}

func (h *StateHelper) GetUserInput() (string, error) {
    if input, ok := h.state[graph.StateKeyUserInput].(string); ok {
        return input, nil
    }
    return "", errors.New("user_input not found")
}
```

### 2. 错误处理

- 在节点函数中返回有意义的错误
- 使用错误类型常量进行分类
- 在条件函数中处理异常情况

```go
import (
    "context"
    "fmt"
    
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func safeNodeFunc(ctx context.Context, state graph.State) (any, error) {
    input, ok := state["input"].(string)
    if !ok {
        return nil, fmt.Errorf("input field not found or wrong type")
    }
    
    if input == "" {
        return nil, fmt.Errorf("input cannot be empty")
    }
    
    // 处理逻辑...
    return result, nil
}
```

### 3. 性能优化

- 合理设置执行器缓冲区大小
- 使用最大步数限制防止无限循环
- 考虑并行执行路径（如果支持）

### 4. 测试

```go
import (
    "context"
    "testing"
    
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestWorkflow(t *testing.T) {
    // 创建测试图
    graph := createTestGraph()
    
    // 创建执行器
    executor, err := graph.NewExecutor(graph)
    require.NoError(t, err)
    
    // 执行测试
    initialState := graph.State{"test_input": "test"}
    eventChan, err := executor.Execute(context.Background(), initialState, nil)
    require.NoError(t, err)
    
    // 验证结果
    for event := range eventChan {
        // 验证事件内容
        assert.NotNil(t, event)
    }
}
```

## 常见用例

### 1. 文档处理工作流

这是一个完整的文档处理工作流示例，展示了如何使用 GraphAgent 和 Runner：

```go
package main

import (
    "context"
    "fmt"
    "strings"
    "time"
    
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 用户自定义的状态键
const (
    StateKeyDocumentLength  = "document_length"
    StateKeyWordCount       = "word_count"
    StateKeyComplexityLevel = "complexity_level"
    StateKeyProcessingStage = "processing_stage"
)

type documentWorkflow struct {
    modelName string
    runner    runner.Runner
    userID    string
    sessionID string
}

func (w *documentWorkflow) setup() error {
    // 1. 创建文档处理图
    workflowGraph, err := w.createDocumentProcessingGraph()
    if err != nil {
        return fmt.Errorf("failed to create graph: %w", err)
    }

    // 2. 创建 GraphAgent
    graphAgent, err := graphagent.New("document-processor", workflowGraph,
        graphagent.WithDescription("综合文档处理工作流"),
        graphagent.WithInitialState(graph.State{}),
    )
    if err != nil {
        return fmt.Errorf("failed to create graph agent: %w", err)
    }

    // 3. 创建会话服务
    sessionService := inmemory.NewSessionService()

    // 4. 创建 Runner
    w.runner = runner.NewRunner(
        "document-workflow",
        graphAgent,
        runner.WithSessionService(sessionService),
    )

    // 5. 设置标识符
    w.userID = "user"
    w.sessionID = fmt.Sprintf("workflow-session-%d", time.Now().Unix())

    return nil
}

func (w *documentWorkflow) createDocumentProcessingGraph() (*graph.Graph, error) {
    // 创建状态模式
    schema := graph.MessagesStateSchema()
    
    // 创建模型实例
    modelInstance := openai.New(w.modelName)
    
    // 创建分析工具
    complexityTool := function.NewFunctionTool(
        w.analyzeComplexity,
        function.WithName("analyze_complexity"),
        function.WithDescription("分析文档复杂度级别"),
    )
    
    // 创建状态图
    stateGraph := graph.NewStateGraph(schema)
    tools := map[string]tool.Tool{
        "analyze_complexity": complexityTool,
    }
    
    // 构建工作流图
    stateGraph.
        AddNode("preprocess", w.preprocessDocument).
        AddLLMNode("analyze", modelInstance,
            `你是一个文档分析专家。分析提供的文档并：
1. 分类文档类型和复杂度（简单、中等、复杂）
2. 提取关键主题
3. 评估内容质量
使用 analyze_complexity 工具进行详细分析。
只返回复杂度级别："simple" 或 "complex"。`,
            tools).
        AddToolsNode("tools", tools).
        AddNode("route_complexity", w.routeComplexity).
        AddLLMNode("summarize", modelInstance,
            `你是一个文档摘要专家。创建文档的全面而简洁的摘要。
专注于：
1. 关键点和主要论点
2. 重要细节和见解
3. 逻辑结构和流程
4. 结论和影响
提供结构良好的摘要，保留重要信息。
记住：只输出最终结果本身，不要其他文本。`,
            map[string]tool.Tool{}).
        AddLLMNode("enhance", modelInstance,
            `你是一个内容增强专家。通过以下方式改进提供的内容：
1. 提高清晰度和可读性
2. 改进结构和组织
3. 在适当的地方添加相关细节
4. 确保一致性和连贯性
专注于使内容更有吸引力和专业性，同时保持原意。
记住：只输出最终结果本身，不要其他文本。`,
            map[string]tool.Tool{}).
        AddNode("format_output", w.formatOutput).
        SetEntryPoint("preprocess").
        SetFinishPoint("format_output")
    
    // 添加工作流边
    stateGraph.AddEdge("preprocess", "analyze")
    stateGraph.AddToolsConditionalEdges("analyze", "tools", "route_complexity")
    stateGraph.AddEdge("tools", "analyze")
    
    // 添加复杂度条件路由
    stateGraph.AddConditionalEdges("route_complexity", w.complexityCondition, map[string]string{
        "simple":  "enhance",
        "complex": "summarize",
    })
    
    stateGraph.AddEdge("enhance", "format_output")
    stateGraph.AddEdge("summarize", "format_output")
    
    // SetEntryPoint 和 SetFinishPoint 会自动处理与虚拟 Start/End 节点的连接
    
    return stateGraph.Compile()
}

// 节点函数实现
func (w *documentWorkflow) preprocessDocument(ctx context.Context, state graph.State) (any, error) {
    var input string
    if userInput, ok := state[graph.StateKeyUserInput].(string); ok {
        input = userInput
    }
    if input == "" {
        return nil, fmt.Errorf("no input document found")
    }
    
    input = strings.TrimSpace(input)
    if len(input) < 10 {
        return nil, fmt.Errorf("document too short for processing (minimum 10 characters)")
    }
    
    return graph.State{
        StateKeyDocumentLength:  len(input),
        StateKeyWordCount:       len(strings.Fields(input)),
        graph.StateKeyUserInput: input,
        StateKeyProcessingStage: "preprocessing",
    }, nil
}

func (w *documentWorkflow) routeComplexity(ctx context.Context, state graph.State) (any, error) {
    return graph.State{
        StateKeyProcessingStage: "complexity_routing",
    }, nil
}

func (w *documentWorkflow) complexityCondition(ctx context.Context, state graph.State) (string, error) {
    if msgs, ok := state[graph.StateKeyMessages].([]model.Message); ok {
        if len(msgs) > 0 {
            lastMsg := msgs[len(msgs)-1]
            if strings.Contains(strings.ToLower(lastMsg.Content), "simple") {
                return "simple", nil
            }
        }
    }
    return "complex", nil
}

func (w *documentWorkflow) formatOutput(ctx context.Context, state graph.State) (any, error) {
    var result string
    if lastResponse, ok := state[graph.StateKeyLastResponse].(string); ok {
        result = lastResponse
    }
    
    finalOutput := fmt.Sprintf(`DOCUMENT PROCESSING RESULTS
========================
Processing Stage: %s
Document Length: %d characters
Word Count: %d words
Complexity Level: %s

Processed Content:
%s
`, 
        state[StateKeyProcessingStage],
        state[StateKeyDocumentLength],
        state[StateKeyWordCount],
        state[StateKeyComplexityLevel],
        result,
    )
    
    return graph.State{
        graph.StateKeyLastResponse: finalOutput,
    }, nil
}

// 工具函数
func (w *documentWorkflow) analyzeComplexity(ctx context.Context, args map[string]any) (any, error) {
    text, ok := args["text"].(string)
    if !ok {
        return nil, fmt.Errorf("text argument is required")
    }
    
    wordCount := len(strings.Fields(text))
    sentenceCount := len(strings.Split(text, "."))
    
    var level string
    var score float64
    
    if wordCount < 100 {
        level = "simple"
        score = 0.3
    } else if wordCount < 500 {
        level = "moderate"
        score = 0.6
    } else {
        level = "complex"
        score = 0.9
    }
    
    return map[string]any{
        "level":          level,
        "score":          score,
        "word_count":     wordCount,
        "sentence_count": sentenceCount,
    }, nil
}

// 执行工作流
func (w *documentWorkflow) processDocument(ctx context.Context, content string) error {
    message := model.NewUserMessage(content)
    eventChan, err := w.runner.Run(ctx, w.userID, w.sessionID, message)
    if err != nil {
        return fmt.Errorf("failed to run workflow: %w", err)
    }
    return w.processStreamingResponse(eventChan)
}

func (w *documentWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
    var workflowStarted bool
    var finalResult string
    
    for event := range eventChan {
        if event.Error != nil {
            fmt.Printf("❌ Error: %s\n", event.Error.Message)
            continue
        }
        
        if len(event.Choices) > 0 {
            choice := event.Choices[0]
            if choice.Delta.Content != "" {
                if !workflowStarted {
                    fmt.Print("🤖 Workflow: ")
                    workflowStarted = true
                }
                fmt.Print(choice.Delta.Content)
            }
            
            if choice.Message.Content != "" && event.Done {
                finalResult = choice.Message.Content
            }
        }
        
        if event.Done {
            if finalResult != "" && strings.Contains(finalResult, "DOCUMENT PROCESSING RESULTS") {
                fmt.Printf("\n\n%s\n", finalResult)
            }
            break
        }
    }
    return nil
}
```

### 2. 对话机器人

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// 创建对话机器人
func createChatBot(modelName string) (*runner.Runner, error) {
    // 创建状态图
    stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())
    
    // 创建模型和工具
    modelInstance := openai.New(modelName)
    tools := map[string]tool.Tool{
        "calculator": calculatorTool,
        "search":     searchTool,
    }
    
    // 构建对话图
    stateGraph.
        AddLLMNode("chat", modelInstance, 
            `你是一个有用的AI助手。根据用户的问题提供帮助，并在需要时使用工具。`,
            tools).
        AddToolsNode("tools", tools).
        AddToolsConditionalEdges("chat", "tools", "chat").
        SetEntryPoint("chat").
        SetFinishPoint("chat")
    
    // 编译图
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        return nil, err
    }
    
    // 创建 GraphAgent
    graphAgent, err := graphagent.New("chat-bot", compiledGraph,
        graphagent.WithDescription("智能对话机器人"),
        graphagent.WithInitialState(graph.State{}),
    )
    if err != nil {
        return nil, err
    }
    
    // 创建 Runner
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "chat-bot-app",
        graphAgent,
        runner.WithSessionService(sessionService),
    )
    
    return appRunner, nil
}
```

### 3. 数据处理管道

```go
import (
    "reflect"
    
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// 创建数据处理管道
func createDataPipeline() (*runner.Runner, error) {
    // 创建自定义状态模式
    schema := graph.NewStateSchema()
    schema.AddField("data", graph.StateField{
        Type:    reflect.TypeOf([]any{}),
        Reducer: graph.AppendReducer,
        Default: func() any { return []any{} },
    })
    schema.AddField("quality_score", graph.StateField{
        Type:    reflect.TypeOf(0.0),
        Reducer: graph.DefaultReducer,
    })
    
    // 创建状态图
    stateGraph := graph.NewStateGraph(schema)
    
    // 构建数据处理管道
    stateGraph.
        AddNode("extract", extractData).
        AddNode("validate", validateData).
        AddConditionalEdges("validate", routeByQuality, map[string]string{
            "high":   "transform",
            "medium": "clean",
            "low":    "reject",
        }).
        AddNode("clean", cleanData).
        AddNode("transform", transformData).
        AddNode("load", loadData).
        AddEdge("clean", "transform").
        AddEdge("transform", "load").
        SetEntryPoint("extract").
        SetFinishPoint("load")
    
    // 编译图
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        return nil, err
    }
    
    // 创建 GraphAgent
    graphAgent, err := graphagent.New("data-pipeline", compiledGraph,
        graphagent.WithDescription("数据处理管道"),
        graphagent.WithInitialState(graph.State{}),
    )
    if err != nil {
        return nil, err
    }
    
    // 创建 Runner
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "data-pipeline-app",
        graphAgent,
        runner.WithSessionService(sessionService),
    )
    
    return appRunner, nil
}
```

### 4. GraphAgent 作为 SubAgent

GraphAgent 可以作为其他 Agent 的子 Agent，实现复杂的多 Agent 协作：

```go
import (
    "context"
    "log"
    
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// 创建文档处理 GraphAgent
func createDocumentProcessor() (agent.Agent, error) {
    // 创建文档处理图
    stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())
    
    // 添加文档处理节点
    stateGraph.
        AddNode("preprocess", preprocessDocument).
        AddLLMNode("analyze", modelInstance, analysisPrompt, tools).
        AddNode("format", formatOutput).
        SetEntryPoint("preprocess").
        SetFinishPoint("format")
    
    // 编译图
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        return nil, err
    }
    
    // 创建 GraphAgent
    return graphagent.New("document-processor", compiledGraph,
        graphagent.WithDescription("专业文档处理工作流"),
    )
}

// 创建协调器 Agent，使用 GraphAgent 作为子 Agent
func createCoordinatorAgent() (agent.Agent, error) {
    // 创建文档处理 GraphAgent
    documentProcessor, err := createDocumentProcessor()
    if err != nil {
        return nil, err
    }
    
    // 创建其他子 Agent
    mathAgent := llmagent.New("math-agent", 
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("数学计算专家"),
        llmagent.WithTools([]tool.Tool{calculatorTool}),
    )
    
    // 创建协调器 Agent
    coordinator := llmagent.New("coordinator",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("任务协调器，可以委托给专业子 Agent"),
        llmagent.WithInstruction(`你是一个协调器，可以委托任务给专业子 Agent：
- document-processor: 文档处理和分析
- math-agent: 数学计算和公式处理

根据用户需求选择合适的子 Agent 处理任务。`),
        llmagent.WithSubAgents([]agent.Agent{
            documentProcessor,  // GraphAgent 作为子 Agent
            mathAgent,
        }),
    )
    
    return coordinator, nil
}

// 使用示例
func main() {
    // 创建协调器 Agent
    coordinator, err := createCoordinatorAgent()
    if err != nil {
        log.Fatal(err)
    }
    
    // 创建 Runner
    runner := runner.NewRunner("coordinator-app", coordinator)
    
    // 执行任务（协调器会自动选择合适的子 Agent）
    message := model.NewUserMessage("请分析这份文档并计算其中的统计数据")
    eventChan, err := runner.Run(ctx, userID, sessionID, message)
    // ...
}
```

**关键特点**：

- GraphAgent 实现了 `agent.Agent` 接口，可以被其他 Agent 作为子 Agent 使用
- 协调器 Agent 可以通过 `transfer_to_agent` 工具委托任务给 GraphAgent
- GraphAgent 专注于工作流执行，不支持自己的子 Agent
- 这种设计实现了复杂工作流与多 Agent 系统的无缝集成

## 故障排除

### 常见错误

1. **"node not found"**：检查节点 ID 是否正确
2. **"invalid graph"**：确保图有入口点和所有节点可达
3. **"maximum execution steps exceeded"**：检查是否有循环或增加最大步数
4. **"state validation failed"**：检查状态模式定义

### 调试技巧

- 使用事件流监控执行过程
- 在节点函数中添加日志
- 验证状态模式定义
- 检查条件函数逻辑

## 总结

Graph 包提供了一个强大而灵活的工作流编排系统，特别适合构建复杂的 AI 应用。通过 GraphAgent 和 Runner 的组合使用，您可以创建高效、可维护的工作流应用。

### 关键要点

**工作流创建**：

- 使用 `StateGraph` 构建器创建图结构
- 定义清晰的状态模式和数据流
- 合理使用条件路由和工具节点

**应用集成**：

- 通过 `GraphAgent` 包装工作流图
- 使用 `Runner` 管理会话和执行环境
- 处理流式事件和错误响应

**Agent 集成**：

- GraphAgent 实现了 `agent.Agent` 接口
- 可以作为其他 Agent 的子 Agent 使用
- 支持复杂的多 Agent 协作场景
- 专注于工作流执行，不支持自己的子 Agent

**最佳实践**：

- 使用类型安全的状态键常量
- 实现适当的错误处理和恢复机制
- 测试和监控工作流执行过程
- 合理配置执行器参数和缓冲区大小
- 考虑将复杂工作流封装为 GraphAgent 子 Agent

### 典型使用流程

```go
import (
    "context"
    
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 1. 创建和编译图
stateGraph := graph.NewStateGraph(schema)
// ... 添加节点和边
compiledGraph, err := stateGraph.Compile()

// 2. 创建 GraphAgent
graphAgent, err := graphagent.New("workflow-name", compiledGraph, opts...)

// 3. 创建 Runner
appRunner := runner.NewRunner("app-name", graphAgent, runnerOpts...)

// 4. 执行工作流
message := model.NewUserMessage("用户输入")
eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
```

这种模式使得 Graph 包特别适合构建企业级的 AI 工作流应用，提供了良好的可扩展性、可维护性和用户体验。
