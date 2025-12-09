# Metadata Filter Example

Demonstrates programmatic metadata filtering for precise search control.

## Features

- **Equal filter**: Exact metadata matching
- **OR filter**: Match any condition
- **AND filter**: Match all conditions
- **NOT filter**: Exclude matches
- **Complex filters**: Nested combinations

## Run

```bash
export OPENAI_API_KEY=your-api-key
go run main.go -vectorstore inmemory    # or pgvector|tcvector|elasticsearch
```

## Filter types

```go
// Equal
searchfilter.Equal("topic", "programming")

// OR
searchfilter.Or(
    searchfilter.Equal("topic", "programming"),
    searchfilter.Equal("topic", "machine_learning"),
)

// AND
searchfilter.And(
    searchfilter.Equal("topic", "programming"),
    searchfilter.Equal("difficulty", "beginner"),
)

// NOT
searchfilter.Not(searchfilter.Equal("topic", "advanced"))
```

## Use cases

- Filter by document type, category, date range
- Combine multiple filter conditions
- Exclude irrelevant content
- Fine-grained search control
