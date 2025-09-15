# Diamond Pattern Workflow Example

This example demonstrates a diamond pattern workflow with correct fan-in behavior, per-node version tracking ("versions_seen") on resume, and a barrier at the aggregator so it emits to the final node only after both branches complete.

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

## What This Example Demonstrates

- Correct fan-in with a barrier: aggregator routes to `final` only when both analyzer results are present.
- Proper result accumulation from parallel branches using a string-slice reducer.
- Checkpointing + resume: when resuming, per-node `versions_seen` avoids redundant executions if nothing new has arrived.

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

- `run <lineage> [input]` — Run with a fixed `lineage_id` and optional input
- `resume <lineage> [ck]` — Resume latest or specific checkpoint for a lineage
- `list <lineage> [n]`    — List latest n checkpoints (default 5)
- `reset`                 — Reset execution counters
- `help`                  — Show available commands
- `exit|quit`             — Exit the program

### Example Session

```bash
> run demo hello
🚀 Starting workflow with input: hello
🔄 [1] SPLITTER: Processing input: hello
🔬 [1] ANALYZER2: Processing: A2-hello
🔬 [1] ANALYZER1: Processing: A1-hello
⚠️  [1] AGGREGATOR: Processing 2 results
   - Result 1: Result1[A1-hello]
   - Result 2: Result2[A2-hello]

📊 [1] FINAL: Workflow Complete
Results collected: [Result1[A1-hello] Result2[A2-hello]]

🔍 Execution Analysis:
✅ splitter: 1 execution(s)
✅ analyzer1: 1 execution(s)
✅ analyzer2: 1 execution(s)
✅ aggregator: 1 execution(s)
✅ final: 1 execution(s)

⏱️  Execution time: ~150ms
```

### Checkpoint + Resume

```bash
> run demo hello
> list demo 5
 1. id=... step=3 time=... next=[]
 2. id=... step=2 time=... next=[final]
 3. id=... step=1 time=... next=[aggregator]
 4. id=... step=0 time=... next=[analyzer1 analyzer2]
 5. id=... step=-1 time=... next=[splitter]
> resume demo
🔁 Resuming workflow (lineage=demo, checkpoint=latest)
⏱️  Execution time: 1ms
```

On resume with no new updates, `versions_seen` prevents redundant executions.

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

3. **State Reducers**: The example uses `StringSliceReducer` for the `results` field to accumulate outputs from parallel branches.

4. **Timing Independence**: The analyzers have different processing times, ensuring they complete at different moments and expose the aggregator execution issue.

## Implementation Notes

- The aggregator has a conditional edge that routes to `final` only when both results are present; otherwise it routes to the special `End` node (no-op). This acts as a barrier to avoid premature routing.
- The executor persists checkpoints at each step and carries forward `versions_seen` so that, upon resume, nodes only re-execute if they have not seen the latest version(s) of their triggering channels.

This example serves as a test case for validating correct fan-in behavior plus checkpoint/resume semantics with per-node version tracking.

## Related Examples

- `examples/graph/basic` - Simple sequential graph execution
- `examples/graph/parallel` - Parallel execution without convergence
- `examples/graph/checkpoint` - State persistence and recovery
- `examples/graph/interrupt` - Handling workflow interruptions
