# Multi-turn Chat with Runner + Tools

This example demonstrates a clean multi-turn chat interface using the `Runner` orchestration component with streaming output, session management, and actual tool calling functionality.

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

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument      | Description                                     | Default Value    |
| ------------- | ----------------------------------------------- | ---------------- |
| `-model`      | Name of the model to use                        | `deepseek-chat`  |
| `-session`    | Session service: `inmemory` or `redis`          | `inmemory`       |
| `-redis-addr` | Redis server address (when using redis session) | `localhost:6379` |
| `-streaming`  | Enable streaming mode for responses             | `true`           |
| `-enable-parallel` | Enable parallel tool execution (faster performance) | `false` |

## Usage

### Basic Chat

```bash
cd examples/runner
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run main.go -model gpt-4o
```

### With Redis Session

```bash
export OPENAI_API_KEY="your-api-key"
go run main.go -session redis -redis-addr localhost:6379
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
go run main.go -model gpt-4o -streaming=false -session redis
```

**When to use each mode:**

- **Streaming mode** (`-streaming=true`, default): Best for interactive chat where you want to see responses appear in real-time, providing immediate feedback and better user experience.
- **Non-streaming mode** (`-streaming=false`): Better for automated scripts, batch processing, or when you need the complete response before processing it further.

### Tool Execution Modes

Control how multiple tools are executed when the AI makes multiple tool calls:

```bash
# Default serial tool execution (safe and compatible)
go run main.go

# Parallel tool execution (faster performance)
go run main.go -enable-parallel=true
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
go run main.go --help
```

Output:

```
Usage of ./runner:
  -enable-parallel
        Enable parallel tool execution (faster performance) (default false)
  -model string
        Name of the model to use (default "deepseek-chat")
  -redis-addr string
        Redis address (default "localhost:6379")
  -session string
        Name of the session service to use, inmemory / redis (default "inmemory")
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
🚀 Multi-turn Chat with Runner + Tools
Model: gpt-4o-mini
Streaming: true
Type 'exit' to end the conversation
Available tools: calculator, current_time
==================================================
✅ Chat ready! Session: chat-session-1703123456

👤 You: Hello! How are you today?
🤖 Assistant: Hello! I'm doing well, thank you for asking. I'm here and ready to help you with whatever you need. How are you doing today?

👤 You: I'm good! Can you remember what I just asked you?
🤖 Assistant: Yes, I can! You just asked me how I was doing today, and I responded that I'm doing well. This shows that I'm maintaining context from our conversation. Is there anything specific you'd like to chat about or any way I can help you?

👤 You: /exit
👋 Goodbye!
```

### Session Commands

- `/history` - Ask the agent to show conversation history
- `/new` - Start a new session (resets conversation context)
- `/exit` - End the conversation
