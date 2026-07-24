# Prompt Optimization Report

- Decision: **REJECTED**
- Pipeline status: `succeeded`
- Mode: `fake`
- Candidate source: `deterministic`
- Seed: `20260717`
- Evaluation model: `deterministic/fake-trace-runner`
- Optimizer model: `deterministic/fake-promptiter-optimizer`
- Fingerprint: `04a7e56a88deec2d8040874e74b24dd86c9067376c8e5be903ce0743c741b3ed`
- Duration: `1 ms`

## Resource usage

| Stage | Calls | Input tokens | Output tokens | Cost CNY | Latency ms |
|---|---:|---:|---:|---:|---:|
| Baseline evaluation | 0 | 0 | 0 | 0.000000 | 0 |
| Optimizer | 0 | 0 | 0 | 0.000000 | 0 |
| Candidate evaluation | 0 | 0 | 0 | 0.000000 | 0 |
| Total | 0 | 0 | 0 | 0.000000 | 1 |

## PromptIter audit

- Completed: `true`
- Source: `deterministic`

| Round | Train score | Inner train score | Accepted | Delta | Patch reason |
|---:|---:|---:|---|---:|---|
| 1 | 0.0000 | 1.0000 | true | +1.0000 | apply only remediation directives observed in training loss gradients |

## Validation summary

| Metric | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| Mean score | 0.6667 | 0.3333 | -0.3333 |
| Pass^3 rate | 0.6667 | 0.3333 | -0.3333 |

Paired bootstrap 90% CI: `[-1.0000, 0.3333]`.

## Gate checks

| Check | Result | Observed | Requirement |
|---|---|---:|---:|
| validation_runs_error_free | PASS | 0.0000 | == 0.0000 |
| minimum_score_gain | FAIL | -0.3333 | &gt;= 0.0200 |
| no_new_hard_failure | PASS | 0.0000 | == 0.0000 |
| critical_cases_do_not_regress | FAIL | 1.0000 | == 0.0000 |
| pass_power_k_does_not_regress | FAIL | 0.3333 | &gt;= 0.6667 |
| bootstrap_ci_lower_bound | FAIL | -1.0000 | &gt;= 0.0000 |
| calls_budget | PASS | 0.0000 | &lt;= 165.0000 |
| tokens_budget | PASS | 0.0000 | &lt;= 200000.0000 |
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

PromptIter receives only the training set. Its inner score is a training-only optimization signal. The final decision uses the independent validation set, 3 repeated runs, hard-failure vetoes, critical-case protection, Pass^k stability, a paired bootstrap interval, and resource budgets.

Selected prompt:

```text
Answer the user's request helpfully and concisely. Use tools when useful.
```
