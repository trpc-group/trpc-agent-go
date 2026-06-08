# Prompt “Rules” / Context Injection Example

This example demonstrates the “rules” pattern as **prompt scaffolding**:
per-run context that is injected into the model request, but **not persisted**
into the session transcript.

It compares two primitives:

- `agent.WithInjectedContextMessages(...)`: injects context **before session history**
- `agent.WithLateContextMessages(...)`: injects context **near the latest user turn**

The example uses an in-process debug model (no API keys required). It prints the
final `request.Messages` seen by the model for each turn, so you can verify the
ordering and the non-persistence behavior.

## Running

From the `examples` module:

```bash
cd examples
go run ./prompt/rules
```

Or from the repository root (Go 1.20+):

```bash
go -C examples run ./prompt/rules
```

## What to Look For

For each scenario the program runs **two turns** in the same session:

1. Turn 1: inject context (either injected or late)
2. Turn 2: do not inject anything

Because both `WithInjectedContextMessages` and `WithLateContextMessages` are
**per-run and non-persistent**, you should see:

- the injected messages appear in **Turn 1**’s request
- the injected messages **do not** appear as separate messages in **Turn 2**’s request

## File-scoped “Rules” (Product-level)

Many “rules” products support file/path scoping (e.g. only apply rules to
`*.go`). That selection policy lives at the application/product layer. The
framework primitive you typically use after selecting rules is still
`WithLateContextMessages` (Cursor-like semantics).

Example sketch:

```go
func rulesForPath(path string) []model.Message {
  if strings.HasSuffix(path, ".go") {
    return []model.Message{model.NewUserMessage("Go rules: ...")}
  }
  return nil
}

events, _ := r.Run(ctx, userID, sessionID, model.NewUserMessage("..."),
  agent.WithLateContextMessages(rulesForPath("main.go")),
)
_ = events
```

