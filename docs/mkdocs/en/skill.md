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
- 🏃 `skill_run` to execute commands, returning stdout/stderr and
  output files
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
vs full injection), see `benchmark/anthropic_skills/README.md` and run
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
**skips** this fallback by default to avoid re-inflating the prompt with
summarized content. In that setup, if the tool result messages were
summarized away, the model will need to call `skill_load` again to see
the full body/docs.

Enable tool-result materialization with:
`llmagent.WithSkillsLoadedContentInToolResults(true)`.
To restore the legacy fallback behavior in summary mode:
`llmagent.WithSkipSkillsFallbackOnSessionSummary(false)`.

To measure the impact in a real tool-using flow, run the
`benchmark/anthropic_skills` `prompt-cache` suite.

How this relates to `SkillLoadMode` (common pitfall):

- The cache-prefix discussion above mostly applies to multiple model
  calls within the same `Runner.Run` (one user message triggers a tool
  loop).
- If you want loaded skill bodies/docs to persist across **multiple
  conversation turns**, set `SkillLoadMode` to `session`. The default
  `turn` mode clears `temp:skill:loaded:*` / `temp:skill:docs:*` before
  the next run starts. As a result, even if your history still contains
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

Provide a repository and an executor. If not set, a local executor is
used for convenience during development.

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
    // Optional: keep the system prompt stable for prompt caching.
    llmagent.WithSkillsLoadedContentInToolResults(true),
)
```

Key points:
- Request processor injects overview and on‑demand content:
  [internal/flow/processor/skills.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/internal/flow/processor/skills.go)
- Tools are auto‑registered with `WithSkills`: `skill_load`,
  `skill_select_docs`, `skill_list_docs`, and `skill_run` show up
  automatically; no manual wiring required.
- Note: when `WithCodeExecutor` is set, LLMAgent will (by default) try to
  execute Markdown fenced code blocks in model responses. If you only need
  the executor for `skill_run`, disable this behavior with
  `llmagent.WithEnableCodeExecutionResponseProcessor(false)`.
- By default, the framework appends a small `Tooling and workspace guidance:`
  block after the `Available skills:` list in the system message.
  - Disable it (to save prompt tokens): `llmagent.WithSkillsToolingGuidance("")`.
  - Or replace it with your own text: `llmagent.WithSkillsToolingGuidance("...")`.
  - If you disable it, make sure your instruction tells the model when to use
    `skill_load`, `skill_select_docs`, and `skill_run`.
  - Loader: [tool/skill/load.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/load.go)
  - Runner: [tool/skill/run.go](https://github.com/trpc-group/trpc-agent-go/blob/main/tool/skill/run.go)

### 3) Run the Example

Interactive demo:
[examples/skillrun/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/main.go)

```bash
cd examples/skillrun
export OPENAI_API_KEY="your-api-key"
go run . -executor local     # or: -executor container
```

GAIA benchmark demo (skills + file tools):
[examples/skill/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skill/README.md)

It includes a dataset downloader script and notes on Python dependencies
for skills like `whisper` (audio) and `ocr` (images).

SkillLoadMode demo (no API key required):
[examples/skillloadmode/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillloadmode/README.md)

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
  - `temp:skill:loaded:<name>` = "1"
  - `temp:skill:docs:<name>` = "*" or JSON array
- Request processors materialize `SKILL.md` body and docs into the next
  model request:
  - Default: appended to the system message (legacy behavior)
  - Optional: appended to the matching tool result message
    (`llmagent.WithSkillsLoadedContentInToolResults(true)`)

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
- Updates `temp:skill:docs:<name>` accordingly:
  - `*` for include all
  - JSON array for explicit list

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
    `WORK_DIR/inputs/<basename>`
  - `mode`: `copy` (default) or `link` when feasible
  - `pin`: for `artifact://name` without `@version`, reuse the first
    resolved version for the same `to` path (best effort)

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

Output:
- `stdout`, `stderr`, `exit_code`, `timed_out`, `duration_ms`
- `primary_output` (optional) with `name`, `ref`, `content`, `mime_type`,
  `size_bytes`, `truncated`
  - Convenience pointer to the "best" small text output file (when one
    exists). Prefer this when there is a single main output.
- `output_files` with `name`, `ref`, `content`, `mime_type`, `size_bytes`,
  `truncated`
  - `ref` is a stable `workspace://<name>` reference that can be passed
    to other tools
  - For non-text files, `content` is omitted.
  - When `omit_inline_content=true`, `content` is omitted for all files.
    Use `ref` with `read_file` to fetch text content on demand.
  - `size_bytes` is the file size on disk; `truncated=true` means the
    collected content hit internal caps (for example, 4 MiB/file).
- `warnings` (optional): non-fatal notes (for example, when artifact
  saving is skipped)
- `artifact_files` with `name`, `version` appears in two cases:
  - Legacy path: when `save_as_artifacts` is set
  - Manifest path: when `outputs.save=true` (executor persists files)

Typical flow:
1) Call `skill_load` to inject body/docs
2) Call `skill_run` and collect outputs:
   - Legacy: use `output_files` globs
   - Declarative: use `outputs` to drive collect/inline/save
   - Use `inputs` to stage upstream files when needed

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
  - Sample skill: [examples/skillrun/skills/python_math/SKILL.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/skills/python_math/SKILL.md)
