# 📝 Summary Injection Mode Example

This example demonstrates the two session summary injection modes:

- **System mode** (default): Summary is merged into the system message, protected from token tailoring trimming.
- **User mode**: Summary is injected as a user message before session history, participating in token-budget trimming for a true sliding-window experience. When the first history message is also a user message, the summary is automatically merged into it.

## What it shows

- Two sessions with identical conversation history — each using a different injection mode for the follow-up turn.
- The actual message sequence sent to the LLM is printed via a `BeforeModel` callback, so you can see exactly where the summary appears and with which role.
- In **system mode**, the summary is merged into the system prompt.
- In **user mode**, the summary is merged into the first user history message (avoiding consecutive user messages).

## Prerequisites

- Go 1.21 or later.
- Model configuration via environment variables.

Environment variables:

- `OPENAI_API_KEY`: API key for model service.
- `OPENAI_BASE_URL` (optional): Base URL for the model API endpoint.

## Run

```bash
cd examples/summary/injection
export OPENAI_API_KEY="your-api-key"
go run main.go
```

Command-line flags:

- `-model`: Model name. Default: `deepseek-chat`.

## Example Output

```text
🧪 Summary Injection Mode Demo
Model: deepseek-chat
======================================================================
== Phase 1: Build conversation history (session: injection-base-...) ==

👤 User: My name is Alice and I work at TechCorp as a senior engineer.
🤖 Assistant: Nice to meet you, Alice! ...
👤 User: I'm working on a distributed cache system using Go and Redis.
🤖 Assistant: That sounds like an interesting project! ...

📝 Forcing summary generation...
✅ Summary: Alice is a senior engineer at TechCorp ...

======================================================================
== Phase 2: System injection mode (default) ==

🧾 LLM request #3 (2 messages):
   [0] role=system    You are a helpful assistant. ... Here is a brief summary... ← SUMMARY
   [1] role=user      Based on our previous discussion, what language am I using?

🤖 Assistant: You are using **Go** ...

======================================================================
== Phase 3: User injection mode (session: injection-user-...) ==

🧾 LLM request #6 (2 messages):
   [0] role=system    You are a helpful assistant. Be concise, keep responses under 80 words.
   [1] role=user      Context from previous interactions: ... Based on our previous discussion... ← SUMMARY (merged)

🤖 Assistant: You are using **Go** ...

== Comparison ==
• System mode: summary merged into system message (preserved head, not trimmable)
• User mode:   summary as user message (participates in token-budget trimming)
```

## Key Observations

1. **System mode**: Summary appears inside `messages[0]` (system role), merged with agent instruction.
2. **User mode**: Summary is merged into the first user message alongside the follow-up question. Token tailoring can trim it like any other user round.
3. Both modes produce correct responses — the LLM can access the summary context regardless of injection position.
4. Phase 2 and Phase 3 run on separate sessions with identical history, ensuring a clean A/B comparison.
