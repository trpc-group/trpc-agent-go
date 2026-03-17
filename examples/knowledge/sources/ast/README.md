# AST-based Proto File Source

Demonstrates how proto files are parsed using an AST-based reader with knowledge base integration.

## Overview

This example shows how the knowledge module handles `.proto` files:

- **Automatic Detection**: Proto files are automatically detected by `.proto` extension
- **Metadata Extraction**: Extracts syntax, package, imports, services, and messages
- **trpc_ast_* Prefix**: Metadata uses `trpc_ast_` prefix (compatible with trpc-ast-rag)
- **Knowledge Integration**: Uses auto source with LLM agent for semantic search

## Usage

```bash
export OPENAI_API_KEY=sk-xxxx
export OPENAI_BASE_URL=https://api.openai.com/v1
export MODEL_NAME=deepseek-chat
go run main.go
```

## What It Demonstrates

### 1. Auto Source with Proto Files

Uses `auto.New()` to automatically detect and parse `.proto` files:

```go
src := auto.New(
    []string{
        util.ExampleDataPath("file/api.proto"),
    },
    auto.WithName("Proto API Documentation"),
    auto.WithMetadataValue("source_type", "ast-proto"),
)
```

### 2. AST-based Metadata Extraction

The proto reader extracts structured metadata with `trpc_ast_` prefix:

| Metadata Key | Description |
|-------------|-------------|
| `trpc_ast_syntax` | proto2 or proto3 |
| `trpc_ast_package` | Package name (e.g., `example.v1`) |
| `trpc_ast_imports` | List of imported files |
| `trpc_ast_import_count` | Number of imports |
| `trpc_ast_services` | List of service names |
| `trpc_ast_service_count` | Number of services |
| `trpc_ast_messages` | List of message names |
| `trpc_ast_message_count` | Number of messages |

### 3. Knowledge Base Integration

Creates a knowledge base with the proto source and uses LLM agent for queries:

```go
kb := knowledge.New(
    knowledge.WithVectorStore(vs),
    knowledge.WithEmbedder(openai.New()),
    knowledge.WithSources([]source.Source{src}),
)

// Load documents
kb.Load(ctx, knowledge.WithShowProgress(true))

// Create search tool and agent
searchTool := knowledgetool.NewKnowledgeSearchTool(kb)
agent := llmagent.New("proto-assistant", ...)
```

## Example Output

```
🔮 AST-based Proto File Source Demo
====================================
Vector Store: inmemory

📥 Loading proto file with AST-based parsing...
   - Extracting syntax, package, imports
   - Identifying services and messages
   - Adding trpc_ast_* metadata for enhanced retrieval

Loading documents: 3 chunks processed

1. 🔍 Query: What services are defined in the API?
   🤖 Response: The API defines AgentService and KnowledgeService...

2. 🔍 Query: Tell me about the AgentRequest message structure
   🤖 Response: AgentRequest includes fields like query, session_id, and context...

✅ Demo completed!
```

## Supported Source Types

Proto files work with all source types:

```go
// Single file
file.New([]string{"api.proto"})

// Directory (recursive)
dir.New([]string{"./proto/"})

// Auto-detection (recommended)
auto.New([]string{"./docs/", "./api.proto"})
```

## Related Examples

- [auto-source](../auto-source/) - Mixed content types (text, file, URL)
- [file-source](../file-source/) - Basic file source usage
- [dir-source](../dir-source/) - Directory source with recursive loading
