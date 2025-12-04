# Provider Agent Example

This example shows how to wire the provider abstraction into `llmagent` so you can switch between OpenAI- and Anthropic-compatible backends while reusing the same tools, runner, and streaming UI.

## Overview

- Uses `model/provider.Model` to resolve the requested provider and model at runtime.
- Wraps the model in a single `llmagent` instance that includes the built-in calculator tool.
- Streams events from `runner.Run`, highlighting tool calls (`🔧`) and tool results (`✅`).
- Keeps chat history in the in-memory session service so each turn builds on prior results.

## Features

- **Provider swap**: `-provider openai` (default) or `-provider anthropic`, `-provider ollama` with matching model names.
- **Model selection**: `-model deepseek-chat` by default; pass any provider-supported model ID.
- **Streaming toggle**: `-stream=true|false` directly maps to `GenerationConfig.Stream`.
- **Provider options**: Override API key, base URL, channel buffer size, and token tailoring knobs via CLI flags which feed directly into `provider.Options`.
- **Calculator tool**: Function tool capable of add, subtract, multiply, divide (with zero checks), and power operations.

## Available Tool

- **calculator**: Performs arithmetic with JSON arguments `{ "operation": "...", "a": <number>, "b": <number> }`.

## CLI Flags

- `-provider`: Provider backend (`openai`, `anthropic`, `ollama`).
- `-model`: Model name understood by the selected provider.
- `-stream`: Whether to request streaming responses from the provider.
- `-api-key`: Inline API key override (otherwise falls back to provider defaults/env vars).
- `-base-url`: Custom endpoint when pointing to non-default provider hosts.
- `-channel-buffer`: Overrides provider channel buffer size for streaming responses.
- `-token-tailor`: Enables token tailoring for providers that support it.
- `-max-input-tokens`: Sets the maximum input tokens used when tailoring is enabled.

## Building and Running

```bash
cd examples/provider
go build -o provider-demo .

# Run with defaults (OpenAI-compatible DeepSeek)
./provider-demo

# Run with a specific provider/model and disable streaming
./provider-demo -provider anthropic -model claude-3-5-sonnet -stream=false

# Override provider options (custom endpoint, API key, tailoring, buffer size)
./provider-demo \
  -provider openai \
  -model gpt-4o-mini \
  -base-url https://api.deepseek.com/v1 \
  -api-key "$DEEPSEEK_API_KEY" \
  -channel-buffer 512 \
  -token-tailor \
  -max-input-tokens 64000
```

## Environment Setup

Set the API keys required by the provider you plan to use before running the demo:

```bash
# OpenAI-compatible
export OPENAI_API_KEY="your-openai-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"  # Optional for non-OpenAI hosts

# Anthropic
export ANTHROPIC_AUTH_TOKEN="your-anthropic-key"
export ANTHROPIC_BASE_URL="https://api.deepseek.com/anthropic"  # Optional for non-Anthropic hosts

# Ollama
export OLLAMA_HOST="http://localhost:11434"  # Optional, default is http://localhost:11434
```

> Flags always take precedence over environment variables. Leave the flags unset to keep the default provider behavior.

## Example Session

```
🧠 Provider Agent Demo
Provider: anthropic
Model: deepseek-chat
Stream: true
Type 'exit' to end the conversation
Available tools: calculator
The agent will perform mathematical calculations
============================================================
✅ Provider agent ready! Session: provider-session-1762515191

💡 Try asking complex questions that require planning, like:
   • 'If I invest $1000 at a 5% annual rate compounded yearly for 10 years, how much will I have?'

👤 You: If I invest $1000 at a 5% annual rate compounded yearly for 10 years, how much will I have?
🧠 Agent: I can help you calculate the future value of your investment! However, I need to clarify that the calculator tool I have access to performs basic arithmetic operations (add, subtract, multiply, divide, power) but doesn't directly handle compound interest calculations.

To calculate compound interest, we need to use the formula: A = P(1 + r)^n, where:
- A = final amount
- P = principal ($1000)
- r = interest rate (5% = 0.05)
- n = number of years (10)

Let me break this down step by step using the available calculator:
🔧 Executing tools:
   • calculator ({"a":1,"b":0.05,"operation":"add"})
{"operation":"add","a":1,"b":0.05,"result":1.05}
   ✅ Tool completed
Now I'll calculate (1.05)^10:
🔧 Executing tools:
   • calculator ({"a":1.05,"b":10,"operation":"power"})
{"operation":"power","a":1.05,"b":10,"result":1.6288946267774416}
   ✅ Tool completed
Finally, I'll multiply this by the principal amount:
🔧 Executing tools:
   • calculator ({"a":1000,"b":1.6288946267774416,"operation":"multiply"})
{"operation":"multiply","a":1000,"b":1.6288946267774416,"result":1628.8946267774415}
   ✅ Tool completed
Based on the calculations, if you invest $1000 at a 5% annual rate compounded yearly for 10 years, you will have approximately **$1,628.89**.

This represents a total return of $628.89 on your initial $1000 investment over 10 years.Based on the calculations, if you invest $1000 at a 5% annual rate compounded yearly for 10 years, you will have approximately **$1,628.89**.

This represents a total return of $628.89 on your initial $1000 investment over 10 years.
```
