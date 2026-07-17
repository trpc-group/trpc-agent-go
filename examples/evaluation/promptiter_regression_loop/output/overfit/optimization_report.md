# Prompt Optimization Report

- Decision: **REJECTED**
- Mode: `fake`
- Seed: `20260717`
- Model: `deterministic/fake-trace-runner`
- Fingerprint: `260f56efd48a978db7f96621504698c54a0e819fa61c1e8c1cae101092fe8fdb`
- Duration: `1 ms`

## Validation summary

| Metric | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| Mean score | 0.6667 | 0.3333 | -0.3333 |
| Pass^3 rate | 0.6667 | 0.3333 | -0.3333 |

Paired bootstrap 90% CI: `[-1.0000, 0.3333]`.

## Gate checks

| Check | Result | Observed | Requirement |
|---|---|---:|---:|
| minimum_score_gain | FAIL | -0.3333 | >= 0.0200 |
| no_new_hard_failure | PASS | 0.0000 | == 0.0000 |
| critical_cases_do_not_regress | FAIL | 1.0000 | == 0.0000 |
| pass_power_k_does_not_regress | FAIL | 0.3333 | >= 0.6667 |
| bootstrap_ci_lower_bound | FAIL | -1.0000 | >= 0.0000 |
| calls_budget | PASS | 0.0000 | <= 48.0000 |
| tokens_budget | PASS | 0.0000 | <= 100000.0000 |
| cost_budget_cny | PASS | 0.0000 | <= 20.0000 |

## Per-case delta

| Case | Critical | Baseline | Candidate | Delta | Pass^3 |
|---|---|---:|---:|---:|---|
| overfit_direct_answer | true | 1.0000 | 0.0000 | -1.0000 | true → false |
| overfit_grounding_improves | false | 0.0000 | 1.0000 | +1.0000 | false → true |
| overfit_no_tool_schema | false | 1.0000 | 0.0000 | -1.0000 | true → false |

## Failure attribution

### Train baseline

- `agent_tool`: 1
- `environment`: 1
- `format`: 1
- `knowledge`: 1
- `prompt`: 2

### Train candidate

- No failed cases.

### Validation baseline

- `knowledge`: 1

### Validation candidate

- `model`: 1
- `prompt`: 1


## Audit and anti-overfitting notes

PromptIter receives only the training set. The final decision uses the independent validation set, three repeated runs, hard-failure vetoes, critical-case protection, Pass^k stability, a paired bootstrap interval, and resource budgets.

Selected prompt:

```text
Answer the user's request helpfully and concisely. Use tools when useful.
```
