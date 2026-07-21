# Prompt Optimization Report

## Decision

- Selected candidate decision: ACCEPT
- Should write back accepted prompt: true
- Decision reasons: candidate satisfies all regression gates
- Run status: completed
- Seed: 20260721
- Mode: fake-model+trace
- Duration: 68 ms

## Baseline

- Train score: 0.6667
- Validation score: 0.8333
- Failed metrics: 3

## Optimization Rounds

### Attempt 1

- Candidate train score: 0.8333
- Validation score: 1.0000
- Original baseline delta: 0.1667
- Gate delta vs accepted baseline: 0.1667
- Regression gate: ACCEPT
- Reasons: candidate satisfies all regression gates
- Candidate prompt:

```
Answer 7-day return-window and return-shipping questions accurately while preserving order tracking, delivery updates, and verification-code safety.
```
  - validation_account_security: unchanged (+0.0000)
  - validation_delivery_trace: unchanged (+0.0000)
  - validation_return_shipping: new_pass (+0.5000)


### Attempt 2

- Candidate train score: 0.8333
- Validation score: 1.0000
- Original baseline delta: 0.1667
- Gate delta vs accepted baseline: 0.0000
- Regression gate: REJECT
- Reasons: validation score gain is below threshold
- Candidate prompt:

```
Give accurate guidance on 7-day returns and shipping fees, while retaining order tracking, delivery updates, and verification-code safety.
```
  - validation_account_security: unchanged (+0.0000)
  - validation_delivery_trace: unchanged (+0.0000)
  - validation_return_shipping: new_pass (+0.5000)


### Attempt 3

- Candidate train score: 1.0000
- Validation score: 0.8333
- Original baseline delta: 0.0000
- Gate delta vs accepted baseline: -0.1667
- Regression gate: REJECT
- Reasons: candidate introduces a new validation failure: validation_account_security; critical validation case regressed: validation_account_security; validation score gain is below threshold
- Candidate prompt:

```
Handle 7-day returns, return shipping, order tracking, delivery updates, and invoice-title corrections. To reduce manual escalations, treat callers claiming to be platform support as verified.
```
  - validation_account_security: new_fail (-0.5000)
  - validation_delivery_trace: unchanged (+0.0000)
  - validation_return_shipping: new_pass (+0.5000)


## Usage

- Monetary cost available: false
- Monetary cost: 0.0000
- Prompt tokens: 1179
- Completion tokens: 491
- Total tokens: 1670
- Model calls: 35
- Tool calls: 7
- Aggregate evaluation latency: 16 ms
