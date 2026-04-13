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

- [examples/codeexecution/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/main.go)
- [examples/codeexecution/jupyter/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/jupyter/README.md)

### Default Behavior of `WithCodeExecutor`

`llmagent.WithCodeExecutor(...)` does more than provide a runtime.

By default, it also enables the response-side code execution processor. If the
assistant reply is exactly one runnable fenced code block, the framework will
extract and execute that block automatically.

If you only want workspace support, `workspace_exec`, or other executor-backed
tools, and do not want model output code blocks to auto-run, disable it:

```go
agent := llmagent.New(
    "demo",
    llmagent.WithModel(m),
    llmagent.WithCodeExecutor(local.New()),
    llmagent.WithEnableCodeExecutionResponseProcessor(false),
)
```

Common cases for disabling auto-execution:

- using `workspace_exec` only
- providing a runtime for other tools
- requiring code execution to happen only through explicit tool calls

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
  - [examples/codeexecution/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/main.go)
  - [examples/codeexecution/jupyter/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/jupyter/README.md)
- Related docs:
  - [Artifact](artifact.md)
