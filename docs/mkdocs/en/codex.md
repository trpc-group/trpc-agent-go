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
| `type == "thread.started"` | Persisted into session state key `codex.StateKeyThreadID` |
| `item.type == "command_execution"` | tool-call and tool-result response events |
| `item.type == "mcp_tool_call"` | tool-call and tool-result response events |
| Built-in tool items such as `web_search`, `file_change`, `image_view`, and `image_generation` | tool-call and tool-result response events |
| `type == "turn.failed"` or `type == "error"` | non-terminal error observation, followed by one terminal error after the command finishes |
| `item.type == "agent_message"` | partial assistant chunk event; the last item also becomes the final response content |
| `type == "turn.completed"` | final response usage |

MCP tool calls are normalized to Claude Code-compatible names when possible: `mcp__<server>__<tool>`.
Built-in tool calls keep their Codex tool names.
Current Codex CLI skill usage is often injected as prompt context or handled through shell `command_execution` items. This agent preserves those actual item types and does not emit a synthetic `skill_run` event.

## Multi-turn sessions

Codex creates its own thread id. The agent persists that id in session state under `codex.StateKeyThreadID` and uses it on later turns:

1. First turn: write the prompt to stdin of `codex exec --json`
2. Later turns: write the prompt to stdin of `codex exec resume --json <thread-id>`

If resume fails, the agent starts a fresh `codex exec` run and updates the stored thread id when the new run reports one. If both resume and create fail, the invocation returns a run error.

To keep context, use the same app name, user ID, and session ID in `runner`.

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
