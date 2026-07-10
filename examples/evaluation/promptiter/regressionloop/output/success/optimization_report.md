# Optimization Report

- App: `eval-optimization-regression-app`
- Decision: `true`
- Baseline validation score: `0.333`
- Accepted validation score: `1.000`
- Candidate validation score: `1.000`
- Validation delta: `+0.667`
- Cost: `6` model calls (estimated), source `model_call_estimate`
- Latency: `150ms`

> `Accepted validation` is the last profile accepted by PromptIter. `Candidate validation` is the final audited candidate, even when PromptIter or the outer gate rejects it.

## Gate Decision

- PromptIter accepted a candidate profile
- validation score gain 0.667 >= threshold 0.050
- no newly failed validation metrics
- critical cases did not regress
- model calls 6 within budget 10

## Delta Summary

| newly passed | newly failed | score up | score down | unchanged |
| ---: | ---: | ---: | ---: | ---: |
| 2 | 0 | 0 | 0 | 1 |

## Case Delta

| eval set | case | metric | baseline | candidate | delta | kind |
| --- | --- | --- | ---: | ---: | ---: | --- |
| validation | val_optimization_ineffective | json_format | 0.000 | 1.000 | +1.000 | newly_passed |
| validation | val_overfit_refund_policy | final_response | 1.000 | 1.000 | +0.000 | unchanged |
| validation | val_prompt_fix_generalizes | tool_trajectory | 0.000 | 1.000 | +1.000 | newly_passed |

## Failure Attribution

Baseline failures: `5`; candidate failures: `0`; combined: `5`.

### Baseline

| category | count | secondary |
| --- | ---: | ---: |
| format_error | 1 | 0 |
| knowledge_recall_gap | 1 | 0 |
| route_error | 1 | 0 |
| tool_argument_error | 1 | 0 |
| tool_call_error | 1 | 1 |

### Combined

| category | count | secondary |
| --- | ---: | ---: |
| format_error | 1 | 0 |
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

## Accepted Prompt

```text
Answer support questions briefly.

```
