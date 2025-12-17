# Filter-Key Summarization Example

This example demonstrates session summarization with custom `filterKey` support,
showing how to categorize events and retrieve filtered summaries.

Key concepts:

- **AppendEventHook**: Demonstrates how to set `event.FilterKey` before persistence
- **FilterKey-based Summarization**: Generate summaries for specific event categories
- **Session Summary Retrieval**: Fetch summaries by filter using `WithSummaryFilterKey`

## What the demo does

1. **Event Categorization**: Shows how events are automatically categorized by
   author via AppendEventHook (with `app` prefix to match runner filtering):

   - `"user"` messages → `filterKey: "<app>/user-messages"`
   - `"tool"` calls → `filterKey: "<app>/tool-calls"`
   - Other events → `filterKey: "<app>/misc"`

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

Expected output (sample):

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
   - User messages → filterKey: '<app>/user-messages'
   - Tool calls → filterKey: '<app>/tool-calls'
   - Assistant/other → filterKey: '<app>/misc'

🤖 Assistant: ✅ Callable tool response (ID: ...): {"operation":"multiply","a":25,"b":4,"result":100}
25 multiplied by 4 equals 100.

👤 You: /show <app>/user-messages
📝 Summary[<app>/user-messages]:
The user requested a multiplication calculation (25 * 4). The assistant provided the correct result (100) in both a structured format and a plain text response.

👤 You: What time is it in EST?
🤖 Assistant: ✅ Callable tool response (ID: ...): {"timezone":"EST","time":"13:05:32","date":"2025-12-09","weekday":"Tuesday"}
The current time in EST is 1:05 PM on Tuesday, December 9, 2025.

👤 You: /summary <app>/tool-calls
📝 Summary[<app>/tool-calls] (forced):
- User requested and received a correct multiplication calculation (25 * 4 = 100).
- User asked for the current time in EST and received the time, date, and weekday in both structured and plain text formats.

👤 You: /list
📝 Summaries (filterKey → summary):
- <app>/misc
  - Assistant can perform basic arithmetic (e.g., 25 * 4 = 100).
  - Provides current time/date in EST (e.g., 1:05 PM, Tuesday, December 9, 2025).
- <app>/user-messages
  The user requested a multiplication calculation (25 * 4). The assistant provided the correct result (100) in both a structured format and a plain text response.
- <app>/tool-calls
  - User requested and received a correct multiplication calculation (25 * 4 = 100).
  - User asked for the current time in EST and received the time, date, and weekday in both structured and plain text formats.

👤 You: /exit
👋 Bye.
```

## Implementation Notes

- **AppendEventHook**: Uses `inmemory.WithAppendEventHook()` to automatically set `event.FilterKey` based on `event.Author`.
- **FilterKey Assignment**: Events are categorized automatically with an `app` prefix (e.g., `filterkey-demo-app/user-messages`). Runner injects the same prefix into invocation filter keys; without the prefix, history will be filtered out and the model may repeatedly trigger tools.
- **Commands**: `/summary [filterKey]`, `/show [filterKey]`, `/list` (list all filterKeys and summaries), `/exit`.
- **LLM vs Local**: With API key, summaries use LLM; without it, local aggregation provides basic summaries
- **Filter Options**: Common filters include `"user-messages"`, `"tool-calls"`, `"misc"`, or `""` (all events)
- **Code Structure**: Refactored to reduce cyclomatic complexity with separate command handlers
