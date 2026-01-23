# Document Source Configuration

> **Example Code**: [examples/knowledge/sources](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources)

The source module provides various document source types, each supporting rich configuration options.

## Supported Document Source Types

| Source Type | Description | Example |
|-------------|-------------|---------|
| **File Source (file)** | Single file processing | [Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/file-source) |
| **Directory Source (dir)** | Batch directory processing | [Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/directory-source) |
| **URL Source (url)** | Fetch content from web pages | [Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/url-source) |
| **Auto Source (auto)** | Intelligent type detection | [Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/auto-source) |

## File Source

Single file processing, supports .txt, .md, .json, .doc, .csv, and other formats:

```go
import (
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

fileSrc := filesource.New(
    []string{"./data/llm.md"},
    filesource.WithChunkSize(1000),      // Chunk size
    filesource.WithChunkOverlap(200),    // Chunk overlap
    filesource.WithName("LLM Doc"),
    filesource.WithMetadataValue("type", "documentation"),
)
```

## Directory Source

Batch directory processing with recursive and filtering support:

```go
import (
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
)

dirSrc := dirsource.New(
    []string{"./docs"},
    dirsource.WithRecursive(true),                           // Recursively process subdirectories
    dirsource.WithFileExtensions([]string{".md", ".txt"}),   // File extension filter
    dirsource.WithChunkSize(800),
    dirsource.WithName("Documentation"),
)
```

## URL Source

Fetch content from web pages and APIs:

```go
import (
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

urlSrc := urlsource.New(
    []string{"https://en.wikipedia.org/wiki/Artificial_intelligence"},
    urlsource.WithChunkSize(1000),
    urlsource.WithChunkOverlap(200),
    urlsource.WithName("Web Content"),
)
```

### URL Source Advanced Configuration

Separate content fetching and document identification:

```go
import (
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

urlSrcAlias := urlsource.New(
    []string{"https://trpc-go.com/docs/api.md"},     // Identifier URL (for document ID and metadata)
    urlsource.WithContentFetchingURL([]string{"https://github.com/trpc-group/trpc-go/raw/main/docs/api.md"}), // Actual content fetching URL
    urlsource.WithName("TRPC API Docs"),
    urlsource.WithMetadataValue("source", "github"),
)
```

> **Note**: When using `WithContentFetchingURL`, the identifier URL should retain the file information from the content fetching URL, for example:
> - Correct: Identifier URL is `https://trpc-go.com/docs/api.md`, fetching URL is `https://github.com/.../docs/api.md`
> - Incorrect: Identifier URL is `https://trpc-go.com`, loses document path information

## Auto Source

Intelligent type detection, automatically selects processor:

```go
import (
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
)

autoSrc := autosource.New(
    []string{
        "Cloud computing provides on-demand access to computing resources.",
        "https://docs.example.com/api",
        "./config.yaml",
    },
    autosource.WithName("Mixed Sources"),
    autosource.WithChunkSize(1000),
)
```

## Combined Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// Combine multiple sources
sources := []source.Source{fileSrc, dirSrc, urlSrc, autoSrc}

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))
vectorStore := vectorinmemory.New()

// Pass to Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
)

// Load all sources
if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load knowledge base: %v", err)
}
```

## Configuring Metadata

To enable filter functionality, it's recommended to add rich metadata when creating document sources.

> For detailed filter usage guide, please refer to [Filter Documentation](filter.md).

```go
sources := []source.Source{
    // File source with metadata
    filesource.New(
        []string{"./docs/api.md"},
        filesource.WithName("API Documentation"),
        filesource.WithMetadataValue("category", "documentation"),
        filesource.WithMetadataValue("topic", "api"),
        filesource.WithMetadataValue("service_type", "gateway"),
        filesource.WithMetadataValue("protocol", "trpc-go"),
        filesource.WithMetadataValue("version", "v1.0"),
    ),

    // Directory source with metadata
    dirsource.New(
        []string{"./tutorials"},
        dirsource.WithName("Tutorials"),
        dirsource.WithMetadataValue("category", "tutorial"),
        dirsource.WithMetadataValue("difficulty", "beginner"),
        dirsource.WithMetadataValue("topic", "programming"),
    ),

    // URL source with metadata
    urlsource.New(
        []string{"https://example.com/wiki/rpc"},
        urlsource.WithName("RPC Wiki"),
        urlsource.WithMetadataValue("category", "encyclopedia"),
        urlsource.WithMetadataValue("source_type", "web"),
        urlsource.WithMetadataValue("topic", "rpc"),
        urlsource.WithMetadataValue("language", "zh"),
    ),
}
```

## Content Transformer

> **Example Code**: [examples/knowledge/features/transform](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/transform)

Transformer is used to preprocess and postprocess content before and after document chunking. This is particularly useful for cleaning text extracted from PDFs, web pages, and other sources, removing excess whitespace, duplicate characters, and other noise.

### Processing Flow

```
Document → Preprocess → Processed Document → Chunking → Chunks → Postprocess → Final Chunks
```

### Built-in Transformers

#### CharFilter - Character Filter

Removes specified characters or strings:

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/transform"

// Remove newlines, tabs, and carriage returns
filter := transform.NewCharFilter("\n", "\t", "\r")
```

#### CharDedup - Character Deduplicator

Merges consecutive duplicate characters or strings into a single instance:

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/transform"

// Merge multiple consecutive spaces into one, merge multiple newlines into one
dedup := transform.NewCharDedup(" ", "\n")

// Example:
// Input:  "hello     world\n\n\nfoo"
// Output: "hello world\nfoo"
```

### Usage

Transformers are passed to various document sources via the `WithTransformers` option:

```go
import (
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

// Create transformers
filter := transform.NewCharFilter("\t")           // Remove tabs
dedup := transform.NewCharDedup(" ", "\n")        // Merge consecutive spaces and newlines

// File source with transformers
fileSrc := filesource.New(
    []string{"./data/document.pdf"},
    filesource.WithTransformers(filter, dedup),
)

// Directory source with transformers
dirSrc := dirsource.New(
    []string{"./docs"},
    dirsource.WithTransformers(filter, dedup),
)

// URL source with transformers
urlSrc := urlsource.New(
    []string{"https://example.com/article"},
    urlsource.WithTransformers(filter, dedup),
)

// Auto source with transformers
autoSrc := autosource.New(
    []string{"./mixed-content"},
    autosource.WithTransformers(filter, dedup),
)
```

### Combining Multiple Transformers

Multiple transformers are executed in sequence:

```go
// First remove tabs, then merge consecutive spaces
filter := transform.NewCharFilter("\t")
dedup := transform.NewCharDedup(" ")

src := filesource.New(
    []string{"./data/messy.txt"},
    filesource.WithTransformers(filter, dedup),  // Executed in order
)
```

### Typical Use Cases

| Scenario | Recommended Configuration |
|----------|---------------------------|
| PDF text cleanup | `CharDedup(" ", "\n")` - Merge excess spaces and newlines from PDF extraction |
| Web content processing | `CharFilter("\t")` + `CharDedup(" ")` - Remove tabs and merge spaces |
| Code documentation processing | `CharDedup("\n")` - Merge excess blank lines, preserve code indentation |
| General text cleanup | `CharFilter("\r")` + `CharDedup(" ", "\n")` - Remove carriage returns and merge whitespace |

## PDF File Support

Since the PDF reader depends on third-party libraries, to avoid introducing unnecessary dependencies in the main module, the PDF reader uses a separate `go.mod`.

To support PDF file reading, manually import the PDF reader package in your code:

```go
import (
    // Import PDF reader to support .pdf file parsing
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)
```

> **Note**: Readers for other formats (.txt/.md/.csv/.json, etc.) are automatically registered and don't need manual import.
