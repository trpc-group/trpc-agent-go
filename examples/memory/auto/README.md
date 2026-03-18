# 🧠 Auto Memory Chat

This example demonstrates automatic memory extraction using the `Runner` orchestration component. Unlike the manual memory tools approach, auto memory extracts user information from conversations automatically in the background without explicit tool calls.

## What is Auto Memory?

Auto memory mode uses an LLM-based extractor to analyze conversations and automatically create/update memories. The system learns about users passively from natural conversation flow.

### Key Differences from Manual Memory

| Aspect              | Manual Memory (Agentic)             | Auto Memory                             |
| ------------------- | ----------------------------------- | --------------------------------------- |
| **Memory Creation** | Agent explicitly calls `memory_add` | System extracts automatically           |
| **User Experience** | Visible tool calls in conversation  | Transparent, no tool call interruptions |
| **Available Tools** | 6 tools (4 default, 2 configurable) | Only `memory_search`                    |
| **Processing**      | Synchronous during response         | Asynchronous after response             |
| **Control**         | Agent decides what to remember      | Extractor analyzes and decides          |

### Key Features

- **🔄 Automatic Extraction**: LLM-based extractor analyzes conversations and creates memories
- **🌊 Background Processing**: Memory extraction happens asynchronously after responses
- **🔍 Search Only**: Agent can search memories but cannot manually add/update/delete
- **💾 Transparent UX**: Users don't see memory tool calls, natural conversation flow
- **⚡ Async Workers**: Configurable worker pool for memory extraction jobs

## Architecture

### Auto Memory Flow

```
User Input → Agent Response → Runner → Async Worker → Extractor → Memory Service
                                                          ↓
                                              LLM analyzes conversation
                                                          ↓
                                              Creates/updates memories
```

### Configuration

Auto memory is enabled by configuring an extractor on the memory service:

```go
// Create memory extractor (uses LLM to analyze conversations).
extractorModel := openai.New("deepseek-chat")
memExtractor := extractor.NewExtractor(extractorModel)

// Create memory service with auto extraction enabled.
// When extractor is set, only search and clear tools are exposed.
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    // Optional: configure async worker settings.
    memoryinmemory.WithAsyncMemoryNum(3),
    memoryinmemory.WithMemoryQueueSize(100),
    memoryinmemory.WithMemoryJobTimeout(30*time.Second),
)

// Create LLM agent with memory tools.
// Only search and clear tools are available since extractor is set.
llmAgent := llmagent.New(
    "auto-memory-assistant",
    llmagent.WithModel(chatModel),
    llmagent.WithTools(memoryService.Tools()), // memory_search and memory_clear.
)

// Create runner with memory service.
// Runner automatically triggers memory extraction after responses.
runner := runner.NewRunner(
    "auto-memory-chat",
    llmAgent,
    runner.WithSessionService(sessionService),
    runner.WithMemoryService(memoryService),
)
```

### Auto Memory Configuration Options

| Option                     | Description                            | Default        |
| -------------------------- | -------------------------------------- | -------------- |
| `WithExtractor(extractor)` | Enable auto mode with LLM extractor    | nil (disabled) |
| `WithAsyncMemoryNum(n)`    | Number of background worker goroutines | 3              |
| `WithMemoryQueueSize(n)`   | Size of memory job queue               | 100            |
| `WithMemoryJobTimeout(d)`  | Timeout for each extraction job        | 30s            |

### Extraction Checkers (>= 1.3.0)

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

memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithChecker(customChecker),
    extractor.WithChecker(extractor.CheckMessageThreshold(10)),
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

### Tool Availability

In auto memory mode, `WithToolEnabled` controls all 6 tools, but they serve different purposes:

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

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable                  | Description                              | Default Value               |
| ------------------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`          | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL`         | Base URL for the model API endpoint      | `https://api.openai.com/v1` |
| `SQLITE_MEMORY_DSN`       | SQLite DSN                               | `file:memories.db?_busy_timeout=5000` |
| `SQLITEVEC_MEMORY_DSN`    | SQLiteVec DSN                            | `file:memories_vec.db?_busy_timeout=5000` |
| `SQLITEVEC_EMBEDDER_MODEL` | SQLiteVec embedder model                 | `text-embedding-3-small`    |
| `OPENAI_EMBEDDING_API_KEY` | API key for embedding model (optional)   | (empty)                     |
| `OPENAI_EMBEDDING_BASE_URL` | Base URL for embedding endpoint (optional) | (empty)                   |
| `OPENAI_EMBEDDING_MODEL`  | Override embedding model name (optional) | (empty)                     |
| `REDIS_ADDR`              | Redis server address                     | `localhost:6379`            |
| `PG_HOST`                 | PostgreSQL host                          | `localhost`                 |
| `PG_PORT`                 | PostgreSQL port                          | `5432`                      |
| `PG_USER`                 | PostgreSQL user                          | `postgres`                  |
| `PG_PASSWORD`             | PostgreSQL password                      | ``                          |
| `PG_DATABASE`             | PostgreSQL database                      | `trpc-agent-go-pgmemory`    |
| `PGVECTOR_HOST`           | pgvector PostgreSQL host                 | `localhost`                 |
| `PGVECTOR_PORT`           | pgvector PostgreSQL port                 | `5432`                      |
| `PGVECTOR_USER`           | pgvector PostgreSQL user                 | `postgres`                  |
| `PGVECTOR_PASSWORD`       | pgvector PostgreSQL password             | ``                          |
| `PGVECTOR_DATABASE`       | pgvector PostgreSQL database             | `trpc-agent-go-pgmemory`    |
| `PGVECTOR_EMBEDDER_MODEL` | pgvector embedder model                  | `text-embedding-3-small`    |
| `MYSQL_HOST`              | MySQL host                               | `localhost`                 |
| `MYSQL_PORT`              | MySQL port                               | `3306`                      |
| `MYSQL_USER`              | MySQL user                               | `root`                      |
| `MYSQL_PASSWORD`          | MySQL password                           | ``                          |
| `MYSQL_DATABASE`          | MySQL database                           | `trpc_agent_go`             |

## Command Line Arguments

| Argument     | Description                                                               | Default Value    |
| ------------ | ------------------------------------------------------------------------- | ---------------- |
| `-model`     | Name of the model for chat responses                                      | `deepseek-chat`  |
| `-ext-model` | Name of the model for memory extraction                                   | Same as `-model` |
| `-memory`    | Memory service type: `inmemory`, `sqlite`, `sqlitevec`, `redis`, `postgres`, `pgvector`, `mysql` | `inmemory` |
| `-streaming` | Enable streaming mode for responses                                       | `true`           |
| `-debug`     | Enable debug mode to print messages sent to model                         | `false`          |

## Usage

### Basic Auto Memory Chat

```bash
cd examples/memory/auto
export OPENAI_API_KEY="your-api-key-here"
go run .
```

### Custom Models

```bash
# Use different models for chat and extraction.
go run . -model gpt-4o -ext-model gpt-4o-mini
```

### Memory Backend Configuration

The auto memory example supports multiple memory backends. Configure the appropriate environment variables and use the `-memory` flag:

```bash
# Default in-memory memory service
go run . -memory inmemory

# SQLite memory service (local file)
export SQLITE_MEMORY_DSN="file:memories.db?_busy_timeout=5000"
go run . -memory sqlite

# SQLiteVec memory service (local file + vector search)
export SQLITEVEC_MEMORY_DSN="file:memories_vec.db?_busy_timeout=5000"
export SQLITEVEC_EMBEDDER_MODEL="text-embedding-3-small"
go run . -memory sqlitevec

# Redis memory service (requires Redis server)
export REDIS_ADDR=localhost:6379
go run . -memory redis

# MySQL memory service (requires MySQL server)
export MYSQL_HOST=localhost
export MYSQL_PASSWORD=password
go run . -memory mysql

# PostgreSQL memory service (requires PostgreSQL server)
export PG_HOST=localhost
export PG_PASSWORD=password
go run . -memory postgres

# pgvector memory service (requires PostgreSQL with pgvector extension)
export PGVECTOR_HOST=localhost
export PGVECTOR_PASSWORD=password
go run . -memory pgvector
```

### Debug Mode

Debug mode shows the messages sent to the model, useful for understanding memory preloading:

```bash
go run . -debug
```

### Non-Streaming Mode

```bash
go run . -streaming=false
```

### Help

```bash
go run . --help
```

Output:

```
Usage of ./auto:
  -debug
        Enable debug mode to print messages sent to model
  -ext-model string
        Model for memory extraction (defaults to chat model)
  -memory string
        Memory service type: inmemory, sqlite, sqlitevec, redis, postgres, pgvector, mysql (default "inmemory")
  -model string
        Model for chat responses (default "deepseek-chat")
  -streaming
        Enable streaming mode for responses (default true)
```

## Chat Interface

The interface is simple and intuitive:

```
🧠 Auto Memory Demo
Chat Model: deepseek-chat
Extractor Model: deepseek-chat
Streaming: true
==================================================

💡 Auto memory mode extracts user information automatically.
   No explicit memory tools are needed - the system learns
   about you from natural conversation.

✅ Auto memory chat ready! Session: auto-memory-session-1703123456

💡 Special commands:
   /memory   - Show what the system remembers about you
   /new      - Start a new session
   /exit     - End the conversation

👤 You: Hi! My name is Alice and I work at TechCorp as a backend engineer.
🤖 Assistant: Hello Alice! Nice to meet you. It's great to connect with a
backend engineer from TechCorp. How can I help you today?

(Background: Extractor analyzes conversation and creates memory automatically)

👤 You: /memory
📚 Stored memories (1):
   1. [abc123] User's name is Alice, works at TechCorp as a backend engineer

👤 You: /new
🆕 Started new session!
   Previous: auto-memory-session-1703123456
   Current:  auto-memory-session-1703123457
   (Memories persist across sessions)

👤 You: What do you know about me?
🔧 Memory tool calls:
   • memory_search (ID: call_xyz789)
     Args: {"query":"user information"}

🔄 Executing...
✅ Tool response (ID: call_xyz789): {"results":[...]}

🤖 Assistant: Based on my memory, I know that your name is Alice and you
work at TechCorp as a backend engineer.

👤 You: /exit
👋 Goodbye!
```

### Session Commands

- `/memory` - Show stored memories for the current user
- `/new` - Start a new session (memories persist across sessions)
- `/exit` - End the conversation

**Note**: Memory extraction runs asynchronously after each assistant response.
If `/memory` shows no entries, wait a few seconds and try again.

## How Auto Memory Works

### 1. Conversation Happens

User has a natural conversation with the agent. No memory tools are called during the response.

### 2. Response Completes

After the agent finishes responding, the Runner triggers memory extraction.

### 3. Async Extraction

A background worker picks up the job and sends the conversation to the extractor.

### 4. LLM Analysis

The extractor (using an LLM) analyzes the conversation and identifies important user information.

### 5. Memory Storage

Extracted memories are automatically added/updated in the memory service.

### 6. Future Conversations

In subsequent conversations, the agent can search these memories to provide personalized responses.

## Memory Preloading

By default, memories are not preloaded into the system prompt. The agent uses tools to access memories when needed. You can enable preloading by configuring `WithPreloadMemory`:

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
)
```

Use `-debug` flag to see preloaded memories in the system prompt.

### Preloading vs memory_load Tool

| Aspect          | WithPreloadMemory                  | memory_load Tool                    |
| --------------- | ---------------------------------- | ----------------------------------- |
| **When**        | Before every request automatically | Agent decides when to call          |
| **Control**     | Configured at agent creation       | Agent-driven, on-demand             |
| **Token Usage** | Always included in context         | Only when agent calls the tool      |
| **Auto Mode**   | Works with preloading              | Disabled by default, can be enabled |
| **Use Case**    | Framework-managed adaptive context | Selective memory access             |

In auto memory mode, `WithPreloadMemory(N)` uses framework-managed adaptive
preloading: small memory sets are injected in full, while larger memory sets
inject only the top `N` search results for the current user message. If
query extraction is empty, the search fails, or the search returns no
matches, it falls back to directly loading up to `N` memories. Use
`WithPreloadMemory(-1)` to force full preload, or enable `memory_load`
via `WithToolEnabled(memory.LoadToolName, true)` for agent-driven
loading.

## Comparison with Manual Memory

### Manual Memory (Parent Directory Example)

```
👤 You: My name is John.
🤖 Assistant: Nice to meet you, John! I'll remember that.

🔧 Tool call: memory_add
   Args: {"memory": "User's name is John", "topics": ["name"]}
✅ Memory added successfully.

🤖 Assistant: I've saved your name. How can I help you?
```

### Auto Memory (This Example)

```
👤 You: My name is John.
🤖 Assistant: Nice to meet you, John! How can I help you today?

(Background: Memory automatically extracted and stored)
```

## Technical Details

### Extractor Implementation

The extractor uses an LLM to analyze conversations:

```go
// Create extractor with custom model.
extractorModel := openai.New("gpt-4o-mini")
memExtractor := extractor.NewExtractor(extractorModel)
```

### Worker Pool

Auto memory uses a worker pool for async processing:

- **Workers**: Configurable number of goroutines (default: 3)
- **Queue**: Bounded job queue to prevent memory issues (default: 100)
- **Timeout**: Per-job timeout for extraction (default: 30s)

### Graceful Shutdown

Always close the memory service to stop workers gracefully:

```go
defer memoryService.Close()
```

## When to Use Auto Memory

**Choose Auto Memory when:**

- Natural conversation flow is important
- Users shouldn't be aware of memory management
- Passive learning about users is preferred
- Background processing is acceptable

**Choose Manual Memory when:**

- Users need explicit control ("Remember that I...")
- Precise decisions on what to store are needed
- Interactive memory management is required
- Immediate memory operations are needed

## See Also

- [Manual Memory Example](../) - Traditional memory tools approach
- [Memory Module Documentation](../../../docs/mkdocs/en/memory.md) - Complete memory guide
