# tRPC-Agent-Go Framework Introduction

## Introduction
The tRPC-Go team previously launched the [MCP development framework](https://github.com/trpc-group/trpc-mcp-go) and [A2A development framework](https://github.com/trpc-group/trpc-a2a-go), which have been widely applied both internally and externally. Now we are launching the [tRPC-Agent-Go framework](https://github.com/trpc-group/trpc-agent-go) to further complete the tRPC AI development framework ecosystem.

## Background and Technical Choices

### Development Background

With the rapid improvement of LLM capabilities, `Agent` development frameworks have become important infrastructure for connecting AI capabilities with business applications. Current frameworks have diverged in their technical approaches, and there is significant room for development in the Go language ecosystem.

#### Industry Framework Technical Route Analysis

Current AI `Agent` application development frameworks are mainly divided into two technical routes: **Autonomous Multi-Agent Frameworks** and **Orchestration Frameworks**.

**Autonomous Multi-Agent Frameworks**

Autonomous multi-agent frameworks embody the true concept of `Agent` (Autonomous Agent), where each `Agent` has environmental perception, autonomous decision-making, and action execution capabilities. Multiple agents collaborate through message passing and negotiation mechanisms, achieving distributed collaboration and dynamically adjusting strategies based on environmental changes, demonstrating emergent intelligent characteristics.

- **AutoGen (Microsoft)**: Multi-agent collaboration system supporting agent role specialization and dynamic negotiation
- **ADK (Google Agent Development Kit)**: Provides complete agent lifecycle management and multi-agent orchestration capabilities
- **CrewAI**: Task-oriented multi-agent collaboration platform emphasizing role definition and responsibility chain patterns
- **Agno**: Lightweight high-performance agent framework focusing on multimodal capabilities and team collaboration

**Orchestration Frameworks**

Orchestration frameworks adopt workflow thinking, organizing LLM calls and component interactions through predefined flowcharts or state machines. While the entire system exhibits "intelligent" characteristics, its execution path is deterministic, more like "intelligent workflows" rather than truly autonomous agents.

- **LangChain**: Component orchestration framework based on Chain abstraction, building LLM applications through predefined execution paths
- **LangGraph**: Directed acyclic graph (DAG) state machine framework providing deterministic state transitions and conditional branching
- **Eino (ByteDance)**: LLM application orchestration framework managing processes based on Pipeline and Graph patterns

#### Technical Comparison of Two Framework Types

| Comparison Dimension | Autonomous Multi-Agent Frameworks | Orchestration Frameworks |
|---------------------|----------------------------------|--------------------------|
| **Control Mode** | Distributed autonomous decision-making, inter-agent negotiation | Centralized process orchestration, deterministic execution |
| **Applicable Scenarios** | Open-domain problem solving, creative tasks, multi-specialty collaboration | Structured business processes, data processing pipelines, standardized operations |
| **Extension Method** | Horizontal extension of agent roles, vertical enhancement of agent capabilities | Node extension and flowchart complexity |
| **Execution Predictability** | Emergent behavior, high result diversity | Deterministic execution, reproducible results |
| **System Complexity** | Complex agent interactions, difficult debugging | Clear processes, easy debugging and monitoring |
| **Technical Implementation** | Based on message passing and conversation protocols | Based on state machines and directed graph execution |

#### Technical Characteristics of Autonomous Multi-Agent Frameworks

Modern LLMs have significantly improved capabilities in complex reasoning and dynamic decision-making. Autonomous multi-agent frameworks compared to orchestration frameworks have the following characteristics:

- **Adaptability**: Agents dynamically adjust decision strategies and execution paths based on context
- **Collaborative Emergence**: Multiple agents achieve decentralized negotiation and task decomposition through message passing
- **Cognitive Integration**: Deep integration of LLM's reasoning, planning, and reflection capabilities to form intelligent decision-making chains

#### tRPC-Agent-Go Technical Positioning

**Industry and Ecosystem Status**: With the continuous breakthrough of LLM capabilities, `Agent` development frameworks are becoming an important trend in AI application development. Current mainstream autonomous multi-agent frameworks (such as AutoGen, CrewAI, ADK, Agno, etc.) are mainly built on the Python ecosystem, providing rich choices for Python developers. However, Go language, with its excellent concurrent performance, memory safety, and deployment convenience, occupies an important position in microservice architectures. Currently, the more mature Go language AI development framework Eino (CloudWeGo) focuses on orchestration architecture, mainly applicable to structured business processes, while autonomous multi-agent frameworks are relatively scarce in the Go ecosystem, presenting development opportunities.

Based on this current situation, tRPC-Agent-Go is positioned to provide autonomous multi-agent framework development capabilities for the Go ecosystem:

- **Architecture Features**: Adopts autonomous multi-agent architecture patterns, fully leveraging Go language's concurrency and high-performance advantages
- **Ecosystem Integration**: Deep integration with tRPC microservice ecosystem, reusing service governance, observability, and other infrastructure
- **Application Adaptation**: Meeting intelligent transformation and deployment requirements for complex business scenarios

## tRPC-Agent-Go Framework Overview

The [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) framework integrates LLM, intelligent planners, session management, observability, and a rich tool ecosystem. It supports creating autonomous agents and semi-autonomous agents with reasoning capabilities, tool calling, sub-agent collaboration, and long-term state persistence, providing developers with a complete technology stack for building intelligent applications.

### Core Technical Features

- **Diverse Agent System**: Provides multiple agent execution modes including LLM, Chain, Parallel, Cycle, and more
- **Rich Tool Ecosystem**: Built-in common tool sets, supporting custom extensions and MCP protocol standardized integration
- **Monitoring Capabilities**: Integrated OpenTelemetry standards, supporting full-link tracing and performance monitoring
- **Intelligent Session Management**: Supports session state persistence, memory management, and knowledge base integration
- **Modular Architecture**: Clear layered design, facilitating extension and custom development

## Core Module Details

### Model Module - Large Language Model Abstraction Layer

The Model module provides unified LLM interface abstraction, supporting OpenAI-compatible API calls. Through standardized interface design, developers can flexibly switch between different model providers, achieving seamless model integration and calling. This module mainly supports OpenAI-like interface compatibility and has been verified with most interfaces both internally and externally.

#### Core Interface Design

```go
// Model is the interface that all language models must implement.
type Model interface {
    // Generate content, supporting streaming responses.
    GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error)
    
    // Return basic model information.
    Info() Info
}

// Model information structure.
type Info struct {
    Name string // Model name.
}
```

#### OpenAI-Compatible Implementation

The framework provides complete OpenAI-compatible implementation, supporting connections to various OpenAI-like interfaces:

```go
// Create OpenAI model.
model := openai.New("gpt-4o-mini",
    openai.WithAPIKey("your-api-key"),
    openai.WithBaseURL("https://api.openai.com/v1"), // Customizable BaseURL.
)

// Support custom configuration.
model := openai.New("custom-model",
    openai.WithAPIKey("your-api-key"),
    openai.WithBaseURL("https://your-custom-endpoint.com/v1"),
    openai.WithChannelBufferSize(512),
    openai.WithExtraFields(map[string]interface{}{
        "custom_param": "value",
    }),
)
```

#### Supported Model Platforms

The current framework supports all model platforms that provide OpenAI-compatible APIs, including but not limited to:

- **OpenAI** - GPT-4o, GPT-4, GPT-3.5 series models
- **Tencent Cloud** - Deeseek, hunyuan series
- **Other Cloud Providers** - Various models providing OpenAI-compatible interfaces, such as deepseek, qwen, etc.

For detailed information about the Model module, please refer to [Model](./model.md)

### Agent Module - Agent Execution Engine

The Agent module is the core component of tRPC-Agent-Go, providing intelligent reasoning engines and task orchestration capabilities. This module has the following core functions:

- **Diverse Agent Types**: Supports different execution modes including LLM, Chain, Parallel, Cycle, Graph, and more
- **Tool Calling and Integration**: Provides rich external capability extension mechanisms
- **Event-Driven Architecture**: Implements streaming processing and real-time monitoring
- **Hierarchical Composition**: Supports sub-agent collaboration and complex process orchestration
- **State Management**: Ensures long conversations and session persistence

The Agent module achieves high modularity through unified interface standards, providing developers with complete technical support from intelligent conversation assistants to complex task automation.

#### Core Interface Design

```go
type Agent interface {
    // Execute agent call, return event stream.
    Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)
    
    // Return list of tools available to the agent.
    Tools() []tool.Tool
    
    // Return basic agent information.
    Info() Info
    
    // Return sub-agent list, supporting hierarchical composition.
    SubAgents() []Agent
    
    // Find sub-agent by name.
    FindSubAgent(name string) Agent
}
```

#### Multiple Agent Types

**LLMAgent - Basic Intelligent Agent**

**Core Features**: LLM-based intelligent agent supporting tool calling, streaming output, and session management.

- **Execution Method**: Direct interaction with LLM, supporting single-round conversations and multi-round sessions
- **Applicable Scenarios**: Intelligent customer service, content creation, code assistance, data analysis, Q&A systems
- **Advantages**: Simple and direct, fast response, flexible configuration, easy to extend

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
    llmagent.WithInstruction("You are a professional AI assistant"),
    llmagent.WithTools([]tool.Tool{calculatorTool, searchTool}),
)
```

**ChainAgent - Chain Processing Agent**

**Core Features**: Pipeline mode, multiple agents execute sequentially, with the output of the previous one becoming the input of the next.

- **Execution Method**: Agent1 → Agent2 → Agent3 sequential execution
- **Applicable Scenarios**: Document processing pipelines, data ETL, content review chains
- **Technical Advantages**: Professional division of labor, clear processes, easy debugging

```go
chain := chainagent.New(
    "content-pipeline",
    chainagent.WithSubAgents([]agent.Agent{
        planningAgent,   // Step 1: Make plans.
        researchAgent,   // Step 2: Collect information.
        writingAgent,    // Step 3: Create content.
    }),
)
```

**ParallelAgent - Parallel Processing Agent**

**Core Features**: Concurrent mode, multiple agents execute the same task simultaneously, then merge results.

- **Execution Method**: Agent1 + Agent2 + Agent3 simultaneous execution
- **Applicable Scenarios**: Multi-expert evaluation, multi-dimensional analysis, decision support
- **Technical Advantages**: Concurrent execution, multi-angle analysis, strong fault tolerance

```go
parallel := parallelagent.New(
    "multi-expert-evaluation",
    parallelagent.WithSubAgents([]agent.Agent{
        marketAgent,      // Market analysis expert.
        technicalAgent,   // Technical evaluation expert.
        financeAgent,     // Financial analysis expert.
    }),
)
```

**CycleAgent - Iterative Agent**

**Core Features**: Iterative mode, through multiple rounds of "execute → evaluate → improve" cycles, continuously optimizing results.

- **Execution Method**: Loop execution until conditions are met or maximum rounds are reached
- **Applicable Scenarios**: Complex problem solving, content optimization, automatic debugging
- **Technical Advantages**: Self-improvement, quality enhancement, intelligent stopping

```go
cycle := cycleagent.New(
    "problem-solver",
    cycleagent.WithSubAgents([]agent.Agent{
        generatorAgent,  // Generate solutions.
        reviewerAgent,   // Evaluate quality.
    }),
    // Set maximum iterations to 5 to prevent infinite loops.
    cycleagent.WithMaxIterations(5),
)
```

**GraphAgent - Graph Workflow Agent**

**Core Features**: Graph-based workflow mode, supporting conditional routing and multi-node collaboration for complex task processing.

**Design Purpose**: To meet and be compatible with most AI Agent applications developed based on graph orchestration frameworks within Tencent, facilitating migration of existing users and preserving existing development habits.

- **Execution Method**: Execute according to graph structure, supporting LLM nodes, tool nodes, conditional branches, and state management
- **Applicable Scenarios**: Complex decision processes, multi-step task collaboration, dynamic routing processing, existing graph orchestration application migration
- **Technical Advantages**: Flexible routing, state sharing, visual processes, compatible with existing development patterns

```go
// Create document processing workflow.
stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())

// Create analysis tool.
complexityTool := function.NewFunctionTool(
    analyzeComplexity,
    function.WithName("analyze_complexity"),
    function.WithDescription("Analyze document complexity"),
)
tools := map[string]tool.Tool{"analyze_complexity": complexityTool}

// Build workflow graph.
g, err := stateGraph.
    AddNode("preprocess", preprocessDocument). // Preprocessing node.
    AddLLMNode("analyze", model,
        "Analyze document complexity using analyze_complexity tool",
                                        tools). // LLM analysis node.
    AddToolsNode("tools", tools).                                         // Tool node.
    AddNode("route_complexity", routeComplexity).                         // Routing decision node.
    AddLLMNode("summarize", model, "Summarize complex documents", nil).   // LLM summary node.
    AddLLMNode("enhance", model, "Enhance simple document quality", nil). // LLM enhancement node.
    AddNode("format_output", formatOutput).                               // Formatting node.
    SetEntryPoint("preprocess").                                          // Set entry point.
    SetFinishPoint("format_output").                                      // Set exit point.
    AddEdge("preprocess", "analyze").                                     // Connect nodes.
    AddToolsConditionalEdges("analyze", "tools", "route_complexity").
    AddConditionalEdges("route_complexity", complexityCondition, map[string]string{
        "simple":  "enhance",
        "complex": "summarize",
    }).
    AddEdge("enhance", "format_output").
    AddEdge("summarize", "format_output").
    Compile()

// Create GraphAgent and run.
graphAgent, err := graphagent.New("document-processor", g,
    graphagent.WithDescription("Document processing workflow"),
    graphagent.WithInitialState(graph.State{}),
)

runner := runner.NewRunner("doc-workflow", graphAgent)
events, _ := runner.Run(ctx, userID, sessionID,
    model.NewUserMessage("Process this document content"))
```

For detailed information about the Agent module, please refer to [Agent](./agent.md), [Multi-Agent](./multiagent.md), and [Graph](./graph.md)

### Event Module - Event-Driven System

The Event module is the core of tRPC-Agent-Go's event system, responsible for state transmission and real-time communication during agent execution. Through a unified event model, it achieves decoupled communication between agents and transparent execution monitoring.

#### Core Features

- **Asynchronous Communication**: Agents communicate through event streams in a non-blocking manner, supporting high-concurrency execution
- **Real-time Monitoring**: All execution states are transmitted in real-time through events, supporting streaming processing
- **Unified Abstraction**: Different types of agents interact through the same event interface
- **Multi-Agent Collaboration**: Supports branch event filtering and state tracking

#### Core Interface

```go
// Event represents an event during agent execution.
type Event struct {
    *model.Response           // Embed all fields of LLM response.
    InvocationID    string    // Unique identifier for this call.
    Author          string    // Event initiator (Agent name).
    ID              string    // Event unique identifier.
    Timestamp       time.Time // Event timestamp.
    Branch          string    // Branch identifier (multi-agent collaboration).
}
```

#### Main Event Types

- **`chat.completion`** - LLM conversation completion event
- **`chat.completion.chunk`** - Streaming conversation event
- **`tool.response`** - Tool response event
- **`agent.transfer`** - Agent transfer event
- **`error`** - Error event

#### Agent.Run() and Event Handling

All agents return event streams through the `Run()` method, implementing a unified execution interface:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// Agent interface definition.
type Agent interface {
    Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)
}

// Create agent and execute using Runner.
agent := llmagent.New("assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(tools))

// Execute agent using Runner (recommended approach).
runner := runner.NewRunner("calculator-app", agent)
events, err := runner.Run(ctx, "user-001", "session-001", 
    model.NewUserMessage("What is 2+3?"))

// Process event stream in real-time.
for event := range events {
    switch event.Object {
    case "chat.completion.chunk":
        fmt.Print(event.Choices[0].Delta.Content)
    case "tool.response":
        fmt.Printf("\n[%s] Tool execution completed\n", event.Author)
    case "chat.completion":
        if event.Done {
            fmt.Printf("\n[%s] Final answer: %s\n", 
                event.Author, event.Choices[0].Message.Content)
        }
    case "error":
        fmt.Printf("Error: %s\n", event.Error.Message)
        return event.Error
    }
    if event.Done { break }
}
```

#### Event Flow in Multi-Agent Collaboration

```go
chainAgent := chainagent.New("chain", 
    chainagent.WithSubAgents([]agent.Agent{
        analysisAgent, solutionAgent,
    }))

events, err := chainAgent.Run(ctx, invocation)
if err != nil {
    return err
}

for event := range events {
    switch event.Object {
    case "chat.completion.chunk":
        fmt.Print(event.Choices[0].Delta.Content)
    case "chat.completion":
        if event.Done {
            fmt.Printf("[%s] Completed: %s\n", event.Author, 
                event.Choices[0].Message.Content)
        }
    case "tool.response":
        fmt.Printf("[%s] Tool execution completed\n", event.Author)
    case "error":
        fmt.Printf("[%s] Error: %s\n", event.Author, event.Error.Message)
    }
}
```

#### Multi-Agent System - Multi-Agent Collaboration System

tRPC-Agent-Go uses the SubAgent mechanism to build multi-agent systems, supporting multiple agents collaborating to handle complex tasks.

```go
// Create professional domain agents.
marketAnalyst := llmagent.New("market-analyst",
    llmagent.WithModel(model),
    llmagent.WithInstruction("You are a market analysis expert"),
    llmagent.WithTools([]tool.Tool{marketDataTool}))

techArchitect := llmagent.New("tech-architect", 
    llmagent.WithModel(model),
    llmagent.WithInstruction("You are a technical architecture expert"),
    llmagent.WithTools([]tool.Tool{techAnalysisTool}))

// Serial collaboration: Market analysis → Technical evaluation.
planningChain := chainagent.New("product-planning",
    chainagent.WithSubAgents([]agent.Agent{
        marketAnalyst, techArchitect,
    }))

// Parallel collaboration: Multiple experts evaluate simultaneously.
expertPanel := parallelagent.New("expert-panel",
    parallelagent.WithSubAgents([]agent.Agent{
        marketAnalyst, techArchitect,
    }))

// Execute multi-agent collaboration.
runner := runner.NewRunner("expert-panel-app", masterAgent)
events, err := runner.Run(ctx, "user-001", "session-001", 
    model.NewUserMessage("Analyze the market and design product solutions"))
```

For detailed information about the Event module, please refer to [Event](./event.md)

### Planner Module - Intelligent Planning Engine

The Planner module provides agents with intelligent planning capabilities, enhancing their reasoning and decision-making abilities through different planning strategies. It supports three modes: built-in thinking models, React structured planning, and custom explicit planning guidance, enabling agents to better decompose complex tasks and formulate execution plans. The React mode, through "thinking-action" cycles and structured labels, provides explicit reasoning guidance for ordinary models, ensuring agents can systematically handle complex tasks.

#### Core Interface Design

```go
// Planner interface defines methods that all planners must implement.
type Planner interface {
    // Build planning instructions, adding planning-related system instructions to LLM requests.
    BuildPlanningInstruction(
        ctx context.Context,
        invocation *agent.Invocation,
        llmRequest *model.Request,
    ) string
    
    // Process planning responses, performing post-processing and structuring of LLM responses.
    ProcessPlanningResponse(
        ctx context.Context,
        invocation *agent.Invocation,
        response *model.Response,
    ) *model.Response
}
```

#### Built-in Planning Strategies

**Builtin Planner - Built-in Thinking Planner**

Applicable to models with native thinking capabilities, enabling internal reasoning mechanisms through model parameter configuration:

```go
// Configure reasoning intensity for OpenAI o-series models.
builtinPlanner := builtin.New(builtin.Options{
    ReasoningEffort: stringPtr("medium"), // "low", "medium", "high".
}

// Enable thinking mode for Claude/Gemini models.
builtinPlanner := builtin.New(builtin.Options{
    ThinkingEnabled: boolPtr(true),
    ThinkingTokens:  intPtr(1000),
})
```

**React Planner - Structured Planner**

The `React (Reasoning and Acting) Planner` is an AI reasoning mode that guides models through "thinking-action" cycles through structured labels. It decomposes complex problems into four standardized stages: planning, reasoning analysis, action execution, and providing answers. This explicit reasoning process enables agents to systematically handle complex tasks while improving the explainability of decisions and error detection capabilities.

#### Integration with Agent

The `React Planner` can be seamlessly integrated into any LLMAgent, providing agents with structured thinking capabilities. After integration, agents will automatically process user requests according to the four stages of React mode, ensuring that each complex task receives systematic processing.

```go
// Create agent with planning capabilities.
agent := llmagent.New(
    "planning-assistant",
    llmagent.WithModel(openai.New("gpt-4o")),
    llmagent.WithPlanner(reactPlanner), // Integrate planner.
    llmagent.WithInstruction("You are an intelligent assistant good at planning"),
)

// Agent will automatically use the planner to:
// 1. Formulate step-by-step plans for complex tasks (PLANNING stage).
// 2. Conduct reasoning analysis during execution (REASONING stage).
// 3. Call corresponding tools to perform specific operations (ACTION stage).
// 4. Integrate all information to provide complete answers (FINAL_ANSWER stage).
```

**Actual Application Effects**: Agents using the `React Planner` exhibit obvious structured thinking characteristics when handling complex queries. For example, when users ask "Help me plan a trip," the agent will first analyze requirements (`PLANNING`), then reason about the best route (`REASONING`), then query specific information (`ACTION`), and finally provide complete travel advice (`FINAL_ANSWER`). This approach not only improves answer quality but also allows users to clearly see the agent's thinking process.

#### Custom Planner

Developers can implement custom planners to meet specific requirements:

```go
// Custom Reflection planner example.
type ReflectionPlanner struct {
    maxIterations int
}

func (p *ReflectionPlanner) BuildPlanningInstruction(
    ctx context.Context,
    invocation *agent.Invocation,
    llmRequest *model.Request,
) string {
    return `Please follow these steps for reflective planning:
1. Analyze the problem and formulate initial plans
2. Execute plans and collect results
3. Reflect on the execution process, identify problems and improvement points
4. Optimize plans based on reflection and re-execute
5. Repeat reflection-optimization process until satisfactory results are achieved`
}

func (p *ReflectionPlanner) ProcessPlanningResponse(
    ctx context.Context,
    invocation *agent.Invocation,
    response *model.Response,
) *model.Response {
    // Process reflection content, extract improvement suggestions.
// Implement reflection logic...
return response
}

// Use custom planner.
reflectionPlanner := &ReflectionPlanner{maxIterations: 3}
agent := llmagent.New(
    "reflection-agent",
    llmagent.WithModel(model),
    llmagent.WithPlanner(reflectionPlanner), // Use custom planner.
)
```

For detailed information about the Planner module, please refer to [Planner](./planner.md)

### Tool Module - Tool Calling Framework

The Tool module provides standardized tool definition, registration, and execution mechanisms, enabling agents to interact with the external world. It supports two modes: synchronous calling (`CallableTool`) and streaming calling (`StreamableTool`), meeting different technical requirements for various scenarios.

#### Core Interface Design

```go
// Basic tool interface.
type Tool interface {
    Declaration() *Declaration  // Return tool metadata.
}

// Synchronous calling tool interface.
type CallableTool interface {
    Call(ctx context.Context, jsonArgs []byte) (any, error)
    Tool
}

// Streaming tool interface.
type StreamableTool interface {
    StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error)
    Tool
}
```

#### Tool Creation Examples

```go
// Calculator tool.
calculatorTool := function.NewFunctionTool(
    func(ctx context.Context, input struct {
        Operation string  `json:"operation"`
        A         float64 `json:"a"`
        B         float64 `json:"b"`
    }) (struct {
        Result float64 `json:"result"`
    }, error) {
        var result float64
        switch input.Operation {
        case "add":
            result = input.A + input.B
        case "multiply":
            result = input.A * input.B
        case "subtract":
            result = input.A - input.B
        case "divide":
            if input.B != 0 {
                result = input.A / input.B
            } else {
                return struct{Result float64}{}, fmt.Errorf("division by zero")
            }
        default:
            return struct{Result float64}{}, fmt.Errorf("unsupported operation: %s", input.Operation)
        }
        return struct{Result float64}{result}, nil
    },
    function.WithName("calculator"),
    function.WithDescription("Perform mathematical calculations"),
)

// Streaming log query tool type definition.
type logInput struct {
    Query string `json:"query"`
}

type logOutput struct {
    Log string `json:"log"`
}

// Streaming log query tool.
logStreamTool := function.NewStreamableFunctionTool[logInput, logOutput](
    func(input logInput) *tool.StreamReader {
        stream := tool.NewStream(10)
        go func() {
            defer stream.Writer.Close()
            for i := 0; i < 5; i++ {
                chunk := tool.StreamChunk{
                    Content: logOutput{
                        Log: fmt.Sprintf("Log %d: %s", i+1, input.Query),
                    },
                }
                if stream.Writer.Send(chunk, nil) {
                    return // stream closed.
                }
                time.Sleep(50 * time.Millisecond)
            }
        }()
        return stream.Reader
    },
    function.WithName("log_stream"),
    function.WithDescription("Streaming log query"),
)

// Create multi-tool agent.
agent := llmagent.New(
    "multi-tool-assistant",
    llmagent.WithModel(model),
    llmagent.WithTools([]tool.Tool{
        calculatorTool,
        logStreamTool,
        duckduckgo.NewTool(),
    }),
)
```

#### MCP Tool Integration

The framework supports various MCP tool calls, providing multiple connection methods. All MCP tools are created through the unified `NewMCPToolSet` function:

```go
// SSE-connected MCP tool set.
sseToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "sse",
        ServerURL: "https://api.example.com/mcp/sse",
        Headers: map[string]string{
            "Authorization": "Bearer your-token",
        },
        Timeout: 10 * time.Second,
    },
)

// Streamable HTTP-connected MCP tool set.
streamableToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "streamable_http",
        ServerURL: "https://api.example.com/mcp",
        Timeout: 10 * time.Second,
    },
)

// StdIO-connected MCP tool set.
stdoutToolSet := mcp.NewMCPToolSet(
    mcp.ConnectionConfig{
        Transport: "stdio",
        Command: "python",
        Args:    []string{"-m", "my_mcp_server"},
        Timeout: 10 * time.Second,
    },
)

agent := llmagent.New(
    "mcp-agent",
    llmagent.WithModel(model),
    llmagent.WithToolSets([]tool.ToolSet{sseToolSet, streamableToolSet, stdioToolSet}),
)
```

For detailed information about the Tool module, please refer to [Tools](./tool.md)

### CodeExecutor Module - Code Execution Engine

The CodeExecutor module provides agents with code execution capabilities, supporting execution of Python and Bash code in local environments or Docker containers, enabling agents to have practical working abilities such as data analysis, scientific computing, and script automation.

#### Core Interface Design

```go
// CodeExecutor is the core interface for code execution.
type CodeExecutor interface {
	ExecuteCode(context.Context, CodeExecutionInput) (CodeExecutionResult, error)
	CodeBlockDelimiter() CodeBlockDelimiter
}

// Code execution input and results.
type CodeExecutionInput struct {
	CodeBlocks  []CodeBlock
	ExecutionID string
}

type CodeExecutionResult struct {
	Output      string // Execution output.
	OutputFiles []File // Generated files.
}
```

#### Two Executor Implementations

**LocalCodeExecutor - Local Executor**

Executes code directly in the local environment, suitable for development testing and trusted environments:

```go
// Create local executor.
localExecutor := local.New(
    local.WithWorkDir("/tmp/code-execution"),
    local.WithTimeout(30*time.Second),
    local.WithCleanTempFiles(true),
)

// Integrate with agent.
agent := llmagent.New(
    "data-analyst",
    llmagent.WithModel(model),
    llmagent.WithCodeExecutor(localExecutor), // Integrate code executor.
    llmagent.WithInstruction("You are a data analyst who can execute Python code"),
)
```

**ContainerCodeExecutor - Container Executor**

Executes code in isolated Docker containers, providing higher security, suitable for production environments:

```go
// Create container executor.
containerExecutor, err := container.New(
    container.WithContainerConfig(container.Config{
        Image: "python:3.11-slim",
    }),
    container.WithHostConfig(container.HostConfig{
        AutoRemove:  true,
        NetworkMode: "none",  // Network isolation.
        Resources: container.Resources{
            Memory: 128 * 1024 * 1024,  // Memory limit.
        },
    }),
)

agent := llmagent.New(
    "secure-analyst",
    llmagent.WithModel(model),
    llmagent.WithCodeExecutor(containerExecutor), // Use container executor.
)
```

#### Automatic Code Block Recognition

The framework automatically extracts markdown code blocks from agent responses and executes them:

```go
// When agent responses contain code blocks, they are automatically executed:
// ```python
// import statistics
// data = [1, 2, 3, 4, 5]
// print(f"Average: {statistics.mean(data)}")
// ```
//
// Supports Python and Bash code:
// ```bash
// echo "Current time: $(date)"
// ```
```

#### Usage Examples

```go
// Data analysis agent.
dataAgent := llmagent.New(
    "data-scientist",
    llmagent.WithModel(model),
    llmagent.WithCodeExecutor(local.New()),
    llmagent.WithInstruction("You are a data scientist using Python standard library for data analysis"),
)

// User asks question, agent automatically generates and executes code.
runner := runner.NewRunner("analysis", dataAgent)
events, _ := runner.Run(ctx, userID, sessionID, 
    model.NewUserMessage("Analyze data: 23, 45, 12, 67, 34, 89"))

// Agent automatically:
// 1. Generates Python analysis code.
// 2. Executes code to get results.
// 3. Interprets analysis results.
```

The CodeExecutor module upgrades agents from pure conversation to intelligent assistants with practical computing capabilities, supporting application scenarios such as data analysis, script automation, and scientific computing.

### Runner Module - Agent Executor

The Runner module is the executor and runtime environment for agents, responsible for agent lifecycle management, session state maintenance, and event stream processing.

#### Core Interface

```go
type Runner interface {
	Run(
		ctx context.Context,
		userID string,               // User identifier.
		sessionID string,            // Session identifier.
		message model.Message,       // Input message.
		runOpts ...agent.RunOptions, // Run options.
	) (<-chan *event.Event, error)   // Return event stream.
}
```

#### Usage Examples

```go
// Step 1: Create agent.
agent := llmagent.New(
    "customer-service-agent",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
    llmagent.WithInstruction("You are a professional customer service assistant"),
)

// Step 2: Create Runner and bind agent.
runner := runner.NewRunner(
    "customer-service-app", // Application name.
    agent,                  // Bind agent.
)

// Step 3: Execute conversation.
events, err := runner.Run(
    context.Background(),
    "user-001",    // User ID.
    "session-001", // Session ID.
    model.NewUserMessage("Hello, I want to inquire about product information"),
)

// Step 4: Process event stream.
for event := range events {
    if event.Object == "agent.message" && len(event.Choices) > 0 {
        fmt.Printf("Agent: %s\n", event.Choices[0].Message.Content)
    }
}
```

For detailed information about the Runner module, please refer to [Runner](./runner.md)

### Invocation - Agent Execution Context

Invocation is the core context object for agent execution, encapsulating all information and state required for a single call. It serves as a parameter for the `Agent`.Run() method, supporting event tracking, state management, and inter-agent collaboration.

#### Core Structure

```go
type Invocation struct {
	Agent             Agent                    // Agent instance to call.
	AgentName         string                   // Agent name.
	InvocationID      string                   // Call unique identifier.
	Branch            string                   // Branch identifier (multi-agent collaboration).
	EndInvocation     bool                     // Whether to end the call.
	Session           *session.Session         // Session state.
	Model             model.Model              // Language model.
	Message           model.Message            // User message.
	EventCompletionCh <-chan string            // Event completion signal.
	RunOptions        RunOptions               // Run options.
	TransferInfo      *TransferInfo            // Agent transfer information.
	AgentCallbacks    *Callbacks               // Agent callbacks.
	ModelCallbacks    *model.Callbacks         // Model callbacks.
	ToolCallbacks     *tool.Callbacks          // Tool callbacks.
}

type TransferInfo struct {
	TargetAgentName string // Target agent name.
	Message         string // Transfer message.
	EndInvocation   bool   // Whether to end after transfer.
}
```

#### Main Functions

- **Execution Context**: Agent identification, call tracking, branch control
- **State Management**: Session history, model configuration, message passing
- **Event Control**: Asynchronous communication, execution options
- **Agent Collaboration**: Control transfer, callback mechanisms

#### Usage Examples

```go
// Basic call.
invocation := &agent.Invocation{
    AgentName:    "assistant",
    InvocationID: "inv-001",
    Model:        openai.New("gpt-4o-mini"),
    Message:      model.NewUserMessage("Hello"),
    Session:      &session.Session{ID: "session-001"},
}
events, err := agent.Run(ctx, invocation)

// Runner automatically creates (recommended).
runner := runner.NewRunner("my-app", agent)
events, err := runner.Run(ctx, userID, sessionID, userMessage)

// Context retrieval.
invocation, ok := agent.InvocationFromContext(ctx)
```

#### Best Practices

- Prioritize using Runner to automatically create Invocation
- The framework automatically fills in Model, Callbacks, and other fields
- Use transfer tools to implement agent transfer, avoid directly setting TransferInfo

### Memory Module - Intelligent Memory System

The Memory module provides agents with persistent memory capabilities, enabling agents to remember and retrieve user information across sessions, providing personalized interaction experiences.

#### How It Works

Agents automatically identify and store important information through built-in memory tools, supporting topic label classification management, and intelligently retrieve relevant memories when needed. Multi-tenant isolation is achieved through AppName+UserID, ensuring user data security.

#### Application Scenarios

Applicable to personal assistants, customer service robots, educational tutoring, project collaboration, and other scenarios requiring cross-session memory of user information, such as remembering user preferences, tracking problem-solving progress, and saving learning plans.

#### Core Interface

```go
type Service interface {
    // Add new memory.
    AddMemory(ctx context.Context, userKey UserKey, memory string, topics []string) error
    // Update existing memory.
    UpdateMemory(ctx context.Context, memoryKey Key, memory string, topics []string) error
    // Delete specified memory.
    DeleteMemory(ctx context.Context, memoryKey Key) error
    // Clear all user memories.
    ClearMemories(ctx context.Context, userKey UserKey) error
    // Read recent memories.
    ReadMemories(ctx context.Context, userKey UserKey, limit int) ([]*Entry, error)
    // Search memories.
    SearchMemories(ctx context.Context, userKey UserKey, query string) ([]*Entry, error)
    // Get memory tools.
    Tools() []tool.Tool
}

// Data structure.
type Entry struct {
    ID        string    `json:"id"`
    AppName   string    `json:"app_name"`
    UserID    string    `json:"user_id"`
    Memory    *Memory   `json:"memory"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type Memory struct {
    Memory      string     `json:"memory"`
    Topics      []string   `json:"topics,omitempty"`
    LastUpdated *time.Time `json:"last_updated,omitempty"`
}
```

#### Quick Integration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
)

// Create memory service.
memoryService := inmemory.NewMemoryService()

// Create agent with memory capabilities.
agent := llmagent.New(
    "memory-bot",
    llmagent.WithModel(model),
    llmagent.WithMemory(memoryService), // Automatically register memory tools.
)
```

#### Built-in Memory Tools

| Tool Name | Default Status | Function Description |
|-----------|----------------|---------------------|
| `memory_add` | ✅ Enabled | Add new memory entries |
| `memory_update` | ✅ Enabled | Update existing memory content |
| `memory_search` | ✅ Enabled | Search memories by keywords |
| `memory_load` | ✅ Enabled | Load recent memory records |
| `memory_delete` | ❌ Disabled | Delete specified memory entries |
| `memory_clear` | ❌ Disabled | Clear all user memories |

#### Usage Examples

```go
// Agent automatically calls memory tools:

// Record information: "My name is Zhang San, I live in Beijing".
// → memory_add("Zhang San lives in Beijing", ["Personal Information"])

// Query information: "Where do I live?".
// → memory_search("address") → Return relevant memories.

// Update information: "I moved to Shanghai".
// → memory_update(id, "Zhang San lives in Shanghai", ["Personal Information"])
```

For detailed information about the Memory module, please refer to [Memory](./memory.md)

### Session Module - Session Management System

The Session module provides session management functionality for maintaining conversation history and context information during interactions between agents and users. The session management module supports multiple storage backends, including in-memory storage and Redis storage. Other storage backends such as MySQL and PostgreSQL will be added based on user requirements, providing flexible state persistence capabilities for agent applications.

#### Core Features

- **Session Persistence**: Save complete conversation history and context
- **Multiple Storage Backends**: Support in-memory storage and Redis storage
- **Event Tracking**: Complete recording of all interaction events in sessions

#### Session Hierarchy Structure

```
Application (Application)
├── User Sessions (User Sessions)
│   ├── Session 1 (Session 1)
│   │   ├── Session Data (Session Data)
│   │   └── Events (Event List)
│   └── Session 2 (Session 2)
│       ├── Session Data (Session Data)
│       └── Events (Event List)
└── App Data (Application Data)
```

#### Core Interface

```go
// Service defines the core interface of session service.
type Service interface {
	// CreateSession creates a new session.
	CreateSession(ctx context.Context, key Key, state StateMap, options ...Option) (*Session, error)

	// GetSession gets a session.
	GetSession(ctx context.Context, key Key, options ...Option) (*Session, error)

	// ListSessions lists all sessions by user scope of session key.
	ListSessions(ctx context.Context, userKey UserKey, options ...Option) ([]*Session, error)

	// DeleteSession deletes a session.
	DeleteSession(ctx context.Context, key Key, options ...Option) error

	// AppendEvent appends an event to a session.
	AppendEvent(ctx context.Context, session *Session, event *event.Event, options ...Option) error

	// Close closes the service.
	Close() error
}
```

#### Storage Backend Support

```go
// In-memory storage (suitable for development and testing).
sessionService := inmemory.NewSessionService()

// Redis storage (suitable for production environments).
sessionService, err := redis.NewService(
    redis.WithURL("redis://localhost:6379/0"),
)
```

#### Integration with Runner

```go
// Create Runner and configure session service.
runner := runner.NewRunner(
    "my-agent",
    llmAgent,
    runner.WithSessionService(sessionService), // Integrate session management.
)

// Use Runner for multi-round conversations.
eventChan, err := runner.Run(ctx, userID, sessionID, userMessage)
```

For detailed information about the Session module, please refer to [Session](./session.md)

### Knowledge Module - Knowledge Management System

The Knowledge module is the core knowledge management component in trpc-agent-go, implementing complete RAG (Retrieval-Augmented Generation) capabilities. This module not only provides basic knowledge storage and retrieval functions but also supports multiple advanced features:

1. **Knowledge Source Management**
   - Support for multiple formats of local files (Markdown, PDF, TXT, etc.)
   - Support for directory batch import, automatically processing subdirectories
   - Support for web scraping, directly loading content from URLs
   - Intelligent input type recognition, automatically selecting appropriate processors

2. **Vector Storage**
   - In-memory storage: Suitable for development and small-scale testing
   - PostgreSQL + pgvector: Suitable for production environments, supporting persistence
   - TcVector: Cloud-native solution, suitable for large-scale deployment

3. **Text Embedding**
   - Default integration with OpenAI text embedding models
   - Support for custom embedding model integration
   - Asynchronous batch processing for performance optimization

4. **Intelligent Retrieval**
   - Semantic-based similarity search
   - Support for multi-round conversation historical context
   - Result reordering to improve relevance

#### Core Interface Design

```go
// Knowledge is the main interface for knowledge management.
type Knowledge interface {
	// Search performs semantic search and returns relevant results.
Search(ctx context.Context, req *SearchRequest) (*SearchResult, error)
}

// SearchRequest represents a search request with context.
type SearchRequest struct {
	Query     string                // Search query text.
	History   []ConversationMessage // Conversation history for context.
	UserID    string                // User identifier.
	SessionID string                // Session identifier.
}

// SearchResult represents the result of knowledge search.
type SearchResult struct {
	Document *document.Document // Matched document.
	Score    float64            // Relevance score.
	Text     string             // Document content.
}
```

#### Integration with Agent

```go
// Create knowledge base.
kb := knowledge.New(
    knowledge.WithVectorStore(inmemory.New()),
    knowledge.WithEmbedder(openai.New()),
    knowledge.WithSources([]source.Source{
        file.New([]string{"./docs/llm.md"}),
        url.New([]string{"https://wikipedia.org/wiki/LLM"}),
    }),
)

// Load knowledge base.
kb.Load(ctx)

// Create agent with knowledge base.
agent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(model),
    llmagent.WithKnowledge(kb), // Automatically add knowledge_search tool.
    llmagent.WithInstruction("Use the knowledge_search tool to search relevant materials to answer questions"),
)
```

For detailed information about the Knowledge module, please refer to [Knowledge](./knowledge.md)

### Observability Module - Observability System

The Observability module integrates OpenTelemetry standards, **automatically recording** detailed telemetry data during agent execution, supporting full-link tracing and performance monitoring. The framework reuses OpenTelemetry standard interfaces without custom abstraction layers.

#### Quick Start

```go
import (
    agentmetric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
    agenttrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func main() {
    ctx := context.Background()
    
    // Start telemetry collection.
cleanupTrace, _ := agenttrace.Start(ctx)   // Default localhost:4317.
cleanupMetric, _ := agentmetric.Start(ctx) // Default localhost:4318.
    defer cleanupTrace()
    defer cleanupMetric()
    
    // Agent execution process will automatically record telemetry data.
agent := llmagent.New("assistant", 
        llmagent.WithModel(openai.New("gpt-4o-mini")))
    
    runner := runner.NewRunner("app", agent)
    events, _ := runner.Run(ctx, "user-001", "session-001", 
        model.NewUserMessage("Hello"))
}
```

#### Automatically Recorded Trace Links

The framework automatically creates the following Span hierarchy:

```
invocation                             # Conversation top-level span
├── call_llm                           # LLM API call
├── execute_tool calculator            # Tool call
├── execute_tool search                # Tool call
└── execute_tool (merged)              # Parallel tool call merge

# GraphAgent execution link
invocation
└── execute_graph
    ├── execute_node preprocess
    ├── execute_node analyze
    │   └── run_model
    └── execute_node format
```

#### Main Span Attributes

- **Common Attributes**: `invocation_id`, `session_id`, `event_id`
- **LLM Calls**: `gen_ai.request.model`, `llm_request/response` JSON
- **Tool Calls**: `gen_ai.tool.name`, `tool_call_args`, `tool_response` JSON
- **Graph Nodes**: `node_id`, `node_name`, `node_description`

#### Configuration Options

**Custom Endpoint Configuration**
```go
cleanupTrace, _ := agenttrace.Start(ctx,
    agenttrace.WithEndpoint("otel-collector:4317"))
```

**Custom Metrics**
```go
counter, _ := metric.Meter.Int64Counter("agent.requests.total")
counter.Add(ctx, 1, metric.WithAttributes(
    attribute.String("agent.name", "assistant")))
```

For detailed information about the Observability module, please refer to [Observability](./observability.md)

### Debug Server - ADK Web Debug Server

The Debug Server provides HTTP debugging services, compatible with ADK Web UI, supporting visual debugging and real-time monitoring of agent execution.

#### Quick Start

```go
// Step 1: Prepare agent instances.
agents := map[string]agent.Agent{
    "chat-assistant": llmagent.New(
        "chat-assistant",
        llmagent.WithModel(openai.New("gpt-4o-mini")),
        llmagent.WithInstruction("You are an intelligent assistant"),
    ),
}

// Step 2: Create Debug Server.
debugServer := debug.New(agents)

// Step 3: Start HTTP server.
http.Handle("/", debugServer.Handler())
log.Fatal(http.ListenAndServe(":8080", nil))
```

#### Configuration Options

```go
// Optional configuration.
debugServer := debug.New(agents,
    debug.WithSessionService(redisSessionService), // Custom session storage.
    debug.WithRunnerOptions( // Runner additional configuration.
        runner.WithObserver(observer),
    ),
)
```

For detailed information about the Debug Server, please refer to [Debug](./debugserver.md)

### Callbacks Module - Callback Mechanism

The Callbacks module provides a complete set of callback mechanisms, allowing interception and processing at key nodes during agent execution, model reasoning, and tool calls. Through the callback mechanism, functions such as logging, performance monitoring, and content review can be implemented.

#### Callback Types

1. **ModelCallbacks (Model Callbacks)**
```go
// Create model callbacks.
modelCallbacks := model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
        // Pre-model call processing.
        fmt.Printf("🔵 BeforeModel: model=%s, query=%s\n",
            req.Model, req.LastUserMessage())
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, req *model.Request,
        resp *model.Response, err error) (*model.Response, error) {
        // Post-model call processing.
        fmt.Printf("🟣 AfterModel: model=%s completed\n", req.Model)
        return nil, nil
    })
```

- BeforeModel: Triggered before model reasoning, can be used for input interception and logging
- AfterModel: Triggered after each output block, can be used for content review and result processing

2. **ToolCallbacks (Tool Callbacks)**
```go
// Create tool callbacks.
toolCallbacks := tool.NewCallbacks().
    RegisterBeforeTool(func(ctx context.Context, name string,
        decl *tool.Declaration, args []byte) (any, error) {
        // Pre-tool call processing.
        fmt.Printf("🟠 BeforeTool: tool=%s, args=%s\n", name, args)
        return nil, nil
    }).
    RegisterAfterTool(func(ctx context.Context, name string,
        decl *tool.Declaration, args []byte,
        result any, err error) (any, error) {
        // Post-tool call processing.
        fmt.Printf("🟤 AfterTool: tool=%s completed\n", name)
        return nil, nil
    })
```

- BeforeTool: Triggered before tool calls, can be used for parameter validation and result simulation
- AfterTool: Triggered after tool calls, can be used for result processing and logging

3. **AgentCallbacks (Agent Callbacks)**
```go
// Create agent callbacks.
agentCallbacks := agent.NewCallbacks().
    RegisterBeforeAgent(func(ctx context.Context,
        inv *agent.Invocation) (*model.Response, error) {
        // Pre-agent execution processing.
        fmt.Printf("🟢 BeforeAgent: agent=%s starting\n",
            inv.AgentName)
        return nil, nil
    }).
    RegisterAfterAgent(func(ctx context.Context,
        inv *agent.Invocation, err error) (*model.Response, error) {
        // Post-agent execution processing.
        fmt.Printf("🟡 AfterAgent: agent=%s completed\n",
            inv.AgentName)
        return nil, nil
    })
```

- BeforeAgent: Triggered before agent execution, can be used for permission checks and input validation
- AfterAgent: Triggered after agent execution, can be used for result processing and error handling

#### Usage Scenarios

1. **Monitoring and Logging**: Record model calls, tool usage, and agent execution processes
2. **Performance Optimization**: Monitor response times and resource usage
3. **Security and Review**: Filter input content and review output content
4. **Custom Processing**: Format results, retry errors, enhance content

#### Integration Examples

```go
// Create agent with callbacks.
agent := llmagent.New(
    "callback-demo",
    llmagent.WithModel(model),
    llmagent.WithModelCallbacks(modelCallbacks),
    llmagent.WithToolCallbacks(toolCallbacks),
    llmagent.WithAgentCallbacks(agentCallbacks),
)

// Create Runner and execute.
runner := runner.NewRunner(
    "callback-app",
    agent,
    runner.WithSessionService(sessionService),
)

// Execute conversation.
events, err := runner.Run(ctx, userID, sessionID, 
    model.NewUserMessage("Hello"))
```

The Callbacks module provides flexible callback mechanisms, making agent behavior more controllable and transparent, while providing powerful support for monitoring, review, customization, and other requirements.

For detailed information about Callbacks, please refer to [Callback](./callback.md)

### A2A Integration - Inter-Agent Communication

The `A2A (Agent-to-Agent)` module provides inter-agent communication capabilities, supporting quick integration of tRPC-Agent-Go agents into the A2A protocol, achieving multi-agent collaboration and external capability exposure.

#### Quick Start

```go
// Step 1: Create agent.
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
    llmagent.WithInstruction("You are an intelligent assistant"),
)

// Step 2: Create A2A server.
a2aServer, err := a2a.New(
    a2a.WithAgent(agent),           // Bind agent.
    a2a.WithHost("localhost:8080"), // Set listening address.
)
if err != nil {
    log.Fatal(err)
}

// Step 3: Start server.
ctx := context.Background()
if err := a2aServer.Start(ctx); err != nil {
    log.Fatal(err)
}

log.Println("A2A server started: localhost:8080")
```

For detailed information about A2A integration, please refer to [A2A](./a2a.md)

### Future Plans

tRPC-Agent-Go will continue to evolve, with plans to expand in the following directions:

- **Artifacts Support**: Integrate structured data display and interaction capabilities, supporting visualization of various data formats such as charts, tables, code, etc.
- **Multimodal Streaming Processing**: Extend streaming processing capabilities for audio, image, video, and other multimodal data, achieving richer interaction experiences
- **Multi-Agent Mode Extension**: Add more agent collaboration modes, such as competitive, voting, hierarchical decision-making, and other advanced collaboration strategies
- **Ecosystem Integration**: Deepen integration with the tRPC ecosystem, providing more component ecosystems such as Knowledge, Memory, Tools, etc.
