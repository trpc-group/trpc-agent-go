# User Input Once Example

This example demonstrates the new `user_input` pattern where user input is consumed once and then cleared after successful LLM execution.

## Key Changes

- **No more direct injection into `messages`**: The entry point no longer writes user messages directly to the durable `messages` state.
- **Ephemeral `user_input`**: User input is stored in `StateKeyUserInput` and consumed once by the LLM node.
- **Automatic persistence**: The LLM node automatically handles persisting the user input to the durable `messages` state.

## How It Works

1. **Entry Point**: Sets `StateKeyUserInput` with the user's message content.
2. **LLM Node**: 
   - Reads from `user_input` if available
   - Applies system prompt
   - Executes the model
   - Atomically updates `messages` and clears `user_input`
3. **Result**: User input is persisted exactly once, avoiding duplication.

## Benefits

- **No duplicate messages**: User input won't be repeated across multiple executions.
- **Cleaner state management**: Clear separation between ephemeral input and durable history.
- **Atomic updates**: State changes happen atomically, preventing race conditions.

## Usage

```bash
go run examples/graph/user_input_once/main.go "Hello, how are you?"
```

## Expected Output

```
[system] You are a helpful assistant.
[user] Hello, how are you?
[assistant] Hello! I'm doing well, thank you for asking...
```

## Implementation Details

The LLM node implements a three-stage rule:
1. **OneShot messages** (highest priority)
2. **UserInput** (fallback)
3. **History** (default)

This ensures flexible input handling while maintaining clean state management.
