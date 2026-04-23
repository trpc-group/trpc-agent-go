# CodeExecutor and Workspace

`codeexecutor` provides a controlled execution environment for an Agent.

## What It Is Used For

Once `codeexecutor` is enabled, an Agent can run programs inside a workspace
and read or write files in that workspace.

Common use cases include:

- running shell commands or code
- processing files in a fixed working directory
- making uploaded user files available to the execution environment
- generating output files for later steps
- switching between local, container, and Jupyter backends

If your Agent only generates text and does not need program execution or local
file handling, this layer is usually unnecessary.

## Quick Start

Configure an executor on `LLMAgent`:

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
    m := openai.New("gpt-4.1-mini")

    agent := llmagent.New(
        "demo",
        llmagent.WithModel(m),
        llmagent.WithInstruction("Use files from the workspace to complete the task."),
        llmagent.WithCodeExecutor(local.New()),
    )

    r := runner.NewRunner("demo", agent)
    defer r.Close()

    msg := model.NewUserMessage("Read the input file and summarize it.")
    events, _ := r.Run(context.Background(), "user-1", "session-1", msg)
    for range events {
    }
}
```

More complete examples:

- [examples/codeexecution/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/main.go) (local backend)
- [examples/codeexecution/container/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/container/README.md) (Docker container backend)
- [examples/codeexecution/jupyter/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/jupyter/README.md) (Jupyter kernel backend)

### `WithCodeExecutor` vs fenced-code auto-execution

`llmagent.WithCodeExecutor(...)` and the response-side fenced-code
auto-execution processor are **two independent switches**. It is worth
internalising this distinction up front, because a lot of confusion
comes from treating them as a single knob.

- `WithCodeExecutor(...)` supplies a *runtime* that execution-backed
  tools — most notably `workspace_exec` — use to run commands. It does
  not, by itself, cause anything to be executed from the assistant's
  reply.
- `EnableCodeExecutionResponseProcessor` (default: `true`, toggled via
  `WithEnableCodeExecutionResponseProcessor(enable bool)`) controls
  whether the framework scans the assistant reply and, if it is exactly
  one runnable fenced code block, runs that block automatically.

Auto-execution of fenced code actually fires only when *both* are true:
an executor is available **and** the response processor is enabled.

If you only want the executor to power `workspace_exec` or other
tool-backed execution paths, and do not want assistant replies to be
auto-executed, opt out of the response-side processor explicitly:

```go
agent := llmagent.New(
    "demo",
    llmagent.WithModel(m),
    llmagent.WithCodeExecutor(local.New()),
    llmagent.WithEnableCodeExecutionResponseProcessor(false),
)
```

Common cases for disabling fenced-code auto-execution:

- using `workspace_exec` only
- providing a runtime for other tools
- requiring code execution to happen only through explicit tool calls

Interaction with `WithSkills(repo)` auto-fallback: when the skills
layer implicitly injects a local `CodeExecutor` on your behalf (see the
Agent Skills guide), that implicit executor is treated as "only here to
power `workspace_exec`". In that case the framework automatically sets
`EnableCodeExecutionResponseProcessor=false` unless you explicitly
called `WithEnableCodeExecutionResponseProcessor(...)` yourself. Using
`WithCodeExecutor(...)` explicitly, by contrast, leaves the switch at
its framework default so your existing behavior is preserved.

## Choosing a Backend

Common backends:

- `local.New()`
  Runs directly on the host machine. Easiest to wire up and debug.
- `container.New()`
  Runs inside a container. Better isolation and closer to production.
- `jupyter.New()`
  Best for notebook or kernel-style execution, especially Python analysis.

Typical recommendations:

- local development: `local`
- isolated or production-like execution: `container`
- notebook workflows: `jupyter`

## Workspace Layout

Programs run inside a workspace. Common directories are:

- `work/inputs/`
  Input files prepared before execution. Uploaded user files usually appear
  here.
- `work/`
  Temporary working directory for intermediate files.
- `out/`
  Output directory for final results or files that later steps may read.
- `runs/`
  Per-run auxiliary files such as logs.

Common paths:

- read user input files from `work/inputs/`
- write intermediate files to `work/`
- write result files to `out/`

## Where Uploaded Files Appear

On execution paths that support conversation-file auto-staging, the framework
materializes these files before execution into:

- the `work/inputs/` directory

The actual filename may be sanitized or de-duplicated, so the original basename
is not guaranteed verbatim.

There are two common ways to provide such files.

### Option 1: Put File Content in the Message

```go
msg := model.NewUserMessage("Please process this file.")
_ = msg.AddFilePath("/tmp/report.pdf")
```

You can also provide raw bytes directly:

```go
msg := model.NewUserMessage("Please process this file.")
_ = msg.AddFileData("report.pdf", pdfBytes, "application/pdf")
```

### Option 2: Upload to Artifact First, Then Attach a Reference

If the file is already stored in the artifact service, attach an
`artifact://...` reference as `file_id`:

```go
msg := model.NewUserMessage("Please process this file.")
msg.AddFileIDWithName("artifact://uploads/report.pdf@1", "report.pdf")
```

Before execution, the framework resolves that reference and writes the file
under `work/inputs/`.

## Example: Upload to Artifact First

This example shows the full flow for uploading a file first and letting the
executor stage it automatically later:

```go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/artifact"
    "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
    ctx := context.Background()

    const (
        appName   = "my-app"
        userID    = "user-1"
        sessionID = "sess-1"
    )

    artifactService := inmemory.NewService()

    rawPath := "/tmp/report.pdf"
    data, err := os.ReadFile(rawPath)
    if err != nil {
        panic(err)
    }
    base := filepath.Base(rawPath)

    info := artifact.SessionInfo{
        AppName:   appName,
        UserID:    userID,
        SessionID: sessionID,
    }

    name := "uploads/" + base
    version, err := artifactService.SaveArtifact(
        ctx,
        info,
        name,
        &artifact.Artifact{
            Data:     data,
            MimeType: "application/pdf",
            Name:     base,
        },
    )
    if err != nil {
        panic(err)
    }

    ref := fmt.Sprintf("artifact://%s@%d", name, version)

    msg := model.NewUserMessage("Read this file and summarize it.")
    msg.AddFileIDWithName(ref, base)

    agent := llmagent.New(
        "demo",
        llmagent.WithModel(openai.New("gpt-4.1-mini")),
        llmagent.WithInstruction("Read the file from work/inputs/ and summarize it."),
        llmagent.WithCodeExecutor(local.New()),
    )

    r := runner.NewRunner(
        appName,
        agent,
        runner.WithArtifactService(artifactService),
    )
    defer r.Close()

    events, err := r.Run(ctx, userID, sessionID, msg)
    if err != nil {
        panic(err)
    }
    for range events {
    }
}
```

Requirement:

- the `AppName / UserID / SessionID` used in `SaveArtifact`
- must match the values used later in `Runner.Run(...)`

Otherwise the framework will not find the artifact when resolving the
`artifact://...` reference.

## How Tools Usually Use These Files

When your Agent exposes workspace tools such as `workspace_exec`, the common
flow is:

1. read files from `work/inputs/...`
2. process them in `work/` or `out/`
3. read `out/...` and return the final answer

This keeps the contract simple: tools and models rely on stable paths instead
of dealing with staging internals.

## Workspace Bootstrap: Preparing the Workspace Before User Commands

Some workspaces need predictable setup before `workspace_exec` runs any
user-authored command: a preloaded config file, a pinned Python virtualenv, a
one-shot `pip install`, etc. Rather than teaching the model to perform this
setup itself (which is error-prone and burns prompt tokens), the framework
lets you declare it once on the agent.

Use `codeexecutor.WorkspaceBootstrapSpec` to list the required files and
commands, and wire it in via `llmagent.WithWorkspaceBootstrap(...)`. The first
`workspace_exec` call in each workspace will converge the workspace to that
spec; later calls find everything already in place and skip the work.

```go
package main

import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func newAgent() *llmagent.LLMAgent {
    bootstrap := codeexecutor.WorkspaceBootstrapSpec{
        Files: []codeexecutor.WorkspaceFile{
            {
                Target:  "work/config.json",
                Content: []byte(`{"threshold": 0.8}`),
            },
            {
                Target: "work/requirements.txt",
                Content: []byte(
                    "numpy==1.26.4\npandas==2.2.2\n",
                ),
            },
        },
        Commands: []codeexecutor.WorkspaceCommand{
            {
                Cmd: "bash",
                Args: []string{
                    "-lc",
                    "python3 -m venv .venv && " +
                        ".venv/bin/pip install -q -r work/requirements.txt",
                },
                MarkerPath: ".venv/bin/pip",
                // FingerprintInputs folds requirements.txt content
                // into the command fingerprint, so the install
                // reruns when the pinned versions change. Without
                // this the marker would short-circuit the command
                // after the first successful install.
                FingerprintInputs: []string{"work/requirements.txt"},
                Timeout:           2 * time.Minute,
            },
        },
    }

    return llmagent.New(
        "analyst",
        llmagent.WithCodeExecutor(local.New()),
        llmagent.WithWorkspaceBootstrap(bootstrap),
    )
}
```

### Field reference

`WorkspaceFile`:

- `Target`: workspace-relative destination path (required). Parent
  directories are created automatically.
- `Content`: inline bytes to write.
- `Input`: alternatively, a `codeexecutor.InputSpec` that resolves
  `artifact://`, `host://`, `workspace://`, or `skill://` URIs. Exactly
  one of `Content` and `Input` must be set.
- `Mode`: optional file mode (octal); defaults to `0o644`.
- `Key`: optional stable identifier used for idempotency; if omitted it is
  derived from `Target`.
- `Optional`: if true, provider errors are logged as warnings instead of
  aborting workspace preparation.

`WorkspaceCommand`:

- `Cmd` / `Args` / `Env` / `Cwd`: standard exec wiring. `Cwd` is
  workspace-relative and defaults to the workspace root.
- `Timeout`: bounds a single invocation.
- `MarkerPath`: workspace-relative file whose *existence* signals the
  command has already run; this is the cheapest way to make a command
  self-healing after the marker gets deleted. When absent, the reconciler
  falls back to fingerprint-only skip.
- `ObservedPaths`: alternative to `MarkerPath` when success is defined by a
  set of files rather than a single marker.
- `FingerprintInputs` / `FingerprintSalt`: fold external inputs into the
  command fingerprint so the command reruns when they change. **The
  fingerprint does not automatically hash files referenced on the command
  line**, so a command like `pip install -r work/requirements.txt` must
  list `work/requirements.txt` here explicitly — otherwise the marker
  will short-circuit the install after the first successful run even if
  `requirements.txt` is later edited.
- `Key`: stable identifier used for idempotency; derived from `Cmd`/`Args`
  when omitted.
- `Optional`: same semantics as for files.

### Ordering and idempotency

- Files are staged first, then commands run, both in declaration order.
- Each entry is fingerprinted (content for files, command line + optional
  inputs for commands). On subsequent invocations, the reconciler checks
  the fingerprint *and* the on-disk sentinel before skipping, so a user
  `rm -rf` inside `workspace_exec` does not silently leave the workspace
  half-broken: the next call re-applies the missing piece.
- Reconciliation is serialized per workspace (in-process), so two
  parallel `workspace_exec` calls on the same session cannot step on each
  other during preparation.
- Entries without `Optional` set (the default required behavior) abort
  the `workspace_exec` call before the user command runs on failure;
  `Optional: true` entries downgrade to a logged warning.

### Opting out

If you want to keep the legacy "stage conversation files only" behavior
— for example, because an existing regression test depends on it —
pass `llmagent.WithWorkspacePreparersDisabled(true)` on the agent. In
normal use this switch should not be necessary.

### Interaction with `skill_load`

Skills loaded via `skill_load` are materialized into
`skills/<name>` through the same reconcile path. You do not need to
add anything to `WorkspaceBootstrapSpec` for skills — they are picked
up automatically from session state whenever `workspace_exec` runs.
The bootstrap spec is for static, session-independent setup.

## What Persists Across Turns

There are two important cases:

- the request lands on the same physical workspace
- the request lands on a fresh workspace

In the same physical workspace:

- files in `work/` and `out/` are usually still available
- result files can often be read again directly

In a fresh workspace:

- conversation file inputs can usually be re-staged from visible message
  history
- old `out/**` files are not restored automatically
- old `work/**` files should not be assumed to exist

If a file must survive beyond a fresh workspace, store it as an artifact or
persist it explicitly in your application layer.

## Provider File IDs vs Artifact References

Some providers support native `file_id` values. Whether those IDs can be
downloaded back by the executor depends on the model integration.

If executor-side access must be reliable, prefer:

- `artifact://...`

This path is managed by the framework's artifact service and does not depend on
provider-specific file download support.

## Environment Variables

When the executor runs in a container, on a remote worker, or in another
isolated environment, environment variables usually need to be injected
explicitly.

Typical use cases:

- passing user-scoped tokens
- injecting tenant or region configuration
- avoiding exposure of sensitive values to the model

Relevant wrappers:

- `NewEnvInjectingCodeExecutor`
- `NewEnvInjectingEngine`

## FAQ

### Why can the model not find an uploaded file?

Check:

- whether the file was actually attached to the message
- whether the filename matches what the prompt refers to
- whether the Runner is configured with an `artifact.Service`
- whether the session information for `artifact://...` matches

### Why did an old file under `out/` disappear on the next turn?

If the next request lands on a different physical workspace, old `out/**` files
may be missing. Conversation file inputs can be re-staged, but `out/**` is not
restored automatically by default.

### When should I use `work/` instead of `out/`?

- temporary intermediate files: `work/`
- result files that later steps may read: `out/`

### Is `codeexecutor` tied to one specific tool?

No. It is a lower-level execution and workspace capability. Which concrete tool
exposes it depends on the higher-level Agent and tool wiring.

## References

- Examples:
  - [examples/codeexecution/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/main.go) (local backend)
  - [examples/codeexecution/container/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/container/README.md) (Docker container backend)
  - [examples/codeexecution/jupyter/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/jupyter/README.md) (Jupyter kernel backend)
- Related docs:
  - [Artifact](artifact.md)
