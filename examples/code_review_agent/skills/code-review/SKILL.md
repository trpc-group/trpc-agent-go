# Code Review Skill

Use this bundled skill only for the `examples/code_review_agent` pipeline. It
contains deterministic Go review rules and one fixed script entry point.

Run checks with:

```text
scripts/run_checks.sh test ./...
scripts/run_checks.sh vet ./...
scripts/run_checks.sh staticcheck ./...
```

Do not execute user-provided shell strings. The host application binds this
skill, script digest, snapshot digest, command ID, cwd, environment allowlist,
timeout, and output limits into a permission decision before staging.
