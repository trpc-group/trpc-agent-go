# PromptIter Regression Loop Report

- Run ID: `promptiter-regression-loop-app-20260717`
- App: `promptiter-regression-loop-app`
- Mode: `deterministic`
- Data source: `fake model with deterministic evalset responses`
- Decision: **REJECT**
- Target surface: `agent:travel-support#instruction`
- Engine: `deterministic-promptiter` (`fake-model-v1`)

## Score Summary

| Split | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| Train | 0.3333 | 0.8333 | 0.5000 |
| Validation | 0.8333 | 0.8333 | 0.0000 |

## Gate Decision

- validation score gain 0.0000 is below threshold 0.0500
- new hard fails 1 exceed limit 0
- 1 critical validation case(s) regressed

## Validation Case Delta

| Case | Critical | Baseline | Candidate | Delta | Transition |
|---|---:|---:|---:|---:|---|
| `val_json_refund` | false | 0.5000 | 1.0000 | 0.5000 | fixed |
| `val_weather_berlin` | false | 1.0000 | 1.0000 | 0.0000 | stayed_pass |
| `val_critical_direct_status` | true | 1.0000 | 0.5000 | -0.5000 | regressed |

## Validation Output Evidence

| Case | Baseline actual | Baseline tools | Candidate actual | Candidate tools |
|---|---|---:|---|---:|
| `val_json_refund` | Refund request r-204 is approved for 35 USD. | 0 | {"refund_id":"r-204","status":"approved","amount_usd":35} | 0 |
| `val_weather_berlin` | Berlin is cloudy today at 8 C. | 1 | Berlin is cloudy today at 8 C. | 1 |
| `val_critical_direct_status` | TR900 is boarding at gate K12. | 0 | {"flight":"TR900","status":"boarding","gate":"K12"} | 0 |

## Failure Attribution

### Train

- `knowledge_recall_gap`: 1

### Validation

- `unknown`: 1


## Audit Summary

- Candidate: `round-1-json-tool-overfit`
- Calls: `12`
- Estimated cost: `$0.000114`
- Duration: `0 ms`
- Seed: `20260717`

The candidate is not automatically safe to publish unless the gate decision is ACCEPT.
