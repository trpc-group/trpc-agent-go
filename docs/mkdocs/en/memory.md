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

Configure the memory service in code. Two backends are supported: in-memory
and Redis.

#### Configuration Example

```go
import (
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
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

// Register memory tools with the Agent.
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithTools(memService.Tools()), // Or use redisService.Tools().
)

// Set memory service in the Runner.
runner := runner.NewRunner(
    "app",
    llmAgent,
    runner.WithMemoryService(memService), // Or use redisService.
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
# -memory: memory service type (inmemory, redis), default is inmemory.
# -redis-addr: Redis server address, default is localhost:6379.
# -model: model name, default is deepseek-chat.
```
