# PromptIter Regression Loop Report

- Run ID: `promptiter-regression-loop-app-20260717-real`
- App: `promptiter-regression-loop-app`
- Mode: `real_llm`
- Data source: `real LLM via OpenAI-compatible endpoint; evalsets remain local reproducible fixtures`
- Decision: **REJECT**
- Target surface: `travel-support#instruction`
- Engine: `real-llm` (`deepseek-chat`)

## Score Summary

| Split | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| Train | 1.0000 | 1.0000 | 0.0000 |
| Validation | 0.6667 | 0.6667 | 0.0000 |

## Gate Decision

- validation score gain 0.0000 is below threshold 0.0500

## Validation Case Delta

| Case | Critical | Baseline | Candidate | Delta | Transition |
|---|---:|---:|---:|---:|---|
| `val_json_refund` | false | 0.0000 | 0.0000 | 0.0000 | stayed_fail |
| `val_weather_berlin` | false | 1.0000 | 1.0000 | 0.0000 | stayed_pass |
| `val_critical_direct_status` | true | 1.0000 | 1.0000 | 0.0000 | stayed_pass |

## Validation Output Evidence

| Case | Baseline actual | Baseline tools | Candidate actual | Candidate tools |
|---|---|---:|---|---:|
| `val_json_refund` | I don't need weather data for this task. Let me provide the requested JSON.  ```json {   "id": "r-204",   "status": "approved",   "amount": ... | 1 | I don't need weather data for this task. Let me provide the requested JSON.  ```json {   "id": "r-204",   "status": "approved",   "amount": ... | 1 |
| `val_weather_berlin` | The weather in Berlin today is cloudy with a temperature of 8°C. | 1 | The weather in Berlin today is cloudy with a temperature of 8°C. | 1 |
| `val_critical_direct_status` | TR900 is boarding at gate K12. | 0 | TR900 is boarding at gate K12. | 0 |

## Failure Attribution

### Train

- none

### Validation

- `tool_call_error`: 1
- `unknown`: 1


## Audit Summary

- Candidate: `promptiter-real-round-1`
- Calls: `12`
- Estimated cost: `$0.000127`
- Duration: `53966 ms`
- Seed: `20260717`

The candidate is not automatically safe to publish unless the gate decision is ACCEPT.
