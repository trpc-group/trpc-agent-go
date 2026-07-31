# optclosedloop: Evaluation + Prompt Optimization Closed Loop (Issue #2003)

> Self-contained reference implementation of the "评测—失败归因—prompt 优化—回归验证—产物审计"
> closed-loop pipeline built on top of the tRPC-Agent-Go `evaluation` framework and
> `evaluation/workflow/promptiter` abstractions.

## What it delivers

1. **Baseline evaluation** — `BaselineEvaluator` produces deterministic per-case
   results (score, pass/fail, reason, session/trace ID, final response, tool
   trajectory) for train and validation sets.
2. **Failure attribution** — `AttributeFailures` classifies every failing metric
   into six pre-defined categories (`final_response_mismatch`, `tool_call_error`,
   `tool_argument_error`, `route_error`, `format_error`,
   `knowledge_recall_insufficient`) with severity, evidence, and human-readable
   reason pulled from trace + trajectory signals.
3. **PromptIter-style optimization** — `PromptOptimizer` proposes prompt
   candidates in a deterministic round-robin schedule over
   `system_prompt / tool_desc_calc / router_prompt / agent_instruction` and
   attaches a rationale grounded in the attribution summary. The integration
   surface is a `ProposeCandidate(ctx, round, trainSummary, attributions,
   currentPrompts)` contract that can be swapped for a real
   `evaluation/workflow/promptiter/engine.Engine` call.
4. **Candidate validation** — `ComputeCaseDelta` recomputes the validation set
   under the candidate and produces per-case deltas covering
   baseline-vs-candidate score, pass/fail transitions, newly introduced hard
   fails, and key-case degradation flags.
5. **Configurable acceptance gates** — `AcceptanceGates` implements the full
   required gate surface: `MinValidationScoreGain`,
   `AllowNewHardFail` (default false), `KeyCaseIDs` (explicit list or
   auto-detected via the `hardfail_guard` label), and `MaxCostBudget` in USD.
6. **Audit persistence** — `Auditer` writes `optimization_report.json` and
   `optimization_report.md` with the full round history (candidate prompts,
   eval results, deltas, accept/reject reasons, cost, timing, seed).

## Quick start

```
cd examples/evaluation/optclosedloop
go mod tidy
go run . -mode fake_deterministic -seed 20250101 -max-rounds 3 \
         -min-score-gain 0.05
```

The CLI is API-key free by default. It uses the `fake_deterministic` runner
where each eval case's outcome is programmed according to the case labels in
`data/train_evalset.json` / `data/val_evalset.json`. Operators can still flip to
`trace_mode` (trace replay) or `real` by swapping `BaselineEvaluator` and
`PromptOptimizer` to the live evaluation service + PromptIter engine via the
documented integration contracts.

## Sample eval cases (6 total, exactly the required split)

- Train (3):
  - `train_case_opt_01` — final_response_mismatch hedging → optimized
  - `train_case_opt_02` — tool_argument_error on calculator → optimized
  - `train_case_opt_03` — stable pass / no effect anchor
- Validation (3):
  - `val_case_opt_01` — knowledge_recall_insufficient → optimized
  - `val_case_opt_02` — route_error **degrades** under `router_prompt` patch
  - `val_case_opt_03` — `hardfail_guard` key-case / no-effect anchor

## Design notes (300–500 words)

The closed-loop pipeline layers three execution modes (`fake_deterministic`,
`trace_mode`, `real`) behind the same core orchestrator so that developers can
iterate on the pipeline itself without burning live tokens. The orchestrator
`Pipeline.Run` intentionally models the PromptIter engine contract
(engine.RunRequest → engine.RunResult) but decouples stages into composable,
testable units rather than requiring the heavy PromptIter engine stack up front.

Baseline evaluation separates train from validation exactly as the issue
requires: training cases drive attribution and candidate proposal; validation
cases exclusively feed the acceptance gate. Attribution runs a deterministic
cascade over structured signals — format check, tool errors, tool arg parse,
route, hedging/knowledge, final-response fallback — so the same rule set
generalises to trace_mode and live runs.

The PromptOptimizer runs a round-robin schedule over the four required prompt
surfaces. Rounds 1–3 each produce exactly one patch, which the baseline
evaluator maps onto programmed per-case deltas. That mapping is the critical
deterministic harness: round 1 `system_prompt` patch lifts `train_case_opt_01`
and `val_case_opt_01`; round 2 `tool_desc_calc` lifts `train_case_opt_02` with
no validation movement; round 3 `router_prompt` patch over-fits and pushes
`val_case_opt_02` from pass into `route_error`, triggering the
`AllowNewHardFail=false` reject gate. The sequence therefore demonstrates both
a successful acceptance (round 1) and a successful rejection (round 3) with a
clean `optimization_report.*` audit trail for every step.

Acceptance gates are additive and every gate's result is recorded individually
in the decision reasons, so operators can read from the markdown report
_exactly which gate_ rejected the candidate (score delta, new hard fail, key
case degradation, or cost). Cost and timing estimates are synthetic but
round-correlated to exercise the cost budget gate when enabled. The final
accepted profile, best round, and per-case delta tables are surfaced both in
the CLI summary and in the markdown report for the PR's required "产物审计"
artifact.
