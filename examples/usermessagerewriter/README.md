# User Message Rewriter Demo

This example demonstrates the `agent.WithUserMessageRewriter(...)` hook.

It keeps the terminal input as the raw user message, but rewrites the current
turn into an ordered `[]model.Message` before Runner persists the turn into
`session.Events`.

For the demo, the rewriter uses two simple English prefixes:

- `rewrite ...`
  - rewrite the current turn into exactly one new `user` message
- `expand ...`
  - expand the current turn into two `user` messages

- `1 -> 1` rewrite
- `1 -> 2` expansion

## Highlights

- **Current-turn rewrite**: Rewrite one user input into one or more persisted messages.
- **Transcript persistence**: Rewritten messages are stored in `session.Events`.
- **Multi-turn carry-over**: Context injected on one turn is available in later turns.
- **OpenAI-compatible endpoint**: Uses `openai.New(...)`, so the OpenAI SDK reads `OPENAI_API_KEY` and `OPENAI_BASE_URL` from environment variables automatically.

## Prerequisites

- Go 1.21 or later
- A valid OpenAI-compatible API key

Environment variables:

- `OPENAI_API_KEY`
- `OPENAI_BASE_URL` (optional for standard OpenAI, required for custom compatible endpoints)

## Run It

If you have already exported `OPENAI_API_KEY` and `OPENAI_BASE_URL`, you can run:

```bash
cd examples/usermessagerewriter
go run . -model deepseek-v4-flash -streaming=true
```

Interactive commands:

- `/dump`: print the persisted session transcript
- `/exit`: quit the demo

If your OpenAI-compatible endpoint does not provide `deepseek-v4-flash`, pass a
different model explicitly. The example was verified with:

```bash
cd examples/usermessagerewriter
go run . -model gpt-4o-mini -streaming=false
```

## Suggested prompts

Try the following sequence:

1. `rewrite we have received your feedback and will reply soon`
2. `What did I ask just now?`
3. `expand please help me reply to this urgent order issue`
4. `What extra context do you still remember?`

On turn 1, the rewriter produces one replacement message. On turn 3, the
rewriter produces two persisted messages. Turns 2 and 4 demonstrate that the
rewritten transcript remains available in later turns.

After any turn, type `/dump` to inspect the actual persisted `session.Events`
transcript. This is the most direct way to verify that the rewriter effect was
stored as expected.

## Verified Delivery

The example was executed locally against an OpenAI-compatible endpoint with
`-model gpt-4o-mini -streaming=false`.

Observed `rewrite` run:

```text
👤 You: rewrite we have received your feedback and will reply soon
✏️ Rewriter replaced current turn with:
   Please rewrite the following request into one concise support-style sentence: we have received your feedback and will reply soon
🤖 Assistant: Thank you for your feedback; we will respond shortly.
```

Observed `/dump` after the `rewrite` turn:

```text
🗂️ Persisted session transcript:
   1. [user] Please rewrite the following request into one concise support-style sentence: we have received your feedback and will reply soon
   2. [assistant] Thank you for your feedback; we will respond shortly.
```

Observed `expand` run:

```text
👤 You: expand please help me reply to this urgent order issue
📚 Rewriter expanded current turn into:
   1. Reference context: Treat the following request as urgent and answer with explicit next steps.
   2. please help me reply to this urgent order issue
🤖 Assistant: To address the urgent order issue, please provide the order number and a brief description of the problem, and I will assist you in resolving it promptly.
```

Observed `/dump` after the `expand` turn:

```text
🗂️ Persisted session transcript:
   1. [user] Please rewrite the following request into one concise support-style sentence: we have received your feedback and will reply soon
   2. [assistant] Thank you for your feedback; we will respond shortly.
   3. [user] Reference context: Treat the following request as urgent and answer with explicit next steps.
   4. [user] please help me reply to this urgent order issue
   5. [assistant] To address the urgent order issue, please provide the order number and a brief description of the problem, and I will assist you in resolving it promptly.
```

## What this example shows

- The caller still sends a raw `model.NewUserMessage(userInput)`.
- If the input starts with `rewrite`, the rewriter returns exactly one rewritten
  `user` message.
- If the input starts with `expand`, the rewriter returns exactly two `user`
  messages.
- Otherwise, the rewriter returns the original single `user` message unchanged.
- Runner persists the rewritten message list in order.
- Future turns continue from the rewritten transcript without extra caller work.
