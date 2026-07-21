# PromptIter Evaluation Regression Loop

This example runs a deterministic evaluation-to-optimization loop around the
existing PromptIter Engine and local Evaluation Service. It needs no API key or
network access. The candidate agent uses a fake model; PromptIter's backward,
aggregation, and optimization stages use deterministic implementations of the
existing interfaces.

The deterministic optimizer applies the configured proposal only when
PromptIter supplies non-empty failed-case gradients. Each patch reason records
the sorted failure case IDs that triggered the proposal, so the offline search
policy remains auditable instead of bypassing the optimization signal.

The outer loop runs one PromptIter round per attempt, applies a stricter
validation gate, and passes only an accepted profile to the next attempt. This
keeps a rejected candidate from influencing later optimization.

See [DESIGN.md](./DESIGN.md) for the concise 300–500-character design
description required by the example specification.

## Run

From the example directory:

```bash
cd examples/evaluation/promptiter_regression_loop
go run . -config ./data/promptiter-regression-app/promptiter.json
```

The default two-minute timeout is below the three-minute acceptance limit.
Outputs are written to `./output` relative to this directory unless
`outputDir` is changed in `promptiter.json`:

```text
optimization_report.json
optimization_report.md
```

## Input Files

```text
data/promptiter-regression-app/
├── baseline_prompt.txt
├── metrics.json
├── promptiter.json
├── train.evalset.json
└── validation.evalset.json
```

- `baseline_prompt.txt` is the initial `candidate#instruction` surface.
- `train.evalset.json` has three optimization cases.
- `validation.evalset.json` has three independent regression cases.
- `metrics.json` combines local deterministic exact-response and tool-trajectory
  evaluators.
- `promptiter.json` fixes the seed, timeout, three candidate prompts, gate,
  budgets, target surface, and output path.

The fake model maps the exact natural-language business questions and complete
prompt instructions to deterministic behavior. Prompts and user messages contain
no test-only markers. Prompt profiles use normalized whole-value matching, and
behavior never depends on concurrent call order.

## E-commerce Support Cases

The six cases model an e-commerce support agent. Each cell is the expected
final-response exact-match score. Tool trajectory is a second metric: all cases
without tools match empty trajectories, while the delivery case validates one
`lookup_shipment` call and its arguments/result.

| Case | Baseline | Candidate 1 | Candidate 2 | Candidate 3 |
| --- | ---: | ---: | ---: | ---: |
| `train_return_window` | 0 | 1 | 1 | 1 |
| `train_invoice_correction` | 0 | 0 | 0 | 1 |
| `train_order_tracking` | 1 | 1 | 1 | 1 |
| `validation_return_shipping` | 0 | 1 | 1 | 1 |
| `validation_delivery_trace` | 1 | 1 | 1 | 1 |
| `validation_account_security` | 1 | 1 | 1 | 0 |

With both equally weighted metrics, validation scores are therefore `5/6`,
`1`, `1`, and `5/6`.

- Candidate 1 learns the return policy and is accepted because return-shipping
  validation becomes a new pass.
- Candidate 2 only rephrases the instruction, so it is rejected for no gain.
- Candidate 3 learns invoice correction but incorrectly trusts callers claiming
  to be support. It is rejected because the account-security case regresses.

`validation_delivery_trace` contains a fixed completed `executionTrace` with a
realistic `lookup_shipment` tool step in `actualConversation`. This exercises
trace-mode loading without calling the fake model.

## Acceptance Gate

The sample gate requires at least `0.1` validation-score gain, rejects new
failures, and allows no regression in `validation_account_security`. Validation
budgets cap tokens, model calls, and tool calls. Candidate results are compared
with both the current
accepted profile and the original baseline, preventing accumulated regression.

## Audit Reports

The compact JSON report records baseline train and validation results, every
attempted prompt and patch, full candidate train and validation results,
per-case and per-metric deltas, failure attribution, regression-gate decisions,
evaluation usage, duration, seed, run status/error, and fake-model metadata.
Successful LLM step payloads and per-case usage are
omitted because aggregate usage and failure evidence are already retained.
`candidate`, `delta`, and `gateDecision` describe the final selected prompt;
all attempted and rejected candidates remain under `rounds`.

Run metadata includes the source configuration SHA-256 and deterministic fake
engine version. JSON duration fields use the explicit `durationNanos` name.
Typed metric criteria determine failure categories; trace errors remain attached
as evidence and classify trace-only or untyped failures. An incomplete run
always clears writeback and records a rejecting final decision.

Configuration parsing rejects unknown fields and trailing JSON values so a
misspelled budget cannot silently disable a gate. Report write failures are
returned explicitly.

Fake-model token usage is a deterministic whitespace-delimited estimate, not a
provider tokenizer. It makes relative cost and budget checks reproducible
offline while clearly remaining a proxy for real billing usage.

The Markdown report presents the same decision in a compact human-readable
form. For the bundled data, candidate 1 remains the writeback profile after the
later no-effect and overfitting attempts are rejected.

## Verify

From `examples/evaluation`:

```bash
go test ./promptiter_regression_loop/... -coverprofile=coverage.out
go tool cover -func=coverage.out
golangci-lint run --config ../../.golangci.yml --timeout=10m ./promptiter_regression_loop/...
```
