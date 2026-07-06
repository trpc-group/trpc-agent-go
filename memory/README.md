# Memory package

The `memory` package defines the `memory.Service` interface and shared types for agent long-term memory. Built-in persistence backends live in subpackages (`postgres`, `mysql`, `redis`, `sqlite`, `inmemory`, `pgvector`, and others).

## Implementing a custom `memory.Service`

`memory.Service` is a public extension point for custom persistence (for example GORM/SQL adapters or proprietary stores). Identity and metadata helpers used by all built-in backends live in `memory/internal/memory`, which **cannot** be imported from outside this module.

Use `memory/memoryutils` on add and update paths so external backends share the same canonical behavior as in-tree services:

| Helper | Use on |
| ------ | ------ |
| `ApplyMetadata` | `AddMemory` — merge episodic metadata from `WithMetadata` options |
| `ApplyMetadataPatch` | `UpdateMemory` — merge metadata fields; zero values mean "leave unchanged" |
| `GenerateMemoryID` | `AddMemory` — derive stable row key from content, metadata, `appName`, and `userID` |
| `ApplyMemoryUpdate` | `UpdateMemory` — apply content/topics/metadata and return the new ID |
| `EffectiveKind` | `SearchMemories` / reads — interpret legacy records without persisted kind |
| `NormalizeMemory` | Before persisting denormalized rows |

### Minimal add path

```go
memObj := &memory.Memory{
    Memory:      content,
    Topics:      topics,
    LastUpdated: &now,
}
memoryutils.ApplyMetadata(memObj, memory.ResolveAddOptions(opts))
id := memoryutils.GenerateMemoryID(memObj, userKey.AppName, userKey.UserID)
// persist id, content, topics, memObj.Kind, episodic fields...
```

### Why this matters

Memory IDs are derived from content and episodic metadata. Duplicating or approximating `internal/memory` logic in an external adapter can produce different IDs than built-in backends, breaking idempotent `AddMemory`, `UpdateMemory` key rotation, and interoperability with auto-memory extraction.

All first-party backends call `memoryutils` (or the same internal helpers) so custom implementations stay compatible.

## Examples

Runnable memory examples (simple, auto, mem0, tencentdb) live under [`examples/memory`](../examples/memory/README.md).
