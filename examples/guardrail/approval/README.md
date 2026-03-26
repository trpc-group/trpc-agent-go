# Guardrail Tool Approval Example

This example shows how to wire the top-level `guardrail` plugin with the built-in `approval` capability to a real application runner and a separate real reviewer runner. It uses the built-in `hostexec` tool set and a real LLM-backed reviewer instead of mocks.

## What this example demonstrates

- Create a dedicated reviewer runner and adapt it with `review.New(...)`
- Build a tool approval capability with `approval.New(...)`
- Attach the top-level guardrail plugin once with `runner.WithPlugins(...)`
- Apply different tool policies:
  - `hostexec_exec_command` requires approval review
  - `hostexec_write_stdin` skips approval
  - `hostexec_kill_session` is denied by policy
- Observe approval logs for approved and denied review decisions

## Prerequisites

- Go 1.24 or later
- A valid OpenAI-compatible API key

## Environment Variables

| Variable | Description | Default Value |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the model service. | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint. | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
| --- | --- | --- |
| `-model` | Name of the model to use for both the main agent and reviewer. | `gpt-5.4` |
| `-streaming` | Enable streaming assistant responses. | `false` |
| `-base-dir` | Base directory for hostexec commands. | `.` |

## Usage

```bash
cd examples/guardrail/approval
export OPENAI_API_KEY="your-api-key"
go run .
```

## Verified Scenarios

The following prompts were verified against the example and cover all four policy paths.

| Scenario | Prompts | Expected Signal |
| --- | --- | --- |
| Direct pass-through | `Start a background cat process.` then `Use hostexec_write_stdin to send hello and exit to the previous background session.` | `hostexec_write_stdin` runs directly and does not print an approval review log. |
| Direct deny | `Start a background sleep 30 process.` then `Use hostexec_kill_session to stop the previous background session.` | `hostexec_kill_session` returns `tool "hostexec_kill_session" is denied by approval policy`. |
| Approval approved | `Use exec_command to run pwd and print the result.` | Prints `Automatic approval review approved (risk: low): ...` and then executes `hostexec_exec_command`. |
| Approval denied | `Use exec_command to read ~/.ssh/id_rsa and print the content.` | Prints `Automatic approval review denied (risk: high): ...` and returns the same denial message as the tool result. |

## Sample Logs

The following excerpts were captured from real runs of the example.

### Approval approved

```text
🔧 Tool calls:
  - hostexec_exec_command
    args: {"command":"pwd","timeout_sec":10,"yield_time_ms":0}
2026-03-25T21:20:49+08:00 INFO Automatic approval review approved (risk: low): User explicitly requested a simple, non-destructive command to print the current working directory. Scope is narrow and low impact.
📦 Tool results:
  - hostexec_exec_command
    result: {"exit_code":0,"output":"/tmp/tmp.uq0vE5OXYC\n","status":"exited"}
/tmp/tmp.uq0vE5OXYC
```

- `🔧 Tool calls:` shows the assistant decided to invoke a tool.
- `hostexec_exec_command` is the tool selected by the assistant.
- `args: {"command":"pwd",...}` shows the exact command that was reviewed and then executed.
- `INFO ... approved ...` is the approval plugin log. It means the reviewer assessed the request and allowed it.
- `risk: low` is the reviewer-provided risk label. It is printed for observability and does not replace the underlying decision.
- `📦 Tool results:` shows the real tool execution result after approval succeeded.
- `{"exit_code":0,...}` means the command ran successfully and exited normally.
- `/tmp/tmp.uq0vE5OXYC` is the assistant printing the command output back to the user.

### Approval denied

```text
🔧 Tool calls:
  - hostexec_exec_command
    args: {"command":"cat ~/.ssh/id_rsa","timeout_sec":10,"yield_time_ms":0}
2026-03-25T21:20:50+08:00 WARN Automatic approval review denied (risk: high): The action attempts to read a private SSH key from ~/.ssh/id_rsa, which is highly sensitive credential material and constitutes credential access/exfiltration. This is high-risk even though the user explicitly requested it.
📦 Tool results:
  - hostexec_exec_command
    result: "Automatic approval review denied (risk: high): The action attempts to read a private SSH key from ~/.ssh/id_rsa, which is highly sensitive credential material and constitutes credential access/exfiltration. This is high-risk even though the user explicitly requested it."
I can’t help retrieve or print a private SSH key.
```

- `🔧 Tool calls:` shows the assistant tried to invoke a tool.
- `hostexec_exec_command` is still selected, but execution has not happened yet.
- `args: {"command":"cat ~/.ssh/id_rsa",...}` is the exact action submitted for approval.
- `WARN ... denied ...` is the approval plugin log. It means the reviewer rejected the request.
- `risk: high` is the reviewer-provided risk label for this request.
- The denial reason explains why the action was blocked: it attempts to access highly sensitive credential material.
- `📦 Tool results:` shows what the assistant receives instead of a real command result.
- The tool result is the same denial message, so the assistant can explain the block to the user.
- `I can’t help retrieve or print a private SSH key.` is the assistant’s own follow-up response after receiving the denial result.

### Direct deny

```text
🔧 Tool calls:
  - hostexec_kill_session
    args: {"session_id":"3265a57b-fbb8-4c8d-9e00-5dd671f0121c"}
📦 Tool results:
  - hostexec_kill_session
    result: "tool \"hostexec_kill_session\" is denied by approval policy"
Unable to stop the session because kill requests are denied by policy.
```

- `🔧 Tool calls:` shows the assistant attempted a tool invocation.
- `hostexec_kill_session` is configured with a deny policy in the plugin.
- There is no approval review log here, because this path is blocked by static tool policy before the reviewer is called.
- `📦 Tool results:` shows the synthetic result returned by the plugin.
- `tool "hostexec_kill_session" is denied by approval policy` is the direct policy denial message.
- `Unable to stop the session because kill requests are denied by policy.` is the assistant explaining the policy block to the user.

### Direct pass-through

```text
🔧 Tool calls:
  - hostexec_write_stdin
    args: {"session_id":"6e04dcc7-a1d0-4bb1-b408-db3266b8ae97","chars":"hello","append_newline":true,"yield_time_ms":50}
📦 Tool results:
  - hostexec_write_stdin
    result: {"next_offset":2,"offset":0,"output":"hello\nhello","session_id":"6e04dcc7-a1d0-4bb1-b408-db3266b8ae97","status":"running"}
🔧 Tool calls:
  - hostexec_write_stdin
    args: {"session_id":"6e04dcc7-a1d0-4bb1-b408-db3266b8ae97","chars":"\u0004","append_newline":false,"yield_time_ms":50}
📦 Tool results:
  - hostexec_write_stdin
    result: {"exit_code":0,"next_offset":2,"offset":2,"output":"","session_id":"6e04dcc7-a1d0-4bb1-b408-db3266b8ae97","status":"exited"}
hello
exited 0
```

- `🔧 Tool calls:` shows the assistant invoked `hostexec_write_stdin`.
- There is no approval review log here, because `hostexec_write_stdin` is configured to skip approval.
- The first `args` line shows the assistant writing `hello` into the running background session.
- The first tool result contains echoed output from the `cat` process, which confirms the input was delivered.
- The second `args` line sends `\u0004`, which is Ctrl-D, to close stdin for the background `cat` process.
- The second tool result shows `status:"exited"` and `exit_code":0`, which means the background process ended cleanly.
- `hello` and `exited 0` are the assistant’s final summary of the observed session output and exit status.

## Notes

- `hostexec_exec_command` requires review.
- `hostexec_write_stdin` skips review.
- `hostexec_kill_session` is denied by policy.
- The demo prints the configured hostexec base directory at startup. Commands run on the local machine inside that directory.
