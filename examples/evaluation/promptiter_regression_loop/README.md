# PromptIter Regression Loop Example (#2003)

This example implements an automated **Evaluation + Optimization Closed-Loop Pipeline** for `tRPC-Agent-Go` (Tencent Rhinoceros Bird Open Source Program Issue #2003).

## Problem & Value

In production AI Agent development, evaluation and prompt optimization must not be disconnected:
1. **Overfitting Risk**: Optimizing prompts solely on a training set can degrade accuracy on validation sets or introduce hard failures on critical edge cases.
2. **Lack of Audit Trail**: Unaudited prompt patches cannot safely enter production workflows.

This pipeline closes the loop by performing **Baseline Evaluation → Causal Failure Attribution → PromptIter Optimization → Validation Set Regression → Multi-Gate Acceptance → Structured Audit Persistence**.

---

## Architecture & Pipeline Stages

```text
                               ┌───────────────────────────┐
                               │  train_evalset.json       │
                               │  val_evalset.json         │
                               └─────────────┬─────────────┘
                                             │
                                             ▼
┌───────────────────────┐         ┌─────────────────────┐
│  Baseline Evaluator   │────────▶│ Failure Attributor  │
│  (Go Eval Service)    │         │ (6 Failure Categories)
└───────────────────────┘         └──────────┬──────────┘
                                             │ Loss Hints
                                             ▼
┌───────────────────────┐         ┌─────────────────────┐
│ Candidate Validation  │◀────────│ PromptIter Engine / │
│ (Val Set Regression)  │         │  PromptOptimizer    │
└──────────┬────────────┘         └─────────────────────┘
           │ Deltas
           ▼
┌───────────────────────┐         ┌─────────────────────┐
│   Acceptance Gates    │────────▶│  Audit Reporter     │
│ (Score/HardFail/Cost) │         │ (JSON + Markdown)   │
└───────────────────────┘         └─────────────────────┘
```

1. **Baseline Evaluation**: Separately evaluates `train_evalset.json` and `val_evalset.json`, recording per-case scores, pass/fail status, and tool trajectories.
2. **Failure Attribution**: Categorizes failed cases into 6 root cause types:
   - `final_response_mismatch` (Final assistant text mismatch)
   - `tool_call_error` (Tool execution error / rejection)
   - `tool_argument_error` (Tool argument type or format error)
   - `route_error` (Routing to wrong sub-agent)
   - `format_error` (Output schema or JSON format error)
   - `knowledge_recall_insufficient` (Uncertain or incomplete answer)
3. **PromptIter Optimization**: Proposes target prompt patches over `system_prompt`, `tool_desc_calc`, `router_prompt`, or `agent_instruction`.
4. **Candidate Validation**: Re-evaluates candidate prompt on the validation set, calculating case deltas.
5. **Acceptance Gates**:
   - `MinValidationScoreGain`: Requires minimum validation score gain (e.g. +0.10).
   - `AllowNewHardFail`: Enforces zero new hard fails (`false`).
   - `KeyCaseIDs`: Guards critical key cases against score degradation.
   - `MaxCostBudgetUSD`: Checks pipeline execution cost against budget limit.
6. **Audit Persistence**: Writes `optimization_report.json` and human-readable `optimization_report.md`.

---

## Quick Start & Usage

### Prerequisites
Go 1.21+ installed.

### Run Deterministic Offline Mode (No API Key Required)
```bash
cd examples/evaluation/promptiter_regression_loop
go run . -mode fake_deterministic -output output
```

### Run Unit & Integration Tests
```bash
go test -v -count=1 ./...
go vet ./...
```

---

## Output Reports

Executing the pipeline creates two audit files in `output/`:
- `optimization_report.json`: Machine-readable audit output containing round summaries, deltas, gate decisions, and attributions.
- `optimization_report.md`: Markdown summary table formatted for human review.

---

## Case Scenarios (6 Cases)

| Case ID | Dataset | Type | Category / Description |
|---|---|---|---|
| `train_opt_01` | Train | Optimizable | User hedging query (`final_response_mismatch`) |
| `train_opt_02` | Train | Optimizable | Calculator tool argument error (`tool_argument_error`) |
| `train_opt_03` | Train | Pass Anchor | Stable pass anchor case |
| `val_opt_01` | Validation | Optimizable | Factual recall query (`knowledge_recall_insufficient`) |
| `val_opt_02` | Validation | Overfit Trap | Router prompt overfit query (`route_error`) |
| `val_opt_03` | Validation | Key Case | Critical system health check anchor case |
