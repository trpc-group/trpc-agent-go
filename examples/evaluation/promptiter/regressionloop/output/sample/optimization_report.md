# PromptIter Regression Report

- Run: `sample`
- Status: `succeeded`
- Decision: **ACCEPT**

## Round 1

| Case | Metric | Change | Score delta |
|---|---|---|---:|
| validation-json-format | json_format | unchanged_pass | 0.000000 |
| validation-json-format | response_pattern | new_pass | 1.000000 |
| validation-json-format | tool_trajectory | unchanged_pass | 0.000000 |
| validation-security-critical | json_format | unchanged_pass | 0.000000 |
| validation-security-critical | response_pattern | new_pass | 1.000000 |
| validation-security-critical | tool_trajectory | unchanged_pass | 0.000000 |
| validation-shipping-return | json_format | unchanged_pass | 0.000000 |
| validation-shipping-return | response_pattern | new_pass | 1.000000 |
| validation-shipping-return | tool_trajectory | unchanged_pass | 0.000000 |

### Candidate prompt

```text
[balanced] answer the request accurately and concisely
```

### Gate checks

| Check | Result | Reason |
|---|---|---|
| case_drop | pass |  |
| critical_case | pass |  |
| estimated_cost | pass |  |
| evidence_shape | pass |  |
| hard_failures | pass |  |
| latency | pass |  |
| minimum_gain | pass |  |
| model_calls | pass |  |
| run_status | pass |  |
| tokens | pass |  |
| tool_calls | pass |  |
## Round 2

| Case | Metric | Change | Score delta |
|---|---|---|---:|
| validation-json-format | json_format | unchanged_pass | 0.000000 |
| validation-json-format | response_pattern | new_failure | -1.000000 |
| validation-json-format | tool_trajectory | unchanged_pass | 0.000000 |
| validation-security-critical | json_format | unchanged_pass | 0.000000 |
| validation-security-critical | response_pattern | new_failure | -1.000000 |
| validation-security-critical | tool_trajectory | unchanged_pass | 0.000000 |
| validation-shipping-return | json_format | unchanged_pass | 0.000000 |
| validation-shipping-return | response_pattern | new_failure | -1.000000 |
| validation-shipping-return | tool_trajectory | unchanged_pass | 0.000000 |

### Candidate prompt

```text
[ineffective] answer the request accurately and concisely
```

### Gate checks

| Check | Result | Reason |
|---|---|---|
| case_drop | fail | a case score drop exceeds the limit |
| critical_case | fail | a critical rule failed |
| estimated_cost | pass |  |
| evidence_shape | pass |  |
| hard_failures | fail | candidate retains too many hard failures |
| latency | pass |  |
| minimum_gain | fail | validation gain is below the minimum |
| model_calls | pass |  |
| run_status | pass |  |
| tokens | pass |  |
| tool_calls | pass |  |

## Round 3

| Case | Metric | Change | Score delta |
|---|---|---|---:|
| validation-json-format | json_format | unchanged_pass | 0.000000 |
| validation-json-format | response_pattern | new_failure | -1.000000 |
| validation-json-format | tool_trajectory | unchanged_pass | 0.000000 |
| validation-security-critical | json_format | unchanged_pass | 0.000000 |
| validation-security-critical | response_pattern | new_failure | -1.000000 |
| validation-security-critical | tool_trajectory | unchanged_pass | 0.000000 |
| validation-shipping-return | json_format | unchanged_pass | 0.000000 |
| validation-shipping-return | response_pattern | new_failure | -1.000000 |
| validation-shipping-return | tool_trajectory | unchanged_pass | 0.000000 |

### Candidate prompt

```text
[overfit] answer the request accurately and concisely
```

### Gate checks

| Check | Result | Reason |
|---|---|---|
| case_drop | fail | a case score drop exceeds the limit |
| critical_case | fail | a critical rule failed |
| estimated_cost | pass |  |
| evidence_shape | pass |  |
| hard_failures | fail | candidate retains too many hard failures |
| latency | pass |  |
| minimum_gain | fail | validation gain is below the minimum |
| model_calls | pass |  |
| run_status | pass |  |
| tokens | pass |  |
| tool_calls | pass |  |
