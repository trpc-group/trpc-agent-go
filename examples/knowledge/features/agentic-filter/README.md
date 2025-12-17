# Agentic Filter Example

Demonstrates intelligent metadata-based filtering using LLM.

## Features

- **Automatic filter selection**: LLM chooses appropriate metadata filters based on query
- **Metadata-aware search**: Filter results by category, topic, content type, etc.
- **Improved accuracy**: More relevant results by filtering before semantic search

## How it works

1. Sources are tagged with metadata (category, topic, etc.)
2. Agent analyzes user query
3. LLM selects appropriate metadata filters
4. Search is performed only on filtered documents

## Run

```bash
export OPENAI_API_KEY=your-api-key
go run main.go -vectorstore inmemory    # or pgvector|tcvector|elasticsearch
```

## Example queries

- "Find programming-related content" → filters by `topic=programming`
- "Show me machine learning docs" → filters by `topic=machine_learning`
- "What's in golang content?" → filters by `content_type=golang`
