# Filter-Key Summarization Example

This example demonstrates session summarization with custom `filterKey` support,
showing how to categorize events and retrieve filtered summaries.

Key concepts:

- **AppendEventHook**: Demonstrates how to set `event.FilterKey` before persistence
- **FilterKey-based Summarization**: Generate summaries for specific event categories
- **Session Summary Retrieval**: Fetch summaries by filter using `WithSummaryFilterKey`

## What the demo does

1. **Event Categorization**: Shows how events are automatically categorized by author via AppendEventHook:

   - `"user"` messages → `filterKey: "user-messages"`
   - `"tool"` calls → `filterKey: "tool-calls"`
   - Other events → `filterKey: "misc"`

2. **Tool Integration**: Includes calculator and time tools for the agent to use

3. **Interactive Demo**: Provides commands to add events and view filtered summaries

4. **LLM Integration**: Uses real LLM summarization (requires `OPENAI_API_KEY`)

5. **Fallback Support**: Local aggregation when LLM is unavailable

## Running the example

From the repo root:

```bash
# With real LLM (requires OPENAI_API_KEY for actual summarization)
OPENAI_API_KEY=sk-xxx go run ./examples/summary/filterkey -model gpt-4o-mini -max-words 120 -streaming=true

# Without API key (demonstrates local aggregation fallback)
go run ./examples/summary/filterkey -model deepseek-chat -max-words 120 -streaming=false
```

Expected output:

```
📝 Filter-Key Summarization Chat
Model: gpt-4o-mini
Service: inmemory
MaxWords: 120
==================================================
✅ Filter-key chat ready! Session: filterkey-session-1234567890

💡 Special commands:
   /summary [filterKey] - Force summarize by filter (default: full)
   /show [filterKey]    - Show summary by filter (default: full)
   /exit                - End the conversation

👤 You: Calculate 25 * 4
💡 FilterKey Demo: Events are automatically categorized by author via AppendEvent hooks:
   - User messages → filterKey: 'user-messages'
   - Tool calls → filterKey: 'tool-calls'
   - Assistant/other → filterKey: 'misc'

🤖 Assistant: The result of 25 * 4 is 100.

👤 You: /show user-messages
📝 Summary[user-messages]:
[user-messages] 1 event(s): Calculate 25 * 4

👤 You: What time is it in EST?
🤖 Assistant: The current time in EST is 14:30:00 on 2025-01-01.

👤 You: /summary tool-calls
📝 Summary[tool-calls] (forced):
[tool-calls] 2 event(s): calculate, get_current_time

👤 You: /exit
👋 Bye.
```

## Implementation Notes

- **AppendEventHook**: Uses `inmemory.WithAppendEventHook()` to automatically set `event.FilterKey` based on `event.Author`
- **FilterKey Assignment**: Events are categorized automatically: user→"user-messages", tool→"tool-calls", other→"misc"
- **LLM vs Local**: With API key, summaries use LLM; without it, local aggregation provides basic summaries
- **Filter Options**: Common filters include `"user-messages"`, `"tool-calls"`, `"misc"`, or `""` (all events)
- **Code Structure**: Refactored to reduce cyclomatic complexity with separate command handlers
