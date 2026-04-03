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

- 🧭 Built-in tool profiles: `full` (default) or `knowledge_only`
- 🔎 Overview injection (name + description) to guide selection
- 📥 `skill_load` to pull `SKILL.md` body and selected docs on demand
- 📚 `skill_select_docs` to add/replace/clear docs
- 🧾 `skill_list_docs` to list available docs
- 🏃 `skill_run` to execute commands in the `full` profile, returning stdout/stderr and
  output files
- ⌨️ `skill_exec` plus session tools for interactive stdin/TTY flows in the
  `full` profile
- 🗂️ Output file collection via glob patterns with MIME detection
- 🧩 Pluggable local or container workspace executors (local by default)
- 🧱 Declarative `inputs`/`outputs`: map inputs and collect/inline/
  save outputs via a manifest

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

Knowledge-only mode:

```go
agent := llmagent.New(
    "skills-assistant",
    llmagent.WithSkills(repo),
    llmagent.WithSkillToolProfile(
        llmagent.SkillToolProfileKnowledgeOnly,
    ),
)
```

Fine-grained allowlist:

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
- `WithSkills` auto-registers built-in skill tools; no manual wiring is
  required.
  - Default `full` profile: `skill_load`, `skill_select_docs`,
    `skill_list_docs`, `skill_run`, and — when the executor supports
    interactive sessions — `skill_exec`, `skill_write_stdin`,
    `skill_poll_session`, `skill_kill_session`.  If the executor does
    not implement `InteractiveProgramRunner` (and no local fallback
    applies), these session tools are omitted and the corresponding
    prompt guidance is suppressed.
  - `knowledge_only` profile: only `skill_load`, `skill_select_docs`,
    and `skill_list_docs`.
  - `WithAllowedSkillTools(...)` overrides the profile with an explicit
    allowlist, for example `SkillToolLoad` only.
  - If the allowlist includes `skill_run` / `skill_exec`, the default
    `WithSkillRunRequireSkillLoaded(true)` still requires `skill_load`
    to be enabled as well. To omit `skill_load`, explicitly set
    `llmagent.WithSkillRunRequireSkillLoaded(false)`.
  - Executor requirement follows the final registered tool set:
    any configuration without `skill_run` / `skill_exec` does not need
    `WithCodeExecutor(...)`.
- Note: when `WithCodeExecutor` is set, LLMAgent will (by default) try to
  execute Markdown fenced code blocks in model responses. If you only need
  the executor for `skill_run`, disable this behavior with
  `llmagent.WithEnableCodeExecutionResponseProcessor(false)`.
- By default, the framework appends a small `Tooling and workspace guidance:`
  block after the `Available skills:` list in the system message.
  - Disable it (to save prompt tokens): `llmagent.WithSkillsToolingGuidance("")`.
  - Or replace it with your own text: `llmagent.WithSkillsToolingGuidance("...")`.
  - Guidance follows the final registered skill tools, including
    `WithAllowedSkillTools(...)`.
  - If you disable it, make sure your instruction tells the model which
    skill tools are available in your chosen profile or allowlist.
  - Loader: [tool/skill/load.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/load.go)
  - Runner: [tool/skill/run.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/run.go)

### 3) Run the Example

Interactive demo:
[examples/skillrun/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/main.go)

This demo and the related skill-focused examples ( `skill`, `skilldynamicschema` and
`structuredoutputskills`) explicitly set
`llmagent.WithEnableCodeExecutionResponseProcessor(false)` so fenced code
blocks in assistant text do not auto-execute while `skill_run` is enabled.

```bash
cd examples/skillrun
export OPENAI_API_KEY="your-api-key"
go run . -executor local     # or: -executor container
```

GAIA benchmark demo (skills + file tools):
[examples/skill/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skill/README.md)

It includes a dataset downloader script and notes on Python dependencies
for skills like `whisper` (audio) and `ocr` (images).

Real discovery/install demo (real model + real web/GitHub):
[examples/skillfind/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillfind/README.md)

It starts with a built-in `skill-find` skill, searches the public web for
candidate skills, installs a public skill from GitHub into a user-private
directory, refreshes the repository, and uses the new skill in the same
conversation. Local execution stays disabled by default and is only
enabled when you opt in.

SkillLoadMode demo (no API key required):
[examples/skillloadmode/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillloadmode/README.md)

SkillToolProfile demo (no API key required):
[examples/skilltoolprofile/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skilltoolprofile/README.md)

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

Sample skill (excerpt):
[examples/skillrun/skills/python_math/SKILL.md]
(https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/skills/python_math/SKILL.md)

Natural prompts:
- Say what you want to accomplish; the model will decide if a skill is
  needed based on the overview.
- When needed, the model calls `skill_load` for body/docs, then
  `skill_run` to execute and return output files.
- In `knowledge_only`, the model can still load skill instructions/docs,
  but it must use them as guidance rather than execute skill scripts.

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

### `skill_run`

Declaration: [tool/skill/run.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/run.go)

Input:
- `skill` (required)
- `command` (required; by default runs via `bash -c`)
- `cwd`, `env` (optional)
- `stdin` (optional): one-shot stdin text passed to the command
- `editor_text` (optional): text used for CLIs that launch `$EDITOR`
- `output_files` (optional, legacy collection): glob patterns (e.g.,
  `out/*.txt`). Patterns are workspace‑relative; env‑style prefixes
  like `$OUTPUT_DIR/*.txt` are also accepted and normalized to
  `out/*.txt`.
- `inputs` (optional, declarative inputs): map external sources into
  the workspace. Each item supports:
  - `from` with schemes:
    - `artifact://name[@version]` to load from the Artifact service
    - `host:///abs/path` to copy/link from a host absolute path
    - `workspace://rel/path` to copy/link from current workspace
    - `skill://<name>/rel/path` to copy/link from a staged skill
  - `to` workspace‑relative destination; defaults to
    `WORK_DIR/inputs/<basename>`. For convenience, `skill_run` treats
    `to` values starting with `inputs/` as `work/inputs/` (because
    `inputs/` is a symlink under the skill root).
  - `mode`: `copy` (default) or `link` when feasible
  - `pin`: for `artifact://name` without `@version`, reuse the first
    resolved version for the same `to` path (best effort)

- Conversation file inputs (automatic staging):
  - If the current session contains user messages with file inputs
    (`content_parts` items of type `file`), `skill_run` automatically
    stages them into `work/inputs/` (and thus `inputs/`) before the
    command runs.
  - Filenames are sanitized to a safe basename and de-duplicated with a
    numeric suffix when needed.
  - If a file input has no filename and `file_id` is an `artifact://...`
    reference, the framework infers the basename from the artifact name.
    Otherwise, it falls back to `upload_N`.
  - If a file input includes raw bytes (`data`), those bytes are written
    directly into the workspace.
  - If a file input is referenced only by `file_id`:
    - When `file_id` starts with `artifact://`, `skill_run` loads it
      from the Artifact service (useful when user uploads are stored as
      artifacts and only referenced in messages).
    - Otherwise, the framework downloads the content via the configured
      model when supported.

- `outputs` (optional, declarative outputs): a manifest to collect
  results with limits and persistence:
  - `globs` workspace‑relative patterns (supports `**` and env‑style
    prefixes like `$OUTPUT_DIR/**` mapping to `out/**`)
  - `inline` to inline file contents into the result
  - `save` to persist via the Artifact service
  - `name_template` prefix for artifact names (e.g., `pref/`)
  - Limits: `max_files` (default 100), `max_file_bytes` (default
    4 MiB/file), `max_total_bytes` (default 64 MiB)
  - Note: `outputs` accepts both snake_case keys (recommended) and
    legacy Go-style keys like `MaxFiles`

- `timeout` (optional seconds)
- `save_as_artifacts` (optional, legacy path): persist files collected
  via `output_files` and return `artifact_files` in the result
- `omit_inline_content` (optional): omit `output_files[*].content` and
  `primary_output.content` (metadata only). Non-text outputs never
  inline content. Use `output_files[*].ref` with `read_file` when you
  need text content later.
- `artifact_prefix` (optional): prefix for the legacy artifact path
  - If the Artifact service is not configured, `skill_run` keeps
    returning `output_files` and reports a `warnings` entry.

Guidance:
- Prefer using `skill_run` only for commands explicitly required by the
  selected skill docs (for example, `SKILL.md`).
- Avoid using `skill_run` for generic shell exploration.
- Prefer using `skill_list_docs` and `skill_select_docs` to inspect
  skill docs, then use file tools to read the selected content.

Optional safety restriction (allowlist):
- Env var `TRPC_AGENT_SKILL_RUN_ALLOWED_COMMANDS`:
  - Comma/space-separated command names (for example, `python3,ffmpeg`)
  - When set, `skill_run` rejects shell syntax (pipes/redirections/
    separators) and only allows a single allowlisted command
  - Because the command is no longer parsed by a shell, patterns like
    `> out/x.txt`, heredocs, and `$OUTPUT_DIR` expansion will not work;
    prefer running scripts or using `outputs` to collect files
- You can also configure this in code via
  `llmagent.WithSkillRunAllowedCommands(...)`.

Optional safety restriction (denylist):
- Env var `TRPC_AGENT_SKILL_RUN_DENIED_COMMANDS`:
  - Comma/space-separated command names
  - When set, `skill_run` also rejects shell syntax (single command only)
    and blocks denylisted command names
- You can also configure this in code via
  `llmagent.WithSkillRunDeniedCommands(...)`.

Optional behavior (force artifact persistence):
- In code, you can force `skill_run` to persist collected outputs as
  artifacts (best effort) even if the model omits `save_as_artifacts`
  / `outputs.save`:
  - `llmagent.WithSkillRunForceSaveArtifacts(true)`

Optional behavior (inline output limits):
- In code, you can customize how much inline text `skill_run` returns:
  - `llmagent.WithSkillRunOutputLimits(toolskill.RunOutputLimits{StdoutStderrBytes: 128 * 1024, PrimaryOutputBytes: 128 * 1024})`
- This changes only `stdout`, `stderr`, and `primary_output`.
  `output_files` / `outputs` still use the workspace collector limits
  (default 4 MiB/file).

Output:
- `stdout`, `stderr`, `exit_code`, `timed_out`, `duration_ms`
  - `stdout` / `stderr` are for logs and short status text. They may be
    truncated at the configured inline limit (default 16 KiB each).
    For large or structured text that the model must read, prefer
    `output_files` or `outputs`.
- `staged_inputs` (optional): files staged from the conversation into
  `work/inputs/` for this run
- `primary_output` (optional) with `name`, `ref`, `content`, `mime_type`,
  `size_bytes`, `truncated`
  - Convenience pointer to the "best" small text output file (when one
    exists). Prefer this when there is a single main output.
  - Only text files within the configured size limit (default 32 KiB)
    are considered for `primary_output`.
- `output_files` with `name`, `ref`, `content`, `mime_type`, `size_bytes`,
  `truncated`
  - `ref` is a stable `workspace://<name>` reference that can be passed
    to other tools
  - For non-text files, `content` is omitted.
  - Prefer this path for large or structured text outputs; file
    collection uses workspace collector limits instead of the smaller
    stdout/stderr inline budget.
  - When `omit_inline_content=true`, `content` is omitted for all files.
    Use `ref` with `read_file` to fetch text content on demand.
  - `size_bytes` is the file size on disk; `truncated=true` means the
    collected content hit internal caps (for example, 4 MiB/file).
  - If the command fails or times out, zero-byte collected files are
    omitted to avoid misleading shell-redirection artifacts.
- `warnings` (optional): non-fatal notes (for example, when artifact
  saving is skipped)
- `artifact_files` with `name`, `version` appears in two cases:
  - Legacy path: when `save_as_artifacts` is set
  - Manifest path: when `outputs.save=true` (executor persists files)

Typical flow:
1) Call `skill_load` to inject body/docs
   - When using `llmagent.LLMAgent`, this step is required by default:
     `skill_run` rejects calls unless `skill_load` has been called for
     that skill. Disable with:
     `llmagent.WithSkillRunRequireSkillLoaded(false)`.
2) Call `skill_run` and collect outputs:
   - Legacy: use `output_files` globs
   - Declarative: use `outputs` to drive collect/inline/save
   - Use `inputs` to stage upstream files when needed

### Interactive skill sessions

Declaration:
- [tool/skill/exec.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/exec.go)

Tools:
- `skill_exec`: start a session-oriented command in the skill workspace
- `skill_write_stdin`: write incremental stdin to a running session
- `skill_poll_session`: fetch more terminal output or the final result
- `skill_kill_session`: terminate and remove a session

Guidance:
- Prefer `skill_run` for one-shot commands.
- Prefer `skill_exec` when the command may prompt for input, present a
  numbered selection, or keep running between turns.
- Prefer `editor_text` on `skill_run` or `skill_exec` for `$EDITOR`
  workflows instead of trying to drive a full-screen editor via stdin.

`skill_exec` reuses the same workspace, `inputs`, `outputs`,
`save_as_artifacts`, `omit_inline_content`, `artifact_prefix`, `stdin`,
and `editor_text` behavior as `skill_run`, but returns session state:
- `status`: `running` or `exited`
- `session_id`: stable id for follow-up calls
- `output`: most recent terminal output seen during that call
- `interaction`: best-effort hint when the process appears to be waiting
  for more input
- `result`: when the session exits, the final `skill_run`-style output

Typical interactive flow:
1) Call `skill_exec`
2) Inspect `output` / `interaction`
3) Use `skill_write_stdin` or `skill_poll_session` until `status`
   becomes `exited`
4) Read `result` and collected outputs, or call `skill_kill_session`
   to stop the session

Examples:

Stage an external input file and collect a small text output:

```json
{
  "skill": "demo",
  "inputs": [
    {
      "from": "host:///tmp/notes.txt",
      "to": "work/inputs/notes.txt",
      "mode": "copy"
    }
  ],
  "command": "mkdir -p out; wc -l work/inputs/notes.txt > out/lines.txt",
  "outputs": {
    "globs": ["$OUTPUT_DIR/lines.txt"],
    "inline": true,
    "save": false,
    "max_files": 1
  }
}
```

Metadata-only outputs (avoid filling context):

```json
{
  "skill": "demo",
  "command": "mkdir -p out; echo hi > out/a.txt",
  "output_files": ["out/*.txt"],
  "omit_inline_content": true
}
```

The tool returns `output_files[*].ref` like `workspace://out/a.txt`
with `content` omitted, plus `size_bytes` and `truncated`.

To read the content later:

```json
{
  "file_name": "workspace://out/a.txt",
  "start_line": 1,
  "num_lines": 20
}
```

Persist large outputs as artifacts (no inline content):

```json
{
  "skill": "demo",
  "command": "mkdir -p out; echo report > out/report.txt",
  "outputs": {
    "globs": ["$OUTPUT_DIR/report.txt"],
    "inline": false,
    "save": true,
    "max_files": 5
  }
}
```

When saved, `skill_run` returns `artifact_files` with `name` and
`version`. You can reference an artifact as `artifact://<name>[@<version>]`
in tools like `read_file`.

Legacy artifact save path (when you use `output_files`):

```json
{
  "skill": "demo",
  "command": "mkdir -p out; echo report > out/report.txt",
  "output_files": ["out/report.txt"],
  "omit_inline_content": true,
  "save_as_artifacts": true,
  "artifact_prefix": "pref/"
}
```

Environment and CWD:
- When `cwd` is omitted, runs at the skill root: `/skills/<name>`
- A relative `cwd` is resolved under the skill root
- `cwd` may start with `$WORK_DIR`, `$OUTPUT_DIR`, `$SKILLS_DIR`,
  `$WORKSPACE_DIR`, `$RUN_DIR` (or `${...}`) and will be normalized to
  workspace‑relative directories
- Runtime injects env vars: `WORKSPACE_DIR`, `SKILLS_DIR`, `WORK_DIR`,
  `OUTPUT_DIR`, `RUN_DIR`; the tool injects `SKILL_NAME`
- Convenience symlinks are created under the skill root: `out/`,
  `work/`, and `inputs/` point to workspace‑level dirs
- `.venv/` is a writable directory under the skill root for per-skill
  dependencies (for example, `python -m venv .venv` + `pip install ...`)
- File tools accept `inputs/<path>` as an alias to `<path>` when the
  configured base directory does not contain a real `inputs/` folder

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
- Timeouts and read‑only skill trees reduce risk
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

- Covers all paths through `Engine.Runner()`: `skill_run`,
  `workspace_exec`, and interactive sessions.
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
- Nil executor: configure `WithCodeExecutor` or rely on the local
  default
- Timeouts/non‑zero exit codes: inspect command/deps/`timeout`; in
  container mode, network is disabled by default
- Missing output files: check your glob patterns and output locations

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
  - Interactive demo: [examples/skillrun/main.go]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/main.go)
  - Real discovery/install demo: [examples/skillfind/README.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillfind/README.md)
  - Sample skill: [examples/skillrun/skills/python_math/SKILL.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/skills/python_math/SKILL.md)
