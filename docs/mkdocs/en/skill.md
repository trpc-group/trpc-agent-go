# Skill

Agent Skills package reusable workflows as folders with a `SKILL.md`
spec plus optional docs and scripts. During a conversation, the agent
injects a low‑cost “overview” first, then loads the full body/docs only
when actually needed, and runs scripts inside an isolated workspace.

Background references:
- Engineering blog:
  https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
- Open Skills repository (structure to emulate):
  https://github.com/anthropics/skills

## Overview

### 🎯 What You Get

- 🔎 Overview injection (name + description) to guide selection
- 📥 `skill_load` to pull `SKILL.md` body and selected docs on demand
- 📚 `skill_select_docs` to add/replace/clear docs
- 🧾 `skill_list_docs` to list available docs
- 🧪 Execution: `skill_load` materializes a writable skill working copy
  under `/skills/<name>/`, and scripts are executed via `workspace_exec`
  (available whenever a code executor is configured)
- 🗂️ Output file collection via glob patterns with MIME detection
- 🧩 Pluggable local or container workspace executors (local by default)

### Three‑Layer Information Model

1) Initial “overview” (very low cost)
   - Inject only `name` and `description` from `SKILL.md` into the
     system message so the model knows what skills exist.

2) Full body (on demand)
   - When a task truly needs a skill, the model calls `skill_load`. The
     framework then materializes that skill’s full `SKILL.md` body into
     the next model request (see “Prompt Cache” below).

3) Docs/Scripts (selective + isolated execution)
   - Docs are included only when requested (via `skill_load` or
     `skill_select_docs`). Scripts are not inlined; they are executed
     inside a workspace, returning results and output files.

### Token Cost

If you inline a full skills repo (all `SKILL.md` bodies and docs) into
the prompt up-front, it can dominate your prompt-token budget and even
exceed the model context window.

For a reproducible, **runtime** token comparison (progressive disclosure
vs full injection), see [trpc-agent-go-benchmark/anthropic_skills/README.md](https://github.com/trpc-group/trpc-agent-go-benchmark/blob/main/anthropic_skills/README.md) and run
the `token-report` suite described there.

### Prompt Cache

Some model providers support **prompt caching**: if a later model request
starts with the exact same tokens as an earlier request, the provider
can reuse that shared **prefix** from cache. This reduces work and can
lower latency and/or input token cost (provider-dependent).

For Skills, *where* the loaded `SKILL.md` body/docs land in the message
sequence affects how long the shared prefix is:

- Legacy behavior (default): loaded skill bodies/docs are appended to the
  **system message**.
  - This inserts new tokens **before** the user message and history,
    which can shorten the shared prefix between consecutive model calls.
- Tool-result materialization (optional): loaded skill bodies/docs are
  appended to the matching **tool result** messages (`skill_load` /
  `skill_select_docs`).
  - This keeps the system message stable, so earlier messages are less
    likely to shift, and prompt caching can often reuse a longer prefix.

Fallback: if the matching tool result message is not present in the
request history (for example, history suppression), the framework can
fall back to a dedicated system message so the model still sees the
loaded content.

Session summary note: if you enable session summary injection
(`WithAddSessionSummary(true)`) and a summary is present, the framework
prefers to **skip** this fallback to avoid re-inflating the prompt with
summarized content. If the matching tool result messages are still
available, the fallback stays suppressed; if summary compaction removes
them, the fallback is re-enabled so the model still sees the full
body/docs.

Enable tool-result materialization with:
`llmagent.WithSkillsLoadedContentInToolResults(true)`.
To restore the legacy fallback behavior in summary mode:
`llmagent.WithSkipSkillsFallbackOnSessionSummary(false)`.

To measure the impact in a real tool-using flow, run the
[trpc-agent-go-benchmark/anthropic_skills](https://github.com/trpc-group/trpc-agent-go-benchmark/tree/main/anthropic_skills) `prompt-cache` suite.

How this relates to `SkillLoadMode` (common pitfall):

- The cache-prefix discussion above mostly applies to multiple model
  calls within the same `Runner.Run` (one user message triggers a tool
  loop).
- If you want loaded skill bodies/docs to persist across **multiple
  conversation turns**, set `SkillLoadMode` to `session`. The default
  `turn` mode clears the agent-scoped skill keys
  (`temp:skill:loaded_by_agent:<agent>/<name>` and
  `temp:skill:docs_by_agent:<agent>/<name>`) before the next run starts.
  As a result, even if your history still contains
  the previous `skill_load` tool result (typically a short `loaded:
  <name>` stub), the framework will not materialize the body/docs again.

Practical guidance (especially with
`WithSkillsLoadedContentInToolResults(true)`):

- First, be clear about *which* caching scenario you mean:
  - **Within a single turn** (multiple model calls inside one
    `Runner.Run`): `turn` and `session` behave similarly because both
    keep loaded content available within the run. The more important
    lever is usually *where* you materialize content (system vs tool
    result).
  - **Across turns**: `session` can be more prompt-cache friendly
    because you load once and avoid repeating `skill_load` every turn.
    The trade-off is a larger prompt and the need to manage clearing.
- Rule of thumb:
  - Default to `turn` (least-privilege, smaller prompts, less likely to
    trigger truncation/summary).
  - Use `session` only for a small number of skills you truly need
    across the whole session, and keep docs selection tight.
- Keep docs selection tight (avoid `include_all_docs` when possible).
  Otherwise the prompt can grow quickly and trigger truncation/summary.
  That increases the chance of falling back to a system message, which
  usually reduces prompt-cache benefits.

### Session Persistence

It helps to separate two concepts:

- **Session (persistent):** a stored log of events (user messages,
  assistant messages, tool calls/results) plus a small key/value
  **state map**.
- **Model request (ephemeral):** the `[]Message` array sent to the model
  for *this* call, built from the session + runtime configuration.

`skill_load` stores only small state keys (loaded flag + doc selection).
Request processors then **materialize** the full `SKILL.md` body/docs
into the *next* model request.

Important: materialization does **not** rewrite the session transcript.
So if you inspect stored tool results, `skill_load` typically still looks
like a short stub (for example, `loaded: internal-comms`). The model
still sees the expanded body/docs because they are added when building
the outbound request.

Note: `SkillLoadMode` controls the lifecycle of those state keys, so it
also determines whether the next turn can keep materializing bodies/docs.

Stability across calls:
- Within a tool loop, the materialization is re-applied deterministically
  on every model call, so later requests see the same skill content as
  long as the skills repo and selection state are unchanged.
- If the relevant tool result message is missing from the request history
  (for example, history suppression or truncation), the framework can
  fall back to a dedicated system message (`Loaded skill context:`) to
  preserve correctness. This fallback may reduce prompt-cache benefits
  because it changes the system content. If a session summary is present,
  this fallback is skipped by default (see above).

### Industry Comparison

Many agent frameworks avoid mutating the system prompt mid-loop. Instead,
they fetch dynamic context via a tool and keep that context in **tool
messages**, which is typically more prompt-cache friendly.

Examples:
- OpenClaw: system prompt lists available skills, but the selected
  `SKILL.md` is read via a tool (so the body lands in a tool result):
  https://github.com/openclaw/openclaw/blob/0cf93b8fa74566258131f9e8ca30f313aac89d26/src/agents/system-prompt.ts
- OpenAI Codex: project docs render a skills list and instruct opening
  `SKILL.md` on demand (skill body comes from a file-read tool result):
  https://github.com/openai/codex/blob/383b45279efda1ef611a4aa286621815fe656b8a/codex-rs/core/src/project_doc.rs

In trpc-agent-go:
- Legacy mode appends loaded skill bodies/docs to the **system message**
  (simple and backward-compatible, but can reduce cacheable prefix length).
- Tool-result materialization keeps the **system message stable** and
  attaches loaded skill bodies/docs to `skill_load` / `skill_select_docs`
  tool result messages (closer to the “tool message carries context”
  pattern used above).

### File Layout

```
skills/
  demo-skill/
    SKILL.md        # YAML (name/description) + Markdown body
    USAGE.md        # optional docs (.md/.txt)
    scripts/build.sh
    ...
```

Repository and parsing: [skill/repository.go](https://github.com/trpc-group/trpc-agent-go/blob/main/skill/repository.go)

## Quickstart

### 1) Requirements

- Go 1.21+
- Model provider API key (OpenAI‑compatible)
- Optional Docker for the container executor

Common env vars:

```bash
export OPENAI_API_KEY="your-api-key"
# Optional: read‑only mount for container runtime
export SKILLS_ROOT=/path/to/skills
# Optional: HTTP(S) URL to a skills archive (.zip/.tar.gz/.tgz/.tar)
# export SKILLS_ROOT=https://example.com/skills.zip
# Optional: override cache location for URL roots
# export SKILLS_CACHE_DIR=/path/to/cache
```

### 2) Enable Skills in an Agent

Provide a repository to `LLMAgent`.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    local "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

repo, _ := skill.NewFSRepository("./skills")
exec := local.New()

agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithCodeExecutor(exec),
    llmagent.WithEnableCodeExecutionResponseProcessor(false),
    // Optional: keep the system prompt stable for prompt caching.
    llmagent.WithSkillsLoadedContentInToolResults(true),
)
```

`NewFSRepository` can scan multiple roots. A common pattern is one
shared skills directory plus one user-private skills directory:

```go
repo, _ := skill.NewFSRepository(
    "./skills/common",
    "./skills/users/alice",
)
```

If one long-lived agent serves many requests against a shared
repository view, add a per-run visibility filter. The filter can read
any business signal available in `ctx` / runtime state (for example
`user_id`, `tenant_id`, role, or other request-scoped flags) and hides
non-matching skills from the overview, tool declarations, and runtime
checks. `user_id` below is only one example:

```go
agt := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillFilter(func(ctx context.Context, s skill.Summary) bool {
        userID, _ := agent.GetRuntimeStateValueFromContext[string](ctx, "user_id")
        return allow(userID, s.Name)
    }),
)

r := runner.NewRunner("skills-app", agt)

ch, _ := r.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage("..."),
    agent.WithRuntimeState(map[string]any{"user_id": userID}),
)
```

If a long-lived process installs, deletes, or renames skills after
startup, call `repo.Refresh()` after the filesystem update is committed.
`Refresh()` is meant for repository structure changes, not for every
request.

Fine-grained allowlist (only expose knowledge-injection tools, suitable
for read-only agents):

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithAllowedSkillTools(
        llmagent.SkillToolLoad,
    ),
)
```

Key points:
- Request processor injects overview and on‑demand content:
  [internal/flow/processor/skills.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/internal/flow/processor/skills.go)
- `WithSkills` auto-registers the built-in skill tools (`skill_load`,
  `skill_select_docs`, `skill_list_docs`); no manual wiring is required.
  These are knowledge-injection tools and do not execute scripts
  themselves.
- `WithAllowedSkillTools(...)` can narrow that set further with an
  explicit allowlist, for example `SkillToolLoad` only.
- **Executor auto-fallback**: when `WithSkills(repo)` is configured
  without an explicit `WithCodeExecutor(...)`, the framework auto-wires
  a local code executor so that `workspace_exec` is available out of
  the box. The model then calls `workspace_exec` inside the writable
  working copy under `/skills/<name>/` and follows the steps described
  in `SKILL.md`. The fallback is intentionally skipped in three cases:
  (1) you passed `WithCodeExecutor(...)` (your executor is used as-is);
  (2) you passed `WithAllowedSkillTools(...)` (fine-grained mode, no
  magic); or (3) you explicitly passed
  `WithSkillToolProfile(SkillToolProfileKnowledgeOnly)` (opt-out for
  "no convenience execution wiring"). For production, prefer wiring an
  explicit container-backed executor rather than relying on the local
  fallback — the local fallback is a development convenience, not a
  deployment target.
- **`CodeExecutor` vs fenced-code auto-execution (two separate
  switches)**. Configuring a `CodeExecutor` only makes
  execution *tools* available (for example, `workspace_exec`). It does
  not by itself cause the framework to scan assistant replies for
  Markdown fenced code blocks and run them. That behavior is controlled
  independently by `EnableCodeExecutionResponseProcessor` (default:
  `true`). Two consequences to be aware of:
  - When you explicitly configure `WithCodeExecutor(...)`, the
    fenced-code auto-execution switch keeps its framework default
    unless you set it yourself; if you want the executor *only* for
    `workspace_exec`, pass
    `llmagent.WithEnableCodeExecutionResponseProcessor(false)`.
  - When the skills fallback injects a local executor on your behalf
    (the case above, `WithSkills(repo)` alone), that implicit executor
    is scoped strictly to powering `workspace_exec`: the fallback path
    automatically sets `EnableCodeExecutionResponseProcessor=false`
    unless you explicitly called
    `WithEnableCodeExecutionResponseProcessor(...)` yourself. This
    prevents `WithSkills(repo)` upgrades from quietly enabling fenced
    code auto-execution that was not part of your original
    configuration.
- By default, the framework appends a small `Tooling and workspace guidance:`
  block after the `Available skills:` list in the system message.
  - Disable it (to save prompt tokens): `llmagent.WithSkillsToolingGuidance("")`.
  - Or replace it with your own text: `llmagent.WithSkillsToolingGuidance("...")`.
  - Guidance follows the final registered skill tools, including
    `WithAllowedSkillTools(...)`.
  - If you disable it, make sure your instruction tells the model which
    skill tools are available under your allowlist.
  - Loader: [tool/skill/load.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/load.go)

### 3) Run the Example

GAIA benchmark demo (skills + file tools + `workspace_exec`):
[examples/skill/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skill/README.md)

It includes a dataset downloader script and notes on Python dependencies
for skills like `whisper` (audio) and `ocr` (images).

Real discovery/install demo (real model + real web/GitHub):
[examples/skillfind/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillfind/README.md)

It starts with a built-in `skill-find` skill, searches the public web for
candidate skills, installs a public skill from GitHub into a user-private
directory, refreshes the repository, and uses the new skill in the same
conversation.

SkillLoadMode demo (no API key required):
[examples/skillloadmode/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillloadmode/README.md)

Sub-agent skill isolation demo (AgentTool + Skills):
[examples/skillisolation/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillisolation/README.md)

Quick start (download dataset JSON into `examples/skill/data/`):

```bash
export HF_TOKEN="hf_..."
python3 examples/skill/scripts/download_gaia_2023_level1_validation.py
```

To also download referenced attachment files:

```bash
python3 examples/skill/scripts/download_gaia_2023_level1_validation.py --with-files
```

Natural prompts:
- Say what you want to accomplish; the model will decide if a skill is
  needed based on the overview.
- When needed, the model calls `skill_load` for body/docs, then follows
  the steps in `SKILL.md` by running `workspace_exec` inside
  `/skills/<name>/` to execute scripts and collect output files.

## SKILL.md Anatomy

`SKILL.md` uses YAML front matter + Markdown body:

```markdown
---
name: python-math
description: Small Python utilities for math and text files.
---

Overview
Run short Python scripts inside the skill workspace...

Examples
1) Print the first N Fibonacci numbers
   Command: python3 scripts/fib.py 10 > out/fib.txt

Output Files
- out/fib.txt
```

Recommendations:
- Keep `name`/`description` succinct for the overview
- In the body, include when‑to‑use, steps/commands, output file paths
- Put scripts under `scripts/` and reference them in commands

For more examples, see:
https://github.com/anthropics/skills

## Tools in Detail

### `skill_load`

Declaration: [tool/skill/load.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/load.go)

Input:
- `skill` (required)
- `docs` (optional array of doc filenames)
- `include_all_docs` (optional bool)

Behavior:
- Writes session-scoped `temp:*` keys:
  - `temp:skill:loaded_by_agent:<agent>/<name>` = "1"
  - `temp:skill:docs_by_agent:<agent>/<name>` = "*" or JSON array
  - `temp:skill:loaded_order_by_agent:<agent>` = JSON array of touched
    skill names, from oldest to newest
  - Legacy keys (`temp:skill:loaded:<name>`, `temp:skill:docs:<name>`) are
    still supported and migrated when seen.
- Multi-agent note: sub-agents typically share the same Session. With
  agent-scoped keys, a child agent’s `skill_load` won’t automatically
  inflate the coordinator’s prompt. If another agent needs the body/docs,
  have that agent call `skill_load` explicitly.
- Request processors materialize `SKILL.md` body and docs into the next
  model request:
  - Default: appended to the system message (legacy behavior)
  - Optional: appended to the matching tool result message
    (`llmagent.WithSkillsLoadedContentInToolResults(true)`)

#### Get the currently loaded skills (before every model call)

If you want the **current loaded skills list** right before *each* model
request (including every step inside a tool loop), register a
`BeforeModel` callback and read the `Invocation` from context.

From first principles:

- `skill_load` does **not** inject the full `SKILL.md` text into the
  session transcript.
- Instead, it writes small “flags” into the session state:
  - Loaded flag: keys with prefix `skill.StateKeyLoadedByAgentPrefix`
  - Docs selection: keys with prefix `skill.StateKeyDocsByAgentPrefix`
  - These keys are scoped by agent name to avoid cross-agent leakage in
    multi-agent sessions.
- The Skills request processors read those keys and materialize bodies
  / docs into the **next** outbound model request.

So, to observe the loaded skills list, you only need to inspect session
state:

In the snippet below, `m` is your model and `repo` is your skills
repository.

```go
import (
    "context"
    "fmt"
    "sort"
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

func loadedSkillNames(inv *agent.Invocation) []string {
    if inv == nil || inv.Session == nil {
        return nil
    }
    state := inv.Session.SnapshotState()
    if len(state) == 0 {
        return nil
    }

    prefix := skill.LoadedPrefix(inv.AgentName)

    var out []string
    for k, v := range state {
        if !strings.HasPrefix(k, prefix) {
            continue
        }
        if len(v) == 0 {
            continue
        }
        name := strings.TrimPrefix(k, prefix)
        if strings.TrimSpace(name) == "" {
            continue
        }
        out = append(out, name)
    }
    sort.Strings(out)
    return out
}

modelCallbacks := model.NewCallbacks().
    RegisterBeforeModel(func(
        ctx context.Context,
        args *model.BeforeModelArgs,
    ) (*model.BeforeModelResult, error) {
        _ = args
        inv, ok := agent.InvocationFromContext(ctx)
        if !ok {
            return nil, nil
        }
        fmt.Printf("loaded skills: %v\n", loadedSkillNames(inv))
        return nil, nil
    })

agt := llmagent.New(
    "skills-assistant",
    llmagent.WithModel(m),
    llmagent.WithSkills(repo),
    llmagent.WithModelCallbacks(modelCallbacks),
)
_ = agt
```

Notes:

- `SkillLoadModeTurn` (default) clears the agent-scoped `temp:skill:*`
  keys (for example, `temp:skill:loaded_by_agent:*` /
  `temp:skill:docs_by_agent:*` /
  `temp:skill:loaded_order_by_agent:*`) at the start of the **next**
  `Runner.Run` call, so the loaded list is usually non-empty only within
  the current turn/tool loop.
- `SkillLoadModeSession` keeps them across turns, so the loaded list can
  remain non-empty until you clear it (or the session expires).

#### Built-in option: cap loaded skills (TopK)

If your goal is simply “keep only the most recent N loaded skills”, use
the built-in option:

- `llmagent.WithMaxLoadedSkills(N)`

This enforces the cap **before every model request** by clearing older
`temp:skill:*` state keys. Recent skill touches are tracked in session
state and updated by `skill_load` / `skill_select_docs`, so the cap does
not depend on alphabetical fallback or surviving tool-result history.

Example:

```go
agt := llmagent.New(
    "skills-assistant",
    llmagent.WithModel(m),
    llmagent.WithSkills(repo),
    llmagent.WithMaxLoadedSkills(3),
)
_ = agt
```

#### Custom policy: cap loaded skills (e.g., keep the most recent 3)

`SkillLoadMode` controls **lifetime** (once/turn/session). If you need
full manual control beyond `llmagent.WithMaxLoadedSkills` (for example,
custom eviction policy), use an `AppendEventHook` on your session
service to modify the state deltas written by `skill_load`.

The core idea:

1) Detect events that load a skill for a specific agent (state delta
   contains keys with prefix `skill.LoadedPrefix(ev.Author)`).
2) Compute which skills would be loaded *after* applying the delta.
3) If the count exceeds your limit, clear the older skills by adding
   `nil` entries into the same `StateDelta` map.

Example (inmemory session service, keep the most recent 3 loaded skills):

```go
import (
    "encoding/json"
    "sort"
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
    maxLoadedSkills = 3
    toolSkillLoad   = "skill_load"
)

type skillLoadArgs struct {
    Skill string `json:"skill"`
}

func loadedSkillsFromState(
    state session.StateMap,
    agentName string,
) []string {
    if len(state) == 0 {
        return nil
    }
    prefix := skill.LoadedPrefix(agentName)
    var out []string
    for k, v := range state {
        if !strings.HasPrefix(k, prefix) {
            continue
        }
        if len(v) == 0 {
            continue
        }
        name := strings.TrimPrefix(k, prefix)
        if strings.TrimSpace(name) == "" {
            continue
        }
        out = append(out, name)
    }
    sort.Strings(out)
    return out
}

func capLoadedSkills(
    sess *session.Session,
    ev *event.Event,
    max int,
) {
    if sess == nil || ev == nil || max <= 0 {
        return
    }
    if len(ev.StateDelta) == 0 {
        return
    }

    agentName := strings.TrimSpace(ev.Author)
    if agentName == "" {
        return
    }
    loadedPrefix := skill.LoadedPrefix(agentName)

    // Only enforce when this event loads a skill.
    var newlyLoaded []string
    for k, v := range ev.StateDelta {
        if !strings.HasPrefix(k, loadedPrefix) {
            continue
        }
        if len(v) == 0 {
            continue
        }
        name := strings.TrimPrefix(k, loadedPrefix)
        if strings.TrimSpace(name) == "" {
            continue
        }
        newlyLoaded = append(newlyLoaded, name)
    }
    if len(newlyLoaded) == 0 {
        return
    }

    // Predict the "post-append" state by applying delta to a copy.
    nextState := sess.SnapshotState()
    for k, v := range ev.StateDelta {
        nextState[k] = v
    }

    loaded := loadedSkillsFromState(nextState, agentName)
    if len(loaded) <= max {
        return
    }

    loadedSet := make(map[string]struct{}, len(loaded))
    for _, name := range loaded {
        loadedSet[name] = struct{}{}
    }

    // Keep the most recent loaded skills by scanning recent events.
    keep := make([]string, 0, max)
    keepSet := make(map[string]struct{}, max)

    // 1) Always keep the skill(s) loaded by this delta.
    sort.Strings(newlyLoaded)
    for _, name := range newlyLoaded {
        if _, ok := loadedSet[name]; !ok {
            continue
        }
        if _, ok := keepSet[name]; ok {
            continue
        }
        keep = append(keep, name)
        keepSet[name] = struct{}{}
        if len(keep) >= max {
            break
        }
    }

    // 2) Fill from newest skill_load calls in the transcript.
    events := sess.GetEvents()
    for i := len(events) - 1; i >= 0 && len(keep) < max; i-- {
        if strings.TrimSpace(events[i].Author) != agentName {
            continue
        }
        rsp := events[i].Response
        if rsp == nil || len(rsp.Choices) == 0 {
            continue
        }
        msg := rsp.Choices[0].Message
        if msg.Role != model.RoleAssistant {
            continue
        }
        for _, tc := range msg.ToolCalls {
            if tc.Function.Name != toolSkillLoad {
                continue
            }
            var in skillLoadArgs
            if err := json.Unmarshal(
                []byte(tc.Function.Arguments),
                &in,
            ); err != nil {
                continue
            }
            name := strings.TrimSpace(in.Skill)
            if name == "" {
                continue
            }
            if _, ok := loadedSet[name]; !ok {
                continue
            }
            if _, ok := keepSet[name]; ok {
                continue
            }
            keep = append(keep, name)
            keepSet[name] = struct{}{}
            if len(keep) >= max {
                break
            }
        }
    }

    // 3) Fallback: fill deterministically from the loaded list.
    for _, name := range loaded {
        if len(keep) >= max {
            break
        }
        if _, ok := keepSet[name]; ok {
            continue
        }
        keep = append(keep, name)
        keepSet[name] = struct{}{}
    }

    // Evict everything else by clearing the same state keys.
    for _, name := range loaded {
        if _, ok := keepSet[name]; ok {
            continue
        }
        ev.StateDelta[skill.LoadedKey(agentName, name)] = nil
        ev.StateDelta[skill.DocsKey(agentName, name)] = nil
    }
}

svc := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(
        ctx *session.AppendEventContext,
        next func() error,
    ) error {
        capLoadedSkills(ctx.Session, ctx.Event, maxLoadedSkills)
        return next()
    }),
)
_ = svc
```

See [Session docs](session/index.md) (“AppendEventHook”) for the hook API, and
`examples/session/hook` for a runnable example.

Notes:
- Prefer progressive disclosure: load only the body first, then list and
  select only the docs you need. Avoid `include_all_docs` unless you
  truly need every doc (or the user explicitly asks).
- Safe to call multiple times to add or replace docs.
- The tools write session state, but **how long the loaded content stays
  in the prompt** depends on `SkillLoadMode`:
  - `turn` (default): loaded bodies/docs stay for the current
    `Runner.Run` call (one user message) and are cleared automatically
    before the next run starts.
  - `once`: loaded bodies/docs are injected for the **next** model
    request only, then offloaded (cleared) from session state.
  - `session` (legacy): loaded bodies/docs persist across turns until
    cleared or the session expires.
- Common question: why do I only see `loaded: <name>` in a tool result
  (and not a `[Loaded] <name>` block with the full body)?
  - First, make sure you enabled tool-result materialization:
    `llmagent.WithSkillsLoadedContentInToolResults(true)`.
    If it is disabled, bodies/docs are appended to the system message,
    not the tool result.
  - If it is enabled but you still see only the stub on the “next
    conversation turn”, you are likely using the default
    `SkillLoadModeTurn`: state is cleared before the next run, so the
    framework won’t re-materialize bodies/docs. Use:

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeSession),
)
```

Multi-turn example: reuse the same `sessionID` so the “loaded state” can
carry across turns:

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

ctx := context.Background()
svc := inmemory.NewSessionService()
r := runner.NewRunner(
    "demo-app",
    agent,
    runner.WithSessionService(svc),
)
defer r.Close()

userID := "u1"
sessionID := "s1"

drain := func(ch <-chan *event.Event) {
    for range ch {
    }
}

ch, _ := r.Run(ctx, userID, sessionID, model.NewUserMessage(
    "Please load the internal-comms skill.",
))
drain(ch)

// Next turn, same sessionID:
ch, _ = r.Run(ctx, userID, sessionID, model.NewUserMessage(
    "Now use internal-comms to generate an update.",
))
drain(ch)

// Optional: inspect what is persisted in the session service.
sess, _ := svc.GetSession(ctx, session.Key{
    AppName:   "demo-app",
    UserID:    userID,
    SessionID: sessionID,
})
_ = sess
```

Clearing guidance (common with `SkillLoadModeSession`):

- Easiest: start a new conversation using a new `sessionID`.
- Or delete the session (in this inmemory example):

```go
_ = svc.DeleteSession(ctx, session.Key{
    AppName:   "demo-app",
    UserID:    userID,
    SessionID: sessionID,
})
```

Hint: tool-result materialization requires the relevant tool result
message to be present in the request history. If history is suppressed
or truncated, the framework falls back to a dedicated system message
(`Loaded skill context:`) to preserve correctness.
- Configure it on the agent:

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
)
```

Config snippet: a common prompt-cache friendly setup (stable system +
per-turn lifetime):

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
)
```

Config snippet: one-shot injection (only the **next** model request sees
the body/docs):

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeOnce),
)
```

### `skill_select_docs`

Declaration: [tool/skill/select_docs.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/select_docs.go)

Input:
- `skill` (required)
- `docs` (optional array)
- `include_all_docs` (optional bool)
- `mode` (optional string): `add` | `replace` | `clear`

Behavior:
- Updates doc selection state for the current agent:
  - `temp:skill:docs_by_agent:<agent>/<name>` = `*` for include all
  - `temp:skill:docs_by_agent:<agent>/<name>` = JSON array for explicit list
  - Also refreshes `temp:skill:loaded_order_by_agent:<agent>` so
    `WithMaxLoadedSkills(N)` treats doc selection as a recent touch
  - Legacy key `temp:skill:docs:<name>` is still supported and migrated.

### `skill_list_docs`

Declaration: [tool/skill/list_docs.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/list_docs.go)

Input:
- `skill` (required)

Output:
- Array of available doc filenames

Note: These keys are managed by the framework; you rarely need to touch
them directly when driving the conversation naturally.

### Skill Workspace and Runtime Environment

When the model executes skill scripts through `workspace_exec`, the
framework materializes a writable skill working copy per session and
injects a set of conveniences so scripts can be written against stable
relative paths:

- The skill root `/skills/<name>` is a **session-scoped writable working
  copy**: scripts may create caches, `__pycache__`, `.venv/`, or other
  files next to the source. The upstream skill repository remains the
  source of truth; when the source digest changes, the next reconcile
  replaces the workspace copy.
- Convenience symlinks are created under the skill root: `out/`,
  `work/`, and `inputs/` point to workspace‑level dirs so the same
  relative paths from `SKILL.md` work inside the sandbox.
- Injected env vars: `WORKSPACE_DIR`, `SKILLS_DIR`, `WORK_DIR`,
  `OUTPUT_DIR`, `RUN_DIR` (set by the executor).
- `.venv/` under the skill root is the recommended place for per-skill
  dependencies (for example, `python -m venv .venv` + `pip install ...`).
- File tools accept `inputs/<path>` as an alias to `<path>` when the
  configured base directory does not contain a real `inputs/` folder.

## Executor

Interface: [codeexecutor/codeexecutor.go](https://github.com/trpc-group/trpc-agent-go/blob/main/codeexecutor/codeexecutor.go)

Implementations:
- Local: [codeexecutor/local/workspace_runtime.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/codeexecutor/local/workspace_runtime.go)
- Container (Docker):
  [codeexecutor/container/workspace_runtime.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/codeexecutor/container/workspace_runtime.go)

Container notes:
- Writable run base; `$SKILLS_ROOT` mounted read‑only when present
- Network disabled by default for repeatability and safety

Security & limits:
- Reads/writes confined to the workspace
- The skill working copy under `/skills/<name>` is writable by
  default; the canonical skill repository remains the source of truth
- `stdout`/`stderr` may be truncated (see `warnings`)
- Output file read size is capped to prevent oversized payloads

## Events and Tracing

Tools emit `tool.response` events and may carry state deltas (used by
`skill_load`). Merging/parallel execution logic:
[internal/flow/processor/functioncall.go]
(internal/flow/processor/functioncall.go)

Common spans:
- `workspace.create`, `workspace.stage.*`, `workspace.run`
- `workspace.collect`, `workspace.cleanup`, `workspace.inline`

## Rationale and Design

- Motivation: Skills often contain lengthy instructions and scripts.
  Inlining all of them is costly and risky. The three‑layer model keeps
  the prompt lean while loading details and running code only when needed.
- Injection & state: Tools write temporary keys (via `StateDelta`), and
  request processors materialize loaded bodies/docs into the prompt
  (system message by default, or tool results when enabled).
- Isolation: Scripts run within a workspace boundary and only selected
  output files are brought back, not the script source.

## Executor Environment Variable Injection

When the executor runs remotely (containers, cloud functions, etc.),
host environment variables are not automatically available.
`codeexecutor.NewEnvInjectingCodeExecutor` wraps any `CodeExecutor`
so that a provider function is called on every `RunProgram` /
`StartProgram` to merge extra env vars into `RunProgramSpec.Env`.

```go
import "trpc.group/trpc-go/trpc-agent-go/codeexecutor"

wrapped := codeexecutor.NewEnvInjectingCodeExecutor(exec,
    func(ctx context.Context) map[string]string {
        // Read caller-supplied env from ctx.
        // Source is up to you: RuntimeState, request headers, DB, etc.
        return map[string]string{"GITHUB_TOKEN": "..."}
    },
)

agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithCodeExecutor(wrapped),  // use wrapped instead of raw exec
)
```

Behavior:

- Covers all paths that go through `Engine.Runner()` (for example,
  `workspace_exec`).
- Provider-returned keys **never override** keys already set in
  the tool's `env` argument.
- Evaluated on each `RunProgram` call; no state shared between calls.
- Zero overhead when the provider returns `nil`.
- You can also wrap at the Engine level:
  `codeexecutor.NewEnvInjectingEngine(eng, provider)`.

Typical use case: in a multi-user agent service, each user passes
their tokens via AG-UI `state` or HTTP headers; the provider reads
them from the request context and injects them into the executor
transparently — the LLM never sees the credentials.

## Troubleshooting

- Unknown skill: verify name and repository path; ensure the overview
  lists the skill before calling `skill_load`
- No executor wired: by default the framework auto-falls back to a
  local executor when `WithSkills(repo)` is set. If you explicitly
  opted out (`WithSkillToolProfile(SkillToolProfileKnowledgeOnly)`
  or `WithAllowedSkillTools(...)`) and still want `workspace_exec`,
  pass `llmagent.WithCodeExecutor(...)` (local or container).
- Timeouts/non‑zero exit codes: inspect command/deps/timeout; in
  container mode, network is disabled by default
- Missing output files: check the glob patterns passed to
  `workspace_exec` and confirm they point to real workspace paths

## References and Examples

- Background:
  - Blog:
    https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills
  - Open repo: https://github.com/anthropics/skills
- Industry patterns:
  - OpenClaw: prompt instructs the model to read a selected skill’s
    `SKILL.md` via a tool:
    https://github.com/openclaw/openclaw/blob/0cf93b8fa74566258131f9e8ca30f313aac89d26/src/agents/system-prompt.ts
  - OpenAI Codex: project docs list skills and instruct opening
    `SKILL.md` on demand:
    https://github.com/openai/codex/blob/383b45279efda1ef611a4aa286621815fe656b8a/codex-rs/core/src/project_doc.rs
- This repo:
  - GAIA benchmark demo: [examples/skill/README.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skill/README.md)
  - Real discovery/install demo: [examples/skillfind/README.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillfind/README.md)
  - Sub-agent skill isolation demo: [examples/skillisolation/README.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillisolation/README.md)
