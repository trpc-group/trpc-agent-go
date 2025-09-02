# OneShot Override Example

This example demonstrates the `one_shot_messages` feature that allows complete control over the current round's input.

## Key Features

- **Complete input override**: `one_shot_messages` takes complete precedence over other input sources.
- **System + User combination**: Can include both system prompts and user messages in a single round.
- **Automatic persistence**: If the last message is from user, it's automatically persisted to durable history.
- **Atomic clearing**: The one-shot messages are cleared after successful execution.

## How It Works

1. **Set OneShot**: Write complete message sequence to `StateKeyOneShotMessages`.
2. **LLM Execution**: The LLM node prioritizes one-shot messages over all other inputs.
3. **Smart Persistence**: 
   - If tail is user → `ReplaceLastUser` in durable history
   - If tail is not user → append user message to durable history
4. **Clear OneShot**: After successful execution, one-shot messages are cleared.

## Use Cases

- **Custom system prompts**: Override the node's default system prompt for specific rounds.
- **Multi-turn input**: Provide a complete conversation context for a single round.
- **Testing scenarios**: Inject specific message sequences for testing or debugging.
- **Advanced workflows**: Complex input patterns that can't be expressed with simple user input.

## Example Usage

```go
state[graph.StateKeyOneShotMessages] = []model.Message{
    model.NewSystemMessage("You are a creative storyteller."),
    model.NewUserMessage("Tell me a story about a robot learning to paint."),
}
```

### Run the executable example

```bash
go run . -model deepseek-chat -sys "You are a creative storyteller." -input "Tell me a story about a robot learning to paint."
```

You can omit `-input` to type the prompt interactively.

### What you will see

- Streaming assistant output.
- A final verification line:

```
🔍 Verification: one_shot_messages cleared after execution.
```

This confirms OneShot messages are consumed in this round and then cleared.

## Expected Behavior

- **Priority**: OneShot > UserInput > History
- **Persistence**: User messages are automatically persisted to durable history
- **Clearing**: OneShot messages are cleared after successful execution
- **Atomicity**: All state updates happen atomically

## Benefits

- **Flexibility**: Complete control over input for specific rounds
- **Efficiency**: No need to modify durable history for temporary input changes
- **Cleanliness**: One-shot messages don't pollute the conversation history
- **Power**: Can express complex input patterns that simple user input cannot
