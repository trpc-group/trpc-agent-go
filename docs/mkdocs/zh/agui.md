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

在前端侧，可以配合 [CopilotKit](https://github.com/CopilotKit/CopilotKit) 等支持 AG-UI 协议的客户端框架，它提供 React/Next.js 组件并内置 SSE 订阅能力。[examples/agui/client/copilotkit](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/copilotkit) 使用 CopilotKit 搭建了 Web UI 界面，通过 AG-UI 协议与 Agent 通信，效果如下图所示。

![copilotkit](../assets/img/agui/copilotkit.png)

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
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

type customTranslator struct {
    inner translator.Translator
}

func (t *customTranslator) Translate(event *event.Event) ([]aguievents.Event, error) {
    out, err := t.inner.Translate(event)
    if err != nil {
        return nil, err
    }
    if payload := buildCustomPayload(event); payload != nil {
        out = append(out, aguievents.NewCustomEvent("trace.metadata", aguievents.WithValue(payload)))
    }
    return out, nil
}

func buildCustomPayload(event *event.Event) map[string]any {
    if event == nil || event.Response == nil {
        return nil
    }
    return map[string]any{
        "object":    event.Response.Object,
        "timestamp": event.Response.Timestamp,
    }
}

factory := func(input *adapter.RunAgentInput) translator.Translator {
    return &customTranslator{inner: translator.New(input.ThreadID, input.RunID)}
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
    if user, ok := input.ForwardedProps["userId"].(string); ok && user != "" {
        return user, nil
    }
    return "anonymous", nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithUserIDResolver(resolver)))
```

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
	if input.ForwardedProps == nil {
		return input, nil
	}
	otherContent, ok := input.ForwardedProps["other_content"].(string)
	if !ok {
		return input, nil
	}

	input.Messages[len(input.Messages)-1].Content += otherContent
	return input, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithRunAgentInputHook(hook)))
```
要点：

- 返回 `nil` 会保留原始输入，但保留原位修改。
- 返回自定义的 `*adapter.RunAgentInput` 会覆盖原始输入；返回 `nil` 表示使用原输入。
- 返回错误会中止本次请求，客户端会收到 `RunError` 事件。

### 消息快照

消息快照用于在页面初始化或断线重连时恢复历史对话，通过 `agui.WithMessagesSnapshotEnabled(true)` 控制功能是否开启，默认关闭。启用该能力后，AG-UI 会同时提供聊天路由和消息快照路由：

- 聊天路由默认是 `/`，可通过 `agui.WithPath` 自定义；
- 消息快照路由默认是 `/history`， 可通过 `WithMessagesSnapshotPath` 自定义，负责返回 `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED` 的事件流。

启用消息快照功能时需要配置下列参数：

- `agui.WithMessagesSnapshotEnabled(true)` 启用消息快照功能；
- `agui.WithMessagesSnapshotPath` 设置消息快照路由的自定义路径，默认为 `/history`；
- `agui.WithAppName(name)` 指定应用名；
- `agui.WithSessionService(service)` 注入 `session.Service` 用于查询历史事件；
- `aguirunner.WithUserIDResolver(resolver)` 自定义 `userID` 解析逻辑，默认恒为 `"user"`。

框架在处理消息快照请求时会从 AG-UI 请求体 `RunAgentInput` 中解析 `threadId` 作为 `SessionID`，结合自定义 `UserIDResolver` 得到 `userID`，再与 `appName` 组装得到 `session.Key`，然后通过 `session.Service` 查询 `Session`。将 `Session` 中存储的事件 `Session.Events` 转换为 AG-UI 消息并封装成 `MESSAGES_SNAPSHOT` 事件，同时发送配套的 `RUN_STARTED`、`RUN_FINISHED` 事件。

代码示例如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

resolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    if user, ok := input.ForwardedProps["userId"].(string); ok && user != "" {
        return user, nil
    }
    return "anonymous", nil
}

sessionService := inmemory.NewService(context.Background())
server, err := agui.New(
    runner,
    agui.WithPath("/chat"),                    // 自定义聊天路由，默认为 "/"
    agui.WithAppName("demo-app"),              // 设置 AppName，用于构造 Session Key
    agui.WithSessionService(sessionService),   // 设置 Session Service，用于查询 Session
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
