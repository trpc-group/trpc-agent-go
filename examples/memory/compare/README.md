# Memory Retrieval Comparison (SQLite vs SQLiteVec)

This example compares two local SQLite-based memory backends:

- `sqlite`: token-based (keyword) matching
- `sqlitevec`: semantic vector search powered by `sqlite-vec` (requires embeddings)

It inserts the same set of memories into both backends, then runs a small set
of queries that intentionally use different wording (synonyms / paraphrases).
The program reports hit rate for top-k search results.

## Prerequisites

- Go 1.21+
- An OpenAI-compatible embedding endpoint

## Environment Variables

Required (for embeddings):

- `OPENAI_API_KEY`

Optional (embedding overrides):

- `OPENAI_EMBEDDING_API_KEY`
- `OPENAI_EMBEDDING_BASE_URL`
- `OPENAI_EMBEDDING_MODEL`

If `OPENAI_EMBEDDING_API_KEY` is not set, the embedder uses `OPENAI_API_KEY`.
If `OPENAI_EMBEDDING_BASE_URL` is not set, the embedder uses `OPENAI_BASE_URL`.

## Run

```bash
cd examples/memory/compare
export OPENAI_API_KEY="your-api-key"
go run .
```

