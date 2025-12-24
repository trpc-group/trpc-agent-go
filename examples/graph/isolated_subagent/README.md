# Isolated Subagent Example

This example demonstrates the correct usage of `WithSubgraphIsolatedMessages(true)`, showing how to isolate a child agent's session history within a Graph.

## Scenario

- **Parent Graph**: Contains preprocess node, agent node, and collect node
- **Child LLMAgent**: Has a calculator tool and uses the default builtin planner
- **Isolation Setting**: `WithSubgraphIsolatedMessages(true)` prevents the child from inheriting parent history

## Key Features

`WithSubgraphIsolatedMessages(true)` achieves two goals:

1. **Isolates parent history** - The child agent does not see the parent Graph's session history
2. **Preserves current invocation history** - The child agent's tool call history within the current invocation is correctly preserved

This ensures the ReAct loop works properly: after the agent calls a tool, it can see the tool result and provide the final answer.

## Usage

```bash
# Set environment variables
export OPENAI_BASE_URL="your-api-base-url"
export OPENAI_API_KEY="your-api-key"
export MODEL_NAME="your-model-name"  # Optional, defaults to deepseek-chat

# Run the example
go run . -question "What is 12 + 7?"

# Or interactive mode
go run .
```

## Command Line Arguments

| Argument | Default | Description |
|----------|---------|-------------|
| `-question` | (interactive) | User question |
| `-isolate` | `true` | Enable WithSubgraphIsolatedMessages |
| `-max-iter` | `3` | Maximum tool iterations |
| `-model` | `deepseek-chat` | LLM model name |
| `-react` | `false` | Use ReActPlanner (default uses builtin) |
| `-v` | `false` | Verbose output |

## Expected Behavior

```
================================================================
Isolated Subagent Demo - WithSubgraphIsolatedMessages Example
================================================================
Model: deepseek-chat
Isolate messages: true (WithSubgraphIsolatedMessages)
...

Tool call #1: calculator({"operation":"add","a":12,"b":7})
Tool result: {"operation":"add","a":12,"b":7,"result":19}
12 + 7 = 19

----------------------------------------------------------------
Total tool calls: 1
Success! The agent correctly called the tool only once.
```

The agent should call the calculator tool only once, then return the result.
