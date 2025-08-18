# Placeholder Demo - Session State Integration

This example demonstrates how to use placeholders in agent instructions with
session service integration. It covers two kinds of placeholders:

- Unprefixed placeholder (readonly): `{research_topics}`. Initialized when the
  session is created and intended not to be modified at runtime.
- Prefixed placeholders (modifiable): `{user:topics}` and `{app:banner}`.
  These are backed by user/app state and can be updated via the session
  service APIs.

## Overview

The demo implements an interactive command-line application that:
1. **Unprefixed Placeholder (Readonly)**: `{research_topics}` is set at session
   creation and is not meant to be mutated.
2. **Prefixed Placeholders (Mutable)**: `{user:topics}` and `{app:banner}` can be
   updated using the session service.
3. **Dynamic Updates**: Changes to user/app state affect future responses.
4. **Interactive Commands**: Command-line interface for managing session state

## Key Features

- **Placeholder Replacement**: `{research_topics}`, `{user:topics}`,
  `{app:banner}` are resolved from session state.
- **Session State Management**: In-memory session service stores app/user state.
- **Interactive Commands**: `/set-user-topics`, `/set-app-banner`, `/show-state`.
- **Real-time Updates**: Changes to session state immediately affect agent behavior

## Architecture

```
User Input → Session State → Placeholder Replacement → Agent Execution
     ↓              ↓                    ↓                    ↓
Commands  {research_topics} / {user:topics} / {app:banner}    Dynamic Values
                                                           Research Results
```

## Components

### PlaceholderDemo

The main application structure that manages the interactive session:

```go
type placeholderDemo struct {
    modelName      string
    runner         runner.Runner
    sessionService session.Service
    userID         string
    sessionID      string
}
```

**Features:**
- Manages session state for placeholder values
- Handles interactive command-line interface
- Processes agent responses with streaming support
- Provides real-time state updates

### Research Agent

- **Purpose**: Specialized research assistant using placeholder values.
- **Instructions**: Contains `{research_topics}` (readonly), `{user:topics?}`
  and `{app:banner?}`.
- **Behavior**: Adapts based on session state; optional markers `?` allow the
  instruction to render even when a value is absent.

## Usage

### Building and Running

```bash
# Build the example
go build -o placeholder-demo main.go

# Run with default model
./placeholder-demo

# Run with specific model
./placeholder-demo -model deepseek-chat
```

### Interactive Commands

The demo supports several interactive commands:

- Set user topics (user state):
  ```bash
  /set-user-topics quantum computing, cryptography
  ```
  Updates `{user:topics}` via `UpdateUserState`.

- Set app banner (app state):
  ```bash
  /set-app-banner Research Mode
  ```
  Updates `{app:banner}` via `UpdateAppState`.

- Show current state snapshot:
  ```bash
  /show-state
  ```
  Prints the current merged session state so you can see the keys:
  `research_topics`, `user:topics`, `app:banner`.

#### Regular Queries
```bash
What are the latest developments?
```
Ask research questions. The agent will use the current topics from session state.

#### Exit
```bash
exit
```
Ends the interactive session.

### Example Session

```
🔑 Placeholder Demo - Session State Integration
Model: deepseek-chat
Type 'exit' to end the session
Features: Dynamic placeholder replacement with session state
Commands: /set-user-topics <topics>, /set-app-banner <text>, /show-state
============================================================

💡 Example interactions:
   • Ask: 'What are the latest developments?'
   • Set user topics: /set-user-topics 'quantum computing, cryptography'
   • Set app banner: /set-app-banner 'Research Mode'
   • Show state: /show-state
   • Ask: 'Explain recent breakthroughs'

👤 You: /show-topics
📋 Current research topics: artificial intelligence, machine learning, deep learning, neural networks

👤 You: /set-topics quantum computing, cryptography
✅ Research topics updated to: quantum computing, cryptography

👤 You: What are the latest developments?
🔬 Research Agent: Based on the current research focus on quantum computing and cryptography, here are the latest developments...
```

## Implementation Details

### Placeholder Mechanism

1. **Initial Setup**: Session is created with an unprefixed
   `research_topics` value used by `{research_topics}` (readonly).
2. **Prefixed Placeholders**: `{user:topics}` and `{app:banner}` resolve to
   user/app state; they are populated by `UpdateUserState` and
   `UpdateAppState`.
3. **Optional Suffix**: `{...?...}` returns empty string if the variable is not
   present.

### Session State Management

The demo uses in-memory session service for simplicity:

- **User State**: `topics` stored at user level (referenced as `{user:topics}`).
- **Session Persistence**: State maintained throughout session
- **Real-time Updates**: Changes immediately available to agent

### Command Processing

The interactive interface processes commands through pattern matching:

- **State Commands**: `/set-user-topics`, `/set-app-banner`, `/show-state` for
  session management
- **Regular Input**: Passed directly to agent for processing
- **Error Handling**: Graceful handling of invalid commands and state errors

## Benefits

1. **Dynamic Behavior**: Agent behavior adapts based on session state
2. **User Control**: Users can customize agent focus during runtime
3. **Session Persistence**: State maintained across multiple interactions
4. **Interactive Experience**: Command-line interface for easy state management
5. **Real-time Updates**: Changes take effect immediately

## Production Considerations

When using this pattern in production:

1. **Persistent Storage**: Use persistent session service (Redis, database) for data durability
2. **Security**: Implement proper access controls for session state
3. **Validation**: Add input validation for placeholder values
4. **Monitoring**: Add logging for placeholder replacements and state changes
5. **Error Handling**: Implement retry logic for session state operations
6. **Caching**: Consider caching frequently accessed state data

## Related Examples

- [Basic Chain Agent](../chainagent/): Simple agent chaining
- [Tool Integration](../tools/): Various tool usage patterns
- [Session Management](../session/): Session state management examples 