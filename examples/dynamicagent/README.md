# Subtask Delegation Example

Demonstrates the `subtask` tool for ephemeral call-return delegation: the parent agent spawns a sub-agent in an isolated context scope, the sub-agent executes the task, and only the final result returns to the parent.

## Why?

LLM attention is a finite resource — as the context window grows, attention to key information dilutes. When the parent agent recognizes a sub-task will generate heavy intermediate reasoning (tool calls, retries, exploration), it delegates to a sub-agent with an independent context. The sub-agent may consume tens of thousands of tokens internally, but the parent only sees the final result — creating a **context compression boundary**.

## Setup

```go
llmAgent := llmagent.New("my-agent",
    llmagent.WithModel(model),
    llmagent.WithTools(userTools),
    llmagent.WithTemporarySubtasks(),  // enables the subtask tool
)
```

## Run

```bash
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_API_KEY="your-key"
cd examples/dynamicagent
go run . -model=gpt-4o-mini
```

## Example: Subtask with Context Isolation

**Prompt:**

```text
Use the subtask tool to calculate 2^10 * 3^5 step by step
```

### Execution Trace

The entire interaction involves **6 LLM calls**: 2 by the parent agent, 4 by the child agent.

#### Step 1 — Parent agent decides to delegate

```text
[Model Before] messages=2 tools=2

Input:
  messages[0]: system prompt ("You are a helpful assistant...")
  messages[1]: user ("Use the subtask tool to calculate 2^10 * 3^5 step by step")
  tools: calculator, subtask

[Model After] done=true content="" tool_calls=1
  → subtask({"request": "Calculate the value of 2^10 * 3^5 step by step."})
```

The model autonomously chose the `subtask` tool to delegate the work.

#### Step 2 — Child agent starts (clean context)

```text
[Model Before] messages=2 tools=1

Input:
  messages[0]: same system prompt (inherited from parent)
  messages[1]: user ("Calculate the value of 2^10 * 3^5 step by step.")  ← the subtask request
  tools: calculator (subtask and transfer_to_agent are stripped from child)

[Model After] done=true content="" tool_calls=2
  → calculator({"operation": "power", "a": 2, "b": 10})  → 1024
  → calculator({"operation": "power", "a": 3, "b": 5})   → 243
```

**Key**: `messages=2` — the child starts from a completely clean context. `tools=1` — the child only sees user tools; framework tools (`subtask`, `transfer_to_agent`) are stripped to prevent recursive spawning.

#### Step 3 — Child agent continues (multi-turn tool calling)

```text
[Model Before] messages=5 tools=1

Input:
  messages[0]: system prompt
  messages[1]: user message
  messages[2]: assistant (tool_calls: calculator x2)
  messages[3]: tool result: 1024
  messages[4]: tool result: 243

[Model After] done=true content="" tool_calls=1
  → calculator({"operation": "multiply", "a": 1024, "b": 243})  → 248832
```

The child's session correctly maintains multi-turn context within its isolated scope.

#### Step 4 — Child agent produces final answer

```text
[Model Before] messages=7 tools=1

[Model After] done=true content="The value of 2^{10} × 3^5 is 248,832." tool_calls=0
```

Messages grew to 7 within the child's scope. The child outputs a text result and its flow ends.

#### Step 5 — Back to parent agent

```text
[Model Before] messages=4 tools=2

Input:
  messages[0]: system prompt
  messages[1]: user ("Use the subtask tool to calculate 2^10 * 3^5 step by step")
  messages[2]: assistant tool_call: subtask({...})
  messages[3]: tool result: "The value of 2^{10} × 3^5 is 248,832."

[Model After] done=true content="The value of 2^{10} × 3^5 is 248,832." tool_calls=0
```

**Key**: `messages=4` — the parent sees only 4 messages. The child's 7-message, 3-tool-call intermediate process is completely invisible. The parent only receives the final result string.

### Context Compression Summary

| | Parent agent | Child agent |
|---|---|---|
| LLM calls | 2 | 4 |
| Max messages | 4 | 7 |
| Tool calls | 1 (subtask) | 3 (calculator x3) |
| What parent sees | Final result string only | — |

The child accumulated 7 messages and 3 tool calls internally, but the parent's context stayed at 4 messages. This is the **context compression boundary** — intermediate reasoning noise is contained within the child's scope, keeping the parent's context clean for subsequent work.
