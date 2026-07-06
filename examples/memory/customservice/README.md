# Custom memory.Service example

This example shows how to implement a minimal `memory.Service` adapter outside the
`memory/` tree while reusing `memory/memoryutils` for canonical ID and metadata
handling.

It is **not** a replacement for [`memory/inmemory`](../../memory/inmemory): the
built-in service includes tool wiring, auto-memory extraction, and richer search.
Use this pattern when you need your own persistence layer (SQL, ORM, proprietary
store) but want the same identity semantics as first-party backends.

## What it demonstrates

| Path | `memoryutils` helper |
| ---- | -------------------- |
| `AddMemory` | `ApplyMetadata`, `GenerateMemoryID` |
| `UpdateMemory` | `ApplyMemoryUpdate` (including ID rotation) |
| `ReadMemories` / `SearchMemories` | `NormalizeMemory`, `EffectiveKind` |

## Run

```bash
cd examples/memory/customservice
go run ./demo
```

Expected output (IDs vary with content):

```text
stored memories: 2
episode match: id=... content="team lunch downtown"
```

## Tests

```bash
cd examples/memory/customservice
go test .
```

Covers idempotent add, update ID rotation, and kind-filtered search.

## Wire into an agent

Pass `customservice.NewMapService()` anywhere a `memory.Service` is accepted
(for example `runner.WithMemoryService`). For a full chat demo with tools and
LLM integration, see [`examples/memory/simple`](../simple/README.md).

## Further reading

- [`memory/README.md`](../../memory/README.md) — custom adapter overview
- [`memory/memoryutils`](../../memory/memoryutils) — exported helpers
