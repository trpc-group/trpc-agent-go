# Dynamic Sub-Agent Tool Example

This example demonstrates the **dynamic AgentTool** (`agenttool.NewDynamicTool`):
a single, code-defined `dynamic_agent` entrypoint that runs a short-lived
sub-agent whose capability surface (tools and a per-call instruction)
is selected by the model **per invocation**, within a safety boundary you
define in code.

The model-facing tool name is `dynamic_agent`; the *dynamic* part means
per-invocation `instruction` and `tools`/`skills` selection within a
code-defined boundary. The sub-agent is derived from the current agent or a
code-defined template and cannot select arbitrary agents, models, or executors.

Use it when you have a pool of tools but the right subset differs per task, so
predefining one expert sub-agent per combination is impractical: the model
picks the minimal tool set (and a role) for each task, runs it in a short-lived
sub-agent, and the parent only gets the result back — no extra `AgentTool`
boilerplate and no intermediate steps pushed into the parent context.

## How it differs from the other sub-agent mechanisms

| Mechanism | What the model picks | Lifetime | Control returns? |
| --------- | -------------------- | -------- | ---------------- |
| `agenttool.NewTool(agent)` | nothing — one fixed wrapped agent | per call | yes (result is the tool output) |
| `transfer_to_agent` | one of N pre-registered agents | rest of the turn | no — control hands off |
| **`agenttool.NewDynamicTool()`** | **a tool subset + instruction for a sub-agent** | **per call** | **yes (result is the tool output)** |

With the dynamic tool, the model does not choose *which agent* to run — it runs
inside the boundary you configure. It only chooses *which subset of the
permitted tools* the sub-agent may use and *what role* it should take for that
one task.

## What the example builds

The example ships two modes; choose one with `-mode` (default `minimal`).

### `-mode=minimal` (default)

The orchestrator registers the workspace tools **and** `dynamic_agent`:

```text
orchestrator (main agent)
├── calculator        (function tool)
├── current_time      (function tool)
├── word_count        (function tool)
└── dynamic_agent     (dynamic AgentTool)
        └── sub-agent: reuses the orchestrator as its base (no template);
            tools are narrowed per call to a subset of the orchestrator's
            surface, e.g. just ["calculator"]
```

The model can still call the direct tools for trivial one-step answers, but
`dynamic_agent`'s default description tells it to delegate self-contained tool
work and multiple independent subtasks when a separate working context keeps the
parent conversation focused. The point is not that the sub-agent has unique
tools; it isolates the work. With no template the sub-agent reuses the
orchestrator's identity, so its streamed output is not visually separated —
this mode is about the simplest possible onboarding.

### `-mode=bounded`

The orchestrator registers **only** `dynamic_agent`; the workspace tools live
behind its capability surface:

```text
orchestrator (main agent)
└── dynamic_agent     (dynamic AgentTool)
        ├── capability tools: calculator, current_time, word_count
        │     (names appear in the `tools` enum; full schemas stay hidden
        │      from the parent model)
        └── subagent (template: identity + model + default role, no tools)
            └── selected tools injected per call, e.g. just ["calculator"]
```

This is where the dynamic tool earns its keep (see [Bounded mode](#bounded-mode)):
the parent model sees only one `dynamic_agent` entry plus the enumerated tool
names, and the full tool schemas are disclosed only inside the short-lived
sub-agent.

In both modes:

- **Max tool surface** — in `minimal` it is the orchestrator's *current
  effective user tools* at call time (any run-scoped filter applied, run-added
  tools included), minus the dynamic tool itself (no recursion) and
  `transfer_to_agent`; in `bounded` it is exactly the `WithCapabilityTools` set.
  Framework-managed tools (e.g. `knowledge_*`) are not hand-pickable.
- The model **narrows tools** per call with the `tools` field and sets a role
  with the `instruction` field.

## Prerequisites

- Go 1.24.4 or later (the `examples` module requires it)
- A valid API key for an OpenAI-compatible endpoint

## Environment Variables

| Variable          | Description                              | Default                     |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

```bash
export OPENAI_BASE_URL="http://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="<your-key>"
```

The endpoint may serve Claude or OpenAI models (e.g. `gpt-5`,
`claude-4-5-sonnet-20250929`, `deepseek-v4-flash`); select one with `-model`.

## Command Line Arguments

| Argument      | Description                                     | Default                      |
| ------------- | ----------------------------------------------- | ---------------------------- |
| `-model`      | Name of the model to use                        | `claude-4-5-sonnet-20250929` |
| `-mode`       | Demo mode: `minimal` or `bounded`               | `minimal`                    |
| `-show-inner` | Show the sub-agent transcript as it streams     | `true`                       |
| `-inner-text` | Inner text mode: `include` or `exclude`         | `include`                    |
| `-show-tool`  | Show the aggregated `tool.response`             | `false`                      |
| `-debug`      | Prefix streamed lines with the event author     | `false`                      |

## Usage

```bash
cd examples/dynamicagenttool
export OPENAI_API_KEY="<your-key>"

# Minimal mode (default): tools + dynamic_agent on the orchestrator.
go run .

# Bounded mode: only dynamic_agent on the orchestrator; tools behind its
# capability surface (progressive disclosure + least privilege).
go run . -mode=bounded
```

Then try prompts that delegate self-contained subtasks, for example:

- `Use a sub-agent to compute (123 * 456) + 789. Grant it only the calculator.`
- `Use a sub-agent to tell me the current time in UTC.`
- `Use one sub-agent to compute 50 * 12, and a separate sub-agent to count the words in "the quick brown fox jumps". Grant each only the tool it needs.`

## Bounded mode

`minimal` mode is for learning the API. `bounded` mode (`-mode=bounded`) shows
why the *dynamic* tool exists, and is the structure to copy for real apps:

- **Progressive tool disclosure.** The parent model sees only the
  `dynamic_agent` entry. Its `tools` field enumerates the selectable names
  (`calculator`, `current_time`, `word_count`), but the full parameter schemas
  of those tools are never shown to the parent — they are disclosed only inside
  the sub-agent that gets spawned. The parent picks names; the child sees
  schemas.
- **Least-privilege delegation.** The orchestrator cannot call the workspace
  tools directly. Every use goes through a short-lived sub-agent that receives
  only the minimal subset selected for that one task.
- **Code-defined boundary.** The selectable surface is fixed in code with
  `WithCapabilityTools`, and `WithTemplateAgent` pins the sub-agent's model,
  identity, and default role. The model can choose a tool subset and a per-call
  `instruction`, but never an arbitrary agent, model, or executor.

Wiring (see `main.go`'s `buildModeSetup`):

```go
dynamicTool := agenttool.NewDynamicTool(
    agenttool.WithTemplateAgent(subTemplate),      // identity + model boundary
    agenttool.WithCapabilityTools([]tool.Tool{     // the selectable surface
        calculatorTool, timeTool, wordCountTool,
    }),
    // ... result-shaping options ...
)

orchestrator := llmagent.New(
    "orchestrator",
    llmagent.WithTools([]tool.Tool{dynamicTool}),  // ONLY the dynamic tool
    // ...
)
```

## Sample transcript

```text
👤 You: Use one sub-agent to compute 50 * 12, and a separate sub-agent to count
        the words in "the quick brown fox jumps". Grant each only the tool it needs.
🤖 Assistant: I'll create two separate sub-agents, each with only the tool they need.
🔧 Tool calls initiated:
   • dynamic_agent  Args: {"request":"Calculate 50 * 12","tools":["calculator"],
                          "instruction":"You are a calculation assistant..."}
   • dynamic_agent  Args: {"request":"Count the words in \"the quick brown fox jumps\"",
                          "tools":["word_count"],"instruction":"You are a text analysis assistant..."}

🔄 Executing tools...
   • calculator    Args: {"operation":"multiply","a":50,"b":12}
   ✅ {"result":600}
   • word_count    Args: {"text":"the quick brown fox jumps"}
   ✅ {"words":5,"characters":25}
```

Each sub-agent only ever sees the single tool it was granted.

## The `dynamic_agent` input schema

By default the tool exposes:

- `request` (string, required) — the task; the sub-agent is isolated by
  default, so this should be self-contained unless `HistoryScopeParentBranch`
  is configured.
- `instruction` (string, optional) — the sub-agent's role / system prompt for
  this run.
- `tools` (string array, optional) — exact names of the orchestrator's user
  tools to grant; omit to allow all currently-permitted user tools. When the
  surface is fixed in code with `WithCapabilityTools`, those names are
  enumerated in the schema (and appended to this field's description) so the
  model picks from a known set instead of guessing strings; the default
  parent-derived surface and `WithCapabilityProvider` are resolved per call and
  are not enumerated here.

A `skills` field can also be exposed (off by default). When it is, the
sub-agent's skills are drawn from the orchestrator's effective skills (never the
template's own), and selecting a skill that needs a code executor the sub-agent
lacks adds a note to the result. Unknown or unavailable tool/skill names are
ignored with a note appended to the tool result rather than failing the call.

## Defining the safety boundary in code

`minimal` mode uses the default parent-derived tool surface with no template;
`bounded` mode uses `WithTemplateAgent` plus `WithCapabilityTools`. Other knobs
let you tighten or reshape the boundary further:

```go
run := agenttool.NewDynamicTool(
    // Identity + model + default instruction for the sub-agent.
    agenttool.WithTemplateAgent(subTemplate),

    // Optionally fix the exact tools the model may choose from, instead of
    // deriving them from the parent agent. Their names are then enumerated in
    // the `tools` schema, so the model selects from a known set:
    // agenttool.WithCapabilityTools([]tool.Tool{calculatorTool}),
    // or compute them dynamically from the parent invocation:
    // agenttool.WithCapabilityProvider(func(ctx, parent) ([]tool.Tool, map[string]bool) { ... }),

    // Toggle which fields the model may set:
    // agenttool.WithExposeToolSelection(true),  // default
    // agenttool.WithExposeInstruction(true),    // default
    // agenttool.WithExposeSkillSelection(true), // default false

    // Rename the entrypoint (default "dynamic_agent"):
    // agenttool.WithName("explore"),

    // Streaming / result shaping (shared with NewTool):
    agenttool.WithStreamInner(true),
    agenttool.WithInnerTextMode(agenttool.InnerTextModeInclude),
    agenttool.WithSkipSummarization(true),
    agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
)
```

### Safety guarantees

- The model can never select an arbitrary agent, model, or executor — only a
  subset of the permitted tools inside the configured boundary.
- The dynamic tool and `transfer_to_agent` are always removed from the
  sub-agent's surface, preventing runaway recursion.
- The sub-agent is isolated by default (`HistoryScopeIsolated`): it cannot see
  the parent conversation, so the `request` must be self-contained.
