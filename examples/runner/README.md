# Runner Quickstart: Multi-turn Chat with Tools

This example demonstrates a minimal multi-turn chat interface using the `Runner` orchestration component. It focuses on core functionality with an in-memory session backend, making it easy to understand and run.

## What is Multi-turn Chat?

This implementation showcases the essential features for building conversational AI applications:

- **🔄 Multi-turn Conversations**: Maintains context across multiple exchanges
- **🌊 Flexible Output**: Support for both streaming (real-time) and non-streaming (batch) response modes
- **💾 Session Management**: Conversation state preservation and continuity
- **🔧 Tool Integration**: Working calculator and time tools with proper execution
- **🚀 Simple Interface**: Clean, focused chat experience

### Key Features

- **Context Preservation**: The assistant remembers previous conversation turns
- **Flexible Response Modes**: Choose between streaming (real-time) or non-streaming (batch) output
- **Session Continuity**: Consistent conversation state across the chat session
- **Tool Call Execution**: Proper execution and display of tool calling procedures
- **Tool Visualization**: Clear indication of tool calls, arguments, and responses
- **Error Handling**: Graceful error recovery and reporting

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable                | Description                                    | Default Value               |
| ----------------------- | ---------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`        | API key for the openai model                   | ``                          |
| `OPENAI_BASE_URL`       | Base URL for the openai model API endpoint     | `https://api.openai.com/v1` |
| `ANTHROPIC_AUTH_TOKEN`  | API key for the anthropic model                | ``                          |
| `ANTHROPIC_BASE_URL`    | Base URL for the anthropic model API endpoint  | `https://api.anthropic.com` |

## Command Line Arguments

| Argument           | Description                                         | Default Value    |
| ------------------ | --------------------------------------------------- | ---------------- |
| `-model`           | Name of the model to use                            | `deepseek-chat`  |
| `-variant`         | Variant to use when calling the OpenAI provider     | `openai`         |
| `-streaming`       | Enable streaming mode for responses                 | `true`           |
| `-enable-parallel` | Enable parallel tool execution (faster performance) | `false`          |

## Usage

### Basic Chat

```bash
cd examples/runner
export OPENAI_API_KEY="your-api-key-here"
go run .
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run . -model gpt-4o
```

### Custom Variant

```bash
export OPENAI_API_KEY="your-api-key"
go run . -variant deepseek
```

### Response Modes

Choose between streaming and non-streaming responses:

```bash
# Default streaming mode (real-time character output)
go run .

# Non-streaming mode (complete response at once)
go run . -streaming=false
```

**When to use each mode:**

- **Streaming mode** (`-streaming=true`, default): Best for interactive chat where you want to see responses appear in real-time, providing immediate feedback and better user experience.
- **Non-streaming mode** (`-streaming=false`): Better for automated scripts, batch processing, or when you need the complete response before processing it further.

### Tool Execution Modes

Control how multiple tools are executed when the AI makes multiple tool calls:

```bash
# Default serial tool execution (safe and compatible)
go run .

# Parallel tool execution (faster performance)
go run . -enable-parallel=true
```

**When to use each mode:**

- **Serial execution** (default, no flag needed):
  - 🔄 Tools execute one by one in sequence
  - 🛡️ **Safe and compatible** default behavior
  - 🐛 Better for debugging tool execution issues
- **Parallel execution** (`-enable-parallel=true`):
  - ⚡ **faster performance** when multiple tools are called
  - ✅ Best for independent tools (calculator + time, weather + population)
  - ✅ Tools execute simultaneously using goroutines

### Help and Available Options

To see all available command line options:

```bash
go run . --help
```

Output:

```
Usage of ./runner:
  -enable-parallel
        Enable parallel tool execution (default: false, serial execution)
  -model string
        Name of the model to use (default "deepseek-chat")
  -variant string
        Name of the variant to use when calling the OpenAI provider (default "openai")
  -streaming
        Enable streaming mode for responses (default true)
```

## Implemented Tools

The example includes two working tools:

### 🧮 Calculator Tool

- **Function**: `calculator`
- **Operations**: add, subtract, multiply, divide
- **Usage**: "Calculate 15 \* 25" or "What's 100 divided by 7?"
- **Arguments**: operation (string), a (number), b (number)

### 🕐 Time Tool

- **Function**: `current_time`
- **Timezones**: UTC, EST, PST, CST, or local time
- **Usage**: "What time is it in EST?" or "Current time please"
- **Arguments**: timezone (optional string)

## Tool Calling Process

When you ask for calculations or time information, you'll see:

```
🔧 Tool calls initiated:
   • calculator (ID: call_abc123)
     Args: {"operation":"multiply","a":25,"b":4}

🔄 Executing tools...
✅ Tool response (ID: call_abc123): {"operation":"multiply","a":25,"b":4,"result":100}

🤖 Assistant: I calculated 25 × 4 = 100 for you.
```

## Chat Interface

The interface is simple and intuitive:

```
🚀 Runner quickstart: multi-turn chat with tools
Model: deepseek-chat
Streaming: true
Parallel tools: false
Session backend: in-memory (simple demo)
Type '/exit' to end the conversation
Available tools: calculator, current_time
==================================================
✅ Chat ready! Session: demo-session-1703123456

👤 You: Hello! How are you today?
🤖 Assistant: Hello! I'm doing well, thank you for asking. I'm here and ready to help you with whatever you need. How are you doing today?

👤 You: I'm good! Can you remember what I just asked you?
🤖 Assistant: Yes, I can! You just asked me how I was doing today, and I responded that I'm doing well. This shows that I'm maintaining context from our conversation. Is there anything specific you'd like to chat about or any way I can help you?

👤 You: /exit
👋 Goodbye!
```

## Session Storage

This example uses **in-memory session storage** for simplicity. This means:
- ✅ Fast and no external dependencies
- ✅ Perfect for development and testing
- ⚠️ Session data is lost when the program exits

**For production use with persistent session storage** (Redis, PostgreSQL, MySQL), see the `examples/session/` directory which demonstrates advanced session management features including:
- Multiple session backends (Redis, PostgreSQL, MySQL)
- Session switching with `/use <id>` command
- Session listing with `/sessions` command
- Creating new sessions with `/new` command
