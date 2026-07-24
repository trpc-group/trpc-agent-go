# Chunking Capability Demo

This document is designed to expose the boundaries selected by different chunking strategies. It combines Markdown headings, mixed punctuation, lists, code blocks, emoji, and long continuous text so fixed-size and structure-aware behavior can be compared directly.

## Why Chunking Matters

A knowledge base usually cannot send an entire document to an embedding model as one retrieval unit. Chunking creates smaller units: very large chunks mix topics, while very small chunks can lose context. Overlap can carry the end of one chunk into the next, but it also increases index size.

The same trade-off appears in English documents. Large chunks preserve context but may reduce retrieval precision. Small chunks are focused, but a sentence or an argument can be split across two boundaries.

### Practical Evaluation Criteria

- Every chunk should stay within the configured size budget.
- Unicode content must remain valid UTF-8.
- RecursiveChunking should prefer paragraph and sentence boundaries.
- MarkdownChunking should preserve the relationship between headings and their content.
- Adjacent chunks should repeat content only when overlap is configured explicitly.

### Readers and Default Strategies

| Document type | Reader | Default strategy | Size unit | Focus |
|---|---|---|---|---|
| Markdown | MarkdownReader | MarkdownChunking | Unicode runes | Header paths, paragraphs, and code blocks |
| Text | TextReader | FixedSizeChunking | Unicode runes | Natural boundaries and overlap |
| CSV | CSVReader | FixedSizeChunking | Unicode runes | Text normalized by the Reader |
| JSON | JSONReader | JSONChunking | serialized bytes | Object and array hierarchy |

The table is a Markdown block in its own right. Set the chunk size to 240 or less to see whether the header, separator, and data rows remain together or require finer splitting.

## Mixed Characters and Natural Boundaries

The following paragraph intentionally uses Chinese punctuation:

第一段使用中文标点。模型收到问题以后，会先分析意图；然后检索知识库；最后组合答案！如果检索结果不足，它是否应该继续调用工具？这取决于 Agent 的执行策略。

The next paragraph uses English punctuation. An agent receives a request, selects a tool, observes the result, and then decides whether another step is required. Sentence-aware splitting should prefer these punctuation boundaries instead of cutting through arbitrary words.

Emoji are also part of the input: 🤖 represents an agent, 🔍 represents retrieval, and ✅ represents completion. A raw byte split can corrupt a multibyte character, while rune-aware splitting keeps the string valid.

## Configuration Example

The following code block is deliberately longer than a typical segment. It shows how the Markdown strategy handles a fenced block that cannot fit in one chunk:

```go
package main

import (
    "fmt"
    "unicode/utf8"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func compare(doc *document.Document, size int, overlap int) error {
    strategies := []struct {
        name     string
        strategy chunking.Strategy
    }{
        {
            name: "fixed",
            strategy: chunking.NewFixedSizeChunking(
                chunking.WithChunkSize(size),
                chunking.WithOverlap(overlap),
            ),
        },
        {
            name: "recursive",
            strategy: chunking.NewRecursiveChunking(
                chunking.WithRecursiveChunkSize(size),
                chunking.WithRecursiveOverlap(overlap),
            ),
        },
        {
            name: "markdown",
            strategy: chunking.NewMarkdownChunking(
                chunking.WithMarkdownChunkSize(size),
                chunking.WithMarkdownOverlap(overlap),
            ),
        },
    }

    for _, candidate := range strategies {
        chunks, err := candidate.strategy.Chunk(doc)
        if err != nil {
            return fmt.Errorf("%s: %w", candidate.name, err)
        }
        fmt.Printf("%s produced %d chunks\n", candidate.name, len(chunks))
        for index, chunk := range chunks {
            fmt.Printf(
                "  #%d runes=%d bytes=%d id=%s\n",
                index+1,
                utf8.RuneCountInString(chunk.Content),
                len(chunk.Content),
                chunk.ID,
            )
        }
    }
    return nil
}
```

After configuring the strategies, compare each chunk's content, size, metadata, and shared boundary with its predecessor. This process requires neither a model nor a vector database.

## Long Paragraph

In a real knowledge base, one paragraph can explain several related concepts in sequence. A document may describe how data enters a Reader, how the Reader produces a Document, how a Chunking Strategy creates multiple chunks, and how an Embedder and Vector Store complete indexing. RecursiveChunking should try higher-priority separators first and use lower-priority separators to refine pieces that remain oversized. This recursive process must preserve source order and avoid corrupting multibyte Unicode content.

The following long identifier tests content without whitespace:

`TRPCAgentGoChunkingBoundaryWithoutSpaces0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz`

## Conclusion

Good chunking does not simply minimize the number of chunks. It balances the size budget, semantic completeness, retrieval precision, and index cost. Adjust the chunk size and overlap repeatedly to compare the strategies' actual output.
