# Parallel Fan-out Graph Example

This example demonstrates **parallel fan-out execution** using the `trpc-agent-go` library. It shows how a single node can return multiple `[]*graph.Command` results that execute the same target node in parallel with different parameters, similar to LangGraph's "Send" functionality.

## Overview

The parallel fan-out workflow demonstrates dynamic task distribution:

1. **Fan-out Node** - Returns `[]*graph.Command` to create multiple parallel tasks
2. **Parallel Execution** - Multiple tasks execute the same worker node simultaneously
3. **Parameter Isolation** - Each task has isolated `Overlay` state parameters
4. **Result Merging** - Results from parallel tasks are merged using `StateSchema` reducers
5. **Dynamic Scaling** - Number of parallel tasks can be determined at runtime

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
- **Parameter Isolation**: Each task has independent `Overlay` state
- **Concurrent Processing**: Tasks run in parallel for better performance
- **Result Aggregation**: Results are merged using custom reducers

### 🔍 State Management with Overlays

- **Global State**: Shared state across all tasks
- **Overlay State**: Task-specific parameters that don't affect global state
- **Reducer Functions**: Custom logic for merging parallel results
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

- **`Update`**: `Overlay` state with task-specific parameters (includes `priority` in this example)
- **`GoTo`**: Target node identifier

### **Execution Flow**

1. **Fan-out Node** returns `[]*graph.Command`
2. **Executor** creates multiple `Task` objects with `Overlay` states
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
    })
```

## Usage

### Prerequisites

- Go 1.24 or later
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

📋 Creating parallel tasks...
✅ Task A (priority: high) created
✅ Task B (priority: medium) created
✅ Task C (priority: low) created

🔄 Executing parallel tasks...
⏱️  Task A completed in 45ms
⏱️  Task B completed in 52ms
⏱️  Task C completed in 48ms

📊 Aggregated Results:
   - task-A (priority: high)
   - task-B (priority: medium)
   - task-C (priority: low)

🎯 Total execution time: 52ms (parallel vs 145ms sequential)
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
- **State Isolation**: `Overlay` state prevents parameter conflicts

### **Error Handling**

- **Task-level Errors**: Individual task failures don't stop others
- **Partial Results**: Successful tasks contribute to final results
- **Error Aggregation**: Failed tasks are logged and reported

### **Resource Management**

- **Memory Efficiency**: Shared state with minimal duplication
- **Goroutine Management**: Controlled concurrency with task limits
- **Cleanup**: Proper resource cleanup after task completion

This example provides a foundation for building scalable, parallel workflows that can dynamically distribute work across multiple execution paths while maintaining clean state management and result aggregation.
