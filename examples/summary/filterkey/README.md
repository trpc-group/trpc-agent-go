# Filter-Key Summarization Example

This example demonstrates session summarization with custom `filterKey` support,
showing how to categorize conversations and generate separate summaries per category.

## Key Concepts

- **AppendEventHook**: Set `event.FilterKey` before persistence to categorize events.
- **FilterKey-based Summarization**: Each filterKey gets its own independent summary.
- **User-defined Categories**: Users can switch filterKey at runtime via `/key` command.

## How It Works

1. **Event Categorization**: All events (user messages, tool calls, assistant responses)
   within a filterKey are grouped together for summarization.

2. **Separate Summaries**: When you switch to a different filterKey, conversations
   under that key generate their own summary, independent of other keys.

3. **Tool Integration**: Includes calculator and time tools for demonstration.

## Running the Example

```bash
# Basic usage.
go run ./examples/summary/filterkey

# With custom model.
go run ./examples/summary/filterkey -model gpt-4o-mini

# With debug mode to see request messages.
go run ./examples/summary/filterkey -debug

# With all options.
go run ./examples/summary/filterkey -model deepseek-chat -max-words 100 -streaming=true -debug
```

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-model` | `deepseek-chat` | Model name for LLM |
| `-streaming` | `true` | Enable streaming mode |
| `-max-words` | `0` | Max summary words (0=unlimited) |
| `-debug` | `false` | Print request messages for debugging |

## Interactive Commands

| Command | Description |
|---------|-------------|
| `/key <name>` | Switch to a filterKey (any name you want) |
| `/key` | Show current filterKey |
| `/show [key]` | Show summary for a filterKey (default: current) |
| `/list` | List all summaries |
| `/help` | Show help |
| `/exit` | End the conversation |

## Example Session

```
📝 Filter-Key Summarization Demo
Model: deepseek-chat | Streaming: true | MaxWords: 0 | Debug: false
============================================================
Session: filterkey-session-1735638000

💡 This demo shows how to use filterKey to categorize conversations.
   Each filterKey gets its own separate summary.

📌 Commands:
   /key <name>    - Switch to a filterKey (any name you want)
   /show [key]    - Show summary for a filterKey (default: current)
   /list          - List all summaries
   /help          - Show this help
   /exit          - End the conversation

👤 [default] You: Hello
🤖 Assistant: Hello! How can I help you today!

👤 [default] You: /key calc
📌 Switched to filterKey: calc
   All messages will now be categorized under this key.

👤 [calc] You: Calculate 123 * 321
🤖 Assistant:
🔧 Tool: calculate({"operation":"multiply","a":123,"b":321})
   → {"operation":"multiply","a":123,"b":321,"result":39483}
123 * 321 is 39483.

👤 [calc] You: /key time
📌 Switched to filterKey: time

👤 [time] You: What time is it
🤖 Assistant:
🔧 Tool: get_current_time({"timezone":"Asia/Shanghai"})
   → {"timezone":"Asia/Shanghai","time":"17:30:00",...}
It is 5:30PM.

👤 [time] You: /list
📝 All Summaries:
--------------------------------------------------
[default]
User greeted the assistant.

[calc]
User asked to calculate 123 * 321, result is 39483.

[time]
User asked for the current time.

[(full session)]
The user greeted the assistant, performed a multiplication calculation,
and asked for the current time.

👤 [time] You: /show calc
📝 Summary[calc]:
User asked to calculate 123 * 321, result is 39483.

👤 [time] You: /exit
👋 Bye.
```

## Implementation Details

### AppendEventHook

The hook sets `event.FilterKey` based on the current user-selected key:

```go
sessService := inmemory.NewSessionService(
    inmemory.WithSummarizer(sum),
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        // Set filterKey with app prefix.
        ctx.Event.FilterKey = appName + "/" + currentFilterKey
        return next()
    }),
)
```

### FilterKey Format

FilterKeys are prefixed with the app name to match the runner's invocation filter:
- User input: `calc`
- Stored filterKey: `filterkey-demo-app/calc`

### Debug Mode

Enable `-debug` flag to see request messages sent to the model:

```
🐛 [DEBUG] Request messages:
   [0] system: A helpful AI assistant with calculator and time tools.
   [1] user: Here is a brief summary of your previous interactions...
   [2] user: Calculate 123 * 321
```
