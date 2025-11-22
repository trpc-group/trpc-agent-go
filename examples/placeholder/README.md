# Placeholder Demo - Session State Integration

This example demonstrates how to use placeholders in agent instructions with
session service integration. It covers three levels of state management:

- **Session-level state**: `{research_topics}` - Session-specific, updatable via `UpdateSessionState` API
- **User-level state**: `{user:topics}` - Shared across all sessions for a user, updatable via `UpdateUserState` API
- **App-level state**: `{app:banner}` - Shared across all users, updatable via `UpdateAppState` API

## Overview

The demo implements an interactive command-line application that:
1. **Session-level State**: `{research_topics}` can be updated via `UpdateSessionState` API
2. **User-level State**: `{user:topics}` can be updated via `UpdateUserState` API
3. **App-level State**: `{app:banner}` can be updated via `UpdateAppState` API
4. **Dynamic Updates**: Changes to any state level affect future responses immediately
5. **Interactive Commands**: Command-line interface for managing all three state levels

## Key Features

- **Placeholder Replacement**: `{research_topics}`, `{user:topics}`, `{app:banner}` are resolved from session state
- **Three-tier State Management**: Session-level, user-level, and app-level state with different scopes
- **Interactive Commands**: `/set-session-topics`, `/set-user-topics`, `/set-app-banner`, `/show-state`
- **Real-time Updates**: Changes to any state level immediately affect agent behavior
- **New UpdateSessionState API**: Demonstrates direct session state updates without creating events

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

- **Purpose**: Specialized research assistant using placeholder values from three state levels
- **Instructions**: Contains `{research_topics}` (session-level), `{user:topics?}` (user-level),
  and `{app:banner?}` (app-level)
- **Behavior**: Adapts based on session state; optional markers `?` allow the
  instruction to render even when a value is absent

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

- **Set session topics** (session-level state):
  ```bash
  /set-session-topics blockchain, web3, NFT, DeFi
  ```
  Updates `{research_topics}` via `UpdateSessionState` API. This is session-specific
  and only affects the current conversation.

- **Set user topics** (user-level state):
  ```bash
  /set-user-topics quantum computing, cryptography
  ```
  Updates `{user:topics}` via `UpdateUserState` API. This is shared across all
  sessions for the same user.

- **Set app banner** (app-level state):
  ```bash
  /set-app-banner Research Mode
  ```
  Updates `{app:banner}` via `UpdateAppState` API. This is shared across all users.

- **Show current state** snapshot:
  ```bash
  /show-state
  ```
  Prints the current merged session state showing all three levels:
  `research_topics` (session), `user:topics` (user), `app:banner` (app).

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
Features: Unprefixed readonly and prefixed placeholders
Commands:
  /set-session-topics <topics> - Update session-level research topics
  /set-user-topics <topics>    - Update user-level topics
  /set-app-banner <text>       - Update app-level banner
  /show-state                  - Show current session state
============================================================

💡 Example interactions:
   • Ask: 'What are the latest developments?'
   • Update session topics: /set-session-topics 'blockchain, web3, NFT'
   • Set user topics: /set-user-topics 'quantum computing, cryptography'
   • Set app banner: /set-app-banner 'Research Mode'
   • Show state: /show-state
   • Ask: 'Explain recent breakthroughs'

👤 You: /show-state
📋 Current Session State:
   - research_topics: artificial intelligence, machine learning, deep learning, neural networks
   - user:topics: quantum computing, cryptography
   - app:banner: Research Mode

👤 You: /set-session-topics blockchain, web3, NFT, DeFi
✅ Session research topics updated to: blockchain, web3, NFT, DeFi
💡 The agent will now focus on these new topics in subsequent queries.

👤 You: What are the latest developments?
🔬 Research Agent: Based on the current research focus on blockchain, web3, NFT, and DeFi, here are the latest developments...
```

## Implementation Details

### Placeholder Mechanism

1. **Session-level State**: `{research_topics}` is session-specific and can be updated
   via `UpdateSessionState` API. Changes only affect the current session.
2. **User-level State**: `{user:topics}` resolves to user state and can be updated
   via `UpdateUserState` API. Changes affect all sessions for that user.
3. **App-level State**: `{app:banner}` resolves to app state and can be updated
   via `UpdateAppState` API. Changes affect all users.
4. **Optional Suffix**: `{...?}` returns empty string if the variable is not present.

### Session State Management

The demo uses in-memory session service for simplicity:

- **Session State**: `research_topics` stored at session level (referenced as `{research_topics}`)
- **User State**: `topics` stored at user level (referenced as `{user:topics}`)
- **App State**: `banner` stored at app level (referenced as `{app:banner}`)
- **State Persistence**: All three levels maintained throughout session
- **Real-time Updates**: Changes at any level immediately available to agent
- **State Isolation**: Each level has its own scope and lifetime

### Command Processing

The interactive interface processes commands through pattern matching:

- **State Commands**:
  - `/set-session-topics` - Updates session-level state via `UpdateSessionState`
  - `/set-user-topics` - Updates user-level state via `UpdateUserState`
  - `/set-app-banner` - Updates app-level state via `UpdateAppState`
  - `/show-state` - Displays merged state from all three levels
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