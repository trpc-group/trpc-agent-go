# Anthropic Skills Benchmark for trpc-agent-go

This benchmark is a focused sanity-check for trpc-agent-go **Agent Skills**
compatibility against the public `anthropics/skills` repository.

It exercises:
- Loading a real skills repository (local path or URL archive)
- Running `skill_run` in a workspace (read-only skill tree + writable outputs)
- Installing Python deps inside the workspace via `.venv/`
- Using an LLM to verify `skill_load` / docs injection behavior

## Directory Structure

```
benchmark/anthropic_skills/
├── README.md
├── skills_cache/               # cache for URL-based skills roots
├── skill_workspaces/           # local workspace roots for skill_run
└── trpc-agent-go-impl/         # runnable benchmark program
    ├── go.mod
    └── *.go
```

## Quickstart

```bash
cd benchmark/anthropic_skills/trpc-agent-go-impl
go run .
```

By default the program downloads `anthropics/skills` as a GitHub ZIP
archive and caches it under `../skills_cache/`.

The first run can take a while (downloads, Python installs, and optional
LLM calls). Progress is printed to stderr, and stdout prints `PASS` only
at the end.

By default the agent suite prints step-by-step tool calls and tool
results (similar to `benchmark/gaia`). Use `-debug` to disable log
truncation and include extra debug fields.

## LLM Setup (Agent Suite)

The agent suite requires a model endpoint via `OPENAI_API_KEY` (and
optionally `OPENAI_BASE_URL`). In this repo's environment, you can run:

```bash
source ~/.zshrc
gpt5
```

If you see a clang "missing sysroot" error after sourcing `~/.zshrc`,
run Go with `CGO_ENABLED=0` or `unset SDKROOT` before `go run`.

## Common Flags

- `-skills-root`:
  - Local directory, or a URL to a ZIP/TAR archive
  - Default: `https://github.com/anthropics/skills/archive/refs/heads/main.zip`
- `-progress`: print progress to stderr (default: true)
- `-skills-cache-dir`: cache dir for URL roots (default: `../skills_cache`)
- `-work-root`: local workspace root (default: `../skill_workspaces`)
- `-suite`:
  - `tool`: deterministic `skill_run` checks only
  - `agent`: LLM-based checks (`skill_load` + `skill_list_docs`)
  - `all`: run both (default)
- `-model`: model name for agent suite (default: `$MODEL_NAME` or `gpt-5`)
- `-with-exec`: run extra exec cases (default: true)
- `-only-skill`: run a single skill name (optional)
- `-debug`: include tool/answer details on failures

## Fast Smoke Runs

```bash
cd benchmark/anthropic_skills/trpc-agent-go-impl
go run . -suite tool -with-exec=false
go run . -suite agent -with-exec=false -only-skill webapp-testing
```

## Notes

- The tool suite verifies that the staged skill tree is read-only while
  `out/`, `work/`, `inputs/`, and `.venv/` remain usable.
