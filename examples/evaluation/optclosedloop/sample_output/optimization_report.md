# Optimization Report

- **App**: `trpc-agent-go-optclosedloop-demo`
- **Mode**: `fake_deterministic`  
- **Pipeline version**: `v0.1.0-closedloop`  
- **Random seed**: `20250101`  
- **Window**: 2026-07-31T22:39:42+08:00 → 2026-07-31T22:39:42+08:00  
- **Duration**: 0.00s  
- **Final accepted**: `true`  
- **Best validation score**: `0.9667` (round 1)  
- **Final validation score**: `0.9667`  

## Gate configuration

- min_validation_score_gain: `+0.0500`
- allow_new_hard_fail: `false`
- key_case_ids: `[]`
- max_cost_budget_usd: `0.0000`

## Baseline evaluation

#### Train (baseline)

- score: **0.6500**  
- cases: total=3 / pass=1 / fail=2

| Case | Pass | Score | Reason |
|---|---|---|---|
| train_case_opt_01 | false | 0.45 | final_response_mismatch: baseline agent hedges instead of answering; expected an… |
| train_case_opt_02 | false | 0.50 | tool_argument_error: baseline passes string operand to calculator; expects numer… |
| train_case_opt_03 | true | 1.00 |  |

Failure attribution:

| Case | Metric | Category | Reason |
|---|---|---|---|
| train_case_opt_01 | overall | final_response_mismatch | final_response_mismatch: baseline agent hedges instead of answering; expected an… |
| train_case_opt_02 | overall | tool_argument_error | tool_argument_error: baseline passes string operand to calculator; expects numer… |

#### Validation (baseline)

- score: **0.7833**  
- cases: total=3 / pass=2 / fail=1

| Case | Pass | Score | Reason |
|---|---|---|---|
| val_case_opt_01 | false | 0.40 | knowledge_recall_insufficient: baseline fails to remember the skill policy; shou… |
| val_case_opt_02 | true | 0.95 |  |
| val_case_opt_03 | true | 1.00 |  |

Failure attribution:

| Case | Metric | Category | Reason |
|---|---|---|---|
| val_case_opt_01 | overall | knowledge_recall_insufficient | knowledge_recall_insufficient: baseline fails to remember the skill policy; shou… |

## Optimization rounds

### Round 1

- timestamp: 2026-07-31T22:39:42+08:00
- seed: 20250132
- candidate: `cand_r1_434792` (by `promptiter_fake_deterministic`)  
  - patched surface `system_prompt`  
- rationale: Top failed cases are FinalResponseMismatch and KnowledgeRecallInsufficient; tighten system-level answer policy. Attribution signal: final_response_mismatch=1, tool_argument_error=1

#### Validation (candidate)

- score: **0.9667**  
- cases: total=3 / pass=3 / fail=0

| Case | Pass | Score | Reason |
|---|---|---|---|
| val_case_opt_01 | true | 0.95 |  |
| val_case_opt_02 | true | 0.95 |  |
| val_case_opt_03 | true | 1.00 |  |

#### Acceptance: **ACCEPTED** (score_delta=+0.1833)

- score_delta=+0.1833 meets min_score_gain=+0.0500
- no newly introduced hard fails (count=0)
- no implicit key case degradations (count=0)
- cost budget disabled (MaxCostBudget=0)

Per-case deltas:

| Case | Baseline | Cand | Δ | NewHardFail | KeyDegrade |
|---|---|---|---|---|---|
| val_case_opt_01 | 0.40 / false | 0.95 / true | +0.55 | false | false |
| val_case_opt_02 | 0.95 / true | 0.95 / true | +0.00 | false | false |
| val_case_opt_03 | 1.00 / true | 1.00 / true | +0.00 | false | false |

- cost: tokens=10500, usd=$0.03150, wall=0.00s

### Round 2

- timestamp: 2026-07-31T22:39:42+08:00
- seed: 20250163
- candidate: `cand_r2_115610` (by `promptiter_fake_deterministic`)  
  - patched surface `tool_desc_calc`  
- rationale: Train attribution shows ToolArgumentError on calculator; patch tool description to stress numeric args only. Attribution signal: tool_argument_error=1

#### Validation (candidate)

- score: **0.9667**  
- cases: total=3 / pass=3 / fail=0

| Case | Pass | Score | Reason |
|---|---|---|---|
| val_case_opt_01 | true | 0.95 |  |
| val_case_opt_02 | true | 0.95 |  |
| val_case_opt_03 | true | 1.00 |  |

#### Acceptance: **REJECTED** (score_delta=+0.0000)

- score_delta=+0.0000 below min_score_gain=+0.0500 (REJECT)
- no newly introduced hard fails (count=0)
- no implicit key case degradations (count=0)
- cost budget disabled (MaxCostBudget=0)

Per-case deltas:

| Case | Baseline | Cand | Δ | NewHardFail | KeyDegrade |
|---|---|---|---|---|---|
| val_case_opt_01 | 0.95 / true | 0.95 / true | +0.00 | false | false |
| val_case_opt_02 | 0.95 / true | 0.95 / true | +0.00 | false | false |
| val_case_opt_03 | 1.00 / true | 1.00 / true | +0.00 | false | false |

- cost: tokens=12000, usd=$0.03600, wall=0.00s

### Round 3

- timestamp: 2026-07-31T22:39:42+08:00
- seed: 20250194
- candidate: `cand_r3_605762` (by `promptiter_fake_deterministic`)  
  - patched surface `router_prompt`  
- rationale: Round 2 did not move the needle. Try router-level guidance: always route email tasks to EmailAgent instead of MathAgent. Attribution signal: tool_argument_error=1

#### Validation (candidate)

- score: **0.8333**  
- cases: total=3 / pass=2 / fail=1

| Case | Pass | Score | Reason |
|---|---|---|---|
| val_case_opt_01 | true | 0.95 |  |
| val_case_opt_02 | false | 0.55 | route_error: candidate over-optimized routing; sends email task to MathAgent |
| val_case_opt_03 | true | 1.00 |  |

Failure attribution:

| Case | Metric | Category | Reason |
|---|---|---|---|
| val_case_opt_02 | overall | route_error | route_error: candidate over-optimized routing; sends email task to MathAgent |

#### Acceptance: **REJECTED** (score_delta=-0.1333)

- score_delta=-0.1333 below min_score_gain=+0.0500 (REJECT)
- introduced 1 new hard fail(s); gate AllowNewHardFail=false (REJECT)
- no implicit key case degradations (count=0)
- cost budget disabled (MaxCostBudget=0)

Per-case deltas:

| Case | Baseline | Cand | Δ | NewHardFail | KeyDegrade |
|---|---|---|---|---|---|
| val_case_opt_01 | 0.95 / true | 0.95 / true | +0.00 | false | false |
| val_case_opt_02 | 0.95 / true | 0.55 / false | -0.40 | true | false |
| val_case_opt_03 | 1.00 / true | 1.00 / true | +0.00 | false | false |

- cost: tokens=13500, usd=$0.04050, wall=0.00s

## Final prompts

### system_prompt

```
# Optimized System Prompt
You are a precise and honest agent. For factual questions you MUST provide the direct answer and cite source when available; NEVER hedge ('I don't know') unless the knowledge is genuinely absent. Always end your response with a clear, concise final answer.
```

### agent_instruction

```
# Agent Instruction (baseline)
Use the available tools to gather evidence, then synthesize a final answer. Always cite your sources when possible.
```

### router_prompt

```
# Router Prompt (baseline)
Choose between MathAgent, EmailAgent, or GeneralAgent. When in doubt, route to GeneralAgent.
```

### tool_desc_calc

```
# Tool: calculator (baseline)
Performs arithmetic. Takes a, b, op.
```

## Notes

- round 1: accepted candidate cand_r1_434792 (Δ=+0.1833)
- round 2: rejected candidate cand_r2_115610 (Δ=+0.0000); reason: score_delta=+0.0000 below min_score_gain=+0.0500 (REJECT)
- round 3: rejected candidate cand_r3_605762 (Δ=-0.1333); reason: score_delta=-0.1333 below min_score_gain=+0.0500 (REJECT)
