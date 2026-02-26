# Anthropic Prompt Cache Example

This example demonstrates Anthropic prompt caching with **three independent controls**, structured in three phases to verify each option's effect separately.

## Three Independent Cache Options

| Option | What it caches | When to use |
|--------|---------------|-------------|
| `WithCacheSystemPrompt(true)` | System prompt | System prompt is stable and > 1024 tokens |
| `WithCacheTools(true)` | Tool definitions | Tools don't change frequently |
| `WithCacheMessages(true)` | Conversation history | Multi-turn conversations with 3+ turns |

All options default to `false`. Enable them individually based on your use case.

```go
llm := anthropic.New("claude-4-5-sonnet-20250929",
    anthropic.WithAPIKey(apiKey),
    anthropic.WithCacheSystemPrompt(true),  // cache stable system prompt
    anthropic.WithCacheTools(true),          // cache tool definitions
    anthropic.WithCacheMessages(true),       // cache multi-turn history
)
```

## Anthropic vs OpenAI Caching

| Feature | Anthropic | OpenAI |
|---------|-----------|--------|
| Discount | **90%** on cached tokens | 50% on cached tokens |
| Mechanism | Explicit `cache_control` markers | Automatic prefix matching |
| Min tokens | 1024 | 1024 |
| TTL | ~5 minutes | 5-10 minutes |
| Configuration | Three independent options | Always automatic |
| Cache creation cost | 25% extra (one-time) | None |

## Prerequisites

```bash
export ANTHROPIC_API_KEY="your-anthropic-key"
cd examples/promptcache/anthropic
go mod tidy
```

## Running

```bash
go run main.go
```

## Three-Phase Verification

### Phase 1: System Prompt Only

Only `WithCacheSystemPrompt(true)` enabled. Verifies that the system prompt (~1200 tokens) is cached and reused across turns.

- Turn 1: Cache creation — system prompt written to cache
- Turn 2: Cache hit — system prompt served from cache, only user message is new

### Phase 2: System + Tools

`WithCacheSystemPrompt(true)` + `WithCacheTools(true)`. Verifies that tool definitions are also cached.

- Both system prompt and tools have independent cache breakpoints
- Cache covers more tokens than Phase 1 (system + tools)

### Phase 3: System + Tools + Messages (Dynamic Breakpoint)

All three options enabled. This is the key phase — it verifies that the cache breakpoint dynamically moves to the latest assistant message each turn.

#### How Dynamic Message Caching Works

```
Turn 1: [system] [tools] [user1]
         → No assistant msg yet, only system+tools cached

Turn 2: [system] [tools] [user1] [asst1 ← breakpoint] [user2]
         → Breakpoint at asst1, covers system+tools+turn1

Turn 3: [system] [tools] [user1] [asst1] [user2] [asst2 ← breakpoint] [user3]
         → Breakpoint moves to asst2, covers system+tools+turn1+turn2

Turn N: breakpoint keeps moving forward, more tokens served from cache
```

#### Expected Results

| Turn | cache_read | Behavior |
|------|-----------|----------|
| 1 | ~1623 | System+tools cached |
| 2 | ~1728 | +turn1 history cached |
| 3 | ~2234 | +turn2 history cached |
| 4 | ~2322 | +turn3 history cached |
| 5 | ~2447 | +turn4 history cached |
| 6 | ~2544 | Most tokens from cache |

`cache_read` increases each turn as the breakpoint moves forward, covering more conversation history.

## Sample Output

```
Phase      Turns    CacheRead    NewTokens    HitRate
-------------------------------------------------------
Phase1     2        3240         711          82.0%
Phase2     2        3450         1003         77.5%
Phase3     6        12898        466          96.5%
```

## Understanding Token Statistics

Anthropic reports tokens differently from OpenAI:

```go
usage := response.Usage
inputTokens := usage.PromptTokens                        // new tokens processed
cachedTokens := usage.PromptTokensDetails.CachedTokens   // tokens read from cache

// total input = new + cached
totalInput := inputTokens + cachedTokens

// cache hit rate
cacheRate := float64(cachedTokens) / float64(totalInput) * 100
```

## Performance Tips

1. **Keep system prompts stable** — changes invalidate the cache
2. **System prompt > 1024 tokens** — minimum required for caching
3. **Use `WithCacheMessages` for 3+ turn conversations** — the cache read savings (90%) outweigh the per-turn creation cost (25%)
4. **Don't cache frequently changing content** — dynamic system prompts or tools that change every request will waste the 25% creation cost without cache read benefits

## Learn More

- [Anthropic Prompt Caching](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
- [OpenAI Prompt Caching](https://platform.openai.com/docs/guides/prompt-caching)
