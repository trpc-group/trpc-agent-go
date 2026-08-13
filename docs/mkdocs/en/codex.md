# Codex Agent Guide

## Overview

tRPC-Agent-Go provides a `Codex` `Agent` implementation. It executes a local Codex CLI with `codex exec --json`, parses the JSONL event stream, and maps Codex activity into framework events as the stream arrives.

The primary use cases include:

- Run Codex in `runner`
- Persist raw CLI stdout and stderr
- Align Codex command and MCP tool traces in evaluation

## Quick start

### Prerequisites

1. Install and authenticate Codex CLI locally
2. Make sure the CLI executable is available in `PATH`, or pass an absolute path via `WithBin`

### Basic usage

See the full example at [examples/codex](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/codex). The example includes a temporary project-local MCP server and a project-local skill.

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent/codex"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

ag, err := codex.New(
  codex.WithBin("codex"),
  codex.WithGlobalArgs("--sandbox", "workspace-write"),
)
if err != nil {
  log.Fatal(err)
}

r := runner.NewRunner("codex-cli-example", ag)
defer r.Close()

ch, err := r.Run(context.Background(), "user-1", "session-1", model.NewUserMessage("Run pwd and summarize the workspace."))
if err != nil {
  log.Fatal(err)
}
```

## Output format and parsing

The agent forces `--json` on `codex exec` and parses stdout as JSONL. Do not append `--json` manually unless you need to control argument ordering.

Prompt text is written to stdin so user input such as `--help` is not parsed as a Codex CLI flag.

`WithExtraArgs` appends arguments after `exec` or `exec resume`. Use it only for flags accepted by both `codex exec` and `codex exec resume`, such as `--model`.

`WithGlobalArgs` appends arguments before the `exec` subcommand. Use it for root-level Codex flags such as `--ask-for-approval`, `--sandbox`, or `--cd`.

```go
ag, err := codex.New(
  codex.WithGlobalArgs("--ask-for-approval", "never"),
  codex.WithGlobalArgs("--sandbox", "read-only"),
)
```

## Skills and external repositories

The agent does not install, merge, or rewrite Codex skill repositories. It runs the local Codex CLI with the configured environment, profile, and working directory. Any skills, plugins, or plugin marketplaces that the same `codex exec` command can load from local Codex configuration are available to this agent as well.

Use the existing CLI configuration mechanisms to select skill sources:

- `WithEnv("CODEX_HOME=/path/to/codex-home")` selects an isolated Codex home.
- `WithGlobalArgs("-p", "profile-name")` selects a Codex profile.
- `WithGlobalArgs("-c", "key=value")` passes a Codex config override.
- `WithGlobalArgs("--cd", "/path/to/workspace")` or `WithWorkDir("/path/to/workspace")` selects the workspace context.

If multiple external skill repositories are configured for that Codex CLI environment, this agent does not filter them. It also does not synthesize skill events from configuration. Current Codex CLI behavior tends to handle skills through shell commands instead of a dedicated skill tool, so skill usage is usually represented as `command_execution` events rather than `skill_run`.

## Event mapping

The agent emits assistant, tool, and error events as Codex JSONL records arrive, then emits a final completion response after the Codex turn completes. Codex `agent_message` items are complete message items rather than token deltas, but the agent exposes them as partial `chat.completion.chunk` segments so session persistence stores only the final assistant response. The final completion response uses the last assistant message content and carries final usage and thread state. It does not emit intermediate reasoning events.

| Codex JSONL output | Framework event |
| --- | --- |
| `type == "thread.started"` | Persisted by default into session state key `codex.StateKeyThreadID` |
| `item.type == "command_execution"` | tool-call and tool-result response events |
| `item.type == "mcp_tool_call"` | tool-call and tool-result response events |
| Built-in tool items such as `web_search`, `file_change`, `image_view`, and `image_generation` | tool-call and tool-result response events |
| `type == "turn.failed"` or `type == "error"` | non-terminal error observation chunk without `Response.Error`, followed by one terminal error after the command finishes |
| `item.type == "agent_message"` | partial assistant chunk event; the last `agent_message` item also becomes the final response content |
| `type == "turn.completed"` | final response usage |

MCP tool calls are normalized to Claude Code-compatible names when possible: `mcp__<server>__<tool>`.
Built-in tool calls keep their Codex tool names.
Current Codex CLI skill usage is often injected as prompt context or handled through shell `command_execution` items. This agent preserves those actual item types and does not emit a synthetic `skill_run` event.

## Multi-turn sessions

### Default mode: use Codex threads

By default, the agent uses Codex CLI's native thread history:

1. On the first turn, the agent writes the prompt to stdin of `codex exec --json`.
2. When Codex returns `thread.started`, the agent stores the thread id in session state under `codex.StateKeyThreadID`.
3. On later turns, if session state has a thread id, the agent writes the prompt to stdin of `codex exec resume --json <thread-id>`.

If resume fails before any transcript event is emitted, the agent starts a fresh `codex exec` run and updates the stored thread id when the new run reports one. If resume has already emitted framework events, or if stdout parsing fails, the agent surfaces that failure instead of starting a fresh run to avoid duplicating visible progress or tool side effects. If both resume and create fail, the invocation returns a run error.

This mode is best for single-instance services, or for deployments where requests are always routed to the same machine, user home, and Codex configuration environment. To keep context in this mode, use the same app name, user ID, and session ID in `runner`.

Use `WithResumeEnabled(false)` to disable native Codex CLI resume. When disabled, the agent does not read `codex.StateKeyThreadID`, does not call `codex exec resume`, and does not write newly observed thread ids back to session state. Each invocation runs a fresh `codex exec`.

### Use framework session events as context

If your service runs multiple instances, rebuilds containers, or does not route each conversation to the same machine, Codex CLI's local thread history is not a reliable context source. In that case, keep context in a framework session service such as Redis or a database. All service instances can read the same session events by using the same app name, user ID, and session ID, and `WithMessageBuilder` can turn those events into the complete prompt passed to Codex CLI.

Recommended configuration:

1. Configure `runner` with a shared session service.
2. Use `WithMessageBuilder` to build the complete prompt from `args.Events`.
3. Use `WithResumeEnabled(false)` to disable local Codex thread resume, so the same history does not come from both the prompt and the local thread.

`MessageBuilderArgs.Events` is a read-only shallow snapshot. Do not mutate events, responses, state deltas, or extensions inside it. When the agent is called through `runner.Run`, the runner persists the current-turn user message before invoking the agent, so the events already include the current user message. Do not append it again by default.

The example below omits standard-library imports such as `context` and `strings`, and shows only the agent-specific import. It joins non-partial message text only; production builders can choose whether to include tool calls and tool results.

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/codex"

ag, err := codex.New(
  codex.WithMessageBuilder(func(ctx context.Context, args *codex.MessageBuilderArgs) (string, error) {
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
  codex.WithResumeEnabled(false),
)
```

## Persist raw CLI output

Use `WithRawOutputHook` to observe stdout/stderr for each invocation. It is recommended to write them into evaluation and observability artifacts:

```go
ag, err := codex.New(
  codex.WithRawOutputHook(func(ctx context.Context, args *codex.RawOutputHookArgs) error {
    // Write args.Stdout / args.Stderr to your log storage.
    return nil
  }),
)
```

`RawOutputHookArgs` carries both the framework `SessionID` and Codex `ThreadID`.

## Options reference

| Option | Description |
| --- | --- |
| `WithName(name)` | Sets the agent name. This value is used as the event author. |
| `WithBin(bin)` | Sets the CLI executable path. Default is `codex`. |
| `WithGlobalArgs(args...)` | Appends root CLI flags before the `exec` subcommand. |
| `WithExtraArgs(args...)` | Appends `codex exec` flags before the optional resume session id. |
| `WithEnv(env...)` | Adds CLI environment variables. Use `KEY=VALUE`. |
| `WithWorkDir(dir)` | Sets the CLI process working directory. |
| `WithRawOutputHook(hook)` | Observes raw stdout and stderr. The hook runs after the CLI finishes and after streamed transcript events are emitted; returning an error appends an error event and skips the final assistant response. |
| `WithMessageBuilder(builder)` | Customizes the complete prompt passed to Codex CLI. |
| `WithResumeEnabled(enabled)` | Controls whether the agent uses Codex CLI thread resume. Default is `true`. |
