# PromptIter Regression Loop Example

This example demonstrates a deterministic Evaluation + Optimization regression loop for prompt changes. It runs without model credentials and writes both machine-readable and reviewer-readable optimization reports.

## What It Covers

- Baseline evaluation on train and validation sets.
- Failure attribution from final response, tool trajectory, trace, rubric, and structured-output evidence.
- PromptIter-style candidate generation for one agent instruction surface.
- Validation-set regression comparison against the baseline.
- Acceptance gates for minimum validation gain, new hard fails, critical-case regression, call budget, cost budget, and latency budget.
- Audit reports with candidate prompts, per-case deltas, gate reasons, seed, fake runner config, cost, and latency.

## Files

```text
data/promptiter-regression-loop-app/
├── baseline_prompt.txt
├── train.evalset.json
├── validation.evalset.json
├── metrics.json
└── promptiter.json

output/
├── optimization_report.json
└── optimization_report.md
```

The train set contains three cases and the validation set contains three cases. The fake optimizer emits three candidate rounds:

- Round 1 improves validation and is accepted.
- Round 2 is ineffective and is rejected for insufficient gain.
- Round 3 improves train score but regresses validation, introduces a hard fail, and is rejected.

## Run

```bash
cd examples/evaluation/promptiter_regression_loop
go run . \
  -config ./data/promptiter-regression-loop-app/promptiter.json \
  -output-dir ./output
```

Expected output:

```text
Baseline validation score: 0.65
Best candidate validation score: 0.86
Gate accepted: true
JSON report: ./output/optimization_report.json
Markdown report: ./output/optimization_report.md
```

No `OPENAI_API_KEY` is required. The sample uses `runner.mode=fake` and a fixed seed from `promptiter.json`.

## Test

From this directory:

```bash
go test .
```

From the evaluation module:

```bash
cd ../../../evaluation
go test ./workflow/promptiter/regressionloop
```

## Report Review

Open `output/optimization_report.md` for a reviewer summary. The JSON report is the source of truth for automation and includes the full baseline, candidates, per-case validation delta, gate decision, attribution stats, cost summary, and latency summary.
