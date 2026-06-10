# LLM Verifier Best-of-N Example

This example runs the same online agent turn multiple times, evaluates each candidate with an LLM-backed evaluation metric, and returns only the selected winner.

It uses:

- `runner/bestofn.NewRunnerOption`
- `llm_verifier_pairwise` as the verifier metric
- A real OpenAI-compatible model for both candidate generation and judging
- Judge score tags and logprobs with `top_logprobs=20` when the provider supports them

## Run

```bash
export OPENAI_BASE_URL="<your-openai-compatible-base-url>"
export OPENAI_API_KEY="<your-api-key>"

cd examples/evaluation
go run ./llmverifier \
  -model deepseek-v4-flash \
  -judge-model deepseek-v4-flash \
  -base-url "$OPENAI_BASE_URL" \
  -api-key "$OPENAI_API_KEY" \
  -attempts 3
```

Optional custom prompt:

```bash
go run ./llmverifier \
  -base-url "$OPENAI_BASE_URL" \
  -api-key "$OPENAI_API_KEY" \
  -prompt "Explain LLM-as-a-Verifier for an online support agent in two bullets."
```

Streaming runs buffer candidate events internally and replay only the selected winner after verification.
