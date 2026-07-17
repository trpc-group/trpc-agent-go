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

Live mode uses the repository's OpenAI-compatible model abstraction with the DeepSeek variant:

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

The key is never written to the configuration or reports. Live execution defaults to `deepseek-v4-flash`, applies bounded retries and timeouts, and stops at the configured call, token, or CNY budget. Fake mode uses zero model calls.

Model errors do not discard the audit trail: the affected case is recorded as a failed run and the final candidate is rejected. Authentication failures are not retried. This makes a failed live check inspectable without weakening the gate.

Outputs are atomically written to:

- `output/optimization_report.json`: machine-readable prompts, per-run results, attribution, deltas, gate evidence, usage, latency, seed, and model configuration.
- `output/optimization_report.md`: human-readable acceptance decision and evidence.

## Design

PromptIter only receives the training eval set. The deterministic PromptIter collaborators produce auditable gradients, aggregation, and a patch against the exported instruction surface; the normal PromptIter engine performs its internal selection using that same training data, alongside training loss extraction, patch application, and its built-in score check. The candidate is then moved into a separate regression harness because the engine's built-in acceptance policy intentionally covers only score gain. The independent validation set is never passed to PromptIter and is reserved exclusively for the outer gate.

The outer harness runs every validation case three times and compares the baseline and candidate case by case. It classifies failures as model, prompt, agent/tool, environment, format, knowledge, or unknown using explicit signals before conservative text inference. Deterministic scorers validate required facts and JSON syntax without an LLM judge. Acceptance requires the configured mean-score gain, no new hard failure, no critical-case regression, non-regressing Pass^k stability, a non-negative paired-bootstrap 90% confidence-interval lower bound, and compliance with call, token, and CNY budgets. A single red-line failure vetoes the candidate even if its average score improves.

Training scores never participate in the final gate, which prevents a PromptIter patch from being accepted merely because it memorizes optimization examples. The committed fixtures cover routing, tool arguments, structured output, missing knowledge, dependency timeouts, and secret disclosure. A fixed seed makes bootstrap decisions and the report fingerprint reproducible; elapsed time is recorded separately and excluded from the fingerprint.

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

The tests cover attribution precedence, paired deltas, Pass^k, bootstrap reproducibility, hard-failure and critical-case vetoes, overfitting rejection, resource budgets, retry accounting, atomic report replacement, and end-to-end deterministic replay.
