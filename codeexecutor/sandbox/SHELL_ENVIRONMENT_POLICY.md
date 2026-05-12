# Sandbox Shell Environment Policy

This package builds an explicit shell environment for every sandboxed command.
The model is aligned with Codex's `ShellEnvironmentPolicy`: start from an
inheritance mode, apply filters and overrides, then inject sandbox runtime
variables.

The environment policy is security-sensitive. A command receives only the
variables selected by this policy and the runtime-owned variables required to
make the sandbox workspace behave consistently.

## Inheritance

`ShellEnvironmentPolicy.Inherit` supports three modes:

- `ShellEnvironmentPolicyInheritAll` inherits the full host environment. This is
  the default and matches Codex shell behavior.
- `ShellEnvironmentPolicyInheritCore` inherits only shell startup variables such
  as `PATH`, `HOME`, `SHELL`, user, locale, and temporary-directory variables.
- `ShellEnvironmentPolicyInheritNone` starts from an empty caller-controlled
  environment.

Use `Core` or `None` when the executor is embedded in a stricter service path
and model/provider credentials must not be inherited by default.

## Resolution

For each command, the runtime resolves the caller-controlled environment in this
order:

1. Start from `Inherit`: `All`, `Core`, or `None`.
2. If `ApplyDefaultExcludes` is true, remove default secret-like names.
3. Apply custom `Exclude` patterns.
4. Apply explicit `Set` overrides.
5. Apply per-run `RunProgramSpec.Env` overrides.
6. If `IncludeOnly` is non-empty, retain only matching names.
7. Inject sandbox runtime variables such as `HOME`, `TMPDIR`, `WORKSPACE_DIR`,
   `WORK_DIR`, `OUTPUT_DIR`, `RUN_DIR`, and `SKILLS_DIR`.
8. Add a fallback `PATH` if no `PATH` remains.

`Exclude` and `IncludeOnly` use case-insensitive name patterns with `*` and `?`
wildcards.

## IncludeOnly

`IncludeOnly` is a final allow-list over caller-controlled environment. It is
not "core plus these extra variables" and it does not rescan the host
environment.

For example:

```go
ShellEnvironmentPolicy{
	Inherit:     ShellEnvironmentPolicyInheritAll,
	IncludeOnly: []string{"TRPC_*"},
	Set:         map[string]string{"FORCED": "1"},
}
```

keeps inherited, `Set`, and per-run names only when they match `TRPC_*`.
`FORCED` is filtered out because it does not match. Sandbox runtime variables
are injected after the allow-list so the workspace still has stable `HOME`, temp,
and output paths.

## Default Excludes

`ApplyDefaultExcludes` removes names that look secret-like, including variables
whose names contain `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, or `CREDENTIAL`.

The default is false because `All` should preserve Codex-like shell fidelity.
Callers that want broad inheritance but automatic credential trimming can enable
it explicitly, or use `Core`/`None` for stronger isolation.

## Runtime Variables

Sandbox-owned variables are not policy grants; they are part of the workspace
contract. The runtime always sets:

- `HOME` to the sandbox home directory.
- `TMPDIR`, `TMP`, and `TEMP` to the sandbox temp directory.
- `WORKSPACE_DIR` to the workspace root.
- `WORK_DIR`, `OUTPUT_DIR`, `RUN_DIR`, and `SKILLS_DIR` to workspace
  subdirectories.
- A fallback `PATH` when no policy or override supplies one.

These values are applied after caller-controlled filters so host variables
cannot redirect workspace-sensitive paths outside the sandbox.

## Enforcement

Both execution paths consume this explicit environment. The disabled backend
sets `cmd.Env`, and the Linux `bubblewrap` backend uses `--clearenv` followed by
explicit `--setenv` entries.

## Diagnostics

`RedactEnvironment` is a diagnostics helper, not an enforcement mechanism. It
redacts names containing `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, or `CREDENTIAL`
before logging. It should not be used as a substitute for inheritance and
filtering policy.
