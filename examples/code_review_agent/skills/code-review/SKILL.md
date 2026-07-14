# Code Review Skill

Use this skill to review Go code changes from a unified diff or repository
workspace. The skill focuses on deterministic, auditable checks before any
model-assisted commentary.

## Inputs

- `diff`: unified diff text.
- `repo_path`: optional repository path staged into the sandbox.
- `changed_files`: parsed file and hunk metadata.

## Workflow

1. Parse the diff and restrict findings to changed Go lines.
2. Apply the rules in `docs/rules.md`.
3. Request permission before running any command.
4. Run allow-listed checks in the sandbox:
   - `go test ./...`
   - `go vet ./...`
   - scripts under `skills/code-review/scripts/`
5. Redact secrets before writing reports, artifacts, or database rows.
6. Return structured findings with severity, file, line, evidence,
   recommendation, confidence, source, and rule_id.

## Safety

Never run network, shell-wrapper, destructive, or repository-external commands
without a permission decision. Denied or unknown commands must be recorded as
governance decisions and must not be executed.
