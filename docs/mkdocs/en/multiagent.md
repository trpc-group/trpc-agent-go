# Multi-Agent System

The Multi-Agent System is one of the core features of the trpc-agent-go framework, allowing you to create complex systems composed of multiple specialized Agents. These Agents can collaborate in different ways to implement various application scenarios from simple to complex.

## Overview

The Multi-Agent System is built on the SubAgent concept, implementing various collaboration patterns through the `WithSubAgents` option:

### Basic Concepts

- **SubAgent** - Specialized Agents configured through the `WithSubAgents` option, serving as the foundation for building complex collaboration patterns

### Core Collaboration Patterns

1. **Chain Agent (ChainAgent)** - Uses SubAgents to execute sequentially, forming processing pipelines
2. **Parallel Agent (ParallelAgent)** - Uses SubAgents to process different aspects of the same input simultaneously
3. **Cycle Agent (CycleAgent)** - Uses SubAgents to iterate in loops until specific conditions are met

### Auxiliary Functions

- **Agent Tool (AgentTool)** - Wraps Agents as tools for other Agents to call
- **Agent Transfer** - Implements task delegation between Agents through the `transfer_to_agent` tool
- **Team** - A high-level wrapper for coordinator teams and swarm-style handoffs (`team` package)

## SubAgent Basics

SubAgent is the core concept of the Multi-Agent System, implemented through the `WithSubAgents` option. It allows you to combine multiple specialized Agents to build complex collaboration patterns.

### Role of SubAgent

- **Specialized Division of Labor**: Each SubAgent focuses on specific domains or task types
- **Modular Design**: Decomposes complex systems into manageable components
- **Flexible Combination**: Can combine different SubAgents as needed
- **Unified Interface**: All collaboration patterns are based on the same `WithSubAgents` mechanism

### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

// Create SubAgent.
mathAgent := llmagent.New(
    "math-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Handles mathematical calculations and numerical problems"),
    llmagent.WithInstruction("You are a mathematics expert, focusing on mathematical operations and numerical reasoning..."),
)

weatherAgent := llmagent.New(
    "weather-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Provides weather information and suggestions"),
    llmagent.WithInstruction("You are a weather expert, providing weather analysis and activity suggestions..."),
)

// Use WithSubAgents option to configure SubAgent.
mainAgent := llmagent.New(
    "coordinator-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Coordinator Agent responsible for task delegation"),
    llmagent.WithInstruction("You are a coordinator, analyzing user requests and delegating to appropriate experts..."),
    llmagent.WithSubAgents([]agent.Agent{mathAgent, weatherAgent}),
)
```

## Core Collaboration Patterns

All collaboration patterns are based on the SubAgent concept, implemented through different execution strategies:

### Chain Agent (ChainAgent)

Chain Agent uses SubAgents connected sequentially to form processing pipelines. Each SubAgent focuses on specific tasks and passes results to the next SubAgent.

#### Use Cases

- **Content Creation Workflow**: Planning → Research → Writing
- **Problem Solving Workflow**: Analysis → Design → Implementation
- **Data Processing Workflow**: Collection → Cleaning → Analysis

#### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

// Create SubAgent.
planningAgent := llmagent.New("planning-agent", ...)
researchAgent := llmagent.New("research-agent", ...)
writingAgent := llmagent.New("writing-agent", ...)

// Create chain Agent, use WithSubAgents to configure SubAgent.
chainAgent := chainagent.New(
    "multi-agent-chain",
    chainagent.WithSubAgents([]agent.Agent{
        planningAgent, 
        researchAgent, 
        writingAgent,
    }),
)
```

#### Example Session

```
🔗 Multi-Agent Chain Demo
Chain Flow: Planning → Research → Writing
==================================================

👤 User: Explain the benefits of renewable energy

📋 Planning Agent: I will create a structured analysis plan...

🔍 Research Agent:
🔧 Using tools:
   • web_search (ID: call_123)
🔄 Executing...
✅ Tool result: Latest renewable energy data...

✍️ Writing Agent: Based on planning and research:
[Structured comprehensive response]
```

### Parallel Agent (ParallelAgent)

Parallel Agent uses SubAgents to process different aspects of the same input simultaneously, providing multi-perspective analysis.

#### Use Cases

- **Business Decision Analysis**: Market analysis, technical assessment, risk evaluation, opportunity analysis
- **Multi-dimensional Evaluation**: Different experts simultaneously evaluating the same problem
- **Fast Parallel Processing**: Scenarios requiring multiple perspectives simultaneously

#### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
)

// Create SubAgent.
marketAgent := llmagent.New("market-analysis", ...)
technicalAgent := llmagent.New("technical-assessment", ...)
riskAgent := llmagent.New("risk-evaluation", ...)
opportunityAgent := llmagent.New("opportunity-analysis", ...)

// Create parallel Agent, use WithSubAgents to configure SubAgent.
parallelAgent := parallelagent.New(
    "parallel-demo",
    parallelagent.WithSubAgents([]agent.Agent{
        marketAgent,
        technicalAgent, 
        riskAgent,
        opportunityAgent,
    }),
)
```

#### Example Session

```
⚡ Parallel Multi-Agent Demo
Agents: Market 📊 | Technical ⚙️ | Risk ⚠️ | Opportunity 🚀
==================================================

💬 User: Should we implement blockchain for supply chain tracking?

🚀 Starting parallel analysis: "Should we implement blockchain for supply chain tracking?"
📊 Agents analyzing different perspectives...
────────────────────────────────────────────────────────────────────────────────

📊 [market-analysis] Starting analysis...
⚙️ [technical-assessment] Starting analysis...
⚠️ [risk-evaluation] Starting analysis...
🚀 [opportunity-analysis] Starting analysis...

📊 [market-analysis]: Blockchain supply chain market is experiencing strong growth with 67% CAGR...

⚙️ [technical-assessment]: Implementation requires distributed ledger infrastructure and consensus mechanisms...

⚠️ [risk-evaluation]: Main risks include 40% target market regulatory uncertainty...

🚀 [opportunity-analysis]: Strategic advantages include enhanced transparency, leading to 15-20% cost reduction...

🎯 All parallel analysis completed successfully!
────────────────────────────────────────────────────────────────────────────────
✅ Multi-perspective analysis completed in 4.1 seconds
```

### Cycle Agent (CycleAgent)

Cycle Agent uses SubAgents to run in iterative loops until specific conditions are met (such as quality thresholds or maximum iterations).

#### Use Cases

- **Content Optimization**: Generate → Evaluate → Improve → Repeat
- **Problem Solving**: Propose → Evaluate → Enhance → Repeat
- **Quality Assurance**: Draft → Review → Revise → Repeat

#### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
)

// Create SubAgent.
generateAgent := llmagent.New("generate-agent", ...)
criticAgent := llmagent.New("critic-agent", ...)

// Create cycle Agent, use WithSubAgents to configure SubAgent.
cycleAgent := cycleagent.New(
    "cycle-demo",
    cycleagent.WithSubAgents([]agent.Agent{
        generateAgent,
        criticAgent,
    }),
    cycleagent.WithMaxIterations(3),
    cycleagent.WithEscalationFunc(qualityEscalationFunc),
)
```

#### Escalation Function (WithEscalationFunc)

In a `CycleAgent`, **escalation** simply means: **stop the loop now**.

A `CycleAgent` runs its SubAgents in order, then repeats the whole sequence.
It stops when one of these happens:

1. Your `EscalationFunc` returns `true` for an event
2. `WithMaxIterations(n)` is reached
3. The `context.Context` is cancelled

##### What does `EscalationFunc` receive?

The callback signature is:

```go
type EscalationFunc func(*event.Event) bool
```

The function is evaluated on events forwarded from sub-agents. To avoid
stopping on half-finished streaming chunks, `CycleAgent` only checks
escalation on "meaningful" events such as:

- error events (`evt.Error != nil`)
- tool response events (`evt.Object == model.ObjectTypeToolResponse`)
- final completion events (`evt.Done == true`, non-streaming)

##### Default behavior

If you do not set `WithEscalationFunc`, `CycleAgent` stops only on errors.

##### Example: quality-based stopping

A common pattern is: **Generate → Critic → stop when "good enough"**.

Have your critic Agent emit a machine-readable signal (for example, a
`record_score` tool that returns JSON with `needs_improvement`). Then stop
the cycle as soon as `needs_improvement` becomes `false` (requires
`encoding/json`):

```go
type scoreResult struct {
	NeedsImprovement bool `json:"needs_improvement"`
}

func qualityEscalationFunc(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	if evt.Error != nil {
		return true
	}
	if evt.Object != model.ObjectTypeToolResponse {
		return false
	}

	for _, choice := range evt.Response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleTool {
			continue
		}

		var res scoreResult
		if err := json.Unmarshal([]byte(msg.Content), &res); err != nil {
			continue
		}
		return !res.NeedsImprovement
	}
	return false
}
```

Keep the function fast and defensive (check `nil`, ignore parse errors),
because it runs inside the event loop.

#### Example Session

```
🔄 Multi-Agent Cycle Demo
Max iterations: 3
Cycle: Generate → Evaluate → Improve → Repeat
==================================================

👤 User: Write a short joke

🤖 Cycle Response:

🤖 Generate Agent: Why don't skeletons fight each other?
Because they don't have the guts!

👀 Evaluate Agent:
🔧 Using tools:
   • record_score (ID: call_123)
🔄 Executing...
✅ Quality score: 75/100
⚠️ Needs improvement - continue iteration

🔄 **2nd Iteration**

🤖 Generate Agent: This is an improved version with a new twist:
**Why do skeletons never win arguments?**
Because they always lose their backbone halfway through!

👀 Evaluate Agent:
🔧 Using tools:
   • record_score (ID: call_456)
🔄 Executing...
✅ Quality score: 85/100
🎉 Quality threshold reached - cycle completed

🏁 Cycle completed after 2 iterations
```

## Auxiliary Functions

### Agent Tool (AgentTool)

Agent Tool is an important foundational function for building complex multi-agent systems. It allows you to wrap any Agent as a callable tool for use by other Agents or applications.

#### Use Cases

- **Specialized Delegation**: Different Agents handle specific types of tasks
- **Tool Integration**: Agents can be integrated as tools into larger systems
- **Modular Design**: Reusable Agent components can be combined together
- **Complex Workflows**: Complex workflows involving multiple specialized Agents

#### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Create specialized Agent.
mathAgent := llmagent.New(
    "math-specialist",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Agent specialized in mathematical operations"),
    llmagent.WithInstruction("You are a mathematics expert, focusing on mathematical operations, calculations and numerical reasoning..."),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
)

// Wrap Agent as tool.
agentTool := agenttool.NewTool(
    mathAgent,
    // The default value for skip summarization=false. 
    // When set to true, the current round will end immediately after tool.response.
    agenttool.WithSkipSummarization(false),
    // Enable inner forwarding: stream child Agent events inline to the parent
    agenttool.WithStreamInner(true),
)

// Use Agent tool in main Agent.
mainAgent := llmagent.New(
    "chat-assistant",
    llmagent.WithTools([]tool.Tool{timeTool, agentTool}),
)
```

#### Agent Tool Architecture

```
Chat Assistant (Main Agent)
├── Time Tool (Function)
└── Math Specialist Agent Tool (Agent)
    └── Math Specialist Agent (Specialized Agent)
        └── Calculator Tool (Function)
```

#### Example Session

```
🚀 Agent Tool Example
Model: deepseek-chat
Available tools: current_time, math-specialist
==================================================

👤 User: Calculate 923476 * 273472354

🤖 Assistant: I will use the math specialist Agent to calculate this result.

🔧 Tool call initiated:
   • math-specialist (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1)
     Parameters: {"request":"Calculate 923476 multiplied by 273472354"}

🔄 Executing tool...
✅ Tool response (ID: call_0_e53a77e9-c994-4421-bfc3-f63fe85678a1):
"The result of calculating 923,476 multiplied by 273,472,354 is:

\[
923,\!476 \times 273,\!472,\!354 = 252,\!545,\!155,\!582,\!504
\]"

✅ Tool execution completed.
```

#### Streaming Inner Forwarding (StreamInner)

When `WithStreamInner(true)` is enabled for the Agent tool:

- Child Agent events are forwarded as streaming `event.Event` items; you can directly display `choice.Delta.Content`
- To avoid duplicates, the child Agent’s final full text is not forwarded again; it is aggregated into the final `tool.response` that follows tool_calls (satisfying provider requirements)
- To keep inner progress but hide child assistant prose, add
  `WithInnerTextMode(agenttool.InnerTextModeExclude)`
- UI recommendations:
  - Show forwarded child deltas as they stream
  - By default, don’t reprint the final aggregated tool response text unless debugging

Example: Distinguish outer assistant, child Agent (forwarded), and tool responses in your event loop

```go
// Child Agent forwarded delta (author != parent)
if ev.Author != parentName && ev.Response != nil && len(ev.Response.Choices) > 0 {
    if delta := ev.Response.Choices[0].Delta.Content; delta != "" {
        fmt.Print(delta)
    }
    return
}

// Tool response (aggregated content), skip by default to avoid duplicates
if ev.Response != nil && ev.Object == model.ObjectTypeToolResponse {
    // ...show on demand or skip
    return
}
```

#### Option Matrix

- `WithSkipSummarization(false)`: (default) Allow one more summarization LLM call after the tool
- `WithSkipSummarization(true)`: Skip the outer summarization so the tool output is surfaced directly
- `WithStreamInner(true)`: Forward child Agent events (use `Stream: true` on both parent and child Agents)
- `WithStreamInner(false)`: Treat as a callable-only tool, without inner forwarding
- `WithInnerTextMode(agenttool.InnerTextModeInclude)`: show child
  assistant text when inner streaming is enabled
- `WithInnerTextMode(agenttool.InnerTextModeExclude)`: keep inner
  progress events, but suppress forwarded child assistant text

### Agent Transfer

Agent Transfer implements task delegation between Agents through the `transfer_to_agent` tool, allowing the main Agent to automatically select appropriate SubAgents based on task type.

#### Use Cases

- **Task Classification**: Automatically select appropriate SubAgents based on user requests
- **Intelligent Routing**: Route complex tasks to the most suitable handlers
- **Specialized Processing**: Each SubAgent focuses on specific domains
- **Seamless Switching**: Seamlessly switch between SubAgents while maintaining conversation continuity

#### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Create SubAgent.
mathAgent := llmagent.New(
    "math-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Handles mathematical calculations and numerical problems"),
    llmagent.WithInstruction("You are a mathematics expert, focusing on mathematical operations and numerical reasoning..."),
    llmagent.WithTools([]tool.Tool{calculatorTool}),
)

weatherAgent := llmagent.New(
    "weather-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Provides weather information and suggestions"),
    llmagent.WithInstruction("You are a weather expert, providing weather analysis and activity suggestions..."),
    llmagent.WithTools([]tool.Tool{weatherTool}),
)

// Create coordinator Agent, use WithSubAgents to configure SubAgent.
coordinatorAgent := llmagent.New(
    "coordinator-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Coordinator Agent responsible for task delegation"),
    llmagent.WithInstruction("You are a coordinator, analyzing user requests and delegating to appropriate experts..."),
    llmagent.WithSubAgents([]agent.Agent{mathAgent, weatherAgent}),
)
```

#### Dynamic SubAgent Discovery (with A2A)

In real systems, SubAgents are often remote Agents exposed through
the A2A protocol. Their list may change over time (for example when
new services are registered in a central registry).

To support this, `LLMAgent` implements the `agent.SubAgentSetter`
interface. You can refresh its SubAgents at runtime without recreating
the coordinator:

```go
import (
    "fmt"
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
)

func refreshSubAgents(ctx context.Context, ag agent.Agent) error {
    cfg, ok := ag.(agent.SubAgentSetter)
    if !ok {
        return fmt.Errorf("agent does not support dynamic SubAgents")
    }

    // 1. Discover remote Agents from your registry or config source.
    urls := []string{
        "http://localhost:8087/",
        "http://localhost:8088/",
    }

    // 2. Build A2AAgent proxies for each remote Agent.
    subAgents := make([]agent.Agent, 0, len(urls))
    for _, url := range urls {
        a2, err := a2aagent.New(a2aagent.WithAgentCardURL(url))
        if err != nil {
            // In production you may want to log and skip failures.
            continue
        }
        subAgents = append(subAgents, a2)
    }

    // 3. Atomically replace SubAgents on the coordinator.
    cfg.SetSubAgents(subAgents)
    return nil
}
```

This pattern lets you:

- Integrate with any registry (service discovery, database, config file)
- Dynamically add or remove remote SubAgents
- Keep `Runner` and session logic unchanged, since the coordinator
  remains the same Agent instance

#### Agent Transfer Architecture

```
Coordinator Agent (Main Entry)
├── Analyze user requests
├── Select appropriate SubAgent
└── Use transfer_to_agent tool to delegate tasks
    ├── Math SubAgent (Mathematical calculations)
    ├── Weather SubAgent (Weather information)
    └── Research SubAgent (Information search)
```

#### Example Session

```
🔄 Agent Transfer Demo
Available SubAgents: math-agent, weather-agent, research-agent
==================================================

👤 User: Calculate compound interest, principal $5000, annual rate 6%, term 8 years

🎯 Coordinator: I will delegate this task to our mathematics expert for accurate calculation.
🔄 Initiating delegation...
🔄 Transfer event: Transferring control to Agent: math-agent

🧮 Math Expert: I will help you calculate compound interest step by step.
🔧 🧮 Executing tool:
   • calculate ({"operation":"power","a":1.06,"b":8})
   ✅ Tool completed
🔧 🧮 Executing tool:
   • calculate ({"operation":"multiply","a":5000,"b":1.593})
   ✅ Tool completed

Compound Interest Calculation Result:
- Principal: $5,000
- Annual Rate: 6%
- Term: 8 years
- Result: $7,969.24 (interest approximately $2,969.24)
```

## Environment Variable Configuration

All multi-agent examples require the following environment variables:

| Variable Name | Required | Default Value | Description |
|---------------|----------|---------------|-------------|
| `OPENAI_API_KEY` | Yes | - | OpenAI API key |
| `OPENAI_BASE_URL` | No | `https://api.openai.com/v1` | OpenAI API base URL |

## Running Examples

All example code is located at [examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples)

### Core Collaboration Pattern Examples

#### Chain Agent Example

```bash
cd examples/multiagent/chain
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### Parallel Agent Example

```bash
cd examples/multiagent/parallel
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### Cycle Agent Example

```bash
cd examples/multiagent/cycle
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat -max-iterations 5
```

### Auxiliary Function Examples

#### Agent Tool Example

```bash
cd examples/agenttool
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### Agent Transfer Example

```bash
cd examples/transfer
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

## Customization and Extension

### Adding New Agents

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// Create custom Agent.
customAgent := llmagent.New(
    "custom-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Custom Agent description"),
    llmagent.WithInstruction("Custom instruction"),
    llmagent.WithTools([]tool.Tool{customTool}),
)

// Integrate into multi-agent system.
chainAgent := chainagent.New(
    "custom-chain",
    chainagent.WithSubAgents([]agent.Agent{
        existingAgent,
        customAgent,  // Add custom Agent.
    }),
)
```

### Configuring Tools

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Create custom tool.
customTool := function.NewFunctionTool(
    customFunction,
    function.WithName("custom_tool"),
    function.WithDescription("Custom tool description"),
)

// Assign tools to Agent.
agent := llmagent.New(
    "tool-agent",
    llmagent.WithTools([]tool.Tool{customTool}),
)
```

### Adjusting Parameters

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// Configure generation parameters.
genConfig := model.GenerationConfig{
    MaxTokens:   intPtr(500),
    Temperature: floatPtr(0.7),
    Stream:      true,
}

// Apply to Agent.
agent := llmagent.New(
    "configured-agent",
    llmagent.WithGenerationConfig(genConfig),
)
```
