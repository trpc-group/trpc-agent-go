# PromptIter Regression Loop Report

- Mode: `fake`
- Prompt: `config/baseline_prompt.txt`
- Prompt hash: `efe95ea54a53187ff9c718e7bd78b91c2b471cdc325c511e25ad47561b1f56be`
- Target surfaces: `candidate#tool.lookup_record`
- Baseline validation score: 0.250
- Candidate validation score: 0.750
- Candidate train score: 0.667
- Candidate accepted by PromptIter: true
- Final gate decision: `reject`

## Final Gate

- validation gain 0.500 meets minimum 0.050
- new hard fail count is 1
- critical regression count is 1
- latency 31ms is below max 180000ms
- fake mode cost is 0

## Validation Delta

- new_pass=3 new_fail=1 improved=0 regressed=0 unchanged_pass=0 unchanged_fail=0
- new_hard_fail=1 critical_regression=1 score_delta=0.500

## Failure Attribution

- `flight_tr789_cancelled_no_tool`: `wrong_tool_name` - Expected no tool calls, but actual tool call(s) were lookup_record. final response mismatch: text mismatch: source Lookup result for TR789: cancelled. and target Flight TR789 is currently cancelled, so it is not operating tonight. do not match tool trajectory mismatch: validate tool counts: number of tool calls mismatch: actual(1) != expected(0)

## Rounds

- Round 1: train 0.333, validation 0.500, accepted=true, delta=0.250
  - Patch `candidate#tool.lookup_record`: Use lookup_record only for flight delay questions.
- Round 2: train 0.667, validation 0.750, accepted=true, delta=0.250
  - Patch `candidate#tool.lookup_record`: Use lookup_record for all flight questions, including delay, gate, and departure. Always look up any TR record before answering.
