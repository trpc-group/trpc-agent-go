# Prompt Optimization Report

- Decision: **REJECTED**
- Mode: `fake`
- Seed: `20260717`
- Model: `deterministic/fake-trace-runner`
- Fingerprint: `beffffedf2fa17f7dbe184f3f5692ae3d545959326a264003e681fdc9207701c`
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
| minimum_score_gain | FAIL | -0.3333 | &gt;= 0.0200 |
| no_new_hard_failure | PASS | 0.0000 | == 0.0000 |
| critical_cases_do_not_regress | FAIL | 1.0000 | == 0.0000 |
| pass_power_k_does_not_regress | FAIL | 0.3333 | &gt;= 0.6667 |
| bootstrap_ci_lower_bound | FAIL | -1.0000 | &gt;= 0.0000 |
| calls_budget | PASS | 0.0000 | &lt;= 162.0000 |
| tokens_budget | PASS | 0.0000 | &lt;= 100000.0000 |
| cost_budget_cny | PASS | 0.0000 | &lt;= 20.0000 |

## Per-case delta

| Case | Critical | Baseline | Candidate | Delta | Pass^3 |
|---|---|---:|---:|---:|---|
| overfit_direct_answer | true | 1.0000 | 0.0000 | -1.0000 | true -> false |
| overfit_grounding_improves | false | 0.0000 | 1.0000 | +1.0000 | false -> true |
| overfit_no_tool_schema | false | 1.0000 | 0.0000 | -1.0000 | true -> false |

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

PromptIter receives only the training set. The final decision uses the independent validation set, 3 repeated runs, hard-failure vetoes, critical-case protection, Pass^k stability, a paired bootstrap interval, and resource budgets.

Selected prompt:

```text
Answer the user's request helpfully and concisely. Use tools when useful.
```
