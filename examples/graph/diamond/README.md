# Diamond Pattern Workflow Example

This example demonstrates a diamond pattern workflow that exposes the need for per-node version tracking (`versions_seen`) in graph execution. Without proper `versions_seen` implementation, nodes that receive input from multiple sources (like the aggregator node in this example) will execute multiple times instead of once.

## Overview

The diamond pattern is a common workflow structure where:
1. A single node (splitter) fans out work to multiple parallel nodes
2. These parallel nodes (analyzer1, analyzer2) process independently
3. All parallel results converge to a single aggregation node
4. The aggregated results proceed to final processing

### Graph Structure

```
        splitter
        /      \
   analyzer1  analyzer2
        \      /
       aggregator
           |
         final
```

## The Problem This Example Exposes

Without proper per-node version tracking:
- The aggregator node executes **twice** (once after each analyzer completes)
- This leads to redundant processing and potential state inconsistencies
- The issue becomes more severe with more parallel branches

With proper `versions_seen` implementation:
- The aggregator node would execute **once** after all analyzers complete
- This ensures correct fan-in behavior and optimal performance

## Features

- **Diamond Pattern Graph**: Demonstrates splitter → parallel processing → aggregator flow
- **Execution Tracking**: Monitors and reports how many times each node executes
- **Issue Detection**: Automatically detects and highlights redundant aggregator executions
- **Interactive CLI**: Test the workflow with different inputs
- **State Management**: Shows proper state handling across parallel branches

## Usage

### Basic Execution

```bash
go run .
```

### Interactive Commands

Once running, use these commands:

- `run [input]` - Execute the workflow with optional input data
- `reset` - Reset execution counters
- `help` - Show available commands
- `exit` or `quit` - Exit the program

### Example Session

```bash
> run test-data
🚀 Starting workflow with input: test-data
🔄 [1] SPLITTER: Processing input: test-data
🔬 [1] ANALYZER1: Processing: A1-test-data
🔬 [1] ANALYZER2: Processing: A2-test-data
⚠️  [1] AGGREGATOR: Processing 1 results
   - Result 1: Result1[A1-test-data]
⚠️  [2] AGGREGATOR: Processing 2 results
❌ ISSUE DETECTED: Aggregator executed multiple times!
   Without versions_seen, aggregator runs once per analyzer update.
   With proper versions_seen, it would run only once after both complete.
   - Result 1: Result1[A1-test-data]
   - Result 2: Result2[A2-test-data]

📊 [1] FINAL: Workflow Complete
Results collected: [Result1[A1-test-data] Result2[A2-test-data]]

🔍 Execution Analysis:
✅ splitter: 1 execution(s)
✅ analyzer1: 1 execution(s)
✅ analyzer2: 1 execution(s)
❌ aggregator: 2 executions (expected: 1) - REDUNDANT EXECUTION!
✅ final: 1 execution(s)

💡 Solution: Implement versions_seen to track per-node channel versions.

⏱️  Execution time: 250ms
```

## Implementation Details

### Node Functions

1. **Splitter Node**: 
   - Receives initial input
   - Distributes work to both analyzer branches
   - Creates separate state keys for each analyzer

2. **Analyzer Nodes** (analyzer1, analyzer2):
   - Process their respective input data independently
   - Simulate different processing times (100ms vs 150ms)
   - Append results to a shared results array

3. **Aggregator Node**:
   - Receives results from both analyzers
   - **Key observation point**: Executes multiple times without `versions_seen`
   - Tracks and reports its execution count

4. **Final Node**:
   - Displays complete results
   - Provides execution analysis
   - Highlights any redundant executions

### State Management

The workflow uses several state keys:
- `input`: Initial workflow input
- `analysis1_data`: Data for analyzer1
- `analysis2_data`: Data for analyzer2
- `results`: Accumulated results (uses AppendReducer)
- `execution_counts`: Tracks node execution counts

### Execution Tracking

The example includes built-in execution tracking to demonstrate the issue:
- Each node records its execution count
- Thread-safe counters using mutex protection
- Final analysis shows expected vs actual execution counts
- Automatic detection of redundant aggregator executions

## Key Learnings

1. **Fan-in Pattern Challenge**: Nodes receiving input from multiple sources need special handling to execute only once after all inputs are ready.

2. **Version Tracking Importance**: Per-node version tracking (`versions_seen`) is crucial for correct graph execution in diamond and similar patterns.

3. **State Reducers**: The example uses `AppendReducer` for the results field to accumulate outputs from parallel branches.

4. **Timing Independence**: The analyzers have different processing times, ensuring they complete at different moments and expose the aggregator execution issue.

## Solution

The proper solution involves implementing `versions_seen` tracking that:
- Records which channel versions each node has processed
- Ensures nodes with multiple inputs wait for all inputs before executing
- Prevents redundant executions while maintaining correctness

This example serves as a test case for validating proper fan-in behavior in graph execution engines.

## Related Examples

- `examples/graph/basic` - Simple sequential graph execution
- `examples/graph/parallel` - Parallel execution without convergence
- `examples/graph/checkpoint` - State persistence and recovery
- `examples/graph/interrupt` - Handling workflow interruptions