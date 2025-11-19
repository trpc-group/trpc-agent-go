# React Graph Streaming Example

This example showcases a minimal Planner → Reasoning → Tool → FinalAnswer → FormatOutput workflow built with `StateGraph`, `GraphAgent`, and `Runner`. It focuses on streaming-friendly output for React-style agents and demonstrates how to:

- Build a multi-node graph with `graph.MessagesStateSchema()`
- Register a simple calculator tool via `function.NewFunctionTool`
- Use `AddToolsConditionalEdges` to branch between tool execution and final answer
- Stream planner/reasoning/tool/final nodes in a fixed order with emoji prefixes
- Support both interactive prompts and the `-question` flag for one-off runs

## Graph Structure

- `Planner` produces a short numbered plan and hands control to `Reasoning`.
- `Reasoning` either emits a JSON tool call or describes the final conclusion.
- `AddToolsConditionalEdges` inspects the last message: tool calls route to the `Tools` node; otherwise it goes directly to `FinalAnswer`.
- `FormatOutput` collects the final answer plus per-node transcripts into JSON.

## Streaming Output Contract

`streamEvents` enforces a strict, real-time log format:

1. `[Plan]`, `[Reasoning]`, `[Action]`, `[FinalAnswer]`, `[FormatOutput]` headers appear in order.
2. Planner/Reasoning content streams with the `🤖` prefix.
3. Tool calls appear as `🔧 { ... }`; tool responses show as `✅ Tool result: ...`.
4. Final answers stream with `🤖`, followed by a JSON summary block.

The latest run is saved to `examples/graph/react/server.log.txt` for reference.

## Usage

From the repository root:

```bash
go run ./examples/graph/react
```

You can also supply a one-off prompt:

```bash
go run ./examples/graph/react -question "123+456"
```

Flags:

- `-model`: Override the default `deepseek-chat` OpenAI-compatible model.
- `-question`: Provide a non-interactive question; otherwise an interactive prompt is shown.

## Sample Output

```log
Enter a question and press Enter (examples: 'How much is 12 + 7?'):
Question> 2*(4+3)
---------- /*PLANNING*/ ----------
🤖 1. Identify the expression structure: a multiplication operation with a nested addition inside parentheses.  
2. Follow the order of operations (PEMDAS/BODMAS): resolve the parentheses first.  
3. Calculate the result of the addition inside the parentheses.  
4. Multiply the result by the number outside the parentheses.  
5. Provide the final numerical answer.
---------- /*REASONING*/ ----------
🤖 I need to calculate the expression 2*(4+3) by first evaluating the addition inside the parentheses and then performing the multiplication.
---------- /*ACTION*/ ----------
🔧 {"operation": "add", "a": 4, "b": 3}
✅ Tool result: {"operation":"add","a":4,"b":3,"result":7}
---------- /*REASONING*/ ----------
🤖 Now I need to multiply the result (7) by 2 to complete the calculation.
---------- /*ACTION*/ ----------
🔧 {"operation": "multiply", "a": 2, "b": 7}
✅ Tool result: {"operation":"multiply","a":2,"b":7,"result":14}
---------- /*REASONING*/ ----------
🤖 The expression 2*(4+3) evaluates to 14. First, I calculated 4+3=7, then multiplied 2×7=14.
---------- /*FINAL_ANSWER*/ ----------
🤖 The result of 2*(4+3) is **14**.

Here's the step-by-step calculation:
1. First, evaluate the expression inside the parentheses: 4 + 3 = 7
2. Then multiply: 2 × 7 = 14
[FormatOutput]
{
  "final_answer": "The result of 2*(4+3) is **14**.\n\nHere's the step-by-step calculation:\n1. First, evaluate the expression inside the parentheses: 4 + 3 = 7\n2. Then multiply: 2 × 7 = 14",
  "node_responses": {
    "finalanswer": "The result of 2*(4+3) is **14**.\n\nHere's the step-by-step calculation:\n1. First, evaluate the expression inside the parentheses: 4 + 3 = 7\n2. Then multiply: 2 × 7 = 14",
    "planner": "1. Identify the expression structure: a multiplication operation with a nested addition inside parentheses.  \n2. Follow the order of operations (PEMDAS/BODMAS): resolve the parentheses first.  \n3. Calculate the result of the addition inside the parentheses.  \n4. Multiply the result by the number outside the parentheses.  \n5. Provide the final numerical answer.",
    "reasoning": "The expression 2*(4+3) evaluates to 14. First, I calculated 4+3=7, then multiplied 2×7=14."
  }
}
```

## Requirements

- Go 1.21+
- A valid OpenAI-compatible API key (used via `openai.New`)

## Customization Ideas

- Swap the calculator tool with domain-specific functions.
- Modify the planner/reasoning prompts to match your task.
- Extend the state schema with additional fields and format them in `formatOutput`.
