# Usage Guide

## Integrate with Agent

Use a **two-step approach** to integrate the Memory Service with an Agent:

1. **Register tools**: Use `llmagent.WithTools(memoryService.Tools())` to register memory tools with the Agent
2. **Set service**: Use `runner.WithMemoryService(memoryService)` to set the memory service in the Runner

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Step 1: Create memory service
memoryService := memoryinmemory.NewMemoryService()

// Step 2: Create Agent and register memory tools
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An assistant with memory capabilities."),
    llmagent.WithTools(memoryService.Tools()), // Explicitly register tools
)

// Step 3: Create Runner and set memory service
appRunner := runner.NewRunner(
    "memory-chat",
    llmAgent,
    runner.WithMemoryService(memoryService), // Set service at Runner level
)
```

## Memory Service

Configure the memory service in code. Nine built-in backends are supported:
in-memory, SQLite, SQLiteVec, Redis, MySQL, MySQL Vec, PostgreSQL, pgvector,
and ChromaDB.

### Configuration Example

```go
import (
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
    memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

// In-memory implementation for development and testing.
memService := memoryinmemory.NewMemoryService()

// Redis implementation for production.
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithToolEnabled(memory.DeleteToolName, true), // Enable delete.
)
if err != nil {
    // Handle error.
}

// MySQL implementation for production (relational database).
// Table is automatically created on service initialization (unless skipped). Returns error on failure.
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysql.WithToolEnabled(memory.DeleteToolName, true), // Enable delete.
)
if err != nil {
    // Handle error.
}

// PostgreSQL implementation for production (relational database).
// Table is automatically created on service initialization (unless skipped). Returns error on failure.
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/dbname?sslmode=disable"),
    memorypostgres.WithSoftDelete(true), // Enable soft delete.
    memorypostgres.WithToolEnabled(memory.DeleteToolName, true), // Enable delete.
)
if err != nil {
    // Handle error.
}

// Register memory tools with the Agent.
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithTools(memService.Tools()), // Or redisService.Tools(), mysqlService.Tools(), or postgresService.Tools().
)

// Set memory service in the Runner.
runner := runner.NewRunner(
    "app",
    llmAgent,
    runner.WithMemoryService(memService), // Or redisService, mysqlService, or postgresService.
)
```

## Memory Tool Configuration

The memory service provides 6 tools. In Agentic mode, common tools are enabled
by default and dangerous operations require manual enabling. In Auto mode,
extractor operation availability and agent-facing tool exposure are controlled
separately.

### Tool List

| Tool            | Function       | Agentic Mode    | Auto Extraction Mode | Description                                    |
| --------------- | -------------- | --------------- | -------------------- | ---------------------------------------------- |
| `memory_add`    | Add new memory | ✅ Default      | ✅ Enabled for extractor; hidden from agent by default | Create new memory entry                        |
| `memory_update` | Update memory  | ✅ Default      | ✅ Enabled for extractor; hidden from agent by default | Modify existing memory                         |
| `memory_search` | Search memory  | ✅ Default      | ✅ Enabled and exposed by default           | Find by keywords                               |
| `memory_load`   | Load memories  | ✅ Default      | ⚙️ Disabled by default; exposed once enabled | Load recent memories                           |
| `memory_delete` | Delete memory  | ⚙️ Configurable | ✅ Enabled for extractor; hidden from agent by default | Delete single memory                           |
| `memory_clear`  | Clear memories | ⚙️ Configurable | ⚙️ Disabled by default | Delete all memories                          |

**Notes**:

- **Agentic Mode**: Agent actively calls tools to manage memory, all tools are configurable
  - Default enabled tools: `memory_add`, `memory_update`, `memory_search`, `memory_load`
  - Default disabled tools: `memory_delete`, `memory_clear`
- **Auto Mode**: LLM extractor handles enabled write operations in background. `Tools()` exposes Search by default; Load is exposed once enabled; `WithAutoMemoryExposedTools()` can selectively expose enabled write tools for hybrid usage.
  - Default enabled tools: `memory_add`, `memory_update`, `memory_delete`, `memory_search`
  - Default disabled tools: `memory_load`, `memory_clear`
  - Enabled but not returned by `Tools()` by default: `memory_add`, `memory_update`, `memory_delete`
- **Default**: Available immediately when service is created, no extra configuration needed
- **Configurable**: Can be enabled/disabled via `WithToolEnabled()`; in Auto mode, enabled write tools can be exposed via `WithAutoMemoryExposedTools()`

### Enable/Disable Tools

Note: `WithToolEnabled()` controls whether a memory operation is available at
all. `WithAutoMemoryExposedTools()` controls which enabled tools are returned
from `Tools()` for the Agent to call in Auto mode. Write tools remain hidden by
default unless you expose them explicitly.

```go
// Scenario 1: User manageable (allow single deletion)
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
)

// Scenario 2: Admin privileges (allow clearing all)
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)

// Scenario 3: Read-only assistant (query only)
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
    memoryinmemory.WithToolEnabled(memory.UpdateToolName, false),
)

// Scenario 4: Hybrid auto memory + explicit agent writes
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    memoryinmemory.WithAutoMemoryExposedTools(memory.AddToolName),
)
```

## Overwrite Semantics (IDs and duplicates)

- Memory IDs are generated from memory content + appName + userID + canonical
  episodic metadata. Topics are intentionally excluded, so changing tags does
  not create a new memory. Adding the same content and identity metadata for the
  same user is idempotent and overwrites the existing entry (not append).
  UpdatedAt is refreshed. If that canonical ID belongs to a soft-deleted row,
  `AddMemory` reactivates it.
- If you need append semantics or different duplicate-handling strategies, you can
  implement custom tools or extend the service with policy options (e.g. allow/overwrite/ignore).

## Update Semantics and ID Rotation

`UpdateMemory` first applies the requested content, topics, and episodic
metadata, then recalculates the canonical memory ID. Topics are not part of the
ID, so a topics-only update stays on the same ID.

The operation follows this state machine:

| State after applying the update | Result |
| ------------------------------- | ------ |
| Source is missing or soft-deleted | Return a not-found error without changing `UpdateResult` |
| Canonical ID is unchanged | Update the active source in place |
| New ID does not exist | Create the target and retire the source |
| New ID is soft-deleted | Reactivate the target; hard-delete mode replaces the stale tombstone |
| New ID is already active | Return a conflict error without modifying either record |

For backends with soft deletion enabled, a successful ID rotation preserves
the old source as a tombstone. With hard deletion, the old source is removed.
SQL backends perform target preparation and source retirement atomically.

Timestamp behavior is also stable across SQL backends:

- A newly inserted target inherits the source `CreatedAt`.
- A reactivated target preserves its own `CreatedAt`.
- A hard-delete replacement of a stale target inherits the source `CreatedAt`.
- Every successful update refreshes `UpdatedAt`.

On success, `UpdateResult.MemoryID` receives the effective canonical ID. On
error, the caller-provided result remains unchanged.

## Custom Tool Implementation

Note: In Auto mode, `Tools()` exposes `memory_search` by default, `memory_load`
when enabled, and any additional enabled tools you explicitly expose with
`WithAutoMemoryExposedTools()`. Dangerous operations like `memory_clear` should usually
stay application-controlled.

You can override default tools with custom implementations. See
`memory/tool/tool.go` for reference on how to implement custom tools.

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    toolmemory "trpc.group/trpc-go/trpc-agent-go/memory/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// A custom clear tool with real logic using the invocation context.
func customClearMemoryTool() tool.Tool {
    clearFunc := func(ctx context.Context, _ *toolmemory.ClearMemoryRequest) (*toolmemory.ClearMemoryResponse, error) {
        // Get memory service and user info from invocation context.
        memSvc, err := toolmemory.GetMemoryServiceFromContext(ctx)
        if err != nil {
            return nil, fmt.Errorf("custom clear tool: %w", err)
        }
        appName, userID, err := toolmemory.GetAppAndUserFromContext(ctx)
        if err != nil {
            return nil, fmt.Errorf("custom clear tool: %w", err)
        }

        if err := memSvc.ClearMemories(ctx, memory.UserKey{AppName: appName, UserID: userID}); err != nil {
            return nil, fmt.Errorf("custom clear tool: failed to clear memories: %w", err)
        }
        return &toolmemory.ClearMemoryResponse{Message: "🎉 All memories cleared successfully!"}, nil
    }

    return function.NewFunctionTool(
        clearFunc,
        function.WithName(memory.ClearToolName),
        function.WithDescription("Clear all memories for the user."),
    )
}

// Register the custom tool with an InMemory service.
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)
```
