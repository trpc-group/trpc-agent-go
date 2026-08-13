# Usage and Configuration

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

## Full Example

Below is a complete interactive chat example demonstrating memory capabilities in action.

### Run the Example

```bash
# View help
cd examples/memory/simple
go run main.go -h

# Use default config (inmemory + streaming)
go run main.go

# Use Redis storage
export REDIS_ADDR=localhost:6379
go run main.go -memory redis

# Use MySQL storage (with soft delete)
export MYSQL_HOST=localhost
export MYSQL_PASSWORD=password
go run main.go -memory mysql -soft-delete

# Use MySQL Vector storage
export MYSQLVEC_HOST=localhost
export MYSQLVEC_PASSWORD=password
go run main.go -memory mysqlvec -soft-delete

# Use PostgreSQL storage
export PG_HOST=localhost
export PG_PASSWORD=password
go run main.go -memory postgres -soft-delete

# Use pgvector storage
export PGVECTOR_HOST=localhost
export PGVECTOR_PASSWORD=password
go run main.go -memory pgvector -soft-delete

# Non-streaming mode
go run main.go -streaming=false
```

### Interactive Demo

```bash
$ go run main.go
🧠 Simple Memory Chat
Model: deepseek-v4-flash
Memory Service: inmemory
In-memory
Streaming: true
Available tools: memory_add, memory_update, memory_search, memory_load
(memory_delete, memory_clear disabled by default, and can be enabled or customized)
==================================================
✅ Memory chat ready! Session: memory-session-1765504626

💡 Special commands:
   /memory   - Show user memories
   /new      - Start a new session
   /exit     - End the conversation

👤 You: Hi, my name is John and I like coffee.
🤖 Assistant: Hi John! Nice to meet you. I've made a note that you like coffee. It's great to know your preferences - I'll remember this for our future conversations. Is there anything specific about coffee that you enjoy, or anything else you'd like me to know about you?
🔧 Memory tool calls initiated:
   • memory_add (ID: call_00_wE9FAqaLEPtWcqgF3tQqRoLn)
     Args: {"memory": "John likes coffee.", "topics": ["preferences", "food-drink"]}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_00_wE9FAqaLEPtWcqgF3tQqRoLn): {"message":"Memory added successfully","memory":"John likes coffee.","topics":["preferences","food-drink"]}
I see you're a coffee enthusiast! What brings you here today, John? Are you looking for coffee recommendations, or is there something else I can help you with?

👤 You: /new
🆕 Started new memory session!
   Previous: memory-session-1765504626
   Current:  memory-session-1765504664
   (Conversation history has been reset, memories are preserved)

👤 You: What do I like?
🤖 Assistant: I'll search through my memories to recall what you like. Let me check what information I have stored about your preferences.
🔧 Memory tool calls initiated:
   • memory_search (ID: call_00_CCn57ylCDDQ7iaL88d2JScvl)
     Args: {"query": "likes preferences favorite enjoy"}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_00_CCn57ylCDDQ7iaL88d2JScvl): {"query":"likes preferences favorite enjoy","results":[{"id":"47f1de6c1318d41001a17a46ebb9f9984b6e89e5ac549aedbf34d7744e8862e0","memory":"John likes coffee.","topics":["preferences","food-drink"],"created":"2025-12-12T09:57:12.456153047+08:00"}],"count":1}
Based on my memories, I know that **you like coffee**. That's the only preference I have recorded so far.

To give you a more complete answer about your likes, I'd need to learn more about you! Could you tell me about some of your other interests, hobbies, or preferences? For example:
- What foods or drinks do you enjoy?
- What hobbies or activities do you like?
- What kind of music, movies, or books do you prefer?
- Are there any particular topics or subjects you're interested in?

The more you share with me, the better I'll be able to remember and help you in the future!

👤 You: /exit
👋 Goodbye!
```

### Code Example

For full code, see [examples/memory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory). Core implementation:

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
    memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    var (
        memType    = flag.String("memory", "inmemory", "Memory service type")
        streaming  = flag.Bool("streaming", true, "Enable streaming")
        softDelete = flag.Bool("soft-delete", false, "Enable soft delete")
        modelName  = flag.String("model", "deepseek-v4-flash", "Model name")
    )
    flag.Parse()

    ctx := context.Background()

    // 1. Create memory service
    memoryService, err := createMemoryService(*memType, *softDelete)
    if err != nil {
        log.Fatalf("Failed to create memory service: %v", err)
    }

    // 2. Create model
    modelInstance := openai.New(*modelName)

    // 3. Create Agent
    genConfig := model.GenerationConfig{
        MaxTokens:   intPtr(2000),
        Temperature: floatPtr(0.7),
        Stream:      *streaming,
    }

    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription(
            "A helpful AI assistant with memory capabilities. "+
            "I can remember important information about you and "+
            "recall it when needed.",
        ),
        llmagent.WithGenerationConfig(genConfig),
        llmagent.WithTools(memoryService.Tools()),
    )

    // 4. Create Runner
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService),
    )
    defer appRunner.Close()

    // 5. Run chat
    log.Println("🧠 Starting memory-enabled chat...")
    // ... handle user input and responses
}

func createMemoryService(memType string, softDelete bool) (
    memory.Service, error) {

    switch memType {
    case "redis":
        redisAddr := os.Getenv("REDIS_ADDR")
        if redisAddr == "" {
            redisAddr = "localhost:6379"
        }
        return memoryredis.NewService(
            memoryredis.WithRedisClientURL(
                fmt.Sprintf("redis://%s", redisAddr),
            ),
            memoryredis.WithToolEnabled(memory.DeleteToolName, false),
        )

    case "mysql":
        dsn := buildMySQLDSN()
        return memorymysql.NewService(
            memorymysql.WithMySQLClientDSN(dsn),
            memorymysql.WithSoftDelete(softDelete),
            memorymysql.WithToolEnabled(memory.DeleteToolName, false),
        )

    case "postgres":
        return memorypostgres.NewService(
            memorypostgres.WithHost(getEnv("PG_HOST", "localhost")),
            memorypostgres.WithPort(getEnvInt("PG_PORT", 5432)),
            memorypostgres.WithUser(getEnv("PG_USER", "postgres")),
            memorypostgres.WithPassword(getEnv("PG_PASSWORD", "")),
            memorypostgres.WithDatabase(getEnv("PG_DATABASE", "trpc-agent-go-pgmemory")),
            memorypostgres.WithSoftDelete(softDelete),
            memorypostgres.WithToolEnabled(memory.DeleteToolName, false),
        )

    default: // inmemory
        return memoryinmemory.NewMemoryService(
            memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
        ), nil
    }
}

func buildMySQLDSN() string {
    host := getEnv("MYSQL_HOST", "localhost")
    port := getEnv("MYSQL_PORT", "3306")
    user := getEnv("MYSQL_USER", "root")
    password := getEnv("MYSQL_PASSWORD", "")
    database := getEnv("MYSQL_DATABASE", "trpc_agent_go")

    return fmt.Sprintf(
        "%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
        user, password, host, port, database,
    )
}

func getEnv(key, defaultVal string) string {
    if val := os.Getenv(key); val != "" {
        return val
    }
    return defaultVal
}

func intPtr(i int) *int             { return &i }
func floatPtr(f float64) *float64   { return &f }
```

## Advanced Configuration

### Auto Mode Configuration Options

| Option                     | Description                            | Default        |
| -------------------------- | -------------------------------------- | -------------- |
| `WithExtractor(extractor)` | Enable auto mode with LLM extractor    | nil (disabled) |
| `WithAsyncMemoryNum(n)`    | Number of background worker goroutines | 1              |
| `WithMemoryQueueSize(n)`   | Size of memory job queue               | 10             |
| `WithMemoryJobTimeout(d)`  | Timeout for each extraction job        | 30s            |

### Extraction Checkers

Checkers control when memory extraction should be triggered. By default, extraction happens on every conversation turn. Use checkers to optimize extraction frequency and reduce LLM costs.

#### Available Checkers

| Checker                 | Description                                               | Example                                          |
| ----------------------- | --------------------------------------------------------- | ------------------------------------------------ |
| `CheckMessageThreshold` | Triggers when accumulated messages exceed threshold       | `CheckMessageThreshold(5)` - when messages > 5   |
| `CheckTimeInterval`     | Triggers when time since last extraction exceeds interval | `CheckTimeInterval(3*time.Minute)` - every 3 min |
| `ChecksAll`             | Combines checkers with AND logic                          | All checkers must pass                           |
| `ChecksAny`             | Combines checkers with OR logic                           | Any checker passing triggers extraction          |

#### Checker Configuration Examples

```go
// Example 1: Extract when messages > 5 OR every 3 minutes (OR logic).
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithCheckersAny(
        extractor.CheckMessageThreshold(5),
        extractor.CheckTimeInterval(3*time.Minute),
    ),
)

// Example 2: Extract when messages > 10 AND every 5 minutes (AND logic).
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithChecker(extractor.CheckMessageThreshold(10)),
    extractor.WithChecker(extractor.CheckTimeInterval(5*time.Minute)),
)
```

#### Model callbacks (before/after)

The extractor also supports injecting before/after model callbacks via `model.Callbacks` (structured only). This is useful for tracing, request rewriting, or short-circuiting the model call in tests.

```go
callbacks := model.NewCallbacks().RegisterBeforeModel(
    func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // You can modify args.Request or return CustomResponse.
        return nil, nil
    },
).RegisterAfterModel(
    func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // You can inspect/override args.Response.
        return nil, nil
    },
)

memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithModelCallbacks(callbacks),
)
```

#### ExtractionContext

The `ExtractionContext` provides information for checker decisions:

```go
type ExtractionContext struct {
    UserKey       memory.UserKey  // User identifier.
    Messages      []model.Message // Accumulated messages since last extraction.
    LastExtractAt *time.Time      // Last extraction timestamp, nil if never extracted.
}
```

**Note**: `Messages` contains all accumulated messages since the last successful extraction. When a checker returns `false`, messages are accumulated and will be included in the next extraction. This ensures no conversation context is lost when using turn-based or time-based checkers.

### Tool Control

In auto extraction mode, `WithToolEnabled` controls whether each tool is
available. `memory_search` is exposed through `Tools()` by default,
`memory_load` is exposed once enabled, and `WithAutoMemoryExposedTools`
selectively exposes enabled write tools for hybrid usage.

**Front-end Tools** (agent-facing tools returned by `Tools()`):

| Tool            | Agent-facing default | Description                   |
| --------------- | -------------------- | ----------------------------- |
| `memory_search` | ✅ Exposed           | Search memories by query      |
| `memory_load`   | ❌ Not exposed       | Load all or recent N memories; exposed once enabled |

**Back-end Operations** (operation availability for the extractor):

| Tool            | Operation default | Agent-facing default | Description                            |
| --------------- | ----------------- | -------------------- | -------------------------------------- |
| `memory_add`    | ✅ On             | ❌ Hidden            | Add new memories                       |
| `memory_update` | ✅ On             | ❌ Hidden            | Update existing memories               |
| `memory_delete` | ✅ On             | ❌ Hidden            | Delete memories                        |
| `memory_clear`  | ❌ Off            | ❌ Hidden            | Clear all user memories (dangerous)    |

**Configuration Examples**:

```go
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    // Front-end: enable memory_load for agent to call.
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    // Hybrid: expose memory_add so the agent can store critical facts immediately.
    memoryinmemory.WithAutoMemoryExposedTools(memory.AddToolName),
    // Back-end: disable memory_delete so extractor cannot delete.
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
    // Back-end: enable memory_clear for extractor (use with caution).
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

**Note**: `WithToolEnabled` and `WithAutoMemoryExposedTools` can be called before or after
`WithExtractor` - the order does not matter.

### Comparison: Agentic Mode vs Auto Mode

| Tool            | Agentic Mode (no extractor)             | Auto Mode (with extractor)                 |
| --------------- | --------------------------------------- | ------------------------------------------ |
| `memory_add`    | ✅ Agent calls via `Tools()`            | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background |
| `memory_update` | ✅ Agent calls via `Tools()`            | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background |
| `memory_search` | ✅ Agent calls via `Tools()`            | ✅ Agent calls via `Tools()`               |
| `memory_load`   | ✅ Agent calls via `Tools()`            | ⚙️ Agent calls via `Tools()` if enabled    |
| `memory_delete` | ⚙️ Agent calls via `Tools()` if enabled | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background |
| `memory_clear`  | ⚙️ Agent calls via `Tools()` if enabled | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background if enabled |

### Memory Preloading

Both modes support preloading memories into the system prompt:

```go
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),
    // Preload options:
    // llmagent.WithPreloadMemory(0),   // Disable preloading (default).
    // llmagent.WithPreloadMemory(10),  // Adaptive preload budget 10.
    //                                  // Loads all memories when count <= 10,
    //                                  // otherwise injects top 10 search results.
    // llmagent.WithPreloadMemory(-1),  // Load all.
    //                                  // ⚠️ WARNING: Loading all memories may significantly
    //                                  //     increase token usage and API costs, especially
    //                                  //     for users with many stored memories. Consider
    //                                  //     using a positive budget for production use.
    // llmagent.WithPreloadMemory(10),  // Recommended production setting.
)
```

When preloading is enabled, memories are automatically injected into the
system prompt, giving the Agent context about the user without explicit
tool calls.

When `WithPreloadMemory(N)` uses a positive value, the framework first probes
how many memories the user has. If the count is at most `N`, it injects all
memories. If the count is larger than `N`, it switches to query-aware
`memory_search` behavior internally and injects only the top `N` relevant
results for the current user message. If query extraction is empty, the
search fails, or the search returns no matches, it falls back to directly
loading up to `N` memories.

**Injection Mechanism**: Preloaded memories are **merged** into the existing
system prompt rather than inserted as a separate system message. This ensures
the request always contains a single system message, maintaining compatibility
with models that have limited support for multiple system messages (e.g.,
Qwen3.5 series may return "System message must be at the beginning" error).

**⚠️ Important Note**: Setting the configuration to `-1` loads all memories,
which may significantly increase **Token Usage** and **API Costs**. By default,
preloading is disabled (`0`), and we recommend using positive budgets (e.g., `10-50`)
to balance performance and cost.

### Hybrid Approach

You can combine both approaches:

1. Use Auto mode for passive learning (background extraction)
2. Enable search tool for explicit memory queries
3. Preload memories for immediate context

```go
// Auto extraction + search tool + preloading.
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(extractor),
)

llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),  // Search by default; Load is optional.
    llmagent.WithPreloadMemory(10),             // Adaptive preload budget.
)
```
