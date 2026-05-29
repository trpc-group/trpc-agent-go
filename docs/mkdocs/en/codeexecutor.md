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
auto-execution processor are **two independent switches**. Understanding
this distinction early avoids a common point of confusion: treating them
as a single switch.

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

If the file content is meant only for tools or the code executor, configure the
OpenAI model with `openai.WithOmitFileContentParts(true)` to omit file content
parts from provider requests. This does not hide normal message text or
file-name hints that you include in the prompt. The current OpenAI adapter uses
the Chat Completions API, whose file content support is limited to the request
shapes accepted by that endpoint. PDF file data can be sent as file content,
but Markdown or plain-text content should be passed as normal message text, or
staged only for tools, instead of being sent as file content parts.

### Option 2: Upload to Artifact First, Then Attach a Reference

If the file is already stored in the artifact service, attach an
`artifact://...` reference as `file_id`:

```go
msg := model.NewUserMessage("Please process this file.")
msg.AddFileIDWithName("artifact://uploads/report.pdf@1", "report.pdf")
```

Before execution, the framework resolves that reference and writes the file
under `work/inputs/`.

### End-to-end example: upload first, stage at execution time

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

## Provider File IDs vs Artifact References

Some providers support native `file_id` values. Whether those IDs can be
downloaded back by the executor depends on the model integration.

If executor-side access must be reliable, prefer:

- `artifact://...`

This path is managed by the framework's artifact service and does not depend on
provider-specific file download support.

## How Tools Usually Use These Files

When your Agent exposes workspace tools such as `workspace_exec`, the common
flow is:

1. read files from `work/inputs/...`
2. process them in `work/` or `out/`
3. read `out/...` and return the final answer

This keeps the contract simple: tools and models rely on stable paths instead
of dealing with staging internals.

## Workspace init hooks

Hooks run **after** `WorkspaceManager.CreateWorkspace` succeeds and **before** that `Workspace` is returned to callers. When the app uses a `WorkspaceRegistry` (the default for session-scoped tools), creation runs **once per logical workspace id**—usually once per agent session workspace. Trusted-local modes may reuse one physical directory across sessions; hooks may still run whenever a distinct workspace acquisition happens. Do not assume hooks run at most once per on-disk path: re-acquisition can run them again.

Use init hooks to stage fixed inputs (`InputSpec`: `artifact://`, `host://`, etc.) and run deterministic setup commands (for example `pip install` inside a shell one-liner).

Artifact-backed inputs require the artifact service and (when applicable)
session fields on `context` when `CreateWorkspace` runs—the same requirements as
`WorkspaceFS.StageInputs`. Standard llmagent workspace tools inject this from the
current `agent.Invocation` when resolving the session workspace, so the example
below works without manual context wiring.

```go
import (
    "fmt"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func newAnalystAgent() (*llmagent.LLMAgent, error) {
    exec, err := codeexecutor.NewWorkspaceInitExecutor(
        local.New(),
        codeexecutor.NewWorkspaceInitHook(codeexecutor.WorkspaceInitSpec{
            Inputs: []codeexecutor.InputSpec{
                {
                    From: "artifact://app/requirements.txt@3",
                    To:   "work/requirements.txt",
                    Mode: "copy",
                },
            },
            Commands: []codeexecutor.WorkspaceInitCommand{
                {
                    Name: "install-deps",
                    Cmd:  "bash",
                    Args: []string{
                        "-lc",
                        "python3 -m venv .venv && " +
                            ".venv/bin/pip install -q -r work/requirements.txt",
                    },
                    Timeout: 2 * time.Minute,
                },
            },
        }),
    )
    if err != nil {
        return nil, fmt.Errorf("workspace init executor: %w", err)
    }
    return llmagent.New(
        "analyst",
        llmagent.WithCodeExecutor(exec),
    ), nil
}
```

If init inputs change later, use a **new** logical workspace (for example a new session id) or run setup again yourself; hooks do not watch files for you.

### Interaction with `skill_load`

Skills loaded via `skill_load` are materialized into `skills/<name>` when
`workspace_exec` runs, using the current session’s loaded skills. You do not
need to duplicate skill sources in init hook `Inputs`; init hooks cover
session-independent files and setup commands you want present before any tool
run.

## Accessing the Workspace From Application Code

The model drives the workspace through `workspace_exec`. Application
code sometimes needs to read what the agent just produced — mirroring
files at agreed paths into a profile store from `AfterAgent`, or
harvesting intermediate output of a specific tool from `AfterTool`;
some scenarios also need to run a command from a callback for
validation or post-processing.

`codeexecutor/workspaceio` exposes a single facade, `Workspace`. Once
`WithCodeExecutor(...)` is configured, `LLMAgent.Run` installs it into
`ctx` at entry, so any callback or tool that receives a `ctx` can
resolve it — `BeforeAgent` / `AfterAgent` / `BeforeTool` / `AfterTool`
/ `BeforeModel` / `AfterModel` / your own tool's `Run`.

It is a thin wrapper over `codeexecutor.WorkspaceFS` plus
`ProgramRunner`: the framework does not enforce truncation, volume,
atomicity, or non-zero-exit handling for you. How to validate after a
read, when to refuse on overflow, what to do when a command exits
non-zero — that all lives in your callback. See *Caller-owned policy*
below.

> **About the name collision with `codeexecutor.Workspace`:** the
> latter is a v1-published workspace descriptor (`{ID, Path}`, no
> methods); this section's `Workspace` is the facade. The two are
> disambiguated by import path; business code rarely references both
> in the same file. When it does, use an import alias:
>
> ```go
> import (
>     "trpc.group/trpc-go/trpc-agent-go/codeexecutor"
>     wsio "trpc.group/trpc-go/trpc-agent-go/codeexecutor/workspaceio"
> )
> ```

### Read and write inside callbacks

There are two recommended use patterns for `Workspace`:

- **`AfterAgent`**: when the whole turn is done, mirror the artifacts
  at agreed paths (`skills/*/SKILL.md`, `out/report.pdf`, ...) back
  into your own store. Skip on failure since the workspace state
  cannot be trusted.
- **`AfterTool` gated on `args.ToolName`**: when you only care about
  the artifacts produced by a specific tool (for example, mirroring
  files written by `workspace_exec`, or harvesting intermediate output
  of a custom skill tool), filter by tool name first so unrelated tool
  calls do not trigger a `Collect`.

`myStore.Save(...)` and `mirror(ctx, files)` in the snippets below are
interfaces you implement; the docs do not pin their signatures.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/workspaceio"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

agentCB := agent.NewCallbacks()
agentCB.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if args.Error != nil {
        return nil, nil // workspace state is unreliable on failure
    }
    ws, ok := workspaceio.WorkspaceFromContext(ctx)
    if !ok {
        return nil, nil
    }
    files, err := ws.Collect(ctx, "skills/*/SKILL.md")
    if err != nil {
        return nil, err
    }
    for _, f := range files {
        if f.Truncated {
            return nil, fmt.Errorf("%s was truncated by the backend", f.Path)
        }
        if err := myStore.Save(ctx, args.Invocation, f); err != nil {
            return nil, err
        }
    }
    return nil, nil
})

toolCB := tool.NewCallbacks()
toolCB.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    // Only mirror after the target tool finishes; avoids scanning the
    // workspace after every unrelated tool call.
    if args.ToolName != "workspace_exec" {
        return nil, nil
    }
    ws, ok := workspaceio.WorkspaceFromContext(ctx)
    if !ok {
        return nil, nil
    }
    files, err := ws.Collect(ctx, "out/**/*.json")
    if err != nil {
        return nil, err
    }
    return nil, mirror(ctx, files)
})

agent := llmagent.New("demo",
    llmagent.WithCodeExecutor(local.New()),
    llmagent.WithAgentCallbacks(agentCB),
    llmagent.WithToolCallbacks(toolCB),
)
```

> Avoid using `BeforeAgent` + `PutFiles` to project external profile
> or skill files into the workspace. That is workspace-initialization
> work and belongs in `codeexecutor.WorkspaceInitHook` (see the
> *Workspace init hooks* section above — use `InputSpec` with
> `skill://` / `artifact://` / `host://` schemes to stage everything
> at workspace creation time). `Workspace` exists for "read what the
> agent produced", "promote a workspace file to an artifact", and
> "run a one-off command for validation/post-processing", not for the
> initialization path.

### Available methods (every `path` is relative to the workspace root)

- `Collect(ctx, patterns...)` — read every file matching one of the
  patterns, returning `[]*File` (`Path`, `Data`, `MIMEType`,
  `SizeBytes`, `Truncated`). Pattern syntax matches
  `codeexecutor.WorkspaceFS.Collect` exactly: a literal path
  (`skills/echoer/SKILL.md`), a wildcard (`out/*.json`), or a recursive
  glob (`runs/**/result.md`). Single-file reads use the same entry
  point — pass a literal pattern and take `result[0]`.
- `PutFiles(ctx, files...)` — write 1..N `codeexecutor.PutFile`
  values; parent directories are created automatically. Single-file
  writes use the same entry point (pass one `PutFile`).
  `PutFile.Mode == 0` falls back to the backend default (0o644 on
  local); use `codeexecutor.DefaultExecFileMode` when you need 0o755.
- `SaveArtifact(ctx, relPath, opts...)` — persist a workspace file as
  an artifact (the `Runner` must be configured with an
  `artifact.Service`); returns `*ArtifactRef`.
- `StageInputs(ctx, specs)` — batch-stage external inputs identified
  by `artifact://`, `host://`, `workspace://`, or `skill://` URIs.
- `RunProgram(ctx, spec)` — run a program inside the workspace
  (`spec.Cwd` is interpreted relative to the workspace root and
  cannot escape it); returns `codeexecutor.RunResult` (`Stdout`,
  `Stderr`, `ExitCode`, `Duration`, `TimedOut`). **A non-zero exit
  code is NOT an error**, matching Go's `os/exec` convention — the
  caller inspects `RunResult.ExitCode` / `TimedOut` and decides
  whether to fail, retry, or accept. The returned `error` is reserved
  for framework-level failures (no executor configured, backend
  rejection, launch failure, internal timeout).

`Workspace` has no internal locking; serialize calls yourself when
ordering matters.

### How to fill `Cmd` / `Args` in `RunProgram`

`Cmd` is the executable name (**no shell parsing**); `Args` is the
argument list. The `workspace_exec` LLM tool goes through the same
`ProgramRunner.RunProgram` underneath — two patterns cover most needs.

**Run a shell one-liner** (identical to how `workspace_exec` runs LLM
commands):

```go
res, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
    Cmd:     "sh",
    Args:    []string{"-lc", "ls -la work && cat out/report.md"},
    Cwd:     "",                  // empty == workspace root
    Timeout: 30 * time.Second,
})
if err != nil {
    return err
}
if res.ExitCode != 0 || res.TimedOut {
    return fmt.Errorf("post-check failed: exit=%d timed_out=%v: %s",
        res.ExitCode, res.TimedOut, res.Stderr)
}
```

**Invoke a program directly** (no shell — zero argument escaping,
more deterministic):

```go
res, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
    Cmd:  "go",
    Args: []string{"vet", "./..."},
    Cwd:  "work",                 // run inside ${WORK}
    Env:  map[string]string{"GOFLAGS": "-count=1"}, // appended/overridden, not a replacement
})
```

Other fields:

- `Stdin` — string piped to the process stdin once at startup, then
  closed; use the `workspace_exec` LLM tool (or your own session) for
  interactive programs.
- `Timeout` — wall-clock timeout for the single run; on hit
  `RunResult.TimedOut = true` while `error` stays `nil`.
- `Env` — appended to / overriding environment variables; the
  workspace already injects `${WORK}` / `${OUT}` / `${RUNS}` /
  `${WORKSPACE_DIR}`, which you can reference directly in `Args`.

### `*File`: what a read returns

`Collect` returns a backend-agnostic snapshot per file:

```go
type File struct {
    Path      string // workspace-relative, e.g. "skills/echoer/SKILL.md"
    Data      []byte // copied bytes; callers may mutate
    MIMEType  string // backend-detected; empty when the backend did not set it
    SizeBytes int64  // actual file size; may exceed len(Data) when Truncated
    Truncated bool   // backend hit its read cap; the framework does not error
}
```

Two real uses:

- **Mirror to external storage**: `os.WriteFile(dst, f.Data, 0o644)` /
  `s3.PutObject(key, bytes.NewReader(f.Data))`. `f.Path` doubles as
  the destination key or sub-path.
- **Structural validation**: `bytes.Contains(f.Data, []byte("# "))`,
  `yaml.Unmarshal(f.Data, &frontmatter)`, schema checks — fail
  fast before mirroring.

### `*ArtifactRef` + `WithSaveArtifactMaxBytes`: expose workspace output to later turns

Use `SaveArtifact` when the model wrote something to `out/` (a
generated PDF, a synthesized dataset, a training checkpoint, ...) and
later turns need to reference it. The returned `ArtifactRef` mirrors
the `workspace_save_artifact` LLM tool's output schema:

```go
ref, err := ws.SaveArtifact(ctx, "out/report.pdf",
    workspaceio.WithSaveArtifactMaxBytes(8 * 1024 * 1024))
if err != nil {
    return err
}

// ref.Ref       == "artifact://out/report.pdf@3"  // pre-formatted reference
// ref.SavedAs   == "out/report.pdf"               // artifact key
// ref.Version   == 3
// ref.SizeBytes == 4_812_345

// Stash the reference in session state so the next turn's prompt can
// quote ref.Ref directly.
args.Invocation.Session.AppendStateValue("last_report", ref.Ref)
```

`WithSaveArtifactMaxBytes` is enforced at the backend's **read step**,
not as a post-check — read-and-discard is avoided and overflow fails
fast. Typical reasons to set it:

- **The output may be large** (datasets, model weights, video) — set
  8 MiB / 32 MiB so the backend fails fast instead of pushing GiB
  through the artifact service.
- **Compliance / audit boundaries** — pinning the per-artifact size
  protects against runaway agents producing oversized outputs.

The default cap is `64 MiB`, which covers most documents and small
datasets, so most callers never need this option.

### Caller-owned policy

The framework does not make these choices for you; write each one in
your callback as needed.

**What to do with the bytes you just read**

- *Mirror on failure?* Default: the example bails on
  `args.Error != nil` — workspace state is unreliable in that case.
  Drop the early return when you want post-mortem snapshots.
- *Single file truncated?* Default: backends cap individual reads at
  a few MiB and return `File.Truncated = true` on overflow; `Collect`
  forwards the flag and does **not** error. Add
  `if f.Truncated { return error }` for strict semantics.
- *Too many / too large in aggregate?* Default: `Collect` caps neither
  count nor total bytes. Apply your own check on `len(files)` /
  summed `SizeBytes` and refuse when over budget.
- *Non-zero exit from `RunProgram`?* Default: surfaced via
  `RunResult.ExitCode` / `TimedOut`, **not** via error. Treat non-zero
  exits as failures yourself with
  `if res.ExitCode != 0 { return error }`.

**Writing into your own store**

- *Need all-or-nothing?* `Collect` followed by a `Save` loop is **not
  transactional**. Stage to a temp prefix and rename, or use a
  transactional store.

**What happens when the callback returns an error**

- Returning a non-`nil` error from `AfterAgent` / `AfterTool` **aborts
  the current invocation**. Log-and-swallow if flush should be
  best-effort.

End-to-end example: [examples/workspace_io](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/workspace_io).

## Restricting `workspace_exec` commands

`workspace_exec` runs whatever shell command the model sends. In sandboxes
with network egress this becomes a prompt-injection / SSRF surface: a
user prompt can trick the model into running `curl <internal-url>` and
the sandbox dutifully obliges.

To shrink that surface you can attach an allow- and/or deny-list of
executable names. When at least one list is non-empty, the command is
parsed before execution and rejected if it does not pass.

### Configure

At the agent level:

```go
agent := llmagent.New(
    "my-agent",
    llmagent.WithCodeExecutor(executor),
    llmagent.WithWorkspaceExecAllowedCommands("ls", "cat", "rg", "git"),
    llmagent.WithWorkspaceExecDeniedCommands("curl", "wget", "nc"),
)
```

Or directly on `ExecTool` if you build it yourself (the first argument
is a `codeexecutor.CodeExecutor` such as `localexec.New()`):

```go
tool := workspaceexec.NewExecTool(
    executor,
    workspaceexec.WithAllowedCommands("ls", "cat", "rg", "git"),
    workspaceexec.WithDeniedCommands("curl", "wget", "nc"),
)
```

Or via environment, for deployment-time configuration (comma- or
whitespace-separated):

- `TRPC_AGENT_WORKSPACE_EXEC_ALLOWED_COMMANDS`
- `TRPC_AGENT_WORKSPACE_EXEC_DENIED_COMMANDS`

Explicit options take precedence over the environment. Leaving both
unset disables the policy and preserves the historical behaviour.

### What still works

Pipelines made of allowed commands joined by the safe sequencing
operators are accepted:

```text
ls | rg foo
git status && git diff
test -f x.txt || echo missing
mkdir -p out; cp a.txt out/
```

Single-quoted, double-quoted and `\X`-escaped literals are accepted as
arguments. `{}` is allowed as a literal so patterns like
`find -exec {} \;` parse fine. Note that `xargs` itself is in the
unconditional built-in deny set below — `xargs -I{}` will be rejected
regardless of what is in the allow list.

### What is rejected

Whenever a policy is active the command is **structurally rejected**
before any name lookup if it contains any of:

- command, parameter, arithmetic or process substitution
  (`$(…)`, `` `…` ``, `$VAR`, `${X}`, `$((…))`, `<(…)`, `>(…)`)
- redirections of any kind (`>`, `>>`, `<`, `2>&1`, here-docs)
- subshells, blocks, control flow, function definitions
  (`(…)`, `{…}`, `if/for/while/case`, `f() { … }`)
- backgrounding, `|&`, leading `VAR=… cmd`, glob characters, `!`, `#`,
  bare or escaped newlines

So a deny on `curl` cannot be sidestepped via `$(c\url)`,
`echo \`curl http://x\``, `curl > /tmp/x`, `(curl http://x)`,
`HOME=/tmp curl http://x`, etc.

On top of the parser there is an **unconditional built-in deny set**
of shell wrappers, re-executing builtins, process-launching
wrappers, and stateful shell builtins. They are blocked whenever
any policy is active because they can launch arbitrary code with
an innocent `argv[0]` (e.g. `time curl http://x` would otherwise
pass a deny on `curl`), register code to run later (e.g.
`trap 'curl http://x' EXIT`) or mutate later-segment resolution
(e.g. `export PATH=./bin && allowed_cmd`):

- shell wrappers: `sh`, `bash`, `zsh`, `ash`, `dash`, `ksh`,
  `mksh`, `fish`, `pwsh`, `powershell`, `cmd`, `busybox`, `toybox`
- re-executing builtins: `eval`, `exec`, `command`, `source`, `.`,
  `builtin`
- process-launching wrappers: `xargs`, `env`, `nohup`, `timeout`,
  `sudo`, `su`, `doas`, `setsid`, `unshare`, `chroot`, `runuser`,
  `time`, `nice`, `ionice`, `taskset`, `stdbuf`, `strace`, `ltrace`,
  `script`, `flock`
- stateful shell builtins: `trap`, `alias`, `unalias`, `enable`,
  `export`, `unset`, `readonly`, `local`, `declare`, `typeset`,
  `set`, `shopt`, `hash`, `cd`, `pushd`, `popd`
- variable-assigning builtins: `printf`, `read`, `getopts`, `let`,
  `mapfile`, `readarray` — on a single-process shell these can
  rewrite `PATH` or other resolution state before a later allowed
  segment runs (e.g. `printf -v PATH ./work/bin; git` would
  otherwise resolve `git` to `./work/bin/git` even when both
  `printf` and `git` pass an `argv[0]`-only check). The bash
  extensions matter because `/bin/sh` is bash on macOS and on
  many container images.

`workspace_exec` exposes a `cwd` parameter for the legitimate cwd-
switching use case, so the model never needs to call `cd` itself.

This deny set is **not overridable** by `WithWorkspaceExecAllowedCommands`
— allow-list entries for these names are ignored. If you legitimately
need one of them (rare, but possible), wrap the desired use in an
auditable script under the workspace and put the script in
`allowed_commands` instead. The auditable wrapper is also better
practice: reviewers can see exactly what is being exposed.

### Matching

Matching is intentionally **asymmetric** so workspace-controlled
binaries cannot smuggle past the allowlist:

- **Allow** matches strictly. An entry `echo` admits bare `echo`
  but rejects `./echo`, `work/bin/echo` and `/usr/bin/echo`. If you
  want to permit a specific absolute or relative path, list that
  exact path (e.g. `WithWorkspaceExecAllowedCommands("/usr/bin/echo")`).
- **Deny** matches permissively. An entry `curl` rejects `curl`,
  `/usr/bin/curl` and `./curl` alike, so an attacker cannot slip a
  full path past the denylist.

Case handling tracks the underlying file system's resolution
rules so the allowlist cannot be silently widened on a case-
sensitive FS:

- **Deny and the built-in deny set** are case-folded on every OS.
  A deny of `curl` rejects `curl`, `Curl` and `CURL` alike, and
  the implicit deny on `sh` blocks `SH -c`, `Sh` and `Bash` too.
  This matters on macOS's default case-insensitive APFS (where
  `CURL` resolves to `/usr/bin/curl`) and on Windows's case-
  insensitive resolver; on Linux the fold is defence-in-depth
  against workspace-controlled upper-case binaries.
- **Allow** is split by entry shape:
    - **Pathful entries** (anything containing `/` or `\`, e.g.
      `./safe`, `work/bin/echo`, `/usr/bin/echo`) are always
      matched **exact-case** on every OS. We cannot reliably tell
      whether the actual workspace volume is case-sensitive
      (macOS APFS supports opt-in case-sensitive volumes, and
      container layers can mix file systems), so folding would
      silently widen `./safe` to admit a workspace-controlled
      `./SAFE` on case-sensitive volumes. Operators who need both
      list both.
    - **Bare-name entries** (e.g. `echo`) resolve through `PATH`,
      which the policy mode resets to a known-good default. They
      follow the OS convention: case-folded on Windows and macOS,
      exact-case on Linux. So `WithWorkspaceExecAllowedCommands("echo")`
      admits `ECHO` on macOS / Windows but only `echo` on Linux.

On Windows the basename match additionally strips common
executable suffixes (`.exe`, `.cmd`, `.bat`, `.com`, `.ps1`) so
`cmd` rejects `cmd.exe`, `curl` rejects `CURL.EXE`, and `echo`
admits `ECHO.EXE`. The configured deny entries are folded through
the same rules, so `WithWorkspaceExecDeniedCommands("CURL")` also
blocks bare `curl` and `curl.exe`.

### Precedence

When the same name appears in both lists, **deny wins**:

```text
explicit Deny  >  implicit deny  >  explicit Allow  >  implicit allow
```

So `WithWorkspaceExecAllowedCommands("git") + WithWorkspaceExecDeniedCommands("git")`
rejects `git`. If you also list `sh` in the allow list, it stays
denied by the implicit-deny set; you cannot weaken the implicit
deny by re-listing its members in `Allow`.

### Spawn hardening

When a policy is active, the spawn itself is also hardened to stop
shell-startup tricks from re-arming a rejected command:

- the shell invocation is `sh -c` instead of `sh -lc`, so
  `/etc/profile` and `$HOME/.profile` are not sourced first;
- per-call env is scrubbed: `HOME`, `ENV`, `BASH_ENV`,
  `PROMPT_COMMAND`, `PS4`, `SHELL`, `SHELLOPTS`, `BASHOPTS`, `PATH`,
  `IFS`, `CDPATH`, `GLOBIGNORE`, `LD_PRELOAD`, `LD_LIBRARY_PATH`,
  `LD_AUDIT`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`,
  `DYLD_FORCE_FLAT_NAMESPACE`, and any `BASH_FUNC_*` entry (Shellshock)
  are dropped. `LANG` and similar benign variables pass through
  untouched.
- `PATH` in particular is dropped because the policy only matches by
  command name; a caller-controlled `PATH=./bin:$PATH` plus a
  workspace-side `./bin/echo` would otherwise pass the policy and
  execute attacker code. Allowed commands resolve against the shell's
  default `PATH` instead.
- on Windows the scrub folds env names to upper-case before
  comparison, because Windows treats env keys case-insensitively at
  runtime. A caller-supplied `Path=./bin`, `Home=.`, `Bash_Env=…` or
  `bash_func_x%%=…` is therefore stripped just like its canonical
  form would be.
- env entries whose **key** is not a POSIX name
  (`/^[A-Za-z_][A-Za-z0-9_]*$/`) are dropped outright. This catches
  the obvious cases (`PATH=.` as a key, embedded `\n` / `\r` / `\0`)
  and also closes the shell-metacharacter bypass on runtimes that
  build env injection through a shell string (`env KEY=value <cmd>`):
  a name like `"X; curl http://x #"` placed into that template
  would otherwise execute `curl` before the checked command.
- `RunEnvProvider` entries are subject to the same scrub when
  policy mode is active. `codeexecutor.mergeProviderEnv` honors
  `spec.CleanEnv` and runs provider-supplied keys through the same
  `internal/envscrub` blocklist, so a `NewEnvInjectingCodeExecutor`
  provider returning `PATH` / `BASH_ENV` / `LD_PRELOAD` cannot
  reintroduce them after `workspace_exec` has removed them.

Without a policy configured none of this kicks in: `sh -lc` and the
caller-supplied env (including `PATH`) are preserved as before.

!!! warning "Policy mode requires a CleanEnv-capable runtime"
    The `sh -c` switch and the per-call env scrubbing above are
    only safe when the underlying runtime actually honors
    `RunProgramSpec.CleanEnv`. To avoid silently degrading the
    contract on a backend that ignores `CleanEnv`, `workspace_exec`
    **fails closed**: with `WithWorkspaceExecAllowedCommands` /
    `WithWorkspaceExecDeniedCommands` configured, the tool refuses
    to start a call when the runtime's
    `codeexecutor.Capabilities.SupportsCleanEnv` is `false`, and
    returns an error pointing the operator at a supported runtime.

    Today only `codeexecutor/local` advertises
    `SupportsCleanEnv: true`. `codeexecutor/container` and
    `codeexecutor/e2b` keep the zero-valued capabilities, so policy
    mode on those backends is currently refused at the gate.
    Implementing `CleanEnv` for them (so the policy gate opens
    automatically once they declare the capability) is tracked in
    [#1845](https://github.com/trpc-group/trpc-agent-go/issues/1845).
    Until then, run policy mode on the local backend, or drop the
    policy lists and rely on the sandbox layer for env / network
    isolation.

### Scope

Enforcement is at the **executable-name** level. If an allowed command
itself shells out based on its arguments — for example
`find . -exec curl …`, `awk 'BEGIN{system("curl …")}'`,
`git -c protocol.ext.allow=…` — the inner command is the allowed
command's own subprocess and is not re-checked. Per-command argument
validators are a planned follow-up; until they land, treat the
network-egress policy of the sandbox itself as the primary defence and
the allow/deny list as defence-in-depth.

!!! note "Allow-list is strictly stronger than deny-only"
    `WithWorkspaceExecAllowedCommands(...)` produces a closed world:
    everything outside the list (and the implicit deny set) is
    rejected.
    `WithWorkspaceExecDeniedCommands(...)` only blocks the named
    tools. In a deny-only configuration, an attacker who finds *any*
    binary outside the deny set — including a workspace-side script
    they themselves staged via an allowed editor — can still execute
    arbitrary code. Where possible, prefer an explicit allow list and
    add deny entries only for extra defence-in-depth.

    Known tool categories that are **not** in the built-in deny set
    today but can launch arbitrary code from their own arguments:
    debuggers / instrumentation (`gdb`, `lldb`, `valgrind`,
    `perf`), language interpreters (`python`, `perl`, `ruby`,
    `node`, `awk`, `lua`), package managers / `git` (`make`,
    `npm`, `pip`, `cargo`, `go run`, `git -c …`,
    `git --exec-path=…`), `find -exec`, and editor escape hatches
    (`vim -c :!`, `less !`). If you choose deny-only mode, add the
    ones you do not need to `denied_commands`, or switch to allow
    mode.

For lower-level (non-shell) skill execution, the equivalent knobs on
`skill_run` are `WithSkillRunAllowedCommands` /
`WithSkillRunDeniedCommands`; see [skill](skill.md).

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
