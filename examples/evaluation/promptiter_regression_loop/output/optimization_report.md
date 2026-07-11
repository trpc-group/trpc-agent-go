# Optimization Audit Report

**Generated**: 2026-07-11T15:36:44Z

## Score Summary

| | Overall Score | Pass | Fail |
|---|---|---|---|
| **Baseline** | 0.6500 | 1 | 2 |
| **Candidate** | 0.8200 | 3 | 0 |

## Gate Decision

**Result: ACCEPTED** — accepted: score improved by 0.1700

### Rules

- ✅ **min_score_gain**: score delta 0.1700 (threshold 0.0200)
- ✅ **no_new_hard_failures**: 0 new failure(s)
- ✅ **critical_cases_no_regression**: 0 critical case(s) regressed
- ✅ **cost_budget**: cost 0.0000 (budget 10.0000)
- ✅ **no_overfitting**: train delta 0.2500, val delta 0.1700

## Failure Attribution

| Category | Count | Cases |
|---|---|---|
| tool_call_error | 1 | train/c1 |
| tool_argument_error | 1 | train/c3 |

## Per-Case Deltas

| Case | Baseline | Candidate | Delta | Status |
|---|---|---|---|---|
| train/c2 | 0.9000 | 0.8800 | -0.0200 |  |
| train/c3 | 0.5500 | 0.7300 | +0.1800 | 🟢 NEW PASS |
| train/c1 | 0.5000 | 0.8500 | +0.3500 | 🟢 NEW PASS |

## Round History

### Round 1 (accepted)

- Train score: 0.7500
- Validation score: 0.7800
- Patches applied:
  - `agent/instruction`: Fix tool call errors

### Round 2 (accepted)

- Train score: 0.8200
- Validation score: 0.8200
- Patches applied:
  - `agent/instruction`: Fix argument errors

## Cost & Latency

- Total tokens: 15000
- Estimated cost: $0.1500
- Total latency: 45000 ms
- Rounds run: 2

## Recommendation

The candidate prompt **should be accepted**. It improves overall quality without introducing regressions or overfitting.
