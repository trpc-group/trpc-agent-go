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
=== Anthropic Prompt Caching Demo ===

📝 First request (cache creation)...
✅ First request completed in 2.5s
   Prompt tokens: 3245
   Cache creation tokens: 3000
   Cached tokens: 0

📝 Second request (cache hit)...
✅ Second request completed in 1.8s
   Prompt tokens: 3250
   Cached tokens: 3000
   Cache read tokens: 3000
   💰 Cache hit rate: 92.31%
   💵 Cost without cache: $0.009750
   💵 Cost with cache: $0.001650
   💵 Savings: $0.008100 (83.08%)
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
// Optimize for cache (default)
model := openai.New("gpt-4o",
    openai.WithAPIKey(apiKey),
    openai.WithOptimizeForCache(true),  // System messages moved to front
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
    
    // Minimum tokens to trigger caching
    anthropic.WithMinCacheableTokens(2048),
    
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
