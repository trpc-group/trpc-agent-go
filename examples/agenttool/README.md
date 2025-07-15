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
- **Tool Composition**: Combine regular tools with agent tools
- **Streaming Support**: Full streaming support for agent tool responses
- **Session Management**: Proper session handling for agent tool calls
- **Error Handling**: Graceful error handling and reporting

## Prerequisites

- Go 1.23 or later
- Valid OpenAI API key (or compatible API endpoint)

## Environment Variables

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `deepseek-chat` |

## Usage

### Basic Agent Tool Chat

```bash
cd examples/agenttool
export OPENAI_API_KEY="your-api-key-here"
go run main.go
```

### Custom Model

```bash
export OPENAI_API_KEY="your-api-key"
go run main.go -model gpt-4o
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
- **Usage**: "Calculate 923476 * 273472354" or "Solve this equation: 2x + 5 = 13"
- **Arguments**: request (string) - the mathematical problem or question
- **Internal Tools**: The math specialist agent has access to a calculator tool for basic operations

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
5. **Response Processing**: The agent tool executes the specialized agent and returns the result

## Tool Calling Process

When you ask for mathematical calculations, you'll see:

```
🔧 Tool calls initiated:
   • math-specialist (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1)
     Args: {"request":"Calculate 923476 multiplied by 273472354"}

🔄 Executing tools...
✅ Tool response (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1): {"request":"Calculate 923476 multiplied by 273472354"}
"The result of multiplying \\( 923,\\!476 \\) by \\( 273,\\!472,\\!354 \\) is:\n\n\\[\n923,\\!476 \\times 273,\\!472,\\!354 = 252,\\!545,\\!155,\\!582,\\!504\n\\]"
The result of multiplying 923,476 by 273,472,354 is:

\[
923,\!476 \times 273,\!472,\!354 = 252,\!545,\!155,\!582,\!504
\]

✅ Tool execution completed.
```

## Chat Interface

The interface is simple and intuitive:

```
🚀 Agent Tool Example
Model: gpt-4o-mini
Type 'exit' to end the conversation
==================================================
✅ Chat ready! Session: chat-session-1703123456

👤 You: Hello! Can you help me with math?
🤖 Assistant: Hello! I'd be happy to help you with math. I have access to a specialized math agent that can handle both basic calculations and complex mathematical problems. What would you like to work on?

👤 You: Calculate 923476 * 273472354
🤖 Assistant: I'll use the math specialist agent to calculate this for you.

🔧 Tool calls initiated:
   • math-specialist (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1)
     Args: {"request":"Calculate 923476 multiplied by 273472354"}

🔄 Executing tools...
✅ Tool response (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1): {"request":"Calculate 923476 multiplied by 273472354"}
"The result of multiplying \\( 923,\\!476 \\) by \\( 273,\\!472,\\!354 \\) is:\n\n\\[\n923,\\!476 \\times 273,\\!472,\\!354 = 252,\\!545,\\!155,\\!582,\\!504\n\\]"
The result of multiplying 923,476 by 273,472,354 is:

\[
923,\!476 \times 273,\!472,\!354 = 252,\!545,\!155,\!582,\!504
\]

✅ Tool execution completed.

👤 You: /exit
👋 Goodbye!
```

### Session Commands

- `/history` - Ask the agent to show conversation history
- `/new` - Start a new session (resets conversation context)
- `/exit` - End the conversation

## Agent Tool Implementation

The agent tool implementation is located in `tool/agent/agent_tool.go`:

```go
// Create a specialized agent with its own calculator tool
calculatorTool := function.NewFunctionTool(
    calculate,
    function.WithName("calculator"),
    function.WithDescription("Perform basic mathematical calculations"),
)

mathAgent := llmagent.New(
    "math-specialist",
    llmagent.WithDescription("A specialized agent for mathematical operations"),
    llmagent.WithInstruction("You are a math specialist with access to a calculator tool..."),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
)

// Wrap the agent as a tool
agentTool := agent.NewTool(
    mathAgent,
    agent.WithSkipSummarization(false),
)

// Add to main agent's tools
llmAgent := llmagent.New(
    "chat-assistant",
    llmagent.WithTools([]tool.Tool{timeTool, agentTool}),
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