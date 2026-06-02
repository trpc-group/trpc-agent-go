# Query Enhancer

> **Example Code**: [examples/knowledge/query-enhancer](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/query-enhancer)

Query Enhancer rewrites and optimizes user queries before retrieval, improving search quality in multi-turn conversations. It is injected via `knowledge.WithQueryEnhancer`.

## Why Query Enhancer

In multi-turn conversations, follow-up questions often contain pronouns or omissions that perform poorly in vector search:

| Turn | User Query | What the retriever sees |
|------|-----------|------------------------|
| 1 | "What are Large Language Models?" | `What are Large Language Models?` — fine |
| 2 | "How does **it** handle context length?" | `How does it handle context length?` — "it" is meaningless |
| 3 | "Compare **the above** with traditional search" | `Compare the above with traditional search` — "the above" is lost |

Query Enhancer uses an LLM and conversation history to rewrite these ambiguous queries into standalone, retrieval-optimized queries.

## Inject into Knowledge

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge"

kb := knowledge.New(
    knowledge.WithQueryEnhancer(enhancer),
    // ... other configurations (VectorStore, Embedder, Sources, etc.)
)
```

## Supported Enhancers

### LLMEnhancer (LLM rewriting)

Uses a language model to rewrite queries based on conversation history, resolving references and removing conversational noise.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/query"
    openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

llm := openaimodel.New("deepseek-v4-flash")
enhancer := query.NewLLMEnhancer(llm)
```

| Option | Description | Required |
|--------|-------------|----------|
| `model.Model` (constructor parameter) | LLM instance for query rewriting | Yes |
| `WithSystemPrompt(string)` | Custom rewriting prompt | No |

#### Custom System Prompt

The default prompt works for general scenarios. Override it for domain-specific use cases:

```go
enhancer := query.NewLLMEnhancer(llm, query.WithSystemPrompt(`
Rewrite the query for a code search engine.
Focus on function names, types, and package names.
Output ONLY the rewritten query.
`))
```

### PassthroughEnhancer (no-op)

Returns the original query unchanged. This is the default behavior when no enhancer is configured.

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/query"

enhancer := query.NewPassthroughEnhancer()
```

## How it Works

Position of the Query Enhancer in the RAG pipeline:

```
User Query + Session History
        │
        ▼
┌──────────────────┐
│  Query Enhancer   │  Rewrite query (resolve refs, denoise, optimize)
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│    Embedder       │  Generate vector for the enhanced query
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│  Vector Store     │  Search with the enhanced query
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│  Reranker (opt.)  │  Rerank results using the original query
└────────┬─────────┘
         │
         ▼
      Results
```

### Automatic Session History Extraction

When Knowledge is used as an Agent tool, the framework automatically extracts recent conversation history (up to 10 messages) from the Session and passes it to the Query Enhancer. No manual history management is needed.

The following messages are filtered out:

- Partial (streaming intermediate) messages
- Tool Call / Tool Result messages
- Empty content messages
- Non user/assistant role messages

### Filter-only Optimization

When the query text is empty and search mode is filter-only (`SearchModeFilter`), the framework automatically skips query enhancement to avoid unnecessary LLM calls.

## Custom Enhancer

Implement the `query.Enhancer` interface to create a custom enhancer:

```go
type Enhancer interface {
    EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error)
}
```

For example, a debug decorator that prints the before/after query:

```go
type debugEnhancer struct {
    inner query.Enhancer
}

func (d *debugEnhancer) EnhanceQuery(ctx context.Context, req *query.Request) (*query.Enhanced, error) {
    result, err := d.inner.EnhanceQuery(ctx, req)
    if err != nil {
        return nil, err
    }
    if result.Enhanced != req.Query {
        fmt.Printf("Query enhanced: %q -> %q\n", req.Query, result.Enhanced)
    }
    return result, nil
}

// Usage:
enhancer := &debugEnhancer{inner: query.NewLLMEnhancer(llm)}
```

## When Do You Need a Query Enhancer

In the default **Agentic RAG** scenario (Knowledge used as an Agent tool), the Agent's LLM already constructs tool call arguments based on conversation context, implicitly performing a degree of query rewriting. In this case, a Query Enhancer is **usually not necessary**.

Query Enhancer is most useful in the following scenarios:

| Scenario | Description |
|----------|-------------|
| **Standalone retrieval (non-agentic)** | Calling `kb.Search()` directly without an Agent LLM to rewrite queries |
| **Embedding model struggles with conversational queries** | The embedding model is optimized for keywords/short text and needs natural language converted to concise queries |
| **Specialized strategies like HyDE** | Need the LLM to generate a hypothetical answer first, then use its embedding for retrieval |
| **Weak Agent LLM query construction** | Some smaller LLMs may lose context when constructing tool call arguments |

In short: **if retrieval quality already meets your needs in an Agentic RAG setup, you do not need to configure a Query Enhancer.**

## Notes

- LLMEnhancer calls the LLM once per query, adding latency and cost.
- Query Enhancer is opt-in. When not configured, the behavior is equivalent to Passthrough.
- The Reranker receives the **original query**, not the enhanced one, to preserve the user's original intent for relevance judgment.
