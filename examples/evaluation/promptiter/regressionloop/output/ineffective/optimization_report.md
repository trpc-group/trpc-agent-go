# Optimization Report: eval-optimization-app

**Verdict: REJECTED — candidate not worth accepting**

- mode: `fake`
- status: `succeeded`

## Score

| phase | overall score |
|---|---|
| baseline | 0.000 |
| candidate | 0.000 |
| gain | +0.000 |

## Delta

- newly passed: **0**
- newly failed: **0**
- score up: 0
- score down: 0
- unchanged: 3

## Failure Attribution (baseline)

- responseMismatch: 3

Terminal-loss severity (training signal): unknown=6

## Release Gate

- released: **false**
  - no candidate profile was accepted by the engine

## Cost (estimated)

- rounds: 2
- evaluated cases: 15
- duration: 0 ms
- model calls: 25
  - aggregator: 2
  - backwarder: 6
  - candidate: 15
  - optimizer: 2
- note: evaluated cases is a case count; model calls are counted per role, distinct from cases
