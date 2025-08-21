# 🧠 Multi Turn Chat with Memory

This example demonstrates intelligent memory management using the `Runner` orchestration component with streaming output, session management, and comprehensive memory tool calling functionality.

## What is Memory Chat?

This implementation showcases the essential features for building AI applications with persistent memory capabilities:

- **🧠 Intelligent Memory**: LLM agents can remember and recall user-specific information
- **🔄 Multi-turn Conversations**: Maintains context and memory across multiple exchanges
- **🌊 Flexible Output**: Support for both streaming (real-time) and non-streaming (batch) response modes
- **💾 Session Management**: Conversation state preservation and continuity
- **🔧 Memory Tool Integration**: Working memory tools with proper execution
- **🚀 Simple Interface**: Clean, focused chat experience with memory capabilities
- **⚡ Automatic Integration**: Memory tools are automatically registered via `WithMemory()`
- **🎨 Custom Tool Support**: Ability to override default tool implementations with custom ones
- **⚙️ Configurable Tools**: Enable or disable specific memory tools as needed
- **🔴 Redis Support**: Support for Redis-based memory service (ready to use)

### Key Features

- **Memory Persistence**: The assistant remembers important information about users across sessions
- **Context Preservation**: The assistant maintains conversation context and memory
- **Flexible Response Modes**: Choose between streaming (real-time) or non-streaming (batch) output
- **Session Continuity**: Consistent conversation state and memory across the chat session
- **Memory Tool Execution**: Proper execution and display of memory tool calling procedures
- **Memory Visualization**: Clear indication of memory operations, arguments, and responses
- **Error Handling**: Graceful error recovery and reporting
- **Automatic Tool Registration**: Memory tools are automatically added to the agent via `WithMemory()`
- **Custom Tool Override**: Replace default tool implementations with custom ones
- **Tool Enablement Control**: Enable or disable specific memory tools

## Architecture

### Memory Integration

The memory functionality is integrated using the `WithMemory()` option, which automatically registers all enabled memory tools:

```go
// Create memory service with default tools enabled
memoryService := memoryinmemory.NewMemoryService(
    // Disable specific tools if needed
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
    // Use custom tool implementations
    memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)

// Create LLM agent with automatic memory tool registration
llmAgent := llmagent.New(
    agentName,
    llmagent.WithModel(modelInstance),
    llmagent.WithMemory(memoryService), // Automatic memory tool registration
)

// Create runner (no memory service needed here)
runner := runner.NewRunner(
    appName,
    llmAgent,
    runner.WithSessionService(sessionService),
)
```

### Default Tool Configuration

By default, the following memory tools are enabled:

| Tool Name       | Default Status | Description                   |
| --------------- | -------------- | ----------------------------- |
| `memory_add`    | ✅ Enabled     | Add a new memory entry        |
| `memory_update` | ✅ Enabled     | Update an existing memory     |
| `memory_search` | ✅ Enabled     | Search memories by query      |
| `memory_load`   | ✅ Enabled     | Load recent memories          |
| `memory_delete` | ❌ Disabled    | Delete a memory entry         |
| `memory_clear`  | ❌ Disabled    | Clear all memories for a user |

### Runtime Context Resolution

Memory tools automatically get `appName` and `userID` from the execution context at runtime:

1. **Agent Invocation Context**: Tools first try to get app/user from the agent invocation context
2. **Context Values**: If not found, tools look for `appName` and `userID` in the context values
3. **Default Values**: As a fallback, tools use default values to ensure functionality

This design provides:

- **Framework-Business Decoupling**: The framework doesn't need to know about specific apps and users
- **Multi-tenancy Support**: A single memory service can serve multiple apps and users
- **Runtime Flexibility**: App and user can be determined dynamically at runtime
- **Backward Compatibility**: Default values ensure basic functionality works

### Available Memory Tools

The following memory tools are automatically registered when using `WithMemory()`:

| Tool Name       | Description                   | Parameters                                                                                         |
| --------------- | ----------------------------- | -------------------------------------------------------------------------------------------------- |
| `memory_add`    | Add a new memory entry        | `memory` (string, required), `topics` (array of strings, optional)                                 |
| `memory_update` | Update an existing memory     | `memory_id` (string, required), `memory` (string, required), `topics` (array of strings, optional) |
| `memory_delete` | Delete a memory entry         | `memory_id` (string, required)                                                                     |
| `memory_clear`  | Clear all memories for a user | None                                                                                               |
| `memory_search` | Search memories by query      | `query` (string, required)                                                                         |
| `memory_load`   | Load recent memories          | `limit` (number, optional, default: 10)                                                            |

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument      | Description                                      | Default Value    |
| ------------- | ------------------------------------------------ | ---------------- |
| `-model`      | Name of the model to use                         | `deepseek-chat`  |
| `-memory`     | Memory service: `inmemory` or `redis`            | `inmemory`       |
| `-redis-addr` | Redis server address (when using redis services) | `localhost:6379` |
| `-streaming`  | Enable streaming mode for responses              | `true`           |

## Usage

### Basic Memory Chat

```bash
cd examples/memory
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

**When to use each mode:**

- **Streaming mode** (`-streaming=true`, default): Best for interactive chat where you want to see responses appear in real-time, providing immediate feedback and better user experience.
- **Non-streaming mode** (`-streaming=false`): Better for automated scripts, batch processing, or when you need the complete response before processing it further.

### Service Configuration

Currently, the example supports both in-memory and Redis memory services, while always using in-memory session service for simplicity:

```bash
# Default in-memory memory service
go run main.go

# Redis memory service (ready to use)
go run main.go -memory redis -redis-addr localhost:6379
```

**Available service combinations:**

| Memory Service | Session Service | Status   | Description                      |
| -------------- | --------------- | -------- | -------------------------------- |
| `inmemory`     | `inmemory`      | ✅ Ready | Default configuration            |
| `redis`        | `inmemory`      | ✅ Ready | Redis memory + in-memory session |

### Help and Available Options

To see all available command line options:

```bash
go run main.go --help
```

Output:

```
Usage of ./memory_chat:
  -memory string
        Name of the memory service to use, inmemory / redis (default "inmemory")
  -model string
        Name of the model to use (default "deepseek-chat")
  -redis-addr string
        Redis server address (when using redis services) (default "localhost:6379")
  -streaming
        Enable streaming mode for responses (default true)
```

## Memory Tool Configuration

### Default Tool Enablement

The memory service comes with sensible defaults:

```go
// Default enabled tools: add, update, search, load
// Default disabled tools: delete, clear
memoryService := memoryinmemory.NewMemoryService()

// You can enable disabled tools if needed:
// memoryService := memoryinmemory.NewMemoryService(
//     memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
//     memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
// )
```

### Customizing Tool Enablement

You can enable or disable specific tools:

```go
memoryService := memoryinmemory.NewMemoryService(
    // Enable disabled tools
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
    // Or disable enabled tools
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
)
```

### Custom Memory Instruction Prompt

You can provide a custom memory instruction prompt builder at service creation. The framework generates a default English instruction based on enabled tools; your builder can wrap or replace that default:

```go
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithInstructionBuilder(func(enabledTools []string, defaultPrompt string) string {
        header := "[Memory Instruction] Follow these guidelines to manage user memories.\n\n"
        // Example A: wrap the default content
        return header + defaultPrompt
        // Example B: replace with your own content
        // return fmt.Sprintf("[Memory Instruction] Tools available: %s\n...", strings.Join(enabledTools, ", "))
    }),
)
```

Notes:

- Enabled tools: the set of memory tools currently active for your service. By default, `memory_add`, `memory_update`, `memory_search`, and `memory_load` are enabled; `memory_delete` and `memory_clear` are disabled. Control them with `WithToolEnabled(...)`. The builder’s `enabledTools` argument reflects this list.
- The default prompt already includes tool-specific guidance; your builder receives it via `defaultPrompt`.

```go
// Redis service: enable delete tool.
memoryService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithToolEnabled(memory.DeleteToolName, true),
)
if err != nil {
    // Handle error appropriately.
}
```

### Custom Tool Implementation

You can override default tool implementations with custom ones:

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/memory"
    toolmemory "trpc.group/trpc-go/trpc-agent-go/memory/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Custom clear tool with enhanced logging.
func customClearMemoryTool(memoryService memory.Service) tool.Tool {
    clearFunc := func(ctx context.Context, _ struct{}) (toolmemory.ClearMemoryResponse, error) {
        fmt.Println("🧹 [Custom Clear Tool] Clearing memories with extra sparkle... ✨")
        // ... implementation ...
        return toolmemory.ClearMemoryResponse{
            Success: true,
            Message: "🎉 All memories cleared successfully with custom magic! ✨",
        }, nil
    }

    return function.NewFunctionTool(
        clearFunc,
        function.WithName(memory.ClearToolName),
        function.WithDescription("🧹 Custom clear tool: Clear all memories for the user with extra sparkle! ✨"),
    )
}

// Use custom tool
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)
```

```go
// Or register the custom tool for Redis service.
memoryService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)
if err != nil {
    // Handle error appropriately.
}
```

### Tool Creator Pattern

Custom tools use the `ToolCreator` pattern to avoid circular dependencies:

```go
type ToolCreator func(memory.Service) tool.Tool

// Example custom tool
func myCustomAddTool(memoryService memory.Service) tool.Tool {
    // Implementation that uses memoryService
    return function.NewFunctionTool(/* ... */)
}

// Register custom tool
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithCustomTool(memory.AddToolName, myCustomAddTool),
)
```

## Memory Tool Calling Process

When you share information or ask about memories in a new session, you'll see:

```
🔧 Memory tool calls initiated:
   • memory_add (ID: call_abc123)
     Args: {"memory":"User's name is John and they like coffee","topics":["name","preferences"]}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_abc123): {"success":true,"message":"Memory added successfully","memory":"User's name is John and they like coffee","topics":["name","preferences"]}

🤖 Assistant: I'll remember that your name is John and you like coffee!
```

### Custom Tool Execution

When using custom tools in a new session, you'll see enhanced output:

```
🧹 [Custom Clear Tool] Clearing memories with extra sparkle... ✨
🔧 Memory tool calls initiated:
   • memory_clear (ID: call_def456)
     Args: {}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_def456): {"success":true,"message":"🎉 All memories cleared successfully with custom magic! ✨"}

🤖 Assistant: All your memories have been cleared with extra sparkle! ✨

👤 You: /new
🆕 Started new memory session!
   Previous: memory-session-1703123457
   Current:  memory-session-1703123458
   (Memory and conversation history have been reset)

👤 You: What do you remember about me?
🤖 Assistant: Let me check what I remember about you.

🔧 Memory tool calls initiated:
   • memory_search (ID: call_ghi789)
     Args: {"query":"John"}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_ghi789): {"success":true,"query":"John","count":0,"results":[]}

I don't have any memories about you yet. Could you tell me something about yourself so I can remember it for future conversations?
```

## Chat Interface

The interface is simple and intuitive:

```
🧠 Multi Turn Chat with Memory
Model: gpt-4o-mini
Memory Service: inmemory
Streaming: true
Available tools: memory_add, memory_update, memory_search, memory_load
(memory_delete, memory_clear disabled by default)
==================================================
✅ Memory chat ready! Session: memory-session-1703123456
   Memory Service: inmemory

💡 Special commands:
   /memory   - Show user memories
   /new      - Start a new session
   /exit      - End the conversation

👤 You: Hello! My name is John and I like coffee.
🤖 Assistant: Hello John! Nice to meet you. I'll remember that you like coffee.

👤 You: /new
🆕 Started new memory session!
   Previous: memory-session-1703123456
   Current:  memory-session-1703123457
   (Memory and conversation history have been reset)

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
- `/new` - Start a new session (resets conversation context and memory)
- `/exit` - End the conversation

**Note**: Use `/new` to reset the session when you want to test memory persistence. In the same session, the LLM maintains conversation context, so memory tools may not be called if the information is already in the conversation history.

## Memory Management Features

### Automatic Memory Storage

The LLM agent automatically decides when to store important information about users based on the conversation context.

### Intelligent Memory Retrieval

The agent can search for and retrieve relevant memories when users ask questions or need information recalled.

### Memory Persistence

Memories are stored in-memory and persist across conversation turns within the same session.

### Memory Visualization

All memory operations are clearly displayed, showing:

- Tool calls with arguments
- Tool execution status
- Tool responses with results
- Memory content and metadata

### Custom Tool Enhancements

Custom tools can provide enhanced functionality:

- **Enhanced Logging**: Custom tools can provide more detailed execution logs
- **Special Effects**: Custom tools can add visual indicators (emojis, colors)
- **Extended Functionality**: Custom tools can perform additional operations
- **Better Error Handling**: Custom tools can provide more specific error messages

## Technical Implementation

### Memory Service Integration

- Uses `inmemory.NewMemoryService()` for in-memory storage
- Memory tools directly access the memory service
- No complex integration required - tools handle memory operations
- Automatic tool registration via `WithMemory()`

### Memory Tools Registration

The memory tools are automatically registered when using `WithMemory()`:

```go
// Create memory service with custom configuration
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
    memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)

// Create LLM agent with automatic memory tool registration
llmAgent := llmagent.New(
    agentName,
    llmagent.WithModel(modelInstance),
    llmagent.WithMemory(memoryService), // Automatic registration
)
```

### Lazy Loading

Memory tools are created lazily when first requested:

- **Performance**: Tools are only created when needed
- **Memory Efficiency**: Reduces initial memory footprint
- **Caching**: Created tools are cached for subsequent use
- **Thread Safety**: Uses `sync.RWMutex` for concurrent access

### Tool Creator Pattern

Custom tools use a factory pattern to avoid circular dependencies:

```go
// ToolCreator type for creating tools
type ToolCreator func(memory.Service) tool.Tool

// Default tool creators
var defaultEnabledTools = map[string]ToolCreator{
    memory.AddToolName:    toolmemory.NewAddMemoryTool,
    memory.UpdateToolName: toolmemory.NewUpdateMemoryTool,
    // ... other tools
}

// Custom tool registration
memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool)
```

### Available Memory Tools

**Default Tools:**

- **memory_add**: Allows LLM to actively add user-related memories
- **memory_update**: Allows LLM to update existing memories
- **memory_delete**: Allows LLM to delete specific memories (disabled by default)
- **memory_clear**: Allows LLM to clear all memories
- **memory_search**: Allows LLM to search for relevant memories
- **memory_load**: Allows LLM to load user memory overview

**Custom Tools:**

You can override any default tool with a custom implementation:

```go
// Custom add tool with enhanced logging
func customAddMemoryTool(memoryService memory.Service) tool.Tool {
    addFunc := func(ctx context.Context, req toolmemory.AddMemoryRequest) (toolmemory.AddMemoryResponse, error) {
        fmt.Println("📝 [Custom Add Tool] Adding memory with special care... 💖")
        // ... implementation ...
        return toolmemory.AddMemoryResponse{
            Success: true,
            Message: "💖 Memory added with extra love! 💖",
        }, nil
    }

    return function.NewFunctionTool(
        addFunc,
        function.WithName(memory.AddToolName),
        function.WithDescription("📝 Custom add tool: Add memories with extra care and love! 💖"),
    )
}
```

### Tool Calling Flow

1. LLM decides when to use memory tools based on user input
2. Calls appropriate memory tools (add/update/delete/clear/search/load)
3. Tools execute and return results
4. LLM generates personalized responses based on memory data

## Architecture Overview

```
User Input → Runner → Agent → Memory Tools → Memory Service → Response
```

- **Runner**: Orchestrates the conversation flow
- **Agent**: Understands user intent and decides which memory tools to use
- **Memory Tools**: LLM-callable memory interface (default or custom)
- **Memory Service**: Actual memory storage and management

## Redis Memory Service

### Redis Support

The example now supports Redis-based memory service for persistent storage:

```go
// Redis memory service
memoryService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithToolEnabled(memory.DeleteToolName, false),
    memoryredis.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)

// Session service always uses in-memory for simplicity
sessionService := sessioninmemory.NewSessionService()
```

**Benefits of Redis support:**

- **Persistence**: Memories survive application restarts
- **Scalability**: Support for multiple application instances
- **Performance**: Redis optimized for high-throughput operations
- **Clustering**: Support for Redis cluster and sentinel
- **Monitoring**: Built-in Redis monitoring and metrics

### Redis Configuration

To use Redis memory service, you need a running Redis instance:

```bash
# Start Redis with Docker (recommended for testing)
docker run -d --name redis-memory -p 6379:6379 redis:7-alpine
```

**Usage examples:**

```bash
# Connect to default Redis port (6379)
go run main.go -memory redis

# Connect to custom Redis port
go run main.go -memory redis -redis-addr localhost:6380

# Connect to Redis with authentication
go run main.go -memory redis -redis-addr redis://username:password@localhost:6379
```

## Extensibility

This example demonstrates how to:

1. Integrate memory tools into existing systems
2. Add memory capabilities to agents
3. Handle memory tool calls and responses
4. Manage user memory storage and retrieval
5. Create custom memory tools with enhanced functionality
6. Configure tool enablement and custom implementations
7. Use lazy loading for better performance
8. Use Redis memory service for persistent storage

Future enhancements could include:

- Persistent storage (database integration)
- Memory expiration and cleanup
- Memory priority and relevance scoring
- Automatic memory summarization and compression
- Vector-based semantic memory search
- Custom memory tool implementations with specialized functionality
- Tool enablement configuration via configuration files
- Dynamic tool registration and unregistration
- Redis cluster and sentinel support
- Memory replication and synchronization
- Advanced memory analytics and insights
