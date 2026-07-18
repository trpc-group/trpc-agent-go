# PromptIter Regression Loop

This example runs a reproducible prompt optimization loop using the Evaluation Service and the real PromptIter engine:

```text
baseline evaluation → failure attribution → candidate generation
→ train/validation regression → acceptance gate → JSON/Markdown audit
```

The default runtime is deterministic and needs no API key. Run it from `examples/evaluation`:

```bash
go run ./promptiter_regression_loop \
  -seed=2003 \
  -output=./promptiter_regression_loop/output
```

The command never modifies `data/baseline_prompt.txt`. An accepted result only recommends write-back.
Do not include production credentials or secrets in prompts, eval fixtures, or model responses written to audit reports.

## Fixture behavior

The fixtures contain three training cases and three held-out validation cases. Every case checks the final response and `echo_ping` tool trajectory through the real Evaluation Service. PromptIter runs its evaluate, backward, aggregate, optimizer, profile-patch, and validation stages; deterministic stage runners make the result reproducible without an API key.

| Round | Candidate behavior | Expected result |
| --- | --- | --- |
| 1 | Same behavior as baseline | Rejected: optimization is ineffective |
| 2 | All training cases pass, validation regresses | Rejected: overfitting |
| 3 | Training and validation improve | Accepted |

Rejected candidates are never promoted. The next round still starts from the last accepted prompt. The loop stops after the first candidate that passes the external regression gate.

## Inputs and outputs

- `data/train.evalset.json`: three training cases.
- `data/validation.evalset.json`: three held-out cases.
- `data/metrics.json`: final-response and tool-trajectory metrics.
- `data/promptiter.json`: candidates, round limit, and gate policy.
- `data/fake_engine.json`: deterministic model usage and latency.
- `output/optimization_report.json`: structured audit artifact.
- `output/optimization_report.md`: reviewer-oriented explanation.

The reports include baseline scores and failure evidence, every candidate prompt, changed case/metric deltas, gate reasons, serving/optimization cost, seed, input hashes, and runtime identity. Candidate summaries omit repeated trajectories and unchanged deltas to keep the JSON reviewable.

Run the end-to-end test with:

```bash
go test -race ./promptiter_regression_loop -count=1
```

To use a model-backed runtime, replace the deterministic PromptIter stage runners in `runtime.go` with audited model runners. The outer regression gate remains the final acceptance authority.
