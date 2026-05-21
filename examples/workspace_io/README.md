# Workspace I/O Example

This example shows how to mirror files out of an `LLMAgent` workspace
into a caller-managed store after the invocation finishes.

The whole pattern is `Collect` + a sink loop, written from a regular
`AgentCallbacks`. The framework does not bake the timing, error type,
or budget into a sugar option — the caller writes them where they
make sense.

| API | Used for |
| --- | --- |
| `workspaceio.WorkspaceFromContext(ctx)` | Resolves the per-invocation workspace facade from any callback context. |
| `ws.PutFiles(ctx, files...)` | Write one or many files into the workspace. The example calls it from `BeforeAgent` only to keep the demo self-contained; production code should stage external profile / skill files via `codeexecutor.WorkspaceInitHook` + `InputSpec` instead — see the *codeexecutor* docs. |
| `ws.Collect(ctx, patterns...)` | Enumerate workspace files after the run; returns `[]*workspaceio.File` with `Truncated` preserved. Same pattern syntax as `codeexecutor.WorkspaceFS.Collect`; pass a literal path to read a single file and take `result[0]`. |
| `ws.SaveArtifact(ctx, relPath)` | Persist an existing workspace file (under `work/`, `out/`, or `runs/`) as an artifact when later turns need to reference it. |
| `ws.RunProgram(ctx, spec)` | Run a program inside the current invocation's workspace and inspect `RunResult` (stdout/stderr/exit code/timed out). Mirrors what the `workspace_exec` LLM tool gives the model. |

The example uses `LocalCodeExecutor` so it runs end-to-end without
Docker or a remote sandbox. The "user-level skill store" is just a
host directory under `./skills_store`. Production deployments swap
`directorySink` for whatever storage backend they use.

## What the example does

1. Configures an `LLMAgent` with `WithCodeExecutor(localexec.New())`.
2. In `BeforeAgent`, resolves `Workspace` from `ctx` and pre-populates
   the workspace with two `SKILL.md` files
   (`skills/echoer/SKILL.md`, `skills/greeter/SKILL.md`).
   *Demo-only convenience*: writing into the workspace from
   `BeforeAgent` keeps this single-file example runnable without
   external storage. In production the same projection belongs in
   `codeexecutor.WorkspaceInitHook` (see the *codeexecutor* docs); the
   read-side patterns shown below — `Collect` + sink — apply unchanged.
3. Runs a trivial prompt against the configured model.
4. In `AfterAgent`, calls `ws.Collect(ctx, "skills/*/SKILL.md")` and
   loops over the result, validating each file and handing it to a
   `directorySink`.
5. `directorySink.Save` writes
   `skills_store/<userID>/<workspace path>` on disk.
6. Prints what landed in the store.

## Prerequisites

- Go 1.21 or later.
- A reachable model endpoint (the example uses the OpenAI Go SDK,
  which honors `OPENAI_API_KEY` and `OPENAI_BASE_URL`).

## Run

```bash
export OPENAI_API_KEY="..."
# Optionally point to an OpenAI-compatible endpoint:
# export OPENAI_BASE_URL="https://api.deepseek.com/v1"

cd examples/workspace_io
go run . \
  -model deepseek-v4-flash \
  -store ./skills_store \
  -prompt "Say a short hello so I can verify the agent finished."
```

Expected output (abridged):

```text
Workspace I/O demo
- model:        deepseek-v4-flash
- skill store:  /abs/path/to/examples/workspace_io/skills_store
============================================================
seeded workspace file: skills/echoer/SKILL.md (38 bytes)
seeded workspace file: skills/greeter/SKILL.md (37 bytes)
[assistant] Hello!
mirrored skills/echoer/SKILL.md -> .../skills_store/demo-user/skills/echoer/SKILL.md (38 bytes)
mirrored skills/greeter/SKILL.md -> .../skills_store/demo-user/skills/greeter/SKILL.md (37 bytes)
------------------------------------------------------------
Skill store after invocation:
- demo-user/skills/echoer/SKILL.md  (38 bytes)
- demo-user/skills/greeter/SKILL.md (37 bytes)
```

## The wiring in three lines

```go
cb.RegisterAfterAgent(func(
    ctx context.Context, args *agent.AfterAgentArgs,
) (*agent.AfterAgentResult, error) {
    ws, ok := workspaceio.WorkspaceFromContext(ctx)
    if !ok {
        // No code executor configured for this agent — nothing to mirror.
        return nil, nil
    }
    files, err := ws.Collect(ctx, "skills/*/SKILL.md")
    if err != nil {
        return nil, err
    }
    for _, f := range files {
        if err := sink.Save(ctx, args.Invocation, f); err != nil {
            return nil, err
        }
    }
    return nil, nil
})
```

Returning the error from `AfterAgent` makes the failure visible to
the caller. Log-and-swallow if you want flush to be best-effort.

## Adapting the example to your stack

- Replace `directorySink` with anything; the file is just `*workspaceio.File`
  (`Path`, `Data`, `MIMEType`, `SizeBytes`, `Truncated`). Wire it to
  a database, object store, HTTP service, etc.
- Tighten or relax the matched paths by changing the `Collect`
  patterns. They use the same syntax as
  `codeexecutor.WorkspaceFS.Collect` (e.g. `out/*.json`,
  `runs/**/result.md`).
- Validate before sinking. The example checks for a markdown heading;
  swap in YAML frontmatter parsing, schema checks, etc.
- Move the same loop into `AfterTool` (filtered by tool name) if you
  want to mirror state after every `workspace_exec` instead of after
  the whole invocation.

## Caller-owned policy

The framework keeps `Workspace` deliberately thin. A few things you
should think through and code yourself:

- **Skip on failure.** The example bails out when `args.Error != nil`
  because the workspace state is unreliable in that case. If you want
  partial state for post-mortem, drop the early return.
- **Truncation.** `ws.Collect` forwards the `File.Truncated` flag
  from the backend (which caps individual reads at a few MiB). If you
  do not want to silently mirror half of a `SKILL.md`, check the flag
  — see `mirrorSkillsAfterAgent` in `main.go`.
- **Volume.** A glob like `skills/**` can drag tens of MiB of
  intermediate scratch state into your sink. Keep patterns specific,
  cap on `len(files)` / sum of `SizeBytes`, or both.
- **Atomicity.** `Collect` then `Save` in a loop is not transactional.
  If you need all-or-nothing semantics, stage to a temp directory and
  rename, or use a transactional store.
- **Non-zero exit codes from `RunProgram`.** A non-zero `ExitCode` is
  reported via `RunResult`, not via error — match Go's `os/exec`
  convention. Inspect `result.ExitCode` / `result.TimedOut` and decide
  whether to fail, retry, or accept the outcome.

## Multi-node caveat

`workspaceio.Workspace` is backend-agnostic but only addresses the
*current invocation's* workspace. Whether two nodes can see the same
workspace depends on the executor:

- `local` / `container` are single-node by construction.
- `pcg123` + CFS can persist when the deployment shares the workspace id.
- Cube-style remote sandboxes depend on whether the runtime exposes a
  stable handle.

The pattern shown here — sinking into a caller-managed store after the
run — is how you get cross-node visibility regardless of backend.

## Where to look in the framework

- `codeexecutor/workspaceio/workspace_io.go` — `Workspace`,
  `Collect`, `PutFiles`, `SaveArtifact`, `StageInputs`, `RunProgram`.
- `codeexecutor/workspaceio/context.go` — `WithWorkspace`,
  `WorkspaceFromContext`.
- `agent/llmagent/llm_agent.go` — installs the `Workspace` into the
  invocation context whenever `WithCodeExecutor` is configured.
