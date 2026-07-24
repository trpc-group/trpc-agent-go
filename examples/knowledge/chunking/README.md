# Chunking Example

This offline example compares the chunk boundaries produced by
`FixedSizeChunking`, `RecursiveChunking`, and `MarkdownChunking`. It does not
require a model, an embedding service, a vector store, or API credentials.

The bundled [`sample.md`](./sample.md) contains:

- Markdown heading levels and a fenced Go code block
- Chinese and English sentence boundaries
- Lists, emoji, and multibyte UTF-8 characters
- A long paragraph and a long token without spaces

## Run

From `examples/knowledge`:

```bash
go run ./chunking
```

Enable overlap and use a smaller budget:

```bash
go run ./chunking -chunk-size 180 -overlap 24
```

Run only one strategy:

```bash
go run ./chunking -strategy recursive -chunk-size 180 -overlap 24
```

The example prints at most 20 chunks per strategy by default. Print every
chunk when investigating all boundaries:

```bash
go run ./chunking -max-chunks 0
```

Use another Markdown document:

```bash
go run ./chunking -strategy markdown -input ./exampledata/file/llm.md
```

## Output

For every chunk, the example prints:

- the chunk ID and exact content boundaries
- byte and Unicode rune counts
- whether the final content stays within the configured rune budget
- the actual shared boundary with the previous chunk
- chunk-size, overlapped-size, and Markdown-header metadata when present

`within_budget` is diagnostic output. If it is `false`, the selected strategy
and configuration produced content larger than the requested budget; the
example intentionally displays that result instead of hiding or correcting it.

This makes several behaviors easy to compare:

| Strategy | Boundary behavior |
|----------|-------------------|
| Fixed | Splits at a hard rune limit and optionally uses a sliding overlap window |
| Recursive | Prefers configured paragraph, sentence, and character separators, recursively refining oversized pieces |
| Markdown | Prefers Markdown heading and block structure before falling back to smaller boundaries |

The default overlap is zero. Pass an explicit overlap value to inspect
overlapping boundaries.
