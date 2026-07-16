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
  - model calls 14 within budget 100

## Accepted candidate

- `candidate#instruction`: USE_STRUCTURED_RECAP 严格按 "<胜队>以<比分>战胜<负队>。" 的格式输出简体中文战报，只输出一句话。

## Cost (estimated)

- rounds: 1
- evaluated cases: 9
- duration: 0 ms
- model calls: 14
  - aggregator: 1
  - backwarder: 3
  - candidate: 9
  - optimizer: 1
- note: evaluated cases is a case count; model calls are counted per role, distinct from cases
