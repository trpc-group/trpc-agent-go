# PromptIter Regression Loop

This example connects PromptIter to an external held-out release gate. It keeps
the real agent, Evaluation Service, metric registry, PromptIter engine, Profile
compiler, and report publisher in both execution modes; only the model
implementation changes.

The supplied deterministic scenario attempts three candidates:

1. a balanced prompt that improves held-out results and is accepted;
2. an ineffective prompt that has insufficient held-out gain and is rejected;
3. an overfit prompt that improves training behavior but fails the held-out
   `critical_case` check and is rejected.

The accepted Profile from the first round remains the released Profile. A
rejected candidate never becomes the input to the next PromptIter round.

## Deterministic mode

The committed configuration uses `"mode": "deterministic"`. Scripted models
run locally without credentials while exercising the same pipeline as live
models. Serial evaluation settings, zeroed durations, and scripted responses
make the committed sample reproducible.

From the repository root, run:

```bash
go -C examples/evaluation run ./promptiter/regressionloop \
  -config ./promptiter/regressionloop/data/promptiter.json \
  -data-dir ./promptiter/regressionloop/data \
  -output-dir ./promptiter/regressionloop/output \
  -run-id my-deterministic-run
```

`-config`, `-data-dir`, `-output-dir`, and `-run-id` are required. The run ID
must be a single safe path component and must be new: a published bundle is
immutable and the command refuses to replace it. The default overall timeout is
two minutes; use `-timeout`, for example `-timeout 5m`, when a live run needs a
larger bound.

## Live mode

Copy `data/promptiter.json` to a private configuration file, set `mode` to
`live`, and configure each role independently. The candidate generates agent
answers, and the worker supplies PromptIter's backwarder, aggregator, and
optimizer models. The judge is used only when at least one configured metric
has an `llmJudge` criterion. The supplied rule-based metrics do not call it, so
the `judge` object and its credential are optional for the committed example.

```json
{
  "candidate": {
    "model": "candidate-model",
    "baseURL": "https://candidate.example.com/v1",
    "apiKeyEnv": "CANDIDATE_API_KEY",
    "inputPerMillion": 2.5,
    "outputPerMillion": 10
  },
  "judge": {
    "model": "judge-model",
    "baseURL": "https://judge.example.com/v1",
    "apiKeyEnv": "JUDGE_API_KEY",
    "inputPerMillion": 1,
    "outputPerMillion": 4
  },
  "worker": {
    "model": "worker-model",
    "baseURL": "https://worker.example.com/v1",
    "apiKeyEnv": "WORKER_API_KEY",
    "inputPerMillion": 3,
    "outputPerMillion": 12
  }
}
```

These objects replace the corresponding role objects in the full configuration;
the remaining fields are still required. Set the named variables only in the
process environment:

```bash
export CANDIDATE_API_KEY='...'
export WORKER_API_KEY='...'

go -C examples/evaluation run ./promptiter/regressionloop \
  -config /secure/path/promptiter-live.json \
  -data-dir ./promptiter/regressionloop/data \
  -output-dir ./promptiter/regressionloop/output \
  -run-id live-001 \
  -timeout 10m
```

`baseURL` may be omitted only when using the default `OPENAI_API_KEY`
credential name and the model client's default endpoint. A non-default
`apiKeyEnv` requires an explicit HTTP(S) Base URL. Base URLs containing user
information, query parameters, or fragments are rejected. Prices are optional,
nonnegative currency units per million input/output tokens; estimated cost is
unknown when token usage or either applicable price is unavailable.

The report records effective model names, non-secret Base URLs, API-key
environment-variable names, pricing, and `maxRetries: 0` for each role used by
the run. It includes the judge role only when the metric file contains an
`llmJudge` criterion; in that case a valid `judge` object and its named
credential are required. The client also disables automatic retries, so retry
attempts are not hidden from the cumulative ledger. API-key values are read
from the environment and are not written to the bundle.

## Held-out release gate

`trainEvalSetId` supplies both PromptIter training and its internal
search-validation input. It must differ from `validationEvalSetId`. Held-out
validation results are never passed back as losses or gradients. Instead, the
outer loop evaluates each complete candidate Profile once on validation and
compares it with the current released Profile.

A candidate is released only if every enabled check passes: successful and
shape-complete evaluation, minimum validation gain, hard-failure limit,
critical-case rules, per-case score-drop limit, and configured resource
budgets. Training improvement cannot compensate for a held-out failure.

Independent evaluators can apply a PromptIter Profile through the public
`engine.CompileProfile` helper. It validates the described structure,
normalizes the complete Profile, and returns the agent run options needed by
the Evaluation Service without exposing PromptIter's internal compiler.

The resource fields are `maxModelCalls`, `maxToolCalls`, `maxTokens`,
`maxEstimatedCost`, and `maxLatencyMillis`. They apply cumulatively to baseline
work plus accepted, rejected, and failed rounds across candidate, judge, and
worker calls. Before expensive stages the loop checks a catalog-based model-call
projection. Because PromptIter's worker
calls depend on runtime failures and traces, `maxModelCalls` is also enforced
atomically before every provider request; the projection is an early rejection
optimization, not the hard-limit mechanism. The gate checks all measured
cumulative dimensions afterward. Omitting a field disables that budget.
Setting it explicitly to zero enables the budget and permits zero use; it is
not the same as omission. If an enabled measurement is unknown, the gate fails
closed.

## Artifacts, permissions, and security

A successful accepted run publishes one bundle atomically:

```text
output/<run-id>/optimization_report.json
output/<run-id>/optimization_report.md
output/<run-id>/candidate_profile.json
```

Rejected or failed runs do not publish a candidate Profile. Publication writes
all files to a private staging directory and renames the complete directory into
place. On POSIX systems, newly created output and bundle directories use mode
`0700`, and artifact files use mode `0600`. If the output root already exists,
its permissions remain the operator's responsibility.

Treat every bundle as sensitive. It contains candidate prompts, evaluation
reasons, endpoint metadata, and operational usage. Use a private output path,
do not put credentials in prompts, eval data, model output, filenames, or Base
URLs, and review artifacts before sharing or committing them. The publisher
rejects API keys and credential-like headers in a candidate Profile, but it
cannot prove that arbitrary model-generated text is free of secrets.

The report does not persist full execution traces or raw invocation details.
Attribution uses the evidence retained during the run, but publishes only the
normalized baseline, candidate deltas and failure attribution, gate checks,
prompts, and usage. Dynamic prompts, reasons, and errors are
credential-pattern redacted and byte-bounded at the rendering boundary without
changing the evidence consumed by the release gate. It is therefore unsuitable
for trace replay or step-level debugging, and attribution categories are
diagnostic evidence rather than proof of root cause.

## Reading the report

Start with top-level `status` and `accepted`. `accepted: true` means at least one
candidate changed the released Profile and the final bundle includes
`candidate_profile.json`; individual later rounds may still be rejected. For
each round, inspect `gate.accepted`, then failed checks and their `observed`,
`limit`, and `reason` values. `delta.metrics` identifies newly passing,
regressed, or unchanged evidence, while `attributions` and
`attributionCounts` summarize non-passing cases. Usage measurements include a
`known` flag; a numeric zero with `known: false` does not mean zero consumption.

The Markdown report is a review summary. Use the JSON report for automation and
verify its input fingerprints before comparing or promoting the complete
candidate Profile. A live-mode acceptance applies only to the configured data,
models, and thresholds; it is not a generalization guarantee.
