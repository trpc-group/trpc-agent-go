# Agent Tool Example

This example demonstrates how to use agent tools to wrap agents as tools within a larger application. Agent tools allow you to treat agents as callable tools, enabling complex multi-agent workflows and specialized agent delegation.

## What are Agent Tools?

Agent tools provide a way to wrap any agent as a tool that can be called by other agents or applications. This enables:

- **🔧 Tool Integration**: Agents can be used as tools within larger systems
- **🎯 Specialized Delegation**: Different agents can handle specific types of tasks
- **🔄 Multi-Agent Workflows**: Complex workflows involving multiple specialized agents
- **📦 Modular Design**: Reusable agent components that can be composed together

### Key Features

- **Agent Wrapping**: Wrap any agent as a callable tool
- **Specialized Agents**: Create agents with specific expertise (e.g., math specialist)
- **Input Schema**: Specify the input schema of the agent tool.
- **Tool Composition**: Combine regular tools with agent tools
- **Streaming Support**: Full streaming support for agent tool responses
- **Session Management**: Proper session handling for agent tool calls
- **Error Handling**: Graceful error handling and reporting

## Prerequisites

- Go 1.21 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable          | Description                              | Default Value               |
| ----------------- | ---------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument      | Description                                     | Default Value   |
| ------------- | ----------------------------------------------- | --------------- |
| `-model`      | Name of the model to use                        | `deepseek-chat` |
| `-show-inner` | Show inner agent deltas streamed by AgentTool   | `false`         |
| `-show-tool`  | Show tool.response deltas/finals in transcript  | `false`         |
| `-debug`      | Prefix streamed lines with author for debugging | `false`         |

## Usage

### Basic Agent Tool Chat

```bash
cd examples/agenttool
export OPENAI_API_KEY="your-api-key-here"
go run .
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run . -model gpt-4o
```

## Implemented Tools

The example includes two types of tools:

### 🕐 Time Tool

- **Function**: `current_time`
- **Timezones**: UTC, EST, PST, CST, or local time
- **Usage**: "What time is it in EST?" or "Current time please"
- **Arguments**: timezone (optional string)

### 🤖 Math Specialist Agent Tool

- **Function**: `math-specialist`
- **Purpose**: Handles complex mathematical operations and reasoning with its own calculator tool
- **Usage**: "Calculate 923476 \* 273472354" or "Solve this equation: 2x + 5 = 13"
- **Arguments**: request (string) - the mathematical problem or question
- **Internal Tools**: The math specialist agent has access to a calculator tool for basic operations
- **Input Schema**: JSON schema with required "request" field for mathematical problems

## Agent Tool Architecture

The example demonstrates a hierarchical agent structure:

```
Chat Assistant (Main Agent)
├── Time Tool (Function)
└── Math Specialist Agent Tool (Agent)
    └── Math Specialist Agent (Specialized Agent)
        └── Calculator Tool (Function)
```

### How Agent Tools Work

1. **Agent Creation**: A specialized agent (e.g., math specialist) is created with specific instructions and capabilities
2. **Tool Wrapping**: The agent is wrapped as a tool using `agent.NewAgentTool()`
3. **Tool Integration**: The agent tool is added to the main agent's tool list
4. **Delegation**: When the main agent encounters tasks that match the specialized agent's expertise, it delegates to the agent tool
5. **Response Processing**: The agent tool executes the specialized agent and returns the result. When the agent tool streams, you will receive `tool.response` events with partial chunks.

## Tool Calling Process

When you ask for mathematical calculations, you'll see callable tool calls and streamed agent-tool outputs:

```
🔧 Tool calls initiated:
   • math-specialist (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1)
     Args: {"request":"Calculate 923476 multiplied by 273472354"}

🔄 Executing tools...
… (streaming tool.response chunks)
The result of multiplying 923,476 by 273,472,354 is:

✅ Tool execution completed.
```

## Chat Interface

The interface is simple and intuitive:

```
🚀 Agent Tool Example
Model: gpt-4o-mini
Available tools: current_time, math-specialist(agent_tool)
==================================================
✅ Chat ready! Session: chat-session-1703123456

💡 Special commands:
   /history  - Show conversation history
   /new      - Start a new session
   /exit     - End the conversation

👤 You: Hello! Can you help me with math?
🤖 Assistant: Of course! What math problem do you need help with?

👤 You: Calculate 923476 * 273472354
🤖 Assistant: 🔧 Tool calls initiated:
   • math-specialist (ID: call_k7LFMLReoHMT7Con94FEWolz)
     Args: {"request":"923476 * 273472354"}

🔄 Executing tools...
🔧 Tool calls initiated:
   • calculator (ID: call_7e7mqv5VDpOLHvLoXpGurzZE)
     Args: {"a":923476,"b":273472354,"operation":"multiply"}

🔄 Executing tools...
I calculated the product of 923,476 and 273,472,354. The result of this multiplication is 252,545,155,582,504.

✅ Tool response (ID: call_k7LFMLReoHMT7Con94FEWolz): "I calculated the product of 923,476 and 273,472,354. The result of this multiplication is 252,545,155,582,504."

✅ Tool execution completed.

👤 You: /exit
👋 Goodbye!
```

### Session Commands

- `/history` - Ask the agent to show conversation history
- `/new` - Start a new session (resets conversation context)
- `/exit` - End the conversation

## Agent Tool Implementation

The agent tool implementation follows the runner/ example structure with separate files:

### main.go

Contains the main chat logic and agent setup.

### tools.go

Contains all tool definitions and implementations with proper JSON schema:

```go
// Tool arguments with JSON schema
type calculatorArgs struct {
    Operation string  `json:"operation" jsonschema:"description=The operation: add, subtract, multiply, divide,enum=add,enum=subtract,enum=multiply,enum=divide"`
    A         float64 `json:"a" jsonschema:"description=First number"`
    B         float64 `json:"b" jsonschema:"description=Second number"`
}

// Math agent with input schema
mathAgent := llmagent.New(
    "math-specialist",
    llmagent.WithDescription("A specialized agent for mathematical operations"),
    llmagent.WithInstruction("You are a math specialist with access to a calculator tool..."),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
    llmagent.WithInputSchema(map[string]any{
        "type": "object",
        "properties": map[string]any{
            "request": map[string]any{
                "type":        "string",
                "description": "The mathematical problem or question to solve",
            },
        },
        "required": []any{"request"},
    }),
)

// Wrap the agent as a tool
agentTool := agent.NewTool(
    mathAgent,
    agent.WithSkipSummarization(true), // opt-in: skip parent summarization after the tool response
    agent.WithStreamInner(true),
)
```

## Benefits of Agent Tools

1. **Modularity**: Each agent can focus on specific domains
2. **Reusability**: Agent tools can be used across different applications
3. **Scalability**: Easy to add new specialized agents
4. **Composability**: Combine multiple agent tools for complex workflows
5. **Specialization**: Each agent can be optimized for specific tasks

## Use Cases

- **Domain Experts**: Create agents specialized in specific fields (math, coding, writing)
- **Multi-Step Workflows**: Chain multiple agents for complex processes
- **Quality Assurance**: Use specialized agents for validation and review
- **Content Generation**: Delegate different types of content to specialized agents
- **Problem Solving**: Break complex problems into specialized sub-tasks

### Streaming Tool Responses in the App

When the main agent invokes the agent tool, the framework emits `tool.response` events for the tool output. In streaming mode, each chunk appears in `choice.Delta.Content` with `Object: tool.response`.

Example handling logic in your event loop:

```go
if evt.Response != nil && evt.Object == model.ObjectTypeToolResponse && len(evt.Response.Choices) > 0 {
    for _, ch := range evt.Response.Choices {
        if ch.Delta.Content != "" { // partial chunk
            fmt.Print(ch.Delta.Content)
            continue
        }
        if ch.Message.Role == model.RoleTool && ch.Message.Content != "" { // final tool message
            fmt.Println(strings.TrimSpace(ch.Message.Content))
        }
    }
    continue // don't treat as assistant content
}
```

This lets the agent tool stream results progressively while keeping the main conversation flow responsive.

### AgentTool Defaults and Flags

- Default behavior: AgentTool lets the outer agent run its follow-up turn after `tool.response`, allowing it to summarize or combine results.
- Streaming inner transcript: By default, inner agent deltas are not forwarded. Pass `-show-inner` to see the inner agent’s streamed deltas in the parent transcript. Under the hood this enables `agenttool.WithStreamInner(true)`.
- Tool output printing: The framework always emits a final non-partial `tool.response` with merged content for session history and provider compliance. To avoid printing the merged content again when you already saw deltas, the example hides it unless `-show-tool` is set.

Examples:

```bash
# Clean UX, no inner streaming; outer agent summarizes after tool.response (default)
go run . -model gpt-4o-mini

# Stream inner agent deltas and show tool messages
go run . -show-inner -show-tool

# Stream inner agent deltas but keep tool messages hidden (marker only)
go run . -show-inner
```

Notes:

- Even when inner deltas are streamed, the example suppresses the inner agent’s final full content to avoid duplication. The final `tool.response` persists the merged content for history, but the UI prints only a completion marker unless `-show-tool` is used.
- The default configuration keeps the outer-agent summary after the tool finishes; pass `agenttool.WithSkipSummarization(true)` if you want the tool result to be surfaced directly instead.
