# PromptIter regression loop

This example adds the release-safety layer around the existing PromptIter engine:

1. validate and normalize baseline and candidate evaluation results;
2. attribute failed cases from typed metrics and trace evidence;
3. calculate per-case and per-metric deltas against both the original baseline and the latest accepted baseline;
4. apply score, hard-failure, critical-case, model-call, tool-call, and token gates;
5. write auditable JSON and Markdown reports atomically; and
6. make prompt writeback available only when both PromptIter and the regression gates accept a candidate.

The production integration consumes `*engine.RunResult`, so it uses the real Evaluation Service and PromptIter execution results rather than maintaining a second optimizer. The deterministic runner below invokes the actual PromptIter engine with offline evaluator, backwarder, aggregator, and optimizer collaborators. It therefore exercises the same engine and regression pipeline without an API key.

## Run offline

```bash
go run . -scenario success
go run . -scenario ineffective
go run . -scenario overfit
```

Each command writes `output/optimization_report.json` and `output/optimization_report.md`.

The six deterministic cases comprise three training cases and three held-out validation cases. The scenarios cover successful optimization, ineffective optimization, and training improvement with a critical validation regression.

The matching input artifacts are under `data/`: `train.evalset.json`, `validation.evalset.json`, `metrics.json`, `promptiter.json`, and `baseline_prompt.txt`.

## Integrate with PromptIter

Pass the result returned by `promptiterengine.Engine.Run` to `regression.AnalyzeRun`. Only call `regression.WriteAcceptedPrompt` when the resulting report is accepted. Rejected, failed, canceled, incomplete, or misaligned results cannot be written back.

## Known limits

Failure attribution is deterministic and evidence-based, but heuristic categories can still be ambiguous. Gate quality depends on representative held-out cases and meaningful metrics. This example prevents unsafe promotion; it does not guarantee that an accepted prompt is correct for unseen production traffic.
