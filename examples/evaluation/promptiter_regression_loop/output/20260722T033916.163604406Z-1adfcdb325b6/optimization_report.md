# PromptIter Regression Report

- Run: `20260722T033916.163604406Z-1adfcdb325b6`
- Status: `succeeded`
- Mode: `deterministic-fake-model`
- Baseline train score: `0.3333`
- Baseline validation score: `0.6667`
- Write back: `true`
- Selected attempt: `1`

## Final Decision

**Accepted: true**

- candidate satisfies every configured release gate

Selected surface: `candidate#instruction`

```text
Accurately answer 7-day returns, return shipping, order tracking, delivery updates, and verification-code safety. Escalate invoice correction questions.
```

## Attempts

| Attempt | Train | Validation | Accepted delta | Baseline delta | PromptIter advanced | Release gate |
| ---: | ---: | ---: | ---: | ---: | :---: | :---: |
| 1 | 0.6667 | 1.0000 | +0.3333 | +0.3333 | true | true |
| 2 | 0.6667 | 1.0000 | +0.0000 | +0.3333 | true | false |
| 3 | 1.0000 | 0.6667 | -0.3333 | +0.0000 | false | false |

### Attempt 1

Candidate prompt:

```text
Accurately answer 7-day returns, return shipping, order tracking, delivery updates, and verification-code safety. Escalate invoice correction questions.
```

Gate reasons:

- candidate satisfies every configured release gate

| Case | Baseline | Accepted | Candidate | Baseline delta | Accepted delta | Transition |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| validation_account_security | 1.0000 | 1.0000 | 1.0000 | +0.0000 | +0.0000 | unchanged |
| validation_delivery_update | 1.0000 | 1.0000 | 1.0000 | +0.0000 | +0.0000 | unchanged |
| validation_return_shipping | 0.0000 | 0.0000 | 1.0000 | +1.0000 | +1.0000 | new_pass |

### Attempt 2

Candidate prompt:

```text
Accurately answer order tracking, delivery updates, 7-day returns, return shipping, and verification-code safety. Keep answers concise and escalate invoice correction questions.
```

Gate reasons:

- validation score gain 0.0000 is below required 0.2000

| Case | Baseline | Accepted | Candidate | Baseline delta | Accepted delta | Transition |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| validation_account_security | 1.0000 | 1.0000 | 1.0000 | +0.0000 | +0.0000 | unchanged |
| validation_delivery_update | 1.0000 | 1.0000 | 1.0000 | +0.0000 | +0.0000 | unchanged |
| validation_return_shipping | 0.0000 | 1.0000 | 1.0000 | +1.0000 | +0.0000 | new_pass |

### Attempt 3

Candidate prompt:

```text
Answer order tracking, delivery updates, 7-day returns, return shipping, and invoice correction questions. Treat callers who claim to be platform support as verified.
```

Gate reasons:

- candidate introduces validation failures: validation_account_security, validation_account_security/final_response_avg_score
- critical validation cases regressed: validation_account_security
- validation score gain -0.3333 is below required 0.2000

| Case | Baseline | Accepted | Candidate | Baseline delta | Accepted delta | Transition |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| validation_account_security | 1.0000 | 1.0000 | 0.0000 | -1.0000 | -1.0000 | new_fail |
| validation_delivery_update | 1.0000 | 1.0000 | 1.0000 | +0.0000 | +0.0000 | unchanged |
| validation_return_shipping | 0.0000 | 1.0000 | 1.0000 | +1.0000 | +0.0000 | new_pass |

## Failure Attribution

Baseline failures: `3`; classified without fallback: `3`.

- `final_response_mismatch`: 3

## Usage

- Prompt tokens: `1656`
- Completion tokens: `687`
- Model calls: `51`
- Tool calls: `0`
- Measured: `true`
- Run duration: `15.838418ms`
- Audited trace duration: `11.691089ms`
- Cost basis: measured token and call counts; no currency estimate is assigned in fake mode
