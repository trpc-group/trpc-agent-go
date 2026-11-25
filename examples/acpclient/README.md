# ACP Client Example

This example runs an external ACP v1 coding agent through
`agent/acpagent` and exposes it as a regular `agent.Agent`.

It demonstrates:

- explicit ACP agent process configuration
- Agent Auth method selection
- multiple prompts in one framework Session
- `CloseSession` for the mapped ACP Session
- shared process recycling through `IdleTimeout`
- forwarding the external agent's `stderr` for local diagnostics

## Requirements

Install an ACP-compatible agent executable. The selected executable must speak
ACP v1 over newline-delimited JSON-RPC on stdio.

## Run

The default command is `codex-acp`:

```bash
cd examples/acpclient
go run .
```

Use another ACP agent by overriding the command and adding arguments:

```bash
go run . \
  -command /path/to/agent \
  -arg=--acp
```

Useful interactive commands:

- `/close-session`: call `CloseSession` for the current framework Session
- `/exit`: close the process and exit

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-command` | ACP agent executable | `codex-acp` |
| `-arg` | Agent argument; may be repeated | empty |
| `-work-dir` | Absolute or relative ACP working directory | `.` |
| `-user-id` | Framework user ID | `demo-user` |
| `-session-id` | Framework Session ID | generated UUID |
| `-idle-timeout` | Recycle the shared process after this idle duration | `5m` |

## Behavior

- A complete `session.Key` maps each framework Session to one ACP Session.
- Normal later prompts reuse the ACP Session and do not call `session/load`.
- If the ACP process exits or is recycled by `IdleTimeout`, the next prompt
  restores the Session using `session/resume`, `session/load`, or `session/new`
  according to the capabilities advertised by the Agent.
- The example selects the first advertised Agent Auth method. Provider-specific
  authentication parameters such as API keys must be supplied through
  `AuthSelection.Meta` by a real application.
- Terminal Auth is not configured in this example. The client therefore
  advertises `auth.terminal=false`.
- The example forwards the external agent's raw `stderr` to the example
  process's `stderr`. Library users that do not set a writer get line-oriented
  forwarding through the framework log.
