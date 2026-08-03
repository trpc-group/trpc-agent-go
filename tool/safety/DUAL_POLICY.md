# Dual policy: Guard + workspaceexec from one YAML

Issue 2002 asks for pre-exec scanning **and** spawn-time allow/deny that stay
aligned. This package does **not** fork a second shell parser. One policy file
feeds both surfaces through `Policy.CommandLists()`.

## One source of truth

```text
tool_safety_policy.yaml
        │
        ▼
safety.NewGuard(WithPolicyFile(...))
        │
        ├─► agent.WithToolPermissionPolicy(guard)     // pre-exec JSON scan
        │
        └─► allow, deny := guard.Policy().CommandLists()
                 │
                 ▼
            workspaceexec.NewExecTool(runner,
                workspaceexec.WithAllowedCommands(allow...),
                workspaceexec.WithDeniedCommands(deny...),
            )                                        // spawn-time shellsafe
```

## Why two hops

| Layer | Sees | Job |
|---|---|---|
| Guard (`PermissionPolicy`) | tool JSON args (`command`, `stdin`, `code_blocks`, paths, URLs, …) | deny/ask before invoke; audit/report |
| workspaceexec spawn | the command string actually executed | `internal/shellsafe` allow/deny at process start |

They are complementary. Skipping `CommandLists()` wiring can yield
“Guard allowed, spawn denied” (or the reverse). Sync sync is intentional —
spawn options stay explicit at the call site.

## Minimal wire-up

```go
guard := safety.NewGuard(safety.WithPolicyFile("tool_safety_policy.yaml"))
allow, deny := guard.Policy().CommandLists()

execTool := workspaceexec.NewExecTool(runner,
    workspaceexec.WithAllowedCommands(allow...),
    workspaceexec.WithDeniedCommands(deny...),
)

// same guard instance:
//   agent.WithToolPermissionPolicy(guard)
```

`CommandLists()` returns **copies**. Mutating the slices does not change the
Guard policy; reload YAML / rebuild Policy if rules change.

## What this proves (and what it does not)

- Proves: apps need not import `internal/shellsafe` to keep **policy YAML**
  allow/deny lists aligned between Guard and ExecTool options.
- Proves: `CommandLists()` returns copies of those YAML-owned slices.
- Note: workspaceexec / shellsafe also apply a **built-in** wrapper deny set
  (`bash`, `sh`, …) at spawn time even when those names are absent from YAML.
  Guard’s text scans + YAML denials cover the pre-exec half; spawn adds the
  built-in set on top.
- Does **not** auto-patch every ExecTool in the process — you still pass the
  options when constructing the tool.
- Does **not** replace sandboxing, CleanEnv, or live output caps.

## Demo

`examples/tool_safety_guard` prints `CommandLists` sizes and the exact
`WithAllowedCommands` / `WithDeniedCommands` call shape on every `go run .`.
