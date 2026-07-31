# PromptIter Regression Report

- Mode: `deterministic`
- Model: `deterministic-rule-v1`
- Seed: `42`
- Decision: **accept_candidate**
- Accepted candidate: `generalized`

## Baseline

Train score: **0.333**; validation score: **0.667**.

## Candidate rounds

| Round | Candidate | Train | Validation | Accepted | Reasons |
|---:|---|---:|---:|:---:|---|
| 1 | `ineffective` | 0.333 | 0.667 | false | validation gain 0.000 is below required 0.200 |
| 2 | `training-overfit` | 1.000 | 0.667 | false | validation gain 0.000 is below required 0.200; case validation-held-out-style became a new failure |
| 3 | `generalized` | 1.000 | 1.000 | true | validation gain 0.333 passed all regression and budget gates |

## Validation deltas

### Round 1 — `ineffective`

- `validation-json-contract`: unchanged (1.0 → 1.0)
- `validation-grounded-search`: unchanged (0.0 → 0.0)
- `validation-held-out-style`: unchanged (1.0 → 1.0)
- Failure attribution:
  - `knowledge_recall`: 1
  - `route_error`: 1
  - `tool_or_grounding_error`: 1

### Round 2 — `training-overfit`

- `validation-json-contract`: unchanged (1.0 → 1.0)
- `validation-grounded-search`: new_pass (0.0 → 1.0)
- `validation-held-out-style`: new_failure (1.0 → 0.0)
- Failure attribution:
  - `overfit`: 1

### Round 3 — `generalized`

- `validation-json-contract`: unchanged (1.0 → 1.0)
- `validation-grounded-search`: new_pass (0.0 → 1.0)
- `validation-held-out-style`: unchanged (1.0 → 1.0)

## Accepted prompt

```text
You are a reliable assistant. format=json concise=true route=search cite=sources
```
