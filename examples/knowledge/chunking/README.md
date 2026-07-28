# Chunking Examples

These offline examples start from the same Reader defaults used by knowledge
sources and show the effective chunking strategy. Advanced controls let you
override the Reader and compare `FixedSizeChunking`, `RecursiveChunking`,
`MarkdownChunking`, and `JSONChunking`. No model, embedding service, vector
store, or API credentials are required.

```text
chunking/
├── demo.go
├── samples/
│   ├── sample-catalog.md
│   ├── sample-edge.md
│   ├── sample-boundaries.md
│   ├── sample.csv
│   ├── sample.json
│   ├── sample.md
│   └── sample.txt
├── cmd/
│   └── main.go
└── web/
    ├── index.html
    └── main.go
```

The bundled samples cover the default Readers and several boundary cases:

| Sample | What it exercises |
|--------|-------------------|
| [`sample.md`](./samples/sample.md) | Markdown headings, a table, mixed-language boundaries, a long fenced Go block, emoji, and a long token |
| [`sample-edge.md`](./samples/sample-edge.md) | Sparse heading levels, nested blocks, a wide table, an oversized code block, combining characters, and fallback boundaries |
| [`sample-boundaries.md`](./samples/sample-boundaries.md) | Strict overlap budgets, CJK boundaries, numeric dots, version labels, punctuation clusters, and rune fallback |
| [`sample-catalog.md`](./samples/sample-catalog.md) | A realistic reference-document shape with grouped rows, several short tables, and a long table cell |
| [`sample.txt`](./samples/sample.txt) | Plain-text paragraphs, punctuation, whitespace, logs, CJK text, emoji, and an unbroken token |
| [`sample.csv`](./samples/sample.csv) | Multilingual records, empty fields, long values, URLs, and CSVReader normalization |
| [`sample.json`](./samples/sample.json) | Nested objects and arrays, empty values, multilingual strings, and properties that compete for a byte budget |

## Text usage example

From `examples/knowledge`:

```bash
go run ./chunking/cmd
```

The example keeps the API calls in `main.go` so they can be copied directly. It
uses `file.Source`, which is the normal application entry point:

1. **Source default (recommended):** set `file.WithChunkSize` and
   `file.WithChunkOverlap`. `FileSource` selects a Reader from the file
   extension, and the Reader selects its format-aware strategy.
2. **Custom strategy:** construct `RecursiveChunking` and pass it through
   `file.WithCustomChunkingStrategy`.

Both paths use a 240-rune chunk budget and a 24-rune overlap. Change the
constants at the top of `cmd/main.go` to try other budgets. The output shows
the Source configuration path, chunk content, and the chunk metadata used by
the knowledge pipeline. Pass another sample or local document with:

```bash
go run ./chunking/cmd -input ./chunking/samples/sample-edge.md
```

## Web viewer

Start the local server:

```bash
go run ./chunking/web
```

Then open [http://127.0.0.1:8080](http://127.0.0.1:8080).

![Knowledge Chunking Viewer](../../../docs/mkdocs/assets/img/knowledge/chunk-viewer.png)

The page opens with the Markdown overview and uses **Reader default
(recommended)**. Use the built-in sample selector to switch between Markdown,
plain text, CSV, and JSON inputs. Each selection restores Reader mode and shows
the Reader and effective strategy before the chunks. The left side contains the
editable source document and controls; the right side contains the generated
chunks. You can:

- switch among bundled overview and edge-case samples or upload a local document
- let the filename select the Reader and its default chunking strategy
- explicitly override the Reader with Fixed, Recursive, Markdown, or JSON
- leave chunk size at zero for the framework default, or tune size and overlap
- follow connector lines from each chunk to its source range
- click a chunk to jump the source view directly to its mapped position
- scroll the mapped source and chunk list together
- compare the same shared boundary highlighted in both the source and chunk views
- inspect syntax-highlighted JSON and tabular CSV source/chunk views
- inspect full chunk content and use **Show metadata** to reveal metadata

Uploaded files are read by the browser and sent only to the local Go server.
The example does not store them. JSONChunking disables overlap because that
strategy does not support it. JSONChunking restructures and serializes the input
instead of returning contiguous text ranges, so the viewer connects each chunk
to its matching JSON paths instead of pretending that it has one textual range.
CSVReader normalizes comma-separated rows into pipe-separated text, so its
connectors use the corresponding source rows. The viewer formats JSON with
syntax colors and renders CSV records as tables. CSV chunks with overlap stay in
the text view so the repeated boundary remains exact and visible.

The viewer uses a small Go HTTP server and plain HTML, CSS, and JavaScript. It
has no frontend framework or build step, and listens on localhost by default.
Use `-addr` to opt into another listen address. To keep the local demo bounded,
the server accepts documents up to 1 MiB, explicit chunk sizes of at least 32,
at least 16 units of new-content budget after overlap, at most 5000 output
chunks, at most 8 MiB of estimated and generated chunk content, and two
concurrent chunking requests.

## What the Reader selects

Most applications configure a `Source` or `Reader`, not a chunking strategy
directly. The framework chooses by document type:

| Document type | Reader behavior |
|---------------|-----------------|
| `.md`, `.markdown` | `MarkdownReader -> MarkdownChunking` |
| `.json` | `JSONReader -> JSONChunking` |
| `.txt`, `.text` | `TextReader` uses `FixedSizeChunking` with natural text boundaries |
| `.csv` | `CSVReader` uses line-preserving `FixedSizeChunking`; a record is split only when it exceeds the active new-content budget |
| `.pdf`, `.doc`, `.docx` | The optional format Reader uses `FixedSizeChunking` when its package is imported |
| `.proto` | `ProtoReader` creates AST entity chunks |
| `.go`, `.py` | The optional language Reader creates AST entity chunks when imported; otherwise Source falls back to `TextReader` |

`RecursiveChunking` is available as an explicit override when separator-aware
plain-text chunks are preferable. The web viewer demonstrates the text,
Markdown, JSON, and CSV Readers; it does not load optional AST readers or
attempt to decode binary PDF or DOCX uploads in the browser.

## Displayed data

Both views report:

- chunk IDs and exact content boundaries
- byte and Unicode rune counts
- whether final content stays within the configured size budget
- the actual shared boundary with the previous chunk
- chunk-size, overlapped-size, and full Markdown-header path metadata when present

Over-budget output is diagnostic. The examples display observed strategy
behavior without hiding or correcting it.

| Strategy | Boundary behavior |
|----------|-------------------|
| Fixed | Prefers lines, sentences, punctuation, and whitespace before a rune fallback, and rebalances a very small final piece |
| Recursive | Recursively refines oversized text by separator priority and rebalances the final rune-fallback pair |
| Markdown | Preserves heading scope and fenced code blocks, then splits oversized blocks by line, sentence, punctuation, whitespace, or rune boundaries |
| JSON | Traverses object keys deterministically, visits array indices numerically, and splits long strings on UTF-8-safe byte boundaries |

Tail rebalancing stays inside the same oversized logical block. Markdown
paragraphs prefer sentence and punctuation boundaries, tables and fenced code
prefer complete lines, and an unbroken token uses UTF-8-safe rune boundaries.
CSVReader enables `WithPreserveLines`, so a normalized record that fits the
active new-content budget stays intact; only an oversized record is refined
internally. Rebalancing does not merge unrelated headings, short sections, JSON
values, or CSV records merely to make all chunks the same size, so semantically
complete small chunks may still appear.

The default overlap is zero. Pass an explicit overlap value to inspect
overlapping boundaries. Overlap is a maximum: it may move to a natural boundary
or shrink to keep final content within the chunk-size budget. A very large
overlap leaves less room for new content and therefore produces more chunks.
Only an unbroken token falls back to an exact rune boundary. Fixed, Recursive,
and Markdown chunk sizes use Unicode runes; JSON chunk size uses serialized
bytes.
