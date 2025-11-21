# Skill (Agent Skills)

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
   - When a task truly needs a skill, the model calls `skill_load` and
     the framework injects that skill’s full `SKILL.md` body.

3) Docs/Scripts (selective + isolated execution)
   - Docs are included only when requested; scripts are not inlined but
     executed inside a workspace, returning results and output files.

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
)
```

Key points:
- Request processor injects overview and on‑demand content:
  [internal/flow/processor/skills.go]
  (https://github.com/trpc-group/trpc-agent-go/blob/main/internal/flow/processor/skills.go)
- Tools are auto‑registered with `WithSkills`: `skill_load`,
  `skill_select_docs`, `skill_list_docs`, and `skill_run` show up
  automatically; no manual wiring required.
- Auto prompt guidance is injected in the system message so the model
  learns to `skill_load` first, select docs with `skill_select_docs`
  as needed, and then `skill_run` at the right time.
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
- Writes ephemeral session keys (per turn):
  - `temp:skill:loaded:<name>` = "1"
  - `temp:skill:docs:<name>` = "*" or JSON array
- Request processor injects `SKILL.md` body and docs into system message

Notes:
- Safe to call multiple times to add or replace docs.

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
- `command` (required, runs via `bash -lc`)
- `cwd`, `env` (optional)
- `output_files` (optional, legacy collection): glob patterns (e.g.,
  `out/*.txt`). Patterns are workspace‑relative; env‑style prefixes
  like `$OUTPUT_DIR/*.txt` are also accepted and normalized to
  `out/*.txt`.
- `inputs` (optional, declarative inputs): map external sources into
  the workspace. Each item supports:
  - `from` with schemes:
    - `artifact://name[@version]` to load from the Artifact service
    - `host://abs/path` to copy/link from a host absolute path
    - `workspace://rel/path` to copy/link from current workspace
    - `skill://<name>/rel/path` to copy/link from a staged skill
  - `to` workspace‑relative destination; defaults to
    `WORK_DIR/inputs/<basename>`
  - `mode`: `copy` (default) or `link` when feasible

- `outputs` (optional, declarative outputs): a manifest to collect
  results with limits and persistence:
  - `globs` workspace‑relative patterns (supports `**` and env‑style
    prefixes like `$OUTPUT_DIR/**` mapping to `out/**`)
  - `inline` to inline file contents into the result
  - `save` to persist via the Artifact service
  - `name_template` prefix for artifact names (e.g., `pref/`)
  - Limits: `max_files` (default 100), `max_file_bytes` (default
    4 MiB/file), `max_total_bytes` (default 64 MiB)

- `timeout` (optional seconds)
- `save_as_artifacts` (optional, legacy path): persist files collected
  via `output_files` and return `artifact_files` in the result
- `omit_inline_content` (optional): with `save_as_artifacts`, omit
  `output_files[*].content` and return metadata only
- `artifact_prefix` (optional): prefix for the legacy artifact path

Output:
- `stdout`, `stderr`, `exit_code`, `timed_out`, `duration_ms`
- `output_files` with `name`, `content`, `mime_type`
- `artifact_files` with `name`, `version` appears in two cases:
  - Legacy path: when `save_as_artifacts` is set
  - Manifest path: when `outputs.save=true` (executor persists files)

Typical flow:
1) Call `skill_load` to inject body/docs
2) Call `skill_run` and collect outputs:
   - Legacy: use `output_files` globs
   - Declarative: use `outputs` to drive collect/inline/save
   - Use `inputs` to stage upstream files when needed

Environment and CWD:
- When `cwd` is omitted, runs at the skill root: `/skills/<name>`
- A relative `cwd` is resolved under the skill root
- Runtime injects env vars: `WORKSPACE_DIR`, `SKILLS_DIR`, `WORK_DIR`,
  `OUTPUT_DIR`, `RUN_DIR`; the tool injects `SKILL_NAME`
- Convenience symlinks are created under the skill root: `out/`,
  `work/`, and `inputs/` point to workspace‑level dirs

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
- Output file read size is capped to prevent oversized payloads

## Events and Tracing

Tools emit `tool.response` events and may carry state deltas (used by
`skill_load`). Merging/parallel execution logic:
[internal/flow/processor/functioncall.go]
(internal/flow/processor/functioncall.go)

Common spans:
- `workspace.create`, `workspace.stage.*`, `workspace.run`
- `workspace.collect`, `workspace.cleanup`, `workspace.inline`

## Rationale and Design (brief)

- Motivation: Skills often contain lengthy instructions and scripts.
  Inlining all of them is costly and risky. The three‑layer model keeps
  the prompt lean while loading details and running code only when needed.
- Injection & state: Tools write temporary keys (via `StateDelta`), and
  the next request processor builds the system message accordingly.
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
- This repo:
  - Interactive demo: [examples/skillrun/main.go]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/main.go)
  - Sample skill: [examples/skillrun/skills/python_math/SKILL.md]
    (https://github.com/trpc-group/trpc-agent-go/blob/main/examples/skillrun/skills/python_math/SKILL.md)
