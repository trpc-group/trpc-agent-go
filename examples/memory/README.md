# Memory Examples

This directory contains examples demonstrating memory management capabilities in trpc-agent-go, showing different approaches to integrating memory with AI agents.

## Overview

Memory enables AI agents to remember and recall information across conversations, creating more personalized and context-aware interactions. These examples showcase two primary approaches:

1. **Agentic Mode (Simple)** - Manual memory tool calling with explicit control
2. **Auto Mode** - Automatic background memory extraction

## Available Examples

### 📁 simple/

**Agentic Mode - Manual Memory Tool Calling**

A simple example that demonstrates manual memory tool integration where LLM agent explicitly calls memory tools when needed.

**Key Features:**

- Manual tool registration and control
- Access to 6 memory tools (default: add, update, search, load; configurable:
  delete, clear)
- Custom tool implementations
- Streaming and non-streaming response modes
- Multiple storage backends (in-memory, SQLite, Redis, MySQL, PostgreSQL, pgvector)

**Use Cases:**

- When you want explicit control over memory operations
- When you need comprehensive memory tool access with configurable options
- When you prefer simpler setup and configuration

**Getting Started:**

```bash
cd examples/memory/simple
export OPENAI_API_KEY="your-api-key"
go run main.go
```

[Read full documentation →](./simple/README.md)

### 📁 auto/

**Auto Mode - Automatic Memory Extraction**

An advanced example that demonstrates automatic memory extraction running in background, without explicit tool calls.

**Key Features:**

- Automatic background memory extraction
- LLM analyzes conversations to extract memories
- Configurable extraction checkers (message threshold, time interval)
- Reduced manual tool configuration
- Memory preloading into system prompt

**Use Cases:**

- When you want transparent memory management
- When you want automatic learning from conversations
- When you want to minimize explicit memory operations

**Getting Started:**

```bash
cd examples/memory/auto
export OPENAI_API_KEY="your-api-key"
go run main.go
```

[Read full documentation →](./auto/README.md)

### 📁 compare/

**Retrieval Comparison - SQLite vs SQLiteVec**

A small example that compares keyword-based SQLite memory (`sqlite`) with
semantic vector memory (`sqlitevec`) powered by `sqlite-vec`.

**Getting Started:**

```bash
cd examples/memory/compare
export OPENAI_API_KEY="your-api-key"
go run .
```

[Read full documentation →](./compare/README.md)

## Common Features

The chat examples (`simple/` and `auto/`) share the following capabilities:

### Memory Services

All examples support multiple storage backends:

| Backend    | Description                                 | Usage              |
| ---------- | ------------------------------------------- | ------------------ |
| `inmemory` | In-memory storage (default)                 | `-memory=inmemory` |
| `sqlite`   | SQLite file storage                         | `-memory=sqlite`   |
| `sqlitevec` | SQLite + sqlite-vec vector search (embeddings) | `-memory=sqlitevec` |
| `redis`    | Redis-based storage                         | `-memory=redis`    |
| `mysql`    | MySQL-based storage                         | `-memory=mysql`    |
| `postgres` | PostgreSQL-based storage                    | `-memory=postgres` |
| `pgvector` | pgvector PostgreSQL storage with embeddings | `-memory=pgvector` |

### Session Management

- Multi-turn conversations with context preservation
- Session isolation and switching
- Session history tracking

### Streaming Output

- Real-time streaming responses (default)
- Batch mode for complete responses
- Configurable via `-streaming` flag

### Tool Visualization

- Clear display of memory tool calls
- Tool execution status and responses
- Arguments and results visibility

## Comparison

| Feature           | Agentic Mode (Simple)               | Auto Mode (Auto)                |
| ----------------- | ----------------------------------- | ------------------------------- |
| Tool Registration | Manual (`WithTools`)                | Automatic (`WithExtractor`)     |
| Memory Extraction | Agent calls tools directly          | Background extraction           |
| Tools Available   | 6 tools (4 default, 2 configurable) | Limited (search, optional load) |
| Control Level     | High (explicit)                     | Medium (background)             |
| Setup Complexity  | Simple                              | Complex                         |
| Best For          | Fine-grained control needs          | Transparent memory needs        |

## Memory Tools

Memory provides 6 tools with different availability in each mode:

| Tool            | Function       | Agentic Mode (Simple) | Auto Extraction Mode (Auto) | Description             |
| --------------- | -------------- | --------------------- | --------------------------- | ----------------------- |
| `memory_add`    | Add new memory | ✅ Default            | ❌ Unavailable              | Create new memory entry |
| `memory_update` | Update memory  | ✅ Default            | ❌ Unavailable              | Modify existing memory  |
| `memory_search` | Search memory  | ✅ Default            | ✅ Default                  | Search relevant memories |
| `memory_load`   | Load memories  | ✅ Default            | ⚙️ Configurable             | Load recent memories    |
| `memory_delete` | Delete memory  | ⚙️ Configurable       | ❌ Unavailable              | Delete single memory    |
| `memory_clear`  | Clear memories | ⚙️ Configurable       | ❌ Unavailable              | Delete all memories     |

**Notes:**

- **Agentic Mode (Simple)**: Agent actively calls tools to manage memory, all tools are configurable
  - Default enabled: `memory_add`, `memory_update`, `memory_search`, `memory_load`
  - Default disabled: `memory_delete`, `memory_clear`
  - Can be enabled/disabled via `WithToolEnabled()`
- **Auto Mode**: LLM extractor handles write operations in background, only read tools are available
  - Default enabled: `memory_search`
  - Default disabled: `memory_load`
  - Not exposed: `memory_add`, `memory_update`, `memory_delete`, `memory_clear` (extractor handles writes)
  - `WithToolEnabled()` only affects `memory_search` and `memory_load` availability

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

### Required

| Variable         | Description               | Default |
| ---------------- | ------------------------- | ------- |
| `OPENAI_API_KEY` | API key for model service | (empty) |

### Optional

| Variable                  | Description                  | Default                     |
| ------------------------- | ---------------------------- | --------------------------- |
| `OPENAI_BASE_URL`         | Base URL for model API       | `https://api.openai.com/v1` |
| `SQLITE_MEMORY_DSN`       | SQLite DSN for memory store  | `file:memories.db?_busy_timeout=5000` |
| `SQLITEVEC_MEMORY_DSN`    | SQLiteVec DSN for memory store | `file:memories_vec.db?_busy_timeout=5000` |
| `SQLITEVEC_EMBEDDER_MODEL` | Embedder model for SQLiteVec | `text-embedding-3-small` |
| `OPENAI_EMBEDDING_API_KEY` | API key for embedding model (optional) | (empty) |
| `OPENAI_EMBEDDING_BASE_URL` | Base URL for embedding API (optional) | (empty) |
| `OPENAI_EMBEDDING_MODEL`  | Override embedding model name (optional) | (empty) |
| `REDIS_ADDR`              | Redis server address         | `localhost:6379`            |
| `PG_HOST`                 | PostgreSQL host              | `localhost`                 |
| `PG_PORT`                 | PostgreSQL port              | `5432`                      |
| `PG_USER`                 | PostgreSQL user              | `postgres`                  |
| `PG_PASSWORD`             | PostgreSQL password          | (empty)                     |
| `PG_DATABASE`             | PostgreSQL database          | `trpc-agent-go-pgmemory`    |
| `PGVECTOR_HOST`           | pgvector PostgreSQL host     | `localhost`                 |
| `PGVECTOR_PORT`           | pgvector PostgreSQL port     | `5432`                      |
| `PGVECTOR_USER`           | pgvector PostgreSQL user     | `postgres`                  |
| `PGVECTOR_PASSWORD`       | pgvector PostgreSQL password | (empty)                     |
| `PGVECTOR_DATABASE`       | pgvector PostgreSQL database | `trpc-agent-go-pgmemory`    |
| `PGVECTOR_EMBEDDER_MODEL` | pgvector embedder model      | `text-embedding-3-small`    |
| `MYSQL_HOST`              | MySQL host                   | `localhost`                 |
| `MYSQL_PORT`              | MySQL port                   | `3306`                      |
| `MYSQL_USER`              | MySQL user                   | `root`                      |
| `MYSQL_PASSWORD`          | MySQL password               | (empty)                     |
| `MYSQL_DATABASE`          | MySQL database               | `trpc_agent_go`             |

## Quick Start

### 1. Set up your API key

```bash
export OPENAI_API_KEY="your-api-key-here"
```

### 2. Choose your example

**Agentic Mode (Simple):**

```bash
cd examples/memory/simple
go run main.go
```

**Auto Mode:**

```bash
cd examples/memory/auto
go run main.go
```

### 3. Interact with the agent

Both examples provide an interactive chat interface:

- Type your message and press Enter
- Use `/memory` to view stored memories
- Use `/new` to start a new session
- Use `/exit` to exit

## Advanced Usage

### Using Different Memory Backends

```bash
# Default in-memory memory service
go run main.go

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

### Custom Models

```bash
# Using a specific model
go run main.go -model=gpt-4o
```

### Non-streaming Mode

```bash
# Get complete responses at once
go run main.go -streaming=false
```

## Architecture

### Memory Integration Pattern

Both examples follow a two-step memory integration pattern:

```go
// Step 1: Register memory tools (agentic) or setup extractor (auto)
llmAgent := llmagent.New(
    agentName,
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(memoryService.Tools()), // Agentic mode
    // OR
    llmagent.WithPreloadMemory(-1), // Auto mode
)

// Step 2: Set memory service in runner
runner := runner.NewRunner(
    appName,
    llmAgent,
    runner.WithSessionService(sessionService),
    runner.WithMemoryService(memoryService),
)
```

### Memory Flow

```
User Input
    ↓
Runner
    ↓
Agent (LLM)
    ↓
[Agentic: Tool Calls] OR [Auto: Background Extraction]
    ↓
Memory Service
    ↓
Storage Backend (InMemory/Redis/MySQL/PostgreSQL)
```

## Extensibility

### Custom Memory Tools

Both examples support custom memory tool implementations:

```go
func customMemoryTool() tool.Tool {
    return function.NewFunctionTool(
        customFunc,
        function.WithName(memory.CustomToolName),
        function.WithDescription("Custom memory operation"),
    )
}

memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithCustomTool(memory.CustomToolName, customMemoryTool),
)
```

### Tool Enablement and Configuration

You can enable or disable specific memory tools and use custom implementations:

#### Default Tool Status

| Tool            | Default Status | Description                   |
| --------------- | -------------- | ----------------------------- |
| `memory_add`    | ✅ Enabled     | Add a new memory entry        |
| `memory_update` | ✅ Enabled     | Update an existing memory     |
| `memory_search` | ✅ Enabled     | Search memories by query      |
| `memory_load`   | ✅ Enabled     | Load recent memories          |
| `memory_delete` | ❌ Disabled    | Delete a memory entry         |
| `memory_clear`  | ❌ Disabled    | Clear all memories for a user |

#### Enabling/Disabling Tools

```go
// Enable delete tool
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
)

// Enable clear tool
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)

// Enable both delete and clear tools
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

#### Using Custom Tools

```go
// Custom clear tool example
func customClearMemoryTool() tool.Tool {
    clearFunc := func(ctx context.Context, _ *toolmemory.ClearMemoryRequest) (*toolmemory.ClearMemoryResponse, error) {
        fmt.Println("🧹 [Custom Clear Tool] Clearing memories...")

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

        return &toolmemory.ClearMemoryResponse{
            Message: "✅ Memories cleared successfully!",
        }, nil
    }

    return function.NewFunctionTool(
        clearFunc,
        function.WithName(memory.ClearToolName),
        function.WithDescription("🧹 Clear all memories for the user"),
    )
}

// Use custom clear tool
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)
```

#### Backend-Specific Options

Each backend supports the same tool configuration options. See the source code comments in `util.go` for backend-specific usage examples.

### Custom Extraction Checkers (Auto Mode)

```go
memExtractor := extractor.NewExtractor(
    extractModel,
    // Extract when messages > 5 OR every 3 minutes
    extractor.WithCheckersAny(
        extractor.CheckMessageThreshold(5),
        extractor.CheckTimeInterval(3*time.Minute),
    ),
)
```

## Best Practices

1. **Choose the Right Mode**:

   - Use Agentic Mode (Simple) for explicit control and full tool access
   - Use Auto Mode for transparent learning and reduced configuration

2. **Memory Persistence**:

   - Use in-memory for testing and development
   - Use Redis for production with scalability needs
   - Use MySQL/PostgreSQL for relational queries and analytics

3. **Session Management**:

   - Use `/new` to reset conversation context
   - Memories persist across sessions by default
   - Consider memory cleanup for production use

4. **Performance**:

   - Use streaming for better user experience
   - Configure extraction checkers to balance CPU and memory
   - Monitor memory usage in production

## Troubleshooting

### Memory Not Working

1. Check if memory service is properly initialized
2. Verify environment variables for storage backends
3. Check if memory tools are enabled
4. Review logs for tool call execution

### Connection Issues

1. Verify storage backend is running
2. Check connection parameters (host, port, credentials)
3. Ensure network connectivity
4. Review firewall rules

### Extraction Issues (Auto Mode)

1. Verify extraction model is accessible
2. Check extraction checker configuration
3. Review background worker settings
4. Monitor extraction queue and timeout

## Additional Resources

- [trpc-agent-go Documentation](https://github.com/trpc-group/trpc-agent-go)
- [Memory Package Documentation](../../memory)
- [Session Examples](../session)
- [OpenAI API Documentation](https://platform.openai.com/docs)

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

## License

Copyright (C) 2025 Tencent. All rights reserved.

trpc-agent-go is licensed under Apache License Version 2.0.
