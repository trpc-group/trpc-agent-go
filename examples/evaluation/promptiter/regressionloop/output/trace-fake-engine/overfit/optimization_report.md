# Optimization Report

- App: `eval-optimization-regression-app`
- Decision: `false`
- Baseline validation score: `0.333`
- Accepted validation score: `0.500`
- Candidate validation score: `0.500`
- Validation delta: `+0.167`
- Cost: `6` model calls (estimated), source `model_call_estimate`
- Latency: `150ms`

> `Accepted validation` is the last profile accepted by PromptIter. `Candidate validation` is the final audited candidate, even when PromptIter or the outer gate rejects it.

## Gate Decision

- PromptIter accepted a candidate profile
- validation score gain 0.167 >= threshold 0.050
- 1 newly failed hard validation metrics: [val_overfit_refund_policy/final_response]
- critical cases regressed: [val_overfit_refund_policy]
- model calls 6 within budget 10

## Delta Summary

| newly passed | newly failed | score up | score down | unchanged |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1 | 1 | 0 | 0 |

## Case Delta

| eval set | case | metric | baseline | candidate | delta | kind |
| --- | --- | --- | ---: | ---: | ---: | --- |
| validation | val_optimization_ineffective | json_format | 0.000 | 0.500 | +0.500 | score_up |
| validation | val_overfit_refund_policy | final_response | 1.000 | 0.000 | -1.000 | newly_failed |
| validation | val_prompt_fix_generalizes | tool_trajectory | 0.000 | 1.000 | +1.000 | newly_passed |

## Failure Attribution

Baseline failures: `5`; candidate failures: `2`; combined: `7`.

### Baseline

| category | count | secondary |
| --- | ---: | ---: |
| format_error | 1 | 0 |
| knowledge_recall_gap | 1 | 0 |
| route_error | 1 | 0 |
| tool_argument_error | 1 | 0 |
| tool_call_error | 1 | 1 |

### Candidate

| category | count | secondary |
| --- | ---: | ---: |
| final_response_mismatch | 1 | 0 |
| format_error | 1 | 0 |

### Combined

| category | count | secondary |
| --- | ---: | ---: |
| final_response_mismatch | 1 | 3 |
| format_error | 2 | 0 |
| knowledge_recall_gap | 1 | 0 |
| route_error | 1 | 0 |
| tool_argument_error | 1 | 0 |
| tool_call_error | 1 | 1 |

### Failure Details

| eval set | case | metric | category | secondary | reason | evidence |
| --- | --- | --- | --- | --- | --- | --- |
| train | train_optimization_ineffective | router_decision | route_error | final_response_mismatch | route error: selected general_support instead of routing_helper | metric=router_decision status=failed score=0.000; metric_reason=route error: selected general_support instead of routing_helper; trace_status=completed; final_output=trace outpu... |
| train | train_overfit_success | knowledge_recall | knowledge_recall_gap | final_response_mismatch | knowledge recall gap: refund policy date omitted | metric=knowledge_recall status=failed score=0.000; metric_reason=knowledge recall gap: refund policy date omitted; trace_status=completed; final_output=trace output for train_ov... |
| train | train_prompt_fix_success | tool_trajectory | tool_argument_error | tool_call_error,final_response_mismatch | tool argument parameter mismatch: invoice_id was omitted | metric=tool_trajectory status=failed score=0.000; metric_reason=tool argument parameter mismatch: invoice_id was omitted; trace_status=completed; final_output=trace output for t... |
| validation | val_optimization_ineffective | json_format | format_error |  | format error: response uses prose instead of JSON | metric=json_format status=failed score=0.000; metric_reason=format error: response uses prose instead of JSON; trace_status=completed; final_output=trace output for val_optimiza... |
| validation | val_prompt_fix_generalizes | tool_trajectory | tool_call_error |  | missing tool call: invoice question was not sent to billing_lookup | metric=tool_trajectory status=failed score=0.000; metric_reason=missing tool call: invoice question was not sent to billing_lookup; trace_status=completed; final_output=trace ou... |
| validation | val_optimization_ineffective | json_format | format_error |  | partial JSON improvement but still fails schema | metric=json_format status=failed score=0.500; metric_reason=partial JSON improvement but still fails schema; trace_status=completed; final_output=trace output for val_optimizati... |
| validation | val_overfit_refund_policy | final_response | final_response_mismatch |  | final response mismatch: candidate overfits train date and drops 2026-08-31 | metric=final_response status=failed score=0.000; metric_reason=final response mismatch: candidate overfits train date and drops 2026-08-31; trace_status=completed; final_output=... |

## Candidate Prompt Rejected By Gate

```text
OVERFIT_PROMPT: optimize train failures aggressively, even if validation policy dates change.
```
