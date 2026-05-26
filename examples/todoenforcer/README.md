# todoenforcer Extension Demo

This example compares the baseline `tool/todo` workflow with the
`agent/extension/todoenforcer` extension.

With `-enforce=true`, the agent cannot emit a final response while
the current invocation still has open todo items. It must either:

- finish the list; or
- call `todo_declare_blocker` with a concrete external blocker.

## Run

```bash
cd examples/todoenforcer
export OPENAI_API_KEY="your-key"
# Optional for an OpenAI-compatible endpoint:
# export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
```

Baseline, without enforcement:

```bash
go run . -enforce=false \
  -seed "Plan and execute a 4-step deployment: 1) write the deployment config, 2) run unit tests, 3) deploy to staging, 4) verify with smoke test."
```

Hardened, with todoenforcer:

```bash
go run . -enforce=true \
  -seed "Plan and execute a 4-step deployment: 1) write the deployment config, 2) run unit tests, 3) deploy to staging, 4) verify with smoke test."
```

Use `-prefill-todos=true` to simulate resuming a previous turn that
already has open todos:

```bash
go run . -enforce=true -prefill-todos=true \
  -seed "Please continue the work you were doing."
```

## Useful Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-model` | `deepseek-chat` | OpenAI-compatible model name |
| `-variant` | `openai` | OpenAI client variant |
| `-streaming` | `true` | Stream assistant output |
| `-enforce` | `true` | Install the todoenforcer extension |
| `-max-retries` | `3` | Number of blocked final responses before fail-open |
| `-max-tokens` | `4000` | Completion token budget |
| `-prefill-todos` | `false` | Seed a synthetic prior turn with open todos |
| `-seed` | empty | First user message; omit for interactive mode |

Inside interactive mode:

- `/list` prints the current checklist.
- `/exit` quits.

## Reading the Output

- `[tool-call] todo_write ...` — the model created or updated the checklist.
- `[tool-call] todo_declare_blocker ...` — the model declared an external blocker.
- `[enforce] BLOCKED ...` — the model tried to finish with open todos; the extension kept the loop going.
- `[enforce] EXHAUSTED ...` — retry budget was consumed; the extension fail-opened.
- `[enforce] BLOCKER_DECLARED ...` — final responses are allowed for the rest of this invocation.

See also `examples/todo/` for the same checklist tool without enforcement.
Use `todoenforcer.WithTodoTool(todo.New(...))` when you need the
underlying `tool/todo` options.
