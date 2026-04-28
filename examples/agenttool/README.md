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

| Argument         | Description                                           | Default Value   |
| ---------------- | ----------------------------------------------------- | --------------- |
| `-model`         | Name of the model to use                              | `deepseek-v4-flash` |
| `-show-inner`    | Show inner agent deltas streamed by AgentTool         | `true`          |
| `-inner-text`    | Inner text mode: `include` or `exclude`               | `include`       |
| `-response-mode` | Tool result mode: `default` or `final-only`           | `default`       |
| `-show-tool`     | Show tool.response deltas/finals in transcript        | `false`         |
| `-debug`         | Prefix streamed lines with author for debugging       | `false`         |

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

## How to Read the Output

This example exposes three visibility controls:

- `-show-inner`: whether AgentTool forwards inner events at all
- `-inner-text`: whether forwarded inner assistant text is visible
- `-show-tool`: whether the example prints the aggregated final
  `tool.response`
- `-response-mode`: which child assistant messages become the tool result
  consumed by the parent agent

The flags map directly to the AgentTool configuration:

```go
agentTool := agenttool.NewTool(
    mathAgent,
    agenttool.WithSkipSummarization(true),
    agenttool.WithStreamInner(showInner),
    agenttool.WithInnerTextMode(innerTextMode),
    agenttool.WithResponseMode(responseMode),
)
```

### Full Inner Transcript

```bash
go run . -show-inner=true -inner-text=include
```

Use this when you want to watch the child agent's natural-language output as it
streams.

You will see:

- tool call markers such as `math-specialist` and `calculator`
- streamed child text from the math specialist
- a completion marker when the tool finishes

### Progress Only, No Child Prose

```bash
go run . -show-inner=true -inner-text=exclude -show-tool=true
```

Use this when you want users to see that work is happening, but you do not want
to render the child agent's prose token by token.

You will see:

- tool call markers
- tool execution progress
- the final aggregated `tool.response`
- no streamed child assistant prose

This is the clearest way to learn `InnerTextModeExclude`, because the result
stays visible through `tool.response` while the child transcript remains hidden.

### Callable-Only Tool View

```bash
go run . -show-inner=false -show-tool=true
```

Use this when you want the child agent to behave like a regular tool from the
user's point of view.

You will see:

- the outer assistant's own text
- tool response output if `-show-tool=true`
- no forwarded inner events

### Final-Only Tool Result

```bash
go run . -response-mode=final-only -show-tool=true
```

Use this when the child agent is doing its own multi-step work and the parent
agent should only receive the child agent's final answer as the tool result.

The default response mode preserves compatibility by concatenating child
assistant messages into the tool result. That means progress-like assistant
messages, drafts, and the final assistant message may all appear in the final
`tool.response`. `final-only` changes only the tool result: it ignores partial
assistant chunks and returns the last complete child assistant message.

This is separate from `-inner-text`. For example:

```bash
go run . -show-inner=true -inner-text=exclude \
  -response-mode=final-only -show-tool=true
```

This combination keeps child tool progress visible, hides streamed child prose,
and gives the parent agent only the child agent's final answer.

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
2. **Tool Wrapping**: The agent is wrapped as a tool using
   `agenttool.NewTool()`
3. **Tool Integration**: The agent tool is added to the main agent's tool list
4. **Delegation**: When the main agent encounters tasks that match the specialized agent's expertise, it delegates to the agent tool
5. **Response Processing**: The agent tool executes the specialized agent and returns the result. When the agent tool streams, you will receive `tool.response` events with partial chunks.

By default, AgentTool builds its callable result by aggregating child assistant
messages. This preserves older behavior. When you configure
`agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly)`, AgentTool still
runs the same child agent, but only the last complete child assistant message is
returned as the tool result.

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
Show inner: true
Inner text mode: include
Show tool: false
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

The important parts all live in `main.go`:

### main.go

Contains the main chat logic, visibility flags, and tool setup:

```go
innerTextMode, err := parseInnerTextMode(*innerTextMode)
if err != nil {
    log.Fatalf("invalid -inner-text: %v", err)
}

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
agentTool := agenttool.NewTool(
    mathAgent,
    agenttool.WithSkipSummarization(true),
    agenttool.WithStreamInner(*showInner),
    agenttool.WithInnerTextMode(innerTextMode),
    agenttool.WithResponseMode(responseMode),
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

- This example opts into `agenttool.WithSkipSummarization(true)`, so the child
  result is surfaced directly instead of asking the outer agent for one more
  summarization turn.
- Inner forwarding is enabled by default in the example
  (`-show-inner=true`), which is why you can immediately see child progress.
- `-inner-text=include` shows the child agent's prose as it streams.
- `-inner-text=exclude` hides child prose but still lets you keep tool
  progress and the aggregated final `tool.response`.
- `-response-mode=default` preserves compatibility by concatenating child
  assistant messages into the tool result.
- `-response-mode=final-only` returns only the child agent's last complete
  assistant message as the tool result.
- The framework always emits a final non-partial `tool.response` for session
  history and provider compliance. In `default` mode, that tool response uses
  merged child content. In `final-only` mode, it uses only the child agent's
  last complete assistant message. The example hides it unless `-show-tool` is
  set.

Examples:

```bash
# Default example mode: show inner child prose
go run . -model gpt-4o-mini

# Show progress only, then render the aggregated tool response
go run . -show-inner -inner-text=exclude -show-tool

# Return only the child agent's final answer as the tool result
go run . -response-mode=final-only -show-tool

# Hide all inner events and only print tool outputs
go run . -show-inner=false -show-tool
```

Notes:

- Even when inner deltas are streamed, the framework does not forward the child
  agent's final full message again. The selected response mode controls what is
  written into the final `tool.response`.
- If you choose `-inner-text=exclude` and also keep `-show-tool=false`, you
  will only see progress markers. That is expected: the child prose is hidden,
  and the example is also choosing not to print the final tool message.
