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
	Messages       []Message       // 消息列表。实时对话请求读取尾部消息：`role=user` 时使用其 content（字符串或多模态数组）作为输入；尾部连续 `role=tool` 时作为当前工具结果批次。
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

取消成功后，框架不会直接丢弃正在收尾的协议状态，而是会继续补齐必要的结束事件，并将聚合器中尚未落库的 AG-UI 事件尽量写入 `SessionService`。因此，后续通过 `/history` 读取历史时，拿到的是取消瞬间最后一个合法的一致快照，而不是一段不完整的中间状态。例如，已经产生的部分 `reasoning` 文本会作为字符串保留；如果某段 `reasoning` 尚未形成任何文本内容，则不会出现在快照中。运行结束后的这段收尾时间可通过 `agui.WithPostRunFinalizationTimeout(d)` 调整，默认值为 `5s`。

### 消息快照路由

消息快照用于在页面初始化或断线重连时恢复历史对话，通过 `agui.WithMessagesSnapshotEnabled(true)` 控制功能是否开启，默认关闭。该路由默认是 `/history`， 可通过 `WithMessagesSnapshotPath` 自定义，负责返回 `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED` 的事件流。

该路由支持同 `userID + sessionID（threadId）` 并发访问，也可以在实时对话运行中访问同会话的快照。

启用消息快照功能时需要配置下列参数：

- `agui.WithMessagesSnapshotEnabled(true)` 启用消息快照功能；
- `agui.WithMessagesSnapshotPath` 设置消息快照路由的自定义路径，默认为 `/history`；
- `agui.WithAppName(name)` 指定应用名，作为默认 `AppName`；
- `agui.WithAppNameResolver(resolver)` 可选，用于按请求覆盖 `AppName`；
- `agui.WithSessionService(service)` 注入 `session.Service` 用于查询历史事件；
- `aguirunner.WithUserIDResolver(resolver)` 自定义 `userID` 解析逻辑，默认恒为 `"user"`。

框架在处理消息快照请求时会从 AG-UI 请求体 `RunAgentInput` 中解析 `threadId` 作为 `SessionID`，结合自定义 `UserIDResolver` 得到 `userID`，再优先使用 `AppNameResolver` 返回的 `appName`；若未返回值，则回退到 `agui.WithAppName(name)`。三者共同组装得到 `session.Key`，并从会话存储中读取已持久化的事件，将其还原为 `MessagesSnapshot` 所需的消息列表，封装成 `MESSAGES_SNAPSHOT` 事件，同时发送配套的 `RUN_STARTED`、`RUN_FINISHED` 事件。

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

### Translator 接口

处理请求时，Translator 接口负责将框架内部事件翻译成 AG-UI 协议事件，再发送给客户端。

接口定义如下：

```go
type Translator interface {
    Translate(ctx context.Context, event *event.Event) ([]aguievents.Event, error)
}

type PostRunFinalizingTranslator interface {
    Translator
    PostRunFinalizationEvents(ctx context.Context) ([]aguievents.Event, error)
}
```

其中，`Translator` 负责将内部事件翻译为 AG-UI 事件；`PostRunFinalizingTranslator` 则用于在运行结束后的收尾阶段补发仍未关闭的协议事件，并在需要时返回收尾阶段错误。

## 进阶用法

### SSE 心跳保活

在一些部署环境中，网关、负载均衡或浏览器可能会关闭长时间没有数据写入的 SSE 连接。如果你的 Agent 运行过程中可能出现较长时间没有事件输出的情况，可以通过 `agui.WithHeartbeatInterval(d)` 开启传输层心跳：

```go
server, err := agui.New(
    runner,
    agui.WithPath("/agui"),
    agui.WithHeartbeatInterval(15*time.Second),
)
```

服务端会按配置间隔写入 SSE comment 帧 `:\n\n`，用于保持连接活跃。该能力默认关闭，传入小于等于 0 的间隔表示关闭。

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

默认的 `translator.New` 会把内部事件翻译成协议里定义的标准事件集。若想在保留默认行为的基础上追加自定义信息，可以实现 `translator.Translator` 接口，并借助 AG-UI 的 `Custom` 事件类型携带扩展数据。

如果自定义 Translator 除了追加自定义事件外，还会维护自己的打开流，或者只是包装默认 Translator 并希望保留取消与运行结束时的自动收尾能力，建议同时实现 `translator.PostRunFinalizingTranslator`，将运行结束后需要补发的收尾事件继续交给框架处理：

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

var _ translator.PostRunFinalizingTranslator = (*customTranslator)(nil)

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

func (t *customTranslator) PostRunFinalizationEvents(ctx context.Context) ([]aguievents.Event, error) {
    finalizer, ok := t.inner.(translator.PostRunFinalizingTranslator)
    if !ok {
        return nil, nil
    }
    return finalizer.PostRunFinalizationEvents(ctx)
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

`PostRunFinalizationEvents` 会在运行结束后的收尾阶段被调用。如果该方法返回错误，框架会在尽力发送已经返回的收尾事件后，再发送一个 `RunError`，从而将收尾阶段的问题显式暴露给客户端。

例如，在使用 React Planner 时，如果希望为不同标签应用不同的自定义事件，可以通过实现自定义 Translator 来实现，效果如下图所示。

![copilotkit-react](../assets/img/agui/copilotkit-react.png)

完整的代码示例可以参考 [examples/agui/server/react](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/react)。

### 暴露 source 元数据供前端分组

开启子 Agent 的流式透传后，前端经常需要知道某条 AG-UI 事件到底来自
哪个 sub-agent，才能把 tool call、tool result 和文本输出按同一个成员
归到一起展示。

可以通过 `agui.WithEventSourceMetadataEnabled(true)`，把原始
`trpc-agent-go` 事件里的精简 source 信息挂到翻译后的 AG-UI 事件
`rawEvent` 字段中：

```go
server, err := agui.New(
    runner,
    agui.WithEventSourceMetadataEnabled(true),
)
```

如果你不是直接用 `agui.New`，而是手动组装各层，也可以在更底层开启：

- `aguirunner.WithEventSourceMetadataEnabled(true)`
- `translator.WithEventSourceMetadataEnabled(true)`

开启后，像 `TOOL_CALL_START`、`TOOL_CALL_ARGS`、`TOOL_CALL_END`、
`TOOL_CALL_RESULT`、文本事件以及 activity 事件，都会携带类似下面的
`rawEvent`：

```json
{
  "rawEvent": {
    "eventId": "evt-tool-call",
    "author": "member-a",
    "invocationId": "inv-1",
    "parentInvocationId": "parent-1",
    "branch": "root.member-a"
  }
}
```

`rawEvent` 是可选字段。它只会出现在 AG-UI translator 或消息快照构建
器生成的 AG-UI 事件上；如果当前事件没有可暴露的非空 source 元数据，
就不会携带这个字段。

在 `/history` 路由上，`MESSAGES_SNAPSHOT` 的 `rawEvent` 不是单条事件
source，而是一份 source 索引：

```json
{
  "rawEvent": {
    "messages": {
      "assistant-1": {
        "eventId": "evt-assistant",
        "author": "member-a",
        "invocationId": "inv-1",
        "branch": "root.member-a"
      }
    },
    "toolCalls": {
      "tool-call-1": {
        "eventId": "evt-tool-call",
        "author": "member-a",
        "invocationId": "inv-1",
        "branch": "root.member-a"
      }
    }
  }
}
```

前端推荐这样使用：

- 想按“这是哪个 Agent 发的”分组时，用 `rawEvent.author`。
- 想按“这一次具体的 sub-agent 执行块”分组时，用 `rawEvent.branch`。
  这样即使同名 Agent 在一次 run 中被调用多次，也不会混在一起。
- 如果你需要唯一执行键，但不想直接把 branch 暴露到 UI 状态里，可以
  使用 `rawEvent.invocationId`。
- 如果你是在恢复 `MESSAGES_SNAPSHOT` 历史消息，可以读取
  `rawEvent.toolCalls[toolCallId]` 或 `rawEvent.messages[messageId]`，
  用同一套规则还原前端分组状态。

兼容性说明：

- 该能力默认关闭。
- 开启后只是增量增加 `rawEvent`，不会改动原有事件顺序、messageId、
  toolCallId，也不会改变现有字段语义。
- 现有前端如果忽略 `rawEvent`，行为保持不变。

### 思考内容

AG-UI 通过`REASONING_*` 事件承载模型思考内容，便于前端在正文回复之前展示思考过程，详细可参考 [AG-UI Reasoning](https://docs.ag-ui.com/concepts/reasoning)。典型事件序列如下：

```text
REASONING_START
  → REASONING_MESSAGE_START
  → REASONING_MESSAGE_CONTENT
  → REASONING_MESSAGE_END
REASONING_END
```

默认情况下会屏蔽思考内容，可以通过 `agui.WithReasoningContentEnabled` 在创建 Server 时开启思考内容：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(runner, agui.WithReasoningContentEnabled(true))
```

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

### 自定义 `AppNameResolver`

默认情况下，AG-UI 使用 `agui.WithAppName(name)` 作为静态 `AppName`，并将其与 `userID`、`threadId` 一起组成 `SessionKey`。

如果希望按请求动态切换 `AppName`，可以实现 `AppNameResolver` 并通过 `agui.WithAppNameResolver` 注入。`AppNameResolver` 返回非空字符串时，会覆盖本次请求的 `AppName`；若返回空字符串，则继续回退到 `agui.WithAppName(name)`。

实时对话路由、取消路由和消息快照路由会复用同一套 `AppName` 解析逻辑，因此，同一会话的 `/agui`、`/cancel`、`/history` 请求应传入一致的业务标识。

需要注意的是，若开启了消息快照功能，服务启动时仍然需要显式配置 `agui.WithAppName(name)` 作为默认值；`AppNameResolver` 只负责请求级覆盖。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

resolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    forwardedProps, ok := input.ForwardedProps.(map[string]any)
    if !ok || forwardedProps == nil {
        return "", nil
    }
    appName, ok := forwardedProps["appName"].(string)
    if !ok || appName == "" {
        return "", nil
    }
    return appName, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(
    runner,
    agui.WithAppName("default-app"),
    agui.WithAppNameResolver(resolver),
)
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
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
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
	userMessageIndex := -1
	for i := len(input.Messages) - 1; i >= 0; i-- {
		if input.Messages[i].Role == types.RoleUser {
			userMessageIndex = i
			break
		}
	}
	if userMessageIndex < 0 {
		return input, nil
	}
	content, ok := input.Messages[userMessageIndex].ContentString()
	if !ok {
		return input, nil
	}
	input.Messages[userMessageIndex].Content = content + otherContent
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

为了解决上述问题，框架会先对事件进行聚合，再写入会话。此外，默认每秒定时刷新一次，每次刷新将当前的聚合结果写入会话。无论 run 是正常结束还是被取消，在退出前框架还会再执行一次运行结束后的收尾流程，用于补发仍然打开的协议流结束事件，并将聚合缓存尽量刷入会话存储。这一阶段与日常的定时刷新是分开的。

- `aggregator.WithEnabled(true)` 用于控制是否开启事件聚合，默认为开启状态。开启后，会将连续且具有相同 `messageId` 的 `TEXT_MESSAGE_CONTENT` 与 `REASONING_MESSAGE_CONTENT` 事件进行聚合；关闭时则不对 AG-UI 事件做聚合。
- `aguirunner.WithFlushInterval(time.Second)` 用于控制事件聚合结果的定时刷新间隔，默认为 1 秒。设置为 0 时表示不开启定时刷新功能。
- `agui.WithPostRunFinalizationTimeout(5*time.Second)` 用于限制运行结束后收尾流程的最长执行时间。该阶段同时覆盖协议收尾事件补发与聚合缓存落库，默认值为 `5s`。设置为 `0` 表示不额外设置超时。

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
  agui.WithFlushInterval(time.Second),                // 设置事件聚合结果的定时刷新间隔，默认 1 秒
  agui.WithPostRunFinalizationTimeout(5*time.Second), // 设置 run 结束后的收尾超时，默认 5 秒
  agui.WithAGUIRunnerOptions(
    aguirunner.WithUserIDResolver(userIDResolver),
    aguirunner.WithAggregationOption(aggregator.WithEnabled(true)), // 开启事件聚合，默认开启
  ),
)
```

如果需要更复杂的聚合策略，可以实现 `aggregator.Aggregator` 并通过自定义工厂注入。需要注意的是，虽然每个会话都会单独创建一个聚合器，省去了跨会话的状态维护和并发处理，但聚合方法本身仍有可能被并发调用，因此仍需妥善处理并发。

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

### 流式工具执行结果

`StreamableTool` 在底层 runner 事件流里，通常会先产生若干条 partial `tool.response`，工具结束时再补一条非 partial 的最终 `tool.response`。如果工具流中显式返回了 `tool.FinalResultChunk` 或 `tool.FinalResultStateChunk`，runner 会直接使用它作为最终结果；如果没有显式 final result，则会继续按现有 merge 规则把之前的 chunk 聚合成最终结果。开启下面这个选项后，前端在工具执行过程中看到的是 activity 事件，工具结束时再收到一条最终的 `TOOL_CALL_RESULT`：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithStreamingToolResultActivityEnabled(true),
)
```

该选项默认关闭。未开启时，同一次 tool call 的实时事件流通常会表现为：

```text
RUN_STARTED
→ TOOL_CALL_START
→ TOOL_CALL_ARGS
→ TOOL_CALL_END
→ TOOL_CALL_RESULT
→ TOOL_CALL_RESULT
→ TOOL_CALL_RESULT
→ ...
→ TEXT_MESSAGE_*
→ RUN_FINISHED
```

也就是说，工具执行过程中的 partial `tool.response` 与工具结束时的最终 `tool.response` 都会继续按现有 `TOOL_CALL_RESULT` 翻译逻辑发送给前端。

启用后，同一次 tool call 的实时事件流通常会表现为：

```text
RUN_STARTED
→ TOOL_CALL_START
→ TOOL_CALL_ARGS
→ TOOL_CALL_END
→ ACTIVITY_SNAPSHOT
→ ACTIVITY_DELTA
→ ACTIVITY_DELTA
→ ...
→ TOOL_CALL_RESULT
→ TEXT_MESSAGE_*
→ RUN_FINISHED
```

- 第一段非空 partial tool result chunk 会被翻译为 `ACTIVITY_SNAPSHOT`。
- 后续非空 partial chunk 会被翻译为 `ACTIVITY_DELTA`。
- 这些 activity 的 `activityType` 固定为 `tool.result.stream`。
- 同一次 tool call 的 activity 会复用同一个合成 `messageId`，前端可以把它当作同一条 activity 流来更新。
- 工具真正结束时，仍然只会发送一条正式的 `TOOL_CALL_RESULT`。

内置 `tool.result.stream` activity 的状态结构固定为：

```json
{
  "toolCallId": "call_xxx",
  "content": "Counted 1 of 3.\n"
}
```

对应的事件通常会表现为：

```json
{
  "type": "ACTIVITY_SNAPSHOT",
  "messageId": "tool-result-activity-call_xxx",
  "activityType": "tool.result.stream",
  "content": {
    "toolCallId": "call_xxx",
    "content": "Counted 1 of 3.\n"
  }
}
```

```json
{
  "type": "ACTIVITY_DELTA",
  "messageId": "tool-result-activity-call_xxx",
  "activityType": "tool.result.stream",
  "patch": [
    {
      "op": "add",
      "path": "/content",
      "value": "Counted 1 of 3.\nCounted 2 of 3.\n"
    }
  ]
}
```

其中 `ACTIVITY_DELTA` 的 `patch.path` 对内置 `tool.result.stream` activity 固定为 `/content`，表示更新这份 activity state 里的 `content` 字段。

activity 中的 `content` 不是单次 chunk，而是服务端按收到的非空 partial `Content` 顺序累计后的完整过程内容，因此前端可以直接按最新 activity 状态覆盖展示，而不需要自己拼接字符串。最终 `TOOL_CALL_RESULT` 的内容仍遵循 runner 的原始语义：
- 如果工具流中没有显式 final result chunk，则最终 `TOOL_CALL_RESULT` 使用之前 chunk 按框架现有 merge 规则聚合后的结果。
- 如果工具流中显式返回了 `tool.FinalResultChunk` 或 `tool.FinalResultStateChunk`，则最终 `TOOL_CALL_RESULT` 直接使用该显式 final result。
- 在显式 final result 场景下，之前过程中的 chunk 不会自动并入最终 `TOOL_CALL_RESULT`。

消息快照路由中，由 partial tool result 改写出来的 activity 不会写入 `SessionService` track，因此：
- 通过消息快照路由返回的 `MESSAGES_SNAPSHOT` 中不会包含这些过程 activity。
- 每次 tool call 在快照消息列表里只保留一条最终 `tool` 消息。
- 这条最终 `tool` 消息的内容与实时对话路由里的最终 `TOOL_CALL_RESULT` 一致。

### 外部工具

外部工具模式适用于 tool call 由客户端、上游服务或其他工具运行环境执行的场景。完整链路如下：

- agent 生成 tool call，AG-UI 事件流返回 `toolCallId` 与参数。
- 调用方执行工具。
- 调用方用后续请求回传工具结果，结果以 `role=tool` message 表示。
- AG-UI 服务端发送 `TOOL_CALL_RESULT`，写入会话历史，并把工具结果交给 agent 继续运行。

当前支持两种服务端形态：普通 LLM 对话使用 LLMAgent Tool-Filter 模式；图编排流程使用 GraphAgent Interrupt 模式。

#### LLMAgent Tool-Filter 模式

当 AG-UI 服务端直接包装 `llmagent.Agent` 时，使用该模式。外部工具仍注册到 agent 中，使模型能够生成对应 tool call；`RunOptionResolver` 返回 `agent.WithToolExecutionFilter(...)`，声明由调用方执行的工具。

第一次请求使用 `role=user`。当模型请求由调用方执行的 tool call 时，事件流输出 assistant tool-call 事件（`TOOL_CALL_START`、`TOOL_CALL_ARGS`、`TOOL_CALL_END`），并在该 assistant tool-call 响应后结束本次 run。调用方从 tool call 事件中获取 `toolCallId` 和参数，执行工具后，再用 `role=tool` message 发起第二次请求。

第二次请求保持同一 `threadId`，使用独立的 `runId`。`messages` 尾部可以包含一条或多条 `role=tool` message，每个 `toolCallId` 对应一条工具结果。多个外部工具结果按多条尾部 `role=tool` message 回传，AG-UI 服务端按尾部 tool message 的顺序生成当前 turn 的工具结果输入。

代码示例片段如下：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    aguiadapter "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

func resolveRunOptions(
    context.Context,
    *aguiadapter.RunAgentInput,
) ([]agent.RunOption, error) {
    return []agent.RunOption{
        agent.WithToolExecutionFilter(
            tool.NewExcludeToolNamesFilter("external_note"),
        ),
    }, nil
}

server, err := agui.New(
    run,
    agui.WithAGUIRunnerOptions(
        aguirunner.WithRunOptionResolver(resolveRunOptions),
    ),
)
```

完整 LLMAgent 示例：服务端可参考 [examples/agui/server/externaltool/llmagent](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/externaltool/llmagent)，前端客户端可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。

LLMAgent 请求示例：

第一次请求（`role=user`）：

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

第二次请求（`role=tool`）：

```json
{
  "threadId": "demo-thread",
  "runId": "demo-run-2",
  "messages": [
    {
      "id": "tool-result-<toolCallIdA>",
      "role": "tool",
      "toolCallId": "<toolCallIdA>",
      "name": "<externalToolNameA>",
      "content": "first external tool output as string"
    },
    {
      "id": "tool-result-<toolCallIdB>",
      "role": "tool",
      "toolCallId": "<toolCallIdB>",
      "name": "<externalToolNameB>",
      "content": "second external tool output as string"
    }
  ]
}
```

LLMAgent 事件流示例：

```text
第一次请求 role=user
  → RUN_STARTED
  → TOOL_CALL_START
  → TOOL_CALL_ARGS
  → TOOL_CALL_END
  → RUN_FINISHED

第二次请求 role=tool
  → RUN_STARTED
  → TOOL_CALL_RESULT 由每条尾部 tool message 生成
  → TEXT_MESSAGE_* 模型继续生成
  → RUN_FINISHED
```

#### GraphAgent Interrupt 模式

当外部执行属于某个 graph 节点，并且后端需要从 graph checkpoint 恢复时，使用该模式。对应 graph 节点调用 `graph.Interrupt` 表示等待调用方回传结果。服务端开启 `agui.WithGraphNodeInterruptActivityEnabled(true)` 后，`graph.node.interrupt` 事件会携带 `lineageId` 与 `checkpointId`，调用方据此定位下一次请求的恢复点。

第一次请求使用 `role=user`。在该流程中，LLM 节点输出 `TOOL_CALL_START`、`TOOL_CALL_ARGS`、`TOOL_CALL_END`；随后 graph 进入触发中断的工具节点，输出 `ACTIVITY_DELTA graph.node.interrupt`，并在 `RUN_FINISHED` 后结束本次 SSE。调用方在事件流中获取外部工具的 `toolCallId`、工具参数、`lineageId` 和 `checkpointId`。

第二次请求使用 `role=tool`。请求中的 `toolCallId` 对应第一次请求中的工具调用，`content` 为工具输出字符串，`forwardedProps.lineage_id` 与 `forwardedProps.checkpoint_id` 分别来自第一次中断事件返回的 `lineageId` 与 `checkpointId`。`RunOptionResolver` 将工具结果转换为 graph resume 信息，通常以 `graph.Command{ResumeMap: ...}` 传给 GraphAgent。服务端发送 `TOOL_CALL_RESULT` 事件、写入会话历史，并从对应 checkpoint 恢复继续生成最终回复。

GraphAgent 的恢复契约由 graph 定义。被中断节点和 `ResumeMap` key 负责消费回传结果；单个 pending tool call 对应一个工具结果。多个外部结果使用 graph 层显式建模的批量恢复契约。graph 同时混用服务端执行工具和调用方执行工具时，推荐拆成独立阶段：先运行内部工具调用 LLM 节点和内置 tools 节点，再运行外部工具调用 LLM 节点和中断节点。这样内部工具执行保留在常规 graph tools 路径上，中断节点只负责调用方回传结果。

代码示例片段如下：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    aguiadapter "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

func externalToolNode(ctx context.Context, state graph.State) (any, error) {
    msgs, _ := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
    pendingToolCall, ok := findPendingToolCall(msgs, "external_search")
    if !ok {
        return nil, nil
    }
    resumeValue, err := graph.Interrupt(ctx, state, pendingToolCall.ID, pendingToolCall.ID)
    if err != nil {
        return nil, err
    }
    content, ok := resumeValue.(string)
    if !ok {
        return nil, fmt.Errorf("resume value for %s must be a string", pendingToolCall.ID)
    }
    return graph.State{
        graph.StateKeyMessages: graph.AppendMessages{
            Items: []model.Message{
                model.NewToolMessage(pendingToolCall.ID, "external_search", content),
            },
        },
    }, nil
}

func resolveRunOptions(
    _ context.Context,
    input *aguiadapter.RunAgentInput,
) ([]agent.RunOption, error) {
    lineageID, checkpointID, resumeMap, err := graphResumeInput(input)
    if err != nil {
        return nil, err
    }
    return []agent.RunOption{
        agent.WithRuntimeState(map[string]any{
            graph.CfgKeyLineageID:    lineageID,
            graph.CfgKeyCheckpointID: checkpointID,
            graph.StateKeyCommand: &graph.Command{ResumeMap: resumeMap},
        }),
    }, nil
}

server, err := agui.New(
    run,
    agui.WithGraphNodeInterruptActivityEnabled(true),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithRunOptionResolver(resolveRunOptions),
    ),
)
```

其中 `graphResumeInput` 负责读取 `forwardedProps.lineage_id` 与 `forwardedProps.checkpoint_id`，并把尾部连续的 `role=tool` message 转换为 `ResumeMap`。

完整 GraphAgent 示例：服务端可参考 [examples/agui/server/externaltool/graphagent](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/externaltool/graphagent)，前端实现可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。

GraphAgent 请求示例：

第一次请求（`role=user`）：

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

第二次请求（`role=tool`）：

```json
{
  "threadId": "demo-thread",
  "runId": "demo-run-2",
  "forwardedProps": {
    "lineage_id": "lineage-from-graph-node-interrupt",
    "checkpoint_id": "checkpoint-from-graph-node-interrupt"
  },
  "messages": [
    {
      "id": "tool-result-<toolCallIdA>",
      "role": "tool",
      "toolCallId": "<toolCallIdA>",
      "name": "<externalToolNameA>",
      "content": "first external tool output as string"
    },
    {
      "id": "tool-result-<toolCallIdB>",
      "role": "tool",
      "toolCallId": "<toolCallIdB>",
      "name": "<externalToolNameB>",
      "content": "second external tool output as string"
    }
  ]
}
```

如果同一次中断暴露多条 pending external tool call，第二次请求应包含这些 tool call 的全部结果。

GraphAgent 事件流示例：

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
  → TOOL_CALL_RESULT 由每条尾部 tool message 生成
  → ACTIVITY_DELTA graph.node.interrupt 恢复确认，开启时出现
  → TEXT_MESSAGE_* 恢复后继续生成
  → RUN_FINISHED
```

#### AG-UI `role=tool` 输入处理

`role=tool` 请求的 `content` 使用字符串。`id` 用作 `TOOL_CALL_RESULT` 的 message id；`toolCallId` 映射到内部 tool result 的 `ToolID`；`name` 表示工具名。AG-UI 服务端读取 `messages` 尾部连续的 `role=tool` message 作为当前工具结果输入批次。

尾部包含多条 `role=tool` message 且 `RunOptionResolver` 同时返回 `agent.WithUserMessageRewriter` 时，AG-UI 会将用户 rewriter 与解析后的工具结果输入组合。用户 rewriter 先执行；它返回的 `role=user`、`role=assistant` 等 message 会保留在最终工具结果块之前。它返回的 `role=tool` message 的内部 `ToolID` 与某条 AG-UI `toolCallId` 一致时，该改写后的 tool message 为该 tool call 提供结果；其他 tool call 使用请求中的 `role=tool` message。AG-UI 始终按请求尾部 tool message 的顺序放置最终工具结果块，因此最后一条 current-turn message 仍然是最后一个工具结果。需要在工具结果解析前改写原始 AG-UI 请求时，使用 `WithRunAgentInputHook`。

如果希望 `role=tool` 输入回显经过 Translator，可以开启 `agui.WithToolResultInputTranslationEnabled(true)`；开启后，AG-UI 服务端会先把每条 tool result 输入规范化为内部事件，再交给 Translator 处理，示例如下。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

server, err := agui.New(
    runner,
    agui.WithToolResultInputTranslationEnabled(true),
)
```

## 最佳实践

默认优先使用服务端工具执行路径。工具必须在客户端或业务侧执行时，采用“外部工具”模式；这类场景适合作为进阶用法来设计与评估。

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
