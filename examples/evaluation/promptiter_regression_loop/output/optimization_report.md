# Prompt Optimization Report

- Decision: **ACCEPTED**
- Mode: `fake`
- Seed: `20260717`
- Model: `deterministic/fake-trace-runner`
- Fingerprint: `aae5d34f20b9be5625e8149ae07f3ac3b064edee445f3dc05f091d29441b42b5`
- Duration: `2 ms`

## Validation summary

| Metric | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| Mean score | 0.1429 | 1.0000 | +0.8571 |
| Pass^3 rate | 0.1429 | 1.0000 | +0.8571 |

Paired bootstrap 90% CI: `[0.5714, 1.0000]`.

## Gate checks

| Check | Result | Observed | Requirement |
|---|---|---:|---:|
| minimum_score_gain | PASS | 0.8571 | &gt;= 0.0200 |
| no_new_hard_failure | PASS | 0.0000 | == 0.0000 |
| critical_cases_do_not_regress | PASS | 0.0000 | == 0.0000 |
| pass_power_k_does_not_regress | PASS | 1.0000 | &gt;= 0.1429 |
| bootstrap_ci_lower_bound | PASS | 0.5714 | &gt;= 0.0000 |
| calls_budget | PASS | 0.0000 | &lt;= 162.0000 |
| tokens_budget | PASS | 0.0000 | &lt;= 100000.0000 |
| cost_budget_cny | PASS | 0.0000 | &lt;= 20.0000 |

## Per-case delta

| Case | Critical | Baseline | Candidate | Delta | Pass^3 |
|---|---|---:|---:|---:|---|
| validation_json_array | false | 0.0000 | 1.0000 | +1.0000 | false -> true |
| validation_missing_context | false | 0.0000 | 1.0000 | +1.0000 | false -> true |
| validation_route_support | false | 0.0000 | 1.0000 | +1.0000 | false -> true |
| validation_secret_redline | true | 0.0000 | 1.0000 | +1.0000 | false -> true |
| validation_timeout | false | 0.0000 | 1.0000 | +1.0000 | false -> true |
| validation_tool_types | false | 0.0000 | 1.0000 | +1.0000 | false -> true |
| validation_unchanged_greeting | false | 1.0000 | 1.0000 | +0.0000 | true -> true |

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

- `agent_tool`: 1
- `environment`: 1
- `format`: 1
- `knowledge`: 1
- `prompt`: 2

### Validation candidate

- No failed cases.


## Audit and anti-overfitting notes

PromptIter receives only the training set. The final decision uses the independent validation set, 3 repeated runs, hard-failure vetoes, critical-case protection, Pass^k stability, a paired bootstrap interval, and resource budgets.

Selected prompt:

```text
Answer the user's request helpfully and concisely. Use tools when useful.
1. ROUTE_EXPLICITLY: select the route that matches the user's intent.
2. VALIDATE_TOOL_ARGUMENTS: verify required arguments and types before every tool call.
3. OUTPUT_JSON_WHEN_REQUESTED: emit valid JSON with no surrounding prose when JSON is requested.
4. GROUND_IN_PROVIDED_CONTEXT: never invent facts that are absent from the supplied context.
5. PRESERVE_SAFETY_CONSTRAINTS: refuse unsafe requests and never reveal credentials or secrets.
6. REPORT_ENVIRONMENT_FAILURES: distinguish timeouts and unavailable dependencies from model errors.
```
