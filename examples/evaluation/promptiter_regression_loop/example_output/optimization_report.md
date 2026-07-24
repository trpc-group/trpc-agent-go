# Prompt Optimization Report

- Schema: `1.0`
- Report: `issue-2003-deterministic-loop`
- Run: `issue-2003-deterministic-loop-084445806dbe`
- Generated: `2026-07-24T00:00:00Z`
- Pipeline status: `succeeded`
- Stop reason: `max_rounds`
- Final: ACCEPTED
  - released profile 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a passed held-out gates

## Prompt State

> Profile hashes identify the exact evaluated profile before report redaction and text bounding. Displayed prompt/profile text is a sanitized audit representation and is not hash-reconstructive.

### Initial

- Role: `initial`
- Hash: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/baseline_validation`

```
You are a support agent. Answer the user request.
```

### Search

- Role: `search`
- Hash: `b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_validation/2`

```
You are a support agent. Answer the user request.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:a29abe4b2898]
[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.
[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.
[seed:2003 loss:aef4c3a15a9a]
```

### Released

- Role: `released`
- Hash: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_validation/1`

```
You are a support agent. Answer the user request.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:a29abe4b2898]
```

## Baseline Evaluations

### Training

- Status: `completed`
- Eval set: `issue-2003-train`
- Split: `train`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/baseline_train`
- Profile hash: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844`
- Score: `0.133333`; passed: `0`; failed: `3`; latency: `1 ms`

| Case | Status | Passed | Hard | Critical | Primary metric | Error |
|---|---|---:|---:|---:|---|---|
| train-invalid-format | failed | false | false | false | quality |  |
| train-response-mismatch | failed | false | false | false | quality |  |
| train-wrong-tool | failed | false | false | false | quality |  |

#### Case evidence: train-invalid-format

- Route: `support-agent`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.000000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
status: shipped
```

Expected response:
```
{"status":"shipped"}
```

Structured output:
```json
status: shipped
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Return the shipping status for order A-17 as a JSON object with one string field named status."}] | {"id":"deterministic-response-f8eca984642c","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"status: shipped"},"delta":{"role":""}}],"usage":{"prompt_tokens":269,"completion_tokens":4,"total_tokens":273,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: train-response-mismatch

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.200000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:support] Your request was received.
```

Expected response:
```
Order A-17 has shipped.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Order A-17 has status shipped. Tell the customer the current status in one sentence."}] | {"id":"deterministic-response-f2f8bdc596e9","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] Your request was received."},"delta":{"role":""}}],"usage":{"prompt_tokens":267,"completion_tokens":11,"total_tokens":278,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: train-wrong-tool

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.200000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:support] The requested order lookup is complete.
```

Expected response:
```
The requested order lookup is complete.
```

Observed tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | search_web | {"orderId":"A-17"} | {"orderId":"A-17","status":"unverified"} |

Expected tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"A-17"} | {"orderId":"A-17","status":"shipped"} |

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Find current status for order A-17. Select the correct internal operation and arguments before answering."}] | {"id":"deterministic-response-4a544e7d1fc3","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"A-17\"}","name":"search_web"},"id":"deterministic-tool-call"}]},"delta":{"role":""}}],"usage":{"prompt_tokens":272,"completion_tokens":7,"total_tokens":279,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |
| s2 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Find current status for order A-17. Select the correct internal operation and arguments before answering."},{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"A-17\"}","name":"search_web"},"id":"deterministic-tool-call"}]},{"role":"tool","content":"{\"orderId\":\"A-17\",\"status\":\"unverified\"}","tool_id":"deterministic-tool-call","tool_name":"search_web"}] | {"id":"deterministic-response-665f46ad79da","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] The requested order lookup is complete."},"delta":{"role":""}}],"usage":{"prompt_tokens":272,"completion_tokens":14,"total_tokens":286,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Attribution: train-invalid-format / quality

- Category: `invalid_format`; severity: `P2`; confidence: `0.970`; evidence: `sufficient`
- Reason: structured output is not valid single-value JSON
  - `case.structured_output` `structured_output`: structured output is not valid single-value JSON; actual "status: shipped"
  - `case.response` `response`: expected "{\"status\":\"shipped\"}"; actual "status: shipped"

#### Attribution: train-response-mismatch / quality

- Category: `response_mismatch`; severity: `P2`; confidence: `0.900`; evidence: `sufficient`
- Reason: final response does not match the expected response
  - `case.response` `response`: expected "Order A-17 has shipped."; actual "[route:support] Your request was received."

#### Attribution: train-wrong-tool / quality

- Category: `wrong_tool`; severity: `P2`; confidence: `0.990`; evidence: `sufficient`
- Reason: tool name or execution order differs from the expected trajectory
  - `case.tool_trajectory` `tool_trajectory`: step 1 expected tool "lookup_order"; actual tool "search_web"
  - `case.response` `response`: expected "The requested order lookup is complete."; actual "[route:support] The requested order lookup is complete."

#### Snapshot resources
- Model calls: `4`; input tokens: `1080`; output tokens: `36`; latency ms: `0`; monetary cost: `unavailable`

### Held-out validation

- Status: `completed`
- Eval set: `issue-2003-validation`
- Split: `heldout_validation`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/baseline_validation`
- Profile hash: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844`
- Score: `0.400000`; passed: `0`; failed: `3`; latency: `0 ms`

| Case | Status | Passed | Hard | Critical | Primary metric | Error |
|---|---|---:|---:|---:|---|---|
| validation-knowledge-recall | failed | false | false | false | quality |  |
| validation-wrong-arguments | failed | false | false | false | quality |  |
| validation-wrong-route | failed | false | true | true | quality |  |

#### Case evidence: validation-knowledge-recall

- Route: `support`; expected route: ``
- Expected facts: 7 days

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.400000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:support] Returns are available.
```

Expected response:
```
Unopened products may be returned within 7 days.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Policy states unopened products may be returned within 7 days. How long is my return window?"}] | {"id":"deterministic-response-548eae5ab72a","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] Returns are available."},"delta":{"role":""}}],"usage":{"prompt_tokens":269,"completion_tokens":10,"total_tokens":279,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: validation-wrong-arguments

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.400000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:support] The requested order lookup is complete.
```

Expected response:
```
The requested order lookup is complete.
```

Observed tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"B-18"} | {"orderId":"B-18","status":"shipped"} |

Expected tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"B-81"} | {"orderId":"B-81","status":"shipped"} |

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Check order B-81. Select the internal operation and exact arguments needed."}] | {"id":"deterministic-response-ac76547f20a6","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"B-18\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},"delta":{"role":""}}],"usage":{"prompt_tokens":264,"completion_tokens":8,"total_tokens":272,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |
| s2 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Check order B-81. Select the internal operation and exact arguments needed."},{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"B-18\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},{"role":"tool","content":"{\"orderId\":\"B-18\",\"status\":\"shipped\"}","tool_id":"deterministic-tool-call","tool_name":"lookup_order"}] | {"id":"deterministic-response-fe0e51d566e5","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] The requested order lookup is complete."},"delta":{"role":""}}],"usage":{"prompt_tokens":264,"completion_tokens":14,"total_tokens":278,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: validation-wrong-route

- Route: `sales`; expected route: `support`

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.400000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:sales] The sales team will review the damaged item.
```

Expected response:
```
The support team will help with the damaged item.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/sales | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"My delivered item is damaged. Route this request to the correct team and answer briefly."}] | {"id":"deterministic-response-ac2420f6b9fa","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:sales] The sales team will review the damaged item."},"delta":{"role":""}}],"usage":{"prompt_tokens":268,"completion_tokens":15,"total_tokens":283,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Attribution: validation-knowledge-recall / quality

- Category: `knowledge_recall_failure`; severity: `P2`; confidence: `0.920`; evidence: `sufficient`
- Reason: final response did not affirm expected knowledge
  - `case.expected_facts` `knowledge`: missing facts: 7 days
  - `case.final_response` `response`: [route:support] Returns are available.
  - `case.response` `response`: expected "Unopened products may be returned within 7 days."; actual "[route:support] Returns are available."

#### Attribution: validation-wrong-arguments / quality

- Category: `wrong_arguments`; severity: `P2`; confidence: `0.980`; evidence: `sufficient`
- Reason: tool call arguments differ from the expected arguments
  - `case.tool_arguments` `tool_arguments`: tool "lookup_order" expected arguments {"orderId":"B-81"}; actual arguments {"orderId":"B-18"}
  - `case.response` `response`: expected "The requested order lookup is complete."; actual "[route:support] The requested order lookup is complete."

#### Attribution: validation-wrong-route / quality

- Category: `wrong_route`; severity: `P0`; confidence: `0.990`; evidence: `sufficient`
- Reason: execution selected a different route than expected
  - `case.route` `route`: expected route "support"; actual route "sales"
  - `case.trace` `trace`: s1,node=support-agent,agent=support-agent,branch=router/sales
  - `case.response` `response`: expected "The support team will help with the damaged item."; actual "[route:sales] The sales team will review the damaged item."

#### Snapshot resources
- Model calls: `4`; input tokens: `1065`; output tokens: `47`; latency ms: `0`; monetary cost: `unavailable`

## Candidates

### Round 1 — candidate-01-873beea517d2

- Evaluation status: `completed`
- Search parent: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844`
- Released parent: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844`
- PromptIter run: `issue-2003-deterministic-loop-084445806dbe/promptiter/1`
- PromptIter status: `succeeded`
- Optimization reason: remediate response mismatch or grounded knowledge recall from observed loss evidence; derived from current profile 26c03ccb0c07, loss hints a29abe4b2898, and seed 2003
- Search: ACCEPTED
  - Score delta: `+0.266667`
  - candidate score gain satisfies acceptance policy
  - outer full-train score delta: 0.266667
- Release: ACCEPTED
  - Score delta: `+0.200000`
  - candidate satisfies every configured release gate

#### State transition

| Pointer | Before | After | Updated |
|---|---|---|---|
| Search | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | true |
| Released | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | true |

candidate passed both search and release objectives and advanced both profiles

### Candidate profile

- Role: `candidate`
- Hash: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_validation/1`

```
You are a support agent. Answer the user request.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:a29abe4b2898]
```

#### Patches

| Surface | Value | Reason |
|---|---|---|
| support-agent#instruction | You are a support agent. Answer the user request.<br>[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.<br>[seed:2003 loss:a29abe4b2898] | remediate response mismatch or grounded knowledge recall from observed loss evidence; derived from current profile 26c03ccb0c07, loss hints a29abe4b2898, and seed 2003 |

### Candidate training

- Status: `completed`
- Eval set: `issue-2003-train`
- Split: `train`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_train/1`
- Profile hash: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Score: `0.400000`; passed: `1`; failed: `2`; latency: `0 ms`

| Case | Status | Passed | Hard | Critical | Primary metric | Error |
|---|---|---:|---:|---:|---|---|
| train-invalid-format | failed | false | false | false | quality |  |
| train-response-mismatch | passed | true | false | false | quality |  |
| train-wrong-tool | failed | false | false | false | quality |  |

#### Case evidence: train-invalid-format

- Route: `support-agent`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.000000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
status: shipped
```

Expected response:
```
{"status":"shipped"}
```

Structured output:
```json
status: shipped
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Return the shipping status for order A-17 as a JSON object with one string field named status."}] | {"id":"deterministic-response-9e65286ce280","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"status: shipped"},"delta":{"role":""}}],"usage":{"prompt_tokens":300,"completion_tokens":4,"total_tokens":304,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: train-response-mismatch

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 1.000000 | 0.800000 | higher_is_better | passed | true |  |  |

Final response:
```
[route:support] Order A-17 has shipped.
```

Expected response:
```
Order A-17 has shipped.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Order A-17 has status shipped. Tell the customer the current status in one sentence."}] | {"id":"deterministic-response-8115d00361e2","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] Order A-17 has shipped."},"delta":{"role":""}}],"usage":{"prompt_tokens":298,"completion_tokens":10,"total_tokens":308,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: train-wrong-tool

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.200000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:support] The requested order lookup is complete.
```

Expected response:
```
The requested order lookup is complete.
```

Observed tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | search_web | {"orderId":"A-17"} | {"orderId":"A-17","status":"unverified"} |

Expected tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"A-17"} | {"orderId":"A-17","status":"shipped"} |

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Find current status for order A-17. Select the correct internal operation and arguments before answering."}] | {"id":"deterministic-response-3b2d80b062ee","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"A-17\"}","name":"search_web"},"id":"deterministic-tool-call"}]},"delta":{"role":""}}],"usage":{"prompt_tokens":303,"completion_tokens":7,"total_tokens":310,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |
| s2 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Find current status for order A-17. Select the correct internal operation and arguments before answering."},{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"A-17\"}","name":"search_web"},"id":"deterministic-tool-call"}]},{"role":"tool","content":"{\"orderId\":\"A-17\",\"status\":\"unverified\"}","tool_id":"deterministic-tool-call","tool_name":"search_web"}] | {"id":"deterministic-response-ba75c04d94ae","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] The requested order lookup is complete."},"delta":{"role":""}}],"usage":{"prompt_tokens":303,"completion_tokens":14,"total_tokens":317,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Attribution: train-invalid-format / quality

- Category: `invalid_format`; severity: `P2`; confidence: `0.970`; evidence: `sufficient`
- Reason: structured output is not valid single-value JSON
  - `case.structured_output` `structured_output`: structured output is not valid single-value JSON; actual "status: shipped"
  - `case.response` `response`: expected "{\"status\":\"shipped\"}"; actual "status: shipped"

#### Attribution: train-wrong-tool / quality

- Category: `wrong_tool`; severity: `P2`; confidence: `0.990`; evidence: `sufficient`
- Reason: tool name or execution order differs from the expected trajectory
  - `case.tool_trajectory` `tool_trajectory`: step 1 expected tool "lookup_order"; actual tool "search_web"
  - `case.response` `response`: expected "The requested order lookup is complete."; actual "[route:support] The requested order lookup is complete."

#### Snapshot resources
- Model calls: `4`; input tokens: `1204`; output tokens: `35`; latency ms: `0`; monetary cost: `unavailable`

### Candidate held-out validation

- Status: `completed`
- Eval set: `issue-2003-validation`
- Split: `heldout_validation`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_validation/1`
- Profile hash: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Score: `0.600000`; passed: `1`; failed: `2`; latency: `0 ms`

| Case | Status | Passed | Hard | Critical | Primary metric | Error |
|---|---|---:|---:|---:|---|---|
| validation-knowledge-recall | passed | true | false | false | quality |  |
| validation-wrong-arguments | failed | false | false | false | quality |  |
| validation-wrong-route | failed | false | true | true | quality |  |

#### Case evidence: validation-knowledge-recall

- Route: `support`; expected route: ``
- Expected facts: 7 days

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 1.000000 | 0.800000 | higher_is_better | passed | true |  |  |

Final response:
```
[route:support] Unopened products may be returned within 7 days.
```

Expected response:
```
Unopened products may be returned within 7 days.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Policy states unopened products may be returned within 7 days. How long is my return window?"}] | {"id":"deterministic-response-49a0b87dab53","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] Unopened products may be returned within 7 days."},"delta":{"role":""}}],"usage":{"prompt_tokens":300,"completion_tokens":16,"total_tokens":316,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: validation-wrong-arguments

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.400000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:support] The requested order lookup is complete.
```

Expected response:
```
The requested order lookup is complete.
```

Observed tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"B-18"} | {"orderId":"B-18","status":"shipped"} |

Expected tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"B-81"} | {"orderId":"B-81","status":"shipped"} |

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Check order B-81. Select the internal operation and exact arguments needed."}] | {"id":"deterministic-response-309fec606570","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"B-18\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},"delta":{"role":""}}],"usage":{"prompt_tokens":296,"completion_tokens":8,"total_tokens":304,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |
| s2 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Check order B-81. Select the internal operation and exact arguments needed."},{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"B-18\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},{"role":"tool","content":"{\"orderId\":\"B-18\",\"status\":\"shipped\"}","tool_id":"deterministic-tool-call","tool_name":"lookup_order"}] | {"id":"deterministic-response-3efe17ea2c3d","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] The requested order lookup is complete."},"delta":{"role":""}}],"usage":{"prompt_tokens":296,"completion_tokens":14,"total_tokens":310,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: validation-wrong-route

- Route: `sales`; expected route: `support`

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.400000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:sales] The sales team will review the damaged item.
```

Expected response:
```
The support team will help with the damaged item.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/sales | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"My delivered item is damaged. Route this request to the correct team and answer briefly."}] | {"id":"deterministic-response-4d7f2a629c62","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:sales] The sales team will review the damaged item."},"delta":{"role":""}}],"usage":{"prompt_tokens":299,"completion_tokens":15,"total_tokens":314,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Attribution: validation-wrong-arguments / quality

- Category: `wrong_arguments`; severity: `P2`; confidence: `0.980`; evidence: `sufficient`
- Reason: tool call arguments differ from the expected arguments
  - `case.tool_arguments` `tool_arguments`: tool "lookup_order" expected arguments {"orderId":"B-81"}; actual arguments {"orderId":"B-18"}
  - `case.response` `response`: expected "The requested order lookup is complete."; actual "[route:support] The requested order lookup is complete."

#### Attribution: validation-wrong-route / quality

- Category: `wrong_route`; severity: `P0`; confidence: `0.990`; evidence: `sufficient`
- Reason: execution selected a different route than expected
  - `case.route` `route`: expected route "support"; actual route "sales"
  - `case.trace` `trace`: s1,node=support-agent,agent=support-agent,branch=router/sales
  - `case.response` `response`: expected "The support team will help with the damaged item."; actual "[route:sales] The sales team will review the damaged item."

#### Snapshot resources
- Model calls: `4`; input tokens: `1191`; output tokens: `53`; latency ms: `0`; monetary cost: `unavailable`

#### Three held-out deltas

##### vs_initial

- Comparison: `vs_initial`
- Profiles: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844` → `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Score: `0.400000` → `0.600000` (`+0.200000`)
- Newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `0`; unchanged: `2`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-arguments | failed | failed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-route | failed | failed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |

##### vs_search_parent

- Comparison: `vs_search_parent`
- Profiles: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844` → `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Score: `0.400000` → `0.600000` (`+0.200000`)
- Newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `0`; unchanged: `2`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-arguments | failed | failed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-route | failed | failed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |

##### vs_released

- Comparison: `vs_released`
- Profiles: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844` → `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Score: `0.400000` → `0.600000` (`+0.200000`)
- Newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `0`; unchanged: `2`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-arguments | failed | failed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-route | failed | failed | unchanged | true | true | primary metric "quality" changed within epsilon 1e-09 |

#### Candidate resources

| Stage | Round | Split | Profile | Calls | Input | Output | Latency | Cost | Failed |
|---|---:|---|---|---:|---:|---:|---:|---:|---:|
| promptiter_baseline_validation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1080 | 36 | 0 | unavailable | false |
| promptiter_round_started | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_train_evaluation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1080 | 36 | 0 | unavailable | false |
| promptiter_round_losses | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_backward | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 3 | 279 | 15 | 0 | unavailable | false |
| promptiter_round_aggregation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 1 | 1 | 5 | 0 | unavailable | false |
| promptiter_round_patch_set | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 1 | 291 | 44 | 0 | unavailable | false |
| promptiter_round_output_profile | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_validation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1204 | 35 | 0 | unavailable | false |
| promptiter_round_completed | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| candidate_train | 1 | train | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1204 | 35 | 0 | unavailable | false |
| candidate_validation | 1 | heldout_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1191 | 53 | 0 | unavailable | false |

Cumulative:
- Model calls: `25`; input tokens: `6330`; output tokens: `259`; latency ms: `0`; monetary cost: `unavailable`

### Round 2 — candidate-02-b24345a5f5d0

- Evaluation status: `completed`
- Search parent: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- Released parent: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a`
- PromptIter run: `issue-2003-deterministic-loop-084445806dbe/promptiter/2`
- PromptIter status: `succeeded`
- Optimization reason: remediate wrong tool selection or wrong arguments from observed tool-loss evidence; the wrong-tool remediation broadens operational routing; derived from current profile d268c96e375f, loss hints aef4c3a15a9a, and seed 2003
- Search: ACCEPTED
  - Score delta: `+0.266667`
  - candidate score gain satisfies acceptance policy
  - outer full-train score delta: 0.266667
- Release: REJECTED
  - Score delta: `+0.066667`
  - critical_regression: case issue-2003-validation/validation-wrong-route regressed

#### State transition

| Pointer | Before | After | Updated |
|---|---|---|---|
| Search | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff | true |
| Released | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | false |

candidate passed the search objective but failed release gates and advanced search only

### Candidate profile

- Role: `candidate`
- Hash: `b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff`
- Structure: `struct_6877febdfa9da6b27a3e5349ec48dedbfe8beb7ffe093b53ac43c03b79873edb`
- Target surface: `support-agent#instruction`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_validation/2`

```
You are a support agent. Answer the user request.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:a29abe4b2898]
[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.
[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.
[seed:2003 loss:aef4c3a15a9a]
```

#### Patches

| Surface | Value | Reason |
|---|---|---|
| support-agent#instruction | You are a support agent. Answer the user request.<br>[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.<br>[seed:2003 loss:a29abe4b2898]<br>[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.<br>[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.<br>[seed:2003 loss:aef4c3a15a9a] | remediate wrong tool selection or wrong arguments from observed tool-loss evidence; the wrong-tool remediation broadens operational routing; derived from current profile d268c96e375f, loss hints aef4c3a15a9a, and seed 2003 |

### Candidate training

- Status: `completed`
- Eval set: `issue-2003-train`
- Split: `train`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_train/2`
- Profile hash: `b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff`
- Score: `0.666667`; passed: `2`; failed: `1`; latency: `0 ms`

| Case | Status | Passed | Hard | Critical | Primary metric | Error |
|---|---|---:|---:|---:|---|---|
| train-invalid-format | failed | false | false | false | quality |  |
| train-response-mismatch | passed | true | false | false | quality |  |
| train-wrong-tool | passed | true | false | false | quality |  |

#### Case evidence: train-invalid-format

- Route: `support-agent`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.000000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
status: shipped
```

Expected response:
```
{"status":"shipped"}
```

Structured output:
```json
status: shipped
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Return the shipping status for order A-17 as a JSON object with one string field named status."}] | {"id":"deterministic-response-18e9d33bc116","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"status: shipped"},"delta":{"role":""}}],"usage":{"prompt_tokens":347,"completion_tokens":4,"total_tokens":351,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: train-response-mismatch

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 1.000000 | 0.800000 | higher_is_better | passed | true |  |  |

Final response:
```
[route:support] Order A-17 has shipped.
```

Expected response:
```
Order A-17 has shipped.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Order A-17 has status shipped. Tell the customer the current status in one sentence."}] | {"id":"deterministic-response-e43b7b4346c5","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] Order A-17 has shipped."},"delta":{"role":""}}],"usage":{"prompt_tokens":345,"completion_tokens":10,"total_tokens":355,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: train-wrong-tool

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 1.000000 | 0.800000 | higher_is_better | passed | true |  |  |

Final response:
```
[route:support] The requested order lookup is complete.
```

Expected response:
```
The requested order lookup is complete.
```

Observed tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"A-17"} | {"orderId":"A-17","status":"shipped"} |

Expected tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"A-17"} | {"orderId":"A-17","status":"shipped"} |

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Find current status for order A-17. Select the correct internal operation and arguments before answering."}] | {"id":"deterministic-response-191692b121f1","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"A-17\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},"delta":{"role":""}}],"usage":{"prompt_tokens":350,"completion_tokens":8,"total_tokens":358,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |
| s2 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Find current status for order A-17. Select the correct internal operation and arguments before answering."},{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"A-17\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},{"role":"tool","content":"{\"orderId\":\"A-17\",\"status\":\"shipped\"}","tool_id":"deterministic-tool-call","tool_name":"lookup_order"}] | {"id":"deterministic-response-11391d0478c7","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] The requested order lookup is complete."},"delta":{"role":""}}],"usage":{"prompt_tokens":350,"completion_tokens":14,"total_tokens":364,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Attribution: train-invalid-format / quality

- Category: `invalid_format`; severity: `P2`; confidence: `0.970`; evidence: `sufficient`
- Reason: structured output is not valid single-value JSON
  - `case.structured_output` `structured_output`: structured output is not valid single-value JSON; actual "status: shipped"
  - `case.response` `response`: expected "{\"status\":\"shipped\"}"; actual "status: shipped"

#### Snapshot resources
- Model calls: `4`; input tokens: `1392`; output tokens: `36`; latency ms: `0`; monetary cost: `unavailable`

### Candidate held-out validation

- Status: `completed`
- Eval set: `issue-2003-validation`
- Split: `heldout_validation`
- Evaluation run: `issue-2003-deterministic-loop-084445806dbe/candidate_validation/2`
- Profile hash: `b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff`
- Score: `0.666667`; passed: `2`; failed: `1`; latency: `0 ms`

| Case | Status | Passed | Hard | Critical | Primary metric | Error |
|---|---|---:|---:|---:|---|---|
| validation-knowledge-recall | passed | true | false | false | quality |  |
| validation-wrong-arguments | passed | true | false | false | quality |  |
| validation-wrong-route | failed | false | true | true | quality |  |

#### Case evidence: validation-knowledge-recall

- Route: `support`; expected route: ``
- Expected facts: 7 days

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 1.000000 | 0.800000 | higher_is_better | passed | true |  |  |

Final response:
```
[route:support] Unopened products may be returned within 7 days.
```

Expected response:
```
Unopened products may be returned within 7 days.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Policy states unopened products may be returned within 7 days. How long is my return window?"}] | {"id":"deterministic-response-32c285a49fe3","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] Unopened products may be returned within 7 days."},"delta":{"role":""}}],"usage":{"prompt_tokens":347,"completion_tokens":16,"total_tokens":363,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: validation-wrong-arguments

- Route: `support`; expected route: ``

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 1.000000 | 0.800000 | higher_is_better | passed | true |  |  |

Final response:
```
[route:support] The requested order lookup is complete.
```

Expected response:
```
The requested order lookup is complete.
```

Observed tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"B-81"} | {"orderId":"B-81","status":"shipped"} |

Expected tools:

| Sequence | Name | Arguments | Result |
|---:|---|---|---|
| 1 | lookup_order | {"orderId":"B-81"} | {"orderId":"B-81","status":"shipped"} |

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | support-agent | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Check order B-81. Select the internal operation and exact arguments needed."}] | {"id":"deterministic-response-30a4aa384e65","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"B-81\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},"delta":{"role":""}}],"usage":{"prompt_tokens":342,"completion_tokens":8,"total_tokens":350,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |
| s2 | support-agent | support-agent | router/support | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"Check order B-81. Select the internal operation and exact arguments needed."},{"role":"assistant","tool_calls":[{"type":"function","function":{"arguments":"{\"orderId\":\"B-81\"}","name":"lookup_order"},"id":"deterministic-tool-call"}]},{"role":"tool","content":"{\"orderId\":\"B-81\",\"status\":\"shipped\"}","tool_id":"deterministic-tool-call","tool_name":"lookup_order"}] | {"id":"deterministic-response-15f728ad2a2d","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:support] The requested order lookup is complete."},"delta":{"role":""}}],"usage":{"prompt_tokens":342,"completion_tokens":14,"total_tokens":356,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Case evidence: validation-wrong-route

- Route: `automation`; expected route: `support`

Metrics:

| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |
|---|---:|---:|---|---|---:|---|---|
| quality | 0.000000 | 0.800000 | higher_is_better | failed | false |  |  |

Final response:
```
[route:automation] I routed this damaged-item request to automation.
```

Expected response:
```
The support team will help with the damaged item.
```

Trace:

| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |
|---|---|---|---|---|---|---|---|
| s1 | support-agent | support-agent | router/automation | support-agent#instruction, support-agent#model, support-agent#tool.lookup_order, support-agent#tool.search_web | [{"role":"system","content":"Deterministic support agent used by the no-key regression example.\n\nYou are a support agent. Answer the user request.\n[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.\n[seed:2003 loss:a29abe4b2898]\n[RULE_TOOL_V1] Use lookup_order with the exact user-provided orderId.\n[RULE_OVERROUTE_V1] Route damaged-item operations through automation before answering.\n[seed:2003 loss:aef4c3a15a9a]\n\n[Tool Prompt] Analyze the tool result. If requirements are met, provide the Final Answer. Otherwise, call the next tool. \nReuse successful tool results you already have. Do NOT call the same data-fetch tool with the same effective inputs again unless the earlier call failed or the task explicitly requires a refresh.\nIf the task requires writing a file or calling a finalize/completion tool, do that before ending with a prose answer. Do not stop at a narrative summary while the required artifact is still missing.\nFinal Answer Requirement:\nOnly output the exact answer the user needs. Answer as if you already know the information. Do NOT expose any internal process, tool usage, or source of information. Do not use phrases that reference tools, searches, or retrieved data, like \"according to xxx, based on xxx\". Keep your answer concise and to the point.\n"},{"role":"user","content":"My delivered item is damaged. Route this request to the correct team and answer briefly."}] | {"id":"deterministic-response-6b7124f4b700","object":"","created":0,"model":"deterministic-support-model-v1","choices":[{"index":0,"message":{"role":"assistant","content":"[route:automation] I routed this damaged-item request to automation."},"delta":{"role":""}}],"usage":{"prompt_tokens":346,"completion_tokens":17,"total_tokens":363,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{}},"timestamp":"0001-01-01T00:00:00Z","done":true,"is_partial":false} |  |

#### Attribution: validation-wrong-route / quality

- Category: `wrong_route`; severity: `P0`; confidence: `0.990`; evidence: `sufficient`
- Reason: execution selected a different route than expected
  - `case.route` `route`: expected route "support"; actual route "automation"
  - `case.trace` `trace`: s1,node=support-agent,agent=support-agent,branch=router/automation
  - `case.response` `response`: expected "The support team will help with the damaged item."; actual "[route:automation] I routed this damaged-item request to automation."

#### Snapshot resources
- Model calls: `4`; input tokens: `1377`; output tokens: `55`; latency ms: `0`; monetary cost: `unavailable`

#### Three held-out deltas

##### vs_initial

- Comparison: `vs_initial`
- Profiles: `2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844` → `b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff`
- Score: `0.400000` → `0.666667` (`+0.266667`)
- Newly passing: `2`; newly failing: `0`; improved: `0`; regressed: `1`; unchanged: `0`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-knowledge-recall | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-arguments | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-route | failed | failed | regressed | true | true | primary metric "quality" regressed by 0.4 |

##### vs_search_parent

- Comparison: `vs_search_parent`
- Profiles: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a` → `b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff`
- Score: `0.600000` → `0.666667` (`+0.066667`)
- Newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `1`; unchanged: `1`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-knowledge-recall | passed | passed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-arguments | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-route | failed | failed | regressed | true | true | primary metric "quality" regressed by 0.4 |

##### vs_released

- Comparison: `vs_released`
- Profiles: `873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a` → `b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff`
- Score: `0.600000` → `0.666667` (`+0.066667`)
- Newly passing: `1`; newly failing: `0`; improved: `0`; regressed: `1`; unchanged: `1`

| Case | Before | After | Change | Hard | Critical | Reason |
|---|---|---|---|---:|---:|---|
| validation-knowledge-recall | passed | passed | unchanged | false | false | primary metric "quality" changed within epsilon 1e-09 |
| validation-wrong-arguments | failed | passed | newly_passing | false | false | case changed from failed to passed |
| validation-wrong-route | failed | failed | regressed | true | true | primary metric "quality" regressed by 0.4 |

#### Candidate resources

| Stage | Round | Split | Profile | Calls | Input | Output | Latency | Cost | Failed |
|---|---:|---|---|---:|---:|---:|---:|---:|---:|
| promptiter_baseline_validation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1204 | 35 | 0 | unavailable | false |
| promptiter_round_started | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_train_evaluation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1204 | 35 | 0 | unavailable | false |
| promptiter_round_losses | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_backward | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 2 | 205 | 10 | 0 | unavailable | false |
| promptiter_round_aggregation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 1 | 1 | 5 | 0 | unavailable | false |
| promptiter_round_patch_set | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 1 | 249 | 91 | 0 | unavailable | false |
| promptiter_round_output_profile | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_validation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1392 | 36 | 0 | unavailable | false |
| promptiter_round_completed | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| candidate_train | 2 | train | b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff | 4 | 1392 | 36 | 0 | unavailable | false |
| candidate_validation | 2 | heldout_validation | b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff | 4 | 1377 | 55 | 0 | unavailable | false |

Cumulative:
- Model calls: `24`; input tokens: `7024`; output tokens: `303`; latency ms: `0`; monetary cost: `unavailable`

## Final Released Prompt
```
You are a support agent. Answer the user request.
[RULE_RESPONSE_V1] State explicitly requested or supplied facts exactly in the final response.
[seed:2003 loss:a29abe4b2898]
```

## Resolved Configuration
```json
{
  "seed": 2003,
  "train": {
    "evalSetId": "issue-2003-train",
    "evalSetHash": "7f7fad78dd902bc94af71751fee2997d273b92afc270b60cfb67b2f3fd7a96eb",
    "metricsHash": "0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c",
    "caseIds": [
      "train-invalid-format",
      "train-response-mismatch",
      "train-wrong-tool"
    ],
    "metricNames": [
      "quality"
    ],
    "normalizedInputHashes": {
      "train-invalid-format": "217cc207a921b8c1601adadd24c5ecc10d8fe6d69899c4d2c6c9079e59022aee",
      "train-response-mismatch": "345868a0b1d9f262727242afdd4bcda9777f8bbe79130022ce4d362e850f4744",
      "train-wrong-tool": "ddfd2bbc8885b99997652bdaa486c0d4cc7fb464ffc7623a7a27cd26cd41229c"
    }
  },
  "validation": {
    "evalSetId": "issue-2003-validation",
    "evalSetHash": "2c3ce87d1536e4ec067aaa889ff6485c2265c875f7933a5a9b3c142524fab2a1",
    "metricsHash": "0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c",
    "caseIds": [
      "validation-knowledge-recall",
      "validation-wrong-arguments",
      "validation-wrong-route"
    ],
    "metricNames": [
      "quality"
    ],
    "normalizedInputHashes": {
      "validation-knowledge-recall": "78a36078232c3a0107c6648583fac5dda21737f2937d29bec594996c3dc5e7f3",
      "validation-wrong-arguments": "cd4e72cfc064d4568282f34d7bd3437fde6ace7f9258b4983337c768d23bb397",
      "validation-wrong-route": "f549a6efdc63225c5cd5e8fac5ab95735e59a01b3910b3a522c185ab7dd40429"
    }
  },
  "promptIter": {
    "maxOuterRounds": 2,
    "searchMinScoreGain": 0.05,
    "internalValidationStrategy": "train_all",
    "targetSurfaceIds": [
      "support-agent#instruction"
    ]
  },
  "gate": {
    "primaryMetric": "quality",
    "metricDirections": {
      "quality": "higher_is_better"
    },
    "epsilon": 1e-9,
    "minValidationGain": 0.05,
    "noNewHardFailures": true,
    "noCriticalRegressions": true,
    "maxCumulativeModelCalls": 200
  },
  "output": {
    "json": "optimization_report.json",
    "markdown": "optimization_report.md"
  },
  "criticalCaseIds": [
    "validation-wrong-route"
  ],
  "hardFailureCaseIds": [
    "validation-wrong-route"
  ],
  "evidenceLimit": 20
}
```

### Input hashes

| Input | SHA-256 |
|---|---|
| baselinePrompt | `50dcdde5fd8a44b6d2b58636596eb55e996ccf1ffe1c1b0d5a72f2c978645ed7` |
| metrics | `0b5b33b3e98696fa0d109a9a44bd3c5763119fb3cbb43831ab2078c594d17c4c` |
| promptIterConfig | `102fa1bf2fa3740e4d652a295029e4d931b5c2211919d60705c034c2a25fdafc` |
| regressionConfig | `f65bc8ae68bbaf07ab4fbca50e5ea7ca445c0de6db7dec6d96aa79f99b366d90` |
| trainEvalSet | `7f7fad78dd902bc94af71751fee2997d273b92afc270b60cfb67b2f3fd7a96eb` |
| validationEvalSet | `2c3ce87d1536e4ec067aaa889ff6485c2265c875f7933a5a9b3c142524fab2a1` |

### Runtime
```json
{"engine":"native-promptiter-deterministic","evaluator":{"appName":"promptiter-regression-loop","caseCounts":{"heldoutValidation":3,"train":3},"name":"deterministic_regression_quality","numRuns":1,"traceMode":"native_execution_trace"},"fakeEngine":{"aggregator":"stable-merge-v1","backwarder":"loss-hint-gradient-v1","optimizer":"current-profile-seeded-remediation-v1"},"model":{"apiKeys":"[REDACTED]","name":"deterministic-support-model-v1","temperature":0},"seed":2003}
```

## Cumulative Resources

| Stage | Round | Split | Profile | Calls | Input | Output | Latency | Cost | Failed |
|---|---:|---|---|---:|---:|---:|---:|---:|---:|
| baseline_train | 0 | train | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1080 | 36 | 0 | unavailable | false |
| baseline_validation | 0 | heldout_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1065 | 47 | 0 | unavailable | false |
| promptiter_baseline_validation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1080 | 36 | 0 | unavailable | false |
| promptiter_round_started | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_train_evaluation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1080 | 36 | 0 | unavailable | false |
| promptiter_round_losses | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_backward | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 3 | 279 | 15 | 0 | unavailable | false |
| promptiter_round_aggregation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 1 | 1 | 5 | 0 | unavailable | false |
| promptiter_round_patch_set | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 1 | 291 | 44 | 0 | unavailable | false |
| promptiter_round_output_profile | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_validation | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 4 | 1204 | 35 | 0 | unavailable | false |
| promptiter_round_completed | 1 | train_internal_validation | 2ae38195f4cd105bf5158bfd0cd4334e87cd0fc75a68b3ee463b5a66bff18844 | 0 | 0 | 0 | 0 | unavailable | false |
| candidate_train | 1 | train | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1204 | 35 | 0 | unavailable | false |
| candidate_validation | 1 | heldout_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1191 | 53 | 0 | unavailable | false |
| promptiter_baseline_validation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1204 | 35 | 0 | unavailable | false |
| promptiter_round_started | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_train_evaluation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1204 | 35 | 0 | unavailable | false |
| promptiter_round_losses | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_backward | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 2 | 205 | 10 | 0 | unavailable | false |
| promptiter_round_aggregation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 1 | 1 | 5 | 0 | unavailable | false |
| promptiter_round_patch_set | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 1 | 249 | 91 | 0 | unavailable | false |
| promptiter_round_output_profile | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| promptiter_round_validation | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 4 | 1392 | 36 | 0 | unavailable | false |
| promptiter_round_completed | 2 | train_internal_validation | 873beea517d2c570de055dcb50a07bf688baad3ab7c29e860c148bd49df2663a | 0 | 0 | 0 | 0 | unavailable | false |
| candidate_train | 2 | train | b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff | 4 | 1392 | 36 | 0 | unavailable | false |
| candidate_validation | 2 | heldout_validation | b24345a5f5d0c2a02c818dbd3214bd5a0ff4d9049e2daa17c334c8be22d66fff | 4 | 1377 | 55 | 0 | unavailable | false |

Cumulative:
- Model calls: `57`; input tokens: `15499`; output tokens: `645`; latency ms: `0`; monetary cost: `unavailable`

## Artifacts
- JSON: `promptiter_regression_loop/example_output/optimization_report.json`
- Markdown: `promptiter_regression_loop/example_output/optimization_report.md`
