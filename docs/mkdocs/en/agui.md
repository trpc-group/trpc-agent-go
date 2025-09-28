# AG-UI Guide

The AG-UI (Agent-User Interaction) protocol is maintained by the open-source community at [AG-UI Protocol](https://github.com/ag-ui-protocol/ag-ui). It enables agents implemented in different languages, frameworks, and execution environments to deliver the outputs generated during a run to user interfaces through a unified event stream. The protocol tolerates loosely-matched event formats and supports multiple transports including SSE and WebSocket.

`tRPC-Agent-Go` integrates with the AG-UI protocol, providing an SSE server implementation out of the box. It also lets you swap in other transports (e.g. WebSocket) by supplying a custom `service.Service`, and plug in bespoke event translators when you need to enrich AG-UI events.

## Getting Started

Assuming you have implemented an Agent, you can integrate with the AG-UI protocol and start the service as follows:

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

// Create your agent.
agent := newAgent()
// Create the AG-UI server and mount it onto an HTTP route.
server, err := agui.New(agent, agui.WithPath("/agui"))
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
// Start the HTTP server.
if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
    log.Fatalf("server stopped with error: %v", err)
}
```

See the full example at [examples/agui/server/default](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/default).

On the client side you can pair this with frameworks such as [CopilotKit](https://github.com/CopilotKit/CopilotKit), which provides React/Next.js components and built-in SSE subscriptions for AG-UI streams. [examples/agui/client/copilotkit](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/copilotkit) uses CopilotKit to build a Web UI interface and communicate with the Agent through the AG-UI protocol. The effect is shown in the figure below.

![copilotkit](../assets/img/agui/copilotkit.png)

## Runner Integration

You can inject `runner.Option` through `agui.WithRunnerOptions` to set components such as Session/Memory. Take Session as an example:

```go
import (
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    agui "trpc.group/trpc-go/trpc-agent-go/server/agui"
    runner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

// Create Agent.
agent := newAgent()
// Create Session Service.
sessionService := sessioninmemory.NewSessionService()
// Create AG-UI Server.
server, err := agui.New(
    agent,
    agui.WithPath("/agui"), // Mount onto an HTTP route.
    agui.WithRunnerOptions(runner.WithSessionService(sessionService)), // Injecting Session Service.
)
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
// Start the HTTP server.
if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
    log.Fatalf("server stopped with error: %v", err)
}
```

## Advanced Usage

### Custom communication protocols

The AG-UI protocol does not mandate a specific transport. This framework uses SSE as the default. If you want to switch to WebSocket or other protocols, implement the `service.Service` interface yourself:

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

type wsService struct{}

func (s *wsService) Handler() http.Handler { /* register WebSocket and stream events */ }

server, _ := agui.New(agent, agui.WithService(&wsService{}))
```

### Custom translator

The default `translator.New` converts internal events into the canonical AG-UI events. To augment the stream while keeping the default behaviour, implement the `translator.Translator` interface and use AG-UI `Custom` events to carry extra information:

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

### Custom `UserIDResolver`

By default every request maps to the fixed userID `"user"`. Override `UserIDResolver` to derive the user ID from `RunAgentInput`:

```go
resolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    if user, ok := input.ForwardedProps["userId"].(string); ok && user != "" {
        return user, nil
    }
    return "anonymous", nil
}

server, _ := agui.New(agent, agui.WithAGUIRunnerOptions(aguirunner.WithUserIDResolver(resolver)))
```
