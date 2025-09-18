# 📝 Session Summarization Example

This example demonstrates LLM-driven session summarization integrated with the framework's `Runner` and `session.Service`.

- Preserves original `events`.
- Stores summary separately from events (not inserted as system events).
- Feeds LLM with "latest summary + incremental event window" to control context size.
- Uses `SessionSummarizer` directly with session service for summarization.

## What it shows

- LLM-based summarization per session turn.
- Simple trigger configuration using event-count threshold.
- Prompt construction that injects the latest summary and recent events.
- Backend-specific persistence:
  - In-memory service mirrors summary text to `sess.State`.
  - Redis service mirrors summary text to Redis `SessionState.State` (see service code).

## Prerequisites

- Go 1.23 or later.
- Model configuration (e.g., OpenAI-compatible) via environment variables.

Environment variables:

- `OPENAI_API_KEY`: API key for model service.
- `OPENAI_BASE_URL` (optional): Base URL for the model API endpoint.

## Run

```bash
cd examples/summary
export OPENAI_API_KEY="your-api-key"
go run main.go -model gpt-4o-mini -window 25
```

Quick start with immediate summarization:

```bash
go run main.go -events 0 -tokens 0 -timeSec 0
```

Command-line flags:

- `-model`: Model name to use for both chat and summarization. Default: `deepseek-chat`.
- `-streaming`: Enable streaming mode for responses. Default: `true`.
- `-window`: Number of recent events to keep for summarization input. Default: `50`.
- `-events`: Event count threshold to trigger summarization. Default: `1`.
- `-tokens`: Token-count threshold to trigger summarization (0=disabled). Default: `0`.
- `-timeSec`: Time threshold in seconds to trigger summarization (0=disabled). Default: `0`.
- `-maxlen`: Max generated summary length (0=unlimited). Default: `0`.

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
Window: 50
EventThreshold: 1
TokenThreshold: 0
TimeThreshold: 0s
MaxLen: 0
Streaming: true
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
- The `Runner` automatically triggers summarization asynchronously after completion via `CreateSessionSummary`.
- The `SessionSummarizer` generates summaries using the configured LLM model.
- The `session.Service` stores summary text in its backend storage (in-memory or Redis).
- Summary injection happens automatically in the `ContentRequestProcessor` for subsequent turns.

## Key design choices

- Do not modify or truncate original `events`.
- Do not insert summary as an event. Summary is stored separately.
- `window` (keepRecentCount) controls the number of recent events used for summarization input.
- Default trigger uses an event-count threshold aligned with Python (`>` semantics).
- Summary generation is asynchronous by default (non-blocking).
- Summary injection into LLM prompts is automatic and implicit.

## Files

- `main.go`: Interactive chat with manual summary commands and automatic background summarization.

## Notes

- If the summarizer is not configured, the service logs a warning and skips summarization.
- No fallback summaries by string concatenation are provided. The system relies on the configured LLM.
