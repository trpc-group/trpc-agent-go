# PromptIter Evaluation Regression Loop

This example implements an API-key-free, reproducible closed loop:

`baseline train/validation evaluation → failure attribution → PromptIter patch → candidate train/validation evaluation → per-case delta → acceptance gate → JSON/Markdown audit`

It uses the repository's standard `evalset.Invocation` / tool-trajectory shapes and PromptIter `Profile` / `PatchSet` contracts. The offline `DeterministicPromptIter` and local evaluator are the task's equivalent extension mechanism: they make the core workflow deterministic without weakening the validation gate. Training failure categories generate concrete remediation rules in each candidate prompt. The runner parses the candidate-and-seed marker from the generated prompt, then requires every explicit candidate output to carry the exact SHA-256 of that prompt's semantic content. Reusing a candidate ID or seed with different prompt text therefore fails before scoring. A production integration can replace the two interfaces with PromptIter Engine and Evaluation Service adapters while retaining delta, gate, and report code.

## Run without an API key

From this directory:

```bash
go run . \
  -data-dir ./data \
  -config ./promptiter.json \
  -prompt ./baseline_prompt.txt \
  -output-dir ./output
```

The command finishes in well under three minutes and creates:

- `output/optimization_report.json`
- `output/optimization_report.md`

The expected sample behavior is:

1. `candidate-overfit` improves two training cases but regresses the critical refund validation case. The gate rejects it for a new failure, new hard fail, critical-case regression, and excessive per-case score drop.
2. `candidate-balanced` fixes the unseen-city validation case, keeps the critical case stable, stays within the fake call/latency budget, and is accepted.
3. Explicit candidate reruns of `train_knowledge_no_gain` and `validation_coupon_no_gain` remain failed, demonstrating that a prompt change is not assumed to fix every case.

Committed example artifacts are in [`sample_output`](./sample_output). Re-running writes the same scores, case transitions, decision, prompt hashes, seed, fake-engine configuration, and usage values; only wall-clock fields can differ.

## Test

The reusable implementation and unit tests live in the `evaluation` module:

```bash
cd ../../../evaluation
go test ./workflow/promptiter/regression
```

To verify the CLI itself from the repository's `examples` module:

```bash
cd ../examples/evaluation/promptiter_regression_loop
go test .
go run . -output-dir "$(mktemp -d)"
```

## Inputs

| File | Purpose |
|---|---|
| `baseline_prompt.txt` | Source instruction surface; never overwritten automatically |
| `data/train.evalset.json` | Three training cases |
| `data/validation.evalset.json` | Three validation cases, including one critical overfit guard |
| `data/metrics.json` | Strict equivalent-local metric config for response, tool trajectory, route, format, knowledge, and fake rubric |
| `promptiter.json` | Seed, candidate patches, target surface, fake engine, and gate budgets |

The evalset `conversation` field is the normal expected invocation. `fakeResponses` is an additive offline extension keyed by the candidate marker embedded in the generated prompt. `promptSemanticSha256` binds each scripted output to its source prompt. Every configured candidate has an explicit output for all six cases, including unchanged outputs for ineffective optimization. The full pipeline rejects any baseline fallback on validation because that would not constitute rerunning the candidate prompt. Reports still record `responseVariantId`, `usedFallback`, and the source prompt hash for every case, and the reusable evaluator audits fallback provenance for standalone use. Each output records final response, tools, route/retrieval signals, trace, rubric, token/call cost, and latency. The loader is strict for safety-critical extension fields, so misspelled `critical`, `requiredFacts`, usage fields, or malformed hashes fail closed. Final-response similarity uses deterministic Unicode tokenization with ROUGE-1/ROUGE-L, so Chinese and English references are scored semantically instead of by binary string equality. `metrics.json` deliberately uses the documented equivalent-local schema rather than claiming to be a drop-in file for every Evaluation Service `criterion` variant.

`mode: "fake"` uses the deterministic prompt marker plus semantic-hash binding to select scripted fake-model output. `mode: "trace"` is a strict replay path. Every selected output must provide monotonic elapsed time; exact route, tool input/result, and final cumulative retrieval evidence; a terminal `final_response`/`llm` message; exact model/tool call counts; and terminal usage/rubric evidence matching the scored output. Failed tools remain part of the trajectory and must lead to a failed terminal response whose message equals the reported error. Unknown or look-alike kinds/statuses provide no evidence. Any missing or contradictory signal fails closed. Candidate-specific traces are required to demonstrate behavior changes; a single fixed trace naturally produces no gain and will be rejected by the gate.

## Acceptance policy

The gate is fail-closed and runs every check without short-circuiting:

- baseline/candidate case and metric coverage must match;
- every validation case must have a candidate-bound rerun; baseline fallback is rejected;
- train and validation case IDs must be disjoint, and configured critical IDs must exist;
- the candidate prompt hash must differ;
- validation macro score gain must meet the threshold;
- new failures and new hard failures must stay within policy;
- critical cases and every individual case must not exceed configured regression tolerance;
- cost, model/tool calls, and latency must stay within budget.

The pipeline independently revalidates evaluator provenance. Direct candidate cases must report the evaluated semantic prompt hash; any fallback result must exactly reproduce its verified baseline case, and validation fallback is rejected regardless. The Markdown report summarizes bound and fallback counts, while JSON retains per-case provenance and full trace evidence.

Training improvement never compensates for validation regression. All rejected and accepted rounds are retained, while only the highest-scoring gate-approved candidate is selected for the top-level decision. The report also records total usage for baseline plus every attempted round, not only the selected candidate.

See [`DESIGN.md`](./DESIGN.md) for the concise design note. For a detailed Chinese walkthrough of the Evaluation architecture, native PromptIter workflow, regression-loop implementation, core code, scoring, attribution, gates, audit artifacts, and real-LLM compatibility work, read [`LEARNING_GUIDE.zh_CN.md`](./LEARNING_GUIDE.zh_CN.md).
