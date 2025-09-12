# Custom Agent

If you don’t want to start with Graph or multi-Agent orchestration and prefer embedding LLM into your existing service logic, implement the `agent.Agent` interface directly and control the flow yourself.

This example shows a small “intent branching” agent:

- First classify intent using the LLM: `chitchat` or `task`
- If chitchat: reply conversationally
- If task: output a short actionable plan (in real apps, you can route to tools or downstream services)

## When to choose a custom Agent

- Logic is simple but you need precise control (validation, fallbacks, branching)
- You don’t need visual orchestration or complex teams yet (you can later evolve to Chain/Parallel/Graph)

## What to implement

You must implement:

- `Run(ctx, *Invocation) (<-chan *event.Event, error)`: execute your flow and emit events (forward model streaming to events)
- `Tools() []tool.Tool`: return available tools (empty if none)
- `Info() Info`: basic agent info
- `SubAgents()/FindSubAgent()`: return empty/nil if not used

Core pattern:

1) Use `invocation.Message` as user input

2) Share framework capabilities via `invocation` (Session, Callbacks, Artifact, etc.)

3) Call `model.Model.GenerateContent(ctx, *model.Request)` for streaming responses; forward via `event.NewResponseEvent(...)`

## Code example

Full example: `examples/customagent`

Key snippet (simplified):

```go
type SimpleIntentAgent struct {
    name        string
    description string
    model       model.Model
}

func (a *SimpleIntentAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
    out := make(chan *event.Event, 64)
    go func() {
        defer close(out)
        intent := a.classifyIntent(ctx, inv) // chitchat | task
        if intent == "task" {
            a.replyTaskPlan(ctx, inv, out)
        } else {
            a.replyChitChat(ctx, inv, out)
        }
    }()
    return out, nil
}

func (a *SimpleIntentAgent) replyChitChat(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event) {
    req := &model.Request{
        Messages: []model.Message{
            model.NewSystemMessage("Be concise and friendly."),
            inv.Message,
        },
        GenerationConfig: model.GenerationConfig{Stream: true},
    }
    rspCh, _ := a.model.GenerateContent(ctx, req)
    for rsp := range rspCh {
        out <- event.NewResponseEvent(inv.InvocationID, a.name, rsp)
    }
}
```

## Runner integration

While you can call Agent directly, we recommend running agents via `Runner` which manages session and appends events for you.

Example:

```go
// Build model and agent
m := openai.New("deepseek-chat")
ag := NewSimpleIntentAgent("biz-agent", "intent branching", m)

// Run with Runner
r := runner.NewRunner("customagent-app", ag)
ch, err := r.Run(ctx, "user-001", "session-001", model.NewUserMessage("Hi there"))
// consume events...
```

## Run the example (interactive)

```bash
cd examples/customagent
export OPENAI_API_KEY="your_api_key"
go run . -model deepseek-chat

# Inside the interactive session:
# /history  - Ask to show conversation history
# /new      - Start a new session
# /exit     - Quit
```

## Extensions

- Add tools: return `[]tool.Tool` (e.g., `function.NewFunctionTool(...)`) to call DB/HTTP/internal services
- Add validation: enforce checks and guards before branching
- Evolve gradually: when if-else grows or you need collaboration, move to `ChainAgent`/`ParallelAgent` or `Graph`
