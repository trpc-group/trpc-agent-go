# Late Context Messages (`WithLateContextMessages`) Example

This example demonstrates how to use `agent.WithLateContextMessages(...)` to
implement a common “rules” / “prompt scaffolding” pattern:

- inject per-run context **near the latest user message**
- do **not** persist that injected context into the session transcript

By default, the example uses an in-process debug model (no API keys required).
It prints the final `request.Messages` seen by the model for each turn, so you
can verify the ordering and the non-persistence behavior.

## Running

### Debug mode (default, no network)

From the `examples` module:

```bash
cd examples
go run ./prompt/late_context_messages
```

Or from the repository root (Go 1.20+):

```bash
go -C examples run ./prompt/late_context_messages
```

### Minimal rules selector (business-layer example)

The example includes a tiny “rules selector”: it chooses different rule presets
based on `-target-path` (e.g. `.go` vs `.md`) and then injects them via
`WithLateContextMessages`.

```bash
go -C examples run ./prompt/late_context_messages -target-path main.go
go -C examples run ./prompt/late_context_messages -target-path README.md
```

If you want to override the selected rules, pass `-rules`:

```bash
go -C examples run ./prompt/late_context_messages -target-path main.go -rules $'- Answer in Chinese.\n- Use <= 2 bullet points.'
```

### OpenAI-compatible mode (optional)

Run against a real OpenAI-compatible endpoint while still printing the final
`request.Messages` (via a `BeforeModel` callback):

```bash
export OPENAI_BASE_URL="https://your-openai-compatible-host/v1"
export OPENAI_API_KEY="your-api-key"

go -C examples run ./prompt/late_context_messages -mode openai -model gpt-4o-mini
```

You can also pass `-base-url` / `-api-key` explicitly (they default to
`OPENAI_BASE_URL` / `OPENAI_API_KEY`).

By default this example does **not** set `max_tokens` (it uses the provider
default). If you want an explicit budget, set `-max-tokens` (for OpenAI this
maps to `max_completion_tokens`).

Some models/proxies only support the default temperature. By default this
example leaves temperature unset; if your model supports it, you can set it via
`-temperature` (e.g. `-temperature 0.1`).

## What to Look For

The program runs **two turns** in the same session:

1. Turn 1: pass `WithLateContextMessages(...)` (rules message contains `run=1`)
2. Turn 2: pass `WithLateContextMessages(...)` again (rules message contains `run=2`)

Because `WithLateContextMessages` is **per-run and non-persistent**, you should see:

- the rules message is inserted before the latest user message **on each turn**
- in **Turn 2**, you should see `run=2`, but **not** `run=1` (because turn 1’s rules were not persisted into session history)

## File-scoped “Rules” (Product-level)

Many “rules” products support file/path scoping (e.g. only apply rules to
`*.go`). That selection policy lives at the application/product layer. The
framework primitive you typically use after selecting rules is still
`WithLateContextMessages`.

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

