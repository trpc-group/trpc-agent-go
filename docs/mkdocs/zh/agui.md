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

func (t *customTranslator) Translate(ctx context.Context, event *event.Event) ([]aguievents.Event, error) {
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

factory := func(ctx context.Context, input *adapter.RunAgentInput) translator.Translator {
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
	if input.ForwardedProps == nil {
		return nil, nil
	}
	opts := make([]agent.RunOption, 0, 2)
	if modelName, ok := input.ForwardedProps["modelName"].(string); ok && modelName != "" {
		opts = append(opts, agent.WithModelName(modelName))
	}
	if filter, ok := input.ForwardedProps["knowledgeFilter"].(map[string]any); ok {
		opts = append(opts, agent.WithKnowledgeFilter(filter))
	}
	return opts, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithRunOptionResolver(resolver)))
```

`RunOptionResolver` 会在每次 `RunAgentInput` 被处理时执行，返回的 `RunOption` 列表会依次传入底层 `runner.Run`。

若返回错误，则会发送 `RunError` 事件；返回 `nil` 则表示不追加任何 `RunOption`。

### 可观测平台上报

AG-UI Runner 提供 `WithStartSpan`，用于在每次 run 开始时统一创建追踪 span 并写入请求相关属性。

```go
import (
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// 自定义 StartSpan，设置 threadId/userId/input 等属性
func startSpan(ctx context.Context, input *adapter.RunAgentInput) (context.Context, trace.Span, error) {
    userID, _ := userIDResolver(ctx, input)
    attrs := []attribute.KeyValue{
        attribute.String("session.id", input.ThreadID),
        attribute.String("user.id", userID),
        attribute.String("trace.input", input.Messages[len(input.Messages)-1].Content),
    }
    return atrace.Tracer.Start(ctx, "agui-run", trace.WithAttributes(attrs...))
}

func userIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    if user, ok := input.ForwardedProps["userId"].(string); ok && user != "" {
        return user, nil
    }
    return "anonymous", nil
}

r := runner.NewRunner(agent.Info().Name, agent)
server, err := agui.New(r,
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(userIDResolver),
        aguirunner.WithStartSpan(startSpan),
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
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(userIDResolver),
        aguirunner.WithAggregationOption(aggregator.WithEnabled(true)), // 开启事件聚合，默认开启
        aguirunner.WithFlushInterval(time.Second),                      // 设置事件聚合结果的定时刷新间隔，默认 1 秒
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

AG-UI 的 MessagesSnapshotEvent 事件格式可见 [messages](https://docs.ag-ui.com/concepts/messages)。

### 设置路由前缀 BasePath

`agui.WithBasePath` 设置 AG-UI 服务的基础路由前缀，默认值为 `/`，用于在统一前缀下挂载聊天路由与消息快照路由，避免与现有服务冲突.

`agui.WithPath` 与 `agui.WithMessagesSnapshotPath` 仅定义基础路径下的子路由，框架会通过 `url.JoinPath` 将它们与基础路径拼接成最终可访问的路由.

使用示例如下所示

```go
server, err := agui.New(
    runner,
    agui.WithBasePath("/agui"),                // 设置 AG-UI 前缀路由
    agui.WithPath("/chat"),                    // 设置聊天路由，默认为 "/"
    agui.WithMessagesSnapshotEnabled(true),    // 开启消息快照功能
    agui.WithMessagesSnapshotPath("/history"), // 设置消息快照路由，默认为 "/history"
)
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
```

此时聊天路由为 `/agui/chat`，用消息快照路由为 `/agui/history`。

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

实际效果如下图所示，完整示例可参考 [examples/agui/server/report](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/report)。

![report](../assets/gif/agui/report.gif)
