# Identity Plugin Example

This example shows how to use `plugin/identity` to propagate a trusted user
identity through every tool call, and how consuming tools read that identity
from the per-call context.

The demo is fully self-contained: it does **not** call any real model backend,
and the two "tools" in the example only print what they would send (fake
`http.Request` headers and fake `exec.Cmd` env) instead of actually dialling
the network or forking a process.

## What this example demonstrates

- Wrap a custom credential-resolution function behind an
  `identity.ProviderFunc`.
- Register the plugin with `plugin.NewManager(identity.NewPlugin(provider))`.
- Drive the plugin lifecycle (`BeforeAgent` → `BeforeTool`) the way `Runner`
  does internally, so you can see the per-tool context grow an `Identity`
  attachment.
- Compare the view of two consumer-side tools — one HTTP-bound, one
  command-bound — with and without the plugin enabled:
  - The HTTP tool reads `identity.HeadersFromContext` and sets request
    headers, mirroring what a real
    `mcp.WithMCPOptions(tmcp.WithHTTPBeforeRequest(...))` hook would do for
    an MCP toolset.
  - The command tool reads `identity.EnvVarsFromContext` and merges the
    values into its `exec.Cmd.Env`, mirroring what
    `codeexecutor.NewEnvInjectingCodeExecutor(exec, identity.EnvVarsFromContext)`
    does transparently for `skill_run` / `workspace_exec`.

## Prerequisites

- Go 1.21 or later

No environment variables are required.

## Usage

```bash
cd examples/plugin/identity
go run .
```

You will see two blocks. The first block runs the tools without the plugin,
so their contexts are empty:

```text
[1] Tools invoked without the identity plugin.
  http_tool    -> ["GET https://api.example.com/api/v1/me"]
  command_tool -> ["exec \"printenv\"","PATH=/usr/local/bin:/usr/bin:/bin"]
```

The second block runs the same tools after `plugin.Manager` has invoked
`BeforeAgent` and `BeforeTool`, so each tool sees the fabricated identity:

```text
[2] Tools invoked with the identity plugin enabled.
  http_tool    -> ["GET https://api.example.com/api/v1/me","Authorization=Bearer demo-token-for-alice","X-User-Id=alice"]
  command_tool -> ["exec \"printenv\"","PATH=/usr/local/bin:/usr/bin:/bin","USER_ACCESS_TOKEN=demo-token-for-alice","USER_ID=alice"]
```

## Core integration

In production code the lifecycle plumbing is handled by `Runner`; you only
wire the plugin, the executor and the MCP hook. A minimal setup looks like:

```go
// 1. Register the plugin once per Runner.
runnerInstance := runner.NewRunner(
    appName,
    agentInstance,
    runner.WithPlugins(identity.NewPlugin(provider)),
)

// 2. For skill_run / workspace_exec / any command tool, wrap the executor
// so identity.EnvVars reach exec.Cmd.Env automatically.
exec = codeexecutor.NewEnvInjectingCodeExecutor(
    exec,
    identity.EnvVarsFromContext,
)

// 3. For MCP HTTP toolsets, install a before-request hook that writes the
// identity headers onto every outgoing request.
ts := toolmcp.NewMCPToolSet(cfg,
    toolmcp.WithMCPOptions(tmcp.WithHTTPBeforeRequest(
        func(ctx context.Context, req *http.Request) error {
            headers, err := identity.HeadersFromContext(ctx)
            if err != nil {
                return err
            }
            for k, v := range headers {
                req.Header.Set(k, v)
            }
            return nil
        },
    )),
)
```

For custom tools you implement yourself, just read identity inside `Call`:

```go
id, ok := identity.FromContext(ctx)
```

See the package doc comment in `plugin/identity/doc.go` for the provider /
consumer boundary and migration guidance when porting an existing
per-tool credential-resolution scheme.

## Files

- `main.go`: program entry and side-by-side comparison. Manually drives
  `BeforeAgent` / `BeforeTool` to show how `Runner` threads identity through
  each tool call.
- `provider.go`: a minimal `identity.Provider` that fabricates a token
  and a set of headers / env vars per user.
- `tool.go`: two fake tools — one HTTP-bound, one command-bound — that
  print what they would emit, so the effect of the plugin is observable
  without any real backend.
