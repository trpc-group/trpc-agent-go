# PromptIter Regression Report

- Run: `sample-c30a7d2ed68a`
- Status: `succeeded`
- Mode: `deterministic-fake-model`
- Seed: `2003`
- Config SHA-256: `1adfcdb325b676d37218eaa974238526701e278f193f216771167b0e549895c3`
- Input SHA-256: `c30a7d2ed68a044ee8504f493908a6b9d5399201e2bc23894fd914b9b6ef2685`
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
| validation_return_shipping | 0.0000 | 1.0000 | 1.0000 | +1.0000 | +0.0000 | unchanged |

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
| validation_return_shipping | 0.0000 | 1.0000 | 1.0000 | +1.0000 | +0.0000 | unchanged |

## Failure Attribution

Baseline failures: `3`; classified without fallback: `3`.

- `final_response_mismatch`: 3

## Usage

- Prompt tokens: `1656`
- Completion tokens: `687`
- Model calls: `51`
- Tool calls: `0`
- Measured: `true`
- Run duration: `0s`
- Audited trace duration: `0s`
- Cost basis: measured token and call counts; no currency estimate is assigned in fake mode
