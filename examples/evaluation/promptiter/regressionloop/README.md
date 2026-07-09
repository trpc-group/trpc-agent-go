# PromptIter Regression Loop (Evaluation + Optimization)

A reproducible **Evaluation → failure attribution → PromptIter optimization →
validation regression → release gate → audit report** loop built on the existing
`evaluation/workflow/promptiter` engine.

The whole pipeline runs deterministically with **no API key** (`--mode=fake`): a
scripted model drives the real optimization engine so the loop, the report, and
the release decision are fully reproducible in CI.

## What it demonstrates

Given a baseline prompt, a training eval set, a validation eval set, and an
optimization config, the pipeline:

1. **Baseline eval** — scores the baseline prompt on the validation set.
2. **Optimization** — runs the real PromptIter engine (backward → aggregate →
   optimize) to produce a candidate instruction.
3. **Validation regression** — re-scores the candidate and computes a per-case
   delta vs baseline (newly passed / newly failed / score up / score down).
4. **Failure attribution** — classifies baseline failures by root cause.
5. **Release gate** — decides whether the engine-accepted candidate is safe to
   publish (score gain threshold, no new hard fails, protected cases, budget).
6. **Audit report** — writes `optimization_report.json` (machine truth) and
   `optimization_report.md` (human summary).

The reusable analysis layer (attribution / delta / gate / report) lives in the
main module at `evaluation/workflow/promptiter/regloop/` and is unit-tested with
hand-built fixtures (no model, no API key). This example is the thin wiring that
runs the engine and feeds its `RunResult` into that package.

See [`DESIGN.md`](./DESIGN.md) for the 300–500 word rationale (failure
attribution, acceptance strategy, anti-overfitting, PromptIter integration,
artifact audit).

## Run

```bash
cd examples/evaluation
go run ./promptiter/regressionloop --mode=fake --scenario=all \
    --data-dir=./promptiter/regressionloop/data \
    --output-dir=./promptiter/regressionloop/output
```

`--scenario` selects one of `success | ineffective | overfit | attribution | all` (default
`success`). Each scenario writes its report to `output/<scenario>/`.

## Scenarios

The same 6 cases (3 train / 3 validation) drive three required outcomes; only
the deterministic candidate behaviour and thresholds differ (see `scenario.go`):

| scenario | what happens | outcome |
|---|---|---|
| **success** | baseline fails all; optimization fixes all | engine accepts, **gate RELEASES** |
| **ineffective** | optimized prompt still fails validation | no gain, **gate REJECTS** |
| **overfit** | training + overall validation improve, but `val_01` regresses | engine accepts on overall gain, **gate REJECTS** on the regressed case |
| **attribution** | a case fails a final-response metric AND a tool-trajectory metric | baseline attribution shows **`responseMismatch` + `toolError`** live |

The **overfit** case is the important one: overall validation improves
(0.333 → 0.667) so the engine's `MinScoreGain` accepts it, but the harness gate
sees `newlyFailed=1` (a case that passed at baseline now fails) and rejects the
candidate — exactly the "training up, validation down" protection an
optimization loop needs. Committed sample reports for all four scenarios are
under [`output/`](./output).

## Inputs (`data/eval-optimization-app/`)

| File | Purpose |
|---|---|
| `train.evalset.json` | 3 training cases (drive gradients) |
| `validation.evalset.json` | 3 validation cases (acceptance + regression) |
| `eval-optimization.metrics.json` | deterministic metric (exact final-response match) |
| `attribution-{train,validation}.evalset.json` | attribution scenario data (expected tool call + gold response) |
| `attribution.metrics.json` | attribution metrics (final-response + tool-trajectory) |
| `baseline.instruction.txt` | baseline prompt source |
| `promptiter.json` | rounds / min score gain / stop / release gate config |

> **Metric naming gotcha:** `metricName` must equal a registered evaluator name
> (`final_response_avg_score`, `tool_trajectory_avg_score`, …). The evaluator is
> resolved by name, not by criterion type; a custom name is silently skipped.

## How the deterministic (fake) mode works

`fakemodel.go` supplies one scripted model per role, so the real engine runs
end to end with no network:

- **candidate** — its answer is drawn from a `baselineGolds` set under the
  baseline instruction and an `optimizedGolds` set once the optimizer injects a
  sentinel marker; a miss yields a poor answer (a fail). Partitioning the two
  sets per scenario expresses success (baseline empty → optimized full),
  ineffective (both empty), and overfit (a case in baseline but not optimized).
- **backwarder / aggregator / optimizer** — return static JSON matching each
  worker's decode contract; the optimizer patch rewrites the instruction to the
  marked "good" version, which the candidate then recognizes.

Because the candidate must fail the training set to generate the terminal losses
that drive optimization, all baseline cases are designed to fail exact match.

## Failure attribution categories

The classifier (`regloop.Attribute`) supports six categories, proven by unit
tests in `regloop`:

`responseMismatch`, `formatError`, `toolError`, `toolArgError`, `routeError`,
`knowledgeRecall` (plus `other`).

The three optimization scenarios use a single exact-match final-response metric,
so their baseline failures are all `responseMismatch`. The **attribution**
scenario adds a `tool_trajectory_avg_score` metric and an eval case with an
expected tool call: the text-only candidate makes no tool call, so that run's
report shows both `responseMismatch` and `toolError` in one live pass.
Format/knowledge categories activate from the metric reason text; the classifier
itself is metric-agnostic and unit-tested across all six categories.

## Cost note

The engine `RunResult` carries no token accounting, so the report's cost is an
**estimate** derived from observable counts (rounds × evaluated cases) and is
labelled `"estimated": true`.
