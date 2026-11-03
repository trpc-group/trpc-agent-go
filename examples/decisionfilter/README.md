# Decision-Based Filtering Example

This example demonstrates the three `plugin/toolfilter` integrations, all
backed by a System One decision model (TypeSafe Jev or a
protocol-compatible endpoint):

- **Tool pre-filtering** — `toolfilter.NewToolCallback` returns a
  `model.BeforeModelCallbackStructured` that removes the tools the decision
  model deems irrelevant for the current user turn. Framework workflow
  tools for skills, workspace sessions, session recall, agent transfer,
  user replies and time are never scored or filtered.
- **Skill visibility** — `toolfilter.NewSkillFilter` returns a
  `skill.VisibilityFilter` for `llmagent.WithSkillFilter` that narrows the
  skill overview to the skills relevant for the current run, decided in one
  batch request on the first visibility query.
- **Model routing** — `toolfilter.NewModelRouter` returns an
  `agent.ModelSelector` that picks a cheap or a strong model per run with one
  Choice question.

All three **fail open**: when the decision endpoint is unreachable or returns
an error, the full tool catalog stays visible, all skills stay visible, and
routing falls back to the configured fallback model. The agent then behaves
exactly as if no filter were configured.

The decision context uses recent user and assistant text from session events
matching the framework's default event prefix filter. Tool results are excluded
and the newest dialogue takes priority within a 2400-rune budget. This event
history can differ from the final model request, for example when messages are
injected without being stored in the session or custom filters are configured.

## Prerequisites

- Go 1.24 or later
- An OpenAI-compatible chat endpoint (see the `OPENAI_API_KEY` and
  `OPENAI_BASE_URL` conventions of `examples/toolfilter`)
- A System One decision endpoint and API key

```bash
export TYPESAFE_API_KEY="your-decision-api-key"
```

## Run

```bash
cd examples/decisionfilter
go run main.go
```

The decision endpoint, decision model and the two routing targets can be
overridden:

```bash
go run main.go \
  -decision-url https://api.typesafe.ai \
  -decision-model jev-2025-10-10 \
  -cheap-model deepseek-v4-flash \
  -strong-model deepseek-v4
```

## What to watch for

- `[toolfilter] tool narrowed: asked=9 dropped=[...] saved~... tokens` — printed
  on the first decision by the `WithObserver` callback; later ReAct steps
  replay it by invocation ID. Tools added after that decision stay visible.
- `📋 Tools sent to model: [...]` — printed by the OpenAI request callback;
  compare it with the dropped list to see the narrowing take effect.
- `Ask about cooking` — a skill-irrelevant question; the skill overview in
  the system message shrinks to the relevant skills (visible through the
  `skill_load` tool's enum-free listing or the report line).
- Simple arithmetic questions route to the cheap model; long-context review
  questions route to the strong model.

## Tuning

The shared options apply to all three integrations, for example:

```go
toolfilter.WithThreshold(0.6)      // keep when P(relevant) >= 0.6
toolfilter.WithSlack(0.15)         // fallback to near-line candidates if none pass
toolfilter.WithMinToolCount(8)     // skip small catalogs
toolfilter.WithMaxQuestions(20)    // question budget per decision request
toolfilter.WithKeepAlways("file_read_file", "memory_search") // model-facing names
toolfilter.WithKeepSkills("core")  // protect core skills
```

Application tools, including search and file tools, are filtered unless
protected with `WithKeepAlways`. Use the exact names exposed to the model,
including any tool set prefix. A filtered tool is not automatically recovered
by `tool_search`.

Tool and skill catalogs at or below `WithShortlistLimit` go straight to the
decision model. Larger catalogs use recent user messages for lexical recall;
when recall finds no candidates, the catalog stays visible. An optional
shortlist embedder adds a semantic ranking path.
