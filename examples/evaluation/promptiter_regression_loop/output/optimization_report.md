# Prompt Optimization Report

- Status: **accepted**
- Decision: candidate accepted by regression gate
- Seed: 2003
- Runtime: fake (deterministic-ping-v1)

## Baseline

| Dataset | Score | Passed |
| --- | ---: | :---: |
| Train | 0.666667 | false |
| Validation | 0.666667 | false |

### Train failures

Failure attribution:
- final_response_mismatch: 2
  - train-case-2: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match
  - train-case-3: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match

### Validation failures

Failure attribution:
- final_response_mismatch: 2
  - validation-case-2: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match
  - validation-case-3: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match

## Optimization Rounds

### Round 1

- Gate: **rejected**
- Candidate prompt:

```text
Rephrase without changing behavior. fixture:ineffective
```
  - validation score gain 0.000000 is below required 0.300000
- Train score: 0.666667 (+0.000000)
- Validation score: 0.666667 (+0.000000)
- Evaluation cost: 12 calls, 60 tokens, 24 ms
- Optimization cost: 22 calls, 110 tokens, 44 ms
- Round total: 34 calls, 170 tokens, 68 ms

| Case | Change | Baseline | Candidate | Delta |
| --- | --- | ---: | ---: | ---: |

Failure attribution:
- final_response_mismatch: 2
  - validation-case-2: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match
  - validation-case-3: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match

### Round 2

- Gate: **rejected**
- Candidate prompt:

```text
Optimize only the training cases. fixture:train-only
```
  - validation score gain -0.166667 is below required 0.300000
  - hard metric "quality" newly failed in case "validation-case-1"
  - metric "quality" in case "validation-case-1" dropped 1.000000
- Train score: 1.000000 (+0.333333)
- Validation score: 0.500000 (-0.166667)
- Evaluation cost: 12 calls, 60 tokens, 24 ms
- Optimization cost: 22 calls, 110 tokens, 44 ms
- Round total: 34 calls, 170 tokens, 68 ms

| Case | Change | Baseline | Candidate | Delta |
| --- | --- | ---: | ---: | ---: |
| validation-case-1 | newly_failed | 1.000000 | 0.500000 | -0.500000 |

Changed metrics:

| Case | Metric | Change | Baseline | Candidate | Delta |
| --- | --- | --- | ---: | ---: | ---: |
| validation-case-1 | quality | newly_failed | 1.000000 | 0.000000 | -1.000000 |

Failure attribution:
- final_response_mismatch: 3
  - validation-case-1: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match
  - validation-case-2: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match
  - validation-case-3: final_response_mismatch — quality: final response mismatch: text mismatch: source not-pong and target pong do not match

### Round 3

- Gate: **accepted**
- Candidate prompt:

```text
Reply with pong for every valid ping request. fixture:balanced
```
- Train score: 1.000000 (+0.333333)
- Validation score: 1.000000 (+0.333333)
- Evaluation cost: 12 calls, 60 tokens, 24 ms
- Optimization cost: 22 calls, 110 tokens, 44 ms
- Round total: 34 calls, 170 tokens, 68 ms

| Case | Change | Baseline | Candidate | Delta |
| --- | --- | ---: | ---: | ---: |
| validation-case-2 | newly_passed | 0.500000 | 1.000000 | +0.500000 |
| validation-case-3 | newly_passed | 0.500000 | 1.000000 | +0.500000 |

Changed metrics:

| Case | Metric | Change | Baseline | Candidate | Delta |
| --- | --- | --- | ---: | ---: | ---: |
| validation-case-2 | quality | newly_passed | 0.000000 | 1.000000 | +1.000000 |
| validation-case-3 | quality | newly_passed | 0.000000 | 1.000000 | +1.000000 |

## Cost

- Model calls: 114
- Tokens: 570
- Latency: 228 ms
