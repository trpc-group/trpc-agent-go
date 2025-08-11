# Time-Aware Multi-Turn Chat Example

This example demonstrates a multi-turn chat application using the tRPC Agent Go framework with enhanced time awareness capabilities. The application showcases streaming responses and intelligent time context integration.

## Features

- **Multi-turn Conversations**: Maintains conversation context across multiple interactions
- **Streaming Responses**: Real-time streaming of AI responses for better user experience
- **Time Awareness**: Intelligent time context integration with customizable timezone and format options
- **Simple Interface**: Clean, straightforward chat interface

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

Once the chat is running, you can use this command:

- `exit` - End the conversation

## Time Awareness Features

The application automatically includes current time information in system prompts, allowing the AI assistant to:

- Provide accurate time-based responses
- Understand temporal context in conversations
- Reference current date and time when relevant
- Adapt responses based on timezone settings

Example time-aware interactions:
```
User: What time is it?
Assistant: Based on the current time information available to me, it's currently [current time] in [timezone].

User: What day of the week is it?
Assistant: Today is [current day] according to the current date information.
```

## Architecture

### Components

1. **LLM Agent**: OpenAI-compatible model integration with time awareness
2. **Runner**: Orchestrates the conversation flow
3. **Event Handling**: Streaming response processing

### Key Structs

- `multiTurnChat`: Main application controller
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
🚀 Multi-turn Chat with Time Aware
Model: deepseek-chat
Streaming: true
Add Current Time: true
Timezone: UTC
Time Format: 2006-01-02 15:04:05 UTC
Type 'exit' to end the conversation
==================================================
✅ Chat ready!

💡 Type 'exit' to end the conversation

👤 You: What time is it?
🤖 Assistant: Based on the current time information available to me, it's currently 2025-01-15 14:30:25 UTC.

👤 You: What day of the week is it?
🤖 Assistant: Today is Wednesday, January 15th, 2025.
```

## Development

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
