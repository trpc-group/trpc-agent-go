# PromptIter Regression Loop Example

This example builds an **evaluation + optimization regression loop** on top of the PromptIter engine
and the Evaluation service. It runs the full closed loop end to end:

```text
baseline eval  →  failure attribution  →  PromptIter optimization  →
candidate eval (held-out)  →  multi-criterion acceptance gate  →  audit report (JSON + Markdown)
```

The candidate is a customer-support message classifier that starts from an intentionally weak
instruction. PromptIter optimizes a single instruction surface (`candidate#instruction`); the loop
then **independently re-evaluates** the optimizer's accepted prompt on a held-out validation set and
applies its own acceptance gate — a second veto on top of the engine's mean-gain acceptance.

The whole loop runs **without an API key** in the default `fake` model source (deterministic scripted
model + deterministic PromptIter stages), completing in a few seconds. This makes it usable as a
CI regression check: the process exits non-zero when the gate rejects a candidate.

## Why a second gate: the overfitting veto

The PromptIter engine accepts a patch when the validation **mean score** rises. That is not enough: a
prompt can raise the aggregate while *breaking a previously-passing case*. The sample data is designed
to trigger exactly this — the engine accepts the optimized "STRICT" prompt (validation mean
`0.333 → 0.667`), but the key case `val_resolved_freeform_KEY` regresses `pass → fail`. The gate
detects the per-case regression and **rejects** the candidate, exiting non-zero. This is the core
value the example demonstrates: aggregate-only acceptance is unsafe.

## Data files

Loaded from `./data/promptiter-regression-loop-app/` by default:

```text
regression-loop-train.evalset.json        # 3 train cases
regression-loop-validation.evalset.json   # 3 validation cases (incl. the key case)
regression-loop.metrics.json              # shared metric: final-response exact-match, threshold 1.0
baseline-prompt.txt                       # the weak seed instruction
```

The six cases jointly cover the three required scenarios in one deterministic run:
optimize-success (cases the STRICT prompt fixes), optimize-ineffective (`train_package_shipping`
stays failing — the STRICT prompt over-narrows), and post-optimization regression
(`val_resolved_freeform_KEY` breaks).

The deterministic fake behavior is scripted in `./fixtures/regression-loop.fake.json`.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-data-dir` | Directory containing evaluation set and metric files | `./data` |
| `-output-dir` | Directory where results and reports are written | `./output` |
| `-fixtures-dir` | Directory containing deterministic fake-model fixtures | `./fixtures` |
| `-model-source` | `fake` (no API key) or `openai` | `fake` |
| `-model` | Candidate model identifier (openai source) | `deepseek-v3.2` |
| `-judge-model` | Judge model identifier (openai source) | `gpt-5.2` |
| `-worker-model` | PromptIter worker model identifier (openai source) | `gpt-5.2` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `3` |
| `-min-score-gain` | Minimum validation gain the **engine** requires to accept a patch | `0.01` |
| `-max-rounds-without-acceptance` | Maximum consecutive rejected rounds before stopping | `2` |
| `-target-score` | Validation score that stops optimization when reached | `1.0` |
| `-gate-min-validation-gain` | Minimum validation mean gain the **acceptance gate** requires (`validation_improves`) | `0.01` |
| `-key-cases` | Comma-separated validation case IDs that must not regress pass→fail (`key_cases_protected`) | `val_resolved_freeform_KEY` |
| `-max-candidate-model-calls` | Budget: max candidate model invocations the gate allows (`within_budget`); `0` disables it | `0` |
| `-baseline-prompt-file` | File holding the baseline instruction; used when `-candidate-instruction` is empty | `./data/promptiter-regression-loop-app/baseline-prompt.txt` |
| `-candidate-instruction` | Baseline instruction; overrides `-baseline-prompt-file` when set | *(empty → file)* |
| `-debug-io` | Log candidate, judge, and worker agent IO | `false` |

(The parallelism flags — `-eval-case-parallelism`, `-parallel-*`, etc. — mirror the syncrun example.)

The baseline instruction is resolved with precedence: explicit `-candidate-instruction` > `-baseline-prompt-file` contents > built-in default.

## Acceptance gate criteria

The gate accepts the optimized candidate only when **every** enabled criterion passes:

| Criterion | Pass condition |
| --- | --- |
| `validation_improves` | validation mean gain ≥ `-gate-min-validation-gain` |
| `no_new_hard_fail` | no validation case regressed pass→fail (the overfitting veto) |
| `key_cases_protected` | no `-key-cases` case regressed pass→fail |
| `within_budget` | candidate model calls ≤ `-max-candidate-model-calls` (skipped when `0`) |

## Run

Deterministic, no API key:

```bash
cd examples/evaluation/promptiter_regression_loop
go run .
echo "exit code: $?"   # 1 when the gate rejects the candidate
```

Against a real OpenAI-compatible endpoint:

```bash
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . -model-source openai -model "deepseek-v3.2" -judge-model "gpt-5.2" -worker-model "gpt-5.2"
```

The `openai` source is an **optional** real-model path (the required core flow is the key-free `fake` source above).

## Sample output (fake source)

```text
── Acceptance gate (validation) ──
  new_pass     val_refund_billing           0.000 → 1.000 (+1.000)
  new_fail     val_resolved_freeform_KEY    1.000 → 0.000 (-1.000)
  new_pass     val_parcel_shipping          0.000 → 1.000 (+1.000)
  ✅ validation_improves        validation mean 0.333 → 0.667 (gain +0.333, required ≥ 0.010)
  ❌ no_new_hard_fail           1 case(s) regressed pass→fail: [val_resolved_freeform_KEY]
  ❌ key_cases_protected        KEY case(s) [val_resolved_freeform_KEY] regressed pass→fail
  ✅ within_budget              no budget configured (candidate model calls: 33)
Gate decision: REJECT
  - rejected: KEY validation case(s) [val_resolved_freeform_KEY] regressed pass→fail (overfitting)
```

The audit report is written to `./output/optimization_report.json` and `.md`. It records baseline and
candidate scores, per-case deltas, the gate decision and reasons, per-category **failure-attribution
statistics**, a **cost/latency summary** (candidate model calls + eval latency; fake mode has no token
cost), each round's candidate prompt, the run **configuration snapshot**, and the **determinism** basis.
Committed copies of a representative run are checked in as `optimization_report.example.json` / `.md`.

## Design note

The regression loop is deliberately built as a **wrapper layer** around the PromptIter engine rather
than a fork of it. `engine.Run` still owns optimization — backward, aggregate, optimize, apply patch,
and its own `MinScoreGain` acceptance loop are untouched. The wrapper adds three things the engine
does not provide for a regression gate: (1) full per-case baseline and candidate evaluations (via
`AgentEvaluator.Evaluate` with `WithRunDetailsEnabled(true)`), (2) failure attribution, and (3) a
multi-criterion acceptance gate that vetoes the engine's decision when needed. Keeping the engine
unmodified means the example tracks upstream changes for free and stays a *usage* example, not a
parallel implementation.

All decision logic lives in the `pipeline` package as **pure functions over
`*evaluation.EvaluationResult`**. That is the single most important design choice: it makes
attribution, delta classification, the gate, and report generation unit-testable with synthetic
results — no model, no network, no fixtures. The `main` package only orchestrates (builds evaluators,
runs the engine, writes files); everything worth testing is in `pipeline`.

**Failure attribution** maps each failing case to one of six categories (response mismatch, tool-call
error, tool-arg error, route error, format error, knowledge recall). The primary signal is the
criterion type on the metric result; a free-text reason disambiguates sub-categories that share a
type (a tool-trajectory failure could be a wrong tool, wrong args, or wrong sequence). Because the
signal is inherently heuristic, the classifier is intentionally simple and its keyword boundaries are
pinned by tests, which double as executable documentation.

**Determinism** for the fake path uses a scripted candidate model plus deterministic
backwarder/aggregator/optimizer implementations (mirroring the engine's own tests). Faking the LLM
backwarder alone is fragile — its output must reference runtime-real step and surface IDs — so the
fake path swaps the whole collaborator trio for deterministic stages, while the `openai` path keeps
the real LLM-driven ones. This is a deliberate deviation that buys a hermetic, sub-second CI run.

**The gate** enforces four independent criteria, all of which must pass to accept: `validation_improves`
(mean gain ≥ threshold), `no_new_hard_fail` (no `pass → fail`), `key_cases_protected` (named critical
cases hold), and `within_budget` (candidate model calls ≤ budget, skipped when unset). `no_new_hard_fail`
is the overfitting veto: a candidate that lifts the aggregate while breaking a previously-passing case is
rejected despite the higher mean. `key_cases_protected` is stricter still, and the report names the
business-critical case that broke. **Audit** artifacts capture every run fact the issue enumerates —
baseline/candidate scores, per-case deltas, gate decision and reasons, per-category attribution
statistics, a cost/latency summary, each round's prompt, the config snapshot, and the determinism basis —
as both machine-readable JSON and human-readable Markdown, and the process exits non-zero on rejection.

## What it does

- Evaluates the baseline prompt on train + validation with per-case run details, and attributes each failure.
- Runs PromptIter to optimize the single `candidate#instruction` surface.
- Re-evaluates the accepted prompt on the held-out sets and applies the acceptance gate.
- Writes `optimization_report.json` and `optimization_report.md` (baseline, candidate, per-case deltas, gate decision + reasons, engine rounds).
- Exits non-zero when the gate rejects the candidate, so CI can block the change.
- Runs with no API key in `fake` mode; supports a real endpoint via `-model-source openai`.
