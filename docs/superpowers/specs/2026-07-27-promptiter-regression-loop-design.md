# PromptIter Regression Loop Design

## Status

Proposed design for [Issue #2003](https://github.com/trpc-group/trpc-agent-go/issues/2003).

The implementation is intentionally example-scoped. It demonstrates a
production-oriented release decision loop without introducing a new framework
API or changing existing PromptIter defaults.

## Problem

Evaluation and PromptIter already provide the two core capabilities needed for
prompt optimization:

- Evaluation turns Agent behavior into repeatable metric results and traces.
- PromptIter uses training failures to generate candidate Profile patches and
  evaluates those candidates during optimization.

Neither capability alone answers whether a candidate is safe to promote. A
candidate may improve training results while regressing held-out validation
cases, preserving an existing hard failure, exceeding its resource budget, or
producing incomplete evidence. A score-only acceptance decision also does not
provide enough evidence for a reviewer to audit or reproduce a promotion.

The example must connect baseline evaluation, failure attribution, PromptIter
optimization, held-out regression, release gating, and artifact publication
into one reproducible workflow.

## Goals

The example will:

1. Run baseline training and validation evaluations through the existing local
   Evaluation Service.
2. Attribute every failed case using metric, response, trace, route, and tool
   trajectory evidence.
3. Generate candidate Profiles through the existing PromptIter Engine.
4. Compare every candidate with the current released Profile by evaluation set,
   case, and metric.
5. Apply a fail-closed release gate that covers quality, safety, completeness,
   critical cases, and resource budgets.
6. Support deterministic offline execution and live OpenAI-compatible models
   through the same pipeline.
7. Publish a consistent audit bundle containing JSON, Markdown, and an accepted
   candidate Profile when applicable.
8. Run the six supplied cases without credentials in less than three minutes.
9. Keep the implementation smaller and easier to review than competing
   framework-level regression packages.

## Non-goals

The change will not:

- add a public `evaluation/workflow/promptiter/regression` package;
- change PromptIter's existing `AcceptancePolicy` or default behavior;
- add a second evaluation schema, metric engine, tool matcher, or trace model;
- use validation failures as optimization gradients;
- treat trace replay as evidence that a new candidate behaves correctly;
- automatically overwrite the source prompt;
- support provider-specific APIs beyond the existing OpenAI-compatible model
  integration; or
- guarantee that one live optimization run generalizes beyond the supplied
  evaluation data.

## Location and Change Boundary

All feature code and data will live under:

```text
examples/evaluation/promptiter/regressionloop/
```

The existing `evaluation`, PromptIter, agent structure, profile compiler, and
model packages will be consumed through their current APIs. Core-package changes
are out of scope unless implementation proves that an existing API cannot carry
required evidence. Any such blocker requires a separate design review before
the scope expands.

The example will use one `main` package. Its types and helpers remain unexported
so the example does not create a parallel public contract.

## Architecture

The pipeline is split into four responsibilities:

```text
configuration and runtime assembly
              |
              v
Evaluation Service + PromptIter Engine
              |
              v
normalization, attribution, delta, and release gate
              |
              v
atomic audit bundle publication
```

### Runtime assembly

Runtime assembly loads input files, validates configuration, creates the target
Agent and Evaluation Service, constructs PromptIter collaborators, and selects
either deterministic or live model components.

The mode switch occurs only at the model boundary. Evaluation, PromptIter,
normalization, attribution, delta calculation, gating, and reporting are shared.
This prevents fake and live modes from developing different scoring or tool
matching semantics.

### Framework execution

The example uses the existing Evaluation Service to evaluate Agents and the
existing PromptIter Engine to execute training evaluation, loss extraction,
backward attribution, gradient aggregation, patch generation, candidate Profile
construction, and validation evaluation.

Each outer pipeline round invokes PromptIter for one candidate round. The outer
pipeline owns release state and decides which Profile becomes the input to the
next outer round. PromptIter's internal acceptance result is retained for audit,
but it does not authorize publication.

### Regression analysis

Framework results are projected into an example-local report model. Projection
preserves evaluation set ID, case ID, metric name, score, status, reason, actual
and expected invocation evidence, trace summary, and resource usage.

Pure functions perform failure attribution, delta calculation, completeness
validation, and release gating. The pure layer does not execute models or mutate
Profiles, which makes release decisions deterministic and directly testable.

### Artifact publication

The report writer renders all artifacts in a staging directory, validates their
cross-references, and publishes the complete directory as one unit. Failed or
canceled runs still produce an audit report when enough safe evidence exists,
but never produce a promotable candidate artifact.

## Profile State Model

The pipeline tracks three distinct Profile concepts:

- `initialProfile`: the Profile compiled from the source prompt at run start;
- `searchProfile`: the Profile supplied to the next PromptIter round; and
- `releasedProfile`: the latest Profile accepted by the external release gate.

At run start, all three refer to the normalized initial Profile. A generated
candidate may become the next search Profile according to search policy, while
only the release gate can replace `releasedProfile`.

Every release delta is calculated against `releasedProfile`, not against a
previous rejected candidate. This prevents a sequence of regressions from being
accepted merely because the latest candidate improves over an already rejected
search state.

Profile identity includes `StructureID` and the complete set of surface
overrides. The implementation must not silently rebind a stale Profile to a new
structure or reduce a Profile to instruction text. Accepted artifacts contain
the complete effective Profile, including explicit empty overrides.

## Pipeline Flow

### 1. Input loading and preflight

The CLI loads:

- `train.evalset.json`;
- `validation.evalset.json`;
- `metrics.json`;
- `promptiter.json`; and
- the baseline prompt source file.

Configuration decoding rejects unknown fields, trailing JSON values, invalid
thresholds, duplicate case IDs, duplicate metric identities, unknown critical
case IDs, empty data sets, unsafe output paths, and invalid model-role settings.

The preflight builds an expected validation shape keyed by evaluation set, case,
and metric. This shape is later used to detect missing, duplicate, unexpected,
or unevaluated evidence even when both baseline and candidate omit the same
item.

### 2. Baseline evaluation

The Evaluation Service evaluates `initialProfile` on training and validation
sets separately with run details enabled. Baseline evaluation records per-case
metrics, pass/fail status, reasons, traces, actual and expected invocations, tool
trajectories, model usage, tool calls, and latency.

A failed, canceled, incomplete, or shape-invalid baseline makes the run
non-releasable. The report records the failure instead of fabricating an empty
baseline.

### 3. Failure attribution

Attribution is metric-scoped and evidence-first. It uses this precedence:

1. explicit structured execution or trace errors;
2. tool trajectory differences for a failed tool metric;
3. structured-output parse or schema failures;
4. route evidence for a failed route metric;
5. retrieval evidence for a failed knowledge metric;
6. final-response or rubric reason; and
7. an `unclassified_failure` fallback containing the failed metric reason.

Supported categories are:

- `final_response_mismatch`;
- `tool_call_error`;
- `tool_parameter_error`;
- `route_error`;
- `format_error`;
- `knowledge_retrieval_insufficient`;
- `runtime_error`; and
- `unclassified_failure`.

Every failed case receives at least one attribution. Attribution does not infer
a tool problem from unrelated tool evidence when only a final-response metric
failed. Dynamic evidence is bounded and redacted before persistence.

Training attributions may be converted into metric-keyed PromptIter `LossHint`
values. Validation attributions are report-only and never enter PromptIter
backward, aggregation, or optimization stages.

### 4. Candidate generation

PromptIter runs one round using `searchProfile`, the training set, configured
target surfaces, and validation evaluation required by the existing Engine.
The generated `OutputProfile` is the candidate of that outer round.

The candidate must retain the current structure identity and contain only
allowed target-surface modifications. Invalid, empty, or mismatched patches are
recorded as rejected round failures.

### 5. Candidate regression

The candidate validation result produced through the Evaluation Service is
normalized against the preflight validation shape. Deltas are keyed by:

```text
evaluation set ID + case ID + metric name
```

Case and metric deltas distinguish:

- newly passing;
- newly failing;
- improved;
- regressed;
- unchanged passing;
- unchanged failing;
- missing; and
- unexpected.

The report records both the delta against `initialProfile` for overall
explanation and the delta against `releasedProfile` used by the release gate.

### 6. Release gate

The gate rejects unless all configured checks pass. It checks:

- run and evaluation status are successful;
- baseline and candidate evidence exactly match the expected shape;
- weighted validation score gain meets the configured minimum;
- candidate hard-failure count does not exceed the configured maximum, which is
  zero by default;
- no critical case or critical metric regresses beyond its limit;
- no case exceeds the maximum permitted score drop;
- model-call, tool-call, token, estimated-cost, and latency budgets are met; and
- required resource measurements are available.

Unknown resource measurements fail closed when their corresponding budget is
enabled. Reported scores and usage are the exact values consumed by the gate.
Training improvement cannot compensate for a validation failure.

Gate output contains stable check identifiers, observed values, limits, and
human-readable reasons. Exact identifiers allow tests and automation to depend
on decisions without parsing prose.

### 7. State transition

If the release gate accepts the candidate, it becomes `releasedProfile`. Search
policy then determines whether it also becomes `searchProfile`. The default
example policy advances search only with a release-accepted candidate, which is
the safest and simplest behavior.

If the candidate is rejected, both Profiles remain unchanged for the next
round. This prevents rejected content from influencing later candidates and
keeps report deltas easy to reproduce.

### 8. Report and promotion artifact

Each completed run publishes:

```text
output/<run-id>/optimization_report.json
output/<run-id>/optimization_report.md
output/<run-id>/candidate_profile.json   # accepted runs only
```

The JSON report records schema version, input fingerprints, seed, mode, safe
model-role configuration, baseline, all rounds, failure-attribution counts,
initial/released deltas, gate checks, usage, cost, latency, and terminal status.

The Markdown report explains whether promotion is recommended, lists every
rejection reason, summarizes case changes, and renders dynamic content using
escaped table cells and content-aware code fences.

The example never writes API keys, authorization headers, raw environment
variables, or unbounded model content. A rejected, failed, or incomplete run
cannot publish `candidate_profile.json`.

## Execution Modes

### Deterministic mode

Deterministic mode is the default and requires no credentials. It uses scripted
model behavior at the existing model boundary while retaining the real Agent,
runner, Evaluation Service, metrics, traces, PromptIter Engine, Profile compiler,
and patch application paths.

The fake model is keyed by explicit scenario and request role, not by wall-clock
timing or random map iteration. A fixed clock, run ID, and seed can be injected
by tests so committed example reports are byte-for-byte reproducible.

The supplied data contains three training and three validation cases covering:

- a candidate that improves held-out validation and is accepted;
- a candidate that produces no meaningful validation gain and is rejected; and
- a candidate that improves training while regressing validation and is
  rejected as overfit.

### Live mode

Live mode uses the existing OpenAI-compatible model implementation. Candidate,
judge, and PromptIter worker roles have separate configuration:

- model name;
- base URL;
- API-key environment-variable name; and
- optional per-role pricing metadata.

Secrets are read only from named environment variables and are never copied to
the report. Roles may share a compatible endpoint, but the implementation does
not assume that they do.

Live mode shares the deterministic pipeline and report schema. It may produce a
rejected result; the example must not manipulate thresholds to force a
successful demonstration.

## Resource Accounting

A shared run ledger observes model and tool execution at their actual call
boundaries. It records stage, role, attempt, input/output tokens when available,
tool calls, latency, and configured price calculation.

The ledger covers candidate inference, judge evaluation, backward, aggregation,
optimization, and tool turns. It avoids adding a second coarse estimate per case.
If an underlying client owns retries, the wrapper records every observable
attempt or disables hidden retries where supported so budgets are not silently
undercounted.

Before a live optimization stage begins, the pipeline reserves enough remaining
budget for mandatory candidate validation and report completion. Budget
exhaustion cancels further model work, records a terminal rejection, and leaves
no promotable artifact.

## Error and Cancellation Semantics

`context.Context` is propagated through every Evaluation, PromptIter, model,
tool, and publication operation. Cancellation is never converted into success.

Preflight errors return before model execution. Runtime errors are wrapped with
stage and round context. When safe audit information exists, the pipeline writes
a failed report; report-publication failure is returned to the caller and cannot
be hidden by the original runtime error.

Resources have explicit ownership. The runtime closes Evaluation managers and
runners exactly once. Tests use bounded contexts so deadlocks cannot wait for the
repository-wide test timeout.

## Report and Configuration Compatibility

The example owns report schema version `1`. Callers cannot supply arbitrary
schema versions. New optional fields may be added compatibly to the example
artifact, while incompatible changes require a new version and updated golden
reports.

Configuration defaults are conservative:

- deterministic mode;
- no source write-back;
- zero allowed hard failures;
- validation shape completeness required;
- rejected candidates do not advance search; and
- resource budgets disabled only when their fields are omitted, not when
  measurements are missing.

## Testing Strategy

Implementation follows test-driven development. Production behavior is added
only after its failing test is observed.

### Pure unit tests

Table-driven tests cover:

- all delta transitions at case and metric level;
- duplicate, missing, unexpected, and not-evaluated evidence;
- metric-scoped failure attribution and the fallback category;
- tool name, argument, result, order-sensitive, and order-insensitive evidence;
- hard failures that are new, retained, resolved, or reclassified;
- critical case and metric regression;
- negative, zero, and boundary thresholds;
- unknown resource measurements and budget exhaustion;
- deterministic gate outcomes with exact per-case assertions;
- Markdown table and code-fence escaping; and
- configuration unknown fields, trailing data, unsafe paths, and secret
  redaction.

### Framework integration tests

Integration tests use the supplied files and deterministic model to prove that:

- Evaluation Service reads the evalsets and metrics;
- PromptIter produces a real candidate Profile;
- editing expected results or metrics changes the measured outcome;
- validation evidence never becomes a training `LossHint`;
- rejected candidates never become release or search baselines;
- accepted Profiles retain complete surface overrides and structure identity;
- canceled and failed runs cannot publish candidate artifacts; and
- the six-case run completes in less than three minutes without credentials.

### Artifact tests

Artifact tests verify byte-stable deterministic output, valid JSON, report
cross-references, accepted-only candidate publication, rollback on write failure,
path containment, safe file permissions, and absence of configured secrets.

Live mode receives construction and configuration tests. Network-dependent live
runs are documented manual smoke tests and are not required by CI.

## Validation

During implementation, targeted tests run from `examples/evaluation`. Before
delivery, validation includes:

```text
go test ./promptiter/regressionloop -count=1
go test -race ./promptiter/regressionloop -count=1
go test ./...
go vet ./...
go run ./promptiter/regressionloop
```

Repository-level validation additionally follows `AGENTS.md`, including the root
module, independent library modules, example checks, formatting, imports, and
lint in proportion to the final diff.

## Expected Review Advantages

This design is intended to outperform existing submissions by combining:

- the real Evaluation and PromptIter path instead of a parallel simulation;
- deterministic and live execution without duplicate scoring implementations;
- correct separation of search and release state;
- strict fail-closed completeness and safety gates;
- complete Profile artifacts rather than prompt-only write-back;
- consistent, redacted, reproducible audit publication; and
- an example-only change boundary with no new public framework contract.

The target is a focused implementation of roughly 4,000 added lines or fewer,
including tests but excluding compact input fixtures and generated example
reports. Scope will be reduced before public APIs or core-package changes are
introduced.
