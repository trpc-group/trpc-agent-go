# PromptIter Evaluation Regression Loop

This example is a complete, no-key Evaluation + PromptIter regression loop. It loads native Evaluation eval sets and metrics, evaluates a baseline prompt on separate training and held-out validation sets, attributes every failed metric, generates prompt patches through the repository's PromptIter engine, evaluates every candidate on both splits, computes per-case deltas, applies release gates, and writes auditable JSON and Markdown reports.

The runner uses a deterministic `model.Model`, a real `llmagent.LLMAgent`, native execution traces, native `evaluation.AgentEvaluator`, and the native PromptIter engine. It performs no network calls and does not read API-key environment variables.

## Inputs

The `data` directory contains every required input:

- `train.evalset.json`: three native training cases.
- `validation.evalset.json`: five disjoint held-out cases.
- `metrics.json`: the native metric and deterministic evaluator binding.
- `baseline_prompt.txt`: the initial instruction surface.
- `promptiter.json`: seed, target surface, rounds, and search policy.
- `regression.json`: held-out gates, critical/hard cases, evidence bound, and artifact names.

The eight public cases (three training and five validation) cover response mismatch, wrong tool, wrong arguments, wrong route, invalid structured output, knowledge-recall failure, direct answers for user-supplied facts, and private-order refusal. The last two validation-only guards explicitly expect an empty tool trajectory and are both critical hard-failure cases. Candidate one improves both training and validation and is released. Candidate two improves the search objective, but its overly broad wrong-tool remediation calls `lookup_order` for both no-tool guards and over-routes the existing critical route case. The search profile advances while the released profile does not.

## Semantics

- `initialProfile` is the immutable baseline. `searchProfile` advances only on a PromptIter `SearchAccepted` decision, while `releasedProfile` advances only when the independent held-out release gate accepts the candidate. The final output is always `releasedProfile`.
- The native PromptIter engine receives only training cases. Its configured `train_all` internal validation also comes from the training domain; held-out validation results, traces, reasons, trajectories, and attributions never become loss hints or optimizer inputs.
- An explicit source `tools: []` is preserved as `expectNoTools: true` and rendered as an empty expected trajectory, so the audit report distinguishes a no-tool oracle from an unspecified tool expectation.
- The deterministic seed is `2003`. Candidate generation depends on the current profile, training loss hints, and that seed, so equivalent inputs reproduce the same semantic result without an external API key.
- Release decisions use the candidate's held-out `vs_released` delta. The sample requires at least `0.05` quality gain, no new hard failure, no critical-case regression, and at most `200` cumulative model calls.

## Run

From `examples/evaluation`:

```bash
go run ./promptiter_regression_loop \
  -data-dir ./promptiter_regression_loop/data \
  -output-dir ./promptiter_regression_loop/output
```

The command writes:

```text
output/optimization_report.json
output/optimization_report.md
```

`example_output` contains the checked-in deterministic result. Its profile hashes, cases, scores, deltas, decisions, stop reason, model-call count, and token totals are regeneration-tested. Only measured wall-clock latency and artifact paths are normalized by that test.

Read the Markdown report from top to bottom for the baseline, each candidate's reason and case deltas, separate search/release decisions, pointer transitions, final released prompt, and cumulative resources. Use the JSON report for complete per-metric evidence, provenance, all three delta sets, and automated audit checks.

## Verify

From `examples/evaluation`:

```bash
go test ./promptiter_regression_loop
```

From `evaluation`:

```bash
go test ./workflow/promptiter/regression
```

The reusable package tests cover strict input loading and provenance, gate decisions, per-case deltas, failure attribution, resource accounting, and JSON/Markdown report generation.
