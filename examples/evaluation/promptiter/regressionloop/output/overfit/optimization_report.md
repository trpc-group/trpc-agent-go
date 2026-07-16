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
  - model calls 20 within budget 100

## Accepted candidate

- `candidate#instruction`: USE_STRUCTURED_RECAP 严格按 "<胜队>以<比分>战胜<负队>。" 的格式输出简体中文战报，只输出一句话。

## Cost (estimated)

- rounds: 2
- evaluated cases: 15
- duration: 0 ms
- model calls: 20
  - aggregator: 1
  - backwarder: 3
  - candidate: 15
  - optimizer: 1
- note: evaluated cases is a case count; model calls are counted per role, distinct from cases
