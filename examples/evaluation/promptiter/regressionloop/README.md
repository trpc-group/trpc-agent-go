# PromptIter Regression and Acceptance Loop

This example adds the production-facing layer that sits around a PromptIter optimization run: separate training and held-out validation, failure attribution, per-case regression analysis, configurable acceptance gates, and durable audit reports. It runs without an API key by using a deterministic rule model, so CI and reviewers can reproduce every decision in seconds.

## Run

From `examples/evaluation`:

```bash
go run ./promptiter/regressionloop
go test ./promptiter/regressionloop
```

The command reads `train.evalset.json`, `validation.evalset.json`, `metrics.json`, `baseline_prompt.txt`, and `promptiter.json`. It writes `optimization_report.json` for automation and `optimization_report.md` for human review.

## Design

The pipeline evaluates the baseline prompt on training and validation sets independently. A failed case is attributed from its observable contract: missing output-format signals become `format_error`, missing tool routing becomes `route_error`, missing evidence becomes `knowledge_recall`, and a held-out training shortcut becomes `overfit`. Each reason records the case, category, triggering signal, and a readable explanation.

Candidate instructions are represented as `evaluation/workflow/promptiter.PatchSet` values. The deterministic candidate source models the boundary after PromptIter's backward, aggregation, and optimizer stages while keeping the complete regression path runnable without credentials. Replacing it with a model-backed PromptIter engine changes candidate generation, not evaluation, gate, or reporting semantics.

Every candidate is evaluated against both sets. Validation results are joined to the baseline by case ID and classified as new pass, new failure, improvement, regression, or unchanged. The acceptance gate requires a minimum held-out score gain, rejects every new failure and hard-case regression, and enforces call and estimated-token budgets. Therefore a prompt that improves all training cases but adds a memorized training-only rule is rejected, while a generalized routing and grounding improvement is accepted.

Audit output preserves every attempted prompt, patch reason, train and validation scores, per-case deltas, attribution counts, acceptance or rejection reasons, estimated cost, deterministic latency, seed, and model configuration. The committed fixtures demonstrate three rounds: an ineffective edit, an overfitted edit, and an accepted generalized edit. Unit tests protect attribution, delta classification, gate behavior, overfit rejection, report serialization, and the full offline pipeline.
