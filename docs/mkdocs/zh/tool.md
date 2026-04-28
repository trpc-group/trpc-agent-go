# Tool 工具使用文档

Tool 工具系统是 tRPC-Agent-Go 框架的核心组件，为 Agent 提供了与外部服务和功能交互的能力。框架支持多种工具类型，包括函数工具和基于 MCP（Model Context Protocol）标准的外部工具集成。

## 概述

### 🎯 核心特性

- **🔧 多类型工具**：支持函数工具（Function Tools）和 MCP 标准工具
- **🌊 流式响应**：支持实时流式响应和普通响应两种模式
- **⚡ 并行执行**：工具调用支持并行执行以提升性能
- **🔄 MCP 协议**：完整支持 STDIO、SSE、Streamable HTTP 三种传输方式
- **🔁 单次调用重试**：支持在 LLMAgent / Graph ToolsNode 上为单次 `CallableTool` 调用配置重试
- **🛠️ 配置支持**：提供配置选项和过滤器支持
- **🧹 参数修复**：可选启用 `agent.WithToolCallArgumentsJSONRepairEnabled(true)`，对 `tool_calls` 的 `arguments` 做一次尽力 JSON 修复，提升工具执行与外部解析的鲁棒性

### 核心概念

#### 🔧 Tool（工具）

Tool 是单个功能的抽象，实现 `tool.Tool` 接口。每个 Tool 提供特定的能力，如数学计算、搜索、时间查询等。

```go
type Tool interface {
    Declaration() *Declaration  // 返回工具元数据
}

type CallableTool interface {
    Call(ctx context.Context, jsonArgs []byte) (any, error)
    Tool
}
```

**建议（务必配置 name 与 description）**

- **name（必填）**：用于让模型精确定位要调用的工具。请保证 **稳定、唯一、语义明确**（建议使用 `snake_case`），不要在不同工具/不同 ToolSet 之间重名。
- **description（必填）**：用于让模型理解“这个工具做什么/何时该用/有什么约束”。没有清晰的描述会显著降低 tool call 的命中率与稳定性。

> 对于 Function Tool：通过 `function.WithName(...)` / `function.WithDescription(...)` 配置；对于自定义 Tool：在 `Declaration()` 返回的 `tool.Declaration` 中设置 `Name` / `Description`。

#### 📦 ToolSet（工具集）

ToolSet 是一组相关工具的集合，实现 `tool.ToolSet` 接口。ToolSet 负责管理工具的生命周期、连接和资源清理。

```go
type ToolSet interface {
    // 返回当前工具集内的工具
    Tools(context.Context) []tool.Tool

    // 释放工具集持有的资源
    Close() error

    // 返回该工具集的名称，用于标识与冲突处理
    Name() string
}
```

**Tool 与 ToolSet 的关系：**

- 一个 "Tool" = 一个具体功能（如计算器）
- 一个 "ToolSet" = 一组相关的 Tool（如 MCP 服务器提供的所有工具）
- Agent 可以同时使用多个 Tool 和多个 ToolSet

#### 🌊 流式工具支持

框架支持流式工具，提供实时响应能力：

```go
// 流式工具接口
type StreamableTool interface {
    StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error)
    Tool
}

// 流式数据单元
type StreamChunk struct {
    Content  any      `json:"content"`
    Metadata Metadata `json:"metadata,omitempty"`
}
```

**流式工具特点：**

- 🚀 **实时响应**：数据逐步返回，无需等待完整结果
- 📊 **大数据处理**：适用于日志查询、数据分析等场景
- ⚡ **用户体验**：提供即时反馈和进度显示

### 工具类型说明

| 工具类型                   | 定义                           | 集成方式                         |
| -------------------------- | ------------------------------ | -------------------------------- |
| **Function Tools**         | 直接调用 Go 函数实现的工具     | `Tool` 接口，进程内调用          |
| **Agent Tool (AgentTool)** | 将任意 Agent 包装为可调用工具  | `Tool` 接口，支持流式内部转发    |
| **DuckDuckGo Tool**        | 基于 DuckDuckGo API 的搜索工具 | `Tool` 接口，HTTP API            |
| **MCP ToolSet**            | 基于 MCP 协议的外部工具集      | `ToolSet` 接口，支持多种传输方式 |

> **📖 相关文档**：Agent 间协作相关的 Agent Tool 和 Transfer Tool 请参考 [多 Agent 系统文档](multiagent.md)。

## Function Tools 函数工具

Function Tools 通过 Go 函数直接实现工具逻辑，是最简单直接的工具类型。

### 基本用法

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/function"

// 1. 定义工具函数
func calculator(ctx context.Context, req struct {
    Operation string  `json:"operation" jsonschema:"description=运算类型，例如 add/multiply"`
    A         float64 `json:"a" jsonschema:"description=第一个操作数"`
    B         float64 `json:"b" jsonschema:"description=第二个操作数"`
}) (map[string]interface{}, error) {
    switch req.Operation {
    case "add":
        return map[string]interface{}{"result": req.A + req.B}, nil
    case "multiply":
        return map[string]interface{}{"result": req.A * req.B}, nil
    default:
        return nil, fmt.Errorf("unsupported operation: %s", req.Operation)
    }
}

// 2. 创建工具
calculatorTool := function.NewFunctionTool(
    calculator,
    function.WithName("calculator"),
    function.WithDescription("执行数学运算"),
)

// 3. 集成到 Agent
agent := llmagent.New("math-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{calculatorTool}))
```

### Input Schema（入参 schema）与字段描述

Function Tool 的入参 `req` 会自动生成对应的 JSON Schema（用于模型理解参数结构）。建议通过 struct tag 补充字段描述：

- **字段名**：使用 `json:"..."` 作为 schema 的字段名。
- **字段描述（推荐）**：使用 `jsonschema:"description=..."` 写入 schema 的 `properties.<field>.description`。
- **注意**：`jsonschema` tag 内部使用英文逗号 `,` 作为分隔符，因此 **description 内容中不能包含 `,`**，否则会被误解析成多个 tag。
- **兼容**：也支持 `description:"..."` 作为字段描述（用于历史代码）；若同时配置 `jsonschema:"description=..."` 与 `description:"..."`，以 `jsonschema` 中的 `description` 为准。
- **更灵活的 schema**：如果想完全自定义入参 schema（例如需要更复杂的 JSON Schema 结构/约束），可使用 `function.WithInputSchema(customInputSchema)` 跳过自动生成。

### 流式工具示例

```go
// 1. 定义输入输出结构
type weatherInput struct {
    Location string `json:"location" jsonschema:"description=查询地点，例如城市名或经纬度"`
}

type weatherOutput struct {
    Weather string `json:"weather"`
}

// 2. 实现流式工具函数
func getStreamableWeather(input weatherInput) *tool.StreamReader {
    stream := tool.NewStream(10)
    go func() {
        defer stream.Writer.Close()

        // 模拟逐步返回天气数据
        result := "Sunny, 25°C in " + input.Location
        for i := 0; i < len(result); i++ {
            chunk := tool.StreamChunk{
                Content: weatherOutput{
                    Weather: result[i : i+1],
                },
                Metadata: tool.Metadata{CreatedAt: time.Now()},
            }

            if closed := stream.Writer.Send(chunk, nil); closed {
                break
            }
            time.Sleep(10 * time.Millisecond) // 模拟延迟
        }
    }()

    return stream.Reader
}

// 3. 创建流式工具
weatherStreamTool := function.NewStreamableFunctionTool[weatherInput, weatherOutput](
    getStreamableWeather,
    function.WithName("get_weather_stream"),
    function.WithDescription("流式获取天气信息"),
)

// 4. 使用流式工具
reader, err := weatherStreamTool.StreamableCall(ctx, jsonArgs)
if err != nil {
    return err
}

// 接收流式数据
for {
    chunk, err := reader.Recv()
    if err == io.EOF {
        break // 流结束
    }
    if err != nil {
        return err
    }

    // 处理每个数据块
    fmt.Printf("收到数据: %v\n", chunk.Content)
}
reader.Close()
```

### 在 Tool 实现里获取 Tool Call ID

当模型发出一条 `tool_call` 后，框架会在真正执行工具前，把这次调用的
`tool_call_id` 注入到工具执行的 `context.Context` 中。

这意味着：在你自己的 Tool 实现里，框架**支持**直接读取这条 ID。

这个能力对以下场景特别有用：

- 同名工具并发调用时，为每次调用生成不冲突的状态键
- 给日志、监控、埋点、trace 打上稳定的工具调用标识
- 当工具内部再触发一个子 Agent 时，把“这个子 Agent 来自哪条
  tool_call”传给 UI 或上层编排逻辑

当前这套机制适用于：

- LLMAgent 中的普通函数工具
- LLMAgent 中的流式工具
- GraphAgent 的工具执行节点
- Tool callbacks / plugins（回调参数里也会带 `ToolCallID`）

#### 最直接的用法

在工具实现中调用 `tool.ToolCallIDFromContext(ctx)` 即可：

```go
const defaultToolCallID = "default"

type searchArgs struct {
    Query string `json:"query"`
}

func searchDocs(
    ctx context.Context,
    args searchArgs,
) (map[string]any, error) {
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
        toolCallID = defaultToolCallID
    }

    log.Printf(
        "tool_call_id=%s query=%s",
        toolCallID,
        args.Query,
    )

    return map[string]any{
        "tool_call_id": toolCallID,
        "query":        args.Query,
    }, nil
}
```

如果你只是想在 Tool 里打印日志、做指标、写 Invocation
State，通常到这里就够了。

完整可运行示例可参考 `examples/toolcallid`。

#### 当 Tool 内部还要拉起子 Agent 时

这里要先区分两个概念：

- `tool_call_id`：模型发出的“这一条工具调用”的 ID
- `InvocationID` / `ParentInvocationID`：Agent 执行树里的父子调用关系

如果你的目标是“让 UI 把子 Agent 的输出挂到主 Agent 的某条工具调用下面”，
推荐把这两层信息都保留下来：

1. 用 `tool.ToolCallIDFromContext(ctx)` 取到当前 `tool_call_id`
2. 用 `agent.InvocationFromContext(ctx)` 取到父 Invocation
3. 用 `parentInv.Clone(...)` 创建子 Invocation
4. 把 `tool_call_id` 放进子 Invocation 的 `RunOptions.RuntimeState`
5. UI 侧同时使用：
   - `InvocationID` / `ParentInvocationID` 建立调用树
   - 你传下去的 `tool_call_id` 绑定“来源于哪条 tool_call”

示例（假设 `childAgent` 已经是一个可运行的子 Agent 实例）：

```go
const runtimeStateParentToolCallID = "display.parent_tool_call_id"
const defaultToolCallID = "default"

type delegateArgs struct {
    Message string `json:"message"`
}

func runChildAgentInsideTool(
    ctx context.Context,
    args delegateArgs,
) (string, error) {
    toolCallID, ok := tool.ToolCallIDFromContext(ctx)
    if !ok || toolCallID == "" {
        toolCallID = defaultToolCallID
    }

    parentInv, ok := agent.InvocationFromContext(ctx)
    if !ok || parentInv == nil {
        return "", errors.New("missing parent invocation")
    }

    childRunOptions := parentInv.RunOptions
    childRunOptions.RuntimeState = make(
        map[string]any,
        len(parentInv.RunOptions.RuntimeState)+1,
    )
    for key, value := range parentInv.RunOptions.RuntimeState {
        childRunOptions.RuntimeState[key] = value
    }
    childRunOptions.RuntimeState[
        runtimeStateParentToolCallID
    ] = toolCallID

    childInv := parentInv.Clone(
        agent.WithInvocationAgent(childAgent),
        agent.WithInvocationMessage(
            model.NewUserMessage(args.Message),
        ),
        agent.WithInvocationRunOptions(childRunOptions),
    )

    childCtx := agent.NewInvocationContext(ctx, childInv)
    eventCh, err := agent.RunWithPlugins(
        childCtx,
        childInv,
        childAgent,
    )
    if err != nil {
        return "", err
    }

    var final string
    for ev := range eventCh {
        if ev.Response != nil && len(ev.Response.Choices) > 0 {
            msg := ev.Response.Choices[0].Message
            if msg.Content != "" {
                final = msg.Content
            }
        }
        // Child events naturally carry:
        // - ev.InvocationID       == childInv.InvocationID
        // - ev.ParentInvocationID == parentInv.InvocationID
        //
        // Your renderer can build the invocation tree from these two
        // fields, and read runtimeStateParentToolCallID from the child
        // invocation path to attach that subtree back to the original
        // tool-call card.
    }

    return final, nil
}
```

写入前先复制一份 `RuntimeState`。`Invocation.Clone(...)`
不会对 `map` 做深拷贝；如果直接复用并写入，就会连父
Invocation 一起改掉。

子 Agent 内部如果还需要继续读取这个“来源 tool_call_id”，可以直接从
运行时状态里拿：

```go
toolCallID, ok := agent.GetRuntimeStateValueFromContext[string](
    ctx,
    runtimeStateParentToolCallID,
)
```

#### 推荐实践

- 如果你只需要“这一条工具调用”的标识，直接用
  `tool.ToolCallIDFromContext(ctx)`
- 如果你要表达“子 Agent 是谁触发的”，不要只依赖 `tool_call_id`
  一个字段；调用树请优先看 `InvocationID` / `ParentInvocationID`
- 如果 UI 还要回挂到“具体哪条工具卡片”，再把 `tool_call_id`
  通过 `RuntimeState` 或自定义事件元数据显式传下去
- 如果你用的是 `AgentTool`，框架已经会用 `Invocation.Clone(...)`
  维护父子 Invocation 关系；UI 侧通常已经能看到清晰的父子调用树。
  只有在你还想把子树额外绑定回某条 tool card 时，才需要再传
  `tool_call_id`

#### 一个容易忽略的细节

框架会在执行工具前把 `tool_call_id` 注入到 context。
但如果你的 `BeforeTool` 回调主动返回了一个全新的裸 `Context`
（没有保留原值），那后续工具代码里就拿不到这个 ID 了。

因此，如果你会在回调里替换 context，记得把已有的 context value
一并透传。

## 内置工具类型

### Tool 调用重试

当工具调用可能因为瞬时问题失败时，可以为它配置重试，例如：

- 下游网络波动；
- 临时超时；
- 外部服务偶发异常。

这项能力默认关闭。当前仅对 `CallableTool` 生效，`StreamableTool` 暂不支持。开启后，框架只会重试当前这次工具调用，不会重跑整个 Agent 或整轮 Graph 工作流。

### 基本配置

```go
policy := &tool.RetryPolicy{
    MaxAttempts:     3,
    InitialInterval: 200 * time.Millisecond,
    BackoffFactor:   2.0,
    MaxInterval:     2 * time.Second,
    Jitter:          true,
}
```

常用字段：

- `MaxAttempts`：总尝试次数，**包含第一次调用**。
- `InitialInterval`：第二次尝试前的初始等待时间。
- `BackoffFactor`：失败后退避倍数。
- `MaxInterval`：等待时间上限。
- `Jitter`：是否开启抖动。

### 默认判定规则

如果未提供 `RetryOn`，框架会使用 `tool.DefaultRetryOn(...)`。

默认规则比较保守，只会重试常见的瞬时错误，例如：

- `io.EOF`
- `io.ErrUnexpectedEOF`
- `net.Error` 中的 timeout / temporary 错误

像 `context.Canceled`、`context.DeadlineExceeded` 以及结果级失败，默认不会重试。

### 自定义重试条件

如果默认规则不够，可以通过 `RetryOn` 自定义判定逻辑。推荐先复用 `tool.DefaultRetryOn(...)`，再补充自己的条件：

```go
policy := &tool.RetryPolicy{
    MaxAttempts:     2,
    InitialInterval: 200 * time.Millisecond,
    BackoffFactor:   2.0,
    MaxInterval:     time.Second,
    RetryOn: func(ctx context.Context, info *tool.RetryInfo) (bool, error) {
        retry, err := tool.DefaultRetryOn(ctx, info)
        if err != nil || retry {
            return retry, err
        }
        if info.ResultError {
            return true, nil
        }
        return false, nil
    },
}
```

`tool.RetryInfo` 里会带上当前调用的信息，例如工具名、当前是第几次尝试、原始错误、结果级失败标记等，方便你在 `RetryOn` 中做判断。

### 在 LLMAgent 中启用

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{myTool}),
    llmagent.WithToolCallRetryPolicy(policy),
)
```

可运行示例：

- [examples/llmagent_tool_call_retry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/llmagent_tool_call_retry)

### 在 Graph 中启用

```go
sg.AddToolsNode(
    "tools",
    tools,
    graph.WithToolCallRetryPolicy(policy),
)
```

可运行示例：

- [examples/graph/tool_call_retry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/graph/tool_call_retry)

### DuckDuckGo 搜索工具

DuckDuckGo 工具基于 DuckDuckGo Instant Answer API，提供事实性、百科类信息搜索功能。

#### 基础用法

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"

// 创建 DuckDuckGo 搜索工具
searchTool := duckduckgo.NewTool()

// 集成到 Agent
searchAgent := llmagent.New("search-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{searchTool}))
```

#### 高级配置

```go
import (
    "net/http"
    "time"
    "trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

// 自定义配置
searchTool := duckduckgo.NewTool(
    duckduckgo.WithBaseURL("https://api.duckduckgo.com"),
    duckduckgo.WithUserAgent("my-app/1.0"),
    duckduckgo.WithHTTPClient(&http.Client{
        Timeout: 15 * time.Second,
    }),
)
```

### Claude Code ToolSet

`tool/claudecode` 提供了一组面向代码工作的 ToolSet，用于在框架内部暴露与 Claude Code 接近的工具接口。它覆盖文件读写、代码检索、命令执行和网页获取等能力，可以直接挂接到 `LLMAgent` 或其他运行时。如果你的目标是调用本地 Claude Code CLI，并消费 CLI 的执行轨迹与工具事件，请参考 [Claude Code Agent 使用指南](claudecode.md)。

从能力组成上看，`claudecode` 默认会提供一组代码工作流工具，包括 `Bash`、`TaskStop`、`TaskOutput`、`Read`、`Glob`、`Grep`、`WebFetch` 和 `WebSearch`。在非只读模式下，还会额外提供 `Write`、`Edit` 和 `NotebookEdit`。

下表列出了当前 `claudecode` 工具集中的主要工具及其用途：

| 工具名 | 说明 |
| --- | --- |
| `Bash` | 执行本地 Shell 命令。 |
| `TaskStop` | 停止由 `Bash` 以后台模式启动的任务。 |
| `TaskOutput` | 读取后台任务的当前输出或最终输出。 |
| `Read` | 读取文件内容。 |
| `Glob` | 按路径模式查找文件。 |
| `Grep` | 按内容搜索仓库。 |
| `WebFetch` | 抓取指定 URL 的页面内容。 |
| `WebSearch` | 进行开放式网页搜索。 |
| `Write` | 创建文件或用完整内容覆盖文件，仅在非只读模式下暴露。 |
| `Edit` | 对已有文本文件做局部替换，仅在非只读模式下暴露。 |
| `NotebookEdit` | 按 cell 粒度编辑 `.ipynb` 文件，仅在非只读模式下暴露。 |

#### 基本用法

```go
import (
	"log"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/claudecode"
)

toolSet, err := claudecode.NewToolSet(
	claudecode.WithBaseDir("."),
)
if err != nil {
	log.Fatal(err)
}
defer toolSet.Close()

agent := llmagent.New(
	"claude-style-agent",
	llmagent.WithToolSets([]tool.ToolSet{toolSet}),
)
```

`llmagent.WithToolSets(...)` 会以 ToolSet 形式接入这组工具；如果调用 `Tools()`，则会得到展开后的单个工具列表。

#### 常用配置

`tool/claudecode` 的配置重点围绕工作目录、只读模式和 Web 能力展开：

| Option | 说明 |
| --- | --- |
| `WithName(name)` | 覆盖 ToolSet 名称，默认值为 `claudecode`。 |
| `WithBaseDir(dir)` | 指定工具集的基础目录。文件、检索和命令执行都会以此为基准。 |
| `WithReadOnly(readOnly)` | 启用只读模式后，不再暴露 `Write`、`Edit`、`NotebookEdit`。 |
| `WithMaxFileSize(size)` | 限制单个文件可读取的最大尺寸。 |
| `WithWebFetchOptions(opts)` | 配置 `WebFetch` 的域名策略、超时与内容处理方式。 |
| `WithWebSearchOptions(opts)` | 配置 `WebSearch` 的后端、分页参数与请求选项。 |

`WithBaseDir` 定义了 `Read`、`Write`、`Edit`、`Glob`、`Grep` 等文件相关工具的工作范围，也决定了 `Bash` 的默认执行目录。启用只读模式后，工具集只保留读取、检索、命令执行和 Web 相关能力；关闭只读模式后，会额外暴露 `Write`、`Edit` 与 `NotebookEdit`。

## MCP Tools 协议工具

MCP（Model Context Protocol）是一个开放协议，标准化了应用程序向 LLM 提供上下文的方式。MCP 工具基于 JSON-RPC 2.0 协议，为 Agent 提供了与外部服务的标准化集成能力。

**MCP ToolSet 特点：**

- 🔗 **统一接口**：所有 MCP 工具都通过 `mcp.NewMCPToolSet()` 创建
- 🚀 **多种传输**：支持 STDIO、SSE、Streamable HTTP 三种传输方式
- 🔧 **工具过滤**：支持包含/排除特定工具
- ✅ **显式初始化**：通过 `(*mcp.ToolSet).Init(ctx)`，可以在应用启动阶段提前发现 MCP 连接/工具加载错误并快速失败

### 基本用法

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/mcp"

// 创建 MCP 工具集（以 STDIO 为例）
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",           // 传输方式
        Command:   "go",              // 执行命令
        Args:      []string{"run", "./stdio_server/main.go"},
        Timeout:   10 * time.Second,
    },
    mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter("echo", "add")), // 可选：工具过滤
)

// （可选但推荐）显式初始化 MCP：建立连接 + 初始化会话 + 列工具
if err := mcpToolSet.Init(ctx); err != nil {
    log.Fatalf("初始化 MCP 工具集失败: %v", err)
}

// 集成到 Agent
agent := llmagent.New("mcp-assistant",
    llmagent.WithModel(model),
    llmagent.WithToolSets([]tool.ToolSet{mcpToolSet}))
```

### ToolSet 生命周期与所有权

`ToolSet` 接口里显式提供了 `Close()`，这意味着它持有的连接、会话和缓存
等资源需要由**创建它的一方**负责释放。

几个容易混淆的边界：

- `llmagent.WithToolSets(...)` 只是把 `ToolSet` 挂到 Agent 上使用，
  **不会**转移其所有权。
- `LLMAgent` 的 `AddToolSet(...)`、`RemoveToolSet(...)`、
  `SetToolSets(...)` 只会更新 Agent 当前暴露的工具集合，
  **不会**自动调用旧 `ToolSet` 的 `Close()`。
- `runner.NewRunner(...)` 和 `runner.NewRunnerWithAgentFactory(...)`
  也不会因为 Agent 使用了某个 `ToolSet`，就在 `Runner.Close()` 时
  自动回收它。

推荐的使用方式：

- **长生命周期 ToolSet**：在应用启动时创建并可选执行 `Init(ctx)`，
  多次请求复用；应用退出时统一 `Close()`。
- **按请求创建的 ToolSet**：只在当前 run 内使用；当前 run 结束后由
  调用方显式清理。

如果你只是希望 ToolSet 在每次执行时重新获取**最新工具列表**，优先使用
`llmagent.WithRefreshToolSetsOnRun(true)`。这会在每次 run 前重新调用
`ToolSet.Tools(ctx)`，但**不会**为你重建或关闭 `ToolSet` 实例本身。

### 传输方式配置

MCP ToolSet 通过 `Transport` 字段支持三种传输方式：

#### 1. STDIO 传输

通过标准输入输出与外部进程通信，适用于本地脚本和命令行工具。

```go
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",
        Command:   "python",
        Args:      []string{"-m", "my_mcp_server"},
        Timeout:   10 * time.Second,
    },
)
if err := mcpToolSet.Init(ctx); err != nil {
    return fmt.Errorf("初始化 STDIO MCP 工具集失败: %w", err)
}
```

#### 2. SSE 传输

使用 Server-Sent Events 进行通信，支持实时数据推送和流式响应。

```go
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
        Headers: map[string]string{
            "Authorization": "Bearer your-token",
        },
    },
)
if err := mcpToolSet.Init(ctx); err != nil {
    return fmt.Errorf("初始化 SSE MCP 工具集失败: %w", err)
}
```

#### 3. Streamable HTTP 传输

使用标准 HTTP 协议进行通信，支持普通 HTTP 和流式响应。

```go
mcpToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "streamable_http",  // 注意：使用完整名称
        ServerURL: "http://localhost:3000/mcp",
        Timeout:   10 * time.Second,
    },
)
if err := mcpToolSet.Init(ctx); err != nil {
    return fmt.Errorf("初始化 Streamable MCP 工具集失败: %w", err)
}
```

### 会话重连支持

MCP ToolSet 支持自动会话重连，当服务器重启或会话过期时自动恢复连接。

```go
// SSE/Streamable HTTP 传输支持会话重连
sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
    },
    mcp.WithSessionReconnect(3), // 启用会话重连，最多尝试3次
)
```

**重连特性：**

- 🔄 **自动重连**：检测到连接断开或会话过期时自动重建会话
- 🎯 **独立重试**：每次工具调用独立计数，不会因早期失败影响后续调用
- 🛡️ **保守策略**：仅针对明确的连接/会话错误触发重连，避免配置错误导致的无限循环

### MCP 工具的动态发现与更新（LLMAgent 配置项）

对于 MCP 工具集，服务器端的工具列表是可以变化的（例如在运行
过程中新增了一个 MCP 工具）。如果希望 LLMAgent 在**每次调用**
时自动看到最新的工具列表，可以在使用 `WithToolSets` 的同时，
开启 `llmagent.WithRefreshToolSetsOnRun(true)`。

#### LLMAgent 配置示例

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// 1. 创建 MCP 工具集（可以是 STDIO、SSE 或 Streamable HTTP）
mcpToolSet := mcp.NewMCPToolSet(connectionConfig)

// 2. 创建 LLMAgent，并开启 ToolSets 的自动刷新
agent := llmagent.New(
    "mcp-assistant",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
    llmagent.WithToolSets([]tool.ToolSet{mcpToolSet}),
    llmagent.WithRefreshToolSetsOnRun(true),
)
```

当启用 `WithRefreshToolSetsOnRun(true)` 时：

- LLMAgent 在每次执行前构造工具列表时，会再次调用
  `ToolSet.Tools(ctx)`，其中 `ctx` 为本次执行的上下文；
- 如果 MCP 服务器新增或删除了工具，该 Agent **下一次执行** 时，
  会自动使用更新后的工具列表。
- 如果你在“非执行期”获取工具（例如直接调用 `agent.Tools()`），
  LLMAgent 会使用 `context.Background()`。

这个配置项的侧重点是**动态发现工具**。如果你还需要基于
`context.Context` 在初始化或工具发现阶段做更细粒度的控制，同时又不希望
在每次执行时刷新工具列表，可以参考 `examples/mcptool/http_headers`
示例，手动调用 `toolSet.Tools(ctx)`，然后配合 `WithTools` 使用。

常见误区：

- `WithRefreshToolSetsOnRun(true)` 刷新的是**工具列表**，不是 `ToolSet`
  实例本身；它不会自动新建、替换或关闭 `ToolSet`。
- `tools/call` 会使用本次 run 的上下文，但如果你在执行期外直接调用
  `agent.Tools()`，ToolSet 看到的是 `context.Background()`。
- 如果你需要让 `initialize/tools/list` 也严格使用某个自定义上下文
  （例如每次请求不同的认证头、追踪字段），更稳妥的做法通常是手动
  `toolSet.Tools(ctx)`，再通过 `WithTools(...)` 注入。

### MCP Broker（按需发现 MCP）

除了直接把远端 MCP 工具展开成一等 Tool 之外，框架还提供了另一种
接入方式：`tool/mcpbroker`。

`mcpbroker` 的核心思路是：

- 不在一开始把远端 MCP 的全部工具都暴露给模型
- 先只暴露少量 broker 工具
- 让模型在需要时再逐步发现和调用远端 MCP 能力

这类模式更适合**长尾工具很多、但单次请求只会命中少量工具**的场景。

#### 什么时候用 MCP Broker

推荐使用 `mcpbroker` 的场景：

- 某个 MCP 服务工具很多，不希望每轮都把完整工具表面塞给模型
- 某些能力是“储备能力”或“长尾能力”，并不是高频调用
- 需要通过 Skill、System Prompt、User Prompt 等增量信息，动态连接某个远端 MCP endpoint
- 希望用较小、较稳定的初始工具面换取更低的上下文压力

更适合继续使用 `mcp.NewMCPToolSet()` 的场景：

- 高频、稳定、已知能力
- 希望把远端工具直接升格成一等 Tool
- 更看重调用路径更短、约束更强、工具调用成功率更高

这两种方式可以组合使用：

- 高频热点能力继续使用 `MCP ToolSet`
- 低频长尾能力放进 `mcpbroker`

#### 与 MCP ToolSet 的区别

两者的主要区别是“工具暴露时机”不同：

- `MCP ToolSet`
  - 在初始化或运行时先 `initialize + tools/list`
  - 把远端每个 MCP tool 直接变成 Agent 可见 Tool
- `mcpbroker`
  - 初始只暴露 4 个 broker 工具
  - 模型先发现 server，再发现 tool，再检查指定 tool 的 schema，最后再调用

可以把它理解为：

- `MCP ToolSet`：直接挂远端工具
- `mcpbroker`：按需发现远端工具

典型 trade-off：

- `MCP ToolSet`：更快、更强约束，但工具面更大
- `mcpbroker`：更省上下文、更适合长尾与动态能力，但多一步 discovery，整体可能更慢

#### 基本接入方式

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcpbroker"
)

broker := mcpbroker.New(
    mcpbroker.WithServers(map[string]mcp.ConnectionConfig{
        "local_stdio_code": {
            Transport:   "stdio",
            Command:     "go",
            Args:        []string{"run", "./stdio_server/main.go"},
            Timeout:     10 * time.Second,
            Description: "Project management, documentation, and calendar tools.",
        },
    }),
    mcpbroker.WithAllowAdHocHTTP(true),
)

agent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(broker.Tools()),
)
```

#### Server Description（服务描述）

`ConnectionConfig` 上的 `Description` 字段为 MCP server 提供一段能力摘要，
帮助模型在 `mcp_list_servers` 阶段判断该去哪个 server 探索。
`mcp_list_servers` 的返回值会包含这个 description：

```json
{
  "servers": [
    {
      "name": "local_stdio_code",
      "transport": "stdio",
      "description": "Project management, documentation, and calendar tools."
    }
  ]
}
```

这类似于 OpenAI tool namespace 的 `description`：模型在第一步
`mcp_list_servers` 时就能根据描述决定"去哪个 server 看"，而不需要
逐个 `mcp_list_tools` 试探。server 数量越多、名字越不直观时，
description 的价值越大。

这个字段是可选的。如果不填，输出中不会出现 `description`，对现有行为
没有任何影响。

当前 `mcpbroker` 是**工具层接入**，不会像 `Skill` 那样自动向
`System Prompt` 注入策略提示。如果你希望模型更稳定地理解：

- 什么时候先列 server
- 什么时候先 `mcp_list_tools`
- 什么时候再 `mcp_inspect_tools`
- 什么时候再 `mcp_call`

通常仍然建议在业务侧 instruction 里补少量高层 guidance。

#### 模型可见的 4 个工具

当前模型只会看到这 4 个工具：

- `mcp_list_servers`
- `mcp_list_tools`
- `mcp_inspect_tools`
- `mcp_call`

推荐的调用顺序通常是：

1. `mcp_list_servers()`：查看 broker 已知的命名 MCP server
2. `mcp_list_tools(selector)`：查看某个 server 或远端 URL 的轻量工具目录
3. `mcp_inspect_tools(selector, tools[])`：只展开指定工具的 schema
4. `mcp_call(selector, arguments)`：调用具体 MCP tool

这意味着模型不再一开始就看到完整远端工具集，而是先通过 broker
渐进式探索。

#### Selector 心智模型

`mcpbroker` 的核心输入不是 `server_name + tool_name + url` 的混合对象，
而是统一使用 `selector`：

- 在 `mcp_list_tools` 中：
  - 命名 server：`local_stdio_code`
  - 动态 URL：`https://example.com/mcp`
- 在 `mcp_call` 中：
  - 命名 tool：`local_stdio_code.add`
  - 动态 URL tool：`https://example.com/mcp.add`

如果某个 ad-hoc HTTP endpoint 会让点分隔 selector 产生歧义，也支持：

- `https://example.com/mcp#tool=add`

真正的 MCP tool 参数统一放在：

- `mcp_call(..., arguments={...})`

而不是放在顶层字段里。

#### 渐进式发现方式

- 先用 `mcp_list_tools` 获取轻量摘要
- 只有准备调用某个具体 tool 时，再用 `mcp_inspect_tools`
  只展开该 tool 的 schema

这和“先把完整 schema 全部塞给模型”相比，更适合上下文预算紧张的场景。

#### 动态 URL 与 Skill 场景

`mcpbroker` 支持 ad-hoc HTTP MCP：

```go
broker := mcpbroker.New(
    mcpbroker.WithAllowAdHocHTTP(true),
)
```

`WithAllowAdHocHTTP(true)` 会让 HTTP(S) MCP 的 `selector` 成为模型可控输入。
生产环境里，建议先在业务侧对 URL、域名和路径做 allowlist 或其它校验，
再把 ad-hoc HTTP 当成可信集成路径使用。

这类动态连接通常需要先有一个“信息来源”告诉模型：

- 这个 MCP endpoint 存在
- 它大概能做什么
- 该用什么 URL 去连接

这个信息来源可以是：

- System Prompt
- User Prompt
- Skill
- 知识库

也就是说，`mcpbroker` 解决的是“如何连、如何看、如何调”，
而不是“模型为什么会想到去连这个 MCP”。

这也让 `mcpbroker` 很适合与 Skill 配合使用。有些 Skill 只在自身
场景下需要某个专用 MCP 能力，这类 MCP 工具不一定要在整个会话中
长期作为全局工具暴露；还有些 Skill 会在加载后提供一个增量出现的
远端 MCP endpoint。此时 Skill 可以负责提供“这个 MCP 存在、能做什么、
该连哪个 URL”的信息来源，而 `mcpbroker` 负责动态连接以及渐进式暴露
工具和 schema。

完整示例可参考：

- `examples/mcpbroker/basic`

其中包含：

- 本地命名 MCP server
- Skill 提供远端 streamable HTTP MCP endpoint
- 模型通过 `skill_load -> mcp_list_tools -> mcp_inspect_tools -> mcp_call` 动态连接远端 MCP

#### 鉴权 Hook（Per-Run Header 注入）

对于 HTTP 型 MCP，`mcpbroker` 还提供两类运行时扩展点：

- `WithHTTPHeaderInjector(...)`
- `WithErrorInterceptor(...)`

适用场景：

- 不希望让模型直接携带 `Authorization`
- 需要根据当前用户、租户、workspace，在每次调用时动态注入 token
- 希望在远端返回 401/403 时，由业务层把底层错误包装成更友好的错误信息

示例：

```go
broker := mcpbroker.New(
    mcpbroker.WithAllowAdHocHTTP(true),
    mcpbroker.WithHTTPHeaderInjector(func(ctx context.Context, req *mcpbroker.HeaderInjectRequest) (map[string]string, error) {
        token, _ := resolveUserTokenFromContext(ctx, req.BaseURL)
        if token == "" {
            return nil, nil
        }
        return map[string]string{
            "Authorization": "Bearer " + token,
        }, nil
    }),
    mcpbroker.WithErrorInterceptor(func(ctx context.Context, req *mcpbroker.BrokerErrorRequest) (*mcpbroker.BrokerErrorDecision, error) {
        if isUnauthorized(req.Err) {
            return &mcpbroker.BrokerErrorDecision{
                Handled:   true,
                WrapError: fmt.Errorf("当前用户需要先在宿主系统完成授权，然后再重试"),
            }, nil
        }
        return nil, nil
    }),
)
```

这里的设计重点是：

- 模型只负责选择 `selector`
- 业务代码负责从 `ctx` 中识别当前用户，并注入 HTTP Header
- `mcpbroker` 本身不管理复杂的 OAuth session 状态机

开启 `WithAllowAdHocHTTP(true)` 后，URL selector 可能来自模型可见上下文。
生产环境中应在 `HTTPHeaderInjector` 里基于 `req.IsAdHoc` 和 `req.BaseURL`
做 allowlist / URL 校验，再返回敏感 Header。

完整示例可参考：

- `examples/mcpbroker/authhooks`

## Agent 工具 (AgentTool)

AgentTool 允许把一个现有的 Agent 以工具的形式暴露给上层 Agent 使用。相比普通函数工具，AgentTool 的优势在于：

- ✅ 复用：将复杂 Agent 能力作为标准工具复用
- 🌊 流式：可选择将子 Agent 的流式事件“内联”转发到父流程
- 🧭 控制：通过选项控制是否跳过工具后的总结补全、是否进行内部转发

### 基本用法

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// 1) 定义一个可复用的子 Agent（可配置为流式）
mathAgent := llmagent.New(
    "math-specialist",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction("你是数学专家..."),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
)

// 2) 包装为 Agent 工具
mathTool := agenttool.NewTool(
    mathAgent,
    agenttool.WithSkipSummarization(false), // 可选，默认 false，当设置为 true 时会跳过外层模型总结，在 tool.response 后直接结束本轮
    agenttool.WithStreamInner(true),        // 开启：把子 Agent 的流式事件转发给父流程
    agenttool.WithInnerTextMode(agenttool.InnerTextModeExclude), // 隐藏子 Agent 正文，仅保留内部进度
)

// 3) 在父 Agent 中使用该工具
parent := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
    llmagent.WithTools([]tool.Tool{mathTool}),
)
```

### 流式内部转发详解

当 `WithStreamInner(true)` 时，AgentTool 会把子 Agent 在运行时产生的事件直接转发到父流程的事件流中：

- 转发的事件本质是子 Agent 里的 `event.Event`，包含增量内容（`choice.Delta.Content`）
- 为避免重复，子 Agent 在结束时产生的“完整大段内容”不会再次作为转发事件打印；但会被聚合到最终 `tool.response` 的内容里，供下一次 LLM 调用作为工具消息使用
- UI 层建议：展示“转发的子 Agent 增量内容”，但默认不重复打印最终聚合的 `tool.response` 内容（除非用于调试）
- 通过 `WithInnerTextMode(agenttool.InnerTextModeExclude)`，你可以保留内部 tool 进度，同时隐藏子 Agent 的 assistant 正文。这在外层协调者还会继续总结时尤其有用。

示例：仅在需要时显示工具片段，避免重复输出

```go
if ev.Response != nil && ev.Object == model.ObjectTypeToolResponse {
    // 工具响应（包含聚合后的内容），默认不打印，避免和子 Agent 转发的内容重复
    // ...仅在调试或需要展示工具细节时再打印
}

// 子 Agent 转发的流式增量（作者不是父 Agent）
if ev.Author != parentName && ev.Response != nil &&
    len(ev.Response.Choices) > 0 {
    if delta := ev.Response.Choices[0].Delta.Content; delta != "" {
        fmt.Print(delta)
    }
}
```

### 选项说明

- WithSkipSummarization(bool)：

  - false（默认）：允许在工具结果后继续一次 LLM 调用进行总结/回答
  - true：外层 Flow 在 `tool.response` 后直接结束本轮（不再额外总结）

- WithStreamInner(bool)：

  - true：把子 Agent 的事件直接转发到父流程（强烈建议父/子 Agent 都开启 `GenerationConfig{Stream: true}`）
  - false：按“仅可调用工具”处理，不做内部事件转发

- WithInnerTextMode(InnerTextMode)：

  - `InnerTextModeInclude`：实际默认行为，开启内部转发时继续展示子 Agent 的 assistant 文本
  - `InnerTextModeExclude`：隐藏子 Agent 的 assistant 文本，但继续保留内部 tool call、tool.done，以及聚合后的最终工具响应

- WithHistoryScope(HistoryScope)：
  - `HistoryScopeIsolated`（默认）：保持子调用完全隔离，只读取本次工具参数（不继承父历史）。
  - `HistoryScopeParentBranch`：通过分层过滤键 `父键/子名-UUID（Universally Unique Identifier，通用唯一识别码）` 继承父会话历史；内容处理器会基于前缀匹配纳入父事件，同时子事件仍写入独立子分支。典型场景：基于上一轮产出进行“编辑/优化/续写”。

示例：

```go
child := agenttool.NewTool(
    childAgent,
    agenttool.WithSkipSummarization(false),
    agenttool.WithStreamInner(true),
    agenttool.WithInnerTextMode(agenttool.InnerTextModeExclude),
    agenttool.WithHistoryScope(agenttool.HistoryScopeParentBranch),
)
```

### 注意事项

- 事件完成信号：工具响应事件会被标记 `RequiresCompletion=true`，Runner 会自动发送完成信号，无需手工处理
- 内容去重：如果已转发子 Agent 的增量内容，默认不要再把最终 `tool.response` 的聚合内容打印出来
- “只看进度”体验：当你希望用户看到内部进度、但不想重复看到子 Agent 正文时，可组合使用 `WithStreamInner(true)` 和 `WithInnerTextMode(agenttool.InnerTextModeExclude)`
- 模型兼容性：一些模型要求工具调用后必须跟随工具消息，AgentTool 已自动填充聚合后的工具内容满足此要求
- `WithSkipSummarization(true)` 只会跳过额外的外层总结型 LLM 调用，不会把 `tool.response` 变成 assistant final response；如果你需要真正的终止信号，仍应持续消费到 `runner.completion`

## 工具集成与使用

### 创建 Agent 与工具集成

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
    "trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// 创建函数工具
calculatorTool := function.NewFunctionTool(calculator,
    function.WithName("calculator"),
    function.WithDescription("执行基础数学运算"))

timeTool := function.NewFunctionTool(getCurrentTime,
    function.WithName("current_time"),
    function.WithDescription("获取当前时间"))

// 创建内置工具
searchTool := duckduckgo.NewTool()

// 创建 MCP 工具集（不同传输方式的示例）
stdioToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",
        Command:   "python",
        Args:      []string{"-m", "my_mcp_server"},
        Timeout:   10 * time.Second,
    },
)

sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "http://localhost:8080/sse",
        Timeout:   10 * time.Second,
    },
)

streamableToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "streamable_http",
        ServerURL: "http://localhost:3000/mcp",
        Timeout:   10 * time.Second,
    },
)

// 创建 Agent 并集成所有工具
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("你是一个有帮助的AI助手，可以使用多种工具协助用户"),
    // 添加单个工具（Tool 接口）
    llmagent.WithTools([]tool.Tool{
        calculatorTool, timeTool, searchTool,
    }),
    // 添加工具集（ToolSet 接口）
    llmagent.WithToolSets([]tool.ToolSet{
        stdioToolSet, sseToolSet, streamableToolSet,
    }),
)
```

### MCP 工具过滤器

MCP 工具集支持在创建时过滤工具。推荐使用统一的 `tool.FilterFunc` 接口：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// ✅ 推荐：使用统一的过滤接口
includeFilter := tool.NewIncludeToolNamesFilter("get_weather", "get_news", "calculator")
excludeFilter := tool.NewExcludeToolNamesFilter("deprecated_tool", "slow_tool")

// 应用过滤器
toolSet := mcp.NewMCPToolSet(
    connectionConfig,
    mcp.WithToolFilterFunc(includeFilter),
)
```

### 运行时工具过滤

- 方式一：运行时工具过滤允许在每次 `runner.Run` 调用时动态控制工具可用性，无需修改 Agent 配置。这是一个"软约束"机制，用于优化 token 消耗和实现基于角色的工具访问控制。针对所有agent生效
- 方式二：通过`llmagent.WithToolFilter`配置运行时过滤function, 只对当前agent生效

**核心特性：**

- 🎯 **Per-Run 控制**：每次调用独立配置，不影响 Agent 定义
- 💰 **成本优化**：减少发送给 LLM 的工具描述，降低 token 消耗
- 🛡️ **智能保护**：框架工具（`transfer_to_agent`、`knowledge_search`、`agentic_knowledge_search`、可选的 `await_user_reply`）自动保留，永不被过滤
- 🔧 **灵活定制**：支持内置过滤器和自定义 FilterFunc

#### Tool Search（自动工具筛选）

除了“规则过滤（Tool Filter）”，框架还提供 **Tool Search**：在每次主模型调用前，先做一次“工具选择”，把**候选工具集压缩到 TopK**（例如 3 个），再交给主模型执行，从而进一步降低 token（尤其是 PromptTokens）。

需要注意的 trade-off：

- **耗时**：Tool Search 会引入额外步骤（额外 LLM 调用、以及/或 embedding + 向量检索），端到端耗时可能增加。
- **Prompt Caching**：每轮传给主模型的工具列表会变化，可能降低部分平台的 prompt caching 命中率。

和 Tool Filter 的区别：

- **Tool Filter**：你（或业务）通过规则决定“允许/禁止哪些工具”（访问控制/成本控制），更偏策略与安全。
- **Tool Search**：框架根据“当前用户问题”自动挑选相关工具，更偏自动化与成本优化。

它们可以组合使用：先用 Tool Filter 做权限/白名单，再用 Tool Search 在剩余工具里做 TopK 选择。

**两种策略：**

- **LLM Search**：把候选工具列表（name + description）拼进 prompt，让 LLM 直接输出“应该使用哪些工具”。
  - 优点：不依赖向量库；实现简单。
  - 缺点：每轮都会把工具列表放进 prompt，开销随工具数量/描述长度近似线性增长。
- **Knowledge Search**：先用 LLM 做 query rewrite，再用 embedding + 向量检索做语义匹配。
  - 优点：不需要每轮把完整工具列表塞进 LLM；并且 **tool embedding 会在同一 `ToolKnowledge` 实例内缓存**，后续轮/后续请求可以复用。
  - 注意：每轮仍需要对 query 做 embedding（固定开销之一）。

##### 基本用法（LLM Search）

Tool Search 既可以作为 Runner plugin 使用，也可以作为单个 Agent 的
callback 使用。

**方案 A：Runner Plugin**

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

ts, err := toolsearch.New(modelInstance,
    toolsearch.WithMaxTools(3),
    toolsearch.WithFailOpen(), // 可选：search 失败时退回到“全部工具可用”
)
if err != nil { /* handle */ }

ag := llmagent.New("assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(allTools), // 仍然注册“全量工具”，Tool Search 会挑 TopK
)

r := runner.NewRunner("app", ag,
    runner.WithPlugins(ts),
)
```

**方案 B：Per-Agent BeforeModel Callback**

通过 `modelCallbacks.RegisterBeforeModel(...)` 注册 Tool Search 的 callback
（会在主模型调用前自动重写 `req.Tools`）：

```go
	import (
	    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	    "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	    "trpc.group/trpc-go/trpc-agent-go/model"
	)

modelCallbacks := model.NewCallbacks()
tc, err := toolsearch.New(modelInstance,
    toolsearch.WithMaxTools(3),
    toolsearch.WithFailOpen(), // 可选：search 失败时退回到“全部工具可用”
)
if err != nil { /* handle */ }
modelCallbacks.RegisterBeforeModel(tc.Callback())

agent := llmagent.New("assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(allTools), // 仍然注册“全量工具”，Tool Search 会在每次调用前挑 TopK
    llmagent.WithModelCallbacks(modelCallbacks),
)
```

##### 基本用法（Knowledge Search）

需要先创建 `ToolKnowledge`（embedding + vector store），再通过 `toolsearch.WithToolKnowledge(...)` 启用 Knowledge Search：

```go
	import (
	    "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	)

toolKnowledge, err := toolsearch.NewToolKnowledge(
    openaiembedder.New(openaiembedder.WithModel(openaiembedder.ModelTextEmbedding3Small)),
    toolsearch.WithVectorStore(vectorinmemory.New()),
)
if err != nil { /* handle */ }

tc, err := toolsearch.New(modelInstance,
    toolsearch.WithMaxTools(3),
    toolsearch.WithToolKnowledge(toolKnowledge),
    toolsearch.WithFailOpen(),
)
if err != nil { /* handle */ }
modelCallbacks.RegisterBeforeModel(tc.Callback())
```

##### Token 统计（可选）

Tool Search 的 token usage 会写入 context，可用于打点与成本分析：

```go
import "trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"

if usage, ok := toolsearch.ToolSearchUsageFromContext(ctx); ok && usage != nil {
    // usage.PromptTokens / usage.CompletionTokens / usage.TotalTokens
}
```

#### 基本用法

**1. 排除特定工具（Exclude Filter）**

使用黑名单方式排除不需要的工具：

```go
import "trpc.group/trpc-go/trpc-agent-go/tool"

// 排除 text_tool，其他工具都可用
filter := tool.NewExcludeToolNamesFilter("text_tool", "dangerous_tool")
eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)
```

**2. 只允许特定工具（Include Filter）**

使用白名单方式只允许指定的工具：

```go
// 方式一：
// 只允许使用计算器和时间工具
filter := tool.NewIncludeToolNamesFilter("calculator", "time_tool")
eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)

// 方式二：
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("你是一个有帮助的AI助手，可以使用多种工具协助用户"),
    // 添加单个工具（Tool 接口）
    llmagent.WithTools([]tool.Tool{
        calculatorTool, timeTool, searchTool,
    }),
    // 添加工具集（ToolSet 接口）
    llmagent.WithToolSets([]tool.ToolSet{
        stdioToolSet, sseToolSet, streamableToolSet,
    }),
    llmagent.WithToolFilter(filter),
)
```

**3. 自定义过滤逻辑（Custom FilterFunc）**

实现自定义过滤函数以支持复杂的过滤逻辑：

```go
// 方式一：
// 自定义过滤函数：只允许名称以 "safe_" 开头的工具
filter := func(ctx context.Context, t tool.Tool) bool {
    declaration := t.Declaration()
    if declaration == nil {
        return false
    }
    return strings.HasPrefix(declaration.Name, "safe_")
}

eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)

// 方式二：
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithInstruction("你是一个有帮助的AI助手，可以使用多种工具协助用户"),
    // 添加单个工具（Tool 接口）
    llmagent.WithTools([]tool.Tool{
        calculatorTool, timeTool, searchTool,
    }),
    // 添加工具集（ToolSet 接口）
    llmagent.WithToolSets([]tool.ToolSet{
        stdioToolSet, sseToolSet, streamableToolSet,
    }),
    llmagent.WithToolFilter(filter),
)
```

**4. Agent 粒度过滤（Per-Agent Filtering）**

通过 `agent.InvocationFromContext` 实现不同 Agent 使用不同工具：

```go
// 为不同 Agent 定义允许的工具
agentAllowedTools := map[string]map[string]bool{
    "math-agent": {
        "calculator": true,
    },
    "time-agent": {
        "time_tool": true,
    },
}

// 自定义过滤函数：根据当前 Agent 名称过滤
filter := func(ctx context.Context, t tool.Tool) bool {
    declaration := t.Declaration()
    if declaration == nil {
        return false
    }
    toolName := declaration.Name

    // 从 context 获取当前 Agent 信息
    inv, ok := agent.InvocationFromContext(ctx)
    if !ok || inv == nil {
        return true // fallback: 允许所有工具
    }

    agentName := inv.AgentName

    // 检查该工具是否在当前 Agent 的允许列表中
    allowedTools, exists := agentAllowedTools[agentName]
    if !exists {
        return true // fallback: 允许所有工具
    }

    return allowedTools[toolName]
}

eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)
```

**完整示例：** 参见 `examples/toolfilter/` 目录

#### 智能过滤机制

框架会自动区分**用户工具**和**框架工具**，只过滤用户工具：

| 工具分类     | 包含的工具                                                                                             | 是否被过滤            |
| ------------ | ------------------------------------------------------------------------------------------------------ | --------------------- |
| **用户工具** | 通过 `WithTools` 注册的工具<br>通过 `WithToolSets` 注册的工具                                          | ✅ 受过滤控制         |
| **框架工具** | `transfer_to_agent`（多 Agent 协调）<br>`knowledge_search`（知识库检索）<br>`agentic_knowledge_search`<br>`await_user_reply`（开启后的一次性追问路由） | ❌ 永不过滤，自动保留 |

**示例：**

```go
// Agent 注册了多个工具
agent := llmagent.New("assistant",
    llmagent.WithTools([]tool.Tool{
        calculatorTool,  // 用户工具
        textTool,        // 用户工具
    }),
    llmagent.WithSubAgents([]agent.Agent{subAgent1, subAgent2}), // 自动添加 transfer_to_agent
    llmagent.WithKnowledge(kb),                                   // 自动添加 knowledge_search
    llmagent.WithAwaitUserReplyTool(true),                        // 自动添加 await_user_reply
)

// 运行时过滤：只允许 calculator
filter := tool.NewIncludeToolNamesFilter("calculator")
runner.Run(ctx, userID, sessionID, message,
    agent.WithToolFilter(filter),
)

// 实际发送给 LLM 的工具：
// ✅ calculator        - 用户工具，在允许列表中
// ❌ textTool          - 用户工具，被过滤
// ✅ transfer_to_agent - 框架工具，自动保留
// ✅ knowledge_search  - 框架工具，自动保留
// ✅ await_user_reply  - 框架工具，自动保留
```

#### 用 `await_user_reply` 处理跨轮追问

`await_user_reply` 是一个可选框架工具。当某个 Agent 可能向用户补问信息，并且
你希望下一条用户消息继续回到这个 Agent 时，可以开启
`llmagent.WithAwaitUserReplyTool(true)`。

它需要和 `runner.WithAwaitUserReplyRouting(true)` 搭配使用：

```go
profileAgent := llmagent.New("profile-agent",
    llmagent.WithAwaitUserReplyTool(true),
    llmagent.WithInstruction(`
如果你必须向用户补一个缺失字段，先调用 await_user_reply，
再提出问题。
`),
)

r := runner.NewRunner(
    "crm-app",
    profileAgent,
    runner.WithAwaitUserReplyRouting(true),
)
```

这条路由是一次性的：Runner 会在下一条用户消息到来时消费它，然后自动清掉。

#### 注意事项

⚠️ **安全提示：** 运行时工具过滤是"软约束"，主要用于优化和用户体验。工具内部仍需实现自己的鉴权逻辑：

### 手动执行工具（中断 tool_calls）

默认情况下，当模型返回 `tool_calls` 时，框架会自动执行工具，然后把工具结果再发回给模型继续推理。

在一些系统里，你可能希望由调用方（例如客户端、上游服务，或外部工具运行时，例如
Model Context Protocol (MCP)）来执行工具。此时可以使用
`agent.WithToolExecutionFilter(...)` 来中断工具的自动执行。

**核心区别：**

- `agent.WithToolFilter(...)` 控制**工具可见性**（模型能看到/能调用哪些工具）
- `agent.WithToolExecutionFilter(...)` 控制**工具执行**（模型请求后，框架是否自动执行）

#### 基本流程

1. 使用 `WithToolExecutionFilter` 发起一次 `runner.Run`，让框架**不执行**指定工具
2. 从事件里读取模型返回的 `tool_calls`
3. 调用方在外部执行工具
4. 通过 `role=tool` 的消息把结果回填，模型继续输出最终答案

```go
execFilter := tool.NewExcludeToolNamesFilter("external_search")

// 第一步：模型会返回 tool_calls，但工具不会被框架执行。
ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("search ..."),
    agent.WithToolExecutionFilter(execFilter),
)

// 第二步：从事件里提取 tool_call_id + arguments（此处省略）。
toolCallID := "call_123"
toolResultJSON := `{"status":"ok","data":"..."}`

// 第三/四步：用 role=tool 回填工具结果，模型继续输出。
toolMsg := model.NewToolMessage(toolCallID, "external_search", toolResultJSON)
ch, err = r.Run(ctx, userID, sessionID, toolMsg,
    agent.WithToolExecutionFilter(execFilter),
)
```

**完整示例：** `examples/toolinterrupt/`

```go
func sensitiveOperation(ctx context.Context, req Request) (Result, error) {
    // ✅ 必须：工具内部鉴权
    if !hasPermission(ctx, req.UserID, "sensitive_operation") {
        return nil, fmt.Errorf("permission denied")
    }

    // 执行操作
    return performOperation(req)
}
```

**原因：** LLM 可能从上下文或记忆中知道工具的存在和用法，并尝试调用。工具过滤减少了这种可能性，但不能完全防止。

### 并行工具执行

```go
// 启用并行工具执行（可选，用于性能优化）
agent := llmagent.New("ai-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(tools),
    llmagent.WithToolSets(toolSets),
    llmagent.WithEnableParallelTools(true), // 启用并行执行
)
```

Graph 工作流下也可以在工具节点开启并行：

```go
stateGraph.AddToolsNode("tools", tools, graph.WithEnableParallelTools(true))
```

**并行执行效果：**

```bash
# 并行执行（启用时）
Tool 1: get_weather     [====] 50ms
Tool 2: get_population  [====] 50ms
Tool 3: get_time       [====] 50ms
总时间: ~50ms（同时执行）

# 串行执行（默认）
Tool 1: get_weather     [====] 50ms
Tool 2: get_population       [====] 50ms
Tool 3: get_time                  [====] 50ms
总时间: ~150ms（依次执行）
```

### 运行时 ToolSet 动态管理

`WithToolSets` 是一种**静态配置方式**：在创建 Agent 时一次性注入 ToolSet。很多实际场景下，你希望在**运行时动态增删 ToolSet**，而不必重建 Agent。

LLMAgent 提供了三个与 ToolSet 相关的运行时方法：

- `AddToolSet(toolSet tool.ToolSet)` —— 按 `ToolSet.Name()` 添加或替换同名 ToolSet
- `RemoveToolSet(name string) bool` —— 按名称移除所有同名 ToolSet，返回是否确实删除
- `SetToolSets(toolSets []tool.ToolSet)` —— 以给定切片整体替换当前所有 ToolSet

这些方法是并发安全的，并会自动重新计算：

- 聚合后的工具列表（显式 `WithTools` 工具 + ToolSet 工具 + 知识检索工具 + Skills 工具）
- “用户工具”跟踪信息（用于前文介绍的智能过滤机制）

需要特别注意：

- `AddToolSet` 替换同名 ToolSet 时，**不会**自动 `Close()` 被替换掉的旧实例。
- `RemoveToolSet` 删除 ToolSet 时，**不会**自动 `Close()` 被移除的实例。
- `SetToolSets` 整体替换时，**不会**自动 `Close()` 旧切片里的实例。

如果这些 ToolSet 是由你创建的，你仍然需要在合适的时机显式回收它们。

**典型使用方式：**

```go
// 1. 初始只挂基础工具
agent := llmagent.New("dynamic-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
)

// 2. 运行时挂载一个 MCP ToolSet
mcpToolSet := mcp.NewMCPToolSet(connectionConfig)
if err := mcpToolSet.Init(ctx); err != nil {
    return fmt.Errorf("初始化 MCP ToolSet 失败: %w", err)
}
agent.AddToolSet(mcpToolSet)

// 3. 从配置中心下发一整套 ToolSet（声明式控制）
toolSetsFromConfig := []tool.ToolSet{mcpToolSet, fileToolSet}
agent.SetToolSets(toolSetsFromConfig)

// 4. 按名称下线某个 ToolSet（例如回滚某个集成）
removed := agent.RemoveToolSet(mcpToolSet.Name())
if !removed {
    log.Printf("未找到 ToolSet %q", mcpToolSet.Name())
}
```

运行时 ToolSet 更新会自动与前文的**工具过滤机制**协同工作：

- 通过 `WithTools` 和所有 ToolSet（包括动态添加的 ToolSet）注册的工具都视为**用户工具**，会受到 `WithToolFilter` 以及每次调用的运行时过滤控制。
- 框架工具（`transfer_to_agent`、`knowledge_search`、
  `agentic_knowledge_search`、可选的 `await_user_reply`）仍然
  **永远不被过滤**，始终对 Agent 可用。

#### Tool Call 参数自动修复

部分模型在生成 `tool_calls` 时，可能产出非严格 JSON 的参数（例如对象 key 未加引号、尾逗号等），从而导致工具执行或外部解析失败。

Tool Call 参数自动修复功能适用于调用方需要在框架外部解析 `toolCall.Function.Arguments`，或工具严格要求入参为合法 JSON 的场景。

在 `runner.Run` 中启用 `agent.WithToolCallArgumentsJSONRepairEnabled(true)` 后，框架会尽力修复 `toolCall.Function.Arguments`。

```go
ch, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("..."),
    agent.WithToolCallArgumentsJSONRepairEnabled(true),
)
```

## 快速开始

### 环境准备

```bash
# 设置 API 密钥
export OPENAI_API_KEY="your-api-key"
```

### 简单示例

```go
package main

import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
    // 1. 创建简单工具
    calculatorTool := function.NewFunctionTool(
        func(ctx context.Context, req struct {
            Operation string  `json:"operation" jsonschema:"description=运算类型，例如 add/multiply"`
            A         float64 `json:"a" jsonschema:"description=第一个操作数"`
            B         float64 `json:"b" jsonschema:"description=第二个操作数"`
        }) (map[string]interface{}, error) {
            var result float64
            switch req.Operation {
            case "add":
                result = req.A + req.B
            case "multiply":
                result = req.A * req.B
            default:
                return nil, fmt.Errorf("unsupported operation")
            }
            return map[string]interface{}{"result": result}, nil
        },
        function.WithName("calculator"),
        function.WithDescription("简单计算器"),
    )

    // 2. 创建模型和 Agent
    llmModel := openai.New("DeepSeek-V3-Online-64K")
    agent := llmagent.New("calculator-assistant",
        llmagent.WithModel(llmModel),
        llmagent.WithInstruction("你是一个数学助手"),
        llmagent.WithTools([]tool.Tool{calculatorTool}),
        llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}), // 启用流式输出
    )

    // 3. 创建 Runner 并执行
    r := runner.NewRunner("math-app", agent)

    ctx := context.Background()
    userMessage := model.NewUserMessage("请计算 25 乘以 4")

    eventChan, err := r.Run(ctx, "user1", "session1", userMessage)
    if err != nil {
        panic(err)
    }

    // 4. 处理响应
    for event := range eventChan {
        if event.Error != nil {
            fmt.Printf("错误: %s\n", event.Error.Message)
            continue
        }

        // 显示工具调用
        if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
            for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
                fmt.Printf("🔧 调用工具: %s\n", toolCall.Function.Name)
                fmt.Printf("   参数: %s\n", string(toolCall.Function.Arguments))
            }
        }

        // 显示流式内容
        if len(event.Response.Choices) > 0 {
            fmt.Print(event.Response.Choices[0].Delta.Content)
        }

        if event.Done {
            break
        }
    }
}
```

### 运行示例

```bash
# 进入工具示例目录
cd examples/tool
go run .

# 进入 MCP 工具示例目录
cd examples/mcp_tool

# 启动外部服务器
cd streamalbe_server && go run main.go &

# 运行主程序
go run main.go -model="deepseek-chat"
```

## 总结

Tool 工具系统为 tRPC-Agent-Go 提供了丰富的扩展能力，支持函数工具、DuckDuckGo 搜索工具和 MCP 协议工具。
