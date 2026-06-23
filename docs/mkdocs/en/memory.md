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
| **Available tools** | `memory_add`, `memory_update`, `memory_search`, `memory_load` by default; delete/clear configurable | `memory_search` exposed by default; `memory_load` exposed once enabled; enabled write operations are used by the extractor and hidden from the agent unless explicitly exposed |
| **Processing**      | Synchronous - during response generation       | Asynchronous - background workers after response          |
| **Best for**        | Precise control, user-driven memory management | Natural conversations, hands-off memory building          |

**Selection Guide**:

- **Agentic Mode**: Agent automatically decides when to call memory tools based on conversation content (e.g., when user mentions personal information or preferences), user sees tool calls, suitable for scenarios requiring precise control over memory content
- **Auto Mode (recommended)**: Natural conversation flow, system passively learns about users, simplified UX

### Progressive DeepSearch

DeepSearch is an optional memory retrieval enhancement. It builds a cue/tag
index from existing `memory.Entry` records and exposes a progressive second
retrieval stage to `LLMAgent`. The original memory entry remains the
authoritative content; DeepSearch does not store raw sessions as independent
memory content.

DeepSearch is disabled by default and requires two explicit opt-ins:

1. Enable the backend index capability with `WithDeepSearch(indexModel)`.
2. Enable the progressive agent tool surface with
   `llmagent.WithMemoryDeepSearch()`.

If either option is missing, the normal memory tools and retrieval behavior stay
unchanged. `memory_search` always uses the original memory service search. The
`memory_deepsearch` tool can only be called after a successful
`memory_search`; once it succeeds, DeepSearch subtools are activated only for
the current invocation.

```go
indexModel := openai.New("gpt-4o-mini")
chatModel := openai.New("gpt-4o-mini")

memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithDeepSearch(indexModel),
)

llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(chatModel),
    llmagent.WithTools(memoryService.Tools()),
    llmagent.WithMemoryDeepSearch(),
)
```

Supported backends in the experimental implementation are `memory/inmemory` and
`memory/pgvector`. Other memory backends keep their existing behavior and do not
expose DeepSearch backend options.

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
    modelInstance := openai.New("deepseek-v4-flash")
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
creates memories. **The key setup difference is in Step 1: add an Extractor**.
With an Extractor configured, `Tools()` follows Auto mode exposure rules and
Runner triggers background extraction after responses.

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
    extractorModel := openai.New("deepseek-v4-flash")
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
    chatModel := openai.New("deepseek-v4-flash")
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
| **Available tools** | add/update/search/load by default; delete/clear configurable | search exposed by default; load exposed once enabled; enabled write operations are used by the extractor and hidden from the agent unless explicitly exposed |
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
Redis, MySQL, PostgreSQL, and pgvector. Two vector search backends are also
available: sqlitevec and mysqlvec.

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

The memory service provides 6 tools. In Agentic mode, common tools are enabled
by default and dangerous operations require manual enabling. In Auto mode,
extractor operation availability and agent-facing tool exposure are controlled
separately.

#### Tool List

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

#### Enable/Disable Tools

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

### Overwrite Semantics (IDs and duplicates)

- Memory IDs are generated from memory content + appName + userID + canonical
  episodic metadata. Topics are intentionally excluded, so changing tags does
  not create a new memory. Adding the same content and identity metadata for the
  same user is idempotent and overwrites the existing entry (not append).
  UpdatedAt is refreshed.
- If you need append semantics or different duplicate-handling strategies, you can
  implement custom tools or extend the service with policy options (e.g. allow/overwrite/ignore).

### Custom Tool Implementation

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

### SQLite Storage

**Use case**: Local persistence, single-node deployments, demos

SQLite stores data in a single file. It is useful when you want persistence
without operating MySQL/PostgreSQL/Redis.

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
)

db, err := sql.Open("sqlite3", "file:memories.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

memoryService, err := memorysqlite.NewService(
    db,
    memorysqlite.WithSoftDelete(true),
    memorysqlite.WithMemoryLimit(200),
)
if err != nil {
    // handle error
}
defer memoryService.Close()
```

**Configuration options**:

- `WithTableName(name)`: Table name (default "memories")
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithSkipDBInit(skip)`: Skip table initialization
- Auto mode: `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout`
- Tools: `WithCustomTool`, `WithToolEnabled`

**Notes**:

- This backend uses `github.com/mattn/go-sqlite3` and requires CGO.
- `NewService` owns the `*sql.DB` and closes it in `Close()`.

### SQLiteVec (sqlite-vec) Storage

**Use case**: Local persistence + semantic memory search on a single node

SQLiteVec stores memories in a SQLite file and uses `sqlite-vec` to do
vector similarity search (semantic search). Compared to the plain SQLite
backend, it requires an **embedder** to generate embeddings.

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

db, err := sql.Open("sqlite3", "file:memories_vec.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

emb := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

memoryService, err := memorysqlitevec.NewService(
    db,
    memorysqlitevec.WithEmbedder(emb),
    memorysqlitevec.WithSoftDelete(true),
    memorysqlitevec.WithMemoryLimit(200),
)
if err != nil {
    // handle error
}
defer memoryService.Close()
```

**Configuration options**:

- `WithTableName(name)`: Table name (default "memories")
- `WithEmbedder(embedder)`: Text embedder for vector generation (required)
- `WithIndexDimension(dim)`: Vector dimension (default is embedder dimension)
- `WithMaxResults(limit)`: Max search results (default 10)
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithSkipDBInit(skip)`: Skip table initialization
- Auto mode: `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`,
  `WithMemoryJobTimeout`
- Tools: `WithCustomTool`, `WithToolEnabled`

**Notes**:

- This backend uses `github.com/mattn/go-sqlite3` and requires CGO.
- The `sqlite-vec` extension is compiled and registered in-process via Go
  bindings (no external `.so/.dylib` download at runtime).

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

### MySQL Vector (mysqlvec) Storage

**Use case**: Production, vector similarity search with MySQL + native VECTOR type

MySQL Vector stores memories in MySQL with embedding vectors for semantic similarity
search. It uses MySQL 9.0+ native `VECTOR` type when available, and automatically
falls back to `BLOB` storage with Go-side cosine similarity for older versions (8.x).

```go
import memorymysqlvec "trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

mysqlvecService, err := memorymysqlvec.NewService(
    memorymysqlvec.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysqlvec.WithEmbedder(embedder),
    memorymysqlvec.WithSoftDelete(true),
)
```

**Configuration options**:

- `WithMySQLClientDSN(dsn)`: MySQL DSN connection string (recommended, requires `parseTime=true`)
- `WithMySQLInstance(name)`: Use pre-registered MySQL instance
- `WithEmbedder(embedder)`: Text embedder for vector generation (required)
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithIndexDimension(dim)`: Vector dimension (default 1536)
- `WithMaxResults(limit)`: Max search results (default 15)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to MySQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL permissions)

**Note**: Requires MySQL 5.7.8+ (for JSON column type). Uses native VECTOR on MySQL 9.0+; falls back to BLOB + Go-side cosine similarity on MySQL 5.7/8.x. No additional vector library required.

**Table schema** (auto-created, MySQL 9.0+):

```sql
CREATE TABLE memories (
    memory_id VARCHAR(64) PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_content TEXT NOT NULL,
    topics JSON,
    embedding VECTOR(1536),
    memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP(6) NULL,
    participants JSON,
    location VARCHAR(1024) NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_updated_at (updated_at DESC),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer mysqlvecService.Close()
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

| Feature           | InMemory  | SQLite            | SQLiteVec        | Redis            | MySQL      | MySQL Vec         | PostgreSQL        | pgvector      |
| ----------------- | --------- | ----------------- | ---------------- | ---------------- | ---------- | ----------------- | ----------------- | ------------- |
| **Persistence**   | ❌        | ✅                | ✅               | ✅               | ✅         | ✅                | ✅                | ✅            |
| **Distributed**   | ❌        | ❌                | ❌               | ✅               | ✅         | ✅                | ✅                | ✅            |
| **Transactions**  | ❌        | ✅ ACID           | ✅ ACID          | Partial          | ✅ ACID    | ✅ ACID           | ✅ ACID           | ✅ ACID       |
| **Queries**       | Simple    | SQL               | SQL + Vector     | Medium           | SQL        | SQL + Vector      | SQL               | SQL + Vector  |
| **JSON**          | ❌        | Basic             | Basic            | Basic            | JSON       | JSON              | JSONB             | JSONB         |
| **Performance**   | Very High | Med-High          | Med-High         | High             | Med-High   | Med-High          | Med-High          | Med-High      |
| **Configuration** | Zero      | Simple            | Medium           | Simple           | Medium     | Medium            | Medium            | Medium        |
| **Soft Delete**   | ❌        | ✅                | ✅               | ❌               | ✅         | ✅                | ✅                | ✅            |
| **Use Case**      | Dev/Test  | Local Persistence | Local Vector     | High Concurrency | Enterprise | MySQL Vector Search | Advanced Features | Vector Search |

**Selection guide**:

```
Development/Testing → InMemory (zero config, fast)
Local Persistence → SQLite (single-file DB, easy setup)
Local Vector Search → SQLiteVec (single-file DB + embeddings)
High Concurrency → Redis (memory-level performance)
ACID Requirements → MySQL/PostgreSQL (transaction guarantees)
Complex JSON → PostgreSQL (JSONB indexing and queries)
MySQL Vector Search → mysqlvec (similarity search on MySQL 9.0+)
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

| Feature                  | In-Memory | SQLite     | SQLiteVec    | Redis      | MySQL          | PostgreSQL     | pgvector      |
| ------------------------ | --------- | ---------- | ----------- | ---------- | -------------- | -------------- | ------------- |
| Data Persistence         | ❌        | ✅         | ✅          | ✅         | ✅             | ✅             | ✅            |
| Distributed Support      | ❌        | ❌         | ❌          | ✅         | ✅             | ✅             | ✅            |
| Transaction Support      | ❌        | ✅ (ACID)  | ✅ (ACID)   | Partial    | ✅ (ACID)      | ✅ (ACID)      | ✅ (ACID)     |
| Query Capability         | Simple    | SQL        | SQL + Vector | Medium     | Powerful (SQL) | Powerful (SQL) | SQL + Vectors |
| JSON Support             | ❌        | Basic      | Basic       | Partial    | ✅ (JSON)      | ✅ (JSONB)     | ✅ (JSONB)    |
| Performance              | Very High | Med-High   | Medium-High | High       | Medium-High    | Medium-High    | Medium-High   |
| Configuration Complexity | Low       | Low        | Medium      | Medium     | Medium         | Medium         | Medium        |
| Use Case                 | Dev/Test  | Local Dev  | Local Vector | Production | Production     | Production     | Vector Search |
| Monitoring Tools         | None      | None       | None        | Rich       | Very Rich      | Very Rich      | Very Rich     |

**Selection Guide:**

- **Development/Testing**: Use in-memory storage for fast iteration
- **Local Development (Persistent)**: Use SQLite when you want persistence without operating an external database
- **Local Development (Vector Search)**: Use SQLiteVec when you want semantic search in a single-file SQLite DB
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

Memory ID is generated from a SHA256 hash of memory content, appName, userID,
and canonical episodic metadata. Topics are not part of identity, so the same
content for the same user keeps the same ID even if topics change:

```go
// First add
memory.AddMemory(ctx, userKey, "User likes programming", []string{"hobby"})
// Generated ID: abc123...

// Second add with same content and different topics
memory.AddMemory(ctx, userKey, "User likes programming", []string{"interests"})
// Same ID: abc123..., overwrites, refreshes topics and updated_at
```

**Implications**:

- ✅ **Natural deduplication**: Avoids redundant storage
- ✅ **Idempotent operations**: Repeated additions don't create multiple records
- ⚠️ **Overwrite update**: Cannot append same content (add timestamp or sequence number if append is needed)

### Search Behavior Notes

Search behavior depends on the backend:

- For `inmemory` / `redis` / `mysql` / `postgres`: `SearchMemories` uses **BM25-style lexical matching** (not semantic search).
- For `pgvector` / `mysqlvec` / `sqlitevec`: `SearchMemories` uses **vector similarity search** and requires an embedder.

**Lexical matching details** (non-vector backends):

**English tokenization**: lowercase → filter stopwords (a, the, is, etc.) → split by spaces

```go
// Can find
Memory: "User likes programming"
Search: "programming" ✅ Match

// Cannot find
Memory: "User likes programming"
Search: "coding" ❌ No match (semantically similar but different words)
```

**Chinese tokenization**: prefers `gse` word segmentation with
low-weight CJK character trigram fallback

```go
Memory: "用户喜欢编程"
Search: "编程" ✅ Match (word-level hit)
Search: "写代码" ❌ No match (different words)
```

**Limitations** (non-vector backends):

- These backends perform filtering and sorting in **application layer** (\[O(n)\] complexity)
- Performance affected by data volume
- Not semantic similarity search
- Ranking uses **BM25-style lexical scoring + query coverage + ordered
  phrase bonus**, not vector semantics

**Recommendations**:

- Use explicit keywords and topic tags to improve hit rate
- If you need semantic similarity search, use the pgvector, mysqlvec, or sqlitevec backend

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

## External Long-Term Memory Integration (`mem0`)

`memory/mem0` integrates [mem0](https://mem0.ai), an externally hosted long-term memory platform. It is suitable when you want mem0 to handle memory extraction and storage, while the Agent continues to query memories through standard tools.

Unlike the built-in backends above, `memory/mem0` is **not** a full `memory.Service` implementation. It uses an ingest-first pattern: Runner forwards session transcripts to mem0 after each turn, mem0 performs extraction on the service side, and the Agent uses read-oriented tools to query the results.

**Use case**: Hosted long-term memory, background extraction after each turn, and no local CRUD write path.

### Configuration Example

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

mem0Svc, err := memorymem0.NewService(
    memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
    memorymem0.WithLoadToolEnabled(true),
)
if err != nil {
    panic(err)
}
defer mem0Svc.Close()

sessionSvc := sessioninmemory.NewSessionService()
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(mem0Svc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc),
)
defer r.Close()
```

**Integration points**:

- Register tools with `llmagent.WithTools(mem0Svc.Tools())`
- Use `runner.WithSessionIngestor(mem0Svc)` to send session transcripts to mem0
- Do **not** use `runner.WithMemoryService(...)` with this integration

### Why `WithSessionIngestor(...)` Instead of `WithMemoryService(...)`

`runner.WithMemoryService(...)` is designed for built-in memory backends that implement the full `memory.Service` contract. In addition to read APIs, that contract includes framework-owned write semantics such as `AddMemory`, `UpdateMemory`, `DeleteMemory`, `ClearMemories`, and `EnqueueAutoMemoryJob(...)`.

`memory/mem0` has a different boundary. It does not expose the full CRUD lifecycle to the framework. Instead, it accepts a completed session transcript, forwards it to mem0 for hosted extraction, and then exposes read-oriented tools for retrieval.

Using `runner.WithSessionIngestor(...)` makes that boundary explicit:

- Runner sends the completed session transcript after each turn
- mem0 performs extraction and storage on the service side
- per-request ingest fields such as `metadata`, `agent_id`, and `run_id` can be passed through `session.IngestOption`
- the integration is not mistaken for a built-in backend that supports full framework-side CRUD or preload behavior

In short, `MemoryService` means "the framework manages memories directly", while `SessionIngestor` means "the framework hands the transcript to an external memory system". `mem0` matches the second model.

### Configuration Options

| Option | Purpose | Default |
| ------ | ------- | ------- |
| `WithAPIKey(key)` | mem0 API key. Required for all requests. | required |
| `WithHost(url)` | Override the mem0 API host/base URL. | `https://api.mem0.ai` |
| `WithOrgProject(orgID, projectID)` | Add mem0 `org_id` / `project_id` to ingest and retrieval requests. | empty |
| `WithAsyncMode(bool)` | Controls mem0's `async_mode` flag on ingest requests. | `true` |
| `WithVersion(v)` | Sets the mem0 ingestion API version field. | `v2` |
| `WithTimeout(d)` | HTTP timeout used by the client. | `10s` |
| `WithLoadToolEnabled(bool)` | Expose `memory_load` from `Tools()`. | `false` |
| `WithAsyncMemoryNum(n)` | Number of background ingest workers. | `1` |
| `WithMemoryQueueSize(n)` | Queue size per ingest worker. | `10` |
| `WithMemoryJobTimeout(d)` | Timeout for queued jobs and synchronous fallback ingest. | `30s` |

### Notes

- `Tools()` exposes `memory_search` by default; `memory_load` can be enabled explicitly.
- All reads remain scoped to the current `<appName, userID>`.
- Runner automatically passes session context into ingest. Custom callers can also use `session.WithIngestMetadata`, `session.WithIngestAgentID`, and `session.WithIngestRunID` when needed.
- When mem0 metadata is available, search results can still carry structured fields such as `Topics`, `Kind`, `EventTime`, `Participants`, and `Location`.
- Call `Close()` on the service so background workers shut down cleanly.
- If you need full CRUD tools or framework-side preload, use one of the built-in memory backends instead.

## TencentDB Agent Memory Integration (`memory/tencentdb`)

`memory/tencentdb` integrates
[TencentDB Agent Memory](https://github.com/Tencent/TencentDB-Agent-Memory)
through its standalone gateway sidecar. It is suitable when you want the
TencentDB Agent Memory SDK to own the L0-L3 memory pipeline while
tRPC-Agent-Go keeps the Runner, session, plugin, and tool lifecycle in Go.

The boundary is intentionally different from built-in backends:

- The TencentDB Agent Memory gateway performs capture, extraction, storage,
  recall, and search.
- The Go adapter sends completed session turns through `session.Ingestor`.
- A Runner plugin calls `/recall` before each model request and injects the
  returned context (opt-in via `WithRecallEnabled(true)`).
- Native tools expose read-oriented search through `tdai_conversation_search`
  (session-scoped, on by default) and `tdai_memory_search` (opt-in via
  `WithMemorySearchTool(true)`).
- An optional short-term context offload plugin delegates tool-result
  externalization, L1/L1.5/L2/L3 processing, drill-down, and persistence to
  TencentDB Agent Memory gateway hook APIs. The Go adapter does not write local
  offload files. It is separate from recall and is off by default.

> **Multi-tenant note:** automatic recall and `tdai_memory_search` read from the
> gateway's shared long-term store, which does not currently enforce
> user/session scoping. They are therefore disabled by default; enable them only
> when the gateway guarantees per-tenant isolation. Only session-scoped capture
> and `tdai_conversation_search` are on by default.

Even when the SDK uses local SQLite storage, the gateway is still required
because it hosts the memory engine. Direct VectorDB or SQLite access only talks
to storage and does not run the SDK's extraction and recall pipeline.

**Use case**: Sidecar memory engine, local or self-managed storage, automatic
recall before model calls, and external SDK-owned memory extraction.

### Start the TencentDB Agent Memory Gateway

Clone the SDK repository and start the standalone gateway:

```bash
git clone https://github.com/Tencent/TencentDB-Agent-Memory.git
cd TencentDB-Agent-Memory
npm install

export TDAI_LLM_API_KEY="your-openai-compatible-api-key"
export TDAI_LLM_BASE_URL="https://api.openai.com/v1"
export TDAI_LLM_MODEL="deepseek-v4-flash"

node --import tsx src/gateway/server.ts
```

The gateway listens on `http://127.0.0.1:8420` by default. It reads
`TDAI_LLM_API_KEY`, `TDAI_LLM_BASE_URL`, and `TDAI_LLM_MODEL` for extraction,
scene/persona generation, and recall.

### Configuration Example

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memorytencentdb "trpc.group/trpc-go/trpc-agent-go/memory/tencentdb"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

gatewayURL := os.Getenv("TENCENTDB_AGENT_MEMORY_GATEWAY")
if gatewayURL == "" {
    gatewayURL = "http://127.0.0.1:8420"
}

memSvc, err := memorytencentdb.NewService(
    memorytencentdb.WithGatewayURL(gatewayURL),
    // Opt-in cross-session/user reads; enable only for a trusted/isolated gateway.
    memorytencentdb.WithRecallEnabled(true),
    memorytencentdb.WithMemorySearchTool(true),
    // Optional short-term tool result offload; requires a gateway that supports
    // /offload/v1/hooks/* and /offload/v1/tools/*.
    // memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    //     Enabled: true,
    // }),
    // memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
)
if err != nil {
    panic(err)
}
defer memSvc.Close()

sessionSvc := sessioninmemory.NewSessionService()
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(memSvc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(memSvc),
    runner.WithPlugins(
        memSvc.Plugin(),
        // memSvc.ContextOffloadPlugin(),
    ),
)
defer r.Close()
```

**Integration points**:

- Register TencentDB-native search tools with `llmagent.WithTools(memSvc.Tools())`
- Use `runner.WithSessionIngestor(memSvc)` to send timestamped session
  transcripts to `/capture`
- Use `runner.WithPlugins(memSvc.Plugin())` to enable automatic `/recall`
  before model calls
- Use `runner.WithPlugins(memSvc.ContextOffloadPlugin())` only when
  `WithContextOffload(memorytencentdb.ContextOffloadConfig{Enabled: true})` is
  set and you want short-term tool result offload. Companion tools
  `tdai_read_offload_ref`,
  `tdai_read_offload_node`, and `tdai_search_offload_index` are exposed
  through `memSvc.Tools()` when enabled.
- Do **not** use `runner.WithMemoryService(...)` with this integration

### Enable Context Offload

Context offload is a separate, opt-in path for large tool results. Use it only
with a TencentDB Agent Memory gateway build that exposes the offload routes
listed in the notes below. The Go adapter does not provide local storage,
summarization models, or local/backend/collect modes.

Minimal setup:

```go
memSvc, err := memorytencentdb.NewService(
    memorytencentdb.WithGatewayURL(gatewayURL),
    memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
        Enabled: true,
    }),
)
if err != nil {
    panic(err)
}

agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(memSvc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(memSvc),
    runner.WithPlugins(memSvc.ContextOffloadPlugin()),
)
```

If offload traffic should use a different gateway or key from normal
capture/search/recall traffic, set `GatewayURL` and `APIKey` on
`ContextOffloadConfig`:

```go
memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    Enabled:    true,
    GatewayURL: "http://127.0.0.1:8420",
    APIKey:     os.Getenv("TDAI_OFFLOAD_GATEWAY_API_KEY"),
})
```

At runtime:

- `ContextOffloadPlugin()` calls the gateway after tool execution. The gateway
  may replace large tool result messages with compact references or summaries.
- Before the next model call, the plugin asks the gateway whether the current
  request should be rewritten with offloaded context.
- `memSvc.Tools()` exposes `tdai_read_offload_ref`,
  `tdai_read_offload_node`, and `tdai_search_offload_index` when context
  offload is enabled, so the model can drill into gateway-owned offload data.
- Do not configure local directories, local models, or L0-L3 policies in the Go
  adapter; those concerns belong to TencentDB Agent Memory.

### Interactive Example

Run the example after the gateway is ready:

```bash
cd examples/memory/tencentdb
export OPENAI_API_KEY="your-openai-compatible-api-key"
export TENCENTDB_AGENT_MEMORY_GATEWAY="http://127.0.0.1:8420"
go run .
```

Then send facts, flush into a new session, and ask related questions:

```text
You: Remember this profile: my project code name is Apollo Lake, my deployment window is Friday night, and I prefer concise answers.
You: /new
You: What is my project code name, deployment window, and answer preference?
```

The first messages are captured by the gateway. The example's `/new` command
flushes the current gateway session before switching to a fresh session, so the
same user can recall extracted memories through the plugin and native search
tools even after conversation history is reset.

### Configuration Options

| Option | Purpose | Default |
| ------ | ------- | ------- |
| `WithGatewayURL(url)` | TencentDB Agent Memory gateway URL. | `http://127.0.0.1:8420` |
| `WithTimeout(d)` | HTTP timeout used by the gateway client. | `5s` |
| `WithIngestWorkers(n)` | Number of async capture workers. | `1` |
| `WithIngestQueueSize(n)` | Queue size for async capture jobs. | `10` |
| `WithIngestJobTimeout(d)` | Timeout for queued capture jobs. | `30s` |
| `WithSessionKeyFunc(fn)` | Customize session to gateway `session_key` mapping. | `base64url(app):base64url(user):base64url(session)` |
| `WithAPIKey(key)` | Send `Authorization: Bearer <key>` (gateway `TDAI_GATEWAY_API_KEY`). | none |
| `WithRecallEnabled(bool)` | Enable automatic recall plugin behavior (opt-in; reads shared store). | `false` |
| `WithMemorySearchTool(bool)` | Expose `tdai_memory_search` (opt-in; reads shared store). | `false` |
| `WithConversationSearchTool(bool)` | Expose `tdai_conversation_search`. | `true` |
| `WithStandardAliases(bool)` | Also expose standard `memory_search` alias (requires memory search enabled). | `false` |
| `WithToolPrefix(prefix)` | Change native tool prefix. | `tdai` |
| `WithContextOffload(ContextOffloadConfig)` | Configure explicit short-term context offload for large tool results. | disabled |

`ContextOffloadConfig` only controls the Go adapter's gateway integration.
Offload layers, state, storage, TTL, and isolation are owned by the TencentDB
Agent Memory gateway. The Go adapter does not expose local/backend offload
modes and does not write local offload state.

| Field | Purpose | Default |
| ----- | ------- | ------- |
| `Enabled` | Enable context offload plugin and companion tools. | `false` |
| `GatewayURL` | Optional gateway URL override for context offload hook/tool calls. Empty reuses `WithGatewayURL`. | none |
| `APIKey` | Optional API key override for context offload hook/tool calls. Empty reuses `WithAPIKey`. | none |

### Notes

- The adapter forwards app, user, and session identifiers to the gateway, but
  hard multi-tenant isolation depends on the gateway and SDK honoring those
  fields end-to-end. Because of this, automatic recall and `tdai_memory_search`
  are opt-in and disabled by default.
- When the gateway is started with `TDAI_GATEWAY_API_KEY`, set `WithAPIKey(...)`
  so requests carry `Authorization: Bearer <key>`; otherwise every non-health
  route returns 401 while the health check still passes.
- `tdai_memory_search` searches extracted long-term memory; extraction is
  asynchronous, so newly captured facts may take a short time to become
  searchable.
- `tdai_conversation_search` searches conversation history and defaults to the
  current gateway `session_key`.
- Context offload is opt-in and gateway-owned. It does not call `/capture` or
  `/recall`, and enabling recall alone will not rewrite tool result messages or
  create Mermaid task maps.
- The Go adapter calls gateway hook endpoints
  `/offload/v1/hooks/after-tool-messages` and
  `/offload/v1/hooks/before-model`. It does not create `.tdai-offload`, local
  refs, JSONL indexes, Mermaid files, or local state.
- The offload tools call gateway drill-down endpoints
  `/offload/v1/tools/read-ref`, `/offload/v1/tools/read-node`, and
  `/offload/v1/tools/search-index`. Scope validation, storage ACLs, byte
  limits, and persistence are gateway responsibilities.
- Call `Close()` on the service so background capture workers shut down cleanly.

## References

- [Memory Module Source](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
- [Agentic Mode Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)
- [Auto Mode Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/auto)
- [mem0 Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/mem0)
- [TencentDB Agent Memory Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/tencentdb)
- [TencentDB Agent Memory SDK](https://github.com/Tencent/TencentDB-Agent-Memory)
- [Ecosystem Guide](https://github.com/trpc-group/trpc-agent-go/blob/main/docs/mkdocs/en/ecosystem.md)
- [API Documentation](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/memory)
