# PromptIter Regression Loop

This example adds an auditable acceptance layer around Evaluation Service and
PromptIter-style prompt patches. It evaluates a baseline on separate train and
validation sets, attributes failures, evaluates deterministic prompt
candidates, rejects regressions, and writes JSON and Markdown reports. No API
key is required.

## Pipeline

1. Load `train.evalset.json`, `validation.evalset.json`, `metrics.json`,
   `baseline_prompt.txt`, and `promptiter.json`.
2. Run the baseline prompt on both sets through the Go Evaluation Service.
3. Classify failed cases from final responses, tool trajectories, metric
   reasons, structured output, and execution traces.
4. Represent each configured candidate as a
   `promptiter.SurfacePatch`/`promptiter.Profile` for
   `candidate#instruction`.
5. Re-evaluate each candidate on train and validation, then calculate
   case-level deltas.
6. Accept only candidates that satisfy every configured gate.
7. Save every prompt, result, decision, cost, latency, seed, and fake model
   setting in the audit reports.

The deterministic runner emits normal response events, tool-call events, and
execution traces. The built-in `final_response_avg_score` and
`tool_trajectory_avg_score` evaluators perform scoring, so fake mode exercises
the same Evaluation Service path as a model-backed integration.

## Run

```bash
cd examples/evaluation
go run ./promptiter/regression \
  -config ./promptiter/regression/data/promptiter.json \
  -output-dir ./promptiter/regression/output
```

The run completes locally in seconds and writes:

```text
output/optimization_report.json
output/optimization_report.md
```

Use `-write-prompt=true` to replace the configured prompt source only when the
selected candidate passes the gate. The default is audit-only and never
modifies the source prompt.

## Included Scenarios

The train and validation files each contain three cases:

| Scenario | Expected behavior |
| --- | --- |
| Lookup routing | Optimization adds the required `lookup_record` call. |
| Structured output | Optimization changes plain text into exact JSON. |
| Knowledge recall | The first candidate cannot fix it; the broader second candidate does. |
| Critical direct answer | A broad search rule introduces an unnecessary tool call and must be rejected. |

With the checked-in config, round 1 raises validation score from `0.5000` to
`1.0000` and is accepted. Round 2 raises train score from `0.6667` to `0.8333`
but lowers validation to `0.6667`. It is rejected for insufficient validation
gain, two new hard failures, a critical-case regression, and excess tool calls.
This is the example's explicit overfitting regression.

## Gate Configuration

`promptiter.json` configures:

- minimum validation score gain;
- whether newly failing cases are permitted;
- critical case IDs and their maximum allowed score drop;
- maximum estimated candidate cost; and
- maximum validation tool calls.

A candidate must pass all checks. Gate decisions compare against the currently
accepted validation result, while the top-level report compares the final
selected prompt with the original baseline.

## Deterministic Task Format

Each user message is a JSON object consumed by the fake runner:

```json
{
  "intent": "lookup",
  "query": "weather:shenzhen",
  "answer": "Shenzhen weather is sunny.",
  "tool": {
    "name": "lookup_record",
    "arguments": {"query": "weather:shenzhen"},
    "result": {"condition": "sunny", "location": "Shenzhen"}
  }
}
```

Supported intents are `lookup`, `structured`, `knowledge`, and `direct`.
Hidden or custom cases can use the same schema without changing Go code.

## Production Adapter

The deterministic candidate generator preserves PromptIter's patch/profile
contract but replaces its LLM-backed backward, aggregation, and optimizer
stages. A production integration can obtain candidates from
`evaluation/workflow/promptiter/engine`, then pass each candidate prompt and
its Evaluation Service result into the same delta, gate, and report functions.
The validation set remains isolated from gradient generation in both modes.

See [DESIGN.md](./DESIGN.md) for the detailed design and
[`sample_output`](./sample_output) for checked-in reports.
