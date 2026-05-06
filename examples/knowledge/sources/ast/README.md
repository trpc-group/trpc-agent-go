# AST-based Multi-language Repo Source

Demonstrates repository-source AST ingestion for Go, and combines an additional directory source for Proto.

## Overview

This example shows how the knowledge module handles:

- **Repo Source**: Loads a remote Git repository as a single-source single-repo entry
- **Go AST Parsing**: Go files are parsed package-aware through the Go reader underneath repo source
- **Proto Source**: Local Proto files are loaded through an additional directory source
- **Proto AST Parsing**: Proto files are parsed semantically through the Proto reader underneath repo source
- **trpc_ast_* Prefix**: Metadata uses `trpc_ast_` prefix aligned with trpc-ast-rag conventions
- **Git URL Support**: Demonstrates that [`repo.New(...)`](main.go:87) can clone and ingest a Git URL directly

## Usage

```bash
export OPENAI_API_KEY=sk-xxxx
export OPENAI_BASE_URL=https://api.openai.com/v1
export MODEL_NAME=deepseek-v4-flash
go run main.go

# force mock embedder for chunk preview without real embeddings
go run main.go -embedder mock

# require real OpenAI embeddings
go run main.go -embedder openai

# or customize the dump directory
mkdir -p /tmp/ast-demo-output
go run main.go -dumpdir /tmp/ast-demo-output
```

By default, [`-dumpdir`](main.go:34) writes parsed documents to a local [`chunked`](examples/knowledge/sources/ast/main.go) directory under the runtime working directory. The example preserves the original source tree layout under separate sections such as `go-reader/`, `proto-reader/`, and `repo-source/`.

The [`-embedder`](examples/knowledge/sources/ast/main.go:42) option controls embedding behavior:

- `auto`: use OpenAI when `OPENAI_API_KEY` is available, otherwise fall back to mock
- `mock`: always use the in-process mock embedder
- `openai`: require a real OpenAI embedder

When using the mock embedder, semantic similarity is **not reliable**, but AST parsing, chunk generation, metadata extraction, repository loading, and dump output are still valid. This makes it suitable for previewing chunking behavior locally.

## What It Demonstrates

### 1. Repo Source for Mixed-language Repository

Uses [`repo.New(...)`](main.go:103) to load one remote Git repository:

```go
src := repo.New(
    repo.WithRepository(repo.Repository{
        URL:    "https://github.com/trpc-group/trpc-go",
        Branch: "main",
    }),
    repo.WithFileExtensions([]string{".go", ".proto", ".md"}),
)
```

### 2. AST-based Metadata Extraction

The AST readers extract structured metadata with `trpc_ast_` prefix:

| Metadata Key | Description |
|-------------|-------------|
| `trpc_ast_syntax` | proto2 or proto3 |
| `trpc_ast_package` | Package name (e.g., `example.v1`) |
| `trpc_ast_imports` | List of imported files |
| `trpc_ast_import_count` | Number of imports |
| `trpc_ast_services` | Proto service names |
| `trpc_ast_service_count` | Number of proto services |
| `trpc_ast_receiver_type` | Go method receiver type |
| `trpc_ast_signature` | Entity signature |

### 3. Knowledge Base Integration

If `OPENAI_API_KEY` is provided, the example also loads the repo source plus an additional Proto directory source into a knowledge base. If it is not provided, the example falls back to a mock embedder.

```go
kb := knowledge.New(
    knowledge.WithVectorStore(vs),
    knowledge.WithEmbedder(openai.New()),
    knowledge.WithSources([]source.Source{src}),
)

kb.Load(ctx, knowledge.WithShowProgress(true))
```

## Example Output

```
🔮 AST Source Demo (Repo Source)
================================

📦 Step 1: Repo source preview on a repository
✓ Repo source parsed N documents from repository
✓ Repo source parse time: 2.314s
✓ This source path covers Go AST extraction and markdown/document ingestion

⚠️ OPENAI_API_KEY not set, using mock embedder for local demo

✓ Loading mixed-language repository into knowledge base with vector store=inmemory
✓ knowledge.Load completed
✓ knowledge.Load time: 13.019s

✅ Demo completed!
```

### Why it feels fast

In practice, the AST extraction stage is usually much faster than the full [`knowledge.Load()`](examples/knowledge/sources/ast/main.go) pipeline.

- `Repo source parse time` mainly reflects:
  - Git clone / local scan
  - Go AST parsing
  - Proto AST parsing
  - markdown/document reader dispatch
- `knowledge.Load time` additionally includes:
  - embedding generation
  - vector store writes
  - progress / load orchestration

So if you see output like:

```text
✓ knowledge.Load completed
✓ knowledge.Load time: 13.019s
```

that does **not** mean AST parsing itself is slow. In mock mode especially, this example is meant to emphasize that:

- AST parsing and chunk generation are quick
- dump output can be previewed locally
- semantic retrieval quality is the only part that is intentionally approximate when using the mock embedder

Example dumped chunk preview:

```text
-----

parsed content:
index: 7
name: Server
content_length: 570

content:
// Server is a tRPC server.
// One process, one server. A server may offer one or more services.
type Server struct {
	MaxCloseWaitTime time.Duration // max waiting time when closing server

	services map[string]Service // k=serviceName,v=Service

	mux sync.Mutex // guards onShutdownHooks
	// onShutdownHooks are hook functions that would be executed when server is
	// shutting down (before closing all services of the server).
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
trpc_agent_go_chunk_index: 0
trpc_agent_go_chunk_size: 570
trpc_agent_go_content_length: 570
trpc_agent_go_file_ext: .go
trpc_agent_go_file_mode: -rw-r--r--
trpc_agent_go_file_name: server.go
trpc_agent_go_file_path: server/server.go
trpc_agent_go_file_size: 3973
trpc_agent_go_modified_at: 2026-04-13 07:10:51.28049578 +0000 UTC
trpc_agent_go_repo_path: /tmp/trpc-agent-go-repo-483441217
trpc_agent_go_source: repo
trpc_agent_go_source_name: AST Multi-language Repository
trpc_agent_go_uri: file:///tmp/trpc-agent-go-repo-483441217/server/server.go
trpc_ast_comment: Server is a tRPC server.
One process, one server. A server may offer one or more services.
trpc_ast_file_path: server/server.go
trpc_ast_full_name: trpc.group/trpc-go/trpc-go/server.Server
trpc_ast_go_type_kind: definition
trpc_ast_import_count: 5
trpc_ast_imports: [context errors os sync time]
trpc_ast_language: go
trpc_ast_line_end: 42
trpc_ast_line_start: 26
trpc_ast_name: Server
trpc_ast_package: trpc.group/trpc-go/trpc-go/server
trpc_ast_repo_name: trpc-go
trpc_ast_repo_url: https://github.com/trpc-group/trpc-go
trpc_ast_scope: code
trpc_ast_signature: type Server struct
trpc_ast_type: Struct

-----
```

## Supported Source Types

This example intentionally focuses on repo source as the single ingestion entrypoint:

```go
// One repo source handles one repository
repo.New(
  repo.WithRepository(repo.Repository{URL: "https://github.com/trpc-group/trpc-go", Branch: "main"}),
  repo.WithFileExtensions([]string{".go", ".md"}),
)
```

## Alignment with trpc-ast-rag

This demo aligns with the multi-language repository parsing direction in [`trpc-ast-rag`](../../../../trpc-ast-rag):

- Git repositories can be cloned and parsed directly
- Go uses package-aware directory parsing
- Proto uses semantic entity extraction (service / rpc / message / enum)
- multiple inputs can be loaded into one knowledge base together
- `trpc_ast_*` metadata remains the unified semantic layer for retrieval and tooling

## Related Examples

- [auto-source](../auto-source/) - Mixed content types (text, file, URL)
- [file-source](../file-source/) - Basic file source usage
- [directory-source](../directory-source/) - Directory source with recursive loading
