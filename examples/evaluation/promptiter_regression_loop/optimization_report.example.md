# PromptIter Regression-Loop Optimization Report

- Generated at: 2026-07-25T10:02:50Z
- App: `promptiter-regression-loop-app`
- Model source: `fake`
- Target surface: `candidate#instruction`

## Decision: REJECT

- Engine accepted candidate: **true**
- Acceptance gate accepted candidate: **false**

  - rejected: KEY validation case(s) [val_resolved_freeform_KEY] regressed pass→fail (overfitting)

## Cost & latency

- Total wall-clock: 146 ms
- Baseline eval: 27 ms · candidate eval: 29 ms
- Candidate model calls: 33
- Note: fake mode: no monetary token cost; candidate model calls and latency are the cost proxy

## Prompts

**Baseline instruction:**

```
You classify a customer support message. Reply with the category.
```

**Candidate instruction:**

```
STRICT OUTPUT: read the support message and reply with exactly one line 'category: <billing|account|shipping>' and nothing else.
```

## Gate criteria (validation)

| Criterion | Passed | Detail |
|---|---|---|
| validation_improves | ✅ | validation mean 0.333 → 0.667 (gain +0.333, required ≥ 0.010) |
| no_new_hard_fail | ❌ | 1 case(s) regressed pass→fail: [val_resolved_freeform_KEY] |
| key_cases_protected | ❌ | KEY case(s) [val_resolved_freeform_KEY] regressed pass→fail |
| within_budget | ✅ | no budget configured (candidate model calls: 33) |

## Validation per-case delta

| Case | Class | Baseline | Candidate | Δ |
|---|---|---|---|---|
| val_refund_billing | new_pass | 0.000 | 1.000 | +1.000 |
| val_resolved_freeform_KEY | new_fail | 1.000 | 0.000 | -1.000 |
| val_parcel_shipping | new_pass | 0.000 | 1.000 | +1.000 |

## Train per-case delta

| Case | Class | Baseline | Candidate | Δ |
|---|---|---|---|---|
| train_refund_billing | new_pass | 0.000 | 1.000 | +1.000 |
| train_login_account | new_pass | 0.000 | 1.000 | +1.000 |
| train_package_shipping | unchanged | 0.000 | 0.000 | +0.000 |

## Baseline — validation (mean 0.333, failed)

| Case | Passed | Score | Failure category |
|---|---|---|---|
| val_refund_billing | ❌ | 0.000 | response_mismatch |
| val_resolved_freeform_KEY | ✅ | 1.000 |  |
| val_parcel_shipping | ❌ | 0.000 | response_mismatch |

Failure attribution: response_mismatch=2

## Candidate — validation (mean 0.667, failed)

| Case | Passed | Score | Failure category |
|---|---|---|---|
| val_refund_billing | ✅ | 1.000 |  |
| val_resolved_freeform_KEY | ❌ | 0.000 | response_mismatch |
| val_parcel_shipping | ✅ | 1.000 |  |

Failure attribution: response_mismatch=1

## Engine optimization rounds

| Round | Train | Validation | Accepted | Δ | Stop | Reason |
|---|---|---|---|---|---|---|
| 1 | 0.000 | 0.667 | true | +0.333 | false | continue optimization |
| 2 | 0.667 | 0.667 | false | +0.000 | false | continue optimization |
| 3 | 0.667 | 0.667 | false | +0.000 | true | max rounds reached |

## Run configuration

- Model source: `fake`
- Max rounds: 3 · engine min-score-gain: 0.010 · gate min-validation-gain: 0.010 · target score: 1.000
- Max candidate model calls (budget): 0 (0 = disabled)
- Key cases: [val_resolved_freeform_KEY]
- Baseline prompt file: `./data/promptiter-regression-loop-app/baseline-prompt.txt`
- Fake fixture file: `fixtures/regression-loop.fake.json`
- Determinism: deterministic=true, seed=none (deterministic) (scripted fake model + deterministic collaborators; no RNG)

