# Agent application with System One tool approval

This is a complete tRPC-Agent-Go command-line application. An OpenAI-compatible
main model decides which `hostexec` tools to call. A System One reviewer (Jev or
a compatible server) reviews each call through the final permission policy before the
tool executes. Tool results, including denials, return to the main model so it
can answer the user.

The application supports interactive conversations and a single-prompt mode.
Approved commands **execute on the local host**. `-base-dir` sets the working
directory; it does not create a sandbox or restrict filesystem access.

## Runtime flow

1. `Runner.Run` persists the user message in the runner's in-memory session.
2. `LLMAgent` sends the conversation and host tool declarations to the main model.
3. A before-tool callback resolves exec_command workdir to an absolute path.
   After callbacks and tool-owned checks, the final permission policy builds a
   `review.Request` from the actual arguments, shared Context, and session history.
4. `systemone.Reviewer` sends one request with Choice authorization and Score
   risk questions. Local rules convert the answers to `review.Decision`.
5. An approved call reaches `hostexec`. A denial or review failure becomes a
   tool result without executing the command.
6. The main model receives that result and continues to its final answer.

All three host tools (`hostexec_exec_command`, `hostexec_write_stdin`, and
`hostexec_kill_session`) use `ToolPolicyRequireApproval`; there are no bypasses
in this example. Each user turn allows at most eight main-model calls.

The same user/session IDs are reused across interactive turns. Follow-up
reviews therefore see earlier user intent, assistant messages, and tool results,
subject to the approval plugin's transcript limits. History is not persisted
across application restarts. The runner owns its session service; application
shutdown closes the runner and the host tool set, including process sessions.
The reviewer needs no separate runner or session.

## Configuration

Use Go 1.24.4 or later, as required by the `examples` module. The main model and
review model have independent endpoints and credentials:

| Environment variable | Purpose |
| --- | --- |
| `OPENAI_API_KEY` | Credential for the main chat model |
| `OPENAI_BASE_URL` | Optional OpenAI-compatible endpoint for the main model |
| `SYSTEMONE_API_KEY` | Credential explicitly passed to the reviewer; empty omits bearer authentication |

| Flag | Default | Purpose |
| --- | --- | --- |
| `-model` | `gpt-5.4` | Main chat model |
| `-review-model` | `jev-1.13.0` | Decision model; empty permits server routing |
| `-review-base-url` | `https://api.typesafe.ai` | System One API root; `/v1/systemone` is appended |
| `-review-timeout` | `30s` | Per-review deadline; zero disables this deadline |
| `-risk-threshold` | `50` | Exclusive normalized risk threshold |
| `-min-confidence` | `0.8` | Inclusive minimum for confidences and relevant probabilities |
| `-base-dir` | `.` | Default directory for host commands |
| `-prompt` | empty | Run one user prompt and exit; otherwise start interactive chat |

The startup banner prints the selected models, reviewer endpoint, working
directory, session ID, and thresholds. API keys are not printed. The library
does not read `SYSTEMONE_API_KEY`; the application passes it explicitly.

## Run an interactive agent

```bash
cd examples/guardrail/approval/systemone
export OPENAI_API_KEY="your-chat-model-api-key"
export SYSTEMONE_API_KEY="your-systemone-api-key"
mkdir -p /tmp/trpc-systemone-demo
go run . -base-dir /tmp/trpc-systemone-demo
```

Try these turns:

```text
You: Run pwd and list the files in the current directory.
You: Explain the command output from the previous turn.
You: /exit
```

`/help` prints usage suggestions. EOF and `/exit` close the application; Ctrl-C
cancels the active run and closes host process sessions. Responses are printed
after each model response, together with tool calls and tool results.

## Run one prompt

```bash
go run . -base-dir /tmp/trpc-systemone-demo \
  -model gpt-5.4 -review-model jev-1.13.0 \
  -prompt 'Run pwd and report the current directory.'
```

For a protocol-compatible local decision service, while keeping the main chat
model configuration:

```bash
SYSTEMONE_API_KEY='' go run . \
  -review-base-url http://localhost:8000 -review-model '' \
  -base-dir /tmp/trpc-systemone-demo \
  -prompt 'Run pwd and report the current directory.'
```

## Observe the decision and execution

The console prints the main model's proposed tool call, then the tool result
and assistant answer. The final review also sees callback-resolved workdir,
which may differ from the initially proposed arguments. Approval logs include:

```text
Automatic approval review approved (risk: low): ...
Automatic approval review denied (risk: low): ...
```

A denied action can have low intrinsic risk but insufficient authorization.
The decision's `Approved` field controls execution, not the risk label alone.
If the service is unavailable, the main model receives an `approval review
failed` tool result. The agent is instructed to explain a denial or failure
without attempting to bypass it. There is no human approval UI or durable
pause/resume in this example.

The defaults require supported authorization, risk below 50, and confidence and
relevant probabilities of at least 0.8. These are initial policy choices, not
live model calibration. See the [reviewer contract](../../../../plugin/guardrail/approval/review/systemone/README.md)
for the rubric, normalization, input limits, and execution boundaries. The agent
is instructed to include an explicit absolute `workdir`; the callback also
normalizes omitted or relative workdir before review. The context provider adds
host execution facts. Provenance and truncation metadata come from the shared
builder, not the model. No duplicate BeforeTool approval hook is registered.

## Code and validation

- `main.go`: CLI configuration, separate model credentials, signal handling.
- `app.go`: main agent, real host tools, final approval permission policy, runner,
  session ownership, and event consumption.
- `console.go`: interactive conversation and startup information.
- `app_test.go`: complete runtime tests with a scripted main model and local
  mock System One HTTP service. Approval writes a file in a temporary directory;
  denial and service failure leave that file absent. A two-turn conversation
  verifies that earlier intent and tool results reach the next review, with
  provenance, tool schema, host facts, and a callback-resolved workdir.

Run from the `examples` module:

```bash
go test -race ./guardrail/approval/systemone
```

These tests exercise the actual runner, agent, reviewer, and host tools. They
do not call live model providers or establish Jev's judgment quality. The
[parent example](../README.md) demonstrates the alternative guardian reviewer
backed by a separate LLM runner.
