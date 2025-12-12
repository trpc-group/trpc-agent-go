# Memory Usage Guide

## Overview

The Memory module is the memory management system in the tRPC-Agent-Go
framework, providing Agents with persistent memory and context management
capabilities. By integrating memory services, session management, and memory
tools, the Memory system helps Agents remember user information, maintain
dialog context, and provide personalized response experiences across multiple
conversations.

### Positioning

Memory manages long-term user information with isolation dimension
`<appName, userID>`. It can be understood as a "personal profile" gradually
accumulated around a single user.

In cross-session scenarios, Memory enables the system to retain key user
information, avoiding repetitive information gathering in each session.

It is suitable for recording stable, reusable facts such as "user name is
John", "occupation is backend engineer", "prefers concise answers", "commonly
used language is English", and directly using this information in subsequent
interactions.

## Core Values

- **Context Continuity**: Maintain user history across sessions, avoiding
  repetitive questioning and input.
- **Personalized Service**: Provide customized responses and suggestions based
  on long-term user profiles and preferences.
- **Knowledge Accumulation**: Transform facts and experiences from
  conversations into reusable knowledge.
- **Persistent Storage**: Support multiple storage backends to ensure data
  safety and reliability.

## Use Cases

The Memory module is suitable for scenarios requiring cross-session user
information and context retention:

### Use Case 1: Personalized Customer Service Agent

**Requirement**: Customer service Agent needs to remember user information,
historical issues, and preferences for consistent service.

**Implementation**:
- First conversation: Agent uses `memory_add` to record name, company, contact
- Record user preferences like "prefers concise answers", "technical
  background"
- Subsequent sessions: Agent uses `memory_load` to load user info, no repeated
  questions needed
- After resolving issues: Use `memory_update` to update issue status

### Use Case 2: Learning Companion Agent

**Requirement**: Educational Agent needs to track student learning progress,
knowledge mastery, and interests.

**Implementation**:
- Use `memory_add` to record mastered knowledge points
- Use topic tags for categorization: `["math", "geometry"]`,
  `["programming", "Python"]`
- Use `memory_search` to query related knowledge, avoid repeated teaching
- Adjust teaching strategies based on memories, provide personalized learning
  paths

### Use Case 3: Project Management Agent

**Requirement**: Project management Agent needs to track project information,
team members, and task progress.

**Implementation**:
- Record key project info: `memory_add("Project X uses Go language",
  ["project", "tech-stack"])`
- Record team member roles: `memory_add("John Doe is backend lead",
  ["team", "role"])`
- Use `memory_search` to quickly find relevant information
- After project completion: Use `memory_clear` to clear temporary information

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

The memory service provides 6 tools. Common tools are enabled by default, while dangerous operations require manual enabling.

#### Tool List

| Tool | Function | Default | Description |
|------|----------|---------|-------------|
| `memory_add` | Add new memory | ✅ Enabled | Create new memory entry |
| `memory_update` | Update memory | ✅ Enabled | Modify existing memory |
| `memory_search` | Search memory | ✅ Enabled | Find by keywords |
| `memory_load` | Load memories | ✅ Enabled | Load recent memories |
| `memory_delete` | Delete memory | ❌ Disabled | Delete single memory |
| `memory_clear` | Clear memories | ❌ Disabled | Delete all memories |

#### Enable/Disable Tools

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

Below is a complete interactive chat example demonstrating memory capabilities in action.

### Run the Example

```bash
# View help
cd examples/memory
go run . -h

# Use default config (in-memory storage + streaming)
go run .

# Use Redis storage
export REDIS_ADDR=localhost:6379
go run . -memory redis

# Use MySQL storage (with soft delete)
export MYSQL_HOST=localhost
export MYSQL_PASSWORD=password
go run . -memory mysql -soft-delete

# Use PostgreSQL storage
export PG_HOST=localhost
export PG_PASSWORD=password
go run . -memory postgres -soft-delete

# Non-streaming mode
go run . -streaming=false
```

### Interactive Demo

```bash
$ go run .                                          
🧠 Multi Turn Chat with Memory
Model: deepseek-chat
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
   (Memory and conversation history have been reset)

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
        modelName  = flag.String("model", "deepseek-chat", "Model name")
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
            memorypostgres.WithDatabase(getEnv("PG_DATABASE", "postgres")),
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

## Storage Backends

### In-Memory Storage

**Use case**: Development, testing, rapid prototyping

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService()
```

**Configuration options**:
- `WithMemoryLimit(limit int)`: Set memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool implementation
- `WithToolEnabled(toolName, enabled)`: Enable/disable specific tool

**Features**: Zero config, high performance, no persistence

### Redis Storage

**Use case**: Production, high concurrency, distributed deployment

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
```

**Configuration options**:
- `WithRedisClientURL(url)`: Redis connection URL (recommended)
- `WithRedisInstance(name)`: Use pre-registered Redis instance
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to Redis client

**Note**: `WithRedisClientURL` takes priority over `WithRedisInstance`

### MySQL Storage

**Use case**: Production, ACID guarantees, complex queries

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

dsn := "user:password@tcp(localhost:3306)/dbname?parseTime=true"
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN(dsn),
    memorymysql.WithSoftDelete(true),
)
```

**Configuration options**:
- `WithMySQLClientDSN(dsn)`: MySQL DSN connection string (recommended, requires `parseTime=true`)
- `WithMySQLInstance(name)`: Use pre-registered MySQL instance
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to MySQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)

**DSN example**:
```
root:password@tcp(localhost:3306)/memory_db?parseTime=true&charset=utf8mb4
```

**Table schema** (auto-created):
```sql
CREATE TABLE memories (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    UNIQUE INDEX (app_name, user_id, memory_id)
)
```

**Resource cleanup**: Call `Close()` method to release database connection:
```go
defer mysqlService.Close()
```

### PostgreSQL Storage

**Use case**: Production, advanced JSONB features

```go
import memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"

postgresService, err := memorypostgres.NewService(
    memorypostgres.WithHost("localhost"),
    memorypostgres.WithPort(5432),
    memorypostgres.WithUser("postgres"),
    memorypostgres.WithPassword("password"),
    memorypostgres.WithDatabase("dbname"),
    memorypostgres.WithSoftDelete(true),
)
```

**Configuration options**:
- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: Connection parameters
- `WithSSLMode(mode)`: SSL mode (default "disable")
- `WithPostgresInstance(name)`: Use pre-registered PostgreSQL instance
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithSchema(schema)`: Specify database schema (default is public)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to PostgreSQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)

**Note**: Direct connection parameters take priority over `WithPostgresInstance`

**Table schema** (auto-created):
```sql
CREATE TABLE memories (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    UNIQUE (app_name, user_id, memory_id)
)
```

**Resource cleanup**: Call `Close()` method to release database connection:
```go
defer postgresService.Close()
```

### Backend Comparison

| Feature | InMemory | Redis | MySQL | PostgreSQL |
|---------|----------|-------|-------|------------|
| **Persistence** | ❌ | ✅ | ✅ | ✅ |
| **Distributed** | ❌ | ✅ | ✅ | ✅ |
| **Transactions** | ❌ | Partial | ✅ ACID | ✅ ACID |
| **Queries** | Simple | Medium | SQL | SQL |
| **JSON** | ❌ | Basic | JSON | JSONB |
| **Performance** | Very High | High | Med-High | Med-High |
| **Configuration** | Zero | Simple | Medium | Medium |
| **Soft Delete** | ❌ | ❌ | ✅ | ✅ |
| **Use Case** | Dev/Test | High Concurrency | Enterprise | Advanced Features |

**Selection guide**:

```
Development/Testing → InMemory (zero config, fast)
High Concurrency → Redis (memory-level performance)
ACID Requirements → MySQL/PostgreSQL (transaction guarantees)
Complex JSON → PostgreSQL (JSONB indexing and queries)
Audit Trail → MySQL/PostgreSQL (soft delete support)
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

## FAQ

### Difference between Memory and Session

Memory and Session solve different problems:

| Dimension | Memory | Session |
|-----------|--------|---------|
| **Purpose** | Long-term user profile | Temporary conversation context |
| **Isolation** | `<appName, userID>` | `<appName, userID, sessionID>` |
| **Lifecycle** | Persists across sessions | Valid within a single session |
| **Content** | User profile, preferences, facts | Conversation history, messages |
| **Data Size** | Small (tens to hundreds) | Large (tens to thousands of messages) |
| **Use Case** | "Remember who the user is" | "Remember what was said" |

**Example**:

```go
// Memory: persists across sessions
memory.AddMemory(ctx, userKey, "User is a backend engineer", []string{"occupation"})

// Session: valid only within a session
session.AddMessage(ctx, sessionKey, userMessage("What's the weather today?"))
session.AddMessage(ctx, sessionKey, agentMessage("It's sunny today"))

// New session: Memory retained, Session reset
```

### Memory ID Idempotency

Memory ID is generated from SHA256 hash of "content + topics". Same content produces the same ID:

```go
// First add
memory.AddMemory(ctx, userKey, "User likes programming", []string{"hobby"})
// Generated ID: abc123...

// Second add with same content
memory.AddMemory(ctx, userKey, "User likes programming", []string{"hobby"})
// Same ID: abc123..., overwrites, refreshes updated_at
```

**Implications**:
- ✅ **Natural deduplication**: Avoids redundant storage
- ✅ **Idempotent operations**: Repeated additions don't create multiple records
- ⚠️ **Overwrite update**: Cannot append same content (add timestamp or sequence number if append is needed)

### Search Function Limitations

Memory uses **token matching**, not semantic search:

**English tokenization**: lowercase → filter stopwords (a, the, is, etc.) → split by spaces

```go
// Can find
Memory: "User likes programming"
Search: "programming" ✅ Match

// Cannot find
Memory: "User likes programming"
Search: "coding" ❌ No match (semantically similar but different words)
```

**Chinese tokenization**: uses bigrams

```go
Memory: "用户喜欢编程"
Search: "编程" ✅ Match ("编程" in bigrams)
Search: "写代码" ❌ No match (different words)
```

**Limitations**:
- All backends perform filtering and sorting in **application layer** (O(n) complexity)
- Performance affected by data volume
- No semantic similarity search

**Recommendations**:
- Use explicit keywords and topic tags to improve hit rate
- Consider integrating vector database for semantic search (requires custom implementation)

### Soft Delete Considerations

**Support status**:
- ✅ MySQL, PostgreSQL: support soft delete
- ❌ InMemory, Redis: not supported (hard delete only)

**Soft delete configuration**:

```go
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("..."),
    memorymysql.WithSoftDelete(true), // Enable soft delete
)
```

**Behavior differences**:

| Operation | Hard Delete | Soft Delete |
|-----------|-------------|-------------|
| Delete | Immediate removal | Set `deleted_at` field |
| Query | Not visible | Auto-filtered (WHERE deleted_at IS NULL) |
| Recovery | Cannot recover | Can manually clear `deleted_at` |
| Storage | Saves space | Occupies space |

**Migration trap**:
```go
// ⚠️ Migrating from soft-delete backend to non-supporting backend
// Soft-deleted records will be lost!

// Migrating from MySQL (soft delete) to Redis (hard delete)
// Need to manually handle soft-deleted records
```

## Best Practices

### Production Environment Configuration

```go
// ✅ Recommended configuration
postgresService, err := memorypostgres.NewService(
    // Use environment variables for sensitive info
    memorypostgres.WithHost(os.Getenv("DB_HOST")),
    memorypostgres.WithUser(os.Getenv("DB_USER")),
    memorypostgres.WithPassword(os.Getenv("DB_PASSWORD")),
    memorypostgres.WithDatabase(os.Getenv("DB_NAME")),
    
    // Enable soft delete (for recovery)
    memorypostgres.WithSoftDelete(true),
    
    // Reasonable limit
    memorypostgres.WithMemoryLimit(1000),
)
```

### Error Handling

```go
// ✅ Complete error handling
err := memoryService.AddMemory(ctx, userKey, content, topics)
if err != nil {
    if strings.Contains(err.Error(), "limit exceeded") {
        // Handle limit: clean old memories or reject
        log.Warnf("Memory limit exceeded for user %s", userKey.UserID)
    } else {
        return fmt.Errorf("failed to add memory: %w", err)
    }
}
```

### Tool Enabling Strategy

```go
// Scenario 1: Read-only assistant
readOnlyService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    memoryinmemory.WithToolEnabled(memory.SearchToolName, true),
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
    memoryinmemory.WithToolEnabled(memory.UpdateToolName, false),
)

// Scenario 2: Regular user
userService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    // clear disabled (prevent accidental deletion)
)

// Scenario 3: Admin
adminService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

## References

- [Memory Module Source](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
- [Complete Examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)
- [API Documentation](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/memory)
