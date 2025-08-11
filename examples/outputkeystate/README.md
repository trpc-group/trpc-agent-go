# Output Key State Chain Example

This example demonstrates how to use ChainAgent with output keys and session state access tools to create a research and content creation pipeline.

## Overview

The example implements a two-agent chain:
1. **Research Agent**: Conducts comprehensive research and stores findings in session state
2. **Writer Agent**: Retrieves research data using a state access tool and creates engaging content

## Key Features

- **State-based Data Flow**: Research findings are stored in session state using `WithOutputKey`
- **Tool Integration**: Writer agent uses a custom tool to access session state data
- **Real-world Workflow**: Mimics actual content creation pipelines used in production
- **No Output Schema**: Removed `WithOutputSchema` to allow full agent capabilities

## Architecture

```
User Query → Research Agent → Session State → Writer Agent → Final Content
                ↓                    ↓              ↓
         Stores findings      State access tool   Creates content
         with output_key      retrieves data      based on research
```

## Components

### StateAccessTool

A custom tool that allows agents to retrieve data from session state:

```go
type StateAccessTool struct {
    sessionService session.Service
    appName        string
    userID         string
    sessionID      string
}
```

**Features:**
- Implements the `tool.CallableTool` interface
- Provides JSON schema for parameter validation
- Handles both existing and non-existent keys gracefully
- Returns available keys when requested key is not found

### Research Agent

- **Purpose**: Comprehensive topic analysis and research
- **Output**: Stores findings under the key `research_findings`
- **Capabilities**: Full research capabilities without output schema restrictions

### Writer Agent

- **Purpose**: Content creation and editing
- **Tools**: Uses `get_session_state` tool to access research data
- **Output**: Creates engaging, well-structured content

## Usage

### Building and Running

```bash
# Build the example
go build -o outputkeystate main.go

# Run with default model
./outputkeystate

# Run with specific model
./outputkeystate -model deepseek-chat
```

### Example Queries

Try these example queries to see the pipeline in action:

- "What are the latest developments in quantum computing?"
- "Explain the impact of AI on healthcare in 2024"
- "What are the environmental benefits of electric vehicles?"
- "How does blockchain technology work and what are its applications?"
- "What are the emerging trends in renewable energy?"

### Testing

Run the included tests to verify the StateAccessTool functionality:

```bash
go test -v
```

## Implementation Details

### Session State Management

The example uses the in-memory session service for simplicity. In production, you would use a persistent session service like Redis or a database.

### Tool Integration

The `StateAccessTool` demonstrates proper tool implementation:
- Implements required interfaces (`tool.Tool` and `tool.CallableTool`)
- Provides proper JSON schema for parameter validation
- Handles errors gracefully
- Returns structured responses

### Agent Instructions

Both agents have detailed, realistic instructions that mimic real-world use cases:

- **Research Agent**: Focuses on comprehensive analysis, accuracy, and structured findings
- **Writer Agent**: Emphasizes content creation, audience engagement, and proper tool usage

## Benefits Over Previous Version

1. **Actual State Access**: The writer agent now actually accesses state data using a tool
2. **No Output Schema**: Removed `WithOutputSchema` to allow full agent capabilities
3. **Realistic Instructions**: Updated instructions to reflect real-world content creation workflows
4. **Proper Tool Implementation**: Demonstrates correct tool interface implementation
5. **Better Error Handling**: Tool provides helpful feedback for missing keys
6. **Comprehensive Testing**: Includes unit tests for the state access tool

## Production Considerations

When using this pattern in production:

1. **Persistent Session Storage**: Use a persistent session service for data durability
2. **Security**: Implement proper access controls for session state
3. **Monitoring**: Add logging and metrics for tool usage and state access
4. **Error Handling**: Implement retry logic and fallback mechanisms
5. **Caching**: Consider caching frequently accessed state data

## Related Examples

- [Basic Chain Agent](../chainagent/): Simple agent chaining
- [Tool Integration](../tools/): Various tool usage patterns
- [Session Management](../session/): Session state management examples 