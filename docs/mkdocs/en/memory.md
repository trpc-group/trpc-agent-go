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

### Two Memory Modes

Memory supports two modes for creating and managing memories. Choose based on your scenario:

Auto Mode is available when an Extractor is configured and is recommended as the default choice.

| Aspect              | Agentic Mode (Tools)                           | Auto Mode (Extractor)                                     |
| ------------------- | ---------------------------------------------- | --------------------------------------------------------- |
| **How it works**    | Agent decides when to call memory tools        | System extracts memories automatically from conversations |
| **User experience** | Visible - user sees tool calls                 | Transparent - memories created silently in background     |
| **Control**         | Agent has full control over what to remember   | Extractor decides based on conversation analysis          |
| **Available tools** | All 6 tools                                    | Search tool (search), optional load tool (load)           |
| **Processing**      | Synchronous - during response generation       | Asynchronous - background workers after response          |
| **Best for**        | Precise control, user-driven memory management | Natural conversations, hands-off memory building          |

**Selection Guide**:

- **Agentic Mode**: Agent automatically decides when to call memory tools based on conversation content (e.g., when user mentions personal information or preferences), user sees tool calls, suitable for scenarios requiring precise control over memory content
- **Auto Mode (recommended)**: Natural conversation flow, system passively learns about users, simplified UX

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

### Agentic Mode Configuration (Optional)

In Agentic mode, the Agent automatically decides when to call memory tools
based on conversation content to manage memories. Configuration involves three steps:

```go
package main

import (
    "context"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // Step 1: Create memory service.
    memoryService := memoryinmemory.NewMemoryService()

    // Step 2: Create Agent and register memory tools.
    modelInstance := openai.New("deepseek-chat")
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("An assistant with memory capabilities."),
        llmagent.WithInstruction(
            "Remember important user info and recall it when needed.",
        ),
        llmagent.WithTools(memoryService.Tools()), // Register memory tools.
    )

    // Step 3: Create Runner with memory service.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService), // Set memory service.
    )
    defer appRunner.Close()

    // Run a dialog (the Agent uses memory tools automatically).
    log.Println("🧠 Starting memory-enabled chat...")
    message := model.NewUserMessage(
        "Hi, my name is John, and I like programming",
    )
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
    // Handle responses ...
    _ = eventChan
}
```

**Conversation example**:

```
User: My name is Alice and I work at TechCorp.

Agent: Nice to meet you, Alice! I'll remember that you work at TechCorp.

🔧 Tool call: memory_add
   Args: {"memory": "User's name is Alice, works at TechCorp", "topics": ["name", "work"]}
✅ Memory added successfully.

Agent: I've saved that information. How can I help you today?
```

### Auto Mode Configuration (Recommended)

In Auto mode, an LLM-based extractor analyzes conversations and automatically
creates memories. **The only difference from Agentic mode is in Step 1: add an Extractor**.

```go
package main

import (
    "context"
    "log"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // Step 1: Create memory service (configure Extractor to enable auto mode).
    extractorModel := openai.New("deepseek-chat")
    memExtractor := extractor.NewExtractor(extractorModel)
    memoryService := memoryinmemory.NewMemoryService(
        memoryinmemory.WithExtractor(memExtractor), // Key: configure extractor.
        // Optional: configure async workers.
        memoryinmemory.WithAsyncMemoryNum(1), // Configure number of async memory worker.
        memoryinmemory.WithMemoryQueueSize(10), // Configure memory queue size.
        memoryinmemory.WithMemoryJobTimeout(30*time.Second), // Configure memory extraction job timeout.
    )
    defer memoryService.Close()

    // Step 2: Create Agent and register memory tools.
    // Note: With Extractor configured, Tools() exposes Search by default.
    // Load can be enabled explicitly.
    chatModel := openai.New("deepseek-chat")
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(chatModel),
        llmagent.WithDescription("An assistant with automatic memory."),
        llmagent.WithTools(memoryService.Tools()), // Search by default; Load is optional.
    )

    // Step 3: Create Runner with memory service.
    // Runner triggers auto extraction after responses.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService),
    )
    defer appRunner.Close()

    // Run a dialog (system extracts memories automatically in background).
    log.Println("🧠 Starting auto memory chat...")
    message := model.NewUserMessage(
        "Hi, my name is John, and I like programming",
    )
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
    // Handle responses ...
    _ = eventChan
}
```

**Conversation example**:

```
User: My name is Alice and I work at TechCorp.

Agent: Nice to meet you, Alice! It's great to connect with someone from TechCorp.
       How can I help you today?

(Background: Extractor analyzes conversation and creates memory automatically)
```

### Configuration Comparison

| Step                | Agentic Mode                        | Auto Mode                              |
| ------------------- | ----------------------------------- | -------------------------------------- |
| **Step 1**          | `NewMemoryService()`                | `NewMemoryService(WithExtractor(ext))` |
| **Step 2**          | `WithTools(memoryService.Tools())`  | `WithTools(memoryService.Tools())`     |
| **Step 3**          | `WithMemoryService(memoryService)`  | `WithMemoryService(memoryService)`     |
| **Available tools** | add/update/delete/clear/search/load | search /load                           |
| **Memory creation** | Agent actively calls tools          | Background auto extraction             |

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

Configure the memory service in code. Five backends are supported: in-memory,
Redis, MySQL, PostgreSQL, and pgvector.

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

### Memory Tool Configuration

The memory service provides 6 tools. Common tools are enabled by default, while dangerous operations require manual enabling.

#### Tool List

| Tool            | Function       | Agentic Mode    | Auto Extraction Mode | Description                                    |
| --------------- | -------------- | --------------- | -------------------- | ---------------------------------------------- |
| `memory_add`    | Add new memory | ✅ Default      | ❌ Unavailable       | Create new memory entry                        |
| `memory_update` | Update memory  | ✅ Default      | ❌ Unavailable       | Modify existing memory                         |
| `memory_search` | Search memory  | ✅ Default      | ✅ Default           | Find by keywords                               |
| `memory_load`   | Load memories  | ✅ Default      | ⚙️ Configurable      | Load recent memories                           |
| `memory_delete` | Delete memory  | ⚙️ Configurable | ❌ Unavailable       | Delete single memory                           |
| `memory_clear`  | Clear memories | ⚙️ Configurable | ❌ Unavailable       | Delete all memories (not exposed in Auto mode) |

**Notes**:

- **Agentic Mode**: Agent actively calls tools to manage memory, all tools are configurable
  - Default enabled tools: `memory_add`, `memory_update`, `memory_search`, `memory_load`
  - Default disabled tools: `memory_delete`, `memory_clear`
- **Auto Mode**: LLM extractor handles write operations in background. Tools() exposes Search by default; Load can be enabled.
  - Default enabled tools: `memory_search`
  - Default disabled tools: `memory_load`
  - Not exposed tools: `memory_add`, `memory_update`, `memory_delete`, `memory_clear`
- **Default**: Available immediately when service is created, no extra configuration needed
- **Configurable**: Can be enabled/disabled via `WithToolEnabled()`
- **Unavailable**: Tool cannot be used in this mode

#### Enable/Disable Tools

Note: In Auto mode, `WithToolEnabled()` only affects whether `memory_search` and
`memory_load` are exposed via `Tools()`. `memory_add`, `memory_update`,
`memory_delete`, and `memory_clear` are not exposed to the Agent.

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

- Memory IDs are generated from memory content + sorted topics + appName + userID.
  Adding the same content and topics for the same user is idempotent and overwrites
  the existing entry (not append). UpdatedAt is refreshed.
- If you need append semantics or different duplicate-handling strategies, you can
  implement custom tools or extend the service with policy options (e.g. allow/overwrite/ignore).

### Custom Tool Implementation

Note: In Auto mode, `Tools()` only exposes `memory_search` and `memory_load`.
If you need to expose tools like `memory_clear`, use Agentic mode or call
`ClearMemories()` from your application code.

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
- `WithKeyPrefix(prefix)`: Set a prefix for all Redis keys. When set, every key is prefixed with `prefix:`. For example, if `prefix` is `"myapp"`, the key `mem:{app:user}` becomes `myapp:mem:{app:user}`. Default is empty (no prefix). This is useful for sharing a single Redis instance across multiple environments or services
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to Redis client

**Note**: `WithRedisClientURL` takes priority over `WithRedisInstance`

**Key prefix example**:

```go
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
```

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
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (app_name, user_id, memory_id),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
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
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS memories_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS memories_updated_at ON memories(updated_at DESC);
CREATE INDEX IF NOT EXISTS memories_deleted_at ON memories(deleted_at);
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer postgresService.Close()
```

### pgvector Storage

**Use case**: Production, vector similarity search with PostgreSQL + pgvector

```go
import memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

pgvectorService, err := memorypgvector.NewService(
    memorypgvector.WithHost("localhost"),
    memorypgvector.WithPort(5432),
    memorypgvector.WithUser("postgres"),
    memorypgvector.WithPassword("password"),
    memorypgvector.WithDatabase("dbname"),
    memorypgvector.WithEmbedder(embedder),
    memorypgvector.WithSoftDelete(true),
)
```

**Configuration options**:

- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: Connection parameters
- `WithSSLMode(mode)`: SSL mode (default "disable")
- `WithPostgresInstance(name)`: Use pre-registered PostgreSQL instance
- `WithEmbedder(embedder)`: Text embedder for vector generation (required)
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithSchema(schema)`: Specify database schema (default is public)
- `WithIndexDimension(dim)`: Vector dimension (default 1536)
- `WithMaxResults(limit)`: Max search results (default 10)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to PostgreSQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)
- `WithHNSWIndexParams(params)`: HNSW index parameters for vector search

**Note**: Direct connection parameters take priority over `WithPostgresInstance`. Requires pgvector extension to be installed in PostgreSQL.

**Table schema** (auto-created):

```sql
CREATE TABLE memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_content TEXT NOT NULL,
    topics TEXT[],
    embedding vector(1536),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- Indexes for performance
CREATE INDEX ON memories(app_name, user_id);
CREATE INDEX ON memories(updated_at DESC);
CREATE INDEX ON memories(deleted_at);
CREATE INDEX ON memories USING hnsw (embedding vector_cosine_ops);
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer pgvectorService.Close()
```

### Backend Comparison

| Feature           | InMemory  | Redis            | MySQL      | PostgreSQL        | pgvector      |
| ----------------- | --------- | ---------------- | ---------- | ----------------- | ------------- |
| **Persistence**   | ❌        | ✅               | ✅         | ✅                | ✅            |
| **Distributed**   | ❌        | ✅               | ✅         | ✅                | ✅            |
| **Transactions**  | ❌        | Partial          | ✅ ACID    | ✅ ACID           | ✅ ACID       |
| **Queries**       | Simple    | Medium           | SQL        | SQL               | SQL + Vector  |
| **JSON**          | ❌        | Basic            | JSON       | JSONB             | JSONB         |
| **Performance**   | Very High | High             | Med-High   | Med-High          | Med-High      |
| **Configuration** | Zero      | Simple           | Medium     | Medium            | Medium        |
| **Soft Delete**   | ❌        | ❌               | ✅         | ✅                | ✅            |
| **Use Case**      | Dev/Test  | High Concurrency | Enterprise | Advanced Features | Vector Search |

**Selection guide**:

```
Development/Testing → InMemory (zero config, fast)
High Concurrency → Redis (memory-level performance)
ACID Requirements → MySQL/PostgreSQL (transaction guarantees)
Complex JSON → PostgreSQL (JSONB indexing and queries)
Vector Search → pgvector (similarity search with embeddings)
Audit Trail → MySQL/PostgreSQL/pgvector (soft delete support)
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

| Feature                  | In-Memory | Redis      | MySQL          | PostgreSQL     | pgvector      |
| ------------------------ | --------- | ---------- | -------------- | -------------- | ------------- |
| Data Persistence         | ❌        | ✅         | ✅             | ✅             | ✅            |
| Distributed Support      | ❌        | ✅         | ✅             | ✅             | ✅            |
| Transaction Support      | ❌        | Partial    | ✅ (ACID)      | ✅ (ACID)      | ✅ (ACID)     |
| Query Capability         | Simple    | Medium     | Powerful (SQL) | Powerful (SQL) | SQL + Vectors |
| JSON Support             | ❌        | Partial    | ✅ (JSON)      | ✅ (JSONB)     | ✅ (JSONB)    |
| Performance              | Very High | High       | Medium-High    | Medium-High    | Medium-High   |
| Configuration Complexity | Low       | Medium     | Medium         | Medium         | Medium        |
| Use Case                 | Dev/Test  | Production | Production     | Production     | Vector Search |
| Monitoring Tools         | None      | Rich       | Very Rich      | Very Rich      | Very Rich     |

**Selection Guide:**

- **Development/Testing**: Use in-memory storage for fast iteration
- **Production (High Performance)**: Use Redis storage for high concurrency scenarios
- **Production (Data Integrity)**: Use MySQL storage when ACID guarantees and complex queries are needed
- **Production (PostgreSQL)**: Use PostgreSQL storage when JSONB support and advanced PostgreSQL features are needed
- **Production (Vector Search)**: Use pgvector storage when similarity search with embeddings is needed
- **Hybrid Deployment**: Choose different storage backends based on different application scenarios

## FAQ

### Difference between Memory and Session

Memory and Session solve different problems:

| Dimension     | Memory                           | Session                               |
| ------------- | -------------------------------- | ------------------------------------- |
| **Purpose**   | Long-term user profile           | Temporary conversation context        |
| **Isolation** | `<appName, userID>`              | `<appName, userID, sessionID>`        |
| **Lifecycle** | Persists across sessions         | Valid within a single session         |
| **Content**   | User profile, preferences, facts | Conversation history, messages        |
| **Data Size** | Small (tens to hundreds)         | Large (tens to thousands of messages) |
| **Use Case**  | "Remember who the user is"       | "Remember what was said"              |

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

Memory ID is generated from SHA256 hash of "content + sorted topics + appName + userID". Same content produces the same ID for the same user:

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

### Search Behavior Notes

Search behavior depends on the backend:

- For `inmemory` / `redis` / `mysql` / `postgres`: `SearchMemories` uses **token matching** (not semantic search).
- For `pgvector`: `SearchMemories` uses **vector similarity search** and requires an embedder.

**Token matching details** (non-pgvector backends):

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

**Limitations** (non-pgvector backends):

- These backends perform filtering and sorting in **application layer** (\[O(n)\] complexity)
- Performance affected by data volume
- Not semantic similarity search

**Recommendations**:

- Use explicit keywords and topic tags to improve hit rate
- If you need semantic similarity search, use the pgvector backend

### Soft Delete Considerations

**Support status**:

- ✅ MySQL, PostgreSQL, pgvector: support soft delete
- ❌ InMemory, Redis: not supported (hard delete only)

**Soft delete configuration**:

```go
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("..."),
    memorymysql.WithSoftDelete(true), // Enable soft delete
)
```

**Behavior differences**:

| Operation | Hard Delete       | Soft Delete                              |
| --------- | ----------------- | ---------------------------------------- |
| Delete    | Immediate removal | Set `deleted_at` field                   |
| Query     | Not visible       | Auto-filtered (WHERE deleted_at IS NULL) |
| Recovery  | Cannot recover    | Can manually clear `deleted_at`          |
| Storage   | Saves space       | Occupies space                           |

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

In auto extraction mode, `WithToolEnabled` controls all 6 tools, but they serve different purposes:

**Front-end Tools** (exposed via `Tools()` for agent to call):

| Tool            | Default | Description                   |
| --------------- | ------- | ----------------------------- |
| `memory_search` | ✅ On   | Search memories by query      |
| `memory_load`   | ❌ Off  | Load all or recent N memories |

**Back-end Tools** (used by extractor in background, not exposed to agent):

| Tool            | Default | Description                            |
| --------------- | ------- | -------------------------------------- |
| `memory_add`    | ✅ On   | Add new memories (extractor uses this) |
| `memory_update` | ✅ On   | Update existing memories               |
| `memory_delete` | ✅ On   | Delete memories                        |
| `memory_clear`  | ❌ Off  | Clear all user memories (dangerous)    |

**Configuration Examples**:

```go
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    // Front-end: enable memory_load for agent to call.
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    // Back-end: disable memory_delete so extractor cannot delete.
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
    // Back-end: enable memory_clear for extractor (use with caution).
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

**Note**: `WithToolEnabled` can be called before or after `WithExtractor` - the order does not matter.

### Comparison: Agentic Mode vs Auto Mode

| Tool            | Agentic Mode (no extractor)             | Auto Mode (with extractor)                 |
| --------------- | --------------------------------------- | ------------------------------------------ |
| `memory_add`    | ✅ Agent calls via `Tools()`            | ✅ Extractor uses in background            |
| `memory_update` | ✅ Agent calls via `Tools()`            | ✅ Extractor uses in background            |
| `memory_search` | ✅ Agent calls via `Tools()`            | ✅ Agent calls via `Tools()`               |
| `memory_load`   | ✅ Agent calls via `Tools()`            | ⚙️ Agent calls via `Tools()` if enabled    |
| `memory_delete` | ⚙️ Agent calls via `Tools()` if enabled | ✅ Extractor uses in background            |
| `memory_clear`  | ⚙️ Agent calls via `Tools()` if enabled | ⚙️ Extractor uses in background if enabled |

### Memory Preloading

Both modes support preloading memories into the system prompt:

```go
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),
    // Preload options:
    // llmagent.WithPreloadMemory(0),   // Disable preloading (default).
    // llmagent.WithPreloadMemory(10),  // Load 10 most recent.
    // llmagent.WithPreloadMemory(-1),  // Load all.
    //                                  // ⚠️ WARNING: Loading all memories may significantly
    //                                  //     increase token usage and API costs, especially
    //                                  //     for users with many stored memories. Consider
    //                                  //     using a positive limit for production use.
    // llmagent.WithPreloadMemory(10),  // Load 10 most recent (recommended for production).
)
```

When preloading is enabled, memories are automatically injected into the
system prompt, giving the Agent context about the user without explicit
tool calls.

**⚠️ Important Note**: Setting the configuration to `-1` loads all memories,
which may significantly increase **Token Usage** and **API Costs**. By default,
preloading is disabled (`0`), and we recommend using positive limits (e.g., `10-50`)
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
    llmagent.WithPreloadMemory(10),             // Preload recent memories.
)
```

## References

- [Memory Module Source](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
- [Agentic Mode Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)
- [Auto Mode Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/auto)
- [API Documentation](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/memory)
