# Memory Usage Guide

## Overview

Memory is the memory management system in the tRPC-Agent-Go framework. It
provides persistent memory and context management for Agents. By integrating
the memory service, session management, and memory tools, the Memory system
helps Agents remember user information, maintain conversation context, and
offer personalized responses across multi-turn dialogs.

### Usage Pattern

The Memory system follows this pattern:

1. Create the Memory Service: configure the storage backend (in-memory or
   Redis).
2. Integrate into the Agent: use `WithMemory()` to attach the Memory Service
   to an LLM Agent.
3. Agent auto-invocation: the Agent manages memory automatically via built-in
   memory tools.
4. Session persistence: memory persists across sessions and supports
   multi-turn dialogs.

This provides:

- Intelligent memory: automatic storage and retrieval based on conversation
  context.
- Multi-turn dialogues: maintain dialog state and memory continuity.
- Flexible storage: supports multiple backends such as in-memory and Redis.
- Tool integration: memory management tools are registered automatically.
- Session management: supports creating, switching, and resetting sessions.

### Agent Integration

Memory integrates with Agents as follows:

- Automatic tool registration: `WithMemory()` automatically adds memory
  management tools.
- Tool invocation: the Agent uses memory tools to store, retrieve, and manage
  information.
- Context enrichment: memory is automatically injected into the Agent's
  context.

## Quick Start

### Requirements

- Go 1.24.1 or later.
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

    // 3. Create the Agent and integrate Memory.
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("An assistant with memory capabilities."),
        llmagent.WithInstruction(
            "Remember important user info and recall it when needed.",
        ),
        llmagent.WithMemory(memoryService), // Automatically adds memory tools.
    )

    // 4. Create the Runner.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
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

Use `llmagent.WithMemory(memoryService)` to integrate the Memory Service with
an Agent. The framework automatically registers memory tools. No custom tool
setup is needed.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
)

// Create the memory service.
memoryService := memoryinmemory.NewMemoryService()

// Create the Agent and integrate Memory.
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An assistant with memory capabilities."),
    llmagent.WithInstruction(
        "Remember important user info and recall it when needed.",
    ),
    llmagent.WithMemory(memoryService), // Automatically adds memory tools.
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

// Pass the service to the Agent.
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithMemory(memService), // Or use redisService.
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

### Custom Memory Instruction Prompt

You can provide a custom instruction builder for memory prompts.

```go
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithInstructionBuilder(
        func(enabledTools []string, defaultPrompt string) string {
            header := "[Memory Instructions] Follow these guidelines to manage user memory.\n\n"
            // Example A: Wrap the default content.
            return header + defaultPrompt
            // Example B: Replace with your own content.
            // return fmt.Sprintf("[Memory Instructions] Tools: %s\n...",
            //     strings.Join(enabledTools, ", "))
        },
    ),
)
```

### Custom Tool Implementation

You can override default tools with custom implementations. See
`memory/tool/tool.go` for reference on how to implement custom tools.

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/memory"
    toolmemory "trpc.group/trpc-go/trpc-agent-go/memory/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// A custom clear tool with a playful output.
func customClearMemoryTool(memoryService memory.Service) tool.Tool {
    clearFunc := func(ctx context.Context, _ struct{}) (
        toolmemory.ClearMemoryResponse, error,
    ) {
        fmt.Println(
            "🧹 [Custom Clear Tool] Running sudo rm -rf /... just kidding! 😄",
        )
        // ... your implementation logic ...
        return toolmemory.ClearMemoryResponse{
            Success: true,
            Message: "🎉 All memories cleared successfully! Just kidding, they are safe.",
        }, nil
    }

    return function.NewFunctionTool(
        clearFunc,
        function.WithName(memory.ClearToolName),
        function.WithDescription(
            "🧹 Custom clear tool: clears all user memory with a playful note.",
        ),
    )
}

// Use the custom tool.
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
            memoryinmemory.WithInstructionBuilder(
                func(enabledTools []string, defaultPrompt string) string {
                    return "[Memory Instructions] Follow these guidelines.\n\n" +
                        defaultPrompt
                },
            ),
            memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
            memoryinmemory.WithCustomTool(
                memory.ClearToolName, customClearMemoryTool,
            ),
        )
    }

    // 2. Create the LLM model.
    modelInstance := openai.New(*modelName)

    // 3. Create the Agent and integrate Memory.
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
        llmagent.WithMemory(memoryService), // Adds memory tools and prompts.
    )

    // 4. Create the Runner.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
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
func customClearMemoryTool(memoryService memory.Service) tool.Tool {
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
