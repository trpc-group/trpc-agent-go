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

### Key Features

- **Memory Persistence**: The assistant remembers important information about users across sessions
- **Context Preservation**: The assistant maintains conversation context and memory
- **Flexible Response Modes**: Choose between streaming (real-time) or non-streaming (batch) output
- **Session Continuity**: Consistent conversation state and memory across the chat session
- **Memory Tool Execution**: Proper execution and display of memory tool calling procedures
- **Memory Visualization**: Clear indication of memory operations, arguments, and responses
- **Error Handling**: Graceful error recovery and reporting

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument     | Description                         | Default Value   |
| ------------ | ----------------------------------- | --------------- |
| `-model`     | Name of the model to use            | `deepseek-chat` |
| `-streaming` | Enable streaming mode for responses | `true`          |

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

### Help and Available Options

To see all available command line options:

```bash
go run main.go --help
```

Output:

```
Usage of ./memory_chat:
  -model string
        Name of the model to use (default "deepseek-chat")
  -streaming
        Enable streaming mode for responses (default true)
```

## Implemented Memory Tools

The example includes six comprehensive memory tools:

### 🧠 Memory Add Tool

- **Function**: `memory_add`
- **Purpose**: Store important information about the user
- **Usage**: "Remember that I like coffee" or "My name is John"
- **Arguments**: memory (string), input (string), topics (optional array)

### 🔄 Memory Update Tool

- **Function**: `memory_update`
- **Purpose**: Update existing memories with new information
- **Usage**: "Update my coffee preference to decaf" or "I now work at Google"
- **Arguments**: memory_id (string), memory (string), input (string), topics (optional array)

### 🗑️ Memory Delete Tool

- **Function**: `memory_delete`
- **Purpose**: Remove specific memories
- **Usage**: "Forget about my old job" or "Delete my coffee preference"
- **Arguments**: memory_id (string)

### 🧹 Memory Clear Tool

- **Function**: `memory_clear`
- **Purpose**: Clear all memories for the user
- **Usage**: "Forget everything about me" or "Clear all my memories"
- **Arguments**: None

### 🔍 Memory Search Tool

- **Function**: `memory_search`
- **Purpose**: Find relevant memories based on a query
- **Usage**: "What do you remember about my preferences?" or "Search for coffee"
- **Arguments**: query (string)

### 📋 Memory Load Tool

- **Function**: `memory_load`
- **Purpose**: Get an overview of all stored memories
- **Usage**: "Show me what you remember about me" or "Load my memories"
- **Arguments**: limit (optional integer)

## Memory Tool Calling Process

When you share information or ask about memories, you'll see:

```
🔧 Memory tool calls initiated:
   • memory_add (ID: call_abc123)
     Args: {"memory":"User's name is John and they like coffee","input":"Hello! My name is John and I like coffee.","topics":["name","preferences"]}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_abc123): {"success":true,"message":"Memory added successfully","memory":"User's name is John and they like coffee","input":"Hello! My name is John and I like coffee.","topics":["name","preferences"]}

🤖 Assistant: I'll remember that your name is John and you like coffee!
```

## Chat Interface

The interface is simple and intuitive:

```
🧠 Multi Turn Chat with Memory
Model: gpt-4o-mini
Streaming: true
Available tools: memory_add, memory_update, memory_delete, memory_clear, memory_search, memory_load
==================================================
✅ Memory chat ready! Session: memory-session-1703123456

💡 Special commands:
   /memory   - Show user memories
   /new      - Start a new session
   /exit      - End the conversation

👤 You: Hello! My name is John and I like coffee.
🤖 Assistant: Hello John! Nice to meet you. I'll remember that you like coffee.

👤 You: What do you remember about me?
🤖 Assistant: Let me check what I remember about you.

🔧 Memory tool calls initiated:
   • memory_load (ID: call_def456)
     Args: {"limit":10}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_def456): {"success":true,"count":1,"memories":[{"id":"abc123","memory":"User's name is John and they like coffee","created":"2025-01-28 20:30:00"}]}

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

## Technical Implementation

### Memory Service Integration

- Uses `inmemory.NewMemoryService()` for in-memory storage
- Memory tools directly access the memory service
- No complex integration required - tools handle memory operations

### Memory Tools Registration

When using MemoryService, you need to register the memory tools manually:

```go
// Create memory service.
memoryService := memoryinmemory.NewMemoryService()

// Create memory tools with the new interface.
appName := "memory-chat"
userID := "user"
memoryTools := toolmemory.NewMemoryTools(memoryService, appName, userID)

// Create agent with memory tools.
agent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A helpful AI assistant with memory capabilities."),
    llmagent.WithInstruction("Use memory tools to provide personalized assistance."),
    llmagent.WithTools(memoryTools), // Register the memory tools.
)

// Create runner with memory service.
runner := runner.NewRunner(
    appName,
    agent,
    runner.WithMemoryService(memoryService),
)
```

**Available Memory Tools:**

- **memory_add**: Allows LLM to actively add user-related memories
- **memory_update**: Allows LLM to update existing memories
- **memory_delete**: Allows LLM to delete specific memories
- **memory_clear**: Allows LLM to clear all memories
- **memory_search**: Allows LLM to search for relevant memories
- **memory_load**: Allows LLM to load user memory overview

**Note:** Currently, memory tools need to be manually registered with the agent. The MemoryService in the runner is used for storage and management, but the tools must be explicitly added to the agent's tool list.

### Custom Memory Tools

You can also create custom memory tools using the Options pattern:

```go
// Create custom memory add tool with enhanced logging.
customAddTool := NewExampleCustomAddTool(memoryService, appName, userID, true)

// Use custom tools with the Options pattern.
memoryTools := toolmemory.NewMemoryTools(
    memoryService, appName, userID,
    toolmemory.WithAddTool(customAddTool),
)
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
- **Memory Tools**: LLM-callable memory interface
- **Memory Service**: Actual memory storage and management

## Extensibility

This example demonstrates how to:

1. Integrate memory tools into existing systems
2. Add memory capabilities to agents
3. Handle memory tool calls and responses
4. Manage user memory storage and retrieval
5. Create custom memory tools with enhanced functionality

Future enhancements could include:

- Persistent storage (database integration)
- Memory expiration and cleanup
- Memory priority and relevance scoring
- Automatic memory summarization and compression
- Vector-based semantic memory search
- Custom memory tool implementations with specialized functionality
