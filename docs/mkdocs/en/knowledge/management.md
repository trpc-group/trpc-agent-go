# Knowledge Base Management

> **Example Code**: [examples/knowledge/features/management](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/management)

The Knowledge system provides powerful knowledge base management functionality, supporting dynamic source management and intelligent sync mechanisms.

## Source Sync Mode Comparison

Knowledge provides two loading modes: **Default Mode (sync disabled)** and **Sync Mode (WithEnableSourceSync enabled)**.

### Default Mode (Sync Disabled)

By default, `WithEnableSourceSync` is `false`, and Knowledge uses **append-only loading**:

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge"
)

kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
    // WithEnableSourceSync defaults to false
)

if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load: %v", err)
}
```

**Default Mode Behavior**:

- **Append Only**: Each `Load()` only adds new documents to vector store
- **No Change Detection**: Existing documents won't be updated even if source file content has changed
- **No Orphan Cleanup**: Vector data for deleted source files won't be automatically cleaned up
- **Use Cases**: One-time imports, scenarios where data only grows, business manages data sources independently

### Sync Mode (WithEnableSourceSync Enabled)

* When `WithEnableSourceSync(true)` is enabled, Knowledge **keeps vector store fully consistent with configured Sources**:

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge"
)

kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
    knowledge.WithEnableSourceSync(true), // Enable sync mode
)

if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load: %v", err)
}
```

**Operations That Trigger Sync**

With sync mode enabled, the following operations trigger sync validation:

| Operation | Sync Behavior |
|-----------|---------------|
| `Load()` | Full sync: Detect changes in all Sources, clean up orphan documents |
| `ReloadSource()` | Single source sync: Detect changes in specified Source, load changes, clean up orphan documents for that Source |
| `RemoveSource()` | Delete sync: Precisely delete all documents for specified Source, update cache state |
| `AddSource()` | Incremental sync: Detect and load document changes in new Source, update cache state (no orphan cleanup triggered) |


**Sync Mode Behavior**:

1. **Pre-load Preparation**: Refresh document info cache, establish sync state tracking
2. **Intelligent Incremental Processing**: Detect document changes, only process new or modified documents
3. **Post-load Cleanup**: Automatically delete orphan documents no longer belonging to any configured Source

### Mode Comparison

| Feature | Default Mode | Sync Mode |
|---------|--------------|-----------|
| New Document Processing | ✅ Append | ✅ Append |
| Change Detection | ❌ Not detected | ✅ Auto-detect and update |
| Orphan Cleanup | ❌ Not cleaned | ✅ Auto cleanup |
| Data Consistency | ❌ May be inconsistent | ✅ Consistent with Sources |
| Performance Overhead | Lower | Slightly higher (state comparison needed) |

> ⚠️ **Important Warning**: When sync mode is enabled, **ensure all Sources that need to be retained are correctly configured**. The sync mechanism compares configured Sources with data in vector store, **any documents not belonging to configured Sources will be treated as orphans and deleted**.
>
> ```go
> import (
>     "trpc.group/trpc-go/trpc-agent-go/knowledge"
>     "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
> )
>
> // ❌ Dangerous: Empty Source configuration will delete all existing documents!
> kb := knowledge.New(
>     knowledge.WithEmbedder(embedder),
>     knowledge.WithVectorStore(vectorStore),
>     knowledge.WithSources([]source.Source{}), // Empty configuration
>     knowledge.WithEnableSourceSync(true),
> )
> kb.Load(ctx) // All documents will be cleaned up!
>
> // ✅ Correct: Ensure all needed Sources are configured
> kb := knowledge.New(
>     knowledge.WithEmbedder(embedder),
>     knowledge.WithVectorStore(vectorStore),
>     knowledge.WithSources([]source.Source{source1, source2, source3}), // Complete configuration
>     knowledge.WithEnableSourceSync(true),
> )
> kb.Load(ctx) // Safe: Only clean up documents not belonging to these Sources
> ```

## Dynamic Source Management

Knowledge supports runtime dynamic management of knowledge sources, ensuring vector store data always stays consistent with user-configured sources:

```go
import (
    "log"

    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

// Add new knowledge source - data will sync with configured sources
newSource := filesource.New([]string{"./new-docs/api.md"})
if err := kb.AddSource(ctx, newSource); err != nil {
    log.Printf("Failed to add source: %v", err)
}

// Reload specified knowledge source - auto-detect changes and sync
if err := kb.ReloadSource(ctx, newSource); err != nil {
    log.Printf("Failed to reload source: %v", err)
}

// Remove specified knowledge source - precisely delete related documents
if err := kb.RemoveSource(ctx, "API Documentation"); err != nil {
    log.Printf("Failed to remove source: %v", err)
}
```

## Knowledge Base Status Monitoring

Knowledge provides rich status monitoring functionality to help users understand the current sync status of configured sources:

```go
import (
    "fmt"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge"
)

// Show all document information
docInfos, err := kb.ShowDocumentInfo(ctx)
if err != nil {
    log.Printf("Failed to show document info: %v", err)
    return
}

// Filter by source name
docInfos, err = kb.ShowDocumentInfo(ctx,
    knowledge.WithShowDocumentInfoSourceName("APIDocumentation"))
if err != nil {
    log.Printf("Failed to show source documents: %v", err)
    return
}

// Filter by document IDs
docInfos, err = kb.ShowDocumentInfo(ctx,
    knowledge.WithShowDocumentInfoIDs([]string{"doc1", "doc2"}))
if err != nil {
    log.Printf("Failed to show specific documents: %v", err)
    return
}

// Iterate and display document information
for _, docInfo := range docInfos {
    fmt.Printf("Document ID: %s\n", docInfo.DocumentID)
    fmt.Printf("Source: %s\n", docInfo.SourceName)
    fmt.Printf("URI: %s\n", docInfo.URI)
    fmt.Printf("Chunk Index: %d\n", docInfo.ChunkIndex)
}
```

**Status Monitoring Output Example**

```
Document ID: a1b2c3d4e5f6...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 0

Document ID: f6e5d4c3b2a1...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 1
```
