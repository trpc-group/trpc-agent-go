# Prompt Optimization Report

- Run: `fake-2aeef6b62d700ac4`
- Decision: **ACCEPT**
- Candidate: `balanced-routing-and-format` (round 1)
- Engine: `evaluation-service+promptiter-deterministic`; model: `deterministic-promptiter-v1`; seed: `20260724`
- Train score: `0.1667` -> `0.6667`
- Validation score: `0.5000` -> `1.0000` (`+0.5000`)

## Gate Decision

- all acceptance checks passed

| Check | Passed | Detail |
| --- | --- | --- |
| minimum_validation_gain | true | validation score gain 0.5000 must be at least 0.0500 |
| no_new_hard_fails | true | candidate introduced 0 new hard fail(s); allowNewHardFails=false |
| critical_cases | true | 1 critical case(s) stayed within the 0.0000 score-drop limit |
| cost_budget | true | candidate cost budget: estimated $0.000072, limit $0.010000 |
| tool_call_budget | true | candidate tool-call budget: used 1, limit 2 |

## Validation Delta

| Case | Baseline | Candidate | Delta | Classification |
| --- | ---: | ---: | ---: | --- |
| validation_direct_critical | 1.0000 | 1.0000 | +0.0000 | unchanged |
| validation_lookup_exchange_rate | 0.0000 | 1.0000 | +1.0000 | newly_passed |
| validation_structured_profile | 0.5000 | 1.0000 | +0.5000 | newly_passed |

Newly passed: **2**. Newly failed: **0**. Improved: **0**. Regressed: **0**.

## Optimization Rounds

| Round | Candidate | Train | Validation | Delta | Decision | Reasons |
| ---: | --- | ---: | ---: | ---: | --- | --- |
| 1 | balanced-routing-and-format | 0.6667 | 1.0000 | +0.5000 | accepted | all acceptance checks passed |
| 2 | knowledge-overfit | 0.8333 | 0.6667 | -0.3333 | rejected | validation score gain -0.3333 must be at least 0.0500; candidate introduced 2 new hard fail(s); allowNewHardFails=false; critical case "validation_direct_critical" score dropped by 0.5000, limit 0.0000; candidate tool-call budget: used 3, limit 2 |

## Failure Attribution

| Category | Baseline | Candidate |
| --- | ---: | ---: |
| format_error | 2 | 0 |
| knowledge_recall_insufficient | 1 | 1 |
| route_error | 2 | 0 |

## Cost and Latency

Total: 18 model calls, 8 tool calls, 1831 tokens, estimated cost `$0.000439`, latency `72 ms`.

## Recommended Prompt

```text
Answer each request concisely using only the provided task payload.

Use lookup tools only for lookup tasks. Emit strict JSON for structured tasks. Preserve direct answers without tools.
[use_lookup_for_lookup_tasks]
[emit_json_for_structured_tasks]
```
