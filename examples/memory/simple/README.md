# Simple Memory Chat

This example demonstrates simple memory management using the `Runner` orchestration component with manual memory tool calling.

## Overview

This is the simple memory example that shows how to integrate memory capabilities using explicit tool registration. The LLM agent can:

- Add new memories about the user
- Update existing memories
- Search for relevant memories
- Load recent memories
- Clear all memories (with custom tool)

## What is Simple Memory Chat?

This implementation showcases the essential features for building AI applications with memory capabilities:

- **🧠 Manual Memory**: LLM agent explicitly calls memory tools when needed
- **🔄 Multi-turn Conversations**: Maintains context across multiple exchanges
- **🌊 Flexible Output**: Support for both streaming and non-streaming response modes
- **💾 Session Management**: Conversation state preservation and continuity
- **🔧 Memory Tool Integration**: Working memory tools with proper execution
- **⚡ Manual Integration**: Memory tools are manually registered for explicit control
- **🎨 Custom Tool Support**: Ability to override default tool implementations with custom ones

### Key Features

- **Memory Persistence**: The assistant remembers important information about users across sessions
- **Context Preservation**: The assistant maintains conversation context and memory
- **Flexible Response Modes**: Choose between streaming or non-streaming output
- **Memory Tool Execution**: Proper execution and display of memory tool calling procedures
- **Memory Visualization**: Clear indication of memory operations, arguments, and responses
- **Manual Tool Registration**: Memory tools are explicitly registered for better control
- **Custom Tool Override**: Replace default tool implementations with custom ones

## Architecture

### Design

This implementation follows principles for explicit control and manual tool registration:

- **Explicit Tool Registration**: Memory tools are manually registered via `llmagent.WithTools(memoryService.Tools())`
- **Service Management**: Memory service is managed at the runner level via `runner.WithMemoryService(memoryService)`
- **Business Logic Control**: Applications have full control over which tools to register and how to use them
- **Clear Separation**: Framework provides building blocks, business logic decides how to use them

### Memory Integration

The memory functionality is integrated using a two-step approach:

```go
// Create memory service
memoryService := memoryinmemory.NewMemoryService()

// Create LLM agent with manual memory tool registration
llmAgent := llmagent.New(
    agentName,
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(memoryService.Tools()), // Step 1: Register memory tools
)

// Create runner with memory service
runner := runner.NewRunner(
    appName,
    llmAgent,
    runner.WithSessionService(sessionService),
    runner.WithMemoryService(memoryService), // Step 2: Set memory service in runner
)
```

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

### Memory Service Environment Variables

| Variable                  | Description                  | Default Value            |
| ------------------------- | ---------------------------- | ------------------------ |
| `SQLITE_MEMORY_DSN`       | SQLite DSN                   | `file:memories.db?_busy_timeout=5000` |
| `SQLITEVEC_MEMORY_DSN`    | SQLiteVec DSN                | `file:memories_vec.db?_busy_timeout=5000` |
| `SQLITEVEC_EMBEDDER_MODEL` | SQLiteVec embedder model     | `text-embedding-3-small` |
| `REDIS_ADDR`              | Redis server address         | `localhost:6379`         |
| `PG_HOST`                 | PostgreSQL host              | `localhost`              |
| `PG_PORT`                 | PostgreSQL port              | `5432`                   |
| `PG_USER`                 | PostgreSQL user              | `postgres`               |
| `PG_PASSWORD`             | PostgreSQL password          | ``                       |
| `PG_DATABASE`             | PostgreSQL database name     | `trpc-agent-go-pgmemory` |
| `PGVECTOR_HOST`           | pgvector PostgreSQL host     | `localhost`              |
| `PGVECTOR_PORT`           | pgvector PostgreSQL port     | `5432`                   |
| `PGVECTOR_USER`           | pgvector PostgreSQL user     | `postgres`               |
| `PGVECTOR_PASSWORD`       | pgvector PostgreSQL password | ``                       |
| `PGVECTOR_DATABASE`       | pgvector PostgreSQL database | `trpc-agent-go-pgmemory` |
| `PGVECTOR_EMBEDDER_MODEL` | pgvector embedder model      | `text-embedding-3-small` |
| `MYSQL_HOST`              | MySQL host                   | `localhost`              |
| `MYSQL_PORT`              | MySQL port                   | `3306`                   |
| `MYSQL_USER`              | MySQL user                   | `root`                   |
| `MYSQL_PASSWORD`          | MySQL password               | ``                       |
| `MYSQL_DATABASE`          | MySQL database name          | `trpc_agent_go`          |

### Embedding Environment Variables (Optional)

When using vector backends (`sqlitevec`, `pgvector`), you can configure a
separate embedding endpoint / API key:

| Variable                  | Description                      | Default Value |
| ------------------------- | -------------------------------- | ------------- |
| `OPENAI_EMBEDDING_API_KEY` | API key for embedding model      | (empty)       |
| `OPENAI_EMBEDDING_BASE_URL` | Base URL for embedding endpoint  | (empty)       |
| `OPENAI_EMBEDDING_MODEL`  | Override embedding model name     | (empty)       |

## Command Line Arguments

| Argument       | Description                                                             | Default Value   |
| -------------- | ----------------------------------------------------------------------- | --------------- |
| `-model`       | Name of the model to use                                                | `deepseek-chat` |
| `-memory`      | Memory service: `inmemory`, `sqlite`, `sqlitevec`, `redis`, `mysql`, `postgres`, or `pgvector` | `inmemory` |
| `-soft-delete` | Enable soft delete for SQLite/SQLiteVec/MySQL/PostgreSQL/pgvector memory service  | `false`         |
| `-streaming`   | Enable streaming mode for responses                                     | `true`          |

## Usage

### Basic Memory Chat

```bash
cd examples/memory/simple
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run main.go -model gpt-4o
```

### Using Environment Variable

If you have `MODEL_NAME` set in your environment:

```bash
source ~/.bashrc && go run main.go -model "$MODEL_NAME"
```

### Response Modes

Choose between streaming and non-streaming responses:

```bash
# Default streaming mode (real-time character output)
go run main.go

# Non-streaming mode (complete response at once)
go run main.go -streaming=false

# Combined with other options
go run main.go -model gpt-4o -streaming=false
```

### Service Configuration

The example supports multiple memory service backends:

```bash
# Default in-memory memory service
go run main.go

# SQLite memory service (local file)
export SQLITE_MEMORY_DSN="file:memories.db?_busy_timeout=5000"
go run main.go -memory sqlite

# SQLiteVec memory service (local file + vector search)
export SQLITEVEC_MEMORY_DSN="file:memories_vec.db?_busy_timeout=5000"
export SQLITEVEC_EMBEDDER_MODEL="text-embedding-3-small"
go run main.go -memory sqlitevec

# Redis memory service (using default or environment variable)
go run main.go -memory redis

# MySQL memory service (using environment variables)
export MYSQL_HOST=localhost
export MYSQL_PORT=3306
export MYSQL_USER=root
export MYSQL_PASSWORD=password
export MYSQL_DATABASE=trpc_agent_go
go run main.go -memory mysql

# PostgreSQL memory service (using environment variables)
export PG_HOST=localhost
export PG_PORT=5432
export PG_USER=postgres
export PG_PASSWORD=""
export PG_DATABASE=trpc-agent-go-pgmemory
go run main.go -memory postgres

# pgvector memory service (using environment variables)
export PGVECTOR_HOST=localhost
export PGVECTOR_PORT=5432
export PGVECTOR_USER=postgres
export PGVECTOR_PASSWORD=""
export PGVECTOR_DATABASE=trpc-agent-go-pgmemory
export PGVECTOR_EMBEDDER_MODEL=text-embedding-3-small
go run main.go -memory pgvector
```

## Chat Interface

The interface is simple and intuitive:

```
🧠 Simple Memory Chat
Model: gpt-4o-mini
Memory Service: inmemory
Streaming: true
Available tools: memory_add, memory_update, memory_search, memory_load
(memory_delete, memory_clear disabled by default, can be enabled or customized)
==================================================
✅ Memory chat ready! Session: memory-session-1703123456
   Memory Service: inmemory

💡 Special commands:
   /memory   - Show user memories
   /new      - Start a new session
   /exit     - End the conversation

👤 You: Hello! My name is John and I like coffee.
🤖 Assistant: Hello John! Nice to meet you. I'll remember that you like coffee.

🔧 Memory tool calls initiated:
   • memory_add (ID: call_abc123)
     Args: {"memory":"User's name is John and they like coffee","topics":["name","preferences"]}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_abc123): {"success":true,"message":"Memory added successfully","memory":"User's name is John and they like coffee","topics":["name","preferences"]}

👤 You: /new
🆕 Started new memory session!
   Previous: memory-session-1703123456
   Current:  memory-session-1703123457
   (Conversation history has been reset, memories are preserved)

👤 You: What do you remember about me?
🤖 Assistant: Let me check what I remember about you.

🔧 Memory tool calls initiated:
   • memory_search (ID: call_def456)
     Args: {"query":"John"}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_def456): {"success":true,"query":"John","count":1,"results":[{"id":"abc123","memory":"User's name is John and they like coffee","topics":["name","preferences"],"created":"2025-01-28 20:30:00"}]}

Based on my memory, I know:
- Your name is John
- You like coffee

👤 You: /exit
👋 Goodbye!
```

### Session Commands

- `/memory` - Ask the agent to show stored memories
- `/new` - Start a new session (resets conversation history, memories are preserved)
- `/exit` - End the conversation

## Memory Management Features

### Manual Memory Storage

The LLM agent explicitly calls memory tools when it decides to store important information about users based on the conversation context.

### Memory Retrieval

The agent can search for and retrieve relevant memories when users ask questions or need information recalled.

### Memory Visualization

All memory operations are clearly displayed, showing:

- Tool calls with arguments
- Tool execution status
- Tool responses with results
- Memory content and metadata

### Custom Tool Enhancements

The example demonstrates a custom clear tool with enhanced logging:

- **Enhanced Logging**: Custom tools can provide more detailed execution logs
- **Special Effects**: Custom tools can add visual indicators (emojis, colors)
- **Extended Functionality**: Custom tools can perform additional operations
- **Better Error Handling**: Custom tools can provide more specific error messages

## Technical Implementation

### Memory Service Integration

- Supports multiple backends: in-memory, Redis, MySQL, PostgreSQL, and pgvector
- Uses `memoryinmemory.NewMemoryService()` for in-memory storage
- Uses `memoryredis.NewService()` for Redis-based storage
- Uses `memorymysql.NewService()` for MySQL-based storage
- Uses `memorypostgres.NewService()` for PostgreSQL-based storage
- Uses `memorypgvector.NewService()` for pgvector-based storage with vector similarity search
- Memory tools directly access the memory service
- Two-step integration: Step 1 (manual tool registration) + Step 2 (runner service setup)

### Available Memory Tools

**Default Tools:**

- **memory_add**: Allows LLM to actively add user-related memories
- **memory_update**: Allows LLM to update existing memories
- **memory_delete**: Allows LLM to delete specific memories (disabled by default)
- **memory_clear**: Allows LLM to clear all memories (custom implementation)
- **memory_search**: Allows LLM to search for relevant memories
- **memory_load**: Allows LLM to load user memory overview

## Architecture Overview

```
User Input → Runner → Agent → Memory Tools → Memory Service → Response
```

- **Runner**: Orchestrates the conversation flow
- **Agent**: Understands user intent and decides which memory tools to use
- **Memory Tools**: LLM-callable memory interface
- **Memory Service**: Actual memory storage and management

## Comparison with Auto Memory

| Feature           | Simple Memory | Auto Memory |
| ----------------- | ------------- | ----------- |
| Tool Registration | Manual        | Automatic   |
| Memory Extraction | LLM decides   | Background  |
| Tools Available   | All tools     | Limited     |
| Control           | High          | Medium      |
| Setup Complexity  | Simple        | Complex     |

Use **Simple Memory** when you want:

- Explicit control over memory operations
- Full access to all memory tools
- Simpler setup and understanding

Use **Auto Memory** (see `../auto/`) when you want:

- Automatic background memory extraction
- Transparent memory management
- Reduced manual tool configuration

## Extensibility

This example demonstrates how to:

1. Integrate memory tools into existing systems
2. Add memory capabilities to agents
3. Handle memory tool calls and responses
4. Manage user memory storage and retrieval
5. Create custom memory tools with enhanced functionality

Future enhancements could include:

- Memory expiration and cleanup
- Memory priority and relevance scoring
- Automatic memory summarization
- Vector-based semantic memory search
- Custom memory tool implementations
