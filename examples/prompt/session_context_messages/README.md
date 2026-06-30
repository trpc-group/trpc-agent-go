# Append-only Context Diff with Session Context Messages

This example shows how to build an append-only context diff flow on top of the
first-layer session context API.

The framework API stays simple:

```go
agent.WithSessionContextMessagesFunc(func(
    ctx context.Context,
    args *agent.SessionContextMessagesArgs,
) ([]model.Message, error) {
    return manager.BuildMessages(currentRuntimeState, cause, args), nil
})
```

The business-owned manager decides whether to return:

- a full snapshot when no baseline exists
- no messages when the runtime state is unchanged
- a diff message when fields changed

Runner persists any returned messages into the session transcript immediately
before the current user message.

## Run

By default the example uses an OpenAI-compatible model. Set `OPENAI_API_KEY`
before running it. `OPENAI_BASE_URL` is optional and only needed for compatible
providers that use a custom endpoint.

```bash
cd examples

export OPENAI_API_KEY="<your-api-key>"
# export OPENAI_BASE_URL="<your-compatible-endpoint>"

go run ./prompt/session_context_messages
```

For a local deterministic run with no network access:

```bash
go run ./prompt/session_context_messages -mode=debug
```

## Scenario

The example simulates a coding-agent runtime state similar to what a stateful
agent product may track:

- current working directory
- workspace roots
- active files
- permission profile
- approval policy
- sandbox mode
- network mode and allowed domains
- model
- task mode
- git branch and dirty files
- last tool action

Across five turns, user actions and tool actions naturally change this state:

1. Session bootstrap collects the initial runtime state.
2. A file discovery tool updates the active files.
3. The user grants write access and an edit tool changes a file.
4. The user asks a follow-up question with no runtime change.
5. A docs lookup enables limited network access and README is edited.

The manager converts that sequence into append-only history:

```text
[runtime_state full snapshot]
U1
A1
[runtime_state diff: active_files, last_tool_action]
U2
A2
[runtime_state diff: permissions, sandbox, dirty_files, ...]
U3
A3
U4
A4
[runtime_state diff: network_mode, allowed_domains, dirty_files, ...]
U5
A5
```

## Design Point

This is intentionally a first-layer example.

The framework does not know what `runtimeState` means. It only guarantees that
messages returned by `WithSessionContextMessagesFunc` are persisted before the
current user turn.

The manager owns:

- the state schema
- the baseline
- the diff algorithm
- replace/patch semantics
- when to emit a full snapshot
- when to emit no messages

For a single-process demo, the manager stores the baseline in memory. In a real
service, that baseline should usually live in durable business storage if it must
survive restarts, distributed workers, summary, compaction, or session replay.
