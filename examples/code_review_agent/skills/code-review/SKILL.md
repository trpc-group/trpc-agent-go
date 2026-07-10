---
name: code-review
description: Automated Go code review skill that runs go vet, go test, staticcheck, and parses unified diffs to surface findings.
---

# Code Review Skill

## Overview

This skill performs automated Go code review against a mounted repository or a
supplied unified diff. It combines deterministic tooling (`go vet`,
`staticcheck`, `go test`) with rule-based diff analysis to surface security,
correctness, reliability and quality findings.

The skill is intentionally offline-friendly: the core checks (`go vet`,
`staticcheck`, `go test`) require no network access and operate solely on the
read-only repository mounted into the sandboxed workspace.

## Safety Boundaries

- This skill only reads from the mounted repo (read-only).
- Scripts execute via the framework's sandboxed `workspace_exec`.
- No network access required for offline checks (`go vet`, `staticcheck`).
- Permission gating: all commands pass through the agent's `PermissionPolicy`.

## Usage

1. Parse the diff:

   ```sh
   sh scripts/parse_diff.sh <diff-file>
   ```

2. Run `go vet`:

   ```sh
   sh scripts/run_go_vet.sh [package]
   ```

3. Run `staticcheck`:

   ```sh
   sh scripts/run_staticcheck.sh [package]
   ```

4. Run `go test`:

   ```sh
   sh scripts/run_go_unit.sh [package]
   ```

The default package argument for the vet / staticcheck / test scripts is
`./...` (all packages under the mounted repo root).

## Outputs

All artifacts are written under the skill workspace's `out/` directory:

- `out/findings.json` — structured findings aggregated by the orchestrator.
- `out/vet.txt` — raw `go vet` output.
- `out/test.txt` — raw `go test` output.
- `out/staticcheck.txt` — raw `staticcheck` output.
- `out/input.diff` — copy of the parsed diff input.

## Rule Catalog

See `docs/rules.md` for the full index of detection rules. Each rule documents
its ID, severity, category, evidence example, recommendation and confidence.
