# Codex Sandbox Adoption Plan

## Goal

Turn the Codex sandbox design into a concrete adoption plan for
`trpc-agent-go` and `openclaw`, and make three decisions explicit:

- what we can copy as-is
- what we must adapt
- what we should not copy

The key conclusion is:

- copy Codex's layered design and Linux local sandbox pipeline
- adapt Codex's product assumptions, especially approvals and repo-centric semantics
- prioritize `openclaw`, because it is the externally reachable runtime surface

## One-Line Conclusion

If we copy the full Codex sandbox design directly, we will likely improve
`codeexecutor/local`, but still leave `openclaw` host tools, cron, A2A,
and long-running remote entrypoints with a different control plane.

So the right goal is not "copy the full Codex product", but:

1. copy Codex's sandbox substrate
2. adapt Codex's control plane
3. make `trpc-agent-go` and `openclaw` share one policy model

## Codex Route vs NanoClaw Route Decision Tables

### Capability Comparison

| Dimension | Codex route | NanoClaw route | Decision for `trpc-agent-go` / `openclaw` |
|---|---|---|---|
| Primary isolation boundary | Process-level sandbox | Container isolation | Codex fits the default local path better; NanoClaw fits a stronger backend |
| Typical implementation | `bubblewrap` + `no_new_privs` + `seccomp` | `docker run` + explicit mounts | Both are reusable, but should not become separate policy worlds |
| Filesystem model | Read-only by default + writable roots + protected subpaths | Only explicitly mounted directories are visible | Codex fits workspace execution better; NanoClaw fits runtime-level directory slicing better |
| Env / secrets model | Minimize inherited env | Keep real credentials on host and inject via proxy | `openclaw` can benefit a lot from NanoClaw-style credential proxying |
| Network model | Easier to make "no network by default" the baseline | More naturally allows container networking, often shaped by proxy / outer runtime | First `trpc-agent-go` baseline should lean toward Codex defaults |
| Session model | Naturally optimized for per-command execution | Better aligned with long-lived agent sessions | `openclaw` PTY / background / cron behavior is closer to NanoClaw's runtime shape |
| Runtime dependency | Local helper / OS capability oriented | Strong Docker / container-runtime dependency | NanoClaw should not be the only default path in all environments |
| Local dev friction | Low to medium | Medium to high | Codex route is better for default local developer experience |
| Higher-risk workloads | Limited; not a hostile multi-tenant final answer | Stronger; still benefits from gVisor / microVM layering | Higher-risk workloads lean toward NanoClaw |
| Fit for `openclaw` host tools | Needs more work on sessions, approvals, and service control plane | A strong fit for hardened `exec_command` / `localexec` isolation | `openclaw` especially benefits from NanoClaw as a hardened mode |

### Scenario Selection Table

| Scenario | Preferred route | Why |
|---|---|---|
| `trpc-agent-go` local development, single-host trusted code, low-friction execution | Codex route | Best fit for making `workspace_exec` and `localexec` the default safe path |
| `openclaw` is externally reachable, but we still want a lower-complexity first step | Codex route as the base, plus service-aware approval and tool policy | Easier to land first while still tightening the control plane |
| `openclaw` `exec_command` / `EnableLocalExec` is exposed to semi-trusted input | NanoClaw route | Container boundary, explicit mounts, and credential proxying fit host tools better |
| We need to ensure real secrets never enter the execution environment | NanoClaw route | Credential proxying is stronger than env minimization alone |
| Heavy use of long-lived sessions, background commands, and cron replays | NanoClaw route or a unified container backend | Better aligned with the natural runtime shape |
| Hostile multi-tenant or high-risk public agent execution | NanoClaw route plus outer microVM / gVisor | Codex route alone is not enough |
| We want something that can run broadly first, then harden over time | Codex route as default, NanoClaw route as enhanced backend | Better fit for the framework role of `trpc-agent-go` |

### Final Decision Table

| Component / path | Recommended default route | Backup / hardened route | Notes |
|---|---|---|---|
| `codeexecutor/local` | Codex route | No-sandbox fallback for development only | Best place for the low-friction default |
| `tool/workspaceexec` | Codex route | NanoClaw-style container backend | Start by unifying local execution policy |
| `codeexecutor/container` | Shared policy layer + NanoClaw-style mount / proxy ideas | Add gVisor / microVM for higher-risk cases | It should stop behaving like a separate product model |
| `openclaw exec_command` | It should no longer run raw host execution | Prefer a NanoClaw-style container backend | This is the highest-value hardening target |
| `openclaw EnableLocalExec` | Raw `localexec` should not remain the long-term default | Route through the shared backend, optionally container-based | At minimum it must be behind unified approval / policy |
| External-facing `openclaw` profiles | NanoClaw route is a better fit | Codex route only for lower-risk deployments | External runtime surfaces need stronger isolation than framework defaults |

### One-Line Selection Rule

- Prefer the Codex route for default developer experience and shared framework-level execution
- Prefer the NanoClaw route for `openclaw` host tools and higher-risk execution
- For hostile multi-tenant cases, do not stop at "Codex vs NanoClaw"; use the NanoClaw route plus a stronger outer boundary

## What To Copy As-Is

| Area | What to copy | Direction in `trpc-agent-go` |
|---|---|---|
| Policy modeling | Split filesystem and network policy instead of one coarse isolation flag | Add `FileSystemPolicy`, `NetworkPolicy`, `EnvPolicy`, and `ResourceLimits` in `codeexecutor` |
| Linux local pipeline | `bubblewrap` builds the filesystem view, then `no_new_privs + seccomp`, then `exec` | Add a Linux helper/backend for `codeexecutor/local` |
| Filesystem defaults | Read-only by default, explicit writable roots, protected read-only subpaths | Apply the same semantics to workspace, repo, and runtime state paths |
| Environment handling | Construct child env from policy instead of inheriting all host env | Replace the current `os.Environ()`-style inheritance |
| Backend abstraction | Choose a platform backend from policy, then transform the command | Unify local-process, container, and no-sandbox backends |
| Temporary permission overlays | Allow narrow, short-lived extra permissions on top of a base policy | Support per-run temporary write roots, network exceptions, or extra env |

## What Must Be Adapted

### 1. Approval model

Codex assumes an interactive terminal user can approve commands.

`trpc-agent-go` and `openclaw` often run as:

- HTTP / Telegram / A2A services
- long-running background processes
- cron-triggered tasks
- systems with no synchronous approver online

So the approval model must become service-aware:

- `Allow`: explicitly permitted commands, paths, network targets, tool classes
- `Deny`: explicitly forbidden commands, paths, network, tool classes
- `Prompt`: only when an approval channel actually exists
- `AutoDenyOnNoApprover`: deny instead of silently allowing when no approver exists

### 2. Filesystem scope model

Codex is strongly repo/workspace-oriented.

`trpc-agent-go` / `openclaw` need a multi-root runtime model:

- workspace
- `openclaw` state dir
- uploads
- skills
- memory / session databases
- managed toolchain paths

Recommended semantics:

- workspace writable by default
- state / uploads writable only within controlled subtrees
- skills read-only by default
- repo metadata and sensitive hidden paths always read-only

### 3. Network policy

In Codex, network policy can mostly be modeled around "can this command use the network?"

In `openclaw`, this must split into two planes:

- runtime plane: gateway, Telegram, model traffic, A2A
- tool execution plane: child processes started by `exec_command`, `localexec`, or `workspace_exec`

That means:

- runtime traffic must not imply tool traffic
- tool execution should default to no network
- future outbound access should prefer domain allowlists or proxy mode

### 4. Long-running and interactive sessions

Codex mainly optimizes per-command execution.

`openclaw` also needs:

- PTY sessions
- `write_stdin`
- background commands
- cron replays
- long-lived A2A-triggered runs

So the sandbox backend must support session-scoped guarantees:

- foreground and background paths use the same policy objects
- PTY sessions keep the same sandbox through their full lifecycle
- cron reruns re-apply policy instead of falling back to raw host execution

### 5. `openclaw` product defaults

This cannot simply inherit Codex CLI expectations.

Because `openclaw` is an external runtime surface, defaults must be stricter:

- host tools off or tightly constrained in external-facing profiles
- `enable-local-exec` must not become a raw host execution escape hatch
- `exec_command` must not remain a direct `bash -lc` surface
- tool exposure must still pass through policy even when `llm` mode is used

### 6. Container executor alignment

Codex's primary path is a local process sandbox.

`trpc-agent-go` already has a container executor, so the design must be adapted so that:

- local-process and container backends share the same policy objects
- filesystem, network, env, and resource semantics match across both paths
- the container backend is a stronger isolation option, not a different product model

## What We Should Not Copy

### 1. Do not treat process sandboxing as the final answer for hostile multi-tenant workloads

Codex-style process sandboxing is strong for local host protection, but it should not be treated as:

- sufficient for hostile multi-tenant code
- enough to expose `openclaw` directly to untrusted internet traffic
- a replacement for containers, gVisor, or microVMs

### 2. Do not assume a human approver is always present

Remote bots, A2A sub-agents, and cron jobs often have no synchronous human approver.

If we copy a prompt-first assumption directly, the system will either:

- stall waiting for approval
- or introduce unsafe auto-allow fallbacks

For service runtimes, the safer rule is:

- prompt only when an approval channel exists
- otherwise deny

### 3. Do not copy repo-only semantics

Codex protects a local repo as the primary asset.

`openclaw` has a wider runtime surface:

- uploads
- state
- sqlite files
- skills
- toolchains
- cron persistence

Protecting only repo-style paths leaves major runtime data outside the model.

### 4. Do not expose a service-facing "fully bypass approvals and sandbox" switch

A local CLI can tolerate a strong bypass option because the operator is usually the host owner.

Once `trpc-agent-go` or `openclaw` is service-facing, that kind of switch becomes a high-risk configuration.

If it exists at all, it should be:

- development-only
- clearly marked dangerous
- off by default
- absent from external-facing profiles

### 5. Do not merge runtime networking and tool networking into one flag

A single "network on/off" switch creates two bad outcomes:

- turning it off can break the runtime itself
- turning it on can give child processes too much host network access

These two planes must stay separate.

## Recommended Target Structure

### 1. Unified policy layer

Add shared execution policy objects:

- `FileSystemPolicy`
- `NetworkPolicy`
- `EnvPolicy`
- `ResourceLimits`
- `ApprovalPolicy`
- `ExecutionIntent`

`ExecutionIntent` should identify where execution came from, for example:

- `workspace_exec`
- `localexec`
- `openclaw_exec_command`
- `openclaw_cron`
- `openclaw_background_session`

### 2. Unified backend layer

At minimum, provide:

- `LocalLinuxSandboxBackend`
- `ContainerSandboxBackend`
- `NoSandboxBackend`

Requirements:

- all backends consume the same policy objects
- `NoSandboxBackend` is explicit and limited to development / compatibility cases
- the secure default path prefers `LocalLinuxSandboxBackend`

### 3. Unified integration points

Connect these call sites first:

1. `codeexecutor/local`
2. `tool/workspaceexec`
3. `openclaw/internal/octool`
4. `openclaw` `EnableLocalExec`
5. `codeexecutor/container`

This removes the split state where one path is sandboxed and another still runs raw `bash -lc` on the host.

## Recommended Rollout

### Phase 1: land the parts we can copy directly

- introduce the split policy model
- add `bubblewrap + no_new_privs + seccomp` for Linux local execution
- minimize inherited environment variables
- make filesystem semantics read-only-by-default with explicit writable roots

### Phase 2: wire `openclaw` into the shared policy layer

- route `exec_command` through the shared sandbox backend
- route `enable-local-exec` through the shared sandbox backend
- tighten host-tool defaults by profile
- add a service-aware approval policy for remote entrypoints

### Phase 3: cover long-running sessions and the container path

- bring PTY / background / cron into the same policy model
- make the container executor consume the same policy objects
- add resource limits, audit records, and policy-hit visibility

### Phase 4: add stronger outer boundaries only when needed

- evaluate gVisor / microVMs for semi-trusted or hostile workloads
- do not force high-isolation runtime choices into the first interface version

## Final Recommendation

The four Codex ideas most worth copying are:

- split policy modeling
- the Linux sandbox pipeline
- read-only-by-default filesystem semantics
- environment minimization

The four parts that most need adaptation are:

- approvals
- multi-root filesystem semantics
- split runtime-plane vs tool-execution-plane networking
- `openclaw` default tool exposure

The three assumptions we should not copy are:

- process sandboxing is enough for hostile multi-tenant execution
- interactive human approval is always available
- repo-centric semantics cover the whole `openclaw` runtime

If this moves into implementation, the first priority should not be new tools.
It should be a shared policy object model and backend interface. Once those are
stable, `workspace_exec`, `localexec`, `openclaw exec_command`, PTY sessions,
and the container executor can finally share one coherent sandbox design.
