# Anthropic Auto-Optimal Prompt Cache Example

This example demonstrates the **new auto-optimal caching feature** for Anthropic Claude, which makes caching as simple as OpenAI - just one switch!

## What's New?

Previously, Anthropic caching required manual configuration of multiple options:

```go
// Old way - manual configuration
anthropic.WithEnablePromptCache(true)
anthropic.WithCacheSystemPrompt(true)
anthropic.WithCacheTools(true)
```

Now, it's as simple as:

```go
// New way - automatic optimal caching
anthropic.WithEnablePromptCache(true)  // That's all you need!
```

The framework automatically applies the optimal caching strategy based on your request.

## Auto-Cache Strategy

The framework automatically selects the best cache breakpoint position:

| Priority | Condition | Cache Position | What's Cached |
|----------|-----------|----------------|---------------|
| 1 | Multi-turn conversation | Last assistant message | System + Tools + All history |
| 2 | Has tools | Last tool | System + Tools |
| 3 | System only | System prompt | System prompt |

This ensures maximum cache efficiency with minimal configuration.

## Anthropic vs OpenAI Caching

| Feature | Anthropic | OpenAI |
|---------|-----------|--------|
| Discount | **90%** on cached tokens | 50% on cached tokens |
| Mechanism | Explicit `cache_control` markers | Automatic prefix matching |
| Min tokens | 1024 | 1024 |
| TTL | ~5 minutes | 5-10 minutes |
| Configuration | Now automatic! | Always automatic |

## Prerequisites

```bash
# Set API key (either works)
export ANTHROPIC_API_KEY="your-anthropic-key"
# or
export ANTHROPIC_AUTH_TOKEN="your-anthropic-token"

# Install dependencies
go mod tidy
```

## Running the Example

```bash
cd examples/promptcache_anthropic
go run main.go
```

## Expected Output

```
=== Anthropic Auto-Optimal Cache Demo ===

This demo shows the NEW auto-cache feature for Anthropic:
  ✓ Just enable caching with WithEnablePromptCache(true)
  ✓ Framework automatically applies optimal cache strategy
  ✓ Works like OpenAI - no need to configure individual options

Auto-cache strategy (in priority order):
  1. Multi-turn: Cache at last assistant message (covers everything)
  2. With tools: Cache at last tool (covers system + tools)
  3. System only: Cache at system prompt

Anthropic caching benefits:
  - Cached tokens are 90% cheaper (vs 50% for OpenAI)
  - Minimum 1024 tokens required
  - Cache TTL: ~5 minutes

============================================================
Starting multi-turn conversation with auto-caching...
============================================================

📝 Turn 1: Regular question (cache creation)
   Query: What is the singleton design pattern? Give a brief explanation.

   💬 Response: The **Singleton design pattern** is a creational pattern that ensures a class has only one instance and provides a global point of access to that instance.

## Key Characteristics:

1. **Single Instan...
   ⏱️  Time: 15.783248924s
   📊 Total input: 1694 (new: 30, cached: 1664)
   💰 Cache hit rate: 98.23% (cached tokens are 90% cheaper!)

   ℹ️  First turn: Cache breakpoint set at tools (covers system+tools)

📝 Turn 2: Tool call - calculator
   Query: Calculate 123 * 456 + 789
   [Expecting tool call]
   🔧 Tool call: calculator
   🔧 Tool call: calculator
   🔧 Tool call: calculator
   💬 Response: **Result:** 123 × 456 + 789 = **56,877**

**Calculation breakdown:**
1. 123 × 456 = 56,088
2. 56,088 + 789 = 56,877
   ✓ Tool was called
   ⏱️  Time: 14.93953985s
   📊 Total input: 2475 (new: 43, cached: 2432)
   💰 Cache hit rate: 98.26% (cached tokens are 90% cheaper!)

   ℹ️  Turn 2: Cache breakpoint moved to last assistant message
       (covers system+tools+all previous messages)

📝 Turn 3: Tool call - time
   Query: What time is it now in UTC?
   [Expecting tool call]
   🔧 Tool call: get_current_time
   💬 Response: The current time in UTC is: **January 12, 2026 at 07:29:21 UTC**

This is Coordinated Universal Time, which is the primary time standard by which the world regulates clocks and time. It's essentially ...
   ✓ Tool was called
   ⏱️  Time: 6.418286554s
   📊 Total input: 2644 (new: 84, cached: 2560)
   💰 Cache hit rate: 96.82% (cached tokens are 90% cheaper!)

   ℹ️  Turn 3: Cache breakpoint moved to last assistant message
       (covers system+tools+all previous messages)

📝 Turn 4: Regular question (expecting cache hit on system+tools+history)
   Query: What is the factory method pattern? Brief answer.
   💬 Response: The **Factory Method pattern** is a creational design pattern that provides an interface for creating objects in a superclass, but allows subclasses to alter the type of objects that will be created.
...
   ⏱️  Time: 12.427465921s
   📊 Total input: 2724 (new: 36, cached: 2688)
   💰 Cache hit rate: 98.68% (cached tokens are 90% cheaper!)

   ℹ️  Turn 4: Cache breakpoint moved to last assistant message
       (covers system+tools+all previous messages)

📝 Turn 5: Tool call (expecting cache hit)
   Query: Calculate the square root of 144
   [Expecting tool call]
   🔧 Tool call: calculator
   💬 Response: The square root of 144 is **12**.

This is a perfect square since 12 × 12 = 144.
   ✓ Tool was called
   ⏱️  Time: 5.810021314s
   📊 Total input: 3158 (new: 86, cached: 3072)
   💰 Cache hit rate: 97.28% (cached tokens are 90% cheaper!)

   ℹ️  Turn 5: Cache breakpoint moved to last assistant message
       (covers system+tools+all previous messages)

📝 Turn 6: Regular question (expecting cache hit)
   Query: Explain the observer pattern briefly.
   💬 Response: The **Observer pattern** is a behavioral design pattern that defines a one-to-many dependency between objects so that when one object (the subject) changes state, all its dependents (observers) are no...
   ⏱️  Time: 15.272292369s
   📊 Total input: 3193 (new: 57, cached: 3136)
   💰 Cache hit rate: 98.21% (cached tokens are 90% cheaper!)

   ℹ️  Turn 6: Cache breakpoint moved to last assistant message
       (covers system+tools+all previous messages)

============================================================
📊 Anthropic Prompt Cache Statistics
============================================================
Total Requests:           6
Requests with Cache Read: 6
Total Input Tokens:       15888
  - New (processed):      336
  - Cached (read):        15552
Cache Hit Rate:           97.89%
Estimated Cost Savings:   88.10%
============================================================

✅ Demo completed!

Key observations:
• Auto-cache requires just ONE switch: WithEnablePromptCache(true)
• Framework automatically picks optimal cache strategy
• Multi-turn conversations get maximum cache benefit
• Cache hits reduce cost by up to 90% (Anthropic's pricing)

Compare with manual configuration (old way):
  anthropic.WithEnablePromptCache(true)
  anthropic.WithCacheSystemPrompt(true)
  anthropic.WithCacheTools(true)
  anthropic.WithCacheMessages(true)  // NEW!

Now it's as simple as OpenAI - just enable and forget!
```

## Code Highlights

### Simple Configuration (Recommended)

```go
// Just one switch - framework handles everything automatically
llm := anthropic.New("claude-sonnet-4-20250514",
    anthropic.WithAPIKey(apiKey),
    anthropic.WithEnablePromptCache(true),  // That's all you need!
)
```

### Advanced Configuration (Optional)

```go
// Fine-grained control if needed
llm := anthropic.New("claude-sonnet-4-20250514",
    anthropic.WithAPIKey(apiKey),
    anthropic.WithEnablePromptCache(true),
    
    // Control what gets cached
    anthropic.WithCacheSystemPrompt(true),   // Cache system prompt
    anthropic.WithCacheTools(true),          // Cache tool definitions
    anthropic.WithCacheMessages(true),       // Cache multi-turn history (NEW!)
    
    // Minimum tokens to trigger caching
    anthropic.WithMinCacheableTokens(1024),
)
```

### Understanding Token Statistics

Anthropic reports tokens differently from OpenAI:

```go
// Anthropic token breakdown
usage := response.Usage
inputTokens := usage.PromptTokens                        // New tokens processed
cachedTokens := usage.PromptTokensDetails.CachedTokens   // Tokens read from cache

// Total input = new + cached
totalInput := inputTokens + cachedTokens

// Cache hit rate
cacheRate := float64(cachedTokens) / float64(totalInput) * 100
```

## How Auto-Caching Works

### Turn 1 (First Request)
```
[System Prompt] [Tools] ← cache_control  [User Message]
                   ↑
         Cache created here (covers system + tools)
```

### Turn 2+ (Multi-turn)
```
[System] [Tools] [User1] [Asst1] ← cache_control  [User2]
                            ↑
         Cache breakpoint moved to last assistant message
         (covers everything before current user message)
```

### Benefits of Dynamic Breakpoint

| Turn | Cached Content | Cache Rate |
|------|----------------|------------|
| 1 | System + Tools | ~0% (creating) |
| 2 | System + Tools + Turn 1 | ~97% |
| 3 | System + Tools + Turn 1-2 | ~97% |
| N | System + Tools + Turn 1-(N-1) | ~97%+ |

## Cost Savings Example

For a typical multi-turn conversation with Claude Sonnet:

- System prompt: 2,000 tokens
- Tool definitions: 500 tokens
- Average turn: 300 tokens
- 10-turn conversation

**Without caching:**
- Total tokens processed: 2,500 × 10 + 300 × 45 = 38,500 tokens
- Cost: $0.1155 (at $3/MTok)

**With auto-caching (90% cache rate):**
- New tokens: 2,500 + 300 × 10 = 5,500 tokens
- Cached tokens: 33,000 tokens
- Cost: $0.0165 + $0.0099 = $0.0264

**Savings: 77%** or ~$89/year per 1000 daily conversations

## Performance Tips

1. **Use consistent system prompts** - Changes invalidate cache
2. **Keep system prompts > 1024 tokens** - Minimum for caching
3. **Reuse sessions** - Multi-turn gets best cache rates
4. **Monitor cache metrics** - Track effectiveness via Usage

## Comparison with Manual Configuration

| Aspect | Manual | Auto-Optimal |
|--------|--------|--------------|
| Configuration | 3-4 options | 1 option |
| Multi-turn | Manual handling | Automatic |
| Breakpoint placement | Fixed | Dynamic |
| Optimal strategy | Developer decides | Framework decides |

## Learn More

- [tRPC-Agent-Go Documentation](../../README.md#prompt-caching)
- [Anthropic Prompt Caching](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
- [OpenAI Prompt Caching](https://platform.openai.com/docs/guides/prompt-caching)
