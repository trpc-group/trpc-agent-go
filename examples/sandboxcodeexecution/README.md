# Sandbox Code Execution Example

This example exercises the `codeexecutor/sandbox` executor at two layers:
real LLMAgent + runner + `workspace_exec` tool scenarios, and deterministic
runtime-level sandbox behavior checks.

From the repository root, load model credentials without printing them:

```bash
source ./glm.sh
```

You can also export equivalent OpenAI-compatible variables yourself:
`OPENAI_API_KEY`, `OPENAI_BASE_URL`, and `MODEL_NAME`.

Then run the example from the `examples` module:

```bash
cd examples
go run ./sandboxcodeexecution -scenario all -model "${MODEL_NAME:-glm-4.7-flash}"
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
  `workspace_exec` to verify env redaction and no-access behavior.
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
  `OPENAI_API_KEY` when the scenario explicitly uses the stricter `Core`
  shell environment policy.
- `metadata-protection`: verifies protected metadata paths are blocked through
  both file API and shell execution.
- `no-access`: verifies no-access policy blocks file API reads and shell access.
- `network-restricted`: verifies restricted networking prevents outbound socket
  connections.
- `network-policy-restricted`: verifies the default managed profile blocks
  outbound socket connections.
- `network-policy-enabled`: verifies `NetworkEnabled` allows outbound socket
  connections through the host network namespace.
- `network-policy-additional-permissions`: verifies a per-command network grant
  can enable networking for one run without changing the base restricted
  profile.
- `network-policy-agent-enforcement`: uses a real LLMAgent and `workspace_exec`
  to verify restricted networking through an actual model call.
- `timeout`: verifies long-running commands are timed out.
- `output-cap`: verifies large output is capped and marked as truncated.
- `additional-permissions`: verifies a host path grant is scoped to one
  operation and expires afterward.
- `shell-environment-policy-default-all`: verifies the default shell environment
  policy inherits a harmless host variable, matching Codex.
- `shell-environment-policy-core`: verifies `Core` preserves shell startup
  variables while hiding a custom host variable.
- `shell-environment-policy-none-set`: verifies `None` starts empty except
  explicit `Set` entries and sandbox runtime variables.
- `shell-environment-policy-include-only`: verifies `IncludeOnly` is a final
  allow-list over inherited, `Set`, and per-run environment variables.
- `shell-environment-policy-exclude-set`: verifies `Exclude` runs before `Set`.
- `shell-environment-policy-agent`: uses a real LLMAgent and `workspace_exec` to
  verify shell environment policy behavior through an actual model call.
- `file-system-policy-access-modes`: verifies read, write, no-access, metadata
  protection, and restricted networking.
- `file-system-policy-specificity`: verifies a more specific read rule can make
  a subtree read-only under a writable workspace.
- `file-system-policy-glob-no-access`: verifies glob no-access policy blocks
  file API reads and writes for existing matches.
- `file-system-policy-agent-enforcement`: uses a real LLMAgent and
  `workspace_exec` to verify no-access enforcement.
- `file-system-policy-symlink-no-access`: uses a real LLMAgent to create a
  symlink to a denied path, then calls a collect helper tool to verify the
  resolved target is still denied.
- `file-system-policy-stage-target-validation`: uses a real LLMAgent to call a
  staging helper tool and verify recursive target validation rejects a
  no-access child.
- `file-system-policy-put-files-symlink-target`: verifies host-side
  `PutFiles` rejects final symlink redirects into protected, no-access, or
  outside-workspace targets.
- `file-system-policy-host-stage-absolute-grant`: verifies host staging only
  accepts absolute sources authorized by absolute host read grants.
- `file-system-policy-host-stage-source-symlink`: verifies recursive host
  staging rejects source symlinks instead of following them outside a grant.
- `file-system-policy-directory-no-access-mask`: verifies directory-level
  no-access masks are not writable scratch space in the Linux sandbox.
- `file-system-policy-missing-no-access-mask`: verifies missing concrete
  no-access paths under writable mounts are protected by an inaccessible
  placeholder mount.
- `file-system-policy-glob-writable-reject`: verifies glob no-access rules that
  overlap writable mounts fail closed before sandbox execution.
- `session-workspace-id-sanitization`: verifies distinct session IDs that
  sanitize similarly, such as `user:a` and `user_a`, remain isolated.
- `session-policy-explicit-zero`: uses a real LLMAgent to call a deterministic
  probe that verifies `SessionPolicy{}` preserves explicit per-turn/parallel
  semantics and cleans up the workspace.

## Flags

```bash
-scenario basic|agent-tool-manual-run|agent-tool-basic|agent-tool-session-persistence|agent-tool-security|agent-artifact-stage|agent-artifact-save|agent-artifact-pin|session-persistence|session-isolation|env-redaction|metadata-protection|no-access|network-restricted|network-policy-restricted|network-policy-enabled|network-policy-additional-permissions|network-policy-agent-enforcement|timeout|output-cap|additional-permissions|shell-environment-policy-default-all|shell-environment-policy-core|shell-environment-policy-none-set|shell-environment-policy-include-only|shell-environment-policy-exclude-set|shell-environment-policy-agent|file-system-policy-access-modes|file-system-policy-specificity|file-system-policy-glob-no-access|file-system-policy-agent-enforcement|file-system-policy-symlink-no-access|file-system-policy-stage-target-validation|file-system-policy-put-files-symlink-target|file-system-policy-host-stage-absolute-grant|file-system-policy-host-stage-source-symlink|file-system-policy-directory-no-access-mask|file-system-policy-missing-no-access-mask|file-system-policy-glob-writable-reject|session-workspace-id-sanitization|session-policy-explicit-zero|all
-model glm-4.7-flash
-workspace-root /tmp/my-sandbox-root
-keep-workspace
-require-os-sandbox=true
```

On Linux, the managed sandbox requires `bwrap` and user namespace support. If
the OS sandbox cannot be set up, the example reports the typed setup/backend
error and does not fall back to local execution.
