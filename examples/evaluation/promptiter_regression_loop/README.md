# PromptIter Regression Loop

This example runs a deterministic Evaluation + PromptIter regression loop without any API key.

The fake optimization path still goes through real `llmagent`, `runner`, `evaluation.AgentEvaluator`, and `evaluation/workflow/promptiter/engine`. Fake code only replaces the candidate `model.Model` and the PromptIter worker interfaces. Trace smoke uses recorded trace-mode evalset data to verify evaluation compatibility and report attribution; it does not run PromptIter optimization.

## Run Fake Mode

```bash
go run . -mode fake
```

Outputs:

- `output/optimization_report.json`
- `output/optimization_report.md`

Optional flags:

```bash
go run . \
  -mode fake \
  -data-dir ./data \
  -output-dir ./output \
  -prompt ./config/baseline_prompt.txt \
  -config ./config/promptiter.json
```

## Run Trace Smoke

```bash
go run . -mode trace-smoke -output-dir ./output-trace
```

Trace smoke reads `data/promptiter-regression-loop-app/trace_smoke.evalset.json`, replays recorded `actualConversation` and `executionTrace` artifacts, and writes the same report filenames to the configured output directory. It sets:

- `traceSmoke.enabled = true`
- `traceSmoke.optimizationSkipped = true`
- `traceSmoke.optimizationSkippedReason = "trace mode replays actual output and cannot validate candidate inference"`

This mode verifies trace evaluation, PromptIter evaluation adaptation, report serialization, and failure attribution. It intentionally skips optimizer rounds because trace mode replays actual outputs instead of running candidate inference after a patch.

## Report Fields

- `baseline.train` and `baseline.validation` summarize the initial profile.
- `candidate.train` reruns train regression against the final accepted profile.
- `candidate.validation` summarizes the final accepted profile validation result.
- `rounds[*].outputProfile` records per-round optimizer output; it is not necessarily the final candidate.
- `delta.perCase` compares baseline and candidate validation cases.
- `gate` decides whether the final accepted profile should be published.
- `attribution` explains failed candidate validation cases.
- `traceSmoke` records trace smoke status and skipped-optimization details.
- `cost` and `latencyMs` summarize deterministic execution cost and duration.

## Fake Mode Behavior

- The baseline prompt is read from `config/baseline_prompt.txt`, hashed, and passed into `llmagent.WithInstruction`.
- Baseline evaluation uses a loyalty-profile tool declaration, so flight lookup cases fail deterministically.
- PromptIter receives deterministic failures, builds gradients through fake worker interfaces, and applies a real surface patch to `candidate#tool.lookup_record`.
- Candidate evaluation sees the patched tool declaration and the fake model changes inference behavior by calling `lookup_record`.
- The fake optimizer runs two accepted rounds: a partial delay patch, followed by an overfit patch that improves aggregate validation while regressing a critical no-tool case.
- The final gate rejects publishing when validation improves but a new hard fail or critical-case regression appears.
- Deterministic metrics run with `RunRequest.Judge = nil`.

## Boundary

Fake mode is the optimization validation path. Trace smoke is a compatibility and reporting smoke test for recorded traces, not a prompt optimization loop. Real-model support can be added by providing another `model.Model`, but this example keeps all tests deterministic and API-key free.
