# Code review Skill

This Skill defines the deterministic and governed portion of the Go code review
example. `SKILL.md` is the Agent-facing workflow contract; it intentionally does
not contain shell recipes or user-controlled execution templates.

## Layout

- `SKILL.md` defines activation boundaries, ordered phases, failure behavior,
  output requirements, and non-negotiable safety rules.
- `rules/rules.json` is the validated executable manifest. It maps stable rule
  IDs to analyzer implementations, modes, severity, confidence, and enablement.
- `docs/rules.md` explains qualifying evidence, common exclusions, confidence
  routing, and deduplication behavior.
- `scripts/checkrunner` is the trusted sandbox entry point. It accepts only
  known check IDs and writes one bounded JSON result artifact.

## Runtime contract

The application loads this Skill before invoking the structured `code_review`
tool. Production uses the container runtime. Fake and rule-only modes preserve
the deterministic orchestration without executing project code. Local mode is
an explicitly enabled development fallback.

The application, not the model, constructs the complete check specification.
Safety filtering and PermissionPolicy evaluate exact workspace/runtime values,
and both decisions are durable before staging or execution. Timeout and failed runs are
persisted and downgrade the task to `completed_with_warnings` instead of
crashing the review. Caller cancellation is persisted but retains a failed task
status.

## Extending the Skill

Adding a rule requires all of the following in one change:

1. Add a unique rule declaration to `rules/rules.json`.
2. Implement its deterministic analyzer and register the implementation name.
3. Document positive evidence, exclusions, and confidence behavior in
   `docs/rules.md`.
4. Add positive, negative, deduplication, and redaction tests as applicable.

Adding a sandbox check additionally requires a fixed runner action, a fixed
argument manifest, Filter validation, Permission handling, bounded result
parsing, and failure-path tests. Arbitrary commands and model-provided runtime
values are not supported extension points.
