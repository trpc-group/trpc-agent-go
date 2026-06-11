# Summary Injection Prompt Cache Example

This example compares prompt-cache behavior for session summary injection modes:

- `SessionSummaryInjectionSystem` (default): summary is merged into the first system message.
- `SessionSummaryInjectionUser`: summary is injected near user/history messages, keeping stable system prefix content together.

The demo seeds two sessions with different summaries. Each request injects two stable system blocks:

1. Block A is intentionally below the typical prompt-cache threshold.
2. Block B completes the stable prefix.

In system mode, the dynamic summary is merged into block A, so it appears before block B. In user mode, blocks A and B remain contiguous, and the dynamic summary moves later into user/history context.

## Prerequisites

- Go 1.21 or later.
- An OpenAI-compatible model endpoint.

Environment variables:

- `OPENAI_API_KEY`: API key for the model service.
- `OPENAI_BASE_URL` (optional): base URL for an OpenAI-compatible endpoint.
- `MODEL_NAME` (optional): model name. Defaults to `gpt-4o`.
- `PROMPT_CACHE_KEY_PREFIX` (optional): sets `prompt_cache_key` through OpenAI extra fields when your provider supports it.

## Run

```bash
cd examples/promptcache/summaryinjection
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint"
export MODEL_NAME="your-model"
go run main.go
```

Run only one case:

```bash
go run main.go -case user
go run main.go -case system
```

## Expected Output Shape

```text
=== Summary Injection Prompt Cache Demo ===
Model: gpt-4o

========================================================================
Case: system
Default mode. Dynamic summary is merged into the first system message.
========================================================================
Request shape system #1:
  [0] role=system    chars=... summary
  [1] role=system    chars=...
  [2] role=user      chars=...
  usage: prompt_tokens=... cached_tokens=...

========================================================================
Case: user
User mode. Dynamic summary is injected near user/history messages.
========================================================================
Request shape user #1:
  [0] role=system    chars=...
  [1] role=system    chars=...
  [2] role=user      chars=... summary
  [3] role=user      chars=...
  usage: prompt_tokens=... cached_tokens=...
```

Provider prompt caching is best-effort. If `cached_tokens` stays at zero, run the demo again shortly after the first run or use a provider/model that reports prompt cache usage.
