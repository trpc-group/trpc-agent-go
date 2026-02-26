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

## Chunking Strategy

> **Example Code**: [fixed-chunking](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/fixed-chunking) | [recursive-chunking](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/recursive-chunking)

Chunking is the process of splitting long documents into smaller fragments, which is crucial for vector retrieval. The framework provides multiple built-in chunking strategies and supports custom strategies.

### Built-in Chunking Strategies

| Strategy | Description | Use Case |
|----------|-------------|----------|
| **FixedSizeChunking** | Fixed-size chunking | General text, simple and fast |
| **RecursiveChunking** | Recursive chunking by separator hierarchy | Preserving semantic integrity |
| **MarkdownChunking** | Chunk by Markdown structure | Markdown documents (default) |
| **JSONChunking** | Chunk by JSON structure | JSON files (default) |

### Default Behavior

Each file type has an associated chunking strategy:

- `.md` files → MarkdownChunking (recursively chunk by heading levels H1→H6→paragraph→fixed size)
- `.json` files → JSONChunking (chunk by JSON structure)
- `.txt/.csv/.docx` etc. → FixedSizeChunking

**Default Parameters**:

| Parameter | Default | Description |
|-----------|---------|-------------|
| ChunkSize | 1024 | Maximum characters per chunk |
| Overlap | 128 | Overlapping characters between adjacent chunks |

> Default chunking strategies are affected by `chunkSize` parameter. The `overlap` parameter only applies to FixedSizeChunking, RecursiveChunking, and MarkdownChunking. JSONChunking does not support overlap.

Adjust default strategy parameters via `WithChunkSize` and `WithChunkOverlap`:

```go
fileSrc := filesource.New(
    []string{"./data/document.txt"},
    filesource.WithChunkSize(512),     // Chunk size (characters)
    filesource.WithChunkOverlap(64),   // Chunk overlap (characters)
)
```

### Custom Chunking Strategy

Use `WithCustomChunkingStrategy` to override the default chunking strategy.

> **Note**: Custom chunking strategy completely overrides `WithChunkSize` and `WithChunkOverlap` configurations. Chunking parameters must be set within the custom strategy.

#### FixedSizeChunking - Fixed Size Chunking

Splits text by fixed character count with overlap support:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

// Create fixed-size chunking strategy
fixedChunking := chunking.NewFixedSizeChunking(
    chunking.WithChunkSize(512),   // Max 512 characters per chunk
    chunking.WithOverlap(64),      // 64 characters overlap between chunks
)

fileSrc := filesource.New(
    []string{"./data/document.md"},
    filesource.WithCustomChunkingStrategy(fixedChunking),
)
```

#### RecursiveChunking - Recursive Chunking

Recursively splits by separator hierarchy, preferring natural boundaries:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

// Create recursive chunking strategy
recursiveChunking := chunking.NewRecursiveChunking(
    chunking.WithRecursiveChunkSize(512),   // Max chunk size
    chunking.WithRecursiveOverlap(64),      // Overlap between chunks
    // Custom separator priority (optional)
    chunking.WithRecursiveSeparators([]string{"\n\n", "\n", ". ", " "}),
)

fileSrc := filesource.New(
    []string{"./data/article.txt"},
    filesource.WithCustomChunkingStrategy(recursiveChunking),
)
```

**Separator Priority Explanation**:

1. `\n\n` - First try to split by paragraph
2. `\n` - Then split by line
3. `. ` - Then split by sentence
4. ` ` - Split by space

Recursive chunking attempts to use higher priority separators, only using the next level separator when chunks still exceed the maximum size. If all separators fail to split text within chunkSize, it will force split by chunkSize.

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
