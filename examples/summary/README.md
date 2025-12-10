# 📝 Session Summarization Example

This example demonstrates LLM-driven session summarization integrated with the framework's `Runner` and `session.Service`.

- Preserves original `events`.
- Stores summary separately from events (not inserted as system events).
- Feeds LLM with "latest summary + incremental events since last summary" to keep context coherent without truncation.
- Uses `SessionSummarizer` directly with session service for summarization.

## What it shows

- LLM-based summarization per session turn.
- Simple trigger configuration using event-count threshold.
- Prompt construction that injects the latest summary and recent events.
- Backend-specific persistence:
  - Summary text is stored in `sess.Summaries[filterKey]` for both backends.

## Prerequisites

- Go 1.21 or later.
- Model configuration (e.g., OpenAI-compatible) via environment variables.

Environment variables:

- `OPENAI_API_KEY`: API key for model service.
- `OPENAI_BASE_URL` (optional): Base URL for the model API endpoint.

## Run

```bash
cd examples/summary
export OPENAI_API_KEY="your-api-key"
go run main.go -model gpt-4o-mini
```

Quick start with immediate summarization:

```bash
go run main.go -events 0 -tokens 0 -time-sec 0
```

Command-line flags:

- `-model`: Model name to use for both chat and summarization. Default: `deepseek-chat`.
- `-streaming`: Enable streaming mode for responses. Default: `true`.
- `-events`: Event count threshold to trigger summarization. Default: `1`.
- `-tokens`: Token-count threshold to trigger summarization (0=disabled). Default: `0`.
- `-time-sec`: Time threshold in seconds to trigger summarization (0=disabled). Default: `0`.
- `-max-words`: Max summary words (0=unlimited). Default: `0`.
- `-skip-recent`: Number of recent events to skip during summarization (0=skip none). Default: `0`.
- `-add-summary`: Prepend latest filter summary as system message. Default: `true`.
- `-max-history`: Max history messages when add-summary=false (0=unlimited). Default: `0`.

## Interaction

- Type any message and press Enter to send.
- Type `/exit` to quit the demo.
- Type `/summary` to force-generate a session summary.
- Type `/show` to display the current session summary.
- After the conversation completes, the framework automatically triggers summarization asynchronously in the background.

Example output:

```
📝 Session Summarization Chat
Model: deepseek-chat
Service: inmemory
EventThreshold: 1
TokenThreshold: 0
TimeThreshold: 0s
MaxWords: 0
SkipRecent: 0
Streaming: true
AddSummary: true
MaxHistory: 0
==================================================
✅ Summary chat ready! Session: summary-session-1757649727

💡 Special commands:
   /summary  - Force-generate session summary
   /show     - Show current session summary
   /exit     - End the conversation

👤 You: Write an article about LLMs
🤖 Assistant: Here's a comprehensive article about Large Language Models (LLMs):

---

### **Understanding Large Language Models: The AI "Brain" and "Language Master"**

In today's AI revolution, tools like ChatGPT, Claude, and Copilot are profoundly changing how we work, learn, and create. Behind all of this lies a core technology driving these innovations—**Large Language Models (LLMs)**. They're not just a hot topic in tech, but a crucial milestone on the path to more general artificial intelligence.

#### **What are Large Language Models?**

Large Language Models are AI systems trained on vast amounts of text data to understand, generate, and predict human language. Think of them as a "super brain" that has read almost all the books, articles, code, and conversations on the internet, learning grammar, syntax, factual knowledge, reasoning patterns, and even different language styles.

[... article content continues ...]

👤 You: /show
📝 Summary:
The user requested an article introducing LLMs. The assistant provided a comprehensive overview covering: the definition of LLMs (large language models based on Transformer architecture), their two-phase workflow (training and inference), core capabilities (e.g., text generation, translation, coding), applications across industries, key limitations (e.g., hallucination, bias, knowledge cutoff), and future trends (e.g., multimodality, specialization). The user did not specify any particular focus or requirements for the article.

👤 You: /exit
👋 Bye.
```

## Architecture

```
User → Runner → Agent(Model) → Session Service → SessionSummarizer
                                    ↑
                            Auto-trigger summary
                                    ↓
                            Persist summary text
```

- The `Runner` orchestrates the conversation and writes events.
- The `Runner` automatically triggers summarization asynchronously immediately after each qualifying event is appended via `EnqueueSummaryJob`.
- The `SessionSummarizer` generates summaries using the configured LLM model.
- The `session.Service` stores summary text in its backend storage (in-memory or Redis).
- Summary injection happens automatically in the `ContentRequestProcessor` for subsequent turns.
- You can control summary injection with `-add-summary`.

## Async Summary Configuration

The session service supports asynchronous summary processing by default. The configuration is set in the code with detailed comments:

```go
// In-memory session service with summarizer and async config.
// Async summary processing is enabled by default with the following configuration:
// - 2 async workers: handles concurrent summary generation without blocking
// - 100 queue size: buffers summary jobs during high traffic
// You can adjust these values based on your workload:
//   - Low traffic: 1-2 workers, 50-100 queue size
//   - Medium traffic: 2-4 workers, 100-200 queue size
//   - High traffic: 4-8 workers, 200-500 queue size
sessService := inmemory.NewSessionService(
    inmemory.WithSummarizer(sum),
    inmemory.WithAsyncSummaryNum(2),                // 2 async workers for concurrent summary generation
    inmemory.WithSummaryQueueSize(100),             // Queue size 100 to buffer summary jobs during high traffic
    inmemory.WithSummaryJobTimeout(30*time.Second), // Per-job timeout to avoid long-running LLM calls
)

// Redis service with async config
sessService := redis.NewService(
    redis.WithSummarizer(sum),
    redis.WithAsyncSummaryNum(2),                // 2 async workers for concurrent summary generation
    redis.WithSummaryQueueSize(100),             // Queue size 100 to buffer summary jobs during high traffic
    redis.WithSummaryJobTimeout(30*time.Second), // Per-job timeout to avoid long-running LLM calls
)
```

### Async Configuration Options

- **`WithAsyncSummaryNum(num int)`**: Sets the number of async summary workers. More workers can handle higher concurrency but consume more resources.
- **`WithSummaryQueueSize(size int)`**: Sets the size of the summary job queue. Larger queues can handle more burst traffic but consume more memory.
- **`WithSummaryJobTimeout(timeout time.Duration)`**: Sets a timeout for each async summary job to prevent worker starvation from long-running LLM calls.

### Performance Tuning

- **Low traffic**: Use 1-2 workers with queue size 50-100
- **Medium traffic**: Use 2-4 workers with queue size 100-200
- **High traffic**: Use 4-8 workers with queue size 200-500

## Prompt Customization

You can customize the summary prompt to control how conversations are summarized. The prompt supports placeholders that will be replaced with actual values during summary generation:

### Available Placeholders

- **`{conversation_text}`**: The conversation content to be summarized
- **`{max_summary_words}`**: The maximum word count for the summary (only included when `WithMaxSummaryWords` is set). In the default prompt, this placeholder is replaced with a natural instruction like "Please keep the summary within 100 words." In custom prompts, it's replaced with just the number, allowing you to control the wording in your preferred language.

### Example Usage

```go
// Custom prompt focusing on key decisions
summary.WithPrompt("Summarize this conversation focusing on key decisions: {conversation_text}")

// Custom prompt for technical discussions
summary.WithPrompt("Extract technical insights from this conversation: {conversation_text}")

// Custom prompt for concise summaries
summary.WithPrompt("Provide a brief summary of this conversation: {conversation_text}")

// Custom prompt with length limit (English)
summary.WithPrompt("Summarize this conversation in no more than {max_summary_words} words: {conversation_text}")

// Custom prompt with length limit (Chinese)
summary.WithPrompt("请将以下对话总结为不超过{max_summary_words}个字的摘要：{conversation_text}")

// Custom prompt with length limit (mixed language)
summary.WithPrompt("请总结以下对话，控制在{max_summary_words}字以内：{conversation_text}")
```

## Summary Options

The `SessionSummarizer` supports various configuration options to customize summarization behavior:

### Basic Options

- **`WithMaxSummaryWords(maxWords int)`**: Sets the maximum word count for generated summaries. When set to 0 (default), no word limit is applied. The word limit is included in the prompt to guide the model's generation rather than truncating the output.

- **`WithPrompt(prompt string)`**: Customizes the prompt template used for summary generation. The prompt must include the `{conversation_text}` placeholder. See the [Prompt Customization](#prompt-customization) section for details and examples.

- **`WithSkipRecent(skipFunc SkipRecentFunc)`**: Sets a custom function that returns how many recent events to skip during summarization. Return 0 to skip none. Useful for avoiding summarizing very recent/incomplete turns, or applying time/content-based skipping strategies.

### Trigger Options

- **`WithEventThreshold(eventCount int)`**: Triggers summarization when the number of events exceeds the threshold.

- **`WithTokenThreshold(tokenCount int)`**: Triggers summarization when the estimated token count exceeds the threshold (0=disabled).

- **`WithTimeThreshold(interval time.Duration)`**: Triggers summarization when the time elapsed since the last event exceeds the interval (useful for periodic summarization in long-running sessions).

### Composite Trigger Options

- **`WithChecksAny(checks ...Checker)`**: Triggers summarization when ANY of the provided checks pass (OR logic).

- **`WithChecksAll(checks ...Checker)`**: Triggers summarization when ALL of the provided checks pass (AND logic).

### Example Usage

```go
// Basic configuration
sum := summary.NewSummarizer(model,
    summary.WithMaxSummaryWords(500),
    summary.WithEventThreshold(10),
)

// Skip recent events (fixed count, similar to old API)
sum := summary.NewSummarizer(model,
    summary.WithSkipRecent(func(_ []event.Event) int { return 2 }), // skip last 2 events
    summary.WithEventThreshold(10),
)

// Complex trigger configuration
sum := summary.NewSummarizer(model,
    summary.WithMaxSummaryWords(1000),
    summary.WithChecksAny(
        summary.CheckEventThreshold(5),
        summary.CheckTokenThreshold(1000),
        summary.CheckTimeThreshold(5*time.Minute),
    ),
)

// Custom prompt
sum := summary.NewSummarizer(model,
    summary.WithPrompt("Summarize this conversation focusing on key decisions: {conversation_text}"),
    summary.WithSkipRecent(func(_ []event.Event) int { return 1 }),
    summary.WithEventThreshold(3),
)

// Skip events from the last 5 minutes (time window)
sum := summary.NewSummarizer(model,
    summary.WithSkipRecent(func(events []event.Event) int {
        cutoff := time.Now().Add(-5 * time.Minute)
        skip := 0
        for i := len(events) - 1; i >= 0; i-- {
            if events[i].Timestamp.After(cutoff) {
                skip++
            } else {
                break
            }
        }
        return skip
    }),
    summary.WithEventThreshold(10),
)

// Skip trailing tool-call messages only
sum := summary.NewSummarizer(model,
    summary.WithSkipRecent(func(events []event.Event) int {
        skip := 0
        for i := len(events) - 1; i >= 0; i-- {
            if events[i].Response != nil && len(events[i].Response.Choices) > 0 &&
                events[i].Response.Choices[0].Message.Role == model.RoleTool {
                skip++
            } else {
                break
            }
        }
        return skip
    }),
    summary.WithEventThreshold(10),
)
```

## Key design choices

- Do not modify or truncate original `events`.
- Do not insert summary as an event. Summary is stored separately.
- Session service handles incremental processing for summarization automatically.
- Default trigger uses an event-count threshold aligned with Python (`>` semantics).
- Summary generation is asynchronous by default (non-blocking) with configurable worker pools.
- Summary injection into LLM prompts is automatic and implicit.

## Files

- `main.go`: Interactive chat with manual summary commands and automatic background summarization.

## Notes

- If the summarizer is not configured, the service logs a warning and skips summarization.
- No fallback summaries by string concatenation are provided. The system relies on the configured LLM.
