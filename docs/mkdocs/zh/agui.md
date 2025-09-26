# AG-UI 使用指南

AG-UI（Agent-User Interaction）协议由开源社区 [AG-UI Protocol](https://github.com/ag-ui-protocol/ag-ui) 维护，旨在让不同语言、不同框架、不同执行环境的智能体，都能够通过统一的事件流把一次执行过程中产生的内容传递给用户界面，允许松散的事件格式匹配，支持 SSE 和 WebSocket 等多种通信协议。

`tRPC-Agent-Go` 接入了 AG-UI 协议，默认提供 SSE 服务端实现，同时也支持通过自定义 `service.Service` 切换到 WebSocket 等其他通信协议，以及自定义扩展事件翻译逻辑。

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

在前端侧，可以配合 [CopilotKit](https://github.com/CopilotKit/CopilotKit) 等支持 AG-UI 协议的客户端框架，它提供 React/Next.js 组件并内置 SSE 订阅能力。

[examples/agui/client/copilotkit](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/copilotkit) 使用 CopilotKit 搭建了 Web UI 界面，通过 AG-UI 协议与 Agent 通信，效果如下图所示。

![copilotkit](../assets/img/agui/copilotkit.png)

## 进阶用法

### 自定义传输层（`service.Service`）

AG-UI 协议未强制规定通信协议，框架使用 SSE 作为 AG-UI 的默认通信协议，如果希望改用 WebSocket 等其他协议，可以实现 `service.Service` 接口：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

type wsService struct{}

func (s *wsService) Handler() http.Handler { /* 注册 WebSocket 并写入事件 */ }

server, _ := agui.New(agent, agui.WithService(&wsService{}))
```

### 自定义 Translator

默认的 `translator.New` 会把内部事件翻译成协议里定义的标准事件集。若想在保留默认行为的基础上追加自定义信息，可以实现 `translator.Translator` 接口，并借助 AG-UI 的 `Custom` 事件类型携带扩展数据：

```go
import (
    aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
    agentevent "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
    aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
)

type customTranslator struct {
    inner translator.Translator
}

func (t *customTranslator) Translate(evt *agentevent.Event) ([]aguievents.Event, error) {
    out, err := t.inner.Translate(evt)
    if err != nil {
        return nil, err
    }
    if payload := buildCustomPayload(evt); payload != nil {
        out = append(out, aguievents.NewCustomEvent("trace.metadata", aguievents.WithValue(payload)))
    }
    return out, nil
}

func buildCustomPayload(evt *agentevent.Event) map[string]any {
    if evt == nil || evt.Response == nil {
        return nil
    }
    return map[string]any{
        "object":    evt.Response.Object,
        "timestamp": evt.Response.Timestamp,
    }
}

factory := func(input *adapter.RunAgentInput) translator.Translator {
    return &customTranslator{inner: translator.New(input.ThreadID, input.RunID)}
}

server, _ := agui.New(agent, agui.WithAGUIRunnerOptions(aguirunner.WithTranslatorFactory(factory)))
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
