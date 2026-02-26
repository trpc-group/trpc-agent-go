# Prompt Cache Example

This example demonstrates how tRPC-Agent-Go optimizes costs using prompt caching.

## What is Prompt Caching?

Prompt caching stores frequently used content (system prompts, tools, context) and reuses it across requests, providing significant cost savings:

- **Anthropic Claude**: 90% discount on cached tokens ($3/MTok → $0.30/MTok)
- **OpenAI GPT**: Automatic caching with 50% discount
- **Google Gemini**: Native cached content support

## Features Demonstrated

1. **Anthropic Explicit Caching**: Cache control with cache_control breakpoints
2. **OpenAI Automatic Caching**: Message ordering optimization
3. **Cost Comparison**: With vs without caching
4. **Cache Monitoring**: Track cache effectiveness via Usage metrics

## Prerequisites

```bash
# Set API keys
export ANTHROPIC_API_KEY="your-anthropic-key"
export OPENAI_API_KEY="your-openai-key"

# Install dependencies
go mod tidy
```

## Running the Example

```bash
cd examples/promptcache
go run main.go
```

## Expected Output

```
=== Prompt Cache Demo with Agent, Multi-turn & Tools ===

This demo shows that prompt caching works with:
  ✓ Agent-based architecture (llmagent + runner)
  ✓ Multi-turn conversations (session)
  ✓ Tool calls (calculator, time)

OpenAI prompt caching requirements:
  - Minimum 1024 tokens in prompt
  - Same prompt prefix across requests
  - Cache TTL: 5-10 minutes
  - Cache is best-effort (not guaranteed)

============================================================
Starting multi-turn conversation with tools...
============================================================

📝 Turn 1: Regular question (cache creation)
   Query: What is the singleton design pattern? Give a brief explanation.
   💬 Response: The Singleton design pattern is a creational pattern that ensures a class has only one instance and provides a global point of access to that instance. It's commonly used in scenarios where exactly on...
   ⏱️  Time: 4.159581607s
   📊 Prompt tokens: 1368, Cached: 1280
   💰 Cache hit rate: 93.57%

📝 Turn 2: Tool call - calculator
   Query: Calculate 123 * 456 + 789
   [Expecting tool call]
   🔧 Tool call: calculator
   🔧 Tool call: calculator
   🔧 Tool call: calculator
   🔧 Tool call: calculator
   💬 Response: The result of the calculation \(123 \times 456 + 789\) is \(56,877\).
   ✓ Tool was called
   ⏱️  Time: 5.69627515s
   📊 Prompt tokens: 1920, Cached: 1792
   💰 Cache hit rate: 93.33%

📝 Turn 3: Tool call - time
   Query: What time is it now in UTC?
   [Expecting tool call]
   🔧 Tool call: get_current_time
   💬 Response: The current time in UTC is 2026-01-11 12:45:50.
   ✓ Tool was called
   ⏱️  Time: 2.116153531s
   📊 Prompt tokens: 2008, Cached: 1280
   💰 Cache hit rate: 63.75%

📝 Turn 4: Regular question (expecting cache hit)
   Query: What is the factory method pattern? Brief answer.
   💬 Response: The Factory Method pattern is a creational design pattern that provides an interface for creating objects in a superclass but allows subclasses to alter the type of objects that will be created. Inste...
   ⏱️  Time: 4.304449533s
   📊 Prompt tokens: 2046, Cached: 1920
   💰 Cache hit rate: 93.84%

📝 Turn 5: Tool call - calculator (expecting cache hit)
   Query: Calculate the square root of 144
   [Expecting tool call]
   🔧 Tool call: calculator
   💬 Response: The square root of 144 is 12.
   ✓ Tool was called
   ⏱️  Time: 2.807503239s
   📊 Prompt tokens: 2481, Cached: 1280
   💰 Cache hit rate: 51.59%

📝 Turn 6: Regular question (expecting cache hit)
   Query: Explain the observer pattern briefly.
   💬 Response: The Observer pattern is a behavioral design pattern that allows an object, known as the subject, to maintain a list of its dependents, called observers, and notify them of any state changes, usually b...
   ⏱️  Time: 5.337916856s
   📊 Prompt tokens: 2505, Cached: 2304
   💰 Cache hit rate: 91.98%

============================================================
📊 Prompt Cache Statistics
============================================================
Total Requests:        6
Requests with Cache:   6
Total Prompt Tokens:   12328
Total Cached Tokens:   9856
Overall Cache Rate:    79.95%
Estimated Cost Savings: 39.97%
============================================================

✅ Demo completed!

Key observations:
• System prompt (>1024 tokens) enables prompt caching
• Cache works across multi-turn conversations with session history
• Tool calls don't prevent caching - system prompt prefix is still cached
• Cache hits reduce cost by up to 50% (cached tokens are half price)

Note: OpenAI caching is best-effort and may not hit on every request.
      Cache hits are more likely in production with sustained traffic.
      Run this demo multiple times to see improved cache hit rates.
```

## Code Highlights

### Anthropic: Enable/Disable Cache

```go
// Cache enabled (default)
model := anthropic.New("claude-3-5-sonnet-20241022",
    anthropic.WithAPIKey(apiKey),
    anthropic.WithEnablePromptCache(true),
)

// Cache disabled for testing
model := anthropic.New("claude-3-5-sonnet-20241022",
    anthropic.WithAPIKey(apiKey),
    anthropic.WithEnablePromptCache(false),
)
```

### OpenAI: Message Optimization

```go
// Enabled by default for OpenAI
model := openai.New("gpt-4o",
    openai.WithAPIKey(apiKey),
)

// Disable when strict message ordering is required
model := openai.New("gpt-4o",
    openai.WithAPIKey(apiKey),
    openai.WithOptimizeForCache(false),
)
```

### Monitor Cache Effectiveness

```go
response := <-model.GenerateContent(ctx, request)
if response.Usage != nil {
    cached := response.Usage.PromptTokensDetails.CachedTokens
    total := response.Usage.PromptTokens
    hitRate := float64(cached) / float64(total) * 100
    
    fmt.Printf("Cache hit rate: %.2f%%\n", hitRate)
}
```

## Configuration Options

### Anthropic Advanced Configuration

```go
model := anthropic.New("claude-3-5-sonnet-20241022",
    // Enable/disable caching
    anthropic.WithEnablePromptCache(true),
    
    // Cache specific content types
    anthropic.WithCacheSystemPrompt(true),
    anthropic.WithCacheTools(true),
    
    // Custom decision logic
    anthropic.WithCacheDecisionFunc(func(req *model.Request) bool {
        return os.Getenv("ENV") == "production"
    }),
)
```

## When to Disable Caching

- **Testing**: Need deterministic behavior
- **Single-shot requests**: Cache won't be reused
- **Variable prompts**: Content changes frequently
- **Debugging**: Isolate caching effects

## Performance Tips

1. **Use consistent system prompts** across requests
2. **Keep system prompts > 1024 tokens** for Anthropic
3. **Structure messages** with system prompts first
4. **Reuse sessions** to maximize cache hits
5. **Monitor cache metrics** to track effectiveness

## Cost Savings Example

For a typical RAG application with Claude Sonnet:

- System prompt: 2,000 tokens
- RAG context: 4,000 tokens
- Tool definitions: 1,000 tokens
- **Total cacheable**: 7,000 tokens

With 50% cache hit rate:
- Regular cost: $0.021/request
- With cache: $0.00945/request
- **Savings**: 55% or ~$12,000/year at 1M requests

## Learn More

- [tRPC-Agent-Go Documentation](../../README.md#prompt-caching)
- [Anthropic Prompt Caching](https://docs.anthropic.com/en/docs/prompt-caching)
- [OpenAI Prompt Caching](https://platform.openai.com/docs/guides/prompt-caching)
