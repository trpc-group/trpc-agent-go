# E2B Native ProgramRunner

Status: Draft design for the first implementation PR

Tracking issue: [#2521](https://github.com/trpc-group/trpc-agent-go/issues/2521)

Maintainer direction: [issue comment](https://github.com/trpc-group/trpc-agent-go/issues/2521#issuecomment-5422781165)

trpc-agent-go baseline: [`71e672fe2772ba53cb4b06f5a149f95dcec085a3`](https://github.com/trpc-group/trpc-agent-go/commit/71e672fe2772ba53cb4b06f5a149f95dcec085a3)

## Summary

The first PR moves the E2B implementation of
`codeexecutor.ProgramRunner.RunProgram` from the Code Interpreter
`/execute` endpoint to envd's native `Process` service.

The public `codeexecutor.Engine`, `ProgramRunner`, `RunProgramSpec`, and
`RunResult` interfaces remain unchanged. `CodeExecutor.ExecuteCode` continues
to use Code Interpreter `/execute`. Workspace filesystem and lifecycle
operations also continue to use their existing implementation until a later
PR migrates `WorkspaceFS`.

The intended split is:

```text
E2B CodeExecutor
├── shared sandbox lifecycle and connection metadata
│
├── Code Interpreter path
│   └── POST /execute
│       └── CodeExecutor.ExecuteCode
│
└── native envd path
    └── Process.Start / SendInput / CloseStdin / SendSignal
        └── Engine().Runner().RunProgram
```

PTY support is not part of this PR. A PTY represents interactive terminal
semantics and belongs behind the optional `InteractiveProgramRunner` seam,
not behind one-shot `ProgramRunner.RunProgram`.

## Motivation

The current E2B workspace runtime implements ordinary process execution by
submitting generated Bash to the Jupyter Bash kernel:

```text
ProgramRunner.RunProgram
    └── workspaceRuntime.runBashStreaming
        └── Sandbox.RunCode(LanguageBash)
            └── POST /execute
```

It then reconstructs `RunResult` from a private text-framing protocol:

```text
__E2B_STDOUT_BEGIN__
__E2B_STDOUT_END__
__E2B_STDERR_BEGIN__
__E2B_STDERR_END__
__E2B_EXITCODE__=
```

This creates four classes of problems:

1. User output can collide with the sentinel strings and be truncated.
2. Exit status, timeout, and process failure must be inferred from text and
   Code Interpreter errors.
3. `Cmd`, `Args`, `Cwd`, `Stdin`, and environment semantics are translated
   through shell quoting and generated Bash.
4. Ordinary process execution depends on Jupyter kernel availability even
   though it requires only operating-system process semantics.

envd already exposes a structured process protocol with separate stdout and
stderr events, a final exit event, stdin operations, signals, and optional
PTY support. The first PR uses the non-PTY form of that protocol.

## Semantic Boundary

The split is based on required semantics, not whether the payload happens to
contain source code.

| Caller intent | Execution path |
| --- | --- |
| Explicit code block requiring language kernels, stateful contexts, or rich results | `CodeExecutor.ExecuteCode` -> `/execute` |
| Ordinary process such as `git`, `go test`, `python script.py`, or `bash script.sh` | `ProgramRunner.RunProgram` -> envd `Process` |
| Interactive shell, REPL, or TUI requiring terminal behavior | Future `InteractiveProgramRunner` -> envd `Process` with PTY |

Therefore a Skill script is an ordinary process even though the file contains
code. Conversely, an explicitly submitted Bash code block remains a Code
Interpreter workload.

## Goals

- Route E2B `ProgramRunner.RunProgram` through envd's native process protocol.
- Preserve the existing public codeexecutor interfaces.
- Preserve `Cmd`, `Args`, `Cwd`, `Stdin`, environment, timeout, and result
  contracts.
- Preserve separate stdout and stderr without private framing.
- Represent non-zero program exits in `RunResult`, not as framework errors.
- Distinguish configured timeout from caller cancellation.
- Ensure timeout and caller cancellation terminate the remote process.
- Preserve `CleanEnv` and `SupportsCleanEnv` behavior.
- Handle envd version and capability requirements explicitly for both newly
  created and connected sandboxes.
- Add protocol-level mock tests and a small opt-in real E2B integration test.

## Non-Goals

- Changing `CodeExecutor.ExecuteCode` or removing `/execute`.
- Migrating `WorkspaceFS` or `WorkspaceManager` operations.
- Implementing `InteractiveProgramRunner`, PTY, background sessions, or TTY
  resize.
- Redesigning `codeexecutor.Engine`, `ProgramRunner`, `RunProgramSpec`, or
  `RunResult`.
- Implementing `RunProgramSpec.Limits` for E2B.
- Changing workspace persistence, metadata, path-containment, or cleanup
  semantics.
- Adding a silent fallback to the legacy `/execute` process path.
- Treating a logical workspace path as a tenant security boundary.

## Source Baselines

The protocol design is based on the following pinned sources:

- trpc-agent-go E2B `RunProgram` and framing implementation at
  [`71e672fe`](https://github.com/trpc-group/trpc-agent-go/blob/71e672fe2772ba53cb4b06f5a149f95dcec085a3/codeexecutor/e2b/workspace_runtime.go#L590-L875).
- E2B envd process protocol at
  [`e2b-dev/infra@01da054a`](https://github.com/e2b-dev/infra/blob/01da054ac9ed73de4b2d803bfa45e02d955ab4c9/packages/envd/spec/process/process.proto).
- E2B SDK envd capability versions at
  [`e2b-dev/E2B@e873ee94`](https://github.com/e2b-dev/E2B/blob/e873ee94b6b59222fdcf9762b7ea2247a531d899/packages/js-sdk/src/envd/versions.ts).
- E2B non-PTY command lifecycle at
  [`e2b-dev/E2B@82add5b4`](https://github.com/e2b-dev/E2B/blob/82add5b4ea25fdb700b4bd5e18ec7099f6b56ebf/packages/js-sdk/src/sandbox/commands/index.ts).

The E2B process protocol exposes:

```protobuf
service Process {
    rpc List(ListRequest) returns (ListResponse);
    rpc Connect(ConnectRequest) returns (stream ConnectResponse);
    rpc Start(StartRequest) returns (stream StartResponse);
    rpc SendInput(SendInputRequest) returns (SendInputResponse);
    rpc SendSignal(SendSignalRequest) returns (SendSignalResponse);
    rpc CloseStdin(CloseStdinRequest) returns (CloseStdinResponse);
}

message ProcessConfig {
    string cmd = 1;
    repeated string args = 2;
    map<string, string> envs = 3;
    optional string cwd = 4;
}

message StartRequest {
    ProcessConfig process = 1;
    optional PTY pty = 2;
    optional string tag = 3;
    optional bool stdin = 4;
}
```

For this PR, `StartRequest.pty` is always absent.

## Module and Seam Design

### Public seam

The public seam remains the existing interface:

```go
type ProgramRunner interface {
    RunProgram(
        ctx context.Context,
        ws Workspace,
        spec RunProgramSpec,
    ) (RunResult, error)
}
```

Callers and contract tests continue to exercise behavior through this seam.
No E2B protocol type becomes public.

### Internal envd process module

Add a private, provider-specific module under:

```text
codeexecutor/e2b/internal/envdprocess/
```

Suggested layout:

```text
internal/envdprocess/
├── client.go
├── client_test.go
├── spec/
│   ├── README.md
│   ├── process.proto
│   ├── process.pb.go
│   └── processconnect/
│       └── process.connect.go
```

`process.proto` and its generated protobuf and Connect files are pinned to the
same E2B infra commit. The generated Connect client's process import is
rewritten to the local internal package, and `spec/README.md` records the
provenance and upgrade procedure. The handwritten module initially exposes a
concrete client rather than a provider interface with only one production
adapter:

```go
type Request struct {
    Cmd     string
    Args    []string
    Envs    map[string]string
    Cwd     string
    Stdin   string
    Timeout time.Duration
}

type Result struct {
    Stdout   string
    Stderr   string
    ExitCode int
    TimedOut bool
}

type Client struct { /* private transport and lifecycle state */ }

func NewClient(baseURL string, httpClient *http.Client, headers http.Header) (
    *Client, error,
)

func (c *Client) Run(context.Context, Request) (Result, error)
```

Protocol tests exercise `Client.Run` against an in-process Connect handler.
When `workspaceRuntime` is wired in the next commit, define any test seam as a
small unexported consumer-owned interface in the `e2b` package; the real
client and workspace-runtime fake then become its two adapters. PID ownership,
Connect RPC details, stream decoding, stdin lifecycle, signals, and cleanup
stay inside `envdprocess.Client`.

Use generated Connect clients rather than hand-implementing Connect stream
framing. Add `connectrpc.com/connect v1.18.1`, which supports the root module's
Go 1.21 baseline and matches its protobuf version. Every new Go file,
including checked-in generated files, must follow the repository
license-header requirement.

## Sandbox Connection Metadata

The existing internal `codeinterpreter.Sandbox` already owns the shared E2B
sandbox lifecycle and the data-plane routing inputs:

- sandbox ID and client ID;
- sandbox domain;
- envd access token;
- traffic access token;
- injected HTTP client;
- request timeout and additional headers.

Extend it to retain:

```go
const EnvdPort = 49983

type Sandbox struct {
    // Existing fields omitted.
    envdPort    int
    envdVersion string
}
```

Both management responses must decode `envdVersion`; `envdPort` should default
to `49983` when absent. Create and connect must construct the same native
process capability.

The envd client must use the sandbox data-plane host, not the management API
host, and send the applicable authentication headers:

- `X-Access-Token`;
- `E2B-Traffic-Access-Token`;
- caller-supplied sandbox headers where applicable.

Connect RPC owns its own content type. Split the existing JSON/Jupyter header
helper so process RPC does not inherit `Content-Type: application/json`.

## Envd Version and Capability Policy

The first PR requires the process protocol plus finite stdin delivery. The
official E2B SDK records these relevant capability versions:

```text
ENVD_COMMANDS_STDIN = 0.3.0
ENVD_ENVD_CLOSE     = 0.5.2
```

`RunProgramSpec.Stdin` is a complete finite string. A correct implementation
must send those bytes and then deliver EOF. Because `CloseStdin` is available
from envd `0.5.2`, the proposed minimum version for the native ProgramRunner
is:

```text
envd >= 0.5.2
```

Version handling must be explicit:

```text
Create new sandbox
    └── unsupported envd
        ├── kill the newly owned sandbox to prevent leakage
        └── return a diagnostic version/capability error

Connect existing sandbox
    └── unsupported envd
        └── return a diagnostic error without killing caller-owned state
```

There is no implicit fallback to `/execute`.

Failing construction is the simplest fail-fast behavior because the E2B
executor always advertises an `Engine`. Before implementation, confirm with
the maintainer whether pure `ExecuteCode` users must remain able to construct
an executor backed by an older envd. If required, preserve that use case by
recording an unavailable native-process capability and returning the explicit
error only from `RunProgram`; do not silently select the legacy path.

An explicit legacy transport option, if required, is a separate compatibility
decision and should not be introduced accidentally in this PR.

## RunProgram Request Mapping

`workspaceRuntime.RunProgram` continues to own provider-independent E2B
workspace mapping and telemetry:

1. Validate that the sandbox and native process runner are initialized.
2. Resolve the default timeout.
3. Resolve `Cwd` relative to `ws.Path` using the existing behavior.
4. Construct workspace base environment variables.
5. Create the per-run directory.
6. Map the invocation to an envd process request.
7. Measure duration and map the result to `codeexecutor.RunResult`.
8. Record exit code and timeout trace attributes.

The normal request mapping is:

```text
ProcessConfig.cmd  = spec.Cmd
ProcessConfig.args = spec.Args
ProcessConfig.cwd  = resolved workspace cwd
ProcessConfig.envs = workspace base vars merged with spec.Env
StartRequest.stdin = spec.Stdin != ""
StartRequest.pty   = absent
```

Do not use the high-level E2B SDK behavior that converts a command string to
`/bin/bash -l -c`. `RunProgramSpec` already carries structured `Cmd` and
`Args`; preserving them avoids shell quoting and argv drift.

### Workspace environment

Preserve the current base variables:

- workspace root;
- skills directory;
- work directory;
- output directory;
- per-run directory.

`spec.Env` continues to override a base variable with the same key where the
existing merge policy permits it.

### Per-run directory

`CreateWorkspace` already creates the `runs/` and `out/` roots, but
`RunProgram` creates one timestamped directory per invocation. Until
`WorkspaceFS` is migrated, create it through the native process service with a
structured command such as:

```text
/bin/mkdir -p -- <runDir>
```

This remains on the native envd process path. Failure to prepare the run
directory is a framework error and the user command must not start.

## CleanEnv

Passing `ProcessConfig.envs` alone does not implement `CleanEnv`; envd still
starts the process with sandbox defaults plus overrides.

For `CleanEnv == false`:

```text
cmd  = spec.Cmd
args = spec.Args
envs = workspace base variables + spec.Env
```

For `CleanEnv == true`, execute the target structurally through `env -i`:

```text
cmd = /usr/bin/env
args =
    - -i
    - KEY=value               # sorted for deterministic behavior
    - ...
    - PATH=<minimal path>      # only if spec.Env did not supply PATH
    - spec.Cmd
    - spec.Args...
```

The target program receives only workspace base variables, `spec.Env`, and the
minimal PATH fallback. The wrapper is expressed as `cmd` plus `args`, not a
shell string. Only advertise `SupportsCleanEnv: true` while this contract is
verified by tests.

## Process Lifecycle State Machine

The native client owns the process lifecycle:

```text
Start request
    │
    ├── StartEvent(pid)
    │      ├── optional SendInput(pid, stdin)
    │      └── optional CloseStdin(pid)
    │
    ├── DataEvent(stdout) ──> stdout buffer
    ├── DataEvent(stderr) ──> stderr buffer
    ├── KeepAlive           ──> ignored as payload
    │
    └── EndEvent
           ├── exit_code
           ├── exited
           ├── status
           └── optional error
```

Required invariants:

- At most one remote process is owned by a `Run` call.
- Once a PID is observed, every timeout or caller-cancellation path either
  observes process exit or sends an explicit terminating signal.
- Cleanup uses a fresh bounded context, not an already canceled caller
  context.
- `NotFound` during kill is success because the process already exited.
- Stream close by itself is never treated as process termination.
- The function does not return a successful result without a terminal
  `EndEvent`.
- Stdout and stderr bytes are preserved exactly and never parsed for sentinel
  strings.

## Timeout and Caller Cancellation

Configured timeout and caller cancellation are distinct externally observable
states.

### Configured timeout

When `spec.Timeout` expires:

1. terminate the remote PID with a bounded cleanup context;
2. retain stdout and stderr already received;
3. return `TimedOut: true`;
4. return a nil framework error, matching the existing `RunResult` contract.

### Caller cancellation or caller deadline

When the parent context is canceled or reaches its own deadline:

1. terminate the remote PID with a bounded cleanup context;
2. retain any confirmed partial output where practical;
3. return `TimedOut: false`;
4. return the caller context error.

### Cancellation before StartEvent

There is a race in which envd may have spawned the process but the client has
not yet received its PID. Canceling the RPC stream at that point can orphan the
remote process because envd intentionally decouples process lifetime from the
request stream.

Use a unique `StartRequest.tag` and a bounded startup/cleanup sequence:

1. issue `Start` with a unique run tag;
2. do not abandon the start stream solely because the caller context was
   canceled before the first event;
3. wait for the PID under a short cleanup deadline and kill it;
4. if the stream fails before yielding a PID, use `List` plus the unique tag to
   locate and terminate a registered process;
5. return only after termination is confirmed or cleanup itself reports a
   diagnostic failure.

Protocol tests must force this ordering; a happy-path cancellation test is not
sufficient.

## Result and Error Mapping

| Condition | `RunResult` | Go error |
| --- | --- | --- |
| Exit code zero | stdout, stderr, exit code zero | `nil` |
| Exit code non-zero | stdout, stderr, exact exit code | `nil` |
| Configured timeout | partial output, `TimedOut: true` | `nil` |
| Caller cancellation/deadline | confirmed partial output where available, `TimedOut: false` | caller context error |
| Authentication, transport, malformed stream, or process-start failure | partial confirmed output where meaningful | wrapped framework error |
| Stream ends without terminal event | no successful result | protocol/framework error |

Do not copy the E2B JavaScript SDK behavior that throws `CommandExitError` for
all non-zero exits. The trpc-agent-go contract represents an ordinary program
failure in `RunResult`.

Do not infer timeout by substring matching an error message. Timeout ownership
is known from the configured timer and lifecycle state.

## Existing Helper Impact

`workspaceRuntime.ExecuteInline` currently writes code blocks into workspace
files and invokes `RunProgram`. It will therefore use the native process path
after this change. The top-level E2B `CodeExecutor.ExecuteCode` remains direct
to `/execute` and is not affected.

Keep `runBashStreaming` because workspace creation, cleanup, staging,
collection, and metadata still use it. Remove only the helpers exclusively
owned by the old `RunProgram` framing path:

- `buildRunWrapper`;
- `parseFramedOutput`;
- `extractBetween`;
- stdout/stderr/exit sentinel constants;
- `isTimeoutErr`, if no remaining caller exists.

## File-Level Change Plan

### New files

```text
codeexecutor/e2b/internal/envdprocess/client.go
codeexecutor/e2b/internal/envdprocess/client_test.go
codeexecutor/e2b/internal/envdprocess/spec/README.md
codeexecutor/e2b/internal/envdprocess/spec/process.proto
codeexecutor/e2b/internal/envdprocess/spec/process.pb.go
codeexecutor/e2b/internal/envdprocess/processconnect/process.connect.go
codeexecutor/e2b/process_integration_test.go
```

### Modified files

`go.mod`, `go.sum`

- Add the minimal compatible Connect Go dependency.

`codeexecutor/e2b/internal/codeinterpreter/constants.go`

- Add the envd port constant if it remains owned by the shared sandbox handle.

`codeexecutor/e2b/internal/codeinterpreter/sandbox.go`

- Decode and retain envd version for create and connect.
- Default the envd port.
- Expose a private construction path for the envd process client.
- Split sandbox authentication headers from Jupyter JSON headers.
- Enforce or record the explicit envd capability decision.

`codeexecutor/e2b/e2b.go`

- Construct the native process runner after sandbox creation or connection.
- Clean up a newly owned sandbox if fail-fast capability validation fails.
- Do not alter `ExecuteCode`.

`codeexecutor/e2b/workspace_runtime.go`

- Inject or reference the internal native process runner.
- Rewrite only `RunProgram`.
- Preserve telemetry and workspace environment mapping.
- Remove RunProgram-only framing helpers.
- Keep the legacy Bash primitive for filesystem and workspace operations.

`codeexecutor/e2b/workspace_runtime_test.go`

- Replace assertions about generated Bash framing with request/result contract
  tests at the internal runner seam.
- Keep unrelated filesystem tests unchanged.

`codeexecutor/e2b/e2b_test.go` and sandbox tests

- Add create/connect envd version fixtures and capability behavior.

## Test Plan

### Envd protocol client tests

Use an in-process Connect mock server to verify:

- exact `cmd`, `args`, `cwd`, and env mapping;
- `pty` is absent;
- authentication and traffic headers;
- StartEvent PID handling;
- multiple stdout and stderr chunks;
- exact EndEvent exit code;
- stdin ordering: start, send bytes, close stdin;
- non-zero exit is a result, not a Go error;
- configured timeout sends `SIGKILL` and sets `TimedOut`;
- caller cancellation sends `SIGKILL` and returns the context error;
- cancellation before StartEvent does not leak the tagged process;
- kill racing with natural exit treats `NotFound` as success;
- stream failure before EndEvent returns a protocol or transport error;
- output containing old sentinel strings is returned unchanged.

### Workspace runtime contract tests

Use a fake implementation of the internal runner seam to verify:

- default timeout;
- workspace-relative cwd mapping;
- exact command and argument preservation;
- stdin preservation;
- workspace base environment variables;
- `CleanEnv` uses structured `/usr/bin/env -i` arguments;
- caller PATH suppresses the minimal PATH fallback;
- environment argument ordering is deterministic;
- non-zero exit mapping;
- timeout and caller cancellation remain distinct;
- trace result fields remain populated.

The sentinel regression must include:

```bash
printf 'before\n__E2B_STDOUT_END__\nafter\n'
```

Expected stdout:

```text
before
__E2B_STDOUT_END__
after
```

### Version tests

Cover both lifecycle paths:

- supported create;
- unsupported create and owned-sandbox cleanup;
- supported connect;
- unsupported connect without killing caller-owned state;
- missing or malformed envd version;
- no silent legacy fallback.

### Real E2B integration test

Follow the repository's integration-test convention:

```go
//go:build integration
```

Skip when `E2B_API_KEY` is unset. Keep the scenario small:

1. create one real sandbox;
2. run one command that consumes stdin, writes stdout and stderr, emits an old
   sentinel, and exits non-zero;
3. verify exact output and exit status;
4. run one timeout/cancellation case;
5. verify the remote process is no longer listed;
6. clean up the sandbox.

## Validation

Targeted validation while iterating:

```bash
go test ./codeexecutor/e2b/...
go test ./tool/workspaceexec ./tool/skill
```

Real integration validation:

```bash
go test -tags=integration ./codeexecutor/e2b -run TestIntegration
```

Broader repository validation before delivery, following `AGENTS.md`:

```bash
go build ./...
go test ./...
gofmt -r 'interface{} -> any' -l .
goimports -l .
git diff --check
```

Run the mandatory second-pass public API and framework design review even
though no public symbol should change. Confirm that externally observable
error, timeout, cancellation, and cleanup behavior did not drift.

## Rejected Alternatives

### Keep the sentinel protocol and only escape collisions

This fixes one concrete bug but retains text framing, shell quoting, inferred
exit state, and Jupyter dependency. It does not establish the intended seam.

### Use PTY for RunProgram

PTY merges stdout and stderr, introduces terminal echo and ANSI behavior,
changes buffering and line endings, and requires resize/input semantics.
Those behaviors conflict with deterministic `RunResult` contracts. PTY belongs
to future interactive execution.

### Copy E2B SDK `commands.run` shell behavior

The high-level SDK accepts one command string and executes Bash. The
trpc-agent-go interface already has structured `Cmd` and `Args`; converting
them back into a shell command recreates quoting and argv problems.

### Cancel only the Connect stream

envd deliberately keeps process lifetime independent from request-stream
lifetime. Stream cancellation without an explicit signal can orphan the
remote process.

### Silently fall back to `/execute`

The same call would have version-dependent transport and error semantics,
making production behavior difficult to diagnose. Any compatibility path must
be explicit.

### Migrate filesystem operations in the same PR

This mixes process lifecycle risk with file mode, staging, symlink/copy, glob,
containment, truncation, tar extraction, and metadata-commit contracts. The
maintainer explicitly recommended `ProgramRunner` first.

## Implementation Order

Recommended commits:

1. `codeexecutor/e2b: add envd process protocol client`
   - pin protocol source;
   - add generated Connect client;
   - implement stream, stdin, signal, timeout, and cancellation lifecycle;
   - add protocol tests.
2. `codeexecutor/e2b: route RunProgram through envd`
   - retain envd metadata on create/connect;
   - wire the internal runner;
   - map `RunProgramSpec` and `RunResult`;
   - remove RunProgram-only framing.
3. `codeexecutor/e2b: add native runner contract tests`
   - replace framing tests;
   - add sentinel, version, cancellation-race, and integration coverage.

## Acceptance Checklist

- [ ] `CodeExecutor.ExecuteCode` still uses `/execute`.
- [ ] `RunProgram` no longer calls `Sandbox.RunCode`.
- [ ] `RunProgram` never sets PTY.
- [ ] `Cmd` and `Args` remain structured.
- [ ] Cwd, stdin, workspace env, and `CleanEnv` contracts are covered.
- [ ] Stdout and stderr remain separate and exact.
- [ ] Old sentinel strings are ordinary user output.
- [ ] Non-zero exit is returned in `RunResult` with nil framework error.
- [ ] Configured timeout sets `TimedOut` and kills the process.
- [ ] Caller cancellation returns the context error and kills the process.
- [ ] Cancellation before StartEvent cannot leak a process.
- [ ] Create and connect both handle envd version explicitly.
- [ ] No silent fallback exists.
- [ ] Workspace filesystem behavior remains unchanged.
- [ ] Protocol mock tests pass.
- [ ] Targeted E2B and caller tests pass.
- [ ] Real E2B integration test is documented and opt-in.
- [ ] Full diff receives the mandatory public API/framework second-pass review.

## Follow-Up Work

After this PR is stable:

1. migrate `WorkspaceFS` operations to envd filesystem calls where native
   operations exist;
2. retain native process calls for filesystem operations not directly exposed
   by envd, such as selected `chmod`, `cp`/`ln`, glob, or tar workflows;
3. separately design `InteractiveProgramRunner` using regular envd processes
   for non-TTY sessions and optional PTY for terminal sessions;
4. remove the remaining workspace dependency on `/execute` only after all
   filesystem contracts are covered.
