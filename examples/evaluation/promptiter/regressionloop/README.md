# PromptIter regression loop

This example runs an auditable Evaluation + Optimization loop without an API
key. It uses the repository's normal execution path rather than a parallel demo
pipeline:

1. standard `EvalSet` and `EvalMetric` assets are loaded into the Evaluation
   Service;
2. a deterministic no-API Agent runs through the real `Runner` and emits normal response, tool, and trace events;
3. the existing PromptIter Engine performs train evaluation, backward
   attribution, aggregation, optimization, candidate validation, and round
   acceptance;
4. the regression audit layer consumes `engine.RunResult` and adds failure
   categories, per-case and per-metric deltas, strict release gates, and JSON /
   Markdown reports.

The audit layer does not rerun candidate validation or implement another
optimization state machine. For intermediate candidates, train evidence is
reused from the next PromptIter round when that candidate becomes the round
input. When a round terminates the run, the Engine performs one final train
evaluation for that round's output profile and stores it as
`RoundResult.CandidateTrain`. The audit layer consumes this direct evidence
before falling back to next-round train evidence.

## Design summary

The regression loop is an audit layer around the existing PromptIter Engine,
not a second optimizer. PromptIter remains responsible for training,
optimization rounds, candidate evaluation, and its own exploration acceptance.
The Analyzer consumes the resulting `engine.RunResult`, compares every
candidate with fixed train and validation baselines, attributes observed
failures, and applies a deterministic release policy. The first version
deliberately supports exactly one target surface: the sole
`Configuration.TargetSurfaceIDs` entry must equal `RunSpec.TargetSurfaceID`.
This prevents a configuration from authorizing several surfaces while the
release audit protects only one.

Usage accounting has one authority. Calls, input tokens, output tokens, total
tokens, and completeness come from Engine telemetry; callers cannot replace
or declare those fields. Each `RoundResult` carries the usage and execution
duration of that round, while the audit exposes both round-only and cumulative
candidate summaries. Candidate resource gates use the cumulative summary, so
an early candidate cannot spend a later round's budget and a late candidate
cannot hide earlier work. A caller may supplement only facts the Engine cannot
derive: measured PromptIter latency and an exact baseline/per-round cost
breakdown with its pricing source. The Analyzer rejects missing, extra, or
inconsistent cost entries before any budget rule runs. `promptIterLatency`
measures only the `Engine.Run` interval, not report rendering or the
surrounding application pipeline.

Evidence collection and artifact retention are separate phases. Expected and
actual final responses, tool names and arguments, routes, trace inputs and
outputs, and evaluator reasons remain available in memory while attribution
and delta analysis run. The Analyzer attributes failures in baseline train,
baseline validation, candidate train, and candidate validation snapshots; the
phase and candidate ID make each finding unambiguous. Only after decisions are
complete does `AuditPolicy` redact, truncate, or omit raw content from JSON
and Markdown. Disabling `IncludeRawContent` therefore reduces persistence
risk without weakening the classifier.

The Regression release gate is the final publication authority. PromptIter
acceptance is advisory by default and can be made mandatory with
`RequirePromptIterAcceptance`. Consumers must obtain the write-back profile
through `RunResult.SelectedProfile()` after an `accepted` decision; they must
not publish `engine.AcceptedProfile`, which represents PromptIter's separate
round policy. The accessor rejects missing, ambiguous, or rejected selections
and returns a clone of the uniquely selected candidate.

Tests separate integration behavior from accuracy evidence. Complete Analyzer
scenarios verify wiring across PromptIter, attribution, deltas, gates, and
reports. Frozen corpora use custom metric names, mixed-language reasons,
multiple simultaneous signals, incomplete evidence, threshold boundaries,
unknown cost, critical regressions, and train/validation divergence. Corpus
tests print expected-to-actual confusion matrices, enforce per-category or
per-decision minimums, and retain overall thresholds of 75% for attribution
and 80% for release decisions.

When `numRuns` is greater than one, PromptIter loss extraction, acceptance,
regression deltas, and reports all use the Evaluation Service's aggregated
case metrics. The individual runs remain available for stability calculations
and audit evidence. If an aggregate metric fails, backward propagation uses an
actual failed run and its trace rather than an unrelated successful run.

Run from `examples/evaluation`:

```bash
go run ./promptiter/regressionloop \
  -scenario success \
  -run-id demo-success \
  -output ./output

go run ./promptiter/regressionloop \
  -scenario no-effect \
  -run-id demo-no-effect \
  -output ./output

go run ./promptiter/regressionloop \
  -scenario overfit \
  -run-id demo-overfit \
  -output ./output
```

Expected decisions are `accepted`, `rejected`, and `rejected`.

- `success` is a target-driven, multi-round optimization. Starting from a
  validation score of `0.733`, it repairs one capability per round: refund
  policy, specialist routing, JSON formatting, and order lookup, then stops at
  `1.000` after exceeding the configured `0.95` target. The expected score
  path is `0.733 -> 0.767 -> 0.833 -> 0.900 -> 1.000`.
- `no-effect` changes the prompt without changing behavior, so the validation
  gain gate rejects it.
- `overfit` memorizes exact training inputs and adds an unsafe disclosure rule.
  Training improves, while validation regresses and introduces a critical
  safety hard failure, so the candidate is rejected.

The fake PromptIter acceptance threshold is deliberately permissive so all
regression candidates reach the audit layer. The progressive success scenario
instead requires a positive per-round gain and uses target-score early stop.
PromptIter acceptance is retained as audit evidence and a warning, but does
not override the Regression release decision unless
`RequirePromptIterAcceptance` is enabled. Production write-back is controlled
by target-surface scope, validation regression rules, hard-failure rules,
evidence completeness, and resource budgets. After an accepted decision,
write back only the clone
returned by `RunResult.SelectedProfile()`, never `engine.AcceptedProfile`.

The final target-reaching profile is evaluated on the train set before the run
is finalized. The success scenario can therefore keep the
train-versus-validation generalization-gap rule enabled, while the separate
`overfit` scenario shows that training gains cannot compensate for validation
and safety regressions. This final evidence adds one train evaluation to the
stopping round and is included in that round's usage and every subsequent
cumulative summary.

Resource budgets require Engine usage that covers the whole optimization
pipeline. The PromptIter Engine aggregates telemetry from Evaluation,
backward, aggregation, and optimization stages. Missing provider usage,
unreported custom-component calls, or separate teacher/judge calls keep the
summary incomplete and produce an `inconclusive` release decision when a
budget is enabled. This deterministic example owns every model-bearing stage
and explicitly declares that trace usage covers all Evaluation calls. It also
verifies the Engine call total against the support Agent's actual call counter
before declaring the summary complete. Production integrations must leave
that declaration false when LLM judges, expected runners, or custom evaluators
perform calls outside the retained traces. Calls and tokens always come from
Engine-owned aggregate and round telemetry; the example supplements only
measured PromptIter latency and an explicit zero-cost baseline/per-round
breakdown.

The example has no random component. Its configured seed is retained as input
metadata, while `seedApplied` remains false; the report therefore does not
claim that an unused seed controlled execution. Integrations with stochastic
components must set `seedApplied` only after passing the seed to every covered
component.

Every PromptIter round remains present in the audit report, even when two
rounds produce the same profile hash. Candidate selection may choose one of
those rounds, but collection never discards round-level evidence.

## Inputs

The `data` directory contains standard Evaluation assets and optimization
policy:

```text
data/
  baseline_prompt.txt
  train.evalset.json
  validation.evalset.json
  metrics.json
  promptiter.json
```

The example contains four training cases and five validation cases. The
validation-only privacy case is marked critical in `promptiter.json`, keeping
business release policy out of the generic EvalSet schema.

## Outputs

Each run writes the following immutable artifacts below `<output>/<run-id>/`:

```text
optimization_report.json
optimization_report.md
```

Both files are checked into `sample_output/` because the example contract
requires machine-readable and human-readable output. The JSON fixture keeps
raw content disabled and uses compact encoding, reducing the four-round sample
from roughly 191 KB / 6,000 lines to about 90 KB / one line. The Markdown
fixture remains the primary artifact for human review.

The checked-in README and generated Markdown report use English to match the
repository's default documentation, JSON field names, metric identifiers, and
CI output. A localized report should be implemented as an explicit renderer
locale rather than mixing languages in the default artifact.

The report includes the baseline and candidate prompts, effective PromptIter
execution settings, per-case observations,
metric reasons, tool trajectories, execution traces, failure attribution,
train and validation deltas, every gate decision and reason, round and
cumulative calls, tokens, cost and latency, seed plus its applied flag,
model/fake-engine metadata, and input fingerprint.

Raw user inputs, final responses, tool payloads, and trace snapshots are not
persisted by default, but remain available to in-memory attribution. The audit
policy can enable them for trusted data and set a per-field size limit;
secret-like keys and inline credentials are still
redacted. This example keeps raw content disabled so its audit artifacts stay
compact even though every asset and response is synthetic. The JSON and
Markdown files are published as one immutable directory bundle, so a failed
write cannot leave a partial report.

## Verification

```bash
cd evaluation
go test ./workflow/promptiter/regression/... -count=1

# Measure coverage through retained business scenarios and public contracts
# across the complete Regression package set.
go test ./workflow/promptiter/regression/... \
  -coverpkg=./workflow/promptiter/regression/... \
  -coverprofile=/tmp/promptiter-regression-cover.out \
  -count=1
go tool cover -func=/tmp/promptiter-regression-cover.out

go test ./workflow/promptiter/... -count=1

cd ../examples/evaluation
go test ./promptiter/regressionloop -count=1
```

Complete Analyzer scenarios cover successful generalization, no-effect
changes, overfitting, hard safety failures, missing evidence, unconfigured
metrics, target-surface violations, and complete/incomplete resource usage.
Independent frozen corpora measure release decisions and attribution rather
than allowing implementation-shaped metric names to prove their own rules.
They print confusion matrices, enforce per-class accuracy, require at least
80% overall release accuracy and 75% overall attribution accuracy, and require
non-empty explanations. The example also runs 30 complete success /
no-effect / overfit experiments and 10 normalized-report reproducibility
trials. The entire fake pipeline remains far below the three-minute acceptance
limit.

Tests in this example and the Regression packages are expected to begin from a
user-visible workflow, an evidence-integrity requirement, or a public safety
contract. Tests whose only purpose is to execute an unexported helper branch,
preserve an internal sort order, or increase statement coverage should not be
added. Statement coverage is diagnostic information, not an acceptance target.
