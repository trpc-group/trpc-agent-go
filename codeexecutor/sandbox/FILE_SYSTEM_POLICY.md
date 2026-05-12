# Sandbox File System Policy

This package uses a small file-system access lattice inspired by Codex:

- `AccessRead` means paths are readable but not writable.
- `AccessWrite` means paths are readable and writable.
- `AccessNone` means paths are neither readable nor writable.

`AccessWrite` intentionally includes read access. The model does not support a
"writable but unreadable" path because the Linux backend cannot enforce that
shape consistently once the path is mounted into the process namespace.

## Rule Targets

File-system rules use one of three target kinds:

- `RulePath` targets a concrete path. Relative paths are workspace-relative;
  absolute paths are host paths.
- `RuleSpecial` targets one of the well-known sandbox directories such as
  `SpecialRoot`, `SpecialWork`, `SpecialOut`, or `SpecialSkills`.
- `RuleGlob` targets workspace-relative glob matches. Glob rules are only valid
  with `AccessNone`.

`AccessRead` and `AccessWrite` support `RulePath` and `RuleSpecial`. `AccessNone`
supports `RulePath`, `RuleSpecial`, and `RuleGlob`.

## Resolution

When multiple rules match a path, the runtime chooses the most specific rule
first. Specificity is based on path depth: `work/secret.txt` is more specific
than `work`, and `work` is more specific than the workspace root.

If two matching rules are equally specific, access precedence breaks the tie:

```text
none > write > read
```

This keeps carve-outs predictable. For example, a profile can grant write access
to `work` but set `work/secret.txt` to `none`, making the secret neither readable
nor writable. A more specific `read` rule under a writable directory can make
that subtree read-only.

## Linux Enforcement

The Linux backend materializes the same policy through `bubblewrap`:

- `/` is mounted read-only to provide a read-mostly host view.
- The sandbox workspace and explicit external write grants are mounted writable.
- Explicit external read grants are mounted read-only.
- `AccessNone` path, special, and glob matches are covered by unreadable masks:
  files are replaced with a zero-permission mask file, and directories are
  covered by an empty `tmpfs`.

The Go-level file APIs and Linux mount setup both derive from the same
`AccessNone` rules so a path denied by `Collect`, `PutFiles`, or `StageInputs` is
also denied to a process running inside the OS sandbox.

## Protected Metadata

Protected metadata is a built-in write protection for sensitive directories
inside the workspace. The default protected set is:

```text
.git
.agents
.trpc-agent-sandbox
```

Protected metadata entries are interpreted as workspace-root-relative paths.
For the default single-segment names above, protection applies to the top-level
workspace path and its children, for example `.git` and `.git/config`. It does
not match the same name at arbitrary depth, such as `vendor/.git/config`.

Protected metadata is not a replacement for `AccessNone`. It only prevents
writes to those paths, even when a broader rule grants workspace write access.
Use `AccessNone` when a path must be neither readable nor writable, or when a
nested metadata directory must be denied explicitly.

## Default Profile

The runtime defaults to `WorkspaceWriteProfile()`. When callers pass
`WithPermissionProfile`, that explicit profile replaces the default.

`WorkspaceWriteProfile()` uses these file-system rules:

- `SpecialRoot` is `AccessRead`, giving the sandbox a read-only host view.
- `SpecialWorkspace`, `SpecialWork`, `SpecialHome`, `SpecialTmp`,
  `SpecialRuns`, `SpecialOut`, and `SpecialSkills` are `AccessWrite`.
- Default protected metadata still blocks writes to `.git`, `.agents`, and
  `.trpc-agent-sandbox` inside the workspace.

## Public Builders

The builder API mirrors the access model:

- `WithReadPaths` adds concrete `AccessRead` path rules.
- `WithWritePaths` adds concrete `AccessWrite` path rules.
- `WithNoAccessPaths` adds concrete `AccessNone` path rules.
- `WithNoAccessGlobs` adds workspace-relative `AccessNone` glob rules.

Path builders accept concrete paths. Glob no-access is intentionally separate so
callers can distinguish exact path rules from workspace-relative pattern rules.
