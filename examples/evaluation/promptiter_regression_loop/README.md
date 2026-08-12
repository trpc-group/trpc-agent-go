# PromptIter Regression Loop

This example closes the gap between prompt optimization and production-safe regression validation. It uses the real `evaluation/workflow/promptiter` engine to turn training failures into an instruction-surface patch, then evaluates the baseline and candidate independently before an outer gate decides whether the patch is publishable. No framework public API is changed.

## Run

Deterministic mode requires no API key and is the CI/review baseline:

```bash
cd examples/evaluation
go run ./promptiter_regression_loop \
  -config ./promptiter_regression_loop/data/config.json \
  -mode fake
```

Live mode uses the repository's OpenAI-compatible model abstraction with the DeepSeek variant. Unlike fake mode, it wires an LLM agent and runner into the framework's official `promptiter/optimizer.New` implementation, so the candidate patch is produced by the configured model:

```bash
export DEEPSEEK_API_KEY="..."
go run ./promptiter_regression_loop \
  -config ./promptiter_regression_loop/data/config.json \
  -mode live
```

On PowerShell, set the variable only in the current process:

```powershell
$env:DEEPSEEK_API_KEY = (Get-Content -Raw 'C:\path\to\key.txt').Trim()
go run ./promptiter_regression_loop -config ./promptiter_regression_loop/data/config.json -mode live
Remove-Item Env:DEEPSEEK_API_KEY
```

Keys are never written to the configuration or reports. Both evaluation and optimizer credentials are loaded and validated before any model is constructed or any dataset content is transmitted. Live execution defaults to `deepseek-v4-flash`, applies bounded retries and timeouts, and stops at the configured call, token, or CNY budget. The underlying OpenAI-compatible SDK retry layer is disabled so every HTTP attempt is owned, counted, and budgeted by this example. Fake mode uses zero model calls.

The optimizer has a sub-budget inside the global gate. Its model, base URL, API-key environment variable, and token prices inherit the evaluation settings only when they refer to the same model endpoint. An independently configured endpoint requires its own API-key environment variable, and an independent model or endpoint requires explicit input and output prices. This prevents evaluation credentials from being forwarded to another provider and keeps optimizer usage priced with the model that produced it. Temperature, output-token limit, timeout, and retries can also be overridden independently.

Before the optimizer runs, the pipeline reserves enough global calls, tokens, and estimated cost for the remaining candidate training evaluation and repeated validation runs. Preflight uses one token per UTF-8 byte as a provider-independent upper bound rather than an average bytes-per-token ratio; provider-reported usage remains the final ledger after a request. Optimizer retries consume both budgets.

The sample configuration allows 165 total HTTP attempts, 500,000 total tokens, and 20 CNY. Within that total, the one-round optimizer is limited to three attempts, 65,536 tokens, and 1 CNY. The larger token ceilings accommodate the conservative preflight bound; they do not change the call or monetary limits. The optimizer token allowance includes the official structured-output schema as well as the baseline and training gradients.

Optimizer errors do not silently fall back to the deterministic patcher. A timeout, exhausted budget, invalid structured response, or invalid patch selects the baseline, writes a rejected audit report, and makes the command exit non-zero. Authentication failures are not retried. This makes a failed live optimization inspectable without presenting a fake candidate as a real model result.

Outputs are atomically written to:

- `output/optimization_report.json`: schema `1.1` machine-readable prompts, per-run results, attribution, deltas, gate evidence, stage-level usage and latency, seed, separate evaluation/optimizer model configurations, and candidate source.
- `output/optimization_report.md`: human-readable acceptance decision and evidence.

## Design

PromptIter only receives the training eval set. Failure attribution, backward gradients, and aggregation remain deterministic and auditable in both modes. Fake mode uses a deterministic optimizer; live mode gives the aggregated training gradients and baseline surface to the official LLM-backed PromptIter optimizer. The `promptIter.optimizer` identifier is fixed to `evaluation/workflow/promptiter`; unsupported or misspelled identifiers fail configuration loading instead of silently selecting a different runtime. A tracking wrapper captures the returned patch and reason without changing framework APIs. The normal PromptIter engine performs its internal selection using the same training data, alongside training loss extraction, patch application, and its built-in score check. Reports call this value `innerTrainScore`, not validation score.

The candidate is then moved into a separate regression harness because the engine's built-in acceptance policy intentionally covers only score gain. The independent validation set is never passed to PromptIter and is reserved exclusively for the outer gate. A regression test inserts a validation-only sentinel and verifies that it never appears in the optimizer model request.

The outer harness runs every validation case `gate.passK` times (three by default) and compares the baseline and candidate case by case. It classifies failures as model, prompt, agent/tool, environment, format, knowledge, or unknown using explicit signals before conservative text inference. Deterministic scorers validate required facts, explicit forbidden phrases, equivalent contradiction cues, credential assignments (including quoted JSON keys and values plus common password punctuation), and JSON syntax without an LLM judge. Scoring and red-line detection inspect the original response, but credential values and sensitive token or private-key matches are replaced with `[REDACTED]` before output, attribution, traces, or reports are retained. Acceptance requires error-free validation runs, zero candidate hard failures, the configured mean-score gain, no new hard failure, no critical-case regression, non-regressing Pass^k stability, a non-negative paired-bootstrap 90% confidence-interval lower bound, and compliance with call, token, and CNY budgets. A retained or newly introduced red-line failure, or any transport/model error, vetoes the candidate even if its average score improves.

`data/metrics.json` declares the fixed policies supported by this example. Loading fails if a metric name, kind, threshold, Pass^k value, or bootstrap confidence differs from those supported settings, so a decorative configuration value can never disagree with the executed gate. All JSON inputs use strict decoding, so a misspelled budget field fails closed instead of silently disabling its limit.

Training scores never participate in the final gate, which prevents a PromptIter patch from being accepted merely because it memorizes optimization examples. Optimizer calls, tokens, estimated CNY cost, and latency are reported separately and included in total gate usage. The committed fixtures cover routing, tool arguments, structured output, missing knowledge, dependency timeouts, and secret disclosure. The pipeline seed is the bootstrap seed; conflicting values are rejected so the reported seed always reproduces the gate evidence. Elapsed time is recorded separately and excluded from the fingerprint.

The default fixture demonstrates an accepted improvement. A second fixture deliberately improves the training set while regressing independent validation cases, so reviewers can reproduce the rejection path:

```bash
go run ./promptiter_regression_loop \
  -config ./promptiter_regression_loop/data/config_overfit.json \
  -mode fake
```

Its reports are written under `output/overfit/`. The committed reports are generated in deterministic fake mode; live output is intentionally not committed.

## Test

```bash
cd examples/evaluation
go test ./promptiter_regression_loop
go vet ./promptiter_regression_loop
```

The tests cover attribution precedence, equivalent contradictions, special-character credential-disclosure counterexamples and audit redaction, paired deltas, Pass^k, bootstrap reproducibility, retained and new hard-failure vetoes, critical-case protection, provider-error rejection, overfitting rejection, strict configuration decoding, optimizer-identifier enforcement, seed consistency, safe configuration inheritance, fail-fast live credential validation, independent endpoint credentials and prices, conservative no-call budget preflight, HTTP retry accounting, optimizer and global budgets, validation-capacity reservation, validation-set leakage, fail-closed live reports, atomic report replacement, and end-to-end deterministic replay.
