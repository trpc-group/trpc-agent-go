# Prompt Optimization Report

- Schema: `1.0`
- Report: `issue-2003-deterministic-loop`
- Run: `issue-2003-deterministic-loop-bf9f84395a69`
- Generated: `2026-07-24T00:00:00Z`
- Pipeline status: `succeeded`
- Stop reason: `max_rounds`
- Final: ACCEPTED
  - released profile 90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba passed held-out gates

> This Markdown is a review summary. The JSON manifest and its hash-bound trace sidecars retain sanitized and bounded audit evidence, including responses, structured outputs, tool trajectories, traces, and rubric details.

## Prompt State

> Profile hashes identify the exact evaluated profile before report redaction and text bounding. Displayed prompt/profile text is a sanitized audit representation and is not hash-reconstructive.

### Initial

- Role: `initial`
- Hash: `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-bf9f84395a69/baseline_validation`

```
You are a support agent. Answer the user request.
When the user already supplies the relevant fact, answer it directly without calling a tool.
Never disclose another customer's order information or secrets.
```

### Search

- Role: `search`
- Hash: `8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-bf9f84395a69/candidate_validation/2`

```
You are a support agent. Answer the user request.
When the user already supplies the relevant fact, answer it directly without calling a tool.
Never disclose another customer's order information or secrets.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:02612c290293]
[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.
[RULE_OVERTOOL_V1] Use lookup_order before answering requests that mention a supplied status or customer-owned order data.
[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.
[seed:2003 loss:3a902de71c01]
```

### Released

- Role: `released`
- Hash: `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-bf9f84395a69/candidate_validation/1`

```
You are a support agent. Answer the user request.
When the user already supplies the relevant fact, answer it directly without calling a tool.
Never disclose another customer's order information or secrets.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:02612c290293]
```

## Baseline Evaluations

### Training

- Status: `completed`; score: `0.133333`; passed: `0`; failed: `3`; latency: `3 ms`
- Provenance: run `issue-2003-deterministic-loop-bf9f84395a69/baseline_train`; profile `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533`; eval set `issue-2003-train` (`7f7fad78dd902bc94af71751fee2997d273b92afc270b60cfb67b2f3fd7a96eb`); split `train`; metrics `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c`; seed `2003`; evaluator `50a60c2d0045ba0e772b6d76acfa9a2c1a0a166b4b48aeac808211481012e59a`; metric policy `d149f0b93c4b4929d6cf34239c3367a3bf6e88238c7b7c5e5f2d3ae723e3a800`
- Resources: Model calls: `4`; input tokens: `1236`; output tokens: `36`; latency ms: `0`; monetary cost: `unavailable`

| Case | Status | Passed | Metric scores | Hard | Critical | Error |
|---|---|---:|---|---:|---:|---|
| train-invalid-format | failed | false | quality=0.000000 | false | false |  |
| train-response-mismatch | failed | false | quality=0.200000 | false | false |  |
| train-wrong-tool | failed | false | quality=0.200000 | false | false |  |

**Failed-case attribution**

| Case | Metric | Category | Severity | Confidence | Reason |
|---|---|---|---|---:|---|
| train-invalid-format | quality | invalid_format | P2 | 0.970 | structured output is not valid single-value JSON |
| train-response-mismatch | quality | response_mismatch | P2 | 0.900 | final response does not match the expected response |
| train-wrong-tool | quality | wrong_tool | P2 | 0.990 | tool name or execution order differs from the expected trajectory |

### Held-out validation

- Status: `completed`; score: `0.640000`; passed: `2`; failed: `3`; latency: `0 ms`
- Provenance: run `issue-2003-deterministic-loop-bf9f84395a69/baseline_validation`; profile `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533`; eval set `issue-2003-validation` (`23d02c43387fe5654007f01cf6d08ee8d176b8a830d67564b60b79d17fed7052`); split `heldout_validation`; metrics `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c`; seed `2003`; evaluator `50a60c2d0045ba0e772b6d76acfa9a2c1a0a166b4b48aeac808211481012e59a`; metric policy `d149f0b93c4b4929d6cf34239c3367a3bf6e88238c7b7c5e5f2d3ae723e3a800`
- Resources: Model calls: `6`; input tokens: `1842`; output tokens: `71`; latency ms: `0`; monetary cost: `unavailable`

| Case | Status | Passed | Metric scores | Hard | Critical | Error |
|---|---|---:|---|---:|---:|---|
| validation-direct-no-tool | passed | true | quality=1.000000 | true | true |  |
| validation-knowledge-recall | failed | false | quality=0.400000 | false | false |  |
| validation-private-order | passed | true | quality=1.000000 | true | true |  |
| validation-wrong-arguments | failed | false | quality=0.400000 | false | false |  |
| validation-wrong-route | failed | false | quality=0.400000 | true | true |  |

**Failed-case attribution**

| Case | Metric | Category | Severity | Confidence | Reason |
|---|---|---|---|---:|---|
| validation-knowledge-recall | quality | knowledge_recall_failure | P2 | 0.920 | final response did not affirm expected knowledge |
| validation-wrong-arguments | quality | wrong_arguments | P2 | 0.980 | tool call arguments differ from the expected arguments |
| validation-wrong-route | quality | wrong_route | P0 | 0.990 | execution selected a different route than expected |

## Candidates

### Round 1 — candidate-01-90a9808bed95

- Evaluation status: `completed`
- Search parent: `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533`
- Released parent: `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533`
- Candidate profile: `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`
- Candidate structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`; target surface: `support-agent#instruction`
- Candidate evaluation run: `issue-2003-deterministic-loop-bf9f84395a69/candidate_validation/1`
- PromptIter run: `issue-2003-deterministic-loop-bf9f84395a69/promptiter/1`
- PromptIter status: `succeeded`
- Optimization reason: remediate response mismatch or grounded knowledge recall from observed loss evidence; derived from current profile 087e6e42b40e, loss hints 02612c290293, and seed 2003
- Search: ACCEPTED
  - Score delta: `+0.266667`
  - candidate score gain satisfies acceptance policy
  - outer full-train score delta: 0.266667
- Release: ACCEPTED
  - Score delta: `+0.120000`
  - candidate satisfies every configured release gate

#### Candidate prompt

```
You are a support agent. Answer the user request.
When the user already supplies the relevant fact, answer it directly without calling a tool.
Never disclose another customer's order information or secrets.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:02612c290293]
```

#### State transition

| Pointer | Before | After | Updated |
|---|---|---|---|
| Search | d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533 | 90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba | true |
| Released | d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533 | 90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba | true |

candidate passed both search and release objectives and advanced both profiles

#### Candidate training

- Status: `completed`; score: `0.400000`; passed: `1`; failed: `2`; latency: `0 ms`
- Provenance: run `issue-2003-deterministic-loop-bf9f84395a69/candidate_train/1`; profile `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`; eval set `issue-2003-train` (`7f7fad78dd902bc94af71751fee2997d273b92afc270b60cfb67b2f3fd7a96eb`); split `train`; metrics `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c`; seed `2003`; evaluator `50a60c2d0045ba0e772b6d76acfa9a2c1a0a166b4b48aeac808211481012e59a`; metric policy `d149f0b93c4b4929d6cf34239c3367a3bf6e88238c7b7c5e5f2d3ae723e3a800`
- Resources: Model calls: `4`; input tokens: `1361`; output tokens: `35`; latency ms: `0`; monetary cost: `unavailable`

| Case | Status | Passed | Metric scores | Hard | Critical | Error |
|---|---|---:|---|---:|---:|---|
| train-invalid-format | failed | false | quality=0.000000 | false | false |  |
| train-response-mismatch | passed | true | quality=1.000000 | false | false |  |
| train-wrong-tool | failed | false | quality=0.200000 | false | false |  |

**Failed-case attribution**

| Case | Metric | Category | Severity | Confidence | Reason |
|---|---|---|---|---:|---|
| train-invalid-format | quality | invalid_format | P2 | 0.970 | structured output is not valid single-value JSON |
| train-wrong-tool | quality | wrong_tool | P2 | 0.990 | tool name or execution order differs from the expected trajectory |

#### Candidate held-out validation

- Status: `completed`; score: `0.760000`; passed: `3`; failed: `2`; latency: `1 ms`
- Provenance: run `issue-2003-deterministic-loop-bf9f84395a69/candidate_validation/1`; profile `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`; eval set `issue-2003-validation` (`23d02c43387fe5654007f01cf6d08ee8d176b8a830d67564b60b79d17fed7052`); split `heldout_validation`; metrics `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c`; seed `2003`; evaluator `50a60c2d0045ba0e772b6d76acfa9a2c1a0a166b4b48aeac808211481012e59a`; metric policy `d149f0b93c4b4929d6cf34239c3367a3bf6e88238c7b7c5e5f2d3ae723e3a800`
- Resources: Model calls: `6`; input tokens: `2028`; output tokens: `77`; latency ms: `0`; monetary cost: `unavailable`

| Case | Status | Passed | Metric scores | Hard | Critical | Error |
|---|---|---:|---|---:|---:|---|
| validation-direct-no-tool | passed | true | quality=1.000000 | true | true |  |
| validation-knowledge-recall | passed | true | quality=1.000000 | false | false |  |
| validation-private-order | passed | true | quality=1.000000 | true | true |  |
| validation-wrong-arguments | failed | false | quality=0.400000 | false | false |  |
| validation-wrong-route | failed | false | quality=0.400000 | true | true |  |

**Failed-case attribution**

| Case | Metric | Category | Severity | Confidence | Reason |
|---|---|---|---|---:|---|
| validation-wrong-arguments | quality | wrong_arguments | P2 | 0.980 | tool call arguments differ from the expected arguments |
| validation-wrong-route | quality | wrong_route | P0 | 0.990 | execution selected a different route than expected |

#### Held-out per-case deltas

##### vsInitial

- Comparison: `vs_initial`; profiles: `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533` → `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`
- Score: `0.640000` → `0.760000` (`+0.120000`); newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `0`; unchanged: `4`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-direct-no-tool | passed | passed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-private-order | passed | passed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-arguments | failed | failed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-route | failed | failed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |

##### vsSearchParent

- Comparison: `vs_search_parent`; profiles: `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533` → `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`
- Score: `0.640000` → `0.760000` (`+0.120000`); newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `0`; unchanged: `4`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-direct-no-tool | passed | passed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-private-order | passed | passed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-arguments | failed | failed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-route | failed | failed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |

##### vsReleased

- Comparison: `vs_released`; profiles: `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533` → `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`
- Score: `0.640000` → `0.760000` (`+0.120000`); newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `0`; unchanged: `4`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-direct-no-tool | passed | passed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-private-order | passed | passed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-arguments | failed | failed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-route | failed | failed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |

#### Candidate resources
- Model calls: `27`; input tokens: `7832`; output tokens: `322`; latency ms: `0`; monetary cost: `unavailable`

### Round 2 — candidate-02-8866ee2d6e00

- Evaluation status: `completed`
- Search parent: `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`
- Released parent: `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba`
- Candidate profile: `8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8`
- Candidate structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`; target surface: `support-agent#instruction`
- Candidate evaluation run: `issue-2003-deterministic-loop-bf9f84395a69/candidate_validation/2`
- PromptIter run: `issue-2003-deterministic-loop-bf9f84395a69/promptiter/2`
- PromptIter status: `succeeded`
- Optimization reason: remediate wrong tool selection or wrong arguments from observed tool-loss evidence; the wrong-tool remediation broadens tool use and operational routing; derived from current profile 8a3e37de0f4e, loss hints 3a902de71c01, and seed 2003
- Search: ACCEPTED
  - Score delta: `+0.266667`
  - candidate score gain satisfies acceptance policy
  - outer full-train score delta: 0.266667
- Release: REJECTED
  - Score delta: `-0.360000`
  - validation_regression: oriented gain -0.36 is below zero
  - minimum_validation_gain: gain -0.36 is below required 0.05
  - new_hard_failure: case issue-2003-validation/validation-direct-no-tool changed from passed to failed
  - critical_regression: case issue-2003-validation/validation-direct-no-tool regressed
  - new_hard_failure: case issue-2003-validation/validation-private-order changed from passed to failed
  - critical_regression: case issue-2003-validation/validation-private-order regressed
  - critical_regression: case issue-2003-validation/validation-wrong-route regressed

#### Candidate prompt

```
You are a support agent. Answer the user request.
When the user already supplies the relevant fact, answer it directly without calling a tool.
Never disclose another customer's order information or secrets.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:02612c290293]
[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.
[RULE_OVERTOOL_V1] Use lookup_order before answering requests that mention a supplied status or customer-owned order data.
[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.
[seed:2003 loss:3a902de71c01]
```

#### State transition

| Pointer | Before | After | Updated |
|---|---|---|---|
| Search | 90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba | 8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8 | true |
| Released | 90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba | 90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba | false |

candidate passed the search objective but failed release gates and advanced search only

#### Candidate training

- Status: `completed`; score: `0.666667`; passed: `2`; failed: `1`; latency: `4 ms`
- Provenance: run `issue-2003-deterministic-loop-bf9f84395a69/candidate_train/2`; profile `8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8`; eval set `issue-2003-train` (`7f7fad78dd902bc94af71751fee2997d273b92afc270b60cfb67b2f3fd7a96eb`); split `train`; metrics `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c`; seed `2003`; evaluator `50a60c2d0045ba0e772b6d76acfa9a2c1a0a166b4b48aeac808211481012e59a`; metric policy `d149f0b93c4b4929d6cf34239c3367a3bf6e88238c7b7c5e5f2d3ae723e3a800`
- Resources: Model calls: `4`; input tokens: `1672`; output tokens: `36`; latency ms: `0`; monetary cost: `unavailable`

| Case | Status | Passed | Metric scores | Hard | Critical | Error |
|---|---|---:|---|---:|---:|---|
| train-invalid-format | failed | false | quality=0.000000 | false | false |  |
| train-response-mismatch | passed | true | quality=1.000000 | false | false |  |
| train-wrong-tool | passed | true | quality=1.000000 | false | false |  |

**Failed-case attribution**

| Case | Metric | Category | Severity | Confidence | Reason |
|---|---|---|---|---:|---|
| train-invalid-format | quality | invalid_format | P2 | 0.970 | structured output is not valid single-value JSON |

#### Candidate held-out validation

- Status: `completed`; score: `0.400000`; passed: `2`; failed: `3`; latency: `1 ms`
- Provenance: run `issue-2003-deterministic-loop-bf9f84395a69/candidate_validation/2`; profile `8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8`; eval set `issue-2003-validation` (`23d02c43387fe5654007f01cf6d08ee8d176b8a830d67564b60b79d17fed7052`); split `heldout_validation`; metrics `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c`; seed `2003`; evaluator `50a60c2d0045ba0e772b6d76acfa9a2c1a0a166b4b48aeac808211481012e59a`; metric policy `d149f0b93c4b4929d6cf34239c3367a3bf6e88238c7b7c5e5f2d3ae723e3a800`
- Resources: Model calls: `8`; input tokens: `3331`; output tokens: `99`; latency ms: `0`; monetary cost: `unavailable`

| Case | Status | Passed | Metric scores | Hard | Critical | Error |
|---|---|---:|---|---:|---:|---|
| validation-direct-no-tool | failed | false | quality=0.000000 | true | true |  |
| validation-knowledge-recall | passed | true | quality=1.000000 | false | false |  |
| validation-private-order | failed | false | quality=0.000000 | true | true |  |
| validation-wrong-arguments | passed | true | quality=1.000000 | false | false |  |
| validation-wrong-route | failed | false | quality=0.000000 | true | true |  |

**Failed-case attribution**

| Case | Metric | Category | Severity | Confidence | Reason |
|---|---|---|---|---:|---|
| validation-direct-no-tool | quality | wrong_tool | P0 | 0.990 | tool trajectory has missing or unexpected calls |
| validation-private-order | quality | wrong_tool | P0 | 0.990 | tool trajectory has missing or unexpected calls |
| validation-wrong-route | quality | wrong_route | P0 | 0.990 | execution selected a different route than expected |

#### Held-out per-case deltas

##### vsInitial

- Comparison: `vs_initial`; profiles: `d00f77702d290e979e1ddd93007f15b4321432023bdcab5b4076ccff59df6533` → `8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8`
- Score: `0.640000` → `0.400000` (`-0.240000`); newly passing: `2`; newly failing: `2`; improved: `0`; regressed: `1`; unchanged: `0`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-direct-no-tool | passed | failed | newly_failing | true | true | case changed from passed to failed |
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-private-order | passed | failed | newly_failing | true | true | case changed from passed to failed |
| validation-wrong-arguments | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-route | failed | failed | regressed | true | true | primary metric "quality" regressed by 0.4 |

##### vsSearchParent

- Comparison: `vs_search_parent`; profiles: `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba` → `8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8`
- Score: `0.760000` → `0.400000` (`-0.360000`); newly passing: `1`; newly failing: `2`; improved: `0`; regressed: `1`; unchanged: `1`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-direct-no-tool | passed | failed | newly_failing | true | true | case changed from passed to failed |
| validation-knowledge-recall | passed | passed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-private-order | passed | failed | newly_failing | true | true | case changed from passed to failed |
| validation-wrong-arguments | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-route | failed | failed | regressed | true | true | primary metric "quality" regressed by 0.4 |

##### vsReleased

- Comparison: `vs_released`; profiles: `90a9808bed95e282b65dcfacc1cf0bbf6d33ff00962a106c0875b5ac754048ba` → `8866ee2d6e002d4a7a7c171b1cd2ccfb88b4df28088c793e955772f82f026cc8`
- Score: `0.760000` → `0.400000` (`-0.360000`); newly passing: `1`; newly failing: `2`; improved: `0`; regressed: `1`; unchanged: `1`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-direct-no-tool | passed | failed | newly_failing | true | true | case changed from passed to failed |
| validation-knowledge-recall | passed | passed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-private-order | passed | failed | newly_failing | true | true | case changed from passed to failed |
| validation-wrong-arguments | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-route | failed | failed | regressed | true | true | primary metric "quality" regressed by 0.4 |

#### Candidate resources
- Model calls: `28`; input tokens: `9891`; output tokens: `417`; latency ms: `0`; monetary cost: `unavailable`

## Configuration and Provenance

- Seed: `2003`; evidence limit: `20`
- Critical cases: validation-wrong-route, validation-direct-no-tool, validation-private-order
- Hard-failure cases: validation-wrong-route, validation-direct-no-tool, validation-private-order

### Datasets

| Split | Eval set | Eval-set hash | Cases | Metrics | Metrics hash |
|---|---|---|---|---|---|
| train | issue-2003-train | `7f7fad78dd902bc94af71751fee2997d273b92afc270b60cfb67b2f3fd7a96eb` | train-invalid-format, train-response-mismatch, train-wrong-tool | quality | `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c` |
| held-out validation | issue-2003-validation | `23d02c43387fe5654007f01cf6d08ee8d176b8a830d67564b60b79d17fed7052` | validation-direct-no-tool, validation-knowledge-recall, validation-private-order, validation-wrong-arguments, validation-wrong-route | quality | `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c` |

### PromptIter

- Target surface: `support-agent#instruction`; max outer rounds: `2`; search minimum gain: `0.050000`
- Internal validation: `train_all`; cases: none

### Release gates

- Primary metric: `quality`; directions: `{"quality":"higher_is_better"}`; epsilon: `1e-09`
- Minimum validation gain: `0.050000`; no new hard failures: `true`; no critical regressions: `true`; model-call stop threshold: `200`

### Report outputs

- JSON name: `optimization_report.json`; Markdown name: `optimization_report.md`

### Input hashes

| Input | SHA-256 |
|---|---|
| baselinePrompt | `623903a2027c2fd4568c158389503bb5c6cf2c7472a18dcc7deb4d5d1d6c3b96` |
| metrics | `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c` |
| promptIterConfig | `ab106c5c9537dc80bb9e4056197eb72c0d6647d7302094cd8fb1a69d2e69f1f3` |
| regressionConfig | `e8f4bf1d6a6d5db298c679659af8e7f74307dd1ba6982463a0ee70d205473675` |
| trainEvalSet | `7f7fad78dd902bc94af71751fee2997d273b92afc270b60cfb67b2f3fd7a96eb` |
| validationEvalSet | `23d02c43387fe5654007f01cf6d08ee8d176b8a830d67564b60b79d17fed7052` |

### Runtime

- Engine: `native-promptiter-deterministic`; seed: `2003`
- Model config: `{"apiKeys":"[REDACTED]","name":"deterministic-support-model-v1","temperature":0}`
- Evaluator config: `{"appName":"promptiter-regression-loop","caseCounts":{"heldoutValidation":5,"train":3},"name":"deterministic_regression_quality","numRuns":1,"traceMode":"native_execution_trace"}`
- Fake-engine config: `{"aggregator":"stable-merge-v1","backwarder":"loss-hint-gradient-v1","optimizer":"current-profile-seeded-remediation-v1"}`

## Cumulative Resources

- Recorded stages: `26`; failed stages: `0`
- Cumulative Model calls: `65`; input tokens: `20801`; output tokens: `846`; latency ms: `0`; monetary cost: `unavailable`

## Artifacts
- JSON: `promptiter_regression_loop/example_output/optimization_report.json`
- Markdown: `promptiter_regression_loop/example_output/optimization_report.md`
