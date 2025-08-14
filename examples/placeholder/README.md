# Placeholder Demo - Session State Integration

This example demonstrates how to use placeholders in agent instructions with session service integration. The demo shows how `{research_topics}` placeholder gets replaced with actual values from session state during agent execution.

## Overview

The demo implements an interactive command-line application that:
1. **Uses Placeholders**: Agent instructions contain `{research_topics}` placeholder
2. **Session State Integration**: Placeholder values are stored and retrieved from session state
3. **Dynamic Updates**: Users can change research topics during runtime
4. **Interactive Commands**: Command-line interface for managing session state

## Key Features

- **Placeholder Replacement**: `{research_topics}` in agent instructions gets replaced with session state values
- **Session State Management**: In-memory session service for storing placeholder values
- **Interactive Commands**: `/set-topics` and `/show-topics` commands for state management
- **Real-time Updates**: Changes to session state immediately affect agent behavior

## Architecture

```
User Input → Session State → Placeholder Replacement → Agent Execution
     ↓              ↓                    ↓                    ↓
Commands      {research_topics}      Dynamic Value      Research Results
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

- **Purpose**: Specialized research assistant using placeholder values
- **Instructions**: Contains `{research_topics}` placeholder for dynamic topic focus
- **Behavior**: Automatically adapts research focus based on session state

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

#### Set Research Topics
```bash
/set-topics quantum computing, cryptography, blockchain
```
Updates the research topics in session state. The agent will focus on these topics in future interactions.

#### Show Current Topics
```bash
/show-topics
```
Displays the current research topics stored in session state.

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
Commands: /set-topics <topics>, /show-topics
============================================================

💡 Example interactions:
   • Ask: 'What are the latest developments?'
   • Set topics: /set-topics 'quantum computing, cryptography'
   • Show topics: /show-topics
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

The placeholder system works through session state integration:

1. **Initial Setup**: Session created with default research topics
2. **Placeholder in Instructions**: Agent instructions contain `{research_topics}`
3. **Runtime Replacement**: Runner replaces placeholder with actual session state value
4. **Dynamic Updates**: Users can change topics, affecting future agent responses

### Session State Management

The demo uses in-memory session service for simplicity:

- **User State**: Research topics stored at user level
- **Session Persistence**: State maintained throughout session
- **Real-time Updates**: Changes immediately available to agent

### Command Processing

The interactive interface processes commands through pattern matching:

- **State Commands**: `/set-topics` and `/show-topics` for session management
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