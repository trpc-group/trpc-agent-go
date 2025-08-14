# Token Usage Tracker Demo

This example demonstrates how to track token usage for each conversation turn in `trpc-agent-go`.

## Features

- 🔍 **Real-time token tracking**: Shows detailed token usage for each conversation turn
- 📊 **Session statistics**: Displays cumulative token usage statistics for the entire session
- 🔄 **Session management**: Supports creating new sessions and resetting token tracking

## Running the Example

### Basic Usage

```bash
cd examples/token_tracker
go run main.go
```

### Command Line Arguments

- `-model`: Specify the model name to use (default: deepseek-chat)
- `-streaming`: Enable streaming output (default: true)

```bash
# Use a specific model
go run main.go -model gpt-3.5-turbo

# Disable streaming output
go run main.go -streaming=false
```

## Special Commands

During the conversation, you can use the following special commands:

- `/stats` - Show current session token usage statistics
- `/new` - Create a new session (reset token tracking)
- `/exit` - Exit the program

## Example Output

```
🚀 Token Usage Tracker Demo
Model: deepseek-chat
Streaming: true
Type 'exit' to end the conversation
Special commands: /stats, /new, /exit
==================================================
✅ Token tracker ready! Session: token-tracker-session-1703123456

💡 Special commands:
   /stats    - Show current session token usage statistics
   /new      - Start a new session (reset token tracking)
   /exit     - End the conversation

👤 You: Hello, how are you?
🤖 Assistant: Hello! I'm doing well, thank you for asking. I'm here to help you with any questions or tasks you might have. How can I assist you today?

📊 Turn 1 Token Usage:
   Prompt: 15, Completion: 25, Total: 40

👤 You: /stats
📊 Session Token Usage Statistics:
   Total Turns: 1
   Total Prompt Tokens: 15
   Total Completion Tokens: 25
   Total Tokens: 40
   Average Prompt Tokens per Turn: 15.0
   Average Completion Tokens per Turn: 25.0
   Average Total Tokens per Turn: 40.0
```

## Implementation Details

This example implements token tracking through the following methods:

1. **Event monitoring**: Listens to the `Response.Usage` field in `event.Event`
2. **Real-time statistics**: Updates token usage statistics at the end of each conversation turn
3. **Session management**: Supports session-level token statistics management

## Core Data Structures

### TurnUsage
```go
type TurnUsage struct {
    TurnNumber        int
    PromptTokens      int
    CompletionTokens  int
    TotalTokens       int
    Model             string
    InvocationID      string
    Timestamp         time.Time
    UserMessage       string
    AssistantResponse string
}
```

### SessionTokenUsage
```go
type SessionTokenUsage struct {
    TotalPromptTokens     int
    TotalCompletionTokens int
    TotalTokens           int
    TurnCount             int
    UsageHistory          []TurnUsage
}
```

## Extension Ideas

You can extend this example with additional features:

- **Cost calculation**: Calculate API call costs based on token count
- **Usage limits**: Set token usage limits
- **Performance analysis**: Analyze token efficiency of different models and prompts
- **Data export**: Export token usage data to CSV or JSON format
- **Visualization**: Create token usage trend charts

## Notes

1. Token counts are returned by the model API, and different models may have different calculation methods
2. In streaming mode, token statistics are provided in the final event
3. It's recommended to add error handling and logging in production environments
