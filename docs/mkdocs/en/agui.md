# AG-UI Guide

The AG-UI (Agent-User Interaction) protocol is maintained by the open-source [AG-UI Protocol](https://github.com/ag-ui-protocol/ag-ui) project. It enables agents built in different languages, frameworks, and execution environments to deliver their runtime outputs to user interfaces through a unified event stream. The protocol tolerates loosely matched payloads and supports transports such as SSE and WebSocket.

`tRPC-Agent-Go` ships with native AG-UI integration. It provides an SSE server implementation by default, while also allowing you to swap in a custom `service.Service` to use transports like WebSocket and to extend the event translation logic.

## Getting Started

Assuming you already have an agent, you can expose it via the AG-UI protocol with just a few lines of code:

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/agui"
)

// Create the agent.
agent := newAgent()
// Build the Runner that will execute the agent.
runner := runner.NewRunner(agent.Info().Name, agent)
// Create the AG-UI server and mount it on an HTTP route.
server, err := agui.New(runner, agui.WithPath("/agui"))
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
// Start the HTTP listener.
if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
    log.Fatalf("server stopped with error: %v", err)
}
```

Note: If `WithPath` is not specified, the AG-UI server mounts at `/` by default.

A complete version of this example lives in [examples/agui/server/default](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/default).

For an in-depth guide to Runners, refer to the [runner](./runner.md) documentation.

On the client side you can pair the server with frameworks that understand the AG-UI protocol, such as [CopilotKit](https://github.com/CopilotKit/CopilotKit). It provides React/Next.js components with built-in SSE subscriptions. The sample at [examples/agui/client/copilotkit](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/copilotkit) builds a web UI that communicates with the agent through AG-UI, as shown below.

![copilotkit](../assets/img/agui/copilotkit.png)

## Advanced Usage

### Custom transport

The AG-UI specification does not enforce a transport. The framework uses SSE by default, but you can implement the `service.Service` interface to switch to WebSocket or any other transport:

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

### Custom translator

`translator.New` converts internal events into the standard AG-UI events. To enrich the stream while keeping the default behaviour, implement `translator.Translator` and use the AG-UI `Custom` event type to carry extra data:

```go
import (
    aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
    agentevent "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/runner"
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

runner := runner.NewRunner(agent.Info().Name, agent)
server, _ := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithTranslatorFactory(factory)))
```

For example, when using React Planner, if you want to apply different custom events to different tags, you can achieve this by implementing a custom Translator, as shown in the image below.

![copilotkit-react](../assets/img/agui/copilotkit-react.png)

You can find the complete code example in [examples/agui/server/react](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/react).

### Custom `UserIDResolver`

By default every request maps to the fixed user ID `"user"`. Implement a custom `UserIDResolver` if you need to derive the user from the `RunAgentInput`:

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

### Event Translation Callback

AG-UI provides an event translation callback mechanism, allowing custom logic to be inserted before and after the event translation process.

- `translator.BeforeTranslateCallback`: Triggered before the internal event is translated into an AG-UI event. The return value convention:
  - Return `(customEvent, nil)`: Use `customEvent` as the input event for translation.
  - Return `(nil, nil)`: Retain the current event and continue with the subsequent callbacks. If all callbacks return `nil`, the original event will be used.
  - Return an error: Terminates the current execution, and the client will receive a `RunError`.
- `translator.AfterTranslateCallback`: Triggered after the AG-UI event translation is completed and just before it is sent to the client. The return value convention:
  - Return `(customEvent, nil)`: Use `customEvent` as the final event to be sent to the client.
  - Return `(nil, nil)`: Retain the current event and continue with the subsequent callbacks. If all callbacks return `nil`, the original event will be sent.
  - Return an error: Terminates the current execution, and the client will receive a `RunError`.

Usage Example:

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
        // Logic to execute before event translation
        return nil, nil
    }).
    RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
        // Logic to execute after event translation
        if msg, ok := event.(*aguievents.TextMessageContentEvent); ok {
            // Modify the message content in the event
            return aguievents.NewTextMessageContentEvent(msg.MessageID, msg.Delta+" [via callback]"), nil
        }
        return nil, nil
    })

server, err := agui.New(runner, agui.WithAGUIRunnerOptions(aguirunner.WithTranslateCallbacks(callbacks)))
```

Event translation callbacks can be used in various scenarios, such as:

- Custom Event Handling: Modify event data or add additional business logic during the translation process.
- Monitoring and Reporting: Insert monitoring and reporting logic before and after event translation. A full example of integrating with Langfuse observability platform can be found at [examples/agui/server/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/langfuse).
