# Document Source Configuration

> **Example Code**: [examples/knowledge/sources](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources)

The source module provides various document source types, each supporting rich configuration options.

## Supported Document Source Types

| Source Type | Description | Example |
|-------------|-------------|---------|
| **File Source (file)** | Single file processing | [Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/file-source) |
| **Directory Source (dir)** | Batch directory processing | [Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/directory-source) |
| **Repo Source (repo)** | Git repository / local repo directory | [AST Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/sources/ast) |
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

## Repo Source

The repo source targets code repository scenarios, suited for:

- Loading a remote **Git URL** directly
- Loading a locally checked-out **repository directory**
- Uniformly processing Go / Proto / Markdown and other content within a single repository

> **Current open-source status**: AST-aware code parsing is currently open-sourced for **Go** and **Proto / PB**. Support for `Python`, `C++`, `JavaScript`, and other languages is being progressively open-sourced. For languages not yet open-sourced, the repo source can still process text files via plain document readers, but without AST-level semantic entities.

### Typical Use Cases

- Loading a remote Git repository to build a code knowledge base
- Loading a local repository restricted to a specific subdirectory
- Unified ingest of Go + Markdown (and other supported types) within a single repository

### Basic Usage

```go
import (
    reposource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/repo"
)

repoSrc := reposource.New(
    reposource.WithRepository(
        reposource.Repository{
            URL:    "https://github.com/trpc-group/trpc-go",
            Branch: "main",
        },
    ),
    reposource.WithName("Code Repository"),
    reposource.WithFileExtensions([]string{".go", ".md"}),
)
```

### Repository Struct

`Repository` describes a single repository input with independent version and scope configuration:

| Field | Description |
|-------|-------------|
| `URL` | Remote Git repository URL |
| `Dir` | Local repository directory |
| `Branch` | Target branch |
| `Tag` | Target tag |
| `Commit` | Target commit |
| `Subdir` | Scan only a subdirectory within the repository |
| `RepoName` | Custom repository name |
| `RepoURL` | Custom repository URL (overrides auto-detection) |

> `URL` and `Dir` are mutually exclusive. A single `repo.Source` processes only one repository input.

### Version Selection Priority

When multiple version fields are set, the priority is:

1. `Commit`
2. `Tag`
3. `Branch`

That is, if both `Commit` and `Branch` are provided, `Commit` is checked out.

### Scan Scope Control

- [`WithFileExtensions`](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/source/repo/options.go) — controls which file extensions are scanned
- [`WithSkipDirs`](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/source/repo/options.go) — controls which directory names are skipped
- [`WithSkipSuffixes`](https://github.com/trpc-group/trpc-agent-go/blob/main/knowledge/source/repo/options.go) — controls which file suffixes are skipped
- `Repository.Subdir` — restricts scanning to a subdirectory within the repository

Example: scan only Go and Markdown files under `server/`:

```go
repoSrc := reposource.New(
    reposource.WithRepository(
        reposource.Repository{
            URL:    "https://github.com/trpc-group/trpc-go",
            Branch: "main",
            Subdir: "server",
        },
    ),
    reposource.WithFileExtensions([]string{".go", ".md"}),
    reposource.WithSkipSuffixes([]string{".pb.go", ".trpc.go", "_mock.go"}),
)
```

### Metadata

The repo source enriches documents produced by readers with repository-level metadata:

| Metadata Key | Description |
|---|---|
| `trpc_agent_go_source=repo` | Document originates from a repo source |
| `trpc_agent_go_repo_path` | Local root directory of the cloned repository |
| `trpc_ast_repo_name` | Repository name |
| `trpc_ast_repo_url` | Repository URL |
| `trpc_ast_branch` | Version identifier being parsed (branch/tag/commit) |
| `trpc_ast_file_path` | Repo-relative file path |

Notes:

- `trpc_ast_file_path` represents the **logical path within the repository**, not a remote Git URL.
- For Git URL inputs, the repo source first clones to a temporary directory, then writes the repo-relative path into `trpc_ast_file_path`.

### Relation to AST Readers

The repo source does not parse code itself; it dispatches to the appropriate reader based on file type:

- `.go` → Go AST reader
- `.proto` → Proto AST reader
- `.md` → Markdown reader
- Other registered extensions → corresponding reader

### Parsed Output Example

Below is a sample output from chunking a struct definition in a remote Go repository:

```text
parsed content:
index: 7
name: Server
content_length: 570

content:
// Server is a tRPC server.
// One process, one server. A server may offer one or more services.
type Server struct {
    MaxCloseWaitTime time.Duration

    services map[string]Service

    mux sync.Mutex
    onShutdownHooks []func()

    failedServices sync.Map
    signalCh       chan os.Signal
    closeCh        chan struct{}
    closeOnce      sync.Once
}

embedding text:
{
  "comment": "Server is a tRPC server.\nOne process, one server. A server may offer one or more services.",
  "file_path": "/tmp/trpc-agent-go-repo-483441217/server/server.go",
  "full_name": "trpc.group/trpc-go/trpc-go/server.Server",
  "id": "trpc.group/trpc-go/trpc-go/server.Server",
  "name": "Server",
  "package": "trpc.group/trpc-go/trpc-go/server",
  "signature": "type Server struct",
  "type": "Struct"
}

metadata:
trpc_agent_go_source: repo
trpc_agent_go_file_path: server/server.go
trpc_ast_repo_name: trpc-go
trpc_ast_repo_url: https://github.com/trpc-group/trpc-go
trpc_ast_file_path: server/server.go
trpc_ast_full_name: trpc.group/trpc-go/trpc-go/server.Server
trpc_ast_type: Struct
trpc_ast_signature: type Server struct
trpc_ast_language: go
...
```

The output has three layers:

#### 1. `content`: Raw chunk content

The `content` field stores the final text written to the knowledge base. For AST-aware Go / Proto readers, content is not a character-truncated fragment but a **semantically complete code entity**.

In the example above, the entity is the `Server` struct, so the content includes the struct comment, `type Server struct { ... }`, and all field definitions.

#### 2. `embedding text`: Structured summary for vectorization

`embedding text` is a compact summary optimized for semantic embedding, retaining fields such as `name`, `full_name`, `package`, `signature`, `comment`, and `file_path`. This helps embeddings focus on "what this entity is, which package it belongs to, and what it does."

#### 3. `metadata`: Filtering, locating, and display

`metadata` is primarily used for retrieval filtering, display, and source tracking — not for embedding.

##### `trpc_agent_go_*`

Framework-level metadata describing the document origin:

- `trpc_agent_go_source=repo`: document comes from a repo source
- `trpc_agent_go_file_path`: repo-relative file path
- `trpc_agent_go_repo_path`: local root of the cloned repository
- `trpc_agent_go_uri`: actual file URI

##### `trpc_ast_*`

AST semantic metadata describing the code entity:

- `trpc_ast_type=Struct`
- `trpc_ast_full_name`
- `trpc_ast_signature`
- `trpc_ast_language=go`
- `trpc_ast_repo_name` / `trpc_ast_repo_url`

These are used for precise filtering, such as retrieving all `Struct` types in a given package, or all `rpc` / `message` definitions in a proto service.

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
