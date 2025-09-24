# Token Tailoring Example

This example demonstrates interactive token tailoring using `openai.WithTokenTailoring` and the built-in strategies in `model/token_tailor.go`.

## What it shows

- Configure a model with token tailoring via option.
- Use simple counter (or swap to `tiktoken` submodule) to enforce a prompt budget.
- Interactively send messages; use `/bulk N` to append many user messages and observe trimming.

## Prerequisites

- Go 1.21 or later
- Optional: An OpenAI-compatible API key for real calls (否则仍可看到裁剪统计)。

## Run

```bash
cd examples/tailor

# Basic run with flags (defaults shown):
go run . \
  -model deepseek-chat \
  -max-prompt-tokens 512 \
  -strategy middle \
  -preserve-system=true \
  -preserve-last-turn=true

# 可选：通过环境变量提供 OPENAI_API_KEY/OPENAI_BASE_URL 以进行真实调用。
```

Then type:

```
/bulk 30
Hello
```

You should see a line like:

```
[tailor] maxPromptTokens=512 before=35 after=3
```

## Notes

- Swap the counter to the `tiktoken` submodule for higher accuracy without changing root Go version.
- Strategies available:
  - MiddleOutStrategy
  - HeadOutStrategy
  - TailOutStrategy

### Switch to tiktoken counter (optional)

In your code, replace:

```go
counter := model.NewSimpleTokenCounter(maxPromptTokens)
```

with a `tiktoken` submodule counter (see `model/tiktoken`), and keep the same `WithTokenTailoring` usage.

## Commands

- `/bulk N`：一次性追加 N 条用户消息（未提供 N 时默认 10）。
- `/history`：查看当前缓存的 message 条数。
- `/exit`：退出。
