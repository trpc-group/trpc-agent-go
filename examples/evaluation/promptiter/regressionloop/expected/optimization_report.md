# PromptIter Regression Report

Mode: `fake` (`fake-deterministic`)

Seed: `42`

Duration: `0.250s`

## Baseline

Train score: **0.4444**

Validation score: **0.7778**

## Candidate Decisions

### Round 1: ACCEPT

PromptIter accepted: `true`; release accepted: `true`.

Train: `0.6667`; validation: `1.0000`.

Reasons: all_release_gate_checks_passed

Validation score delta vs initial: `+0.2222`; vs search input: `+0.2222`; vs last released: `+0.2222`.

Validation comparison against last released profile:

| Case | Baseline | Candidate | Delta | Transition |
|---|---:|---:|---:|---|
| val-01 | 0.6667 | 1.0000 | +0.3333 | newly_passed |
| val-02 | 1.0000 | 1.0000 | +0.0000 | unchanged_pass |
| val-03 | 0.6667 | 1.0000 | +0.3333 | newly_passed |

Resource comparison against last released profile:

| Set | Side | Model calls | Tool calls | Case runs | Latency | Cost |
|---|---|---:|---:|---:|---:|---:|
| Train | Last released | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Train | Candidate | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Train | Delta | +0 | +0 | +0 | +0.0000s | +0.000000 |
| Validation | Last released | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Validation | Candidate | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Validation | Delta | +0 | +0 | +0 | +0.0000s | +0.000000 |

### Round 2: REJECT

PromptIter accepted: `true`; release accepted: `false`.

Train: `0.8889`; validation: `1.0000`.

Reasons: min_validation_gain_not_met, no_generalization

Validation score delta vs initial: `+0.2222`; vs search input: `+0.0000`; vs last released: `+0.0000`.

Validation comparison against last released profile:

| Case | Baseline | Candidate | Delta | Transition |
|---|---:|---:|---:|---|
| val-01 | 1.0000 | 1.0000 | +0.0000 | unchanged_pass |
| val-02 | 1.0000 | 1.0000 | +0.0000 | unchanged_pass |
| val-03 | 1.0000 | 1.0000 | +0.0000 | unchanged_pass |

Resource comparison against last released profile:

| Set | Side | Model calls | Tool calls | Case runs | Latency | Cost |
|---|---|---:|---:|---:|---:|---:|
| Train | Last released | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Train | Candidate | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Train | Delta | +0 | +0 | +0 | +0.0000s | +0.000000 |
| Validation | Last released | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Validation | Candidate | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Validation | Delta | +0 | +0 | +0 | +0.0000s | +0.000000 |

### Round 3: REJECT

PromptIter accepted: `true`; release accepted: `false`.

Train: `1.0000`; validation: `0.8889`.

Reasons: min_validation_gain_not_met, new_hard_failure, overfitting, validation_regression, critical_case_regression:val-03

Validation score delta vs initial: `+0.1111`; vs search input: `-0.1111`; vs last released: `-0.1111`.

Validation comparison against last released profile:

| Case | Baseline | Candidate | Delta | Transition |
|---|---:|---:|---:|---|
| val-01 | 1.0000 | 1.0000 | +0.0000 | unchanged_pass |
| val-02 | 1.0000 | 1.0000 | +0.0000 | unchanged_pass |
| val-03 | 1.0000 | 0.6667 | -0.3333 | newly_failed |

Resource comparison against last released profile:

| Set | Side | Model calls | Tool calls | Case runs | Latency | Cost |
|---|---|---:|---:|---:|---:|---:|
| Train | Last released | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Train | Candidate | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Train | Delta | +0 | +0 | +0 | +0.0000s | +0.000000 |
| Validation | Last released | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Validation | Candidate | 4 | 1 | 3 | 0.0570s | 0.004200 |
| Validation | Delta | +0 | +0 | +0 | +0.0000s | +0.000000 |

## Failure Attribution Summary

| Evaluation | Category | Count |
|---|---|---:|
| Baseline train | final_response_mismatch | 3 |
| Baseline train | format_error | 1 |
| Baseline train | tool_argument_error | 1 |
| Baseline validation | final_response_mismatch | 1 |
| Baseline validation | tool_argument_error | 1 |
| Round 1 train | final_response_mismatch | 2 |
| Round 1 train | format_error | 1 |
| Round 1 validation | none | 0 |
| Round 2 train | final_response_mismatch | 1 |
| Round 2 validation | none | 0 |
| Round 3 train | none | 0 |
| Round 3 validation | tool_argument_error | 1 |

## Write-Back Decision

Recommended: **true**

Performed: **false**

Accepted profile: `round_1/candidate_profile.json`

## Usage and Cost

Evaluation case runs: `36`; model calls: `51`; tool calls: `12`; retries: `0`.

Estimated cost: `0.0534 USD` (fake-model).
