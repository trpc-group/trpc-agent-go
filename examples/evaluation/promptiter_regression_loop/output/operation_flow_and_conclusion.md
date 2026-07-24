# PromptIter Regression Loop Operation Flow and Conclusions

## Runtime Defaults

- GOPATH/GOCACHE: use Go defaults, or set local values outside the repository when needed.
- Default LLM base URL: `https://api.deepseek.com`
- Default LLM model: `deepseek-chat`
- API key source: environment only (`LLM_API_KEY`, `DEEPSEEK_API_KEY`, `DEEPSEEK_API_KEY1`, or `OPENAI_API_KEY`)

The API key is intentionally not written into repository files.

## Mock / Deterministic Flow

Command:

```powershell
go run ./promptiter_regression_loop `
  -config ./promptiter_regression_loop/data/promptiter.json
```

Result files:

- `optimization_report.json`
- `optimization_report.md`
- `deterministic_optimization_report.json`
- `deterministic_optimization_report.md`

Conclusion data:

| Field | Value |
|---|---:|
| Decision | REJECT |
| Baseline train score | 0.4444 |
| Candidate train score | 0.8889 |
| Baseline validation score | 0.7778 |
| Candidate validation score | 0.8889 |
| Validation score delta | 0.1111 |
| Newly failed validation cases | 1 |
| Critical regressions | 1 |
| Total calls | 12 |
| Estimated cost | 0.000114 |

Gate reasons:

- New hard fails `1` exceed limit `0`.
- `1` critical validation case regressed.

Interpretation:

The deterministic path is the default path and requires no API key. It reproduces the intended regression loop: the candidate improves training score and validation score, fixes `val_json_refund`, but regresses `val_critical_direct_status` by wrapping a direct answer in JSON. The outer gate correctly rejects the candidate because score gain alone is not enough when a critical case regresses.

## Real LLM Flow

Command:

```powershell
$env:DEEPSEEK_API_KEY="<set locally>"
$env:LLM_BASE_URL="https://api.deepseek.com"
$env:LLM_MODEL="deepseek-chat"
go run ./promptiter_regression_loop `
  -config ./promptiter_regression_loop/data/promptiter.json `
  -mode real_llm
```

Result files:

- `optimization_report.json`
- `optimization_report.md`
- `real_llm_optimization_report.json`
- `real_llm_optimization_report.md`

Conclusion data from the latest successful real LLM run:

| Field | Value |
|---|---:|
| Decision | REJECT |
| Baseline train score | 0.7778 |
| Candidate train score | 0.6667 |
| Baseline validation score | 0.7778 |
| Candidate validation score | 0.7778 |
| Validation score delta | 0.0000 |
| Newly failed validation cases | 0 |
| Critical regressions | 0 |
| Total calls | 12 |
| Estimated cost | 0.000125 |
| Duration | 57758 ms |

Gate reason:

- Validation score gain `0.0000` is below threshold `0.0500`.

Validation case deltas:

| Case | Critical | Baseline | Candidate | Delta | Transition |
|---|---:|---:|---:|---:|---|
| `val_json_refund` | false | 0.3333 | 0.3333 | 0.0000 | stayed_fail |
| `val_weather_berlin` | false | 1.0000 | 1.0000 | 0.0000 | stayed_pass |
| `val_critical_direct_status` | true | 1.0000 | 1.0000 | 0.0000 | stayed_pass |

Interpretation:

The real DeepSeek path ran successfully with the Evaluation Service, metric registry, judge runner, and PromptIter engine. PromptIter produced a candidate instruction that tried to tighten JSON schema compliance, but the candidate still returned `amount` instead of `amount_usd` for `val_json_refund`, so validation score did not improve. The candidate also reduced train score by causing `train_refund_policy` to fail the rubric, which is additional evidence that the prompt edit is not production-ready.

Because real LLM outputs are stochastic, earlier successful runs may differ in exact wording and scores. The acceptance gate intentionally uses the latest validation delta, hard-fail count, critical-case regression count, cost, and call budget from the persisted report instead of trusting the optimizer proposal by itself.

## Engineering Conclusion

- The mock path is runnable and demonstrates the target regression-gate behavior.
- The sample config defaults to deterministic mode, so the core flow is runnable without a real API key.
- The code now keeps DeepSeek model/base URL defaults explicit while keeping secrets out of source control.
- The real path now evaluates PromptIter rounds through the same outer regression gate selection logic instead of blindly selecting round 1.
- The latest real DeepSeek run completed end to end, but the candidate was rejected because validation score gain was `0.0000`, below the configured `0.0500` threshold.
