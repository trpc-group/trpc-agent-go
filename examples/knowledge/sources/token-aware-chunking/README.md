# Token-Aware Chunking Example

This offline example configures a tokenizer directly on `FileSource`. `MarkdownReader` still selects `MarkdownChunking`, so Markdown headings and header-path metadata are preserved while `chunkSize` and `overlap` are measured in tokens.

## Run

From `examples/knowledge`:

```bash
go run ./sources/token-aware-chunking
```

No model, embedding service, vector store, or API credentials are required.

The example uses:

- `tiktoken.New("text-embedding-3-small")` to create a token counter matching an embedding model
- `file.WithChunkLengthFunc(counter.CountText)` to select token units
- a 48-token chunk content budget and an 8-token maximum overlap
- a bundled Markdown document containing nested headings and mixed English and Chinese text

The output reports the original document size and each chunk's token count, rune count, Markdown header path, and content. The budget applies to the chunker's output `Document.Content`; later postprocessing or embedding metadata may add content, so production configurations should reserve margin.
