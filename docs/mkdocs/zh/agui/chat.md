# 实时对话路由

## 核心概念

实时对话路由负责处理一次实时对话请求，并通过 SSE 把执行过程中的事件流推送给前端。该路由默认是 `/`，可通过 `agui.WithPath` 自定义。如果需要统一路由前缀，可参考 [路由前缀](index.md#路由前缀)。

需要注意的是，同一 `SessionKey`(`AppName`+`userID`+`sessionID`) 在同一时刻只允许有一个实时对话请求运行；如果重复发起会返回 `409 Conflict`。

即使前端 SSE 连接断开，后端也会继续执行直到正常结束（或被取消/超时）。默认情况下单次请求最多执行 1h，可通过 `agui.WithTimeout(d)` 调整，设置为 `0` 表示不设置超时；实际生效的超时时间取请求上下文超时时间与 `agui.WithTimeout(d)` 的较小值。

完整代码示例参见 [examples/agui/server/default](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/default)。

## 请求体 RunAgentInput

`RunAgentInput` 是 AG-UI 服务端路由使用的请求体，实时对话、消息快照和取消路由都会通过它传递会话与运行信息。其中，实时对话路由主要读取 `messages` 尾部输入：

- 尾部是 `role=user` 时，按用户输入启动本次运行；
- 尾部连续为 `role=tool` 时，按外部工具结果继续本次对话。

```go
type RunAgentInput struct {
	ThreadID       string          // 会话线程 ID，框架会将其作为 SessionID。
	RunID          string          // 本次运行 ID，用于关联运行生命周期事件。
	ParentRunID    *string         // 父运行 ID，可选。
	State          any             // 任意状态，可通过 StateResolver 写入 RuntimeState。
	Messages       []Message       // 消息列表，用于传递本次用户输入或外部工具结果。
	Tools          []Tool          // 工具定义列表，协议字段，可选。
	Context        []Context       // 上下文列表，协议字段，可选。
	ForwardedProps any             // 任意透传字段，通常用于携带业务自定义参数。
}
```

完整字段定义可参考 [AG-UI Go SDK](https://github.com/ag-ui-protocol/ag-ui/blob/main/sdks/community/go/pkg/core/types/types.go)

### 文本输入

发起实时对话请求时，`messages` 尾部的 `role=user` 消息通过字符串形式的 `content` 承载本轮用户输入，服务端会将这条消息转换为本轮 Agent 运行的输入。

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

对应的 `curl` 示例：

```bash
curl -N -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{
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
  }'
```

### 多模态输入

多模态输入使用 `messages` 尾部的 `role=user` 消息表示本轮用户输入。与文本输入不同，`content` 不再是字符串，而是由多个 `InputContent` 片段组成的数组。数组中的每个元素表示一段输入内容，常用类型包括：

- 文本片段：`type` 为 `"text"`，文本内容写在 `text` 字段中。
- 二进制片段：`type` 为 `"binary"`，需要提供 `mimeType`。图片输入可以通过 `url` 指向图片地址，其他二进制内容可以通过 `data` 传递 base64 内容。

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

`url` 方式仅用于图片输入；其他类型的二进制内容使用 `data`。使用 `data` 时，服务端会按标准 base64 解码；`data` 既可以是原始 base64 字符串，也可以带有 `data:*;base64,` 前缀。

### 外部工具结果输入

当上一轮事件流返回需要外部执行的 tool call 后，调用方可以再次请求实时对话路由，并在 `messages` 尾部放置一条或多条 `role=tool` 消息。服务端会将这些尾部连续的工具消息作为本轮工具结果输入，交给 Agent 继续运行。

```json
{
    "threadId": "thread-id",
    "runId": "run-id",
    "messages": [
        {
            "id": "tool-result-tool-call-id",
            "role": "tool",
            "toolCallId": "tool-call-id",
            "name": "external_tool",
            "content": "tool result"
        }
    ]
}
```

每条 `role=tool` 消息对应一个工具调用结果。`toolCallId` 用于关联上一轮事件流中的工具调用，`name` 表示工具名，`content` 使用字符串承载工具执行结果；`id` 会作为返回 `TOOL_CALL_RESULT` 事件时的 message id。

## RunAgentInput Hook

`RunAgentInput Hook` 会在 AG-UI Runner 处理请求前执行，用于统一规范化或改写 `RunAgentInput`。实时对话、消息快照和取消路由都会使用 Hook 处理后的请求体。

Hook 接收当前 `RunAgentInput`，可以返回原请求体、原位修改后的请求体，或一个新的 `RunAgentInput`。如果只需要解析 `UserID`、`AppName`、`State` 或运行选项，优先使用后续对应的 Resolver。

下面示例演示一种历史业务字段兼容方式，旧请求把用户输入放在 `forwardedProps.legacy_message` 中，且 `messages` 为空时，Hook 会补齐一条 `role=user` 消息。

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
	if len(input.Messages) > 0 {
		return input, nil
	}
	forwardedProps, ok := input.ForwardedProps.(map[string]any)
	if !ok || forwardedProps == nil {
		return input, nil
	}
	legacyMessage, ok := forwardedProps["legacy_message"].(string)
	if !ok || legacyMessage == "" {
		return input, nil
	}
	input.Messages = []types.Message{
		{
			Role:    types.RoleUser,
			Content: legacyMessage,
		},
	}
	return input, nil
}

run := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(run, agui.WithAGUIRunnerOptions(aguirunner.WithRunAgentInputHook(hook)))
```

要点：

- 返回自定义的 `*adapter.RunAgentInput` 会使用新的请求体继续后续处理。
- 返回 `nil` 会沿用原始请求体；如果 Hook 已经原位修改了原始对象，修改会保留。
- 返回错误会中止本次请求，客户端会收到 `RunError` 事件。

## 自定义 `UserIDResolver`

默认情况下，AG-UI 会把请求归到固定的用户 ID `"user"`。`UserIDResolver` 用于从 `RunAgentInput` 中解析业务用户标识，解析结果会参与会话定位。实时对话、消息快照和取消路由会复用同一套解析逻辑，因此同一会话的相关请求需要解析出一致的 `UserID`。

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
    userID, ok := forwardedProps["userId"].(string)
    if !ok || userID == "" {
        return "anonymous", nil
    }
    return userID, nil
}

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithUserIDResolver(resolver)))
```

## 自定义 `AppNameResolver`

`AppName` 会与 `UserID`、`threadId` 一起参与会话定位。默认情况下，AG-UI 使用 `agui.WithAppName(name)` 配置的静态 `AppName`。如果需要按请求解析应用标识，可以实现 `AppNameResolver` 并通过 `agui.WithAppNameResolver` 注入。

`AppNameResolver` 返回非空字符串时，会使用该值作为本次请求的 `AppName`；返回空字符串时，会回退到 `agui.WithAppName(name)`。实时对话、消息快照和取消路由会复用同一套解析逻辑，因此同一会话的相关请求需要解析出一致的 `AppName`。

开启消息快照功能时，需要配置 `agui.WithAppName(name)` 作为默认值。

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

## 自定义 `RunOptionResolver`

`RunOptionResolver` 用于为本次 Agent 运行补充 [`agent.RunOption`](https://github.com/trpc-group/trpc-agent-go/blob/main/agent/invocation.go)。它会在每次请求处理时执行，返回的选项只作用于当前这次运行。

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

## 自定义 `StateResolver`

`StateResolver` 用于把 `RunAgentInput.State` 转换为本次运行的 RuntimeState。返回的 map 会作为 `agent.WithRuntimeState(...)` 传入 Runner，只作用于当前这次运行。

返回 `nil` 表示不设置 RuntimeState；返回空 map 表示设置一个空的 RuntimeState。

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

## 自定义 Translator

[Translator](index.md#translator) 负责将框架内部事件转换为 AG-UI 事件。框架内置 Translator 会将框架内部事件翻译为 AG-UI 协议定义的标准事件，并负责维护流式事件状态和运行结束时的收尾。自定义 Translator 可以独立实现这一转换，也可以包装框架内置 Translator，在保留默认翻译与收尾逻辑的基础上扩展事件输出。

自定义 Translator 通常通过 `aguirunner.WithTranslatorFactory` 注入。Factory 会在每次运行开始时创建 Translator，因此 Translator 可以维护本次运行内的翻译状态。

如果自定义 Translator 会生成需要在运行结束时关闭的流式事件，或包装了框架内置 Translator，需要实现 `translator.PostRunFinalizingTranslator`，让框架在运行结束时补齐必要的收尾事件。

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

`PostRunFinalizationEvents` 会在运行结束后的收尾阶段被调用。如果该方法返回错误，框架会尽力发送已经返回的收尾事件，并向客户端发送 `RunError`。

例如，在使用 React Planner 时，如果希望为不同标签应用不同的自定义事件，可以通过实现自定义 Translator 来实现，效果如下图所示。

![agui-react](../../assets/img/agui/agui-react.png)

完整的代码示例可以参考 [examples/agui/server/react](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/react)。

## 事件翻译回调

事件翻译回调用于在框架内置 Translator 翻译单个事件的前后执行自定义逻辑。

`translator.BeforeTranslateCallback` 会在框架内部事件进入 Translator 前执行，可用于替换本次翻译使用的内部事件。`translator.AfterTranslateCallback` 会在 AG-UI 事件生成后、发送给客户端前执行，可用于替换本次即将发送的 AG-UI 事件。

多个回调会按注册顺序执行。第一个返回非 nil 事件的回调会替换当前事件，后续回调不再执行；全部返回 nil 时，保持原事件。任一回调返回错误时，本次请求会失败。

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
        return nil, nil
    }).
    RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
        if msg, ok := event.(*aguievents.TextMessageContentEvent); ok {
            return aguievents.NewTextMessageContentEvent(msg.MessageID, msg.Delta+" [via callback]"), nil
        }
        return nil, nil
    })

server, err := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithTranslateCallbacks(callbacks)))
```

与 Langfuse 可观测平台结合的完整示例可参考 [examples/agui/server/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/langfuse)。

## 连接断开处理

默认情况下，实时对话请求的 SSE 连接与后端 Agent 运行是解耦的。浏览器刷新、页面关闭或网络中断导致 SSE 连接断开时，后端运行不会因此立即停止，而是继续运行到正常结束、被取消路由取消，或触发超时。

如果希望请求上下文结束时同步取消后端运行，可以开启 `agui.WithCancelOnContextDoneEnabled(true)`。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithPath("/agui"),
    agui.WithCancelOnContextDoneEnabled(true),
)
```

## SSE 心跳保活

某些网关、负载均衡或浏览器会关闭长时间没有数据写入的 SSE 连接。如果 Agent 运行期间可能长时间没有事件输出，可以开启 SSE 心跳。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithPath("/agui"),
    agui.WithHeartbeatInterval(15*time.Second),
)
```

开启后，服务端会按配置间隔写入 SSE comment 帧 `:\n\n`，用于保持连接活跃。心跳不会产生 AG-UI 事件。该能力默认关闭，传入小于等于 0 的间隔表示关闭。

## 自定义传输协议

框架默认使用 SSE 传输 AG-UI 事件流。需要接入 WebSocket 或其他传输方式时，可以自定义 `service.Service`。自定义 Service 负责接收 HTTP 请求、调用 `aguirunner.Runner`，并把返回的 AG-UI 事件写回客户端。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

type customService struct {
    runner  aguirunner.Runner
    handler http.Handler
}

func NewCustomService(runner aguirunner.Runner, opt ...service.Option) service.Service {
	opts := service.NewOptions(opt...)
	s := &customService{
		runner: runner,
	}
	h := http.NewServeMux()
	h.HandleFunc(opts.Path, s.handle)
	s.handler = h
	return s
}

func (s *customService) handle(w http.ResponseWriter, r *http.Request) {
    // Implement custom transport handling here.
}

func (s *customService) Handler() http.Handler {
    return s.handler
}

server, err := agui.New(runner, agui.WithServiceFactory(NewCustomService))
```

## 思考内容

AG-UI 使用 `REASONING_*` 事件表示模型返回的 reasoning content，前端可以在正文回复之前展示这部分内容。相关事件定义可参考 [AG-UI Reasoning](https://docs.ag-ui.com/concepts/reasoning)。

流式 reasoning content 通常会形成如下事件序列。

```text
REASONING_START
  → REASONING_MESSAGE_START
  → REASONING_MESSAGE_CONTENT
  → REASONING_MESSAGE_END
REASONING_END
```

框架默认不输出 reasoning content。创建 Server 时开启 `agui.WithReasoningContentEnabled(true)` 后，Translator 会将模型返回的 reasoning content 转换为 `REASONING_*` 事件。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithReasoningContentEnabled(true),
)
```

## 流式工具调用参数

默认情况下，AG-UI 服务端会在模型完成一次工具调用后发送完整的 `TOOL_CALL_START → TOOL_CALL_ARGS → TOOL_CALL_END`。也就是说，前端通常只能在工具参数全部生成完成后，才能看到这次工具调用的参数。

如果工具参数本身生成时间较长，或者前端需要在工具执行前实时展示参数生成进度，可以开启工具调用参数流式输出。开启后，AG-UI 服务端会把模型流式产生的工具参数分片转换成多条 `TOOL_CALL_ARGS` 事件，前端可以按 `toolCallId` 累积这些分片并增量展示。

该能力要求底层模型适配层支持并开启 tool call delta 输出。以 OpenAI 适配层为例，可以同时开启模型层和 AG-UI 层开关：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

llm := openai.New(
    "gpt-5.5",
    openai.WithShowToolCallDelta(true), // Forward tool_calls chunks.
)

server, err := agui.New(
    runner,
    agui.WithToolCallDeltaStreamingEnabled(true),
)
```

这里有两个开关需要同时满足：

- `openai.WithShowToolCallDelta(true)`：OpenAI 适配层不再过滤原始 `tool_calls` 流式分片，并把它们转成框架内部的工具调用增量。
- `agui.WithToolCallDeltaStreamingEnabled(true)`：AG-UI 服务端将这些分片转换为实时 `TOOL_CALL_ARGS` 事件。

其他模型适配层如果也支持框架内部的工具调用增量，AG-UI 层会按同一逻辑处理。

启用后，同一次工具调用的实时事件流通常会表现为：

```text
RUN_STARTED
→ TOOL_CALL_START
→ TOOL_CALL_ARGS
→ TOOL_CALL_ARGS
→ ...
→ TOOL_CALL_END
→ TOOL_CALL_RESULT
→ TEXT_MESSAGE_*
→ RUN_FINISHED
```

前端处理时只需要关注两点：

- `TOOL_CALL_ARGS.delta` 是本次新增的参数字符串片段，不一定是完整 JSON；应按 `toolCallId` 累积后再解析。
- 同一工具调用的 `TOOL_CALL_ARGS` 不保证在事件流中连续；前端状态应按 `toolCallId` 分组维护，而不是依赖相邻事件。

工具调用结束时，AG-UI 服务端会发送 `TOOL_CALL_END`。如果运行被取消或异常结束，服务端也会尽量补齐仍未关闭的协议事件，避免前端停留在未完成状态。

实时对话路由会把每个 `TOOL_CALL_ARGS` 分片发送给前端；如果配置了 `SessionService`，写入会话前会对相邻且相同 `toolCallId` 的 `TOOL_CALL_ARGS` 做聚合。消息快照路由用于恢复累计后的工具调用参数，不保留实时分片的数量和边界。

完整示例可参考 [examples/agui/server/toolcall_delta](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/toolcall_delta)。

## 流式工具执行结果

[`StreamableTool`](../tool.md#🌊-流式工具支持) 在执行过程中先返回流式中间结果，在结束时返回最终结果。工具可以在流中返回 `tool.FinalResultChunk` 或 `tool.FinalResultStateChunk` 指定最终结果；如果没有返回这两类结果，框架会把已收到的普通流式中间结果转成文本，并按返回顺序拼接为最终结果。

默认情况下，Translator 会把流式中间结果和最终结果都翻译为 `TOOL_CALL_RESULT`，因此同一个 tool call 可能出现多条 `TOOL_CALL_RESULT`。

开启 `agui.WithStreamingToolResultActivityEnabled(true)` 后，流式中间结果会改写为 Activity 事件，`activityType` 为 `tool.result.stream`；工具结束时，前端仍会收到一条最终的 `TOOL_CALL_RESULT`。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithStreamingToolResultActivityEnabled(true),
)
```

该选项默认关闭。未开启时，同一次 tool call 的实时事件流通常表现为：

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

启用后，同一次 tool call 的实时事件流通常表现为：

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

流式中间结果会以完整的 AG-UI Activity 事件发送。第一段非空流式中间结果会生成 `ACTIVITY_SNAPSHOT`：

```json
{
  "type": "ACTIVITY_SNAPSHOT",
  "timestamp": 1767950998788,
  "messageId": "tool-result-stream-call_xxx",
  "activityType": "tool.result.stream",
  "content": {
    "toolCallId": "call_xxx",
    "content": "Counted 1 of 3.\n"
  },
  "replace": true
}
```

后续非空流式中间结果会生成 `ACTIVITY_DELTA`：

```json
{
  "type": "ACTIVITY_DELTA",
  "timestamp": 1767950998799,
  "messageId": "tool-result-stream-call_xxx",
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

同一次 tool call 的 Activity 事件使用同一个 `messageId`，`activityType` 固定为 `tool.result.stream`。`ACTIVITY_DELTA` 的 `patch.path` 固定为 `/content`，其中的 `value` 是服务端累计后的完整中间结果内容，前端可以按最新 Activity 状态覆盖展示。

最终 `TOOL_CALL_RESULT` 的内容来源保持不变。如果工具流中没有返回 `tool.FinalResultChunk` 或 `tool.FinalResultStateChunk`，最终结果会由已收到的普通流式中间结果按顺序拼接得到；如果工具流中返回了这两类结果，最终结果会直接使用其中的 `Result`。

消息快照路由不会保存这些流式中间结果 Activity 事件。通过消息快照路由恢复历史时，每次 tool call 只保留一条最终 `tool` 消息，内容与实时对话路由中的最终 `TOOL_CALL_RESULT` 一致。

完整示例可参考 [examples/agui/server/streamtool](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/streamtool)。

## 事件来源元数据

多 Agent 或子 Agent 流式透传场景下，同一轮 AG-UI 事件流可能包含来自不同 Agent invocation 的文本、tool call、tool result 和 Activity 事件。开启事件来源元数据后，框架会把内部事件中的来源信息写入 AG-UI 事件的 `rawEvent` 字段，调用方可以据此识别事件来源并恢复前端分组状态。

该能力默认关闭，可以通过 `agui.WithEventSourceMetadataEnabled(true)` 开启：

```go
server, err := agui.New(
    runner,
    agui.WithEventSourceMetadataEnabled(true),
)
```

开启后，Translator 生成的 AG-UI 事件在存在非空来源信息时会携带 `rawEvent`，例如：

```json
{
  "type": "TOOL_CALL_START",
  "toolCallId": "tool-call-1",
  "rawEvent": {
    "eventId": "evt-tool-call",
    "author": "member-a",
    "invocationId": "inv-1",
    "parentInvocationId": "parent-1",
    "branch": "root.member-a"
  }
}
```

其中 `author` 表示事件作者，通常可用于按 Agent 或成员分组。`invocationId` 表示本次执行，`parentInvocationId` 表示父级执行，`branch` 表示当前执行在调用链中的分支位置。同名 Agent 在单次运行中被多次调用时，`branch` 可以用于区分不同执行分支。

消息快照路由返回的 `MESSAGES_SNAPSHOT` 事件也可以携带来源信息。此时 `rawEvent` 不是单条事件的来源信息，而是按消息和 tool call 建立的来源索引：

```json
{
  "type": "MESSAGES_SNAPSHOT",
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

恢复历史消息时，可以通过 `rawEvent.messages[messageId]` 获取消息来源，也可以通过 `rawEvent.toolCalls[toolCallId]` 获取 tool call 来源。索引中的来源信息与实时事件里的 `rawEvent` 使用同一组字段，前端可以沿用这些字段含义恢复分组状态。

## 外部工具

外部工具用于 tool call 由调用方执行的场景。AG-UI 服务端不直接运行这些工具，但仍负责让 Agent 生成 tool call、把调用信息发送给调用方、接收工具结果，并把结果交给 Agent 继续运行。

通用链路如下：

- Agent 生成 tool call，AG-UI 事件流返回 `toolCallId` 与参数。
- 调用方执行工具。
- 调用方用后续请求回传工具结果，结果以 `role=tool` message 表示。
- AG-UI 服务端发送 `TOOL_CALL_RESULT`，写入会话历史，并把工具结果交给 Agent 继续运行。

当前支持两种服务端形态。直接包装 `llmagent.Agent` 时，使用 LLMAgent Tool-Filter 模式；外部执行属于 GraphAgent 节点并且需要从 checkpoint 恢复时，使用 GraphAgent Interrupt 模式。

### LLMAgent Tool-Filter 模式

当 AG-UI 服务端直接包装 `llmagent.Agent`，并且只需要把部分工具交给调用方执行时，使用该模式。外部工具仍注册到 Agent 中，使模型能够生成对应 tool call；`RunOptionResolver` 返回 `agent.WithToolExecutionFilter(...)`，声明哪些工具不在服务端执行。

第一次请求使用 `role=user`。当模型生成需要调用方执行的 tool call 时，事件流输出 `TOOL_CALL_START`、`TOOL_CALL_ARGS` 和 `TOOL_CALL_END`，并在该 assistant tool-call 响应后结束本次 run。调用方从事件流中获取 `toolCallId` 和工具参数，执行工具后，再用 `role=tool` message 发起第二次请求。

第二次请求保持同一 `threadId`，使用新的 `runId`。`messages` 尾部可以包含一条或多条 `role=tool` message，每个 `toolCallId` 对应一条工具结果。AG-UI 服务端按尾部 tool message 的顺序生成当前 turn 的工具结果输入，并驱动 Agent 继续运行。

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
      "id": "tool-result-<toolCallId>",
      "role": "tool",
      "toolCallId": "<toolCallId>",
      "name": "<toolName>",
      "content": "tool output as string"
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
  → TOOL_CALL_RESULT 由尾部 tool message 生成
  → TEXT_MESSAGE_* 模型继续生成
  → RUN_FINISHED
```

### GraphAgent Interrupt 模式

当外部执行属于 GraphAgent 中的某个节点，并且后端需要从 graph checkpoint 恢复时，使用该模式。对应 graph 节点调用 `graph.Interrupt` 暂停执行，等待调用方回传结果。服务端开启 `agui.WithGraphNodeInterruptActivityEnabled(true)` 后，`graph.node.interrupt` 事件会携带 `lineageId` 与 `checkpointId`，调用方据此定位下一次请求的恢复点。

第一次请求使用 `role=user`。LLM 节点输出 `TOOL_CALL_START`、`TOOL_CALL_ARGS` 和 `TOOL_CALL_END`；随后 graph 进入触发中断的工具节点，输出 `ACTIVITY_DELTA graph.node.interrupt`，并在 `RUN_FINISHED` 后结束本次 SSE。调用方在事件流中获取外部工具的 `toolCallId`、工具参数、`lineageId` 和 `checkpointId`。

第二次请求使用 `role=tool`。请求中的 `toolCallId` 对应第一次请求中的工具调用，`content` 为工具输出字符串，`forwardedProps.lineage_id` 与 `forwardedProps.checkpoint_id` 分别来自第一次中断事件返回的 `lineageId` 与 `checkpointId`。`RunOptionResolver` 将工具结果转换为 graph resume 信息，通常以 `graph.Command{ResumeMap: ...}` 传给 GraphAgent。服务端发送 `TOOL_CALL_RESULT`，写入会话历史，并从对应 checkpoint 恢复继续生成最终回复。

GraphAgent 的恢复契约由 graph 定义。被中断节点通过 `ResumeMap` key 消费回传结果；单个 pending tool call 对应一个工具结果。一次中断如果包含多个 pending tool call，对应的多个工具结果由 graph 层的 `ResumeMap` 契约消费。graph 同时混用服务端执行工具和调用方执行工具时，推荐拆成独立阶段，使中断节点只负责调用方回传结果，内部工具执行保留在常规 graph tools 路径上。

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
      "id": "tool-result-<toolCallId>",
      "role": "tool",
      "toolCallId": "<toolCallId>",
      "name": "<toolName>",
      "content": "tool output as string"
    }
  ]
}
```

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
  → TOOL_CALL_RESULT 由尾部 tool message 生成
  → ACTIVITY_DELTA graph.node.interrupt 恢复确认，开启时出现
  → TEXT_MESSAGE_* 恢复后继续生成
  → RUN_FINISHED
```

### AG-UI `role=tool` 输入处理

`role=tool` 输入的请求结构可参考 [外部工具结果输入](#外部工具结果输入)。AG-UI 服务端会读取 `messages` 尾部连续的 `role=tool` message，作为当前工具结果输入批次。

如果一次事件流返回了多个需要调用方执行的 tool call，后续请求可以在 `messages` 尾部按顺序放置多条 `role=tool` message，每条对应一个 `toolCallId`。

`RunOptionResolver` 同时返回 `agent.WithUserMessageRewriter` 时，用户 rewriter 会先执行。rewriter 返回的非 tool message 会保留在最终工具结果块之前；rewriter 返回的 tool message 如果与某个 AG-UI `toolCallId` 对应，会替换该工具调用的请求结果。AG-UI 会按请求尾部 tool message 的顺序放置最终工具结果块。

如果希望 `role=tool` 输入回显经过 Translator，可以开启 `agui.WithToolResultInputTranslationEnabled(true)`。开启后，AG-UI 服务端会先把每条工具结果输入规范化为内部事件，再交给 Translator 处理，示例如下。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

server, err := agui.New(
    runner,
    agui.WithToolResultInputTranslationEnabled(true),
)
```

## GraphAgent 节点活动事件

在 `GraphAgent` 场景下，单次运行通常会按图执行多个节点。框架可以额外发送 Activity 事件，用于让前端展示节点执行进度，以及渲染 Human-in-the-Loop 中断状态。该能力默认关闭，可在创建 AG-UI Server 时按需开启。

`ACTIVITY_DELTA` 事件格式可参考 [AG-UI 官方文档](https://docs.ag-ui.com/concepts/events#activitydelta)

### 节点生命周期（`graph.node.lifecycle`）

节点生命周期事件用于表示 graph 节点的执行阶段。创建 AG-UI Server 时通过 `agui.WithGraphNodeLifecycleActivityEnabled(true)` 开启：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
	runner,
	agui.WithGraphNodeLifecycleActivityEnabled(true),
)
```

开启后，节点在 `start`、`complete` 和 `error` 阶段都会发送 `ACTIVITY_DELTA`。`activityType` 固定为 `graph.node.lifecycle`，`patch` 会通过 `add /node` 写入当前节点状态，其中 `phase` 表示具体阶段，`error` 只在失败时出现。

示例：

节点开始阶段（`phase=start`）：

```json
{
  "type": "ACTIVITY_DELTA",
  "messageId": "activity-node-1",
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

节点成功结束阶段（`phase=complete`）：

```json
{
  "type": "ACTIVITY_DELTA",
  "messageId": "activity-node-2",
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

节点失败结束阶段（`phase=error`）：

```json
{
  "type": "ACTIVITY_DELTA",
  "messageId": "activity-node-3",
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

前端可以根据 `/node.nodeId` 定位节点，根据 `/node.phase` 更新展示状态。例如 `phase=start` 时高亮节点，`phase=complete` 时标记完成，`phase=error` 时展示 `/node.error`。

### 中断状态（`graph.node.interrupt`）

中断状态事件用于表示 graph 执行暂停和恢复，通过 `agui.WithGraphNodeInterruptActivityEnabled(true)` 开启：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
	runner,
	agui.WithGraphNodeInterruptActivityEnabled(true),
)
```

当节点调用 `graph.Interrupt(ctx, state, key, prompt)` 且当前没有可用的 resume 输入时，框架会发送 `ACTIVITY_DELTA`。`activityType` 固定为 `graph.node.interrupt`，`patch` 会通过 `add /interrupt` 写入中断信息，包含 `nodeId`、`key`、`prompt`、`checkpointId` 与 `lineageId`：

```json
{
  "type": "ACTIVITY_DELTA",
  "messageId": "activity-interrupt-1",
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

该事件表示执行在当前节点暂停。前端可使用 `/interrupt.prompt` 渲染中断提示，并用 `/interrupt.key` 选择需要提供的恢复值。`checkpointId` 与 `lineageId` 可用于定位需要恢复的 checkpoint 并关联多次运行。

当新的运行携带 resume 输入发起恢复时，AG-UI Server 会在该运行的事件流开始处发送一条恢复回执，并且先于任何 `graph.node.lifecycle` 事件发送。恢复回执同样使用 `activityType: graph.node.interrupt`，先将 `/interrupt` 置为 `null`，再通过 `add /resume` 写入本次恢复输入：

```json
{
  "type": "ACTIVITY_DELTA",
  "messageId": "activity-resume-1",
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

如果使用多级 GraphAgent，子图中断会向上冒泡，事件流中默认可能出现多条 `graph.node.interrupt`。如果前端只希望保留用于恢复的最外层中断，可额外开启 `agui.WithGraphNodeInterruptActivityTopLevelOnly(true)`：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
	runner,
	agui.WithGraphNodeInterruptActivityEnabled(true),
	agui.WithGraphNodeInterruptActivityTopLevelOnly(true),
)
```

完整示例可参考 [examples/agui/server/graph](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/graph)，前端渲染与审批交互可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。

## 可观测平台上报

可观测平台通常需要记录本次对话的输入、业务标签和最终输出。AG-UI 可以通过 `RunOptionResolver` 为本次 Agent 运行补充 span 属性，也可以配合事件翻译回调在流式输出结束后写入最终结果。

输入侧可以在 `RunOptionResolver` 中返回 `agent.WithSpanAttributes(...)`，框架会把这些属性写入 Agent 入口 span：

```go
import (
    "go.opentelemetry.io/otel/attribute"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
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

server, err := agui.New(
    runner,
    agui.WithAGUIRunnerOptions(
        aguirunner.WithRunOptionResolver(runOptionResolver),
    ),
)
```

输出侧可以在事件翻译回调 `AfterTranslate` 中累积文本事件，并在输出结束后写入 `trace.output`。这样前端流式事件与后端 trace 可以按同一次运行对齐，便于在可观测平台中同时查看输入和最终输出。

与 Langfuse 可观测平台的结合示例可参考 [examples/agui/server/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/langfuse)。

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

![report](../../assets/gif/agui/report.gif)
