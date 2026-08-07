# Holdout Review Fixtures

These fixtures are self-contained adversarial samples for local acceptance. They are not private hidden data; they are committed holdout cases that exercise variants outside the public fixture matrix.

- `holdout-safe-refactor.diff`: clean helper refactor, expected zero findings.
- `holdout-placeholder-secret.diff`: placeholder secret-like names that should not produce critical findings.
- `holdout-secret-private-key.diff`: private-key shaped secret leak.
- `holdout-lifecycle-combo.diff`: combined context, resource, and database lifecycle risks.
- `holdout-pr-shaped-service.diff`: multi-file PR-shaped sample with risky Go code and safe docs change.
- `holdout-guarded-lifecycle.diff`: false-positive guardrail for same-line cleanup/lifecycle guards.
- `holdout-batch-worker-combo.diff`: combined worker lifecycle risks in a more realistic batch path.
- `holdout-env-secret-guard.diff`: false-positive guardrail for env-sourced bearer token construction.
- `holdout-expanded-go-risks.diff`: adversarial mix of HTTP body, SQL concat, command injection, context propagation, mutex, loop defer, bare error return, and loop string concat rules.
- `holdout-expanded-safe-patterns.diff`: safe guardrail for the expanded Go rule family.
- `model-semantic.diff`: generic deterministic fake-model semantic signal.
- `model-authz-bypass.diff`: authorization bypass signal that deterministic line rules should not claim.
- `model-nil-boundary.diff`: nil/zero-value boundary signal.
- `model-state-inconsistency.diff`: cross-function state drift signal.
- `model-transaction-semantic.diff`: transaction semantic misuse signal.
- `model-error-swallow.diff`: swallowed-error success signal.
- `model-safe-semantic.diff`: safe semantic guardrail, expected zero findings.

The `model-*` fixtures prove the model merge path without a real API key. They are not private hidden samples.
