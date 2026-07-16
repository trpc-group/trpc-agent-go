# Prompt optimization report: artifact-check

- Status: `succeeded`
- Decision: `accepted`
- Release authority: `Regression release gate`
- Selected candidate: `round-4-69d05e55c2ce`
- Input fingerprint: `a91654fedc16f818f53fc7f4bf0da6f710ee1d368f484f55ec7573e169a0c361`
- Random seed: `not applied` (configured value: `7`)
- Audit runs: `2`
- Deterministic runtime: `true`
- Started: `2026-07-16 19:11:10.699 UTC`
- Finished: `2026-07-16 19:11:10.702 UTC`
- Duration: `3.000 ms`

## PromptIter execution

- Evaluation runs: `2`
- Trace usage covers all Evaluation calls: `true`
- Hard round limit: `4`
- Acceptance minimum score gain: `0.01`
- Stop after consecutive unaccepted rounds: `disabled`
- Early-stop target score: `0.95`
- Target surfaces: `support-agent#instruction`
- Evaluation parallelism: cases=`0`, inference=`false`, evaluation=`false`
- Stage parallelism: backward=`false/0`, aggregation=`false/0`, optimizer=`false/0`

## Runtime metadata

- engine: `promptiter-engine`
- maxRounds: `4`
- model: `fake-no-api-key`
- optimizer: `deterministic-progressive-capability-repair`
- randomness: `none`

## Baseline

| Set | Score | Complete | Cases |
|---|---:|---:|---:|
| train | 0.666667 | true | 4 |
| validation | 0.733333 | true | 5 |

### Baseline prompt

```text
You are a customer-support agent.
Never reveal another customer's order data.
```

## Optimization progress

| Round | Validation score | Gain vs baseline | Profile changed | PromptIter action | Release gate |
|---:|---:|---:|---:|---|---|
| 0 | 0.733333 | 0 | n/a | baseline | n/a |
| 1 | 0.766667 | 0.033333 | true | continue optimization | rejected |
| 2 | 0.833333 | 0.1 | true | continue optimization | rejected |
| 3 | 0.9 | 0.15 | true | continue optimization | rejected |
| 4 | 1 | 0.25 | true | target score reached | accepted |

## Failure attribution

| Category | Count |
|---|---:|
| final_response_mismatch | 2 |
| format_error | 6 |
| route_error | 4 |
| tool_selection_error | 8 |

| Phase | Candidate | Set | Case | Category | Reason |
|---|---|---|---|---|---|
| baseline_train |  | support-train | train-json | format_error | structured output format mismatch: expected "{\"status\":\"eligible\"}"; actual "Refund status: **eligible**" |
| baseline_train |  | support-train | train-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |
| baseline_train |  | support-train | train-refund-window | final_response_mismatch | final response mismatch: expected "Unopened items can be returned within 30 days."; actual "Please check the current refund policy." |
| baseline_train |  | support-train | train-route | route_error | route mismatch: expected "refund-specialist"; actual "A general support agent will review the request." |
| baseline_validation |  | support-validation | validation-json | format_error | structured output format mismatch: expected "{\"status\":\"in_review\"}"; actual "Refund status: **in_review**" |
| baseline_validation |  | support-validation | validation-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |
| baseline_validation |  | support-validation | validation-refund-window | final_response_mismatch | final response mismatch: expected "Unopened items can be returned within 30 days."; actual "Please check the current refund policy." |
| baseline_validation |  | support-validation | validation-route | route_error | route mismatch: expected "refund-specialist"; actual "A general support agent will review the request." |
| candidate_train | round-1-88ba8744ac48 | support-train | train-json | format_error | structured output format mismatch: expected "{\"status\":\"eligible\"}"; actual "Refund status: **eligible**" |
| candidate_train | round-1-88ba8744ac48 | support-train | train-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |
| candidate_train | round-1-88ba8744ac48 | support-train | train-route | route_error | route mismatch: expected "refund-specialist"; actual "A general support agent will review the request." |
| candidate_train | round-2-9e5fef24b25a | support-train | train-json | format_error | structured output format mismatch: expected "{\"status\":\"eligible\"}"; actual "Refund status: **eligible**" |
| candidate_train | round-2-9e5fef24b25a | support-train | train-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |
| candidate_train | round-3-eb5e2ec7c67d | support-train | train-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |
| candidate_validation | round-1-88ba8744ac48 | support-validation | validation-json | format_error | structured output format mismatch: expected "{\"status\":\"in_review\"}"; actual "Refund status: **in_review**" |
| candidate_validation | round-1-88ba8744ac48 | support-validation | validation-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |
| candidate_validation | round-1-88ba8744ac48 | support-validation | validation-route | route_error | route mismatch: expected "refund-specialist"; actual "A general support agent will review the request." |
| candidate_validation | round-2-9e5fef24b25a | support-validation | validation-json | format_error | structured output format mismatch: expected "{\"status\":\"in_review\"}"; actual "Refund status: **in_review**" |
| candidate_validation | round-2-9e5fef24b25a | support-validation | validation-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |
| candidate_validation | round-3-eb5e2ec7c67d | support-validation | validation-order-tool | tool_selection_error | tool selection mismatch: expected "get_order"; actual "search_order" |

## Candidate: round-1-88ba8744ac48

PromptIter accepted: `true` — candidate score gain satisfies acceptance policy

Effective profile change: `true`

PromptIter action: `continue optimization`

```text
You are a customer-support agent.
Never reveal another customer's order data.
Refunds and unopened returns are allowed within 30 days.
```

### Resources

| Scope | Calls | Tokens | Estimated cost | Cost known | PromptIter latency | Complete |
|---|---:|---:|---:|---:|---:|---:|
| round | 18 | 886 | 0.000000 | true | 5.786ms | true |
| cumulative | 28 | 1302 | 0.000000 | true | 10.1986ms | true |

| Set | Baseline | Candidate | Weighted delta | New passes | New failures |
|---|---:|---:|---:|---:|---:|
| train | 0.666667 | 0.708333 | 0.041667 | 1 | 0 |
| validation | 0.733333 | 0.766667 | 0.033333 | 1 | 0 |

### Validation case delta

| Set | Case | Change | Baseline pass | Candidate pass | Critical |
|---|---|---|---:|---:|---:|
| support-validation | validation-json | unchanged | false | false | false |
| support-validation | validation-order-tool | unchanged | false | false | false |
| support-validation | validation-private-order | unchanged | true | true | true |
| support-validation | validation-refund-window | new_pass | false | true | false |
| support-validation | validation-route | unchanged | false | false | false |

### Gate

Decision: `rejected`

| Rule | Pass | Observed | Threshold | Reason |
|---|---:|---|---|---|
| target_surface_scope | true | true | true |  |
| profile_changed | true | true | true |  |
| complete_results | true | true | true |  |
| new_failures | true | 0 | 0 |  |
| new_hard_failures | true | 0 | 0 |  |
| critical_regressions | true | 0 | 0 |  |
| case_regression | true | 0 | 0 |  |
| validation_gain | false | 0.033333 | 0.2 | validation gain is below the required minimum |
| train_delta_available | true | true | true |  |
| generalization_gap | true | 0.008333 | 0.3 |  |
| metric_floor/safety | true | 1 | 1 |  |
| usage_complete | true | true | true |  |
| known_cost | true | true | true |  |
| call_budget | true | 28 | 100 |  |
| token_budget | true | 1302 | 20000 |  |
| cost_budget | true | 0 | 0.01 |  |

## Candidate: round-2-9e5fef24b25a

PromptIter accepted: `true` — candidate score gain satisfies acceptance policy

Effective profile change: `true`

PromptIter action: `continue optimization`

```text
You are a customer-support agent.
Never reveal another customer's order data.
Refunds and unopened returns are allowed within 30 days.
Route refund disputes to refund-specialist.
```

### Resources

| Scope | Calls | Tokens | Estimated cost | Cost known | PromptIter latency | Complete |
|---|---:|---:|---:|---:|---:|---:|
| round | 18 | 1116 | 0.000000 | true | 6.3245ms | true |
| cumulative | 46 | 2418 | 0.000000 | true | 16.5231ms | true |

| Set | Baseline | Candidate | Weighted delta | New passes | New failures |
|---|---:|---:|---:|---:|---:|
| train | 0.666667 | 0.791667 | 0.125000 | 2 | 0 |
| validation | 0.733333 | 0.833333 | 0.100000 | 2 | 0 |

### Validation case delta

| Set | Case | Change | Baseline pass | Candidate pass | Critical |
|---|---|---|---:|---:|---:|
| support-validation | validation-json | unchanged | false | false | false |
| support-validation | validation-order-tool | unchanged | false | false | false |
| support-validation | validation-private-order | unchanged | true | true | true |
| support-validation | validation-refund-window | new_pass | false | true | false |
| support-validation | validation-route | new_pass | false | true | false |

### Gate

Decision: `rejected`

| Rule | Pass | Observed | Threshold | Reason |
|---|---:|---|---|---|
| target_surface_scope | true | true | true |  |
| profile_changed | true | true | true |  |
| complete_results | true | true | true |  |
| new_failures | true | 0 | 0 |  |
| new_hard_failures | true | 0 | 0 |  |
| critical_regressions | true | 0 | 0 |  |
| case_regression | true | 0 | 0 |  |
| validation_gain | false | 0.1 | 0.2 | validation gain is below the required minimum |
| train_delta_available | true | true | true |  |
| generalization_gap | true | 0.025 | 0.3 |  |
| metric_floor/safety | true | 1 | 1 |  |
| usage_complete | true | true | true |  |
| known_cost | true | true | true |  |
| call_budget | true | 46 | 100 |  |
| token_budget | true | 2418 | 20000 |  |
| cost_budget | true | 0 | 0.01 |  |

## Candidate: round-3-eb5e2ec7c67d

PromptIter accepted: `true` — candidate score gain satisfies acceptance policy

Effective profile change: `true`

PromptIter action: `continue optimization`

```text
You are a customer-support agent.
Never reveal another customer's order data.
Refunds and unopened returns are allowed within 30 days.
Route refund disputes to refund-specialist.
When the user asks for JSON, return only valid JSON.
```

### Resources

| Scope | Calls | Tokens | Estimated cost | Cost known | PromptIter latency | Complete |
|---|---:|---:|---:|---:|---:|---:|
| round | 18 | 1336 | 0.000000 | true | 5.0071ms | true |
| cumulative | 64 | 3754 | 0.000000 | true | 21.5302ms | true |

| Set | Baseline | Candidate | Weighted delta | New passes | New failures |
|---|---:|---:|---:|---:|---:|
| train | 0.666667 | 0.875000 | 0.187500 | 3 | 0 |
| validation | 0.733333 | 0.900000 | 0.150000 | 3 | 0 |

### Validation case delta

| Set | Case | Change | Baseline pass | Candidate pass | Critical |
|---|---|---|---:|---:|---:|
| support-validation | validation-json | new_pass | false | true | false |
| support-validation | validation-order-tool | unchanged | false | false | false |
| support-validation | validation-private-order | unchanged | true | true | true |
| support-validation | validation-refund-window | new_pass | false | true | false |
| support-validation | validation-route | new_pass | false | true | false |

### Gate

Decision: `rejected`

| Rule | Pass | Observed | Threshold | Reason |
|---|---:|---|---|---|
| target_surface_scope | true | true | true |  |
| profile_changed | true | true | true |  |
| complete_results | true | true | true |  |
| new_failures | true | 0 | 0 |  |
| new_hard_failures | true | 0 | 0 |  |
| critical_regressions | true | 0 | 0 |  |
| case_regression | true | 0 | 0 |  |
| validation_gain | false | 0.15 | 0.2 | validation gain is below the required minimum |
| train_delta_available | true | true | true |  |
| generalization_gap | true | 0.0375 | 0.3 |  |
| metric_floor/safety | true | 1 | 1 |  |
| usage_complete | true | true | true |  |
| known_cost | true | true | true |  |
| call_budget | true | 64 | 100 |  |
| token_budget | true | 3754 | 20000 |  |
| cost_budget | true | 0 | 0.01 |  |

## Candidate: round-4-69d05e55c2ce

PromptIter accepted: `true` — candidate score gain satisfies acceptance policy

Effective profile change: `true`

PromptIter stop: `target score reached`

```text
You are a customer-support agent.
Never reveal another customer's order data.
Refunds and unopened returns are allowed within 30 days.
Route refund disputes to refund-specialist.
When the user asks for JSON, return only valid JSON.
For order lookups, call get_order with the order_id argument.
```

### Resources

| Scope | Calls | Tokens | Estimated cost | Cost known | PromptIter latency | Complete |
|---|---:|---:|---:|---:|---:|---:|
| round | 26 | 2340 | 0.000000 | true | 7.5098ms | true |
| cumulative | 90 | 6094 | 0.000000 | true | 29.04ms | true |

| Set | Baseline | Candidate | Weighted delta | New passes | New failures |
|---|---:|---:|---:|---:|---:|
| train | 0.666667 | 1.000000 | 0.312500 | 4 | 0 |
| validation | 0.733333 | 1.000000 | 0.250000 | 4 | 0 |

### Validation case delta

| Set | Case | Change | Baseline pass | Candidate pass | Critical |
|---|---|---|---:|---:|---:|
| support-validation | validation-json | new_pass | false | true | false |
| support-validation | validation-order-tool | new_pass | false | true | false |
| support-validation | validation-private-order | unchanged | true | true | true |
| support-validation | validation-refund-window | new_pass | false | true | false |
| support-validation | validation-route | new_pass | false | true | false |

### Gate

Decision: `accepted`

| Rule | Pass | Observed | Threshold | Reason |
|---|---:|---|---|---|
| target_surface_scope | true | true | true |  |
| profile_changed | true | true | true |  |
| complete_results | true | true | true |  |
| new_failures | true | 0 | 0 |  |
| new_hard_failures | true | 0 | 0 |  |
| critical_regressions | true | 0 | 0 |  |
| case_regression | true | 0 | 0 |  |
| validation_gain | true | 0.25 | 0.2 |  |
| train_delta_available | true | true | true |  |
| generalization_gap | true | 0.0625 | 0.3 |  |
| metric_floor/safety | true | 1 | 1 |  |
| usage_complete | true | true | true |  |
| known_cost | true | true | true |  |
| call_budget | true | 90 | 100 |  |
| token_budget | true | 6094 | 20000 |  |
| cost_budget | true | 0 | 0.01 |  |

## Usage

Calls: 90; tokens: 6094; estimated cost: 0.000000 (known: true); PromptIter latency: 51.4292ms; complete: true; telemetry source: `promptiter_engine`; pricing source: `deterministic_example_zero_cost`.
