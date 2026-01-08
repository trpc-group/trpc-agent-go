# Parallel Fan-out Graph Example

This example demonstrates **parallel fan-out execution** using the `trpc-agent-go` library. It shows how a single node can return multiple `[]*graph.Command` results that execute the same target node in parallel with different parameters, similar to LangGraph's "Send" functionality.

## Overview

The parallel fan-out workflow demonstrates dynamic task distribution:

1. **Fan-out Node** - Returns `[]*graph.Command` to create multiple parallel tasks
2. **Parallel Execution** - Multiple tasks execute the same worker node simultaneously
3. **Task-specific State** - Each task runs with its own state snapshot (global + command update)
4. **Result Merging** - Results from parallel tasks are merged using `StateSchema` reducers
5. **Dynamic Scaling** - Number of parallel tasks can be determined at runtime
6. **Readable Output** - Only the planner streams; after completion, results are replayed sequentially for clarity

## Key Features

### 🔄 Dynamic Task Distribution

The example demonstrates how to create multiple parallel tasks dynamically:

```go
// Fan-out node returns multiple commands
cmds := []*graph.Command{
    &graph.Command{
        Update: graph.State{"param": "task-A", "priority": "high"},
        GoTo:   "worker",
    },
    &graph.Command{
        Update: graph.State{"param": "task-B", "priority": "medium"},
        GoTo:   "worker",
    },
    &graph.Command{
        Update: graph.State{"param": "task-C", "priority": "low"},
        GoTo:   "worker",
    },
}
return cmds, nil
```

### 📊 Parallel Task Execution

- **Same Target Node**: Multiple tasks execute the same worker node
- **Task-specific State**: Each task has an independent state snapshot (global + command update)
- **Concurrent Processing**: Tasks run in parallel for better performance
- **Result Aggregation**: Results are merged using custom reducers

### 🔍 State Management

- **Global State**: Shared baseline state across tasks
- **Per-task Snapshot**: Task-specific parameters from `Command.Update` are merged into a per-task snapshot (isolated from other tasks)
- **Reducer Functions**: Custom logic for merging parallel results back into the global state
- **Type Safety**: Strong typing with `StateSchema` definitions

## Graph Structure

```
fanout → [Command1, Command2, Command3]
              ↓           ↓           ↓
          worker      worker      worker
              ↓           ↓           ↓
          [result1]   [result2]   [result3]
              ↓           ↓           ↓
              └───────────┼───────────┘
                          ↓
                    merged_results
```

## Architecture

This example uses the GraphAgent and Runner architecture: GraphAgent wraps the
compiled graph and manages state, while Runner handles sessions and streaming
execution.

### **Task Structure**

Each `Command` contains:

- **`Update`**: Task-specific parameters merged into a per-task state snapshot (includes `priority` in this example)
- **`GoTo`**: Target node identifier

### **Execution Flow**

1. **Fan-out Node** returns `[]*graph.Command`
2. **Executor** creates multiple `Task` objects with per-task merged state snapshots
3. **Parallel Execution** of tasks targeting the same worker node
4. **State Merging** using `StateSchema.ApplyUpdate` and reducers
5. **Result Aggregation** from all parallel tasks

### **State Schema with Reducers**

```go
schema := graph.MessagesStateSchema().
    AddField("results", graph.StateField{
        Type:    reflect.TypeOf([]string{}),
        Reducer: graph.StringSliceReducer,  // Merges string slices
        Default: func() any { return []string{} },
    }).
    AddField("node_execution_history", graph.StateField{
        Type:    reflect.TypeOf([]map[string]any{}),
        Reducer: appendMapSliceReducer,   // Appends []map[string]any across branches
        Default: func() any { return []map[string]any{} },
    }).
    AddField("error_count", graph.StateField{
        Type:    reflect.TypeOf(0),
        Reducer: intSumReducer,           // Sums errors across branches
        Default: func() any { return 0 },
    })
```

## Usage

### Prerequisites

- Go 1.21 or later
- Access to an LLM model (optional, for enhanced examples)
- Valid API key configured for the selected model

### Running the Example

```bash
# Navigate to the fanout example directory
cd trpc-agent-go/examples/graph/fanout

# Run the fan-out example
go run main.go

# Default model is "deepseek-chat"; override with -model to change
# (Optional) Specify model
go run main.go -model gpt-4o-mini
```

### Interactive Mode

The example demonstrates:

1. **Dynamic Task Creation** - Generate tasks based on input parameters
2. **Parallel Execution** - Execute multiple worker instances simultaneously
3. **Parameter Variation** - Different parameters for each parallel task
4. **Result Collection** - Aggregate results from all parallel executions
5. **Performance Monitoring** - Track execution times and task distribution

## Example Scenarios

### **Basic Fan-out**

- Creates 3 parallel tasks with different priorities
- Each task processes with isolated parameters
- Results are merged into a single collection

### **Dynamic Scaling**

- Number of tasks determined at runtime
- Task parameters generated dynamically
- Configurable worker node behavior

### **Priority-based Processing**

- High priority tasks processed first
- Medium and low priority tasks follow
- Results maintain execution order

## Expected Output

```
🚀 Parallel Fan-out Execution Example

🤖 LLM Streaming: 2
🧭 Planner decided to run 2 tasks
📋 Creating 2 parallel tasks...
✅ task-A (priority: high) created
✅ task-B (priority: medium) created

🔄 Executing 2 parallel tasks...

🧵 Replaying results sequentially:

[1/2] task-A (priority: high)
... worker A content ...

[2/2] task-B (priority: medium)
... worker B content ...
```

## Benefits

### **Performance**

- **Parallel Execution**: Multiple tasks run simultaneously
- **Reduced Latency**: Total time equals slowest task, not sum of all
- **Resource Utilization**: Better CPU and I/O utilization

### **Scalability**

- **Dynamic Task Creation**: Number of tasks can vary based on input
- **Load Distribution**: Work can be distributed across multiple workers
- **Flexible Routing**: Tasks can target different nodes as needed

### **Maintainability**

- **Code Reuse**: Same worker node handles multiple task types
- **Parameter Isolation**: Task-specific state doesn't interfere
- **Result Aggregation**: Built-in mechanisms for combining results

## Use Cases

### **Content Processing**

- Multiple documents processed in parallel
- Different processing strategies for each document
- Aggregated results from all processing paths

### **API Aggregation**

- Multiple API calls executed simultaneously
- Different parameters for each API request
- Combined responses from all endpoints

### **Data Pipeline**

- Multiple data sources processed in parallel
- Different transformation rules for each source
- Merged datasets from all processing streams

### **Workflow Orchestration**

- Multiple workflow branches executed concurrently
- Different business logic for each branch
- Consolidated outcomes from all paths

## Technical Details

### **Concurrency Control**

- **Task Queue**: `pendingTasks` queue manages parallel task execution
- **Mutex Protection**: Separate mutexes for global state and task queue
- **State Isolation**: Per-task state snapshots prevent parameter conflicts
- **Streaming UX**: Only the planner streams; worker results are replayed sequentially after completion to avoid interleaved logs

### **Error Handling**

- **Task-level Errors**: Individual task failures don't stop others
- **Partial Results**: Successful tasks contribute to final results
- **Error Aggregation**: Failed tasks are logged and reported

### **Resource Management**

- **Memory Efficiency**: Shared state with minimal duplication
- **Goroutine Management**: Controlled concurrency with task limits
- **Cleanup**: Proper resource cleanup after task completion

This example provides a foundation for building scalable, parallel workflows that can dynamically distribute work across multiple execution paths while maintaining clean state management and result aggregation.

## Notes on Routing and Events

- **Routing via `GoTo`**: When commands specify `GoTo`, you don’t need a static `AddEdge` from the fan-out node to the worker — routing is explicit in the command.
- **Channel correctness**: Fan-out task writers/triggers are derived from the target node, ensuring correct downstream triggering without duplicates.
- **Execution stats and errors**: The example’s callbacks persist `node_execution_history` and `error_count` in state on successful nodes, enabling the aggregator to display execution flow and error counts in the final output.
- **Planner tool usage & parsing**: The planning node must call `analyze_task_complexity` first, then output a single integer (1–5). The example includes a robust parser that extracts a valid number even if minor formatting (e.g., `**2**`) appears.
