# PromptIter Regression Loop Example

This example demonstrates a reproducible Evaluation + Optimization regression loop around PromptIter.

The default path uses the real PromptIter engine with deterministic fake collaborators, so it does not require `OPENAI_API_KEY`.

## What It Covers

- Baseline train and validation evaluation.
- Failure attribution for failed metrics.
- PromptIter optimization with attribution-derived loss hints.
- Explicit candidate validation rerun against the baseline validation set, including rejected final candidates.
- Per-case delta classification.
- Release gate rejection for overfitting.
- JSON and Markdown audit reports.
- Primary and secondary failure attribution labels with conflict evidence.
- Optional injectable attribution judge fallback for ambiguous low-confidence failures.
- Multi-scenario runs for success, ineffective optimization, and overfitting.
- Trace-backed fake-engine mode for the full optimization loop.
- Trace-smoke mode for deterministic trace replay coverage.
- Metrics-file validation for every sample metric name.

The sample intentionally creates an overfitting situation: the candidate improves train score and validation aggregate score, but it newly fails a critical validation case. The outer release gate rejects it even though PromptIter accepted the candidate by score gain.

## Files

```text
data/eval-optimization-regression-app/
  baseline_prompt.txt
  train.evalset.json
  validation.evalset.json
  metrics.json
  promptiter.json
output/
  optimization_report.json
  optimization_report.md
  success/
    optimization_report.json
    optimization_report.md
  ineffective/
    optimization_report.json
    optimization_report.md
  overfit/
    optimization_report.json
    optimization_report.md
  trace-smoke/
    optimization_report.json
    optimization_report.md
  trace-fake-engine/
    success/
      optimization_report.json
      optimization_report.md
    ineffective/
      optimization_report.json
      optimization_report.md
    overfit/
      optimization_report.json
      optimization_report.md
```

## Run

```bash
cd examples/evaluation/promptiter/regressionloop
go run .
```

The same default config also resolves correctly when the example is launched
from the parent evaluation module:

```bash
cd examples/evaluation
go run ./promptiter/regressionloop
```

Expected summary:

```text
accepted: false
- PromptIter accepted a candidate profile
- validation score gain 0.167 >= threshold 0.050
- 1 newly failed validation metrics
- critical cases regressed: [val_overfit_refund_policy]
- model calls 5 within budget 10
```

Run all deterministic scenarios:

```bash
go run . -mode fake-engine -scenario all -output-dir ./output
```

The committed scenario reports under `output/success`, `output/ineffective`,
and `output/overfit` show the three important outcomes: `success` passes the
release gate, `ineffective` is rejected for no validation gain, and `overfit`
is rejected despite aggregate validation improvement because a critical
validation case newly fails.

Run trace replay smoke coverage:

```bash
go run . -mode trace-smoke -output-dir ./output/trace-smoke
```

The committed trace-smoke report under `output/trace-smoke` records the
deterministic trace replay path. Trace-smoke mode intentionally replays
trace-bearing results and skips PromptIter acceptance; it validates trace
capture, attribution, delta, gate, and report auditing without claiming that a
prompt patch changed future model behavior.

Run the full PromptIter optimization loop with trace-bearing fake evaluations:

```bash
go run . -mode trace-fake-engine -scenario all -output-dir ./output/trace-fake-engine
```

This mode uses the same real PromptIter engine path as `fake-engine`, but marks
the report as `deterministic-trace-fake-engine` and preserves trace evidence in
baseline, PromptIter round, and final candidate validation results.

Available modes:

- `fake-engine`: default; runs `evaluation/workflow/promptiter/engine.Engine` with deterministic evaluator/backwarder/aggregator/optimizer implementations.
- `trace-fake-engine`: runs the full fake-engine PromptIter loop with trace-bearing evaluations.
- `scripted`: legacy deterministic PromptIterator stub for fast pipeline-only checks.
- `trace-smoke`: replays deterministic trace-bearing results and skips optimization acceptance; use `trace-fake-engine` when the complete PromptIter optimization loop must also carry traces.

## Test

```bash
cd evaluation
go test ./workflow/promptiter/regressionloop

cd ../examples/evaluation
go test ./promptiter/regressionloop
```

This repository is split into multiple Go modules. Run the commands above from
the module directories; running `go test ./evaluation/...` from the repository
root does not select the nested evaluation module.

## Configuration

`promptiter.json` controls the source prompt, metrics file, train and validation eval sets, target surface, PromptIter round settings, scenario, and release gate:

- `minValidationScoreGain`: minimum validation score gain required for release.
- `allowNewHardFail`: whether newly failed validation metrics are allowed.
- `hardFailMetricNames`: metric names whose newly failed validation results
  count as hard fails; when omitted, every newly failed metric is treated as a
  hard fail for backward-compatible strict gating.
- `rejectAnyScoreDown`: optional strict mode that rejects a candidate when any
  validation metric has a score-down regression, even when the case is not
  listed in `criticalCaseIds`.
- `criticalCaseIds`: validation cases that must not regress.
- `maxModelCalls`: estimated or provider-reported model-call budget.
- `maxCost`: requires a pipeline `CostProvider`; without one the gate rejects
  because token and amount data are unavailable.
- `requireEngineAccepted`: candidate must have been accepted by PromptIter before release.
- `attribution.metricCategoryHints`: optional promptiter.json overrides that
  map custom metric names to failure categories such as `route_error`,
  `format_error`, or `knowledge_recall_gap`.

Each metric in `metrics.json` may also include an optional `failureCategory`
field. The pipeline uses these hints before falling back to deterministic
metric-name, reason, and trace rules; promptiter.json hints override metrics
file hints when both are present.

For hidden or business-specific eval sets, prefer adding `failureCategory` for
metrics whose semantics are single-purpose, for example `route_error` for a
router-selection metric, `format_error` for a JSON/schema contract metric, or
`knowledge_recall_gap` for a grounding metric. Use
`attribution.metricCategoryHints` only when a shared metrics file cannot be
changed. Do not add a broad hint to mixed metrics such as a generic tool
trajectory score if failures can mean both "wrong tool" and "wrong arguments";
either split those metrics or let the attribution rules inspect actual/expected
invocations, metric reason, and trace. The built-in rules also recognize common
English and Chinese phrases such as missing tool calls, wrong sub-agent
selection, missing structured fields, insufficient citations, factually
incorrect policy dates, `failed`, `score below threshold`, and `低于阈值`.

When a metric name is opaque, the attribution layer also scans `metrics.json`
criterion keys and raw JSON text for aliases such as `function_call`,
`tool_arguments`, `json_schema`, `router`, `handoff`, `retrieval`, `citation`,
`grounding`, `exact_match`, `response_match`, and `rubric`. The report keeps
one primary category for stable counters and may add `secondaryCategories` when
multiple signals are present. If structured actual/expected evidence overrides
a configured hint, the evidence includes the conflict, for example
`configured_hint=knowledge_recall_gap overridden_by=structured_diff`.

Production callers that want an LLM/judge fallback can inject
`Pipeline.AttributionJudge`. The default example leaves it nil, so fake model,
trace mode, and deterministic CI runs do not require a real API key. The judge
is only called for `unknown_failure`, generic failed-metric fallback, or
low-confidence `rubric_failure`; deterministic structured/rule matches remain
the primary path.

The core implementation lives in `evaluation/workflow/promptiter/regressionloop`, so the gate, delta, attribution, and report logic are reusable outside this example.

## Production PromptIter Wiring

The example default already drives the real PromptIter engine with fake collaborators. For production wiring, the core package also provides adapters:

- `regressionloop.EvaluationServiceEvaluator` wraps `evaluation.AgentEvaluator` for baseline train and validation evaluation.
- `regressionloop.TextPromptSurfaceApplier` applies `promptSource` to configured prompt `targetSurfaceIds` through evaluation run options, so the baseline evaluator uses the prompt file rather than a separately constructed static agent prompt.
- `regressionloop.EnginePromptIterator` wraps `evaluation/workflow/promptiter/engine.Engine` for real PromptIter execution.

That lets callers reuse the same pipeline with local evaluation service, trace mode, tool trajectory metrics, LLM rubrics, and real PromptIter worker agents. The built-in applier supports `#instruction`, `#global_instruction`, `#few_shot`, `#tool.<name>`, and `#skill.<name>` by deriving runtime patches from the prompt text. Router prompts are ordinary instruction/global-instruction surfaces on the router node. `#model` surfaces still require a custom `PromptApplier` because selecting a model requires a concrete `model.Model` instance rather than text.

Production wiring:

```go
candidateRunner := runner.NewRunner(candidateAppName, candidateAgent)
agentEvaluator, err := evaluation.New(
	appName,
	candidateRunner,
	evaluation.WithEvalSetManager(evalSetManager),
	evaluation.WithMetricManager(metricManager),
	evaluation.WithEvalResultManager(evalResultManager),
	evaluation.WithJudgeRunner(judgeRunner),
	evaluation.WithNumRuns(1),
)
if err != nil {
	return err
}

engineInstance, err := promptiterengine.New(
	ctx,
	promptiterengine.WithAgent(candidateAgent),
	promptiterengine.WithAgentEvaluator(agentEvaluator),
	promptiterengine.WithBackwarder(backwarderInstance),
	promptiterengine.WithAggregator(aggregatorInstance),
	promptiterengine.WithOptimizer(optimizerInstance),
)
if err != nil {
	return err
}

pipeline := regressionloop.Pipeline{
	Evaluator: regressionloop.EvaluationServiceEvaluator{
		Evaluator: agentEvaluator,
		PromptApplier: regressionloop.TextPromptSurfaceApplier{
			SurfaceIDs: cfg.TargetSurfaceIDs,
		},
		Options: []evaluation.Option{
			evaluation.WithRunDetailsEnabled(true),
		},
	},
	PromptIterator: regressionloop.EnginePromptIterator{
		Engine: engineInstance,
	},
	CostProvider: productionCostProvider,
}

result, err := pipeline.Run(ctx, cfg)
if err != nil {
	return err
}
fmt.Println(result.Report.GateDecision.Accepted)
```

The pipeline reads `cfg.PromptSource`, applies it to supported prompt surfaces such as `support_agent#instruction`, `support_agent#global_instruction`, `router#instruction`, `support_agent#tool.billing_lookup`, or `support_agent#skill.refund_policy`, and also passes the same text into PromptIter as `RunRequest.InitialProfile`. It parses `cfg.MetricsPath`, records metric names in the report, and deterministic fake evaluators reject sample cases whose metric name is absent from the metrics file. It then runs baseline train and validation before invoking PromptIter. Train failures are attributed with deterministic metric-name, reason, and trace rules and injected into `RunRequest.Train[0].LossHints`, while the wrapped engine still performs the normal PromptIter train, backward, aggregation, optimization, validation, and engine acceptance stages. Without `CostProvider`, the report marks cost as a model-call estimate only; token and amount budgets require provider data.
After PromptIter returns a final candidate profile, the pipeline applies the
candidate prompt and explicitly reruns the validation set once more. The report
uses this outer candidate validation pass for the final delta and gate decision,
while PromptIter round validation remains available in the per-round audit.
