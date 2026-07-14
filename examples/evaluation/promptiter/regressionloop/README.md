# PromptIter Regression Loop

This example runs a deterministic Evaluation + PromptIter regression workflow without an API key.

```bash
cd examples/evaluation/promptiter/regressionloop
go run .
```

The command writes JSON/Markdown reports, baseline snapshots, and six artifacts per round. It never writes the source prompt; it only records whether an accepted release profile is recommended for later write-back.

## Design

The evalset and metric JSON files are installed in the repository's managers at startup. A normal `LLMAgent` runs every case through the local Evaluation Service. The fake model generates only responses, tool calls, and optimizer patches, never scores or pass/fail results. Standard evaluators compare actual invocations with file-backed expectations, so editing a case, answer, threshold, or metric set changes the measured result.

PromptIter acceptance and the release Gate have intentionally different jobs. PromptIter acceptance controls optimization search and may advance the search profile even when a candidate is not safe to release. The independent release Gate decides whether a candidate should be recommended for publication or write-back. It always compares the candidate with the last profile accepted by the release Gate, not merely with the current PromptIter search input. A search profile rejected by the release Gate therefore never becomes the release baseline. A round may have `promptIterAccepted=true` and `releaseGate.accepted=false`. The Gate checks validation gain, regressions, new hard failures, P0 must-pass and score-drop rules, overfitting, missing generalization, evaluation completeness, latency, model/tool calls, and cost, and reports every failed check.

Failure attribution starts with structured actual/expected evidence: final responses, JSON validity, tool names, arguments, results, routes, and trace errors. Metric type and rubric text are fallbacks, and every failed case receives an explainable category. Reports aggregate those categories for baseline train/validation and every candidate train/validation evaluation. Candidate validation comes from PromptIter; candidate training is deliberately rerun through the same evaluator because `RoundResult.Train` describes the round input profile. Reports retain candidate comparisons with the initial profile, current search input, and last released profile. The last comparison drives the release decision.

The regression workflow collects baseline and candidate evaluation resources through an injectable `ResourceMeter` and uses them in the release Gate and audit report. This fake environment derives case, model, and tool counts from each actual evaluation execution, then applies fixed prices and deterministic synthetic latency increments. It does not use wall-clock latency for golden decisions, and token usage remains explicitly unavailable.

Artifacts are atomically replaced below a traversal-safe output root. Reports retain per-case deltas, attribution summaries, trace surfaces, actual/expected tool trajectories, Gate checks, model configuration, seed, latency, usage, estimated cost, and references to each snapshot. Round 1 generalizes and is released; Round 2 improves training only and is rejected; Round 3 improves training while regressing validation and the P0 tool case, so the workflow retains Round 1. Public examples and boundary tests exercise these behaviors; hidden-set acceptance and attribution accuracy remain external acceptance measurements and are not claimed by this example.

## Test

```bash
go test ./...
go test . -run TestDeterministicEndToEnd -count=5
```
