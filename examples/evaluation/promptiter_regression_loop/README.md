# Phase 4 v2 PromptIter Regression Loop

This example demonstrates a deterministic Evaluation + PromptIter regression loop. It runs without API keys and keeps all implementation code inside this example directory.

The main `fake` mode proves that tool description patches travel through the real `llmagent -> runner.NewRunner -> evaluation.AgentEvaluator -> promptiter engine` path and change candidate inference. The `trace-smoke` mode separately proves that trace-mode evalsets can be replayed, evaluated, attributed, and reported without entering the optimization loop.

## Run

From `examples/evaluation`:

```bash
go test ./promptiter_regression_loop
go run ./promptiter_regression_loop -mode fake -output-dir ./promptiter_regression_loop/output
go run ./promptiter_regression_loop -mode trace-smoke -output-dir ./promptiter_regression_loop/output/trace-smoke
```

From this directory:

```bash
go test .
go run . -mode fake
go run . -mode trace-smoke -output-dir ./output/trace-smoke
```

The demo also accepts:

```bash
-data-dir ./data
-output-dir ./output
-prompt-path ./config/baseline_prompt.txt
-config-path ./config/promptiter.json
```

`fake` is the default mode and is the main closed-loop acceptance path. `trace-smoke` is a compatibility smoke test; it replays recorded actual invocations from `evalMode: "trace"` cases and explicitly skips optimization because replayed traces cannot prove that a new prompt patch changes candidate inference.

The default output directory is `./output`. When running from this example directory, regular fake-mode reports are written under the ignored `output/` directory. The commands above use an explicit path when running from `examples/evaluation` so that those reports are written to the same ignored directory.

`config/promptiter.json` configures the target surface, round count, PromptIter acceptance policy, stop policy, and final gate. The demo currently supports only `candidate#tool.lookup_record`, so unsupported target surfaces fail fast instead of silently producing unauditable output.

`finalGate.criticalCaseIDs` distinguishes an omitted field from an explicit empty array. Omitting the field keeps the example default, a non-empty array selects the configured critical cases, and `[]` disables critical-case checks. Setting `rejectOnCriticalRegression` to `false` disables rejection while preserving regression statistics for any configured critical cases.

## Fake Mode

The baseline instruction is deliberately neutral:

```text
You are a helpful assistant.
```

The initial `lookup_record` tool description only mentions a traveler loyalty profile. Round 1 teaches delay lookup. Round 2 teaches status, delay, departure, and gate lookup, but also overfits by forcing tool use for flight records even when the user asks not to.

The fake model reads only user messages, tool descriptions, and tool results. It does not read evalset expected invocations, expected final responses, case IDs, or test state.

The checked-in sample snapshot under `sample/` is refreshed only with an explicit output directory:

```bash
go run . -mode fake -output-dir ./sample
```

It intentionally ends with `gate.decision = "reject"`: train reaches `1.0` and validation rises from `0.25` to `0.75`, but `validation_status_tr789` becomes a new hard fail and critical regression. Normal runs do not overwrite this snapshot.

## Trace Smoke Mode

`trace-smoke` uses `data/promptiter-regression-loop-app/trace_smoke.evalset.json` and the shared `metrics.json`. Each case uses:

- `evalMode: "trace"`
- `conversation` as the expected invocation
- `actualConversation` as the replayed actual invocation
- `executionTrace` attached to the actual invocation

The mode calls `AgentEvaluator.Evaluate()` with run details enabled, adapts the result into the report shape, and records:

- `traceSmoke.enabled`
- `traceSmoke.evalSetId`
- `traceSmoke.optimizationSkipped`
- `traceSmoke.optimizationSkippedReason`
- `traceSmoke.evaluation`
- `traceSmoke.attribution`

The skip reason is fixed:

```text
trace mode replays actual output and cannot validate candidate inference
```

## Report

The fake report includes baseline and candidate train/validation scores, the accepted profile, each round output profile, patch values, validation delta, final gate decision, failure attribution for baseline train, baseline validation, and candidate validation, zero fake cost, latency, and deterministic model call count.

For reproducibility, the report also records deterministic seed `0`, the PromptIter config path and SHA-256, the resolved PromptIter round/acceptance/stop settings, and the fake model generation settings. The config hash covers the original file bytes; the prompt hash continues to cover the normalized prompt text actually supplied to the agent.

The trace-smoke report uses the same top-level JSON schema but leaves optimization fields empty or nil. Its result is only reported under `traceSmoke`, so it cannot be mistaken for a prompt release recommendation.
