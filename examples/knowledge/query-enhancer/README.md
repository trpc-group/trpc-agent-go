# Query Enhancer Example

This example demonstrates how to use the **LLM-based query enhancer** to improve retrieval quality in multi-turn conversations.

## The Problem

In multi-turn chat, follow-up questions often contain pronouns or references that make no sense to a retriever on their own:

| Turn | User Query | What the retriever sees |
|------|-----------|------------------------|
| 1 | "What are Large Language Models?" | `What are Large Language Models?` — works fine |
| 2 | "How does **it** handle context length?" | `How does it handle context length?` — "it" is meaningless |
| 3 | "Compare **the above** with traditional search" | `Compare the above with traditional search` — "the above" is lost |

Without a query enhancer, the retriever searches for "it" or "the above" verbatim, producing poor results.

## The Solution

The `query.LLMEnhancer` rewrites each query using conversation history before it reaches the retriever:

| Turn | Original Query | Enhanced Query |
|------|---------------|----------------|
| 1 | "What are Large Language Models?" | "What are Large Language Models?" (unchanged) |
| 2 | "How does it handle context length?" | "How do Large Language Models handle context length?" |
| 3 | "Compare the above with traditional search" | "Compare Large Language Models with traditional search engines" |

## What it demonstrates

- **`query.NewLLMEnhancer`**: Create an LLM-based query enhancer with a single line
- **`knowledge.WithQueryEnhancer`**: Plug it into the knowledge base
- **Decorator pattern**: Wrap the enhancer with a `debugEnhancer` to observe the before/after query and conversation history — no framework hooks needed
- **Multi-turn session**: All turns share the same `sessionID` so context accumulates automatically

## Key Code

```go
// Create the LLM query enhancer
enhancer := query.NewLLMEnhancer(llm)

// Plug into knowledge base
kb := knowledge.New(
    knowledge.WithVectorStore(vs),
    knowledge.WithEmbedder(openai.New()),
    knowledge.WithSources(sources),
    knowledge.WithQueryEnhancer(enhancer),  // <-- this line
)
```

To observe what the enhancer does, wrap it with a decorator:

```go
// debugEnhancer wraps any query.Enhancer and prints the before/after query.
type debugEnhancer struct {
    inner query.Enhancer
}

func (d *debugEnhancer) EnhanceQuery(ctx context.Context, req *query.Request) (*query.Enhanced, error) {
    result, err := d.inner.EnhanceQuery(ctx, req)
    if err != nil {
        return nil, err
    }
    fmt.Printf("Query enhanced: %q -> %q\n", req.Query, result.Enhanced)
    return result, nil
}

// Use it:
enhancer := &debugEnhancer{inner: query.NewLLMEnhancer(llm)}
```

## Custom System Prompt

Override the default rewriting prompt with `query.WithSystemPrompt`:

```go
enhancer := query.NewLLMEnhancer(llm, query.WithSystemPrompt(`
Rewrite the query for a code search engine.
Focus on function names, types, and package names.
Output ONLY the rewritten query.
`))
```

## Prerequisites

```bash
export OPENAI_API_KEY=your-api-key
export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional
export MODEL_NAME=deepseek-v4-flash                # Optional
```

## Run

```bash
go run main.go
```

With a different vector store:

```bash
go run main.go -vectorstore pgvector
```

## Example Output

```text
🔄 Query Enhancer Demo — Multi-turn Knowledge Chat
===================================================

── Turn 1 ─────────────────────────────────
👤 User: What are Large Language Models?
   🔄 Query unchanged: "What are Large Language Models?"
   🤖 Assistant: Large Language Models (LLMs) are ...

── Turn 2 ─────────────────────────────────
👤 User: How does it handle context length?
   📜 History (2 messages):
      [user] What are Large Language Models?
      [assistant] Large Language Models (LLMs) are ...
   🔄 Query enhanced: "How does it handle context length?" -> "How do Large Language Models handle context length?"
   🤖 Assistant: LLMs handle context length through ...

── Turn 3 ─────────────────────────────────
👤 User: Compare the above with traditional search engines
   📜 History (4 messages):
      [user] What are Large Language Models?
      [assistant] Large Language Models (LLMs) are ...
      [user] How does it handle context length?
      [assistant] LLMs handle context length through ...
   🔄 Query enhanced: "Compare the above with traditional search engines" -> "Compare Large Language Models with traditional search engines"
   🤖 Assistant: ...
```

## How it Works

```text
User Query + Session History
        │
        ▼
┌──────────────────┐
│  Query Enhancer   │  Rewrites "how does it work?" into
│  (LLMEnhancer)    │  "how do Large Language Models work?"
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│    Embedder       │  Generates vector for the enhanced query
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│  Vector Store     │  Searches with the enhanced query
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│  Reranker (opt.)  │  Re-ranks results using original query
└────────┬─────────┘
         │
         ▼
      Results
```

## Next Steps

- **Custom enhancer**: Implement `query.Enhancer` interface for domain-specific rewriting (e.g., HyDE, sub-question decomposition)
- **Reranker**: Combine with a reranker for even better results — see `../reranker/`
- **Agentic filter**: Add metadata filters — see `../features/agentic-filter/`
