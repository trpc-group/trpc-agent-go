# Optimization Report: eval-optimization-app

**Verdict: RELEASED — candidate improves quality and passes the gate**

- mode: `fake`
- status: `succeeded`

## Score

| phase | overall score |
|---|---|
| baseline | 0.000 |
| candidate | 1.000 |
| gain | +1.000 |

## Delta

- newly passed: **3**
- newly failed: **0**
- score up: 0
- score down: 0
- unchanged: 0

## Failure Attribution (baseline)

- responseMismatch: 3

Terminal-loss severity (training signal): unknown=3

## Release Gate

- released: **true**
  - total gain 1.000 >= threshold 0.500
  - no newly failed cases
  - rounds 1 within budget 4

## Cost (estimated)

- rounds: 1
- teacher calls: 9
- note: teacher calls derived from evaluated cases across baseline and rounds
