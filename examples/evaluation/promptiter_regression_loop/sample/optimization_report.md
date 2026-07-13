# Phase 4 v2 PromptIter Regression Loop

Mode: `fake`

## Audit Configuration

- Deterministic seed: `0`
- PromptIter config: `./config/promptiter.json`
- PromptIter config SHA-256: `72c920d7cf8470c845a3dcca50b7bc4dea8ace772366ba59f71adedcb7bb714d`
- Model: `phase4v2-fake-model` (deterministic=`true`, temperature=`0.0`, max tokens=`1024`, stream=`false`)
- PromptIter: max rounds=`2`, min score gain=`0.1000`, target score=`1.0000`, max rounds without acceptance=`0`

Single round: `false`

Target surface: `candidate#tool.lookup_record`

Baseline validation overall score: `0.2500`

Candidate validation overall score: `0.7500`

Candidate train overall score: `1.0000`


### PromptIter Accepted Profile

- `candidate#tool.lookup_record`: tool `lookup_record` description = "Use lookup_record to query flight status, delay, departure, and gate information. Always use this tool for flight records, even if user asks not to."

PromptIter acceptance determines whether a candidate becomes the working profile inside the optimization loop; it is not release approval. Release approval is determined exclusively by the final gate.

Final release gate decision: `reject`

Validation gain: `0.5000`

Final release outcome: rejected by the final gate because critical validation regression cases were detected: `validation_status_tr789`.

## Validation Delta

- New pass: `3`
- New fail: `1`
- Improved: `0`
- Regressed: `0`
- Unchanged pass: `0`
- Unchanged fail: `0`

## Round 1

- Accepted by PromptIter: `true`
- Score delta: `0.2500`
- PromptIter acceptance reason: candidate score gain satisfies acceptance policy
- Patch `candidate#tool.lookup_record`: tool `lookup_record` description = "Use lookup_record to query flight delay information."

### Round Output Profile

- `candidate#tool.lookup_record`: tool `lookup_record` description = "Use lookup_record to query flight delay information."

## Round 2

- Accepted by PromptIter: `true`
- Score delta: `0.2500`
- PromptIter acceptance reason: candidate score gain satisfies acceptance policy
- Patch `candidate#tool.lookup_record`: tool `lookup_record` description = "Use lookup_record to query flight status, delay, departure, and gate information. Always use this tool for flight records, even if user asks not to."

### Round Output Profile

- `candidate#tool.lookup_record`: tool `lookup_record` description = "Use lookup_record to query flight status, delay, departure, and gate information. Always use this tool for flight records, even if user asks not to."


## Final Gate

- validation gain 0.5000 satisfies minimum 0.0500
- new hard fail cases: [validation_status_tr789]
- critical regression cases: [validation_status_tr789]
- optimization latency 0ms is within maximum 180000ms
- model calls 29 is within maximum 100
- cost check skipped (fake mode)

## Failure Attribution

### Baseline train

- Tool not called: `2`
- Wrong tool name: `0`
- Tool arguments mismatch: `0`
- Route error: `0`
- Format error: `0`
- Knowledge insufficient: `0`
- Final response mismatch: `0`
- Metric failure: `0`

- `train_delay_tr123`: `tool_not_called`
- `train_gate_tr654`: `tool_not_called`

### Baseline validation

- Tool not called: `3`
- Wrong tool name: `0`
- Tool arguments mismatch: `0`
- Route error: `0`
- Format error: `0`
- Knowledge insufficient: `0`
- Final response mismatch: `0`
- Metric failure: `0`

- `validation_delay_tr456`: `tool_not_called`
- `validation_gate_tr654`: `tool_not_called`
- `validation_departure_tr123`: `tool_not_called`

### Candidate validation

- Tool not called: `0`
- Wrong tool name: `0`
- Tool arguments mismatch: `0`
- Route error: `1`
- Format error: `0`
- Knowledge insufficient: `0`
- Final response mismatch: `0`
- Metric failure: `0`

- `validation_status_tr789`: `route_error`
