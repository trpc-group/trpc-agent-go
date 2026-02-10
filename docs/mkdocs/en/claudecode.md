# Claude Code Agent Guide

## Overview

tRPC-Agent-Go provides a `ClaudeCode` `Agent` implementation. It executes a local Claude Code CLI, obtains an execution trace, and maps it into framework events.

The primary use cases include:

- Run Claude Code in `runner`
- Persist raw CLI stdout and stderr
- Align tool traces in evaluation

## Quick start

### Prerequisites

1. Install and authenticate Claude Code CLI locally
2. Make sure the CLI executable is available in `PATH`, or pass an absolute path via `WithBin`

### Basic usage

See the full example at [examples/claudecode](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/claudecode).

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

ag, err := claudecode.New(
  claudecode.WithBin("claude"),
  claudecode.WithExtraArgs("--permission-mode", "bypassPermissions"),
)
if err != nil {
  log.Fatal(err)
}

r := runner.NewRunner("claudecode-cli-example", ag)
defer r.Close()

ch, err := r.Run(context.Background(), "user-1", "session-1", model.NewUserMessage("Use the Bash tool to run ls and return the first filename."))
if err != nil {
  log.Fatal(err)
}
```

## Output format and parsing

The agent runs Claude Code CLI in `--print` mode. The CLI output must be JSON output records. Two formats are supported:

- `json`, JSON array
- `stream-json`, JSONL

The agent relies on `tool_use` and `tool_result` records in the output, so it enables `--verbose` by default and forces `--output-format`.

Use `WithOutputFormat` to switch the output format. Do not append `--output-format` via `WithExtraArgs`.

```go
ag, err := claudecode.New(
  claudecode.WithOutputFormat(claudecode.OutputFormatStreamJSON),
)
```

## Event mapping

The agent emits tool events and one final response event. It does not emit intermediate assistant text message events. The final response content is taken from the `result` field of the last record where `type` is `result`.

| Claude Code JSON output | Framework event |
| --- | --- |
| `message.content[].type == "tool_use"` | tool-call response event |
| `message.content[].type == "tool_result"` | tool-result response event |
| `type == "result"` | final response event |
| `tool_use.name == "Task"` and contains `subagent_type` | transfer event |
| `tool_use.name == "Skill"` | tool name normalized to `skill_run` |

## Multi-turn sessions

Claude Code CLI requires UUID values for `--session-id`. The agent derives a deterministic UUID as the CLI session id from `invocation.Session.AppName`, `invocation.Session.UserID`, and `invocation.Session.ID`.

Each run uses the following order:

1. `--resume <cli-session-id>`
2. `--session-id <cli-session-id>`

To keep context, use the same app name, user ID, and session ID in `runner`.

## Persist raw CLI output

Use `WithRawOutputHook` to observe stdout and stderr for each invocation. It is recommended to write them into evaluation and observability artifacts:

```go
ag, err := claudecode.New(
  claudecode.WithRawOutputHook(func(ctx context.Context, args *claudecode.RawOutputHookArgs) error {
    // Write args.Stdout / args.Stderr to your log storage.
    return nil
  }),
)
```

`RawOutputHookArgs` carries both the framework `SessionID` and the CLI `CLISessionID`.

## Options reference

| Option | Description |
| --- | --- |
| `WithName(name)` | Sets the agent name. This value is used as the event author. |
| `WithBin(bin)` | Sets the CLI executable path. Default is `claude`. |
| `WithExtraArgs(args...)` | Appends CLI flags. This argument is inserted before the session flags and prompt. |
| `WithOutputFormat(format)` | Sets JSON output format: `json` or `stream-json`. |
| `WithEnv(env...)` | Adds CLI environment variables. Use `KEY=VALUE`. |
| `WithWorkDir(dir)` | Sets the CLI working directory. |
| `WithRawOutputHook(hook)` | Observes raw stdout and stderr. The hook runs after the CLI finishes and before parsing. |
