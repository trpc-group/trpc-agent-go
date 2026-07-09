# Optimization Report: eval-optimization-app

**Verdict: REJECTED — candidate not worth accepting**

- mode: `fake`
- status: `succeeded`

## Score

| phase | overall score |
|---|---|
| baseline | 0.333 |
| candidate | 0.667 |
| gain | +0.333 |

## Delta

- newly passed: **2**
- newly failed: **1**
- score up: 0
- score down: 0
- unchanged: 0

## Failure Attribution (baseline)

- responseMismatch: 2

Terminal-loss severity (training signal): unknown=3

## Release Gate

- released: **false**
  - total gain 0.333 >= threshold 0.200
  - 1 newly failed cases
  - rounds 2 within budget 4

## Cost (estimated)

- rounds: 2
- teacher calls: 15
- note: teacher calls derived from evaluated cases across baseline and rounds
