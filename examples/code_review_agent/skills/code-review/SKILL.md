# code-review

This Skill packages the deterministic review rules used by the Go CR Agent. It is loaded through `tool/skill` and executed through `skill_run` before optional sandbox Go checks and optional model review.

## Role in the Agent

The Skill is the reusable review contract package:

- documents rule IDs, severity, confidence, and output schema;
- provides the fixed script entrypoint `scripts/check.sh`;
- scans unified diffs from stdin and emits structured JSON;
- keeps deterministic findings separate from model-only semantic findings.

The Agent remains responsible for input parsing, PermissionPolicy decisions, CodeExecutor/container sandboxing, model review, dedupe/redaction, reports, SQLite audit records, artifacts, and metrics.

## Execution Contract

`scripts/check.sh` reads a unified diff from stdin and writes one JSON object to stdout:

```json
{
  "findings": [
    {
      "severity": "high",
      "category": "resource",
      "file": "service.go",
      "line": 42,
      "title": "Opened resource has no close path",
      "evidence": "f, _ := os.Open(path)",
      "recommendation": "Defer Close() immediately after the resource is opened.",
      "confidence": "high",
      "source": "skill_run",
      "rule_id": "resource-leak",
      "status": "finding"
    }
  ],
  "warnings": []
}
```

Findings must use these fields: `severity`, `category`, `file`, `line`, `title`, `evidence`, `recommendation`, `confidence`, `source`, `rule_id`, and `status`.

## Severity and Status

- `critical`: secret leakage or equivalent must-fix security exposure.
- `high`: likely runtime bug, lifecycle leak, concurrency risk, or dangerous error handling.
- `medium`: maintainability issue that should be addressed before merge when feasible.
- `low`: low-confidence or advisory signal.

Status routing:

- `finding`: high-confidence review item.
- `warning`: deterministic but low-severity advisory item.
- `needs_human_review`: governance, sandbox, model, or low-confidence signal that should not block automatically.

## Deterministic Rules

See [rules.md](rules.md) for rule IDs, trigger boundaries, false-positive guardrails, and fixture coverage.

Current categories cover:

- secret leakage;
- direct panic/error-handling hazards;
- goroutine/context lifecycle risks;
- resource close paths;
- database handle/transaction lifecycle;
- TODO/FIXME markers;
- missing-test hints.

## Model Review Boundary

The optional model stage runs after this Skill. The model receives a redacted diff summary, existing deterministic findings, sandbox summary, input metadata, and governance summary. It must only return incremental semantic value and must not duplicate deterministic findings.

Examples of model-only risks:

- cross-file authorization bypass;
- nil or zero-value boundary behavior changes;
- business state transition inconsistency;
- transaction semantics that commit failed business operations;
- swallowed errors that still return success;
- cancellation swallowed across function boundaries;
- integration behavior that deterministic line rules cannot infer.

Low-confidence model output is routed to `needs_human_review`.

## Change Discipline

- Every new `rule_id` needs at least one fixture and matrix row.
- Every false-positive guardrail needs a safe fixture or holdout case.
- Keep `scripts/check.sh` as the stable entrypoint even if helper scripts are added later.
- Do not emit secrets in evidence; redact before stdout, and rely on Agent redaction as a second layer.
