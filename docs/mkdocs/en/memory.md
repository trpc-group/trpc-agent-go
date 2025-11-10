# Memory Usage Guide

## Overview

Memory is the memory management system in the tRPC-Agent-Go framework. It
provides persistent memory and context management for Agents. By integrating
the memory service, session management, and memory tools, the Memory system
helps Agents remember user information, maintain conversation context, and
offer personalized responses across multi-turn dialogs.

## ⚠️ Breaking Changes Notice

**Important**: The memory integration approach has been updated to provide better separation of concerns and explicit control. This is a **breaking change** that affects how memory services are integrated with Agents.

### What Changed

- **Removed**: `llmagent.WithMemory(memoryService)` - automatic memory tool registration
- **Added**: Two-step integration approach:
  1. `llmagent.WithTools(memoryService.Tools())` - manual tool registration
  2. `runner.WithMemoryService(memoryService)` - service management in runner

### Migration Guide

**Before (old approach)**:

```go
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithMemory(memoryService), // ❌ No longer supported
)
```

**After (new approach)**:

```go
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithTools(memoryService.Tools()), // ✅ Step 1: Register tools
)

runner := runner.NewRunner(
    "app",
    llmAgent,
    runner.WithMemoryService(memoryService), // ✅ Step 2: Set service
)
```

### Benefits of the New Approach

- **Explicit Control**: Applications have full control over which tools to register
- **Better Separation**: Clear separation between framework and business logic
- **Service Management**: Memory service is managed at the appropriate level (runner)
- **No Automatic Injection**: Framework doesn't automatically inject tools or prompts, which can be used as needed.

### Usage Pattern

The Memory system follows this pattern:

1. Create the Memory Service: configure the storage backend (in-memory or
   Redis).
2. Register memory tools: manually register memory tools with the Agent using
   `llmagent.WithTools(memoryService.Tools())`.
3. Set memory service in runner: configure the memory service in the runner
   using `runner.WithMemoryService(memoryService)`.
4. Agent auto-invocation: the Agent manages memory automatically via registered
   memory tools.
5. Session persistence: memory persists across sessions and supports
   multi-turn dialogs.

This provides:

- Intelligent memory: automatic storage and retrieval based on conversation
  context.
- Multi-turn dialogues: maintain dialog state and memory continuity.
- Flexible storage: supports multiple backends such as in-memory and Redis.
- Tool integration: memory management tools are registered manually for explicit control.
- Session management: supports creating, switching, and resetting sessions.

### Agent Integration

Memory integrates with Agents as follows:

- Manual tool registration: memory tools are explicitly registered using
  `llmagent.WithTools(memoryService.Tools())`.
- Service management: memory service is managed at the runner level using
  `runner.WithMemoryService(memoryService)`.
- Tool invocation: the Agent uses memory tools to store, retrieve, and manage
  information.
- Explicit control: applications have full control over which tools to register
  and how to use them.

## Quick Start

### Requirements

- Go 1.21 or later.
- A valid LLM API key (OpenAI-compatible endpoint).
- Redis service (optional for production).

### Environment Variables

```bash
# OpenAI API configuration
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
```

### Minimal Example

```go
package main

import (
    "context"
    "log"

    // Core components.
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // 1. Create the memory service.
    memoryService := memoryinmemory.NewMemoryService()

    // 2. Create the LLM model.
    modelInstance := openai.New("deepseek-chat")

    // 3. Create the Agent and register memory tools.
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("An assistant with memory capabilities."),
        llmagent.WithInstruction(
            "Remember important user info and recall it when needed.",
        ),
        llmagent.WithTools(memoryService.Tools()), // Register memory tools.
    )

    // 4. Create the Runner with memory service.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService), // Set memory service.
    )

    // 5. Run a dialog (the Agent uses memory tools automatically).
    log.Println("🧠 Starting memory-enabled chat...")
    message := model.NewUserMessage(
        "Hi, my name is John, and I like programming",
    )
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 6. Handle responses ...
    _ = eventChan
}
```

## Core Concepts

The [memory module](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
is the core of tRPC-Agent-Go's memory management. It provides complete memory
storage and retrieval capabilities with a modular design that supports
multiple storage backends and memory tools.

```textplain
memory/
├── memory.go          # Core interface definitions.
├── inmemory/          # In-memory memory service implementation.
├── redis/             # Redis memory service implementation.
└── tool/              # Memory tools implementation.
    ├── tool.go        # Tool interfaces and implementations.
    └── types.go       # Tool type definitions.
```

## Usage Guide

### Integrate with Agent

Use a two-step approach to integrate the Memory Service with an Agent:

1. Register memory tools with the Agent using `llmagent.WithTools(memoryService.Tools())`
2. Set the memory service in the runner using `runner.WithMemoryService(memoryService)`

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Create the memory service.
memoryService := memoryinmemory.NewMemoryService()

// Create the Agent and register memory tools.
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An assistant with memory capabilities."),
    llmagent.WithInstruction(
        "Remember important user info and recall it when needed.",
    ),
    llmagent.WithTools(memoryService.Tools()), // Register memory tools.
)

// Create the runner with memory service.
appRunner := runner.NewRunner(
    "memory-chat",
    llmAgent,
    runner.WithMemoryService(memoryService), // Set memory service.
)
```

### Memory Service

Configure the memory service in code. Three backends are supported: in-memory,
Redis, MySQL, and PostgreSQL.

#### Configuration Example

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
// Table is automatically created on service initialization. Panics if creation fails.
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysql.WithToolEnabled(memory.DeleteToolName, true), // Enable delete.
)
if err != nil {
    // Handle error.
}

// PostgreSQL implementation for production (relational database).
// Table is automatically created on service initialization. Panics if creation fails.
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresConnString("postgres://user:password@localhost:5432/dbname"),
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

### Memory Tool Configuration

By default, the following tools are enabled. Others can be toggled via
configuration.

```go
// Default enabled tools: add, update, search, load.
// Default disabled tools: delete, clear.
memoryService := memoryinmemory.NewMemoryService()

// Enable disabled tools.
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)

// Disable enabled tools.
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
)
```

### Overwrite Semantics (IDs and duplicates)

- Memory IDs are generated from content + topics. Adding the same content and topics
  is idempotent and overwrites the existing entry (not append). UpdatedAt is refreshed.
- If you need append semantics or different duplicate-handling strategies, you can
  implement custom tools or extend the service with policy options (e.g. allow/overwrite/ignore).

### Custom Tool Implementation

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

Below is a complete example showing how to create an Agent with memory
capabilities.

```go
package main

import (
    "context"
    "flag"
    "log"
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    var (
        memServiceName = flag.String(
            "memory", "inmemory", "Memory service type (inmemory, redis)",
        )
        redisAddr = flag.String(
            "redis-addr", "localhost:6379", "Redis server address",
        )
        modelName = flag.String("model", "deepseek-chat", "Model name")
    )

    flag.Parse()

    ctx := context.Background()

    // 1. Create the memory service (based on flags).
    var memoryService memory.Service
    var err error

    switch *memServiceName {
    case "redis":
        redisURL := fmt.Sprintf("redis://%s", *redisAddr)
        memoryService, err = memoryredis.NewService(
            memoryredis.WithRedisClientURL(redisURL),
            memoryredis.WithToolEnabled(memory.DeleteToolName, true),
            memoryredis.WithCustomTool(
                memory.ClearToolName, customClearMemoryTool,
            ),
        )
        if err != nil {
            log.Fatalf("Failed to create redis memory service: %v", err)
        }
    default: // inmemory.
        memoryService = memoryinmemory.NewMemoryService(
            memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
            memoryinmemory.WithCustomTool(
                memory.ClearToolName, customClearMemoryTool,
            ),
        )
    }

    // 2. Create the LLM model.
    modelInstance := openai.New(*modelName)

    // 3. Create the Agent and register memory tools.
    genConfig := model.GenerationConfig{
        MaxTokens:   intPtr(2000),
        Temperature: floatPtr(0.7),
        Stream:      true,
    }

    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription(
            "An assistant with memory. I can remember key info about you "+
                "and recall it when needed.",
        ),
        llmagent.WithGenerationConfig(genConfig),
        llmagent.WithTools(memoryService.Tools()), // Register memory tools.
    )

    // 4. Create the Runner with memory service.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService), // Set memory service.
    )

    // 5. Run a dialog (the Agent uses memory tools automatically).
    log.Println("🧠 Starting memory-enabled chat...")
    message := model.NewUserMessage(
        "Hi, my name is John, and I like programming",
    )
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 6. Handle responses ...
    _ = eventChan
}

// Custom clear tool.
func customClearMemoryTool() tool.Tool {
    // ... implementation ...
    return nil
}

// Helpers.
func intPtr(i int) *int   { return &i }
func floatPtr(f float64) *float64 { return &f }
```

The environment variables are configured as follows:

```bash
# OpenAI API configuration
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
```

### Command-line Flags

```bash
# Choose components via flags when running the example.
go run main.go -memory inmemory
go run main.go -memory redis -redis-addr localhost:6379

# Flags:
# -memory: memory service type (inmemory, redis, mysql, postgres), default is inmemory.
# -redis-addr: Redis server address, default is localhost:6379.
# -mysql-dsn: MySQL Data Source Name (DSN), required when using MySQL.
# -postgres-dsn: PostgreSQL connection string, required when using PostgreSQL.
# -soft-delete: Enable soft delete for MySQL/PostgreSQL memory service (default false).
# -model: model name, default is deepseek-chat.
```

## Storage Backends

### In-Memory Storage

In-memory storage is suitable for development and testing:

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

// Create in-memory service
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithMemoryLimit(100), // Set memory limit
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true), // Enable delete tool
)
```

**Features:**

- ✅ Zero configuration, ready to use
- ✅ High performance, no network overhead
- ❌ No data persistence, lost on restart
- ❌ No distributed deployment support

### Redis Storage

Redis storage is suitable for production and distributed applications:

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

// Create Redis memory service
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithMemoryLimit(1000), // Set memory limit
    memoryredis.WithToolEnabled(memory.DeleteToolName, true), // Enable delete tool
)
if err != nil {
    log.Fatalf("Failed to create redis memory service: %v", err)
}
```

**Features:**

- ✅ Data persistence, survives restarts
- ✅ High performance for high concurrency
- ✅ Distributed deployment support
- ✅ Cluster and sentinel mode support
- ⚙️ Requires Redis server

**Redis Configuration Options:**

- `WithRedisClientURL(url string)`: Set Redis connection URL
- `WithRedisInstance(name string)`: Use pre-registered Redis instance
- `WithMemoryLimit(limit int)`: Set maximum memories per user
- `WithToolEnabled(toolName string, enabled bool)`: Enable or disable specific tools
- `WithCustomTool(toolName string, creator ToolCreator)`: Use custom tool implementation

### MySQL Storage

MySQL storage is suitable for production environments requiring relational databases:

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

// Create MySQL memory service
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysql.WithMemoryLimit(1000), // Set memory limit
    memorymysql.WithTableName("memories"), // Custom table name (optional)
    memorymysql.WithToolEnabled(memory.DeleteToolName, true), // Enable delete tool
)
if err != nil {
    log.Fatalf("Failed to create mysql memory service: %v", err)
}
```

**Features:**

- ✅ Data persistence with ACID transaction guarantees
- ✅ Relational database with complex query support
- ✅ Master-slave replication and clustering support
- ✅ Automatic table creation
- ✅ Comprehensive monitoring and management tools
- ⚙️ Requires MySQL server (5.7+ or 8.0+)

**MySQL Configuration Options:**

- `WithMySQLClientDSN(dsn string)`: Set MySQL Data Source Name (DSN)
- `WithMySQLInstance(name string)`: Use pre-registered MySQL instance
- `WithTableName(name string)`: Custom table name (default "memories"). Panics if invalid.
- `WithMemoryLimit(limit int)`: Set maximum memories per user
- `WithSoftDelete(enabled bool)`: Enable soft delete (default false). When enabled, delete operations set `deleted_at` and queries filter out soft-deleted rows.
- `WithToolEnabled(toolName string, enabled bool)`: Enable or disable specific tools
- `WithCustomTool(toolName string, creator ToolCreator)`: Use custom tool implementation

**Note:** The table is automatically created when the service is initialized. If table creation fails, the service will panic.

**DSN Format:**

```
[username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
```

**Common DSN Parameters:**

- `parseTime=true`: Parse DATE and DATETIME to time.Time (required)
- `charset=utf8mb4`: Character set
- `loc=Local`: Location for time.Time values
- `timeout=10s`: Connection timeout

**Table Schema:**

MySQL memory service automatically creates the following table structure on initialization:

```sql
CREATE TABLE IF NOT EXISTS memories (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    INDEX idx_app_user (app_name, user_id),
    UNIQUE INDEX idx_app_user_memory (app_name, user_id, memory_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
```

**Start MySQL with Docker:**

```bash
# Start MySQL container
docker run -d --name mysql-memory \
  -e MYSQL_ROOT_PASSWORD=password \
  -e MYSQL_DATABASE=memory_db \
  -p 3306:3306 \
  mysql:8.0

# Wait for MySQL to be ready
docker exec mysql-memory mysqladmin ping -h localhost -u root -ppassword

# Use MySQL memory service
go run main.go -memory mysql -mysql-dsn "root:password@tcp(localhost:3306)/memory_db?parseTime=true"
```

**Register MySQL Instance (Optional):**

```go
import (
    storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
    memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
)

// Register MySQL instance
storage.RegisterMySQLInstance("my-mysql",
    storage.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
)

// Use registered instance
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLInstance("my-mysql"),
)
```

### PostgreSQL Storage

PostgreSQL storage is suitable for production environments requiring relational databases with JSONB support:

```go
import memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"

// Create PostgreSQL memory service
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresConnString("postgres://user:password@localhost:5432/dbname"),
    memorypostgres.WithMemoryLimit(1000), // Set memory limit
    memorypostgres.WithTableName("memories"), // Custom table name (optional)
    memorypostgres.WithSoftDelete(true), // Enable soft delete
    memorypostgres.WithToolEnabled(memory.DeleteToolName, true), // Enable delete tool
)
if err != nil {
    log.Fatalf("Failed to create postgres memory service: %v", err)
}
```

**Features:**

- ✅ Data persistence with ACID transaction guarantees
- ✅ Relational database with complex query support
- ✅ JSONB support for efficient JSON operations
- ✅ Master-slave replication and clustering support
- ✅ Automatic table creation
- ✅ Comprehensive monitoring and management tools
- ✅ Optional soft delete support
- ⚙️ Requires PostgreSQL server (12+)

**PostgreSQL Configuration Options:**

- `WithPostgresConnString(connString string)`: Set PostgreSQL connection string
- `WithPostgresInstance(name string)`: Use pre-registered PostgreSQL instance
- `WithTableName(name string)`: Custom table name (default "memories"). Panics if invalid.
- `WithMemoryLimit(limit int)`: Set maximum memories per user
- `WithSoftDelete(enabled bool)`: Enable soft delete (default false). When enabled, delete operations set `deleted_at` and queries filter out soft-deleted rows.
- `WithToolEnabled(toolName string, enabled bool)`: Enable or disable specific tools
- `WithCustomTool(toolName string, creator ToolCreator)`: Use custom tool implementation

**Note:** The table is automatically created when the service is initialized. If table creation fails, the service will panic.

**Connection String Format:**

```
postgres://[user[:password]@][netloc][:port][/dbname][?param1=value1&...]
```

**Common Connection String Parameters:**

- `sslmode=disable`: Disable SSL (for local development)
- `sslmode=require`: Require SSL connection
- `connect_timeout=10`: Connection timeout in seconds

**Table Schema:**

PostgreSQL memory service automatically creates the following table structure on initialization:

```sql
CREATE TABLE IF NOT EXISTS memories (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    CONSTRAINT idx_app_user_memory UNIQUE (app_name, user_id, memory_id)
);

CREATE INDEX IF NOT EXISTS idx_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS idx_deleted_at ON memories(deleted_at);
```

**Start PostgreSQL with Docker:**

```bash
# Start PostgreSQL container
docker run -d --name postgres-memory \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=password \
  -e POSTGRES_DB=memory_db \
  -p 5432:5432 \
  postgres:15-alpine

# Wait for PostgreSQL to be ready
docker exec postgres-memory pg_isready -U postgres

# Use PostgreSQL memory service
go run main.go -memory postgres -postgres-dsn "postgres://postgres:password@localhost:5432/memory_db"
```

**Register PostgreSQL Instance (Optional):**

```go
import (
    storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

// Register PostgreSQL instance
storage.RegisterPostgresInstance("my-postgres",
    storage.WithClientConnString("postgres://user:password@localhost:5432/dbname"),
)

// Use registered instance
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresInstance("my-postgres"),
)
```

### Storage Backend Comparison

| Feature                  | In-Memory | Redis      | MySQL          | PostgreSQL     |
| ------------------------ | --------- | ---------- | -------------- | -------------- |
| Data Persistence         | ❌        | ✅         | ✅             | ✅             |
| Distributed Support      | ❌        | ✅         | ✅             | ✅             |
| Transaction Support      | ❌        | Partial    | ✅ (ACID)      | ✅ (ACID)      |
| Query Capability         | Simple    | Medium     | Powerful (SQL) | Powerful (SQL) |
| JSON Support             | ❌        | Partial    | ✅ (JSON)      | ✅ (JSONB)     |
| Performance              | Very High | High       | Medium-High    | Medium-High    |
| Configuration Complexity | Low       | Medium     | Medium         | Medium         |
| Use Case                 | Dev/Test  | Production | Production     | Production     |
| Monitoring Tools         | None      | Rich       | Very Rich      | Very Rich      |

**Selection Guide:**

- **Development/Testing**: Use in-memory storage for fast iteration
- **Production (High Performance)**: Use Redis storage for high concurrency scenarios
- **Production (Data Integrity)**: Use MySQL storage when ACID guarantees and complex queries are needed
- **Production (PostgreSQL)**: Use PostgreSQL storage when JSONB support and advanced PostgreSQL features are needed
- **Hybrid Deployment**: Choose different storage backends based on different application scenarios
