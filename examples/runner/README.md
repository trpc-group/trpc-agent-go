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
| `-provider`        | Provider of the model to use                        | `openai`         |
| `-model`           | Name of the model to use                            | `deepseek-chat`  |
| `-session`         | Session service: `inmemory` or `redis`              | `inmemory`       |
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

### Custom Model Provider

```bash
export OPENAI_API_KEY="your-api-key"
go run . -provider anthropic
```

### With Redis Session

```bash
export OPENAI_API_KEY="your-api-key"
go run . -session redis -redis-addr localhost:6379
```

### With PostgreSQL Session

**Minimal setup** (using defaults):

```bash
export OPENAI_API_KEY="your-api-key"
export PG_PASSWORD="your-password"
go run . -session pgsql
```

**Custom configuration**:

```bash
export OPENAI_API_KEY="your-api-key"
go run . -session pgsql -pg-host localhost -pg-user postgres -pg-password your-password -pg-database trpc_sessions
```

**Using environment variables**:

```bash
export OPENAI_API_KEY="your-api-key"
export PG_HOST="localhost"
export PG_USER="postgres"
export PG_PASSWORD="your-password"
export PG_DATABASE="trpc_sessions"
go run . -session pgsql
```

### Using Environment Variable

If you have `MODEL_NAME` set in your environment:

```bash
source ~/.bashrc && go run . -model "$MODEL_NAME"
```

### Response Modes

Choose between streaming and non-streaming responses:

```bash
# Default streaming mode (real-time character output)
go run .

# Non-streaming mode (complete response at once)
go run . -streaming=false

# Combined with other options
go run . -model gpt-4o -streaming=false -session redis
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
  -provider string
        Name of the provider to use, openai or anthropic (default "openai")
  -session string
        Name of the session service to use: inmemory / redis / pgsql (default "inmemory")
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
Parallel Tools: disabled (serial execution)
Session: inmemory
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

### Session Storage Options

The runner supports three session storage backends:

#### In-Memory Session (Default)
- **Usage**: `-session inmemory` (or omit the flag)
- **Best for**: Development, testing, single-instance applications
- **Pros**: Fast, no external dependencies
- **Cons**: Data lost on restart, not suitable for distributed systems

#### Redis Session
- **Usage**: `-session redis`
- **Best for**: Production, distributed applications
- **Pros**: Persistent, supports multiple instances, automatic TTL
- **Cons**: Requires Redis server

#### PostgreSQL Session
- **Usage**: `-session pgsql` (minimal) or with custom options
- **Best for**: Production, complex queries, relational data needs
- **Pros**:
  - Relational database with ACID guarantees
  - JSONB storage for efficient JSON operations
  - Soft delete & TTL cleanup (automatic data management)
  - Built-in indexing and query optimization
- **Cons**: Requires PostgreSQL server

**Key Features:**

1. **JSONB Storage**: All session data stored as JSONB for efficient querying
2. **Soft Delete**: Deleted data marked (not removed), can be recovered
3. **TTL Cleanup**: Automatic cleanup of expired data (configurable interval)
4. **Partial Unique Index**: Allows recreating sessions after soft delete

**Default Configuration:**
- Host: `localhost:5432`
- Database: `test`
- User: `root`
- Soft delete: enabled
- TTL cleanup: 5 minutes (when TTL configured)

3. **Automatic Schema Management**: Tables and indexes created automatically on first run

4. **TTL Support**: Automatic expiration of old sessions via `expires_at` column

### Session Commands

- `/history` - Ask the agent to show conversation history
- `/new` - Start a new session (resets conversation context)
- `/exit` - End the conversation
