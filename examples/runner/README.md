# Multi-turn Chat with Runner + Tools

This example demonstrates a clean multi-turn chat interface using the `Runner` orchestration component with streaming output, session management, and actual tool calling functionality.

## What is Multi-turn Chat?

This implementation showcases the essential features for building conversational AI applications:

- **🔄 Multi-turn Conversations**: Maintains context across multiple exchanges
- **🌊 Streaming Output**: Real-time character-by-character response generation
- **💾 Session Management**: Conversation state preservation and continuity
- **🔧 Tool Integration**: Working calculator and time tools with proper execution
- **🚀 Simple Interface**: Clean, focused chat experience

### Key Features

- **Context Preservation**: The assistant remembers previous conversation turns
- **Streaming Responses**: Live, responsive output for better user experience
- **Session Continuity**: Consistent conversation state across the chat session
- **Tool Call Execution**: Proper execution and display of tool calling procedures
- **Tool Visualization**: Clear indication of tool calls, arguments, and responses
- **Error Handling**: Graceful error recovery and reporting

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `gpt-4o-mini` |

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

### Using Environment Variable

If you have `MODEL_NAME` set in your environment:

```bash
source ~/.bashrc && go run main.go -model "$MODEL_NAME"
```

## Implemented Tools

The example includes two working tools:

### 🧮 Calculator Tool
- **Function**: `calculator`
- **Operations**: add, subtract, multiply, divide
- **Usage**: "Calculate 15 * 25" or "What's 100 divided by 7?"
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
🚀 Multi-turn Chat with Runner
Model: gpt-4o-mini
Type 'exit' to end the conversation
==================================================
✅ Chat ready! Session: chat-session-1703123456

👤 You: Hello! How are you today?
🤖 Assistant: Hello! I'm doing well, thank you for asking. I'm here and ready to help you with whatever you need. How are you doing today?

👤 You: I'm good! Can you remember what I just asked you?
🤖 Assistant: Yes, I can! You just asked me how I was doing today, and I responded that I'm doing well. This shows that I'm maintaining context from our conversation. Is there anything specific you'd like to chat about or any way I can help you?

👤 You: exit
👋 Goodbye!
```
