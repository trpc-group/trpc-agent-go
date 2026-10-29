# Agent application with LLM tool approval

This example uses a main LLMAgent, a separate LLM reviewer runner, real hostexec
tools, and the final `PermissionPolicy` hook. The reviewer evaluates the exact
arguments after before-tool callbacks, using the shared `review.Context` also
consumed by the [System One example](systemone/README.md).

## Run

From this directory, using Go 1.24.4 or later:

```bash
export OPENAI_API_KEY="your-api-key"
mkdir -p /tmp/trpc-approval-demo
go run . -model gpt-5.4 -base-dir /tmp/trpc-approval-demo
```

`OPENAI_BASE_URL` can select an OpenAI-compatible chat endpoint. `-streaming`
controls the main model's streaming. `/help`, `/exit`, and EOF are supported.
Ctrl-C cancels the run and closes host sessions; each turn allows up to eight
main-model calls.
Try “Run pwd and report the current directory.”

Approved commands run on the local host with the application's permissions.
The base directory is a default, not a sandbox or a filesystem restriction.

## Runtime and ownership

1. Runner persists host user input, including its framework provenance.
2. The main agent proposes a host tool call.
3. A before-tool callback makes exec_command's workdir explicit and absolute.
4. Tool-owned permission checks run first; the approval permission policy then
   applies static rules, any composed host policy, and the reviewer.
5. The reviewer receives final arguments, tool schema/metadata, bounded history,
   source/omission information, and host execution facts. An LLM produces a risk
   score; execution requires a valid score strictly below 80.
6. Execution or denial/error returns to the main agent as a tool result.

`hostexec_exec_command` and `hostexec_write_stdin` require review;
`hostexec_kill_session` is statically denied. No stdin bypass is configured.
The main agent reuses its session across turns. Each review uses a fresh session
with one self-contained evidence request. The application owns the reviewer
session service (one-hour TTL) and closes both runners, the host tool set, and
that service on exit. It does not register a duplicate BeforeTool approval hook.

The reviewer has no investigation tools. Hosts can supply relevant read-only
facts through `approval.WithContextProvider`; hard restrictions belong in tool
checkers or the composed host policy. See the [approval contract](../../../plugin/guardrail/approval/README.md)
for context limits, compatibility, and provenance semantics.

## Tests

From the `examples` module:

```bash
go test -race ./guardrail/approval/...
```

The LLM application tests use two scripted models with the actual agent, runner,
structured-output decoder, reviewer, callbacks, and host tools. An approved
command creates a temporary file; denied and missing-score responses leave it
absent. They check resolved workdir, shared context, two-turn history, isolated
reviewer sessions, and return of tool results to the main model. The System One
application tests use a local HTTP mock for the same execution flow.

These tests do not call live model providers or establish judgment quality.
