# AG-UI 使用指南

AG-UI（Agent-User Interaction）协议由开源社区 [AG-UI Protocol](https://github.com/ag-ui-protocol/ag-ui) 维护，旨在让不同语言、不同框架、不同执行环境的 Agent，都能够通过统一的事件流把执行过程中产生的内容传递给用户界面，允许松散的事件格式匹配，支持 SSE 和 WebSocket 等多种通信协议。

`tRPC-Agent-Go` 接入了 AG-UI 协议，默认提供 SSE 服务端实现，也支持通过自定义 `service.Service` 切换到 WebSocket 等通信协议，并扩展事件翻译逻辑。

## 快速上手

假设你已实现一个 Agent，可以按如下方式接入 AG-UI 协议并启动服务：

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

// 创建 Agent
agent := newAgent()
// 创建 Runner
runner := runner.NewRunner(agent.Info().Name, agent)
// 创建 AG-UI 服务，指定 HTTP 路由
server, err := agui.New(runner, agui.WithPath("/agui"))
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
// 启动 HTTP 服务
if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
    log.Fatalf("server stopped with error: %v", err)
}
```

注意：若未显式指定 `WithPath`，AG-UI 服务默认路由为 `/`。

完整代码示例参见 [examples/agui/server/default](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/default)。

Runner 全面的使用方法参见 [runner](./runner.md)。

在前端侧，可以配合 [CopilotKit](https://github.com/CopilotKit/CopilotKit) 和 [TDesign Chat](https://tdesign.tencent.com/react-chat/overview) 等支持 AG-UI 协议的客户端框架，它提供 React/Next.js 组件并内置 SSE 订阅能力。仓库内提供两个可运行的 Web UI 示例：

- [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)：基于 Vite + React + TDesign 的客户端，演示自定义事件、Graph interrupt 审批、消息快照加载以及报告侧边栏等能力。
- [examples/agui/client/copilotkit](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/copilotkit)：基于 CopilotKit 搭建的 Next.js 客户端。

![copilotkit](../assets/img/agui/copilotkit.png)

## 核心概念

### 请求体 RunAgentInput

`RunAgentInput` 是 AG-UI 聊天路由和消息快照路由的请求体，用于描述一次对话运行所需的输入与上下文，结构如下所示。

```go
type RunAgentInput struct {
	ThreadID       string          // 会话线程 ID，框架会将其作为 `SessionID`。
	RunID          string          // 本次运行 ID，用于和事件流中的 `RUN_STARTED`、`RUN_FINISHED` 等事件关联。
	ParentRunID    *string         // 父运行 ID，可选。
	State          any             // 任意状态。
	Messages       []Message       // 消息列表，框架要求最后一条消息为 `role=user` 并把其内容（字符串或多模态数组）作为输入。
	Tools          []Tool          // 工具定义列表，协议字段，可选。
	Context        []Context       // 上下文列表，协议字段，可选。
	ForwardedProps any             // 任意透传字段，通常用于携带业务自定义参数。
}
```

完整字段定义可参考 [AG-UI Go SDK](https://github.com/ag-ui-protocol/ag-ui/blob/main/sdks/community/go/pkg/core/types/types.go)

最小请求 JSON 示例：

```json
{
    "threadId": "thread-id",
    "runId": "run-id",
    "messages": [
        {
            "role": "user",
            "content": "hello"
        }
    ],
    "forwardedProps": {
        "userId": "alice"
    }
}
```

### 实时对话路由

实时对话路由负责处理一次实时对话请求，并通过 SSE 把执行过程中的事件流推送给前端。该路由默认是 `/`，可通过 `agui.WithPath` 自定义。

需要注意的是，同一 `SessionKey`(`AppName`+`userID`+`sessionID`) 在同一时刻只允许有一个实时对话请求运行；如果重复发起会返回 `409 Conflict`。

即使前端 SSE 连接断开，后端也会继续执行直到正常结束（或被取消/超时）。默认情况下单次请求最多执行 1h，可通过 `agui.WithTimeout(d)` 调整，设置为 `0` 表示不设置超时；实际生效的超时时间取请求上下文超时时间与 `agui.WithTimeout(d)` 的较小值。

完整代码示例参见 [examples/agui/server/default](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/default)。

#### 连接断开与取消语义

默认情况下，AG-UI 服务会将一次 Agent 运行与请求的取消信号解耦，即使 SSE 连接断开，例如页面刷新导致连接中断，并触发请求 `ctx` 被取消，后端 run 仍会继续执行，直到正常结束、通过取消路由主动取消，或触发超时。

如果你希望请求 `ctx` 结束即停止 Agent 运行，也就是客户端断开或 `ctx` cancel 时停止 Agent 运行，可以显式开启：

```go
server, err := agui.New(
    runner,
    agui.WithPath("/agui"),
    agui.WithCancelOnContextDoneEnabled(true),
)
```

#### 多模态输入

对于 `role=user` 的消息，`content` 也可以是一个多模态数组，每个元素是一个 `InputContent` 片段，例如：

- `type: "text"` + `text`
- `type: "binary"` + `mimeType`，并且至少提供 `url` / `data`（base64 字符串或 base64 data URL）/ `id` 之一

URL 请求体示例：

```json
{
    "threadId": "thread-id",
    "runId": "run-id",
    "messages": [
        {
            "role": "user",
            "content": [
                { "type": "text", "text": "请描述这张图片。" },
                { "type": "binary", "mimeType": "image/png", "url": "https://example.com/image.png" }
            ]
        }
    ]
}
```

DATA 请求体示例：

```json
{
    "threadId": "thread-id",
    "runId": "run-id",
    "messages": [
        {
            "role": "user",
            "content": [
                { "type": "text", "text": "请描述这张图片。" },
                { "type": "binary", "mimeType": "image/png", "data": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMBAH+X1d0AAAAASUVORK5CYII=" }
            ]
        }
    ]
}
```

`url` 仅支持 `image/*` 类型的二进制输入；其他 `mimeType` 请使用 `data` 或 `id`。服务端会将 `data` 按标准 base64 解码。若希望只传原始 base64，也可以去掉 `data:*;base64,` 前缀。

### 取消路由

如果希望在实时对话运行中主动中断后端执行，可以通过 `agui.WithCancelEnabled(true)` 启用取消路由，默认关闭。路由默认是 `/cancel`，可通过 `agui.WithCancelPath` 自定义。

取消路由的请求体与实时对话请求一致，要成功取消，需要传入与实时对话路由相同的 `SessionKey`(`AppName`+`userID`+`sessionID`)。

#### 取消路由到底会停止什么？

取消路由会停止同一 `SessionKey` 下正在运行的后端 **run**（同一 `AppName`、解析
出来的 `userID`、以及相同的 `threadId`）。

它通常用于：

- 前端有“停止生成”按钮，需要中断后端执行。
- SSE 连接断开了，但仍希望停止后端（避免白白消耗模型/工具资源）。
- 你希望做服务端预算控制（时间/成本），及时中断异常 run。

#### 最小取消请求

大多数情况下，你只需要：

- `threadId`（映射到 `sessionID`）
- 以及 `UserIDResolver` 需要读取的字段（通常是 `forwardedProps.userId`）

当然，你也可以直接把实时对话请求的 JSON 原样再发一遍。

示例：

```bash
curl -X POST http://localhost:8080/cancel \
  -H 'Content-Type: application/json' \
  -d '{"threadId":"thread-id","runId":"run-id","forwardedProps":{"userId":"alice"}}'
```

典型返回：

- `200 OK`：取消成功
- `404 Not Found`：没有找到对应 `SessionKey` 的运行中任务（可能已结束，或标识不匹配）

### 消息快照路由

消息快照用于在页面初始化或断线重连时恢复历史对话，通过 `agui.WithMessagesSnapshotEnabled(true)` 控制功能是否开启，默认关闭。该路由默认是 `/history`， 可通过 `WithMessagesSnapshotPath` 自定义，负责返回 `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED` 的事件流。

该路由支持同 `userID + sessionID（threadId）` 并发访问，也可以在实时对话运行中访问同会话的快照。

启用消息快照功能时需要配置下列参数：

- `agui.WithMessagesSnapshotEnabled(true)` 启用消息快照功能；
- `agui.WithMessagesSnapshotPath` 设置消息快照路由的自定义路径，默认为 `/history`；
- `agui.WithAppName(name)` 指定应用名；
- `agui.WithSessionService(service)` 注入 `session.Service` 用于查询历史事件；
- `aguirunner.WithUserIDResolver(resolver)` 自定义 `userID` 解析逻辑，默认恒为 `"user"`。

框架在处理消息快照请求时会从 AG-UI 请求体 `RunAgentInput` 中解析 `threadId` 作为 `SessionID`，结合自定义 `UserIDResolver` 得到 `userID`，再与 `appName` 组装得到 `session.Key`，并从会话存储中读取已持久化的事件，将其还原为 `MessagesSnapshot` 所需的消息列表，封装成 `MESSAGES_SNAPSHOT` 事件，同时发送配套的 `RUN_STARTED`、`RUN_FINISHED` 事件。

代码示例如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

resolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    forwardedProps, ok := input.ForwardedProps.(map[string]any)
    if !ok {
        return "anonymous", nil
    }
    user, ok := forwardedProps["userId"].(string)
    if !ok || user == "" {
        return "anonymous", nil
    }
    return user, nil
}

sessionService := inmemory.NewSessionService()
server, err := agui.New(
    runner,
    agui.WithPath("/chat"),                    // 自定义实时对话路由，默认为 "/"
    agui.WithAppName("demo-app"),              // 设置 AppName，用于区分不同应用的历史记录
    agui.WithSessionService(sessionService),   // 设置 Session Service，用于读取历史事件
    agui.WithMessagesSnapshotEnabled(true),    // 开启消息快照功能
    agui.WithMessagesSnapshotPath("/history"), // 设置消息快照路由，默认为 "/history"
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(resolver), // 自定义 UserID 解析逻辑
    ),
)
if err != nil {
	log.Fatalf("create agui server failed: %v", err)
}
if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
	log.Fatalf("server stopped with error: %v", err)
}
```

完整的示例可参考 [examples/agui/messagessnapshot](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/messagessnapshot)。

AG-UI 的 MessagesSnapshotEvent 事件格式可见 [messages](https://docs.ag-ui.com/concepts/messages)。

## 进阶用法

### 自定义通信协议

AG-UI 协议未强制规定通信协议，框架使用 SSE 作为 AG-UI 的默认通信协议，如果希望改用 WebSocket 等其他协议，可以实现 `service.Service` 接口：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

type wsService struct {
	path    string
	runner  aguirunner.Runner
	handler http.Handler
}

func NewWSService(runner aguirunner.Runner, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	s := &wsService{
		path:   opts.Path,
		runner: runner,
	}
	h := http.NewServeMux()
	h.HandleFunc(s.path, s.handle)
	s.handler = h
	return s
}

func (s *wsService) Handler() http.Handler { /* HTTP Handler */ }

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithServiceFactory(NewWSService))
```

### 自定义 Translator

默认的 `translator.New` 会把内部事件翻译成协议里定义的标准事件集。若想在保留默认行为的基础上追加自定义信息，可以实现 `translator.Translator` 接口，并借助 AG-UI 的 `Custom` 事件类型携带扩展数据：

```go
import (
    aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

type customTranslator struct {
    inner translator.Translator
}

func (t *customTranslator) Translate(ctx context.Context, evt *event.Event) ([]aguievents.Event, error) {
    out, err := t.inner.Translate(ctx, evt)
    if err != nil {
        return nil, err
    }
    if payload := buildCustomPayload(evt); payload != nil {
        out = append(out, aguievents.NewCustomEvent("trace.metadata", aguievents.WithValue(payload)))
    }
    return out, nil
}

func buildCustomPayload(evt *event.Event) map[string]any {
    if evt == nil || evt.Response == nil {
        return nil
    }
    return map[string]any{
        "object":    evt.Response.Object,
        "timestamp": evt.Response.Timestamp,
    }
}

factory := func(ctx context.Context, input *adapter.RunAgentInput, opts ...translator.Option) (translator.Translator, error) {
    inner, err := translator.New(ctx, input.ThreadID, input.RunID, opts...)
    if err != nil {
        return nil, fmt.Errorf("create inner translator: %w", err)
    }
    return &customTranslator{inner: inner}, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithTranslatorFactory(factory)))
```

例如，在使用 React Planner 时，如果希望为不同标签应用不同的自定义事件，可以通过实现自定义 Translator 来实现，效果如下图所示。

![copilotkit-react](../assets/img/agui/copilotkit-react.png)

完整的代码示例可以参考 [examples/agui/server/react](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/react)。

### 自定义 `UserIDResolver`

默认所有请求都会归到固定的 `"user"` 用户 ID，可以通过自定义 `UserIDResolver` 从 `RunAgentInput` 中提取 `UserID`：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

resolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    forwardedProps, ok := input.ForwardedProps.(map[string]any)
    if !ok {
        return "anonymous", nil
    }
    user, ok := forwardedProps["userId"].(string)
    if !ok || user == "" {
        return "anonymous", nil
    }
    return user, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithUserIDResolver(resolver)))
```

### 自定义 `RunOptionResolver`

默认情况下，AG-UI Runner 不会为底层 `runner.Run` 附加额外的 `agent.RunOption`。

如果希望为 `runner.Run` 设置 `agent.RunOption`，可以实现 `RunOptionResolver` 并通过 `aguirunner.WithRunOptionResolver` 注入，例如从 `ForwardedProps` 中读取模型名称和知识过滤条件。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

resolver := func(_ context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
	if input == nil {
		return nil, errors.New("empty input")
	}
	forwardedProps, ok := input.ForwardedProps.(map[string]any)
	if !ok || forwardedProps == nil {
		return nil, nil
	}
	opts := make([]agent.RunOption, 0, 2)
	if modelName, ok := forwardedProps["modelName"].(string); ok && modelName != "" {
		opts = append(opts, agent.WithModelName(modelName))
	}
	if filter, ok := forwardedProps["knowledgeFilter"].(map[string]any); ok {
		opts = append(opts, agent.WithKnowledgeFilter(filter))
	}
	return opts, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithRunOptionResolver(resolver)))
```

`RunOptionResolver` 会在每次 `RunAgentInput` 被处理时执行，返回的 `RunOption` 列表会依次传入底层 `runner.Run`。

若返回错误，则会发送 `RunError` 事件；返回 `nil` 则表示不追加任何 `RunOption`。

### 自定义 `StateResolver`

默认情况下，AG-UI Runner 不会读取 `RunAgentInput.State` 并写入 `RunOptions.RuntimeState`。

如果希望基于 `State` 构造 RuntimeState，可以实现 `StateResolver` 并通过 `aguirunner.WithStateResolver` 注入。返回的 map 会在调用底层 `runner.Run` 前写入 `RunOptions.RuntimeState`，并覆盖此前通过 `RunOptionResolver` 等设置的 `RuntimeState`。

注意：若返回 `nil` 则表示不设置 `RuntimeState`；若返回空 map 则会将 `RuntimeState` 置为空。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

stateResolver := func(_ context.Context, input *adapter.RunAgentInput) (map[string]any, error) {
	state, ok := input.State.(map[string]any)
	if !ok || state == nil {
		return nil, nil
	}
	return map[string]any{
		"custom_key": state["custom_key"],
	}, nil
}

server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithStateResolver(stateResolver)))
```

### 可观测平台上报

在 `RunOptionResolver` 里附加自定义 span 属性，框架会在 Agent 入口 span 处自动打标：

```go
import (
    "go.opentelemetry.io/otel/attribute"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent"
)

runOptionResolver := func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
    content, ok := input.Messages[len(input.Messages)-1].ContentString()
    if !ok {
        return nil, errors.New("last message content is not a string")
    }
    attrs := []attribute.KeyValue{
        attribute.String("trace.input", content),
    }
    forwardedProps, ok := input.ForwardedProps.(map[string]any)
    if ok {
        if scenario, ok := forwardedProps["scenario"].(string); ok {
            attrs = append(attrs, attribute.String("conversation.scenario", scenario))
        }
    }
    return []agent.RunOption{agent.WithSpanAttributes(attrs...)}, nil
}

r := runner.NewRunner(agent.Info().Name, agent)
server, err := agui.New(r,
    agui.WithAGUIRunnerOptions(
        aguirunner.WithRunOptionResolver(runOptionResolver),
    ),
)
```

配合事件翻译回调 `AfterTranslate`，可在累积输出并写入 `trace.output`。这样前端流式事件与后端 trace 对齐，便于在可观测平台中同时查看输入和最终输出。 

与 Langfuse 可观测平台的结合示例可参考 [examples/agui/server/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/langfuse)。

### 事件翻译回调

AG-UI 提供了事件翻译的回调机制，便于在事件翻译流程的前后插入自定义逻辑。

- `translator.BeforeTranslateCallback`：在内部事件被翻译为 AG-UI 事件之前触发。返回值约定：
  - 返回 `(customEvent, nil)`：使用 `customEvent` 作为翻译的输入事件。
  - 返回 `(nil, nil)`：保留当前事件并继续执行后续回调；若所有回调都返回 `nil`，则最终使用原事件。
  - 返回错误：终止当前的执行流程，客户端将接收到 `RunError`。
- `translator.AfterTranslateCallback`：在 AG-UI 事件翻译完成，准备发送到客户端之前触发。返回值约定：
  - 返回 `(customEvent, nil)`：使用 `customEvent` 作为最终发送给客户端的事件。
  - 返回 `(nil, nil)`：保留当前事件并继续执行后续回调；若所有回调都返回 `nil`，则最终发送原事件。
  - 返回错误：终止当前的执行流程，客户端将接收到 `RunError`。

使用示例：

```go
import (
	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

callbacks := translator.NewCallbacks().
    RegisterBeforeTranslate(func(ctx context.Context, event *event.Event) (*event.Event, error) {
        // 在事件翻译前执行的逻辑
        return nil, nil
    }).
    RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
        // 在事件翻译后执行的逻辑
        if msg, ok := event.(*aguievents.TextMessageContentEvent); ok {
            // 在事件中修改消息内容
            return aguievents.NewTextMessageContentEvent(msg.MessageID, msg.Delta+" [via callback]"), nil
        }
        return nil, nil
    })

server, err := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithTranslateCallbacks(callbacks)))
```

事件翻译回调可以用于多种场景，比如：

- 自定义事件处理：在事件翻译过程中修改事件数据，添加额外的业务逻辑。
- 监控上报：在翻译前后插入监控上报逻辑，与 Langfuse 可观测平台的结合示例可参考 [examples/agui/server/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/langfuse)。

### RunAgentInput Hook

可以使用 `WithRunAgentInputHook` 对 AG-UI 请求体进行统一改写，示例中演示了如何从 `ForwardedProps` 读取提示词并合并到最后一条用户消息中：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

hook := func(ctx context.Context, input *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
	if input == nil {
		return nil, errors.New("empty input")
	}
	if len(input.Messages) == 0 {
		return nil, errors.New("missing messages")
	}
	forwardedProps, ok := input.ForwardedProps.(map[string]any)
	if !ok || forwardedProps == nil {
		return input, nil
	}
	otherContent, ok := forwardedProps["other_content"].(string)
	if !ok {
		return input, nil
	}

	content, ok := input.Messages[len(input.Messages)-1].ContentString()
	if !ok {
		return input, nil
	}
	input.Messages[len(input.Messages)-1].Content = content + otherContent
	return input, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithRunAgentInputHook(hook)))
```
要点：

- 返回 `nil` 会保留原始输入，但保留原位修改。
- 返回自定义的 `*adapter.RunAgentInput` 会覆盖原始输入；返回 `nil` 表示使用原输入。
- 返回错误会中止本次请求，客户端会收到 `RunError` 事件。

### Session 存储与事件聚合

在构造 AG-UI Runner 时传入 `SessionService`，实时对话产生的事件会通过 `SessionService` 写入会话，便于后续通过 `MessagesSnapshot` 回放历史记录。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

runner := runner.NewRunner(agent.Info().Name, agent)
sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithPath("/agui"),
    agui.WithMessagesSnapshotPath("/history"),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithAGUIRunnerOptions(aguirunner.WithUserIDResolver(userIDResolver)),
)
```

在流式响应场景下，同一条回复通常会包含多个增量文本事件，如果将它们全部直接写入会话，会给 `SessionService` 带来较大压力。

为了解决上述问题，框架会先对事件进行聚合，再写入会话。此外，默认每秒定时刷新一次，每次刷新将当前的聚合结果写入会话。

- `aggregator.WithEnabled(true)` 用于控制是否开启事件聚合，默认为开启状态。开启后，会将连续且具有相同 `messageId` 的文本事件进行聚合；关闭时则不对 AG-UI 事件做聚合。
- `aguirunner.WithFlushInterval(time.Second)` 用于控制事件聚合结果的定时刷新间隔，默认为 1 秒。设置为 0 时表示不开启定时刷新功能。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

runner := runner.NewRunner(agent.Info().Name, agent)
sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithPath("/agui"),
    agui.WithMessagesSnapshotPath("/history"),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithFlushInterval(time.Second), // 设置事件聚合结果的定时刷新间隔，默认 1 秒
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(userIDResolver),
        aguirunner.WithAggregationOption(aggregator.WithEnabled(true)), // 开启事件聚合，默认开启
    ),
)
```

如果需要更复杂的聚合策略，可以实现 `aggregator.Aggregator` 并通过自定义工厂注入。需要注意的是，虽然每个会话都会单独创建一个聚合器，省去了跨会话的状态维护和并发处理，但聚合方法本身仍有可能被并发调用，因此仍需妥善处理并发。

例如，在兼容默认文本聚合的同时，将类型为 `"think"` 的自定义事件内容累计后再落库。

完整示例代码可见 [examples/agui/server/thinkaggregator](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/thinkaggregator)

```go
import (
	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type thinkAggregator struct {
	mu    sync.Mutex
	inner aggregator.Aggregator
	think strings.Builder
}

func newAggregator(ctx context.Context, opt ...aggregator.Option) aggregator.Aggregator {
	return &thinkAggregator{inner: aggregator.New(ctx, opt...)}
}


func (c *thinkAggregator) Append(ctx context.Context, event aguievents.Event) ([]aguievents.Event, error) {
	// 通过互斥锁保证内部聚合器和 think 缓冲区的并发安全，避免多个事件并发追加时出现竞态条件。
	c.mu.Lock()
	defer c.mu.Unlock()

	// 针对自定义的 "think" 事件类型，采取“只累计不立刻下发”的策略：
	// 1. 先强制刷新 inner，将之前累积的普通事件完整输出；
	// 2. 当前的 think 内容只累加到缓冲区，不立即返回；
	// 这样可以保证 think 片段之间不会被普通事件打断，且之前的普通事件先于新的思考内容输出。
	if custom, ok := event.(*aguievents.CustomEvent); ok && custom.Name == string(thinkEventTypeContent) {
		flushed, err := c.inner.Flush(ctx)
		if err != nil {
			return nil, err
		}
		// 仅在值为字符串时参与累积，避免不符合预期类型的数据污染缓冲区。
		if v, ok := custom.Value.(string); ok {
			c.think.WriteString(v)
		}
		// 当前 think 事件只参与内部累积，不立刻对外可见，返回的是之前 inner 已有的聚合结果。
		return flushed, nil
	}

	// 非 think 事件走默认的 inner 聚合逻辑，保证原有文本聚合行为不被破坏。
	events, err := c.inner.Append(ctx, event)
	if err != nil {
		return nil, err
	}

	// 若尚无累积的 think 内容，直接返回 inner 的聚合结果，无需额外封装。
	if c.think.Len() == 0 {
		return events, nil
	}

	// 若存在已累积的 think 内容，则在当前批次事件前插入一个聚合后的 think 事件：
	// 1. 将缓冲区内容打包成一个整体的 CustomEvent；
	// 2. 清空缓冲区，避免重复下发；
	// 3. 将该 think 事件放在当前 events 之前，保持时间顺序：先输出完整思考，再输出后续事件。
	think := aguievents.NewCustomEvent(string(thinkEventTypeContent), aguievents.WithValue(c.think.String()))
	c.think.Reset()

	out := make([]aguievents.Event, 0, len(events)+1)
	out = append(out, think)
	out = append(out, events...)
	return out, nil
}

func (c *thinkAggregator) Flush(ctx context.Context) ([]aguievents.Event, error) {
	// Flush 同样需要保证内聚合器与 think 缓冲的并发安全。
	c.mu.Lock()
	defer c.mu.Unlock()

	// 先刷新 inner，确保所有普通事件按照其自身的聚合策略输出。
	events, err := c.inner.Flush(ctx)
	if err != nil {
		return nil, err
	}

	// 若 think 缓冲区中仍有未输出的内容，
	// 则将其封装为一个聚合后的 think 事件并插入当前批次的最前面，
	// 保证聚合后的思考内容不会被后续刷新产生的事件打乱顺序。
	if c.think.Len() > 0 {
		think := aguievents.NewCustomEvent(string(thinkEventTypeContent), aguievents.WithValue(c.think.String()))
		c.think.Reset()
		events = append([]aguievents.Event{think}, events...)
	}
	return events, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithPath("/agui"),
    agui.WithMessagesSnapshotPath("/history"),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(userIDResolver),
        aguirunner.WithAggregatorFactory(newAggregator),
    ),
)
```

### 消息快照续传

默认情况下，消息快照路由只返回一次性快照并立即结束连接。当用户在一次实时对话的中途刷新或重连时，仅靠快照可能无法覆盖快照边界之后继续产生的事件。如需在快照之后继续流式接收后续 AG-UI 事件，需要使用消息快照续传功能。

开启续传后，服务端会在发送 `MESSAGES_SNAPSHOT` 后继续通过同一条 SSE 连接续传后续事件，直到读到 `RUN_FINISHED` 或 `RUN_ERROR`。返回序列变为：

`RUN_STARTED → MESSAGES_SNAPSHOT → ...events... → RUN_FINISHED/RUN_ERROR`

消息快照续传功能有以下配置参数：

- `agui.WithMessagesSnapshotFollowEnabled(bool)`：控制是否启用消息快照续传功能
- `agui.WithMessagesSnapshotFollowMaxDuration(time.Duration)`：限制续传最长时间，避免无限等待
- `agui.WithFlushInterval(time.Duration)`：控制历史事件落库频率，续传的轮询间隔会复用该值

代码示例如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

runner := runner.NewRunner(agent.Info().Name, agent)
sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotFollowEnabled(true),
    agui.WithMessagesSnapshotFollowMaxDuration(30*time.Second),
    agui.WithFlushInterval(50*time.Millisecond),
)
```

完整示例可参考 [examples/agui/server/follow](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/follow)，前端可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。

多实例部署时，不同实例需要共享同一个 `SessionService`，否则消息快照路由无法读取其他实例写入的历史事件。

### 设置路由前缀 BasePath

`agui.WithBasePath` 设置 AG-UI 服务的基础路由前缀，默认值为 `/`，用于在统一前缀下挂载实时对话路由、消息快照路由以及取消路由（若启用），避免与现有服务冲突.

`agui.WithPath`、`agui.WithMessagesSnapshotPath` 与 `agui.WithCancelPath` 仅定义基础路径下的子路由，框架会自动将它们与基础路径拼接成最终可访问的路由.

使用示例如下所示

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithBasePath("/agui/"),               // 设置 AG-UI 前缀路由
    agui.WithPath("/chat"),                    // 设置实时对话路由，默认为 "/"
    agui.WithCancelEnabled(true),              // 开启取消路由
    agui.WithCancelPath("/cancel"),            // 设置取消路由，默认为 "/cancel"
    agui.WithMessagesSnapshotEnabled(true),    // 开启消息快照功能
    agui.WithMessagesSnapshotPath("/history"), // 设置消息快照路由，默认为 "/history"
)
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
```

此时实时对话路由为 `/agui/chat`，取消路由为 `/agui/cancel`，消息快照路由为 `/agui/history`。

### GraphAgent 节点活动事件

在 `GraphAgent` 场景下，一个 run 通常会按图执行多个节点。为了让前端能持续展示“当前正在执行哪个节点”，并在 Human-in-the-Loop 场景中渲染中断提示，框架支持额外发送节点生命周期与中断相关的 `ACTIVITY_DELTA` 事件。该能力默认关闭，可在创建 AG-UI Server 时按需开启。

`ACTIVITY_DELTA` 事件格式可参考 [AG-UI 官方文档](https://docs.ag-ui.com/concepts/events#activitydelta)

#### 节点生命周期（`graph.node.lifecycle`）

该事件默认关闭，可在创建 AG-UI Server 时通过 `agui.WithGraphNodeLifecycleActivityEnabled(true)` 开启。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
	runner,
	agui.WithGraphNodeLifecycleActivityEnabled(true),
)
```

启用后，节点在 `start` / `complete` / `error` 三个阶段都会发送 `ACTIVITY_DELTA`，且 `activityType` 均为 `graph.node.lifecycle`，通过 `/node.phase` 区分具体阶段。

节点开始阶段（`phase=start`）在节点执行前发出，并通过 `add /node` 写入当前节点信息：

```json
{
  "type": "ACTIVITY_DELTA",
  "activityType": "graph.node.lifecycle",
  "patch": [
    {
      "op": "add",
      "path": "/node",
      "value": {
        "nodeId": "plan_llm_node",
        "phase": "start"
      }
    }
  ]
}
```

该事件用于前端展示进度。前端可将 `/node.nodeId` 作为当前正在执行的节点，用于高亮或展示节点执行过程。

节点成功结束阶段（`phase=complete`）在节点执行结束后发出，并通过 `add /node` 写入本次结束的节点信息：

```json
{
  "type": "ACTIVITY_DELTA",
  "activityType": "graph.node.lifecycle",
  "patch": [
    {
      "op": "add",
      "path": "/node",
      "value": {
        "nodeId": "plan_llm_node",
        "phase": "complete"
      }
    }
  ]
}
```

节点失败结束阶段（`phase=error`）会在 `/node` 中携带错误信息：

```json
{
  "type": "ACTIVITY_DELTA",
  "activityType": "graph.node.lifecycle",
  "patch": [
    {
      "op": "add",
      "path": "/node",
      "value": {
        "nodeId": "plan_llm_node",
        "phase": "error",
        "error": "node execution failed"
      }
    }
  ]
}
```

#### 中断提示（`graph.node.interrupt`）

该事件默认关闭，可在创建 AG-UI Server 时通过 `agui.WithGraphNodeInterruptActivityEnabled(true)` 开启。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
	runner,
	agui.WithGraphNodeInterruptActivityEnabled(true),
)
```

`activityType` 为 `graph.node.interrupt`，在节点调用 `graph.Interrupt(ctx, state, key, prompt)` 且当前没有可用的 resume 输入时发出。`patch` 会通过 `add /interrupt` 写入中断信息到 `/interrupt`，包含 `nodeId`、`key`、`prompt`、`checkpointId` 与 `lineageId`：

```json
{
  "type": "ACTIVITY_DELTA",
  "activityType": "graph.node.interrupt",
  "patch": [
    {
      "op": "add",
      "path": "/interrupt",
      "value": {
        "nodeId": "confirm",
        "key": "confirm",
        "prompt": "Confirm continuing after the recipe amounts are calculated.",
        "checkpointId": "checkpoint-xxx",
        "lineageId": "lineage-xxx"
      }
    }
  ]
}
```

该事件表示执行在该节点暂停。前端可使用 `/interrupt.prompt` 渲染中断提示，并用 `/interrupt.key` 选择需要提供的恢复值。`checkpointId` 与 `lineageId` 可用于定位需要恢复的 checkpoint 并关联多次 run。

如果使用多级 GraphAgent，子图中断会向上冒泡，事件流中默认可能出现多条 `graph.node.interrupt`。如果前端只希望保留用于恢复的最外层中断，可额外开启 `agui.WithGraphNodeInterruptActivityTopLevelOnly(true)`，开启后仅发送最外层中断事件。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
	runner,
	agui.WithGraphNodeInterruptActivityEnabled(true),
	agui.WithGraphNodeInterruptActivityTopLevelOnly(true),
)
```

#### 恢复回执（`graph.node.interrupt`）

当新的 run 携带 resume 输入发起恢复时，AG-UI Server 会在该 run 的事件流开始处额外发送一条 `ACTIVITY_DELTA`，并且会先于任何 `graph.node.lifecycle` 事件发送。该事件同样使用 `activityType: graph.node.interrupt`，先将 `/interrupt` 置为 `null`，再通过 `add /resume` 写入本次恢复输入。`/resume` 包含 `resumeMap` 或 `resume`，并可包含 `checkpointId` 与 `lineageId`：

```json
{
  "type": "ACTIVITY_DELTA",
  "timestamp": 1767950998788,
  "messageId": "293cec35-9689-4628-82d3-475cc91dab20",
  "activityType": "graph.node.interrupt",
  "patch": [
    {
      "op": "add",
      "path": "/interrupt",
      "value": null
    },
    {
      "op": "add",
      "path": "/resume",
      "value": {
        "checkpointId": "checkpoint-xxx",
        "lineageId": "lineage-xxx",
        "resumeMap": {
          "confirm": true
        }
      }
    }
  ]
}
```

完整示例可参考 [examples/agui/server/graph](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/graph)，前端渲染与审批交互可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。

## 最佳实践

### 生成文档

长篇文档如果直接插入到对话正文，很容易把主对话“刷屏”，用户也难以区分对话内容和文档内容。为了解决这个问题，建议使用“文档面板”来承载长文档。通过 AG-UI 的事件流约定一套“打开文档面板 → 写入文档内容 → 关闭文档面板”的工作流，将长文档从对话中“抽离”出来，避免干扰正常交流，示例方案如下。

1. **后端：定义工具并约束调用顺序**

   为 Agent 提供两个工具：**打开文档面板** 和 **关闭文档面板**，并在 prompt 中约束生成顺序：
   当进入文档生成流程时，按以下顺序执行：

   1. 先调用“打开文档面板”工具
   2. 紧接着输出文档内容
   3. 最后调用“关闭文档面板”工具

   转换为 AG-UI 事件流，大致形态如下：

   ```text
   打开文档面板工具
     → ToolCallStart
     → ToolCallArgs
     → ToolCallEnd
     → ToolCallResult

   文档内容
     → TextMessageStart
     → TextMessageContent
     → TextMessageEnd

   关闭文档面板工具
     → ToolCallStart
     → ToolCallArgs
     → ToolCallEnd
     → ToolCallResult
   ```

2. **前端：监听工具事件并维护文档面板**

   在前端监听事件流：

   - 当捕捉到 `open_report_document` 工具事件时：创建文档面板，并将其后的文本消息内容写入该文档面板；
   - 当捕捉到 `close_report_document` 工具事件时：关闭文档面板（或将其标记为生成完成）。

实际效果如下图所示，完整示例可参考 [examples/agui/server/report](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/report)，前端实现可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。

![report](../assets/gif/agui/report.gif)

### 外部工具

当工具必须在客户端或业务侧执行时，可以采用外部工具模式。后端只生成工具调用并在工具节点触发中断，前端执行工具并回传结果，后端从中断点恢复继续运行。该模式要求工具结果进入 LLM 上下文，并写入会话历史，便于后续通过消息快照回放完整对话。

建议在服务端开启 `agui.WithGraphNodeInterruptActivityEnabled(true)`，用于在 `graph.node.interrupt` 事件中携带 `lineageId` 与 `checkpointId`，以便前端定位恢复点并发起下一次请求。

一次外部工具调用对应两次请求。第一次请求使用 `role=user`，当 LLM 触发工具调用时，事件流会输出 `TOOL_CALL_START`、`TOOL_CALL_ARGS`、`TOOL_CALL_END`，随后在工具节点输出 `ACTIVITY_DELTA graph.node.interrupt` 并结束本次 SSE。前端在事件流中获取 `toolCallId` 与参数，并从中断事件获取 `lineageId`。

第二次请求使用 `role=tool`，把工具执行结果回传给后端。`toolCallId` 必须与第一次请求一致，`content` 为工具输出字符串，同时在 `forwardedProps.lineage_id` 填入第一次中断事件返回的 `lineageId`。服务端会先把该 tool message 翻译为 `TOOL_CALL_RESULT` 并写入会话，再从对应 checkpoint 恢复继续生成最终回复。

如果第一次请求中 LLM 未触发任何工具调用，则不会出现中断事件，也不需要发起第二次请求。

两次请求需要保持 `threadId` 一致，每次请求使用新的 `runId`。当前框架仅处理 `messages` 的最后一条消息，且只支持 `role=user` 或 `role=tool`，`content` 仅支持字符串。

请求示例如下：

第一次请求：

```json
{
  "threadId": "demo-thread",
  "runId": "demo-run-1",
  "messages": [
    {
      "role": "user",
      "content": "Search and answer my question."
    }
  ]
}
```

第二次请求：

```json
{
  "threadId": "demo-thread",
  "runId": "demo-run-2",
  "forwardedProps": {
    "lineage_id": "lineage-from-graph-node-interrupt"
  },
  "messages": [
    {
      "id": "tool-result-<toolCallId>",
      "role": "tool",
      "toolCallId": "<toolCallId>",
      "name": "external_search",
      "content": "external tool output as string"
    }
  ]
}
```

事件流示例如下：

```text
第一次请求 role=user
  → RUN_STARTED
  → TOOL_CALL_START
  → TOOL_CALL_ARGS
  → TOOL_CALL_END
  → ACTIVITY_DELTA graph.node.interrupt
  → RUN_FINISHED

第二次请求 role=tool
  → RUN_STARTED
  → TOOL_CALL_RESULT 由输入的 tool message 生成
  → TEXT_MESSAGE_* 恢复后继续生成
  → RUN_FINISHED
```

完整示例可参考 [examples/agui/server/externaltool](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/externaltool)，前端实现可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。
