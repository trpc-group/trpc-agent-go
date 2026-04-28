# Todo Tool Demo

A minimal multi-turn chat that exercises the `tool/todo` package end to end.

The agent is given a single tool — `todo_write` — and a system instruction
that explains when and how to use it. After every assistant turn the
example reads the checklist out of the session store and prints it in
the terminal, so you can watch the plan evolve turn by turn.

## What this demo shows

- Registering `todo.New()` as a plain LLM tool.
- Persisting the checklist through the tool's `StateDeltaForInvocation`
  hook, so it survives across `Runner.Run` invocations.
- Two ways of consuming the list:
  - in-stream, decoding `todo.Output` from each tool-result event
    (what a cloud/AG-UI frontend will do);
  - end-of-turn, reading the canonical state with `todo.GetTodos` and
    rendering it via this demo's local `formatTodos` helper (what a
    REST endpoint or audit job would do).
- Attaching a custom `NudgeHook` — in this example, a reminder to
  summarise the outcome once every task is completed.

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or a compatible endpoint)

## Environment Variables

| Variable          | Description                                    | Default Value               |
| ----------------- | ---------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service                  | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint            | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument     | Description                                           | Default Value   |
| ------------ | ----------------------------------------------------- | --------------- |
| `-model`     | Name of the model to use (any OpenAI-compatible id)   | `deepseek-chat` |
| `-variant`   | Variant passed to the OpenAI provider                 | `openai`        |
| `-streaming` | Enable streaming mode for responses                   | `true`          |
| `-seed`      | Optional first user message; auto-starts the chat     | ``              |

## Usage

### Basic Chat

```bash
cd examples/todo
export OPENAI_API_KEY="your-api-key-here"
go run .
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run . -model gpt-4o
```

### Auto-start with a Seed Prompt

```bash
go run . -streaming=false \
  -seed "Plan 3 small steps with todo_write: 1) greet, 2) define dark mode in one sentence, 3) suggest 2 CSS variables. Execute one by one, updating the checklist each time."
```

### Response Modes

Choose between streaming and non-streaming responses:

```bash
# Default streaming mode (real-time character output)
go run .

# Non-streaming mode (complete response at once)
go run . -streaming=false
```

Interactive commands inside the chat:

- `/list` – print the current checklist on demand.
- `/exit` – leave the chat.

## Example output

```text
You (seed): Plan 3 small steps with todo_write: 1) greet, 2) define dark mode in 1 sentence, 3) suggest 2 CSS variables. Then execute them one by one, updating the list each time.
Assistant:   [tool-call] todo_write {"todos":[
  {"content":"Greet the user","activeForm":"Greeting the user","status":"in_progress"},
  {"content":"Define dark mode in 1 sentence","activeForm":"...","status":"pending"},
  {"content":"Suggest 2 CSS variables","activeForm":"...","status":"pending"}
]}
  [tool-result] {"message":"Todos have been modified successfully. ..."}
  ... (three more tool-calls as the model flips each step to completed) ...
  [tool-result] {"message":"Todos have been modified successfully. ...

Reminder: all tasks are marked completed. Before finishing, briefly summarise the outcome for the user."}

A: All steps completed! I've greeted you, defined dark mode as a color scheme using light elements on dark backgrounds, and suggested two CSS variables: `--background-color` and `--text-color`.

----- Current checklist -----
- [x] Greet the user
- [x] Define dark mode in 1 sentence
- [x] Suggest 2 CSS variables
-----------------------------
```

## Multi-turn: pause, ask, resume

Real agents often need to stop mid-plan to ask the user something — a
permission, a preference, a missing fact — and then pick up exactly
where they left off. The todo tool is designed for this: the checklist
survives across `Runner.Run` invocations, and the tool reports the
pre-write list as `oldTodos` so downstream consumers can render a
precise diff.

Seed prompt (shortened):

> Plan a 3-section blog post (Intro, Main Body, Conclusion). Mark Intro
> in_progress, write it, flip to completed and Main Body to in_progress.
> **Before writing the Main Body, ASK me for the mood I want and STOP.**

### Turn 1 — partial progress, then pause

```text
You (seed): I'm writing a 3-section blog post ... ASK ME ... then STOP.
Assistant:
  [tool-call] todo_write { ... Intro in_progress, Main Body pending, Conclusion pending ... }
  [tool-result decoded] 3 todos (was 0)
    - [in_progress] Write Intro paragraph
    - [pending]     Write Main Body section
    - [pending]     Write Conclusion paragraph

  ... (Intro paragraph streamed as plain text) ...

  [tool-call] todo_write { ... Intro completed, Main Body in_progress ... }
  [tool-result decoded] 3 todos (was 3)
    - [completed]   Write Intro paragraph
    - [in_progress] Write Main Body section
    - [pending]     Write Conclusion paragraph

A: Before I write the Main Body section, what mood/aesthetic would you
   like for it?

----- Current checklist -----
- [x] Write Intro paragraph
- [>] Write Main Body section
- [ ] Write Conclusion paragraph
-----------------------------
```

The end-of-turn block is printed by `printTodos()` reading session
state via `todo.GetTodos(sess, agentName)`. Turn 1 ends with the
Main Body item truly in `in_progress`.

### Turn 2 — user answers, agent resumes from the same list

```text
You: Autumn vibes, 2 candles, warm yellow lighting
Assistant:
  [tool-call] todo_write { ... Main Body completed, Conclusion in_progress ... }
  [tool-result raw] {
    "message":"Todos have been modified successfully. ...",
    "todos":    [ ... Intro done, Main Body done, Conclusion in_progress ... ],
    "oldTodos": [
      {"content":"Write Intro paragraph",      "status":"completed"},
      {"content":"Write Main Body section",    "status":"in_progress"},
      {"content":"Write Conclusion paragraph", "status":"pending"}
    ]
  }
  [tool-result decoded] 3 todos (was 3)
    - [completed]   Write Intro paragraph
    - [completed]   Write Main Body section
    - [in_progress] Write Conclusion paragraph

  ... (Main Body paragraph streamed; then todo_write flipping Conclusion to completed) ...

A: All three sections of your blog post are complete! ...

----- Current checklist -----
- [x] Write Intro paragraph
- [x] Write Main Body section
- [x] Write Conclusion paragraph
-----------------------------
```

The `oldTodos` field on the very first tool-result of Turn 2 is **byte-
identical to the final state of Turn 1**. That proves two independent
pieces of the plumbing:

1. `StateDeltaForInvocation` flushed Turn 1's list into the canonical
   session store at the end of the previous run.
2. On Turn 2, the tool's `Call()` re-read that persisted state (via
   `readTodos(sess, key)`) to compute the diff — the agent did not
   re-plan from scratch, nor did it invent a new list.

Real-world analogues where this matters:

- The agent needs a credential / permission and pauses for approval.
- Two valid implementation paths exist; the agent asks the user to
  choose.
- A destructive operation is about to run; the agent stops for
  confirmation.
- The user explicitly batches work: "do the first three steps now,
  I'll come back for the rest later."
- The user closes the browser and resumes tomorrow on the same
  session ID.

All of these boil down to "the list must still be there next time
`Runner.Run` is called" — which is exactly what this demo verifies.

## Notes

- **Branch scoping.** The tool writes to the key
  `temp:todos[:<branch>]`. When the branch field of the invocation is
  empty it defaults to the agent's name, so this demo reads back with
  `todo.GetTodos(sess, agentName)`.
- **Clear-on-all-done.** By default the tool wipes the list once every
  item is completed (so a new planning cycle starts from scratch). The
  demo passes `todo.WithClearOnAllDone(false)` to keep the completed
  items visible in the final render.
- **Tool contract vs. prompt guidance — know the boundary.** The
  default prompt (`todo.DefaultToolPrompt`) and the runtime validator
  (`validateTodos`) talk about overlapping concerns, but they are not
  the same thing:
  - **Hard tool contract (enforced in code, tool call fails on
    violation):**
    - The `todos` field is required and must be an array. A missing
      field or an explicit `null` is rejected at the runtime edge —
      not silently treated as "clear all". The legitimate clear
      gesture is an explicit empty array, `{"todos": []}`. This
      asymmetry is on purpose: a destructive operation should require
      an explicit token, so that an upstream provider, retry
      middleware, or hand-rolled caller that accidentally drops the
      field cannot wipe a session's plan in one shot.
    - Every item must carry a non-empty `content`, a non-empty
      `activeForm`, and a valid `status`
      (`pending` / `in_progress` / `completed`).
    - **At most one** item may be `in_progress` at a time.
    - `content` strings must be unique across the list (exact string
      match — no trim / case-fold / unicode-normalisation on purpose,
      to avoid silently merging items the model meant to keep
      distinct).

  Why explicit `[]` is the only clear path: the tool _is_ the
  legitimate way to clear a checklist (e.g. after all items
  completed under `WithClearOnAllDone(false)`, or when a plan is
  abandoned and replaced), but only when the caller explicitly says
  so. Forcing the empty-array spelling preserves the symmetry the
  schema declares (`required: ["todos"]`, type: array) and matches
  how the same shape is enforced for any structured tool input.
  - **Prompt-only guidance (the model is encouraged to follow, but
    the tool will not reject it):** keeping exactly one item
    `in_progress` while actively working, not leaving stale
    `in_progress` items across turns, avoiding multi-step work that
    never produces a checklist, tone/phrasing of `activeForm`, and
    the few-shot examples about when `todo_write` is worth the call.

  The split is intentional: the contract stays small so it never
  fights a well-behaved agent, while the prompt can evolve with
  taste and model-family idiosyncrasies without touching the tool
  schema. If you ever find yourself wanting to enforce more in code
  (e.g. "exactly one in_progress when the list is non-empty"), do
  it by adding a validator, not by rewriting the prompt: keep the
  two layers distinguishable.

## Where to look next

- `tool/todo/todo.go` – the `Tool` implementation, default nudge, and
  the `StateDeltaForInvocation` hook that persists the list.
- `tool/todo/options.go` – `WithNudgeHook`, `WithStateKeyPrefix`,
  `WithClearOnAllDone`, etc.
- `tool/todo/helpers.go` – `GetTodos` / `GetTodosWithPrefix` for
  server-side reads of the current list. The package ships no
  pretty-printer on purpose: terminal demos like this one inline a
  small ASCII formatter (`formatTodos` in `main.go`), rich frontends
  render `Output.Todos` in their own native style.
- `tool/todo/prompt.go` – `DefaultToolPrompt` injected into the system
  instruction of this demo.
