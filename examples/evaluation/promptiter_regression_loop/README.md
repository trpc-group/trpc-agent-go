# PromptIter Regression Loop

This example implements the auditable evaluation and prompt-optimization loop
requested by issue #2003. It evaluates a baseline on separate training and
validation sets, attributes every failure, evaluates PromptIter-style prompt
candidates, compares validation results case by case, applies an acceptance
gate, and writes JSON and Markdown audit reports.

The default `fake_trace` engine is deterministic and requires no API key. Its
trace fixtures model final responses, tool trajectories, routing, structured
format validity, retrieval signals, cost, latency, and tool-call count. The
pipeline interfaces and report model can also wrap a real PromptIter run: map
each engine round and Evaluation Service result into the same candidate and
case result structures before applying the shared regression gate.

## Run

From `examples/evaluation`:

```bash
go run ./promptiter_regression_loop \
  --data-dir promptiter_regression_loop/data \
  --output-dir promptiter_regression_loop/out
```

The command writes:

- `optimization_report.json`: machine-readable baseline, rounds, case deltas,
  attribution counts, costs, latency, seed, engine configuration, and decision.
- `optimization_report.md`: reviewer-friendly summary and write-back advice.

Run the tests with:

```bash
go test ./promptiter_regression_loop
```

## Inputs

| File | Purpose |
|---|---|
| `train.evalset.json` | Three optimization cases |
| `validation.evalset.json` | Three held-out regression cases |
| `metrics.json` | Metric weights and pass threshold |
| `promptiter.json` | Seed, fake engine, candidate rounds, and gate budgets |
| `baseline_prompt.txt` | Source prompt under optimization |
| `candidate_*.txt` | PromptIter-style candidate patches |

Each case owns its fixed `expected_route`, optional tri-state
`retrieval_required`, and ordered `expected_tool_calls`. Observed runs contain
only runtime signals. Tool-call arguments are JSON values, so numbers, arrays,
and nested objects round-trip without being coerced to strings. All four JSON
input files reject unknown fields to prevent misspelled safeguards from being
silently disabled.

The sample deliberately covers three outcomes. `focused` improves both sets and
is accepted. `ineffective` changes wording without improving validation and is
rejected. `overfit` memorizes the training set, reaches a perfect training
score, regresses held-out critical cases, and is rejected.

## Acceptance gate

A candidate must satisfy every configured condition:

- validation score gain is positive and reaches `min_validation_gain`;
- no newly failing hard case;
- explicitly protected cases do not regress;
- validation cost increase stays within budget;
- total tool calls stay within budget.

Runtime success, route, retrieval, structured-format, and the complete ordered
tool trajectory are hard case contracts when the case declares them. A high
weighted response score cannot hide a contract regression. JSON and Markdown
reports snapshot both metrics and gate configuration so an acceptance decision
can be reproduced from the audit artifact alone.

Candidates are always compared with the immutable baseline. Only the
highest-scoring candidate that passes every gate becomes the selected prompt;
the example never rewrites the source prompt automatically.
