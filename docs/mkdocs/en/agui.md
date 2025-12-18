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

func (t *customTranslator) Translate(ctx context.Context, evt *agentevent.Event) ([]aguievents.Event, error) {
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

factory := func(ctx context.Context, input *adapter.RunAgentInput) translator.Translator {
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

### Custom `RunOptionResolver`

By default, the AG-UI Runner does not append extra `agent.RunOption`s to the underlying `runner.Run`. Implement `RunOptionResolver`, inject it with `aguirunner.WithRunOptionResolver`, and translate client-provided configuration (for example, `modelName` or `knowledgeFilter`) from `ForwardedProps`.

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

`RunOptionResolver` executes for every incoming `RunAgentInput`. Its return value is forwarded to `runner.Run` in order. Returning an error surfaces a `RunError` to the client, while returning `nil` means no extra options are added.

### Observability Reporting

AG-UI Runner exposes `WithStartSpan` to create a tracing span at the beginning of each run and attach request attributes.

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

// Custom StartSpan: set threadId/userId/input as span attributes
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

Pair this with an `AfterTranslate` callback to accumulate output and write it to `trace.output`. This keeps streaming events aligned with backend traces so you can inspect both input and final output in your observability platform. 

For a Langfuse-specific example, see [examples/agui/server/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/langfuse).

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

### RunAgentInput Hook

You can use `WithRunAgentInputHook` to mutate the AG-UI request before it reaches the runner. The following example reads `other_content` from `ForwardedProps` and appends it to the latest user message:

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

Key points:

- Returning `nil` keeps the original input object while preserving in-place edits.
- Returning a custom `*adapter.RunAgentInput` replaces the original input; returning `nil` keeps it.
- Returning an error aborts the request and the client receives a `RunError` event.

### Session Storage and Event Aggregation

When constructing the AG-UI Runner, pass in `SessionService`. Events generated by real-time conversations will be written into the session through `SessionService`, making it convenient to replay the history later via `MessagesSnapshot`.

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

In streaming response scenarios, a single reply usually consists of multiple incremental text events. Writing all of them directly into the session can put significant pressure on `SessionService`.

To address this, the framework first aggregates events and then writes them into the session. In addition, it performs a periodic flush once per second by default, and each flush writes the current aggregation result into the session.

* `aggregator.WithEnabled(true)` is used to control whether event aggregation is enabled. It is enabled by default. When enabled, it aggregates consecutive text events that share the same `messageId`. When disabled, no aggregation is performed on AG-UI events.
* `aguirunner.WithFlushInterval(time.Second)` is used to control the periodic flush interval of aggregated results. The default is 1 second. Setting it to 0 disables the periodic flush mechanism.

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
        aguirunner.WithAggregationOption(aggregator.WithEnabled(true)), // Enable event aggregation, enabled by default
        aguirunner.WithFlushInterval(time.Second),                      // Set the periodic flush interval for aggregation results, default is 1 second
    ),
)
```

If more complex aggregation strategies are required, you can implement `aggregator.Aggregator` and inject it through a custom factory. Note that although an aggregator is created separately for each session, avoiding cross-session state management and concurrency handling, the aggregation methods themselves may still be called concurrently, so concurrency must still be handled properly.

For example, while remaining compatible with the default text aggregation, you can accumulate the content of custom events of type `"think"` and then persist them.

A complete example can be found at [examples/agui/server/thinkaggregator](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/thinkaggregator)

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
	// Use a mutex to ensure concurrent safety of the inner aggregator and the think buffer, avoiding race conditions when multiple events are appended concurrently.
	c.mu.Lock()
	defer c.mu.Unlock()

	// For custom "think" events, use a strategy of "accumulate only, do not emit immediately":
	// 1. Force flush inner first to fully emit previously accumulated normal events.
	// 2. Append the current think content to the buffer only, without returning it immediately.
	// This ensures that think fragments are not interrupted by normal events, and that previous normal events are emitted before new thinking content.
	if custom, ok := event.(*aguievents.CustomEvent); ok && custom.Name == string(thinkEventTypeContent) {
		flushed, err := c.inner.Flush(ctx)
		if err != nil {
			return nil, err
		}
		// Only participate in accumulation when the value is a string, to avoid polluting the buffer with values of unexpected types.
		if v, ok := custom.Value.(string); ok {
			c.think.WriteString(v)
		}
		// The current think event only participates in internal accumulation and is not immediately visible externally. The return value is the previously aggregated results from inner.
		return flushed, nil
	}

	// Non-think events follow the default inner aggregation logic to ensure that the original text aggregation behavior is preserved.
	events, err := c.inner.Append(ctx, event)
	if err != nil {
		return nil, err
	}

	// If there is no accumulated think content, directly return the aggregation results from inner without additional wrapping.
	if c.think.Len() == 0 {
		return events, nil
	}

	// If there is accumulated think content, insert a single aggregated think event before the current batch of events:
	// 1. Package the buffer content into a single CustomEvent.
	// 2. Clear the buffer to avoid sending duplicate content.
	// 3. Place this think event before the current events to preserve the time order: output the complete thinking content first, then subsequent events.
	think := aguievents.NewCustomEvent(string(thinkEventTypeContent), aguievents.WithValue(c.think.String()))
	c.think.Reset()

	out := make([]aguievents.Event, 0, len(events)+1)
	out = append(out, think)
	out = append(out, events...)
	return out, nil
}

func (c *thinkAggregator) Flush(ctx context.Context) ([]aguievents.Event, error) {
	// Flush likewise needs to ensure concurrent safety of the inner aggregator and the think buffer.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Flush inner first to ensure that all normal events are emitted according to its own aggregation strategy.
	events, err := c.inner.Flush(ctx)
	if err != nil {
		return nil, err
	}

	// If there is still unflushed content in the think buffer,
	// wrap it into a single aggregated think event and insert it at the front of the current batch,
	// ensuring that the aggregated thinking content is not reordered by events produced by subsequent flushes.
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

### Message Snapshot

Message snapshots restore historical conversations when a page is first opened or a connection is re-established. The feature is controlled by `agui.WithMessagesSnapshotEnabled(true)` and is disabled by default. Once enabled, AG-UI exposes both the chat route and the snapshot route:

- The chat route defaults to `/` and can be customised with `agui.WithPath`;
- The snapshot route defaults to `/history`, can be customised with `WithMessagesSnapshotPath`, and returns the event flow `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED`.

When enabling message snapshots, configure the following options:

- `agui.WithMessagesSnapshotEnabled(true)` enables the snapshot endpoint;
- `agui.WithMessagesSnapshotPath` sets the custom snapshot route, defaulting to `/history`;
- `agui.WithAppName(name)` specifies the application name;
- `agui.WithSessionService(service)` injects the `session.Service` used to look up historical events;
- `aguirunner.WithUserIDResolver(resolver)` customises how `userID` is resolved, defaulting to `"user"`.

While serving a snapshot request, the framework parses `threadId` from the `RunAgentInput` as the `SessionID`, combines it with the custom `UserIDResolver` to obtain `userID`, and then builds a `session.Key` together with `appName`. It queries the session through `session.Service`, converts the stored events (`Session.Events`) into AG-UI messages, and wraps them in a `MESSAGES_SNAPSHOT` event alongside matching `RUN_STARTED` and `RUN_FINISHED` events.

Example:

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
    agui.WithPath("/chat"),                    // Custom chat route, defaults to "/"
    agui.WithAppName("demo-app"),              // AppName used to build the session key
    agui.WithSessionService(sessionService),   // Session Service used to query sessions
    agui.WithMessagesSnapshotEnabled(true),    // Enable message snapshots
    agui.WithMessagesSnapshotPath("/history"), // Snapshot route, defaults to "/history"
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(resolver), // Custom userID resolver
    ),
)
if err != nil {
	log.Fatalf("create agui server failed: %v", err)
}
if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
	log.Fatalf("server stopped with error: %v", err)
}
```

You can find a complete example at [examples/agui/messagessnapshot](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/messagessnapshot).

The format of AG-UI's MessagesSnapshot event can be found at [messages](https://docs.ag-ui.com/concepts/messages).

### Setting the BasePath for Routes

`agui.WithBasePath` sets the base route prefix for the AG-UI service. The default value is `/`, and it is used to mount the chat route and message snapshot route under a unified prefix, avoiding conflicts with existing services.

`agui.WithPath` and `agui.WithMessagesSnapshotPath` only define sub-routes under the base path. The framework will use `url.JoinPath` to concatenate them with the base path to form the final accessible routes.

Here’s an example of usage:

```go
server, err := agui.New(
    runner,
    agui.WithBasePath("/agui"),                // Set the AG-UI prefix route
    agui.WithPath("/chat"),                    // Set the chat route, default is "/"
    agui.WithMessagesSnapshotEnabled(true),    // Enable message snapshot feature
    agui.WithMessagesSnapshotPath("/history"), // Set the message snapshot route, default is "/history"
)
if err != nil {
    log.Fatalf("create agui server failed: %v", err)
}
```

In this case, the chat route will be `/agui/chat`, and the message snapshot route will be `/agui/history`.

## Best Practices

### Generating Documents

If a long-form document is inserted directly into the main conversation, it can easily “flood” the dialogue, making it hard for users to distinguish between conversation content and document content. To solve this, it’s recommended to use a **document panel** to carry long documents. By defining a workflow in the AG-UI event stream — “open document panel → write document content → close document panel” — you can pull long documents out of the main conversation and avoid disturbing normal interactions. A sample approach is as follows.

1. **Backend: Define tools and constrain call order**

   Provide the Agent with two tools: **open document panel** and **close document panel**, and constrain the generation order in the prompt:
   when entering the document generation flow, execute in the following order:

   1. Call the “open document panel” tool first
   2. Then output the document content
   3. Finally call the “close document panel” tool

   Converted into an AG-UI event stream, it looks roughly like this:

   ```text
   Open document panel tool
     → ToolCallStart
     → ToolCallArgs
     → ToolCallEnd
     → ToolCallResult

   Document content
     → TextMessageStart
     → TextMessageContent
     → TextMessageEnd

   Close document panel tool
     → ToolCallStart
     → ToolCallArgs
     → ToolCallEnd
     → ToolCallResult
   ```

2. **Frontend: Listen for tool events and manage the document panel**

   On the frontend, listen to the event stream:

   * When an `open_report_document` tool event is captured: create a document panel and write all subsequent text message content into that panel.
   * When a `close_report_document` tool event is captured: close the document panel (or mark it as completed).

The effect is shown below. For a full example, refer to
[examples/agui/server/report](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/report).

![report](../assets/gif/agui/report.gif)
