# Graph Package Usage Guide

The Graph package is a core component in trpc-agent-go for building and executing workflows. It provides a type-safe, extensible graph execution engine that supports complex AI workflow orchestration.

## Overview

The Graph package allows you to model complex AI workflows as directed graphs, where nodes represent processing steps and edges represent data flow and control flow. It is particularly suitable for building AI applications that require conditional routing, state management, and multi-step processing.

### Usage Pattern

The usage of the Graph package follows this pattern:

1. **Create Graph**: Use `StateGraph` builder to define workflow structure
2. **Create GraphAgent**: Wrap the compiled Graph as an Agent
3. **Create Runner**: Use Runner to manage sessions and execution environment
4. **Execute Workflow**: Execute workflow through Runner and handle results

This pattern provides:

- **Type Safety**: Ensures data consistency through state schema
- **Session Management**: Supports concurrent execution for multiple users and sessions
- **Event Stream**: Real-time monitoring of workflow execution progress
- **Error Handling**: Unified error handling and recovery mechanisms

### Agent Integration

GraphAgent implements the `agent.Agent` interface and can:

- **Act as Independent Agent**: Execute directly through Runner
- **Act as SubAgent**: Be used as a sub-agent by other Agents (such as LLMAgent)
- **Host SubAgents**: Register child agents via `graphagent.WithSubAgents` and invoke them through `AddAgentNode`

This design lets GraphAgent plug into other agents while orchestrating its own specialized sub-agents.

### Key Features

- **Type-safe state management**: Use Schema to define state structure, support custom Reducers
- **Conditional routing**: Dynamically select execution paths based on state
- **LLM node integration**: Built-in support for large language models
- **Tool nodes**: Support function calls and external tool integration
- **Agent nodes**: Delegate parts of the workflow to registered sub-agents
- **Streaming execution**: Support real-time event streams and progress tracking
- **Concurrency safety**: Thread-safe graph execution
- **Checkpoint-based Time Travel**: Navigate through execution history and restore previous states
- **Human-in-the-Loop (HITL)**: Support for interactive workflows with interrupt and resume capabilities
- **Atomic checkpointing**: Atomic storage of checkpoints with pending writes for reliable recovery
- **Checkpoint Lineage**: Track related checkpoints forming execution threads with parent-child relationships

## Core Concepts

### 1. Graph

A graph is the core structure of a workflow, consisting of nodes and edges:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Create state schema.
schema := graph.NewStateSchema()

// Create graph.
graph := graph.New(schema)
```

**Virtual Nodes**:

- `Start`: Virtual start node, automatically connected through `SetEntryPoint()`
- `End`: Virtual end node, automatically connected through `SetFinishPoint()`
- These nodes don't need to be explicitly created, the system automatically handles connections

### 2. Node

A node represents a processing step in the workflow:

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Node function signature.
type NodeFunc func(ctx context.Context, state graph.State) (any, error)

// Create node.
node := &graph.Node{
    ID:          "process_data",
    Name:        "Data Processing",
    Description: "Process input data",
    Function:    processDataFunc,
}
```

### 3. State

State is a data container passed between nodes:

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// State is a key-value pair mapping.
type State map[string]any

// User-defined state keys.
const (
	StateKeyInput         = "input"          // Input data.
	StateKeyResult        = "result"         // Processing result.
	StateKeyProcessedData = "processed_data" // Processed data.
	StateKeyStatus        = "status"         // Processing status.
)
```

**Built-in State Keys**:

The Graph package provides some built-in state keys, mainly for internal system communication:

**User-accessible Built-in Keys**:

- `StateKeyUserInput`: User input (one-shot, cleared after consumption, persisted by LLM nodes)
- `StateKeyOneShotMessages`: One-shot messages (complete override for current round, cleared after consumption)
- `StateKeyLastResponse`: Last response (used to set final output, Executor reads this value as result)
- `StateKeyMessages`: Message history (durable, supports append + MessageOp patch operations)
- `StateKeyNodeResponses`: Per-node responses map. Key is node ID, value is the
  node's final textual response. Use `StateKeyLastResponse` for the final
  serial output; when multiple parallel nodes converge, read each node's
  output from `StateKeyNodeResponses`.
- `StateKeyMetadata`: Metadata (general metadata storage available to users)

**System Internal Keys** (users should not use directly):

- `StateKeySession`: Session information (automatically set by GraphAgent)
- `StateKeyExecContext`: Execution context (automatically set by Executor)
- `StateKeyToolCallbacks`: Tool callbacks (automatically set by Executor)
- `StateKeyModelCallbacks`: Model callbacks (automatically set by Executor)

Users should use custom state keys to store business data, and only use user-accessible built-in state keys when necessary.

### 4. State Schema

State schema defines the structure and behavior of state:

```go
import (
    "reflect"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Create state schema.
schema := graph.NewStateSchema()

// Add field definitions.
schema.AddField("counter", graph.StateField{
    Type:    reflect.TypeOf(0),
    Reducer: graph.DefaultReducer,
    Default: func() any { return 0 },
})
```

## Usage Guide

### Node I/O Conventions

Nodes communicate exclusively through the shared state. Each node returns a state delta which is merged into the graph state using the schema’s reducers. Downstream nodes read whatever upstream nodes wrote.

- Common built‑in keys (user‑facing)
  - `user_input`: One‑shot input for the next LLM/Agent node. Cleared after consumption.
  - `one_shot_messages`: Full message override for the next LLM call. Cleared after consumption.
  - `messages`: Durable conversation history (LLM/Tools append here). Supports MessageOp patches.
  - `last_response`: The last textual assistant response.
  - `node_responses`: Map[nodeID]any — per‑node final textual response. Use `last_response` for the most recent.

- Function node
  - Input: the entire state
  - Output: return a `graph.State` delta with custom keys (declare them in the schema), e.g. `{"parsed_time": "..."}`

- LLM node
  - Input priority: `one_shot_messages` → `user_input` → `messages`
  - Output:
    - Appends assistant message to `messages`
    - Sets `last_response`
    - Sets `node_responses[<llm_node_id>]`

- Tools node
  - Input: scans `messages` for the latest assistant message with `tool_calls`
  - Output: appends tool responses to `messages`

- Agent node (sub‑agent)
  - Input: state is injected into the sub‑agent’s `Invocation.RunOptions.RuntimeState`.
    - Model/Tool callbacks can access it via `agent.InvocationFromContext(ctx)`.
  - Output on finish:
    - Sets `last_response`
    - Sets `node_responses[<agent_node_id>]`
    - Clears `user_input`

Recommended patterns

- Add your own keys in the schema (e.g., `parsed_time`, `final_payload`) and write/read them in function nodes.
- To feed structured hints into an LLM node, write `one_shot_messages` in the previous node (e.g., prepend a system message with parsed context).
- To consume an upstream node’s text, read `last_response` immediately downstream or fetch from `node_responses[that_node_id]` later.

See examples:

- `examples/graph/io_conventions` — Function + LLM + Agent I/O
- `examples/graph/io_conventions_tools` — Adds a Tools node path and shows how to capture tool JSON

#### Constant references (import and keys)

- Import: `import "trpc.group/trpc-go/trpc-agent-go/graph"`
- Defined in: `graph/state.go`

- User‑facing keys
  - `user_input` → `graph.StateKeyUserInput`
  - `one_shot_messages` → `graph.StateKeyOneShotMessages`
  - `messages` → `graph.StateKeyMessages`
  - `last_response` → `graph.StateKeyLastResponse`
  - `node_responses` → `graph.StateKeyNodeResponses`

- Other useful keys
  - `session` → `graph.StateKeySession`
  - `metadata` → `graph.StateKeyMetadata`
  - `current_node_id` → `graph.StateKeyCurrentNodeID`
  - `exec_context` → `graph.StateKeyExecContext`
  - `tool_callbacks` → `graph.StateKeyToolCallbacks`
  - `model_callbacks` → `graph.StateKeyModelCallbacks`
  - `agent_callbacks` → `graph.StateKeyAgentCallbacks`
  - `parent_agent` → `graph.StateKeyParentAgent`

Snippet:

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func myNode(ctx context.Context, state graph.State) (any, error) {
    last, _ := state[graph.StateKeyLastResponse].(string)
    return graph.State{"my_key": last}, nil
}
```

#### Event metadata keys (StateDelta)

- Import: `import "trpc.group/trpc-go/trpc-agent-go/graph"`
- Defined in: `graph/events.go`

- Model metadata: `_model_metadata` → `graph.MetadataKeyModel` (struct `graph.ModelExecutionMetadata`)
- Tool metadata: `_tool_metadata` → `graph.MetadataKeyTool` (struct `graph.ToolExecutionMetadata`)

Snippet:

```go
if b, ok := event.StateDelta[graph.MetadataKeyModel]; ok {
    var md graph.ModelExecutionMetadata
    _ = json.Unmarshal(b, &md)
}
```

### 1. Creating GraphAgent and Runner

Users mainly use the Graph package by creating GraphAgent and then using it through Runner. This is the recommended usage pattern:

```go
package main

import (
    "context"
    "fmt"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    // 1. Create state schema.
    schema := graph.MessagesStateSchema()

    // 2. Create state graph builder.
    stateGraph := graph.NewStateGraph(schema)

    // 3. Add nodes.
    stateGraph.AddNode("start", startNodeFunc).
        AddNode("process", processNodeFunc)

    // 4. Set edges.
    stateGraph.AddEdge("start", "process")

    // 5. Set entry point and finish point.
    // SetEntryPoint automatically creates edge from virtual Start node to "start" node.
    // SetFinishPoint automatically creates edge from "process" node to virtual End node.
    stateGraph.SetEntryPoint("start").
        SetFinishPoint("process")

    // 6. Compile graph.
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        panic(err)
    }

    // 7. Create GraphAgent.
    graphAgent, err := graphagent.New("simple-workflow", compiledGraph,
        graphagent.WithDescription("Simple workflow example"),
        graphagent.WithInitialState(graph.State{}),
    )
    if err != nil {
        panic(err)
    }

    // 8. Create session service.
    sessionService := inmemory.NewSessionService()

    // 9. Create Runner.
    appRunner := runner.NewRunner(
        "simple-app",
        graphAgent,
        runner.WithSessionService(sessionService),
    )

    // 10. Execute workflow.
    ctx := context.Background()
    userID := "user"
    sessionID := fmt.Sprintf("session-%d", time.Now().Unix())

    // Create user message (Runner automatically puts message content into StateKeyUserInput).
    message := model.NewUserMessage("Hello World")

    // Execute through Runner.
    eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
    if err != nil {
        panic(err)
    }

    // Handle event stream.
    for event := range eventChan {
        if event.Error != nil {
            fmt.Printf("Error: %s\n", event.Error.Message)
            continue
        }

        if len(event.Response.Choices) > 0 {
            choice := event.Response.Choices[0]
            if choice.Delta.Content != "" {
                fmt.Print(choice.Delta.Content)
            }
        }

        if event.Done {
            break
        }
    }
}

// Node function implementations.
func startNodeFunc(ctx context.Context, state graph.State) (any, error) {
    // Get user input from built-in StateKeyUserInput (automatically set by Runner).
    input := state[graph.StateKeyUserInput].(string)
    return graph.State{
        StateKeyProcessedData: fmt.Sprintf("Processed: %s", input),
    }, nil
}

func processNodeFunc(ctx context.Context, state graph.State) (any, error) {
    processed := state[StateKeyProcessedData].(string)
    result := fmt.Sprintf("Result: %s", processed)
    return graph.State{
        StateKeyResult: result,
        // Use built-in StateKeyLastResponse to set final output.
        graph.StateKeyLastResponse: fmt.Sprintf("Final result: %s", result),
    }, nil
}
```

### 2. Using LLM Nodes

LLM nodes implement a fixed three-stage input rule without extra configuration:

1. OneShot first: If `one_shot_messages` exists, use it as the input for this round.
2. UserInput next: Otherwise, if `user_input` exists, persist once to history.
3. History default: Otherwise, use durable `messages` as input.

```go
// Create LLM model.
model := openai.New("gpt-4")

// Add LLM node.
stateGraph.AddLLMNode("analyze", model,
    `You are a document analysis expert. Analyze the provided document and:
1. Classify document type and complexity
2. Extract key themes
3. Evaluate content quality
Please provide structured analysis results.`,
    nil) // Tool mapping.
```

Important notes:

- System prompt is only used for this round and is not persisted to state.
- One-shot keys (`user_input` / `one_shot_messages`) are automatically cleared after successful execution.
- All state updates are atomic.
- GraphAgent/Runner only sets `user_input` and no longer pre-populates `messages` with a user message. This allows any pre-LLM node to modify `user_input` and have it take effect in the same round.

#### Three input paradigms

- OneShot (`StateKeyOneShotMessages`):

  - When present, only the provided `[]model.Message` is used for this round, typically including a full system prompt and user prompt. Automatically cleared afterwards.
  - Use case: a dedicated pre-node constructs the full prompt and must fully override input.

- UserInput (`StateKeyUserInput`):

  - When non-empty, the LLM node uses durable `messages` plus this round's user input to call the model. After the call, it writes the user input and assistant reply to `messages` using `MessageOp` (e.g., `AppendMessages`, `ReplaceLastUser`) atomically, and clears `user_input` to avoid repeated appends.
  - Use case: conversational flows where pre-nodes may adjust user input.

- Messages only (just `StateKeyMessages`):
  - Common in tool-call loops. After the first round via `user_input`, routing to tools and back to LLM, since `user_input` is cleared, the LLM uses only `messages` (history). The tail is often a `tool` response, enabling the model to continue reasoning based on tool outputs.

#### Atomic updates with Reducer and MessageOp

The Graph package supports `MessageOp` patch operations (e.g., `ReplaceLastUser`,
`AppendMessages`) on message state via `MessageReducer` to achieve atomic merges. Benefits:

- Pre-LLM nodes can modify `user_input`. The LLM node returns a single state delta with the needed patch operations (replace last user message, append assistant message) for one-shot, race-free persistence.
- Backwards compatible with appending `[]Message`, while providing more expressive updates for complex cases.

Example: modify `user_input` in a pre-node before entering the LLM node.

```go
stateGraph.
    AddNode("prepare_input", func(ctx context.Context, s graph.State) (any, error) {
        cleaned := strings.TrimSpace(s[graph.StateKeyUserInput].(string))
        return graph.State{graph.StateKeyUserInput: cleaned}, nil
    }).
    AddLLMNode("ask", modelInstance,
        "You are a helpful assistant. Answer concisely.",
        nil).
    SetEntryPoint("prepare_input").
    SetFinishPoint("ask")
```

### 3. GraphAgent Configuration Options

GraphAgent supports various configuration options:

```go
// Multiple options can be used when creating GraphAgent.
graphAgent, err := graphagent.New(
    "workflow-name",
    compiledGraph,
    graphagent.WithDescription("Workflow description"),
    graphagent.WithInitialState(graph.State{
        "initial_data": "Initial data",
    }),
    graphagent.WithChannelBufferSize(1024),            // Tune event buffer size
    graphagent.WithCheckpointSaver(memorySaver),       // Persist checkpoints if needed
    graphagent.WithSubAgents([]agent.Agent{subAgent}), // Register sub-agents by name
    graphagent.WithAgentCallbacks(&agent.Callbacks{
        // Agent-level callbacks.
    }),
)
```

> Model/tool callbacks are configured per node, e.g. `AddLLMNode(..., graph.WithModelCallbacks(...))`
> or `AddToolsNode(..., graph.WithToolCallbacks(...))`.

Once sub-agents are registered you can delegate within the graph via agent nodes:

```go
// Assume subAgent.Info().Name == "assistant"
stateGraph.AddAgentNode("assistant",
    graph.WithName("Delegate to assistant agent"),
    graph.WithDescription("Invoke the pre-registered assistant agent"),
)

// During execution the GraphAgent looks up a sub-agent with the same name and runs it
```

> The agent node uses its ID for the lookup, so keep `AddAgentNode("assistant")`
> aligned with `subAgent.Info().Name == "assistant"`.

### 4. Conditional Routing

```go
// Define condition function.
func complexityCondition(ctx context.Context, state graph.State) (string, error) {
    complexity := state["complexity"].(string)
    if complexity == "simple" {
        return "simple_process", nil
    }
    return "complex_process", nil
}

// Add conditional edges.
stateGraph.AddConditionalEdges("analyze", complexityCondition, map[string]string{
    "simple_process":  "simple_node",
    "complex_process": "complex_node",
})
```

### 5. Tool Node Integration

```go
// Create tools.
tools := map[string]tool.Tool{
    "calculator": calculatorTool,
    "search":     searchTool,
}

// Add tool node.
stateGraph.AddToolsNode("tools", tools)

// Add conditional routing from LLM to tools.
stateGraph.AddToolsConditionalEdges("llm_node", "tools", "fallback_node")
```

Tool-call pairing and second entry into LLM:

- Scan `messages` backward from the tail to find the most recent `assistant(tool_calls)`; stop at `user` to ensure correct pairing.
- When returning from tools to the LLM node, since `user_input` is cleared, the LLM follows the “Messages only” branch and continues based on the tool response in history.

#### Placeholder Variables in LLM Instructions

LLM nodes support placeholder injection in their `instruction` string (same rules as LLMAgent):

- `{key}` → replaced by `session.State["key"]`
- `{key?}` → optional; missing values become empty
- `{user:subkey}`, `{app:subkey}`, `{temp:subkey}` → access user/app/temp scopes (session services merge app/user state into session with these prefixes)

Notes:

- GraphAgent writes the current `*session.Session` into graph state under `StateKeySession`; the LLM node reads values from there
- Unprefixed keys (e.g., `research_topics`) must be present directly in `session.State`

Example:

```go
mdl := openai.New(modelName)
stateGraph.AddLLMNode(
  "research",
  mdl,
  "You are a research assistant. Focus: {research_topics}. User: {user:topics?}. App: {app:banner?}.",
  nil,
)
```

See the runnable example: `examples/graph/placeholder`.

### 6. Runner Configuration

Runner provides session management and execution environment:

```go
// Create session service.
sessionService := inmemory.NewSessionService()
// Or use Redis session service.
// sessionService, err := redis.NewService(redis.WithRedisClientURL("redis://localhost:6379"))

// Create Runner.
appRunner := runner.NewRunner(
    "app-name",
    graphAgent,
    runner.WithSessionService(sessionService),
    // Can add more configuration options.
)

// Use Runner to execute workflow.
// Runner only sets StateKeyUserInput; it no longer pre-populates StateKeyMessages.
message := model.NewUserMessage("User input")
eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
```

### 7. Message State Schema

For conversational applications, you can use predefined message state schema:

```go
// Use message state schema.
schema := graph.MessagesStateSchema()

// This schema includes:
// - messages: Conversation history (StateKeyMessages).
// - user_input: User input (StateKeyUserInput).
// - last_response: Last response (StateKeyLastResponse).
// - node_responses: Map of nodeID -> response (StateKeyNodeResponses).
// - metadata: Metadata (StateKeyMetadata).
```

### 8. State Key Usage Scenarios

**User-defined State Keys**: Used to store business logic data.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Recommended: Use custom state keys.
const (
    StateKeyDocumentLength = "document_length"
    StateKeyComplexityLevel = "complexity_level"
    StateKeyProcessingStage = "processing_stage"
)

// Use in nodes.
return graph.State{
    StateKeyDocumentLength: len(input),
    StateKeyComplexityLevel: "simple",
    StateKeyProcessingStage: "completed",
}, nil
```

**Built-in State Keys**: Used for system integration.

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Get user input (automatically set by system).
userInput := state[graph.StateKeyUserInput].(string)

// Set final output (system will read this value).
return graph.State{
    graph.StateKeyLastResponse: "Processing complete",
}, nil

// Read per-node responses when multiple nodes (e.g., parallel LLM nodes)
// produce outputs. Values are stored as a map[nodeID]any and merged across
// steps. Use LastResponse for the final serial output; use NodeResponses for
// converged parallel outputs.
responses, _ := state[graph.StateKeyNodeResponses].(map[string]any)
news := responses["news"].(string)
dialog := responses["dialog"].(string)

// Use them separately or combine into the final output.
return graph.State{
    "news_output":  news,
    "dialog_output": dialog,
    graph.StateKeyLastResponse: news + "\n" + dialog,
}, nil

// Store metadata.
return graph.State{
    graph.StateKeyMetadata: map[string]any{
        "timestamp": time.Now(),
        "version": "1.0",
    },
}, nil
```

## Advanced Features

### 1. Interrupt and Resume (Human-in-the-Loop)

The Graph package supports human-in-the-loop (HITL) workflows through interrupt and resume functionality. This enables workflows to pause execution, wait for human input or approval, and then resume from the exact point where they were interrupted.

#### Basic Usage

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Create a node that can interrupt execution for human input
b.AddNode("approval_node", func(ctx context.Context, s graph.State) (any, error) {
    // Use the Interrupt helper for clean interrupt/resume handling
    prompt := map[string]any{
        "message": "Please approve this action (yes/no):",
        "data":    s["some_data"],
    }
    
    // Interrupt execution and wait for user input
    // The key "approval" identifies this specific interrupt point
    resumeValue, err := graph.Interrupt(ctx, s, "approval", prompt)
    if err != nil {
        return nil, err
    }
    
    // Process the resume value when execution continues
    approved := false
    if resumeStr, ok := resumeValue.(string); ok {
        approved = resumeStr == "yes"
    }
    
    return graph.State{
        "approved": approved,
    }, nil
})
```

#### Multi-Stage Approval Example

```go
// First approval stage
b.AddNode("first_approval", func(ctx context.Context, s graph.State) (any, error) {
    prompt := map[string]any{
        "message": "Manager approval required:",
        "level": 1,
    }
    
    approval, err := graph.Interrupt(ctx, s, "manager_approval", prompt)
    if err != nil {
        return nil, err
    }
    
    if approval != "yes" {
        return graph.State{"rejected_at": "manager"}, nil
    }
    
    return graph.State{"manager_approved": true}, nil
})

// Second approval stage (only if first approved)
b.AddNode("second_approval", func(ctx context.Context, s graph.State) (any, error) {
    if !s["manager_approved"].(bool) {
        return s, nil // Skip if not approved by manager
    }
    
    prompt := map[string]any{
        "message": "Director approval required:",
        "level": 2,
    }
    
    approval, err := graph.Interrupt(ctx, s, "director_approval", prompt)
    if err != nil {
        return nil, err
    }
    
    return graph.State{
        "director_approved": approval == "yes",
        "final_approval": approval == "yes",
    }, nil
})
```

#### Resume from Interrupt

```go
// Resume execution with user input using ResumeMap
cmd := &graph.Command{
    ResumeMap: map[string]any{
        "approval": "yes", // Resume value for the "approval" interrupt key
    },
}

// Pass the command through state
state := graph.State{
    graph.StateKeyCommand: cmd,
}

// Execute with resume command
events, err := executor.Execute(ctx, state, invocation)

// Resume merge rule:
// When resuming, if the caller provides initial state keys that do not start
// with an underscore ("_") and are not present in the restored checkpoint
// state, they will be merged into the execution state. Internal framework
// keys (prefixed with "_") are ignored for this merge.
```

#### Resume Helper Functions

```go
// Type-safe resume value extraction
if value, ok := graph.ResumeValue[string](ctx, state, "approval"); ok {
    // Use the resume value
}

// Resume with default value
value := graph.ResumeValueOrDefault(ctx, state, "approval", "no")

// Check if resume value exists
if graph.HasResumeValue(state, "approval") {
    // Handle resume case
}

// Clear resume values
graph.ClearResumeValue(state, "approval")
graph.ClearAllResumeValues(state)
```

### 2. Checkpoint-based Time Travel

Checkpoints enable "time travel" capabilities, allowing you to navigate through execution history and restore previous states. This is essential for debugging, auditing, and implementing sophisticated recovery strategies.

#### Checkpoint Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
    "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

// Create checkpoint saver (Memory or SQLite)
// Memory saver - good for development/testing
memorySaver := inmemory.NewSaver()

// SQLite saver - persistent storage for production
sqliteSaver, err := sqlite.NewCheckpointSaver("checkpoints.db")

// Create executor with checkpoint support
executor, err := graph.NewExecutor(compiledGraph,
    graph.WithCheckpointSaver(sqliteSaver),
    graph.WithCheckpointSaveTimeout(30*time.Second), // Configurable timeout
    graph.WithMaxSteps(100),
)
```

#### Checkpoint Lineage and Branching

```go
// Checkpoints form a lineage - a thread of execution
lineageID := "user-session-123"
namespace := "" // Optional namespace for branching
// Note: when namespace is empty (""), Latest/List/GetTuple perform cross-namespace
// queries within the same lineage. Use a concrete namespace to restrict scope.

// Create checkpoint configuration
config := graph.NewCheckpointConfig(lineageID).
    WithNamespace(namespace)

// Execute with checkpoint support
state := graph.State{
    "lineage_id": lineageID,
    "checkpoint_ns": namespace,
}

events, err := executor.Execute(ctx, state, invocation)
```

#### Checkpoint Management

```go
// Create checkpoint manager
manager := graph.NewCheckpointManager(saver)

// List all checkpoints for a lineage
checkpoints, err := manager.ListCheckpoints(ctx, config.ToMap(), &graph.CheckpointFilter{
    Limit: 10, // Results are ordered by timestamp (newest first)
})

// Get the latest checkpoint
// When namespace is empty (""), Latest searches across namespaces for the lineage
latest, err := manager.Latest(ctx, lineageID, namespace)
if latest != nil && latest.Checkpoint.IsInterrupted() {
    fmt.Printf("Workflow interrupted at: %s\n", latest.Checkpoint.InterruptState.NodeID)
}

// Get specific checkpoint by ID
ckptConfig := graph.CreateCheckpointConfig(lineageID, checkpointID, namespace)
tuple, err := manager.GetTuple(ctx, ckptConfig)

// Delete a lineage (all its checkpoints)
err = manager.DeleteLineage(ctx, lineageID)
```

#### Checkpoint Tree Visualization

```go
// Build checkpoint tree showing parent-child relationships
tree, err := manager.GetCheckpointTree(ctx, lineageID)

// Visualize the tree
for _, node := range tree {
    indent := strings.Repeat("  ", node.Level)
    marker := "📍"
    if node.Checkpoint.IsInterrupted() {
        marker = "🔴" // Interrupted checkpoint
    }
    fmt.Printf("%s%s %s (step=%d)\n", 
        indent, marker, node.ID[:8], node.Metadata.Step)
}
```

#### Resume from Specific Checkpoint

```go
// Resume from a specific checkpoint (time travel)
state := graph.State{
    "lineage_id": lineageID,
    "checkpoint_id": checkpointID, // Resume from this checkpoint
}

// The executor will load the checkpoint and continue from there
events, err := executor.Execute(ctx, state, invocation)
```

### 3. Checkpoint Storage Strategies

#### In-Memory Storage
Best for development and testing:
```go
saver := memory.NewCheckpointSaver()
```

#### SQLite Storage
Best for production with persistence:
```go
saver, err := sqlite.NewCheckpointSaver("workflow.db",
    sqlite.WithMaxConnections(10),
    sqlite.WithTimeout(30*time.Second),
)
```

#### Checkpoint Metadata
Each checkpoint stores:
- **State**: Complete workflow state at that point
- **Metadata**: Source (input/loop/interrupt), step number, timestamp
- **Parent ID**: Link to parent checkpoint for tree structure
- **Interrupt State**: If interrupted, contains node ID, task ID, and prompt
- **Next Nodes**: Nodes to execute when resuming
- **Channel Versions**: For Pregel-style execution
- **Pending Writes**: Uncommitted channel writes recorded and atomically stored
  with checkpoints to deterministically rebuild the frontier during resume
- **Versions Seen**: Per-node, per-channel version map used to avoid re-running
  a node if it has already observed the latest version of its trigger channels

### 4. Custom Reducer

Reducer defines how to merge state updates:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Default Reducer: Override existing values.
graph.DefaultReducer(existing, update) any

// Merge Reducer: Merge maps.
graph.MergeReducer(existing, update) any

// Append Reducer: Append to slices.
graph.AppendReducer(existing, update) any

// Message Reducer: Handle message arrays.
graph.MessageReducer(existing, update) any
```

### 5. Command Pattern

Nodes can return commands to simultaneously update state and specify routing:

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func routingNodeFunc(ctx context.Context, state graph.State) (any, error) {
    // Decide next step based on conditions.
    if shouldGoToA(state) {
        return &graph.Command{
            Update: graph.State{"status": "going_to_a"},
            GoTo:   "node_a",
        }, nil
    }

    return &graph.Command{
        Update: graph.State{"status": "going_to_b"},
        GoTo:   "node_b",
    }, nil
}
```

Fan-out and dynamic routing:

- Return `[]*graph.Command` from a node to create parallel branches that run in the next step.
- Using `Command{ GoTo: "target" }` dynamically routes to `target` at runtime; no static edge is required for reachability checks. Ensure the target node exists, and use `SetFinishPoint(target)` if it is terminal.

Example (fan-out with dynamic routing):

```go
stateGraph.AddNode("fanout", func(ctx context.Context, s graph.State) (any, error) {
    tasks := []*graph.Command{
        {Update: graph.State{"param": "A"}, GoTo: "worker"},
        {Update: graph.State{"param": "B"}, GoTo: "worker"},
        {Update: graph.State{"param": "C"}, GoTo: "worker"},
    }
    return tasks, nil
})

stateGraph.AddNode("worker", func(ctx context.Context, s graph.State) (any, error) {
    p, _ := s["param"].(string)
    if p == "" {
        return graph.State{}, nil
    }
    return graph.State{"results": []string{p}}, nil
})

// Entry/finish
stateGraph.SetEntryPoint("fanout")
stateGraph.SetFinishPoint("worker")

// No need to add a static edge fanout->worker; routing is driven by GoTo.
```

### 6. Executor Configuration

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

// Create executor with comprehensive configuration
executor, err := graph.NewExecutor(compiledGraph,
    graph.WithChannelBufferSize(1024),      // Event channel buffer size
    graph.WithMaxSteps(50),                  // Maximum execution steps
    graph.WithStepTimeout(5*time.Minute),    // Timeout per step
    graph.WithNodeTimeout(2*time.Minute),    // Timeout per node execution
    graph.WithCheckpointSaver(inmemory.NewSaver()),  // Enable checkpointing
    graph.WithCheckpointSaveTimeout(30*time.Second), // Checkpoint save timeout
)
```

### 7. Virtual Nodes and Routing

The Graph package uses virtual nodes to simplify workflow entry and exit:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Special node identifiers.
const (
    Start = "__start__"  // Virtual start node.
    End   = "__end__"    // Virtual end node.
)

// Set entry point (automatically creates edge from Start -> nodeID).
stateGraph.SetEntryPoint("first_node")

// Set finish point (automatically creates edge from nodeID -> End).
stateGraph.SetFinishPoint("last_node")

// No need to explicitly add these edges:
// stateGraph.AddEdge(Start, "first_node")  // Not needed.
// stateGraph.AddEdge("last_node", End)     // Not needed.
```

This design makes workflow definitions more concise, developers only need to focus on actual business nodes and their connections.

## Best Practices

### 1. State Management

- Use constants to define state keys, avoid hardcoded strings
- Create Helper functions for complex states
- Use Schema to validate state structure
- Distinguish between built-in state keys and user-defined state keys

```go
import (
    "errors"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

// Define user-defined state key constants.
const (
    StateKeyInput        = "input"          // User business data.
    StateKeyResult       = "result"         // Processing result.
    StateKeyProcessedData = "processed_data" // Processed data.
    StateKeyStatus       = "status"         // Processing status.
)

// User-accessible built-in state keys (use with caution).
// StateKeyUserInput    - User input (automatically set by GraphAgent).
// StateKeyLastResponse - Last response (read by Executor as final result).
// StateKeyMessages     - Message history (automatically updated by LLM nodes).
// StateKeyMetadata     - Metadata (general storage available to users).

// System internal state keys (users should not use directly).
// StateKeySession      - Session information (automatically set by GraphAgent).
// StateKeyExecContext  - Execution context (automatically set by Executor).
// StateKeyToolCallbacks - Tool callbacks (automatically set by Executor).
// StateKeyModelCallbacks - Model callbacks (automatically set by Executor).

// Create state Helper.
type StateHelper struct {
    state graph.State
}

func (h *StateHelper) GetInput() (string, error) {
    if input, ok := h.state[StateKeyInput].(string); ok {
        return input, nil
    }
    return "", errors.New("input not found")
}

func (h *StateHelper) GetUserInput() (string, error) {
    if input, ok := h.state[graph.StateKeyUserInput].(string); ok {
        return input, nil
    }
    return "", errors.New("user_input not found")
}
```

### 2. Error Handling

- Return meaningful errors in node functions
- Use error type constants for classification
- Handle exceptional cases in condition functions

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func safeNodeFunc(ctx context.Context, state graph.State) (any, error) {
    input, ok := state["input"].(string)
    if !ok {
        return nil, fmt.Errorf("input field not found or wrong type")
    }

    if input == "" {
        return nil, fmt.Errorf("input cannot be empty")
    }

    // Processing logic...
    return result, nil
}
```

### 3. Performance Optimization

- Reasonably set executor buffer size
- Use maximum step limits to prevent infinite loops
- Consider parallel execution paths (if supported)

### 4. Testing

```go
import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestWorkflow(t *testing.T) {
    // Create test graph.
    graph := createTestGraph()

    // Create executor.
    executor, err := graph.NewExecutor(graph)
    require.NoError(t, err)

    // Execute test.
    initialState := graph.State{"test_input": "test"}
    eventChan, err := executor.Execute(context.Background(), initialState, nil)
    require.NoError(t, err)

    // Verify results.
    for event := range eventChan {
        // Verify event content.
        assert.NotNil(t, event)
    }
}
```

## Common Use Cases

### 1. Document Processing Workflow

This is a complete document processing workflow example, demonstrating how to use GraphAgent and Runner:

```go
package main

import (
    "context"
    "fmt"
    "strings"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// User-defined state keys.
const (
    StateKeyDocumentLength  = "document_length"
    StateKeyWordCount       = "word_count"
    StateKeyComplexityLevel = "complexity_level"
    StateKeyProcessingStage = "processing_stage"
)

type documentWorkflow struct {
    modelName string
    runner    runner.Runner
    userID    string
    sessionID string
}

func (w *documentWorkflow) setup() error {
    // 1. Create document processing graph.
    workflowGraph, err := w.createDocumentProcessingGraph()
    if err != nil {
        return fmt.Errorf("failed to create graph: %w", err)
    }

    // 2. Create GraphAgent.
    graphAgent, err := graphagent.New("document-processor", workflowGraph,
        graphagent.WithDescription("Comprehensive document processing workflow"),
        graphagent.WithInitialState(graph.State{}),
    )
    if err != nil {
        return fmt.Errorf("failed to create graph agent: %w", err)
    }

    // 3. Create session service.
    sessionService := inmemory.NewSessionService()

    // 4. Create Runner.
    w.runner = runner.NewRunner(
        "document-workflow",
        graphAgent,
        runner.WithSessionService(sessionService),
    )

    // 5. Set identifiers.
    w.userID = "user"
    w.sessionID = fmt.Sprintf("workflow-session-%d", time.Now().Unix())

    return nil
}

func (w *documentWorkflow) createDocumentProcessingGraph() (*graph.Graph, error) {
    // Create state schema.
    schema := graph.MessagesStateSchema()

    // Create model instance.
    modelInstance := openai.New(w.modelName)

    // Create analysis tool.
    complexityTool := function.NewFunctionTool(
        w.analyzeComplexity,
        function.WithName("analyze_complexity"),
        function.WithDescription("Analyze document complexity level"),
    )

    // Create state graph.
    stateGraph := graph.NewStateGraph(schema)
    tools := map[string]tool.Tool{
        "analyze_complexity": complexityTool,
    }

    // Build workflow graph.
    stateGraph.
        AddNode("preprocess", w.preprocessDocument).
        AddLLMNode("analyze", modelInstance,
            `You are a document analysis expert. Analyze the provided document and:
1. Classify document type and complexity (simple, moderate, complex)
2. Extract key themes
3. Evaluate content quality
Use the analyze_complexity tool for detailed analysis.
Only return complexity level: "simple" or "complex".`,
            tools).
        AddToolsNode("tools", tools).
        AddNode("route_complexity", w.routeComplexity).
        AddLLMNode("summarize", modelInstance,
            `You are a document summarization expert. Create a comprehensive and concise summary of the document.
Focus on:
1. Key points and main arguments
2. Important details and insights
3. Logical structure and flow
4. Conclusions and implications
Provide a well-structured summary that preserves important information.
Remember: Only output the final result itself, no other text.`,
            map[string]tool.Tool{}).
        AddLLMNode("enhance", modelInstance,
            `You are a content enhancement expert. Improve the provided content by:
1. Improving clarity and readability
2. Improving structure and organization
3. Adding relevant details where appropriate
4. Ensuring consistency and coherence
Focus on making content more engaging and professional while maintaining the original meaning.
Remember: Only output the final result itself, no other text.`,
            map[string]tool.Tool{}).
        AddNode("format_output", w.formatOutput).
        SetEntryPoint("preprocess").
        SetFinishPoint("format_output")

    // Add workflow edges.
    stateGraph.AddEdge("preprocess", "analyze")
    stateGraph.AddToolsConditionalEdges("analyze", "tools", "route_complexity")
    stateGraph.AddEdge("tools", "analyze")

    // Add complexity conditional routing.
    stateGraph.AddConditionalEdges("route_complexity", w.complexityCondition, map[string]string{
        "simple":  "enhance",
        "complex": "summarize",
    })

    stateGraph.AddEdge("enhance", "format_output")
    stateGraph.AddEdge("summarize", "format_output")

    // SetEntryPoint and SetFinishPoint automatically handle connections with virtual Start/End nodes.

    return stateGraph.Compile()
}

// Node function implementations.
func (w *documentWorkflow) preprocessDocument(ctx context.Context, state graph.State) (any, error) {
    var input string
    if userInput, ok := state[graph.StateKeyUserInput].(string); ok {
        input = userInput
    }
    if input == "" {
        return nil, fmt.Errorf("no input document found")
    }

    input = strings.TrimSpace(input)
    if len(input) < 10 {
        return nil, fmt.Errorf("document too short for processing (minimum 10 characters)")
    }

    return graph.State{
        StateKeyDocumentLength:  len(input),
        StateKeyWordCount:       len(strings.Fields(input)),
        graph.StateKeyUserInput: input,
        StateKeyProcessingStage: "preprocessing",
    }, nil
}

func (w *documentWorkflow) routeComplexity(ctx context.Context, state graph.State) (any, error) {
    return graph.State{
        StateKeyProcessingStage: "complexity_routing",
    }, nil
}

func (w *documentWorkflow) complexityCondition(ctx context.Context, state graph.State) (string, error) {
    if msgs, ok := state[graph.StateKeyMessages].([]model.Message); ok {
        if len(msgs) > 0 {
            lastMsg := msgs[len(msgs)-1]
            if strings.Contains(strings.ToLower(lastMsg.Content), "simple") {
                return "simple", nil
            }
        }
    }
    return "complex", nil
}

func (w *documentWorkflow) formatOutput(ctx context.Context, state graph.State) (any, error) {
    var result string
    if lastResponse, ok := state[graph.StateKeyLastResponse].(string); ok {
        result = lastResponse
    }

    finalOutput := fmt.Sprintf(`DOCUMENT PROCESSING RESULTS
========================
Processing Stage: %s
Document Length: %d characters
Word Count: %d words
Complexity Level: %s

Processed Content:
%s
`,
        state[StateKeyProcessingStage],
        state[StateKeyDocumentLength],
        state[StateKeyWordCount],
        state[StateKeyComplexityLevel],
        result,
    )

    return graph.State{
        graph.StateKeyLastResponse: finalOutput,
    }, nil
}

// Tool function.
func (w *documentWorkflow) analyzeComplexity(ctx context.Context, args map[string]any) (any, error) {
    text, ok := args["text"].(string)
    if !ok {
        return nil, fmt.Errorf("text argument is required")
    }

    wordCount := len(strings.Fields(text))
    sentenceCount := len(strings.Split(text, "."))

    var level string
    var score float64

    if wordCount < 100 {
        level = "simple"
        score = 0.3
    } else if wordCount < 500 {
        level = "moderate"
        score = 0.6
    } else {
        level = "complex"
        score = 0.9
    }

    return map[string]any{
        "level":          level,
        "score":          score,
        "word_count":     wordCount,
        "sentence_count": sentenceCount,
    }, nil
}

// Execute workflow.
func (w *documentWorkflow) processDocument(ctx context.Context, content string) error {
    message := model.NewUserMessage(content)
    eventChan, err := w.runner.Run(ctx, w.userID, w.sessionID, message)
    if err != nil {
        return fmt.Errorf("failed to run workflow: %w", err)
    }
    return w.processStreamingResponse(eventChan)
}

func (w *documentWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
    var workflowStarted bool
    var finalResult string

    for event := range eventChan {
        if event.Error != nil {
            fmt.Printf("❌ Error: %s\n", event.Error.Message)
            continue
        }

        if len(event.Response.Choices) > 0 {
            choice := event.Response.Choices[0]
            if choice.Delta.Content != "" {
                if !workflowStarted {
                    fmt.Print("🤖 Workflow: ")
                    workflowStarted = true
                }
                fmt.Print(choice.Delta.Content)
            }

            if choice.Message.Content != "" && event.Done {
                finalResult = choice.Message.Content
            }
        }

        if event.Done {
            if finalResult != "" && strings.Contains(finalResult, "DOCUMENT PROCESSING RESULTS") {
                fmt.Printf("\n\n%s\n", finalResult)
            }
            break
        }
    }
    return nil
}
```

### 2. Chat Bot

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// Create chat bot.
func createChatBot(modelName string) (*runner.Runner, error) {
    // Create state graph.
    stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())

    // Create model and tools.
    modelInstance := openai.New(modelName)
    tools := map[string]tool.Tool{
        "calculator": calculatorTool,
        "search":     searchTool,
    }

    // Build conversation graph.
    stateGraph.
        AddLLMNode("chat", modelInstance,
            `You are a helpful AI assistant. Provide help based on user questions and use tools when needed.`,
            tools).
        AddToolsNode("tools", tools).
        AddToolsConditionalEdges("chat", "tools", "chat").
        SetEntryPoint("chat").
        SetFinishPoint("chat")

    // Compile graph.
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        return nil, err
    }

    // Create GraphAgent.
    graphAgent, err := graphagent.New("chat-bot", compiledGraph,
        graphagent.WithDescription("Intelligent chat bot"),
        graphagent.WithInitialState(graph.State{}),
    )
    if err != nil {
        return nil, err
    }

    // Create Runner.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "chat-bot-app",
        graphAgent,
        runner.WithSessionService(sessionService),
    )

    return appRunner, nil
}
```

### 3. Data Processing Pipeline

```go
import (
    "reflect"

    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// Create data processing pipeline.
func createDataPipeline() (*runner.Runner, error) {
    // Create custom state schema.
    schema := graph.NewStateSchema()
    schema.AddField("data", graph.StateField{
        Type:    reflect.TypeOf([]any{}),
        Reducer: graph.AppendReducer,
        Default: func() any { return []any{} },
    })
    schema.AddField("quality_score", graph.StateField{
        Type:    reflect.TypeOf(0.0),
        Reducer: graph.DefaultReducer,
    })

    // Create state graph.
    stateGraph := graph.NewStateGraph(schema)

    // Build data processing pipeline.
    stateGraph.
        AddNode("extract", extractData).
        AddNode("validate", validateData).
        AddConditionalEdges("validate", routeByQuality, map[string]string{
            "high":   "transform",
            "medium": "clean",
            "low":    "reject",
        }).
        AddNode("clean", cleanData).
        AddNode("transform", transformData).
        AddNode("load", loadData).
        AddEdge("clean", "transform").
        AddEdge("transform", "load").
        SetEntryPoint("extract").
        SetFinishPoint("load")

    // Compile graph.
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        return nil, err
    }

    // Create GraphAgent.
    graphAgent, err := graphagent.New("data-pipeline", compiledGraph,
        graphagent.WithDescription("Data processing pipeline"),
        graphagent.WithInitialState(graph.State{}),
    )
    if err != nil {
        return nil, err
    }

    // Create Runner.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "data-pipeline-app",
        graphAgent,
        runner.WithSessionService(sessionService),
    )

    return appRunner, nil
}
```

### 4. GraphAgent as SubAgent

GraphAgent can be used as a sub-agent of other Agents, implementing complex multi-Agent collaboration:

```go
import (
    "context"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

// Create document processing GraphAgent.
func createDocumentProcessor() (agent.Agent, error) {
    // Create document processing graph.
    stateGraph := graph.NewStateGraph(graph.MessagesStateSchema())

    // Add document processing nodes.
    stateGraph.
        AddNode("preprocess", preprocessDocument).
        AddLLMNode("analyze", modelInstance, analysisPrompt, tools).
        AddNode("format", formatOutput).
        SetEntryPoint("preprocess").
        SetFinishPoint("format")

    // Compile graph.
    compiledGraph, err := stateGraph.Compile()
    if err != nil {
        return nil, err
    }

    // Create GraphAgent.
    return graphagent.New("document-processor", compiledGraph,
        graphagent.WithDescription("Professional document processing workflow"),
    )
}

// Create coordinator Agent, using GraphAgent as sub-agent.
func createCoordinatorAgent() (agent.Agent, error) {
    // Create document processing GraphAgent.
    documentProcessor, err := createDocumentProcessor()
    if err != nil {
        return nil, err
    }

    // Create other sub-agents.
    mathAgent := llmagent.New("math-agent",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("Mathematical calculation expert"),
        llmagent.WithTools([]tool.Tool{calculatorTool}),
    )

    // Create coordinator Agent.
    coordinator := llmagent.New("coordinator",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("Task coordinator, can delegate to professional sub-agents"),
        llmagent.WithInstruction(`You are a coordinator that can delegate tasks to professional sub-agents:
- document-processor: Document processing and analysis
- math-agent: Mathematical calculations and formula processing

Choose appropriate sub-agents based on user needs to handle tasks.`),
        llmagent.WithSubAgents([]agent.Agent{
            documentProcessor,  // GraphAgent as sub-agent.
            mathAgent,
        }),
    )

    return coordinator, nil
}

// Usage example.
func main() {
    // Create coordinator Agent.
    coordinator, err := createCoordinatorAgent()
    if err != nil {
        log.Fatal(err)
    }

    // Create Runner.
    runner := runner.NewRunner("coordinator-app", coordinator)

    // Execute task (coordinator will automatically choose appropriate sub-agent).
    message := model.NewUserMessage("Please analyze this document and calculate the statistical data in it")
    eventChan, err := runner.Run(ctx, userID, sessionID, message)
    // ...
}
```

**Key Features**:

- GraphAgent implements the `agent.Agent` interface and can be used as a sub-agent by other Agents
- Coordinator Agents can delegate tasks to GraphAgent through the `transfer_to_agent` tool or custom logic
- GraphAgent can in turn delegate to registered sub-agents through `graphagent.WithSubAgents` + `AddAgentNode`
- This design enables seamless, bi-directional integration between complex workflows and multi-Agent systems

## Troubleshooting

### Common Errors

1. **"node not found"**: Check if node ID is correct
2. **"invalid graph"**: Ensure graph has entry point and all nodes are reachable
3. **"maximum execution steps exceeded"**: Check for loops or increase maximum steps
4. **"state validation failed"**: Check state schema definition

### Debugging Tips

- Use event streams to monitor execution process
- Add logs in node functions
- Validate state schema definitions
- Check condition function logic

## Summary

The Graph package provides a powerful and flexible workflow orchestration system, particularly suitable for building complex AI applications. Through the combined use of GraphAgent and Runner, you can create efficient and maintainable workflow applications.

### Key Points

**Workflow Creation**:

- Use `StateGraph` builder to create graph structure
- Define clear state schemas and data flow
- Reasonably use conditional routing and tool nodes

**Application Integration**:

- Wrap workflow graphs through `GraphAgent`
- Use `Runner` to manage sessions and execution environment
- Handle streaming events and error responses

**Agent Integration**:

- GraphAgent implements the `agent.Agent` interface
- Can be used as a sub-agent of other Agents
- Can also orchestrate other agents via `graphagent.WithSubAgents` and `AddAgentNode`
- Supports complex multi-Agent collaboration scenarios

**Best Practices**:

- Use type-safe state key constants
- Implement appropriate error handling and recovery mechanisms
- Test and monitor workflow execution process
- Reasonably configure executor parameters and buffer size
- Consider encapsulating complex workflows as GraphAgent sub-agents

### Typical Usage Flow

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
    "trpc.group/trpc-go/trpc-agent-go/graph"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 1. Create and compile graph.
stateGraph := graph.NewStateGraph(schema)
// ... Add nodes and edges.
compiledGraph, err := stateGraph.Compile()

// 2. Create GraphAgent.
graphAgent, err := graphagent.New("workflow-name", compiledGraph, opts...)

// 3. Create Runner.
appRunner := runner.NewRunner("app-name", graphAgent, runnerOpts...)

// 4. Execute workflow.
message := model.NewUserMessage("User input")
eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
```

This pattern makes the Graph package particularly suitable for building enterprise-level AI workflow applications, providing good scalability, maintainability, and user experience.
### Runtime Isolation (Executor vs ExecutionContext)

- Executor is reusable and safe for concurrent runs. It intentionally does not store per-run mutable state.
- All per-run artifacts (e.g., restored checkpoint metadata, versions seen, pending writes) are carried inside an ExecutionContext instance created for that run.
- Functions like resumeFromCheckpoint only read from the checkpoint store and reconstruct state; they do not mutate the Executor. Callers pass any needed checkpoint-derived data into the ExecutionContext used for that run.
- Completion event serialization operates on a deep-copied snapshot and skips non-serializable/internal keys to avoid data races and reduce payload size.
