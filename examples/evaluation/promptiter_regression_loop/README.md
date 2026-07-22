# PromptIter Evaluation Regression Loop

This example turns evaluation and prompt optimization into one auditable release loop. It uses the real tRPC-Agent-Go `llmagent`, Evaluation Service, execution trace, and PromptIter engine, while deterministic local stages make the complete workflow runnable without an API key.

The example is intentionally kept outside the framework's public API. It provides a reference pipeline that applications can copy and adapt after choosing their own quality metrics, failure taxonomy, prompt surfaces, and release policy. The report-facing implementation remains under `internal/regression` until those contracts have broader production evidence.

## Pipeline

The entrypoint performs these stages in order:

1. Evaluate the baseline prompt on independent train and validation evalsets.
2. Normalize every case, metric, reason, trace, and measured usage record.
3. Attribute each failed metric from metric metadata, metric reasons, execution errors, and trace-step errors.
4. Run one PromptIter round for each deterministic optimizer proposal.
5. Re-evaluate every candidate on train and validation, then calculate exact case and metric deltas.
6. Apply a release gate against both the original validation baseline and the last accepted validation result.
7. Publish JSON and Markdown together in a run-specific directory.

PromptIter's round acceptance and the release gate are deliberately separate. PromptIter acceptance controls its own optimization progression; only the release gate controls `shouldWriteBack`.

## Data

The default configuration is in `data/promptiter-regression-app/`:

```text
baseline_prompt.txt
train.evalset.json
validation.evalset.json
metrics.json
promptiter.json
```

The train and validation evalsets contain three cases each. The fixed exact-match answers make the following behaviors reproducible:

| Profile | Train | Validation | Release result |
| --- | ---: | ---: | --- |
| Baseline | 1/3 | 2/3 | Baseline only |
| Candidate 1 | 2/3 | 3/3 | Accepted |
| Candidate 2 | 2/3 | 3/3 | Rejected: no additional gain |
| Candidate 3 | 3/3 | 2/3 | Rejected: new critical security failure |

Candidate 3 is the overfitting case: its train score is best, but it regresses the validation safety case. The selected prompt must remain candidate 1 after all three attempts.

## Run

From the repository root:

```bash
go -C examples/evaluation run ./promptiter_regression_loop
```

From the evaluation examples module:

```bash
cd examples/evaluation
go run ./promptiter_regression_loop
```

Use another configuration when adapting the example:

```bash
go run ./promptiter_regression_loop -config /path/to/promptiter.json
```

No API key or network model endpoint is required. A normal local run finishes well below the three-minute acceptance limit.

## Reports

Each run atomically publishes:

```text
output/<run-id>/optimization_report.json
output/<run-id>/optimization_report.md
```

The JSON report retains full normalized case and metric evidence, traces, PromptIter patches, attribution results, gate decisions, seed, model/config identity, measured token and call counts, and durations. Each round has `delta` relative to the last accepted validation result and `baselineDelta` relative to the immutable original validation baseline. Token and call counts are the auditable cost basis in fake mode; the example does not invent a currency estimate without a model price table. The Markdown report summarizes both deltas and the same release decision for human review.

A complete deterministic sample is checked in as [optimization_report.json](./output/20260722T033916.163604406Z-1adfcdb325b6/optimization_report.json) and [optimization_report.md](./output/20260722T033916.163604406Z-1adfcdb325b6/optimization_report.md).

Publication uses a staging directory followed by one directory rename, so readers cannot observe JSON from one run beside Markdown from another. A run ID collision fails instead of overwriting prior audit evidence.

## Release Gate

`promptiter.json` configures these checks:

- minimum validation score gain;
- no newly failing validation case or metric;
- no regression of named critical cases;
- maximum validation token, model-call, and tool-call budgets.

Missing cases or metrics, duplicate identities, changed metric thresholds, unknown metric states, failed traces, execution errors, and unmeasured usage under an enabled budget all fail closed. Comparing with the original baseline as well as the last accepted result prevents a sequence of small accepted changes from hiding cumulative regression.

## Adaptation Boundary

For a real application, keep the orchestration and replace the deterministic pieces:

- replace `deterministicModel` with the application's candidate model;
- replace the deterministic backwarder, aggregator, and optimizer with PromptIter model-backed stages;
- provide business train/validation evalsets and metrics;
- extend metric-derived attribution rules only when reliable evidence exists;
- configure critical cases and budgets for the deployment policy;
- perform source prompt writeback outside this pipeline after `shouldWriteBack` is reviewed.

This example supports one instruction surface, `candidate#instruction`. Other instruction, skill-description, router, or tool-description surfaces should use their owning framework contracts rather than broadening this example with parallel APIs.

## API Readiness Validation

`TestToolDescriptionSurfaceEndToEnd` validates a second surface without an API key. It runs a real `llmagent` tool declaration through Evaluation Service and PromptIter, changes `candidate#tool.lookup_flight`, and verifies that the patched description reaches the model request. The same normalized delta, attribution, release gate, and report contracts accept the result, which shows that the evidence and decision layer is surface-neutral.

The CLI orchestration remains intentionally instruction-specific. Its independent candidate re-evaluation compiles instruction overrides with public agent run options, while PromptIter's generic tool-description profile compiler is internal to the engine. This is evidence against publishing the whole pipeline API today: the stable reusable boundary is the evidence/gate/report layer, but a public cross-surface profile-evaluation contract still needs a framework-level design decision.

See [DESIGN.md](./DESIGN.md) for the design rationale.
