# Sandbox Code Execution Example

This example exercises the `codeexecutor/sandbox` executor at two layers:
real LLMAgent + runner + `workspace_exec` tool scenarios, and deterministic
runtime-level sandbox behavior checks.

From the repository root, load model credentials without printing them:

```bash
source ./glm.sh
```

Then run the example from the `examples` module:

```bash
cd examples
go run ./sandboxcodeexecution -scenario all -model glm-4.7-flash
```

If `OPENAI_API_KEY` is not set, the LLM-backed scenarios are skipped with a
clear message. The example never reads or prints key contents.

## Scenarios

- `basic`: runs an LLMAgent with the sandbox executor and asks it to execute a
  deterministic Python calculation.
- `agent-tool-manual-run`: manual observation scenario for ad hoc prompts.
- `agent-tool-basic`: runs a real LLMAgent through `runner.Run`; the model must
  call the `workspace_exec` tool to compute deterministic statistics.
- `agent-tool-session-persistence`: runs two real turns in the same runner
  session; the model uses `workspace_exec` to create a file, then reads it in a
  later turn.
- `agent-tool-security`: runs a real LLMAgent and asks it to use
  `workspace_exec` to verify env redaction and deny-read behavior.
- `agent-artifact-stage`: seeds an in-memory artifact service, asks a real
  LLMAgent to call an artifact staging tool, then uses `workspace_exec` to
  consume the staged artifact in the sandbox.
- `agent-artifact-save`: asks a real LLMAgent to create a workspace output and
  persist it through `workspace_save_artifact`, verifying the returned
  `artifact://` reference is actually loadable.
- `agent-artifact-pin`: verifies pinned `artifact://` inputs stay on the
  originally resolved version across turns in the same session.
- `session-persistence`: verifies one session can see files created by a prior
  turn.
- `session-isolation`: verifies a different session cannot see another
  session's workspace files.
- `env-redaction`: verifies sandbox child processes do not inherit
  `OPENAI_API_KEY` by default.
- `metadata-protection`: verifies protected metadata paths are blocked through
  both file API and shell execution.
- `deny-read`: verifies deny-read policy blocks file API reads and shell access.
- `network-restricted`: verifies restricted networking prevents outbound socket
  connections.
- `timeout`: verifies long-running commands are timed out.
- `output-cap`: verifies large output is capped and marked as truncated.
- `additional-permissions`: verifies a host path grant is scoped to one
  operation and expires afterward.

## Flags

```bash
-scenario basic|agent-tool-manual-run|agent-tool-basic|agent-tool-session-persistence|agent-tool-security|agent-artifact-stage|agent-artifact-save|agent-artifact-pin|session-persistence|session-isolation|env-redaction|metadata-protection|deny-read|network-restricted|timeout|output-cap|additional-permissions|all
-model glm-4.7-flash
-workspace-root /tmp/my-sandbox-root
-keep-workspace
-require-os-sandbox=true
```

On Linux, the managed sandbox requires `bwrap` and user namespace support. If
the OS sandbox cannot be set up, the example reports the typed setup/backend
error and does not fall back to local execution.
