---
name: code-review
description: Automated code review skill for Go projects, covering security, goroutine leaks, resource management, error handling, test coverage, sensitive data, and database lifecycle issues.
---

# Code Review Skill

The Code Review Skill automates static analysis and rule-based review for Go
projects. It inspects source diffs and full files, matches them against a
curated set of rules, runs `go vet` and `go test` for validation, and emits
structured findings that map directly to the CR agent's `Finding` and
`ReviewReport` types.

## Overview

This skill is designed to be used by the CR agent pipeline. It provides:

- A rule library under `rules/` that documents detection patterns, severity
  levels, and remediation guidance for each category.
- Helper scripts under `scripts/` that wrap `go vet`, `go test`, and diff
  parsing so the agent can gather evidence inside a sandbox.
- A consistent output format that aligns with `examples/cr_agent/internal/types`.

The rules are intentionally written as human-readable specifications. The agent
applies them by reading a rule document, inspecting the relevant code, and
producing one `Finding` per confirmed violation.

## Usage

### Reviewing a unified diff

Pass a diff (for example from `git diff`) to the diff parser, then review each
changed file against the applicable rules:

```bash
# Produce a JSON list of changed files from a unified diff.
python3 scripts/parse_diff.py diff.patch > changed_files.json

# Run go vet on the package owning the changed files.
bash scripts/run_go_vet.sh ./...

# Run the test suite affected by the change.
bash scripts/run_go_test.sh ./...
```

### Reviewing explicit files

When the review input is a set of file paths (`InputTypeFiles`), evaluate each
file against every rule category that can apply to it. Exported logic, database
code, goroutine usage, and resource handling are the primary targets.

### Reviewing a git commit range

When the review input is a git repository and commit range
(`InputTypeGit`), generate the diff first:

```bash
git diff <commit-range> > diff.patch
python3 scripts/parse_diff.py diff.patch > changed_files.json
```

## Supported rule categories

The skill ships seven rule categories. Each category lives in its own document
under `rules/` and maps to a `Category` constant in
`examples/cr_agent/internal/types`.

| Category constant        | Rule document                  | Focus                                                       |
|--------------------------|--------------------------------|-------------------------------------------------------------|
| `CategorySecurity`       | `rules/security.md`            | SQL injection, command injection, hardcoded secrets, crypto |
| `CategoryGoroutineLeak`  | `rules/goroutine.md`           | Uncancelled contexts, no-exit goroutines, blocking sends   |
| `CategoryResourceClose`  | `rules/resource.md`            | Unclosed files, connections, HTTP bodies, rows, missing defer |
| `CategoryErrorHandling`  | `rules/error_handling.md`      | Ignored errors, error shadowing, unwrapped errors          |
| `CategoryMissingTest`    | `rules/test_missing.md`        | New exported functions without tests, logic without updates |
| `CategorySensitiveLeak`  | `rules/sensitive_info.md`      | Logged passwords/tokens, hardcoded credentials             |
| `CategoryDBLifecycle`    | `rules/db_lifecycle.md`        | Uncommitted transactions, missing rollback, pool leaks     |

Each rule document contains:

- **Rule ID**: a stable identifier used as the `Finding.RuleID`.
- **Severity**: the default `Severity` for findings produced by the rule.
- **Description**: what the rule detects and why it matters.
- **Detection patterns**: the code shapes the rule looks for.
- **Examples**: incorrect and corrected code side by side.
- **Fix recommendation**: actionable remediation guidance copied into
  `Finding.Recommendation`.

## Scripts

All scripts live under `scripts/` and are meant to run from the skill root or
with paths relative to the repository under review.

### `scripts/run_go_vet.sh`

Runs `go vet` over a package pattern and reports failures.

```bash
bash scripts/run_go_vet.sh ./...
```

The first positional argument is the package pattern; it defaults to `./...`.
The script exits non-zero when `go vet` reports issues, so the agent can treat
the exit code as a signal.

### `scripts/run_go_test.sh`

Runs `go test` over a package pattern and reports failures.

```bash
bash scripts/run_go_test.sh ./...
```

The first positional argument is the package pattern; it defaults to `./...`.
Use the `-run` or `-short` flags by passing them after the package pattern.

### `scripts/parse_diff.py`

Parses a unified diff and prints a JSON array describing every changed file.

```bash
python3 scripts/parse_diff.py diff.patch
# Or read from stdin:
git diff main...HEAD | python3 scripts/parse_diff.py -
```

The output is a JSON array of objects, one per file, ordered by first
appearance in the diff.

## Output format

### Diff parser output (`parse_diff.py`)

```json
[
  {
    "path": "internal/db/query.go",
    "old_path": "internal/db/query.go",
    "status": "modified",
    "added_lines": 9,
    "deleted_lines": 3,
    "hunks": [
      {
        "old_start": 18,
        "old_count": 11,
        "new_start": 18,
        "new_count": 19
      }
    ],
    "added_line_numbers": [22, 23, 24],
    "deleted_line_numbers": [20]
  }
]
```

Fields:

- `path`: the post-change file path (`+++` side).
- `old_path`: the pre-change file path (`---` side); identical to `path` for
  pure modifications.
- `status`: `added`, `modified`, `renamed`, or `deleted`.
- `added_lines` / `deleted_lines`: counts of `+` and `-` content lines.
- `hunks`: per-hunk header metadata.
- `added_line_numbers` / `deleted_line_numbers`: 1-based line numbers in the
  new and old file respectively, useful for anchoring `Finding.Line`.

### Findings

Each confirmed rule violation is emitted as a `Finding` as defined in
`examples/cr_agent/internal/types/types.go`. The agent populates:

- `RuleID` from the rule document.
- `Severity` from the rule's stated severity, possibly raised for
  high-confidence matches.
- `Category` from the matching `Category` constant.
- `File` and `Line` from the diff parser output or full-file analysis.
- `Title`, `Evidence`, and `Recommendation` from the rule document and the
  matched code.
- `Source` set to the analyzer that produced the finding (for example
  `code-review-skill` or `go-vet`).
- `Confidence` reflecting how strongly the pattern matched.

Findings are ordered highest severity first in the final `ReviewReport`.
