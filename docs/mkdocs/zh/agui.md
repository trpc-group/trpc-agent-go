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

## 依赖说明

由于 AG-UI 官方仓库尚未合并 Golang SDK 的 PR，我们已经 fork 并修复了相关 BUG。使用时，需要在 go.mod 中添加以下 replace 语句：

```go
replace github.com/ag-ui-protocol/ag-ui/sdks/community/go => github.com/Flash-LHR/ag-ui/sdks/community/go trpc-temp-fix
```

完成后，执行 `go mod tidy` 以更新依赖。

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
