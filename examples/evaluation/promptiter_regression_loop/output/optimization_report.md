# Optimization Report

Decision: **ACCEPT**

App: `promptiter-regression-loop-app`

Seed: `20260708`

Runner: `fake`

Duration: `0ms`

## Score Summary

| Phase | Train | Validation |
| --- | ---: | ---: |
| Baseline | 0.40 | 0.65 |
| Candidate 1 | 0.90 | 0.86 |
| Candidate 2 | 0.43 | 0.66 |
| Candidate 3 | 0.98 | 0.53 |

## Gate Decision

- Accepted: `true`
- Validation score delta: `0.21`
- Reason: candidate satisfies all acceptance gates

## Validation Delta

| Case | Baseline | Candidate | Delta | Transition | New hard fail | Critical regression |
| --- | ---: | ---: | ---: | --- | --- | --- |
| val_json_receipt | 0.80 | 0.85 | 0.05 | `improved` | `false` | `false` |
| val_route_skill | 0.65 | 0.82 | 0.17 | `newly_passed` | `false` | `false` |
| val_tool_selection | 0.50 | 0.92 | 0.42 | `newly_passed` | `false` | `false` |

## Failure Attribution

- `format_error`: 3
- `knowledge_recall_insufficient`: 2
- `routing_error`: 3
- `tool_argument_error`: 2
- `tool_selection_error`: 2

## Candidate Rounds

### Round 1

- Accepted: `true`
- Prompt:

```text
SUCCESS_PROMPT: call tools only when needed, preserve final answer format, and verify numeric facts.
```
- Gate reason: candidate satisfies all acceptance gates

### Round 2

- Accepted: `false`
- Prompt:

```text
INEFFECTIVE_PROMPT: be helpful and concise.
```
- Gate reason: validation score gain 0.0067 is below threshold 0.0500

### Round 3

- Accepted: `false`
- Prompt:

```text
OVERFIT_PROMPT: optimize train cases aggressively even if validation formatting changes.
```
- Gate reason: validation score gain -0.1167 is below threshold 0.0500
- Gate reason: candidate introduced 1 new hard fail(s)
- Gate reason: candidate regressed 1 critical case(s)

## Cost and Latency

- Calls: `24`
- Estimated cost: `0.0240`
- Total latency: `120ms`

## Artifacts

- `promptiter_regression_loop/output/optimization_report.json`
- `promptiter_regression_loop/output/optimization_report.md`
