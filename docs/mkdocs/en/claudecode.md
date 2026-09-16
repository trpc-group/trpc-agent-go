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

### Default mode: use Claude Code sessions

By default, the agent uses Claude Code CLI's native session history. Each run uses the following order:

1. `--resume <cli-session-id>`
2. `--session-id <cli-session-id>`

If `--resume` cannot find an existing conversation, the agent creates one with `--session-id` and the same deterministic UUID. Later turns derive the same CLI session id when you keep using the same app name, user ID, and session ID.

This mode is best for single-instance services, or for deployments where requests are always routed to the same machine, user home, and Claude Code configuration environment. To keep context in this mode, use the same app name, user ID, and session ID in `runner`.

Use `WithResumeEnabled(false)` to disable native Claude Code CLI session resume. When disabled, the agent passes neither `--resume` nor `--session-id`; it passes `--no-session-persistence` instead, so the local CLI session is not an implicit context source.

### Use framework session events as context

If your service runs multiple instances, rebuilds containers, or does not route each conversation to the same machine, Claude Code CLI's local session history is not a reliable context source. In that case, keep context in a framework session service such as Redis or a database. All service instances can read the same session events by using the same app name, user ID, and session ID, and `WithMessageBuilder` can turn those events into the complete prompt passed to Claude Code CLI.

Recommended configuration:

1. Configure `runner` with a shared session service.
2. Use `WithMessageBuilder` to build the complete prompt from `args.Events`.
3. Use `WithResumeEnabled(false)` to disable local Claude Code session resume, so the same history does not come from both the prompt and the local session.

`MessageBuilderArgs.Events` is a read-only shallow snapshot. Do not mutate events, responses, state deltas, or extensions inside it. When the agent is called through `runner.Run`, the runner persists the current-turn user message before invoking the agent, so the events already include the current user message. Do not append it again by default.

The example below omits standard-library imports such as `context` and `strings`, and shows only the agent-specific import. It joins non-partial message text only; production builders can choose whether to include tool calls and tool results.

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/claudecode"

ag, err := claudecode.New(
  claudecode.WithMessageBuilder(func(ctx context.Context, args *claudecode.MessageBuilderArgs) (string, error) {
    var prompt strings.Builder
    for _, evt := range args.Events {
      if evt.Response == nil || len(evt.Choices) == 0 || evt.IsPartial {
        continue
      }
      msg := evt.Choices[0].Message
      if msg.Content == "" {
        continue
      }
      prompt.WriteString(string(msg.Role))
      prompt.WriteString(": ")
      prompt.WriteString(msg.Content)
      prompt.WriteString("\n")
    }
    return prompt.String(), nil
  }),
  claudecode.WithResumeEnabled(false),
)
```

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
| `WithMessageBuilder(builder)` | Customizes the complete prompt passed to Claude Code CLI. |
| `WithResumeEnabled(enabled)` | Controls whether the agent uses Claude Code CLI session resume. Default is `true`. |
