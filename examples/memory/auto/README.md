# 🧠 Auto Memory Chat

This example demonstrates automatic memory extraction using the `Runner` orchestration component. Unlike the manual memory tools approach, auto memory extracts user information from conversations automatically in the background without explicit tool calls.

## What is Auto Memory?

Auto memory mode uses an LLM-based extractor to analyze conversations and automatically create/update memories. The system learns about users passively from natural conversation flow.

### Key Differences from Manual Memory

| Aspect | Manual Memory (Agentic) | Auto Memory |
|--------|------------------------|-------------|
| **Memory Creation** | Agent explicitly calls `memory_add` | System extracts automatically |
| **User Experience** | Visible tool calls in conversation | Transparent, no tool call interruptions |
| **Available Tools** | All 6 tools (add/update/delete/clear/search/load) | Only `memory_search` |
| **Processing** | Synchronous during response | Asynchronous after response |
| **Control** | Agent decides what to remember | Extractor analyzes and decides |

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
// When extractor is set, only search tool is exposed.
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    // Optional: configure async worker settings.
    memoryinmemory.WithAsyncMemoryNum(3),
    memoryinmemory.WithMemoryQueueSize(100),
    memoryinmemory.WithMemoryJobTimeout(30*time.Second),
)

// Create LLM agent with memory tools.
// Only search tool is available since extractor is set.
llmAgent := llmagent.New(
    "auto-memory-assistant",
    llmagent.WithModel(chatModel),
    llmagent.WithTools(memoryService.Tools()), // Only memory_search.
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

| Option | Description | Default |
|--------|-------------|---------|
| `WithExtractor(extractor)` | Enable auto mode with LLM extractor | nil (disabled) |
| `WithAsyncMemoryNum(n)` | Number of background worker goroutines | 3 |
| `WithMemoryQueueSize(n)` | Size of memory job queue | 100 |
| `WithMemoryJobTimeout(d)` | Timeout for each extraction job | 30s |

### Tool Availability

| Tool | Manual Mode | Auto Mode |
|------|-------------|-----------|
| `memory_add` | ✅ Available | ❌ Hidden |
| `memory_update` | ✅ Available | ❌ Hidden |
| `memory_delete` | ⚙️ Configurable | ❌ Hidden |
| `memory_clear` | ⚙️ Configurable | ❌ Hidden |
| `memory_search` | ✅ Available | ✅ Available |
| `memory_load` | ✅ Available | ❌ Hidden |

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model for chat responses | `deepseek-chat` |
| `-ext-model` | Name of the model for memory extraction | Same as `-model` |
| `-streaming` | Enable streaming mode for responses | `true` |
| `-debug` | Enable debug mode to print messages sent to model | `false` |

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

By default, memories are preloaded into the system prompt. This means the agent has context about the user without needing to call `memory_search`:

```go
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),
    // Preload options:
    // llmagent.WithPreloadMemory(-1),  // Load all (default).
    // llmagent.WithPreloadMemory(0),   // Disable preloading.
    // llmagent.WithPreloadMemory(10),  // Load 10 most recent.
)
```

Use `-debug` flag to see preloaded memories in the system prompt.

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
