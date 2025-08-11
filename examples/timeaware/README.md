# Time-Aware Multi-Turn Chat Example

This example demonstrates a multi-turn chat application using the tRPC Agent Go framework with enhanced time awareness capabilities. The application showcases streaming responses, session management, tool calling, and intelligent time context integration.

## Features

- **Multi-turn Conversations**: Maintains conversation context across multiple interactions
- **Streaming Responses**: Real-time streaming of AI responses for better user experience
- **Session Management**: In-memory session storage for conversation persistence
- **Tool Integration**: Built-in calculator tool for mathematical operations
- **Time Awareness**: Intelligent time context integration with customizable timezone and format options
- **Interactive Commands**: Special commands for session management and history viewing

## Prerequisites

- Go 1.19 or later
- tRPC Agent Go framework dependencies
- OpenAI-compatible model access (or other supported models)

## Installation

```bash
# Navigate to the example directory
cd examples/timeaware

# Build the application
go build -o timeaware-chat main.go
```

## Usage

### Basic Usage

```bash
# Run with default settings
./timeaware-chat

# Run with custom model
./timeaware-chat --model "gpt-4"

# Disable streaming
./timeaware-chat --streaming=false
```

### Time-Related Options

The application provides several time-aware configuration options:

```bash
# Enable current time in system prompts (default: true)
./timeaware-chat --add-time

# Specify custom timezone
./timeaware-chat --timezone "EST"

# Custom time format
./timeaware-chat --time-format "Jan 2, 2006 at 3:04 PM"

# Combine multiple options
./timeaware-chat --add-time --timezone "PST" --time-format "15:04:05"
```

### Available Timezones

- `UTC` - Coordinated Universal Time
- `EST` - Eastern Standard Time
- `PST` - Pacific Standard Time
- `CST` - Central Standard Time
- Custom timezone strings

### Time Format Examples

The time format follows Go's time formatting conventions:

- `"2006-01-02 15:04:05 UTC"` - ISO-like format with timezone
- `"Jan 2, 2006 at 3:04 PM"` - Human-readable format
- `"15:04:05"` - Time only
- `"2006-01-02"` - Date only

## Interactive Commands

Once the chat is running, you can use these special commands:

- `/history` - Display conversation history
- `/new` - Start a new session (resets conversation context)
- `/exit` - End the conversation

## Tool Capabilities

### Calculator Tool

The built-in calculator supports basic mathematical operations:

- **Addition**: `add`, `+`
- **Subtraction**: `subtract`, `-`
- **Multiplication**: `multiply`, `*`
- **Division**: `divide`, `/`

Example usage in chat:
```
User: Calculate 15 * 7
Assistant: I'll calculate that for you using the calculator tool.
🔧 Tool calls initiated:
   • calculator (ID: calc_123)
     Args: {"operation": "multiply", "a": 15, "b": 7}
🔄 Executing tools...
✅ Tool response (ID: calc_123): The result of 15 * 7 is 105
```

## Architecture

### Components

1. **LLM Agent**: OpenAI-compatible model integration with tool support
2. **Runner**: Orchestrates the conversation flow and tool execution
3. **Session Service**: In-memory session management for conversation persistence
4. **Tool System**: Extensible tool calling framework
5. **Event Handling**: Streaming response processing and tool call visualization

### Key Structs

- `multiTurnChat`: Main application controller
- `calculatorArgs/calculatorResult`: Calculator tool data structures
- Event-driven architecture for streaming responses

## Configuration

### Environment Variables

The application uses command-line flags for configuration. All settings can be customized at runtime.

### Default Settings

- **Model**: `deepseek-chat`
- **Streaming**: `true`
- **Add Current Time**: `true`
- **Timezone**: `UTC`
- **Time Format**: `"2006-01-02 15:04:05 UTC"`

## Example Session

```
🚀 Multi-turn Chat with Runner + Tools
Model: deepseek-chat
Streaming: true
Add Current Time: true
Timezone: UTC
Time Format: 2006-01-02 15:04:05 UTC
Type 'exit' to end the conversation
Available tools: calculator
==================================================
✅ Chat ready! Session: chat-session-1703123456

💡 Special commands:
   /history  - Show conversation history
   /new      - Start a new session
   /exit     - End the conversation

👤 You: What's 25 + 17?
🤖 Assistant: I'll calculate that for you using the calculator tool.
🔧 Tool calls initiated:
   • calculator (ID: calc_abc123)
     Args: {"operation": "add", "a": 25, "b": 17}
🔄 Executing tools...
✅ Tool response (ID: calc_abc123): The result of 25 + 17 is 42

The answer is 42!
```

## Development

### Adding New Tools

To add new tools, implement the tool interface and register them in the `setup` function:

```go
newTool := function.NewFunctionTool(
    yourFunction,
    function.WithName("tool_name"),
    function.WithDescription("Tool description"),
)

// Add to tools array
llmagent.WithTools([]tool.Tool{calculatorTool, newTool}),
```

### Customizing Time Behavior

Modify the time-related options in the LLM agent configuration:

```go
llmAgent := llmagent.New(
    agentName,
    // ... other options ...
    llmagent.WithAddCurrentTime(true),
    llmagent.WithTimezone("EST"),
    llmagent.WithTimeFormat("Jan 2, 2006 at 3:04 PM"),
)
```
