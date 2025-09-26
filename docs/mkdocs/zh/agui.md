# AG-UI 使用指南

AG-UI（Agent-User Interaction）协议由开源社区 [AG-UI Protocol](https://github.com/ag-ui-protocol/ag-ui) 维护，旨在让不同语言、不同框架、不同执行环境的智能体，都能够通过统一的事件流把一次执行过程中产生的内容传递给用户界面，允许松散的事件格式匹配，支持 SSE 和 WebSocket 等多种通信协议。

`trpc-agent-go` 接入了 AG-UI 协议，并默认提供了 SSE 服务端实现。

## 快速上手

假设你已经实现了一个 Agent，可以如下所示接入 AG-UI 协议并启动服务：

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

// 创建 Agent
agent := newAgent()
// 创建 AG-UI 服务，指定 HTTP 路由
server, err := agui.New(agent, agui.WithPath("/agui"))
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
// 启动 HTTP 服务
if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
    log.Fatalf("server stopped with error: %v", err)
}
```

完整代码示例参见 [examples/agui/server/default](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/default)。

## 进阶用法

### 自定义传输层（`service.Service`）

AG-UI 协议未强制规定通信协议，框架使用 SSE 作为 AG-UI 的默认通信协议，如果希望改用 WebSocket 等其他协议，可以实现 `service.Service` 接口：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

type wsService struct{}

func (s *wsService) Handler() http.Handler { /* 注册 WebSocket 并写入事件 */ }

server, _ := agui.New(agent, agui.WithService(&wsService{}))
```

### 自定义 Bridge

默认的 `event.NewBridge` 会把内部事件翻译成协议里定义的标准事件集。若想在保留默认行为的基础上追加自定义信息，可以实现 Bridge 接口，并借助 AG-UI 的 `Custom` 事件类型携带扩展数据：

```go
import (
    "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

type customBridge struct {
    inner event.Bridge
}

func (b *customBridge) NewRunStartedEvent() events.Event         { return b.inner.NewRunStartedEvent() }
func (b *customBridge) NewRunErrorEvent(msg string) events.Event { return b.inner.NewRunErrorEvent(msg) }
func (b *customBridge) NewRunFinishedEvent() events.Event        { return b.inner.NewRunFinishedEvent() }

func (b *customBridge) Translate(evt *trpcevent.Event) ([]events.Event, error) {
    out, err := b.inner.Translate(evt)
    if err != nil {
        return nil, err
    }
    if payload := buildCustomPayload(evt); payload != nil {
        out = append(out, events.NewCustomEvent("trace.metadata", events.WithValue(payload)))
    }
    return out, nil
}

func buildCustomPayload(evt *trpcevent.Event) map[string]any {
    if evt == nil || evt.Response == nil {
        return nil
    }
    return map[string]any{
        "object":    evt.Response.Object,
        "timestamp": evt.Response.Timestamp.Format(time.RFC3339Nano),
    }
}

factory := func(input *adapter.RunAgentInput) event.Bridge {
    return &customBridge{inner: event.NewBridge(input.ThreadID, input.RunID)}
}

server, _ := agui.New(agent, agui.WithAGUIRunnerOptions(aguirunner.WithBridgeFactory(factory)))
```

### 自定义 `UserIDResolver`

默认所有请求都会归到固定的 `"user"` 会话，可以通过自定义 `UserIDResolver` 从 `RunAgentInput` 中提取 `UserID`：

```go
resolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    if user, ok := input.ForwardedProps["userId"].(string); ok && user != "" {
        return user, nil
    }
    return "anonymous", nil
}

server, _ := agui.New(agent, agui.WithAGUIRunnerOptions(aguirunner.WithUserIDResolver(resolver)))
```
