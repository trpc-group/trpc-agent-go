# Advanced Checkpoint Example

This example demonstrates comprehensive checkpoint functionality in the trpc-agent-go graph package, showing how the checkpoint system enables workflow resumption, time-travel debugging, branching, and fault tolerance.

## Overview

The example showcases the full capabilities of the checkpoint system:

- **Automatic checkpoint creation** during graph execution
- **Checkpoint restoration** to resume workflows from any point
- **Branching** to create alternative execution paths
- **Lineage management** for conversation/workflow threads
- **History tracking** to view complete execution timelines
- **Multiple storage backends** supporting in-memory and SQLite
- **Namespace-based organization** for checkpoint isolation

## Architecture

### Workflow Graph Structure

The example implements a 4-node workflow demonstrating checkpoint features:

```
increment1 -> increment2 -> increment3 -> final
```

- **increment1/2/3**: Increment a counter and track execution
- **final**: Complete the workflow with summary

### State Management

The workflow maintains several state fields:
- `counter`: Incremental value showing workflow progress
- `messages`: Log of all operations performed
- `step_count`: Total number of steps executed
- `last_action`: Most recent node executed

## Usage

### Building and Running

```bash
# Build the example
go build .

# Run with default settings (in-memory storage)
./checkpoint

# Run with verbose output
./checkpoint -verbose

# Use SQLite storage (requires uncommenting SQLite import)
./checkpoint -storage sqlite -db checkpoints.db

# Use a different model
./checkpoint -model gpt-4o-mini
```

### Command-Line Flags

- `-model`: LLM model to use (default: "deepseek-chat")
- `-storage`: Storage backend - "memory" or "sqlite" or "redis" (default: "memory")
- `-db`: SQLite database file path (default: "checkpoints.db", only used with -storage=sqlite)
- `-verbose`: Enable verbose execution output (default: false)

## Interactive Commands

The example provides a comprehensive CLI for checkpoint management:

### Workflow Execution

- `run [lineage-id]` - Start a new workflow (auto-generates ID if not provided)
- `resume <lineage-id> [checkpoint-id] ["input"]` - Resume from latest or specific checkpoint with optional input

### Checkpoint Operations

- `list [lineage-id]` - List all checkpoints for a lineage
- `latest [lineage-id]` - Show detailed information about the latest checkpoint
- `history [lineage-id]` - Display complete execution history
- `branch <lineage-id> <checkpoint-id>` - Create a branch within the same lineage
- `tree <lineage-id>` - Display checkpoint tree showing branches

### Management

- `delete <lineage-id>` - Delete all checkpoints for a lineage
- `demo` - Run a comprehensive demonstration of all features
- `help` - Show command help
- `exit` - Exit the application

## Example Sessions

### Basic Workflow with Checkpoints

```bash
$ ./checkpoint
🚀 Advanced Checkpoint Example
✅ Checkpoint workflow ready!

🔐 checkpoint> run workflow-1
🚀 Starting workflow with lineage: workflow-1
✅ Workflow execution finished
   Last node: 

🔐 checkpoint> list workflow-1
📜 Checkpoints for lineage: workflow-1
--------------------------------------------------------------------------------
1. ID: 78824eea-3331-478e-b34d-0ce8cc669326
   Namespace: <empty>
   Created: 14:23:45 | Source: loop | Step: 3
   State: counter=3, steps=4, last_action=final
2. ID: 9e68f67c-e216-40c7-a1d2-4b3cc7a05da0
   Namespace: <empty>
   Created: 14:23:45 | Source: loop | Step: 2
   State: counter=3, steps=3, last_action=increment3
3. ID: 0470d394-1a5e-4977-955f-c4da26d86f11
   Namespace: <empty>
   Created: 14:23:45 | Source: loop | Step: 1
   State: counter=2, steps=2, last_action=increment2
4. ID: 8abb0365-a0a5-4594-8d1d-a9a8f47cb731
   Namespace: <empty>
   Created: 14:23:45 | Source: loop | Step: 0
   State: counter=1, steps=1, last_action=increment1
5. ID: b8b99ece-a14c-406f-9e52-27f025f05f8e
   Namespace: <empty>
   Created: 14:23:45 | Source: input | Step: -1
   State: counter=0, steps=0, last_action=
```

### Resume from Checkpoint

```bash
🔐 checkpoint> run workflow-2
🚀 Starting workflow with lineage: workflow-2
✅ Workflow execution finished
   Last node: final, nodes executed: 4

🔐 checkpoint> resume workflow-2
🔄 Resuming workflow from lineage: workflow-2
✅ Workflow execution finished
   Last node: final, nodes executed: 4

# Resume with additional input
🔐 checkpoint> resume workflow-2 auto "continue processing"
🔄 Resuming workflow from lineage: workflow-2 with input: continue processing
✅ Workflow execution finished
   Last node: final, nodes executed: 4
```

### Branching Workflows

```bash
# Branch from the initial checkpoint (step -1)
🔐 checkpoint> branch workflow-1 b8b99ece-a14c-406f-9e52-27f025f05f8e
🌿 Creating branch in lineage workflow-1 from checkpoint b8b99ece-a14c-406f-9e52-27f025f05f8e
✅ Branch created successfully
   Branched checkpoint ID: 4b3789c4-a790-4a5d-8a51-a46b2a721979
   Parent checkpoint ID: b8b99ece-a14c-406f-9e52-27f025f05f8e
   Lineage ID (unchanged): workflow-1

🔐 checkpoint> resume workflow-1 4b3789c4-a790-4a5d-8a51-a46b2a721979
🔄 Resuming workflow from lineage: workflow-1, checkpoint: 4b3789c4-a790-4a5d-8a51-a46b2a721979
✅ Workflow execution finished
   Last node: final, nodes executed: 4

🔐 checkpoint> tree workflow-1
🌳 Checkpoint Tree for lineage: workflow-1
--------------------------------------------------------------------------------
Total checkpoints: 9
Branch points: 1

📍 b8b99ece (counter=0, source=input, 14:23:45)
    ├── 📍 8abb0365 (counter=1, source=loop, 14:23:45)
    │   └── 📍 0470d394 (counter=2, source=loop, 14:23:46)
    │       └── 📍 9e68f67c (counter=3, source=loop, 14:23:46)
    │           └── 📍 78824eea (counter=3, source=loop, 14:23:46)
    └── 📍 4b3789c4 (counter=0, source=fork, 14:24:10)
        └── 📍 c5d9e3a1 (counter=1, source=loop, 14:24:10)
            └── 📍 f2a8b7d4 (counter=2, source=loop, 14:24:10)
                └── 📍 a9c6e5b2 (counter=3, source=loop, 14:24:10)
                    └── 📍 d7f3c8e9 (counter=3, source=loop, 14:24:10)
```

### Viewing History

```bash
🔐 checkpoint> history workflow-1
📚 Execution history for lineage: workflow-1
================================================================================

⏰ 14:23:45 (Step -1)
   State: counter=0, total_steps=0

⏰ 14:23:45 (Step 0)
   Action: increment1 executed
   State: counter=1, total_steps=1
   Message: Node increment1 executed at 14:23:45

⏰ 14:23:46 (Step 1)
   Action: increment2 executed
   State: counter=2, total_steps=2
   Message: Node increment2 executed at 14:23:46

[... more history entries ...]
================================================================================
```

## Demo Mode

Run the comprehensive demo to see all features in action:

```bash
🔐 checkpoint> demo
```

The demo will:
1. Run a new workflow with a unique demo lineage ID
2. List all created checkpoints (5 checkpoints: step -1 through step 3)
3. Display the latest checkpoint details with full state information
4. Show the checkpoint tree structure
5. Display complete execution history
6. Create a branch from the second checkpoint (step 1)
7. Resume the branched workflow to completion
8. Show the final tree with both execution paths

## Key Concepts

### Lineage
- **Definition**: A unique identifier for a conversation or workflow thread
- **Purpose**: Groups related checkpoints together
- **Usage**: All checkpoints in a lineage share the same conversation context
- **Example**: `workflow-1`, `demo-1234567890`
- **Auto-generation**: If not provided, generates format like `auto-<timestamp>`

### Checkpoint
- **Definition**: A saved snapshot of the graph's complete state
- **Contents**: Channel values, versions, pending writes, interrupt state, next nodes for recovery
- **Sources**: `input` (initial), `loop` (execution), `interrupt` (paused), `fork` (branched)
- **Storage**: Automatically created by the executor during execution
- **Step numbering**: Starts at -1 (initial checkpoint), then 0, 1, 2, etc.

### Namespace
- **Definition**: Organizational unit within a lineage
- **Format**: `default:<lineage-id>:<timestamp>`
- **Purpose**: Enables parallel branches within the same lineage
- **Usage**: Automatically generated, can be customized for advanced scenarios

### Branching
- **Definition**: Creating a fork within the same lineage from an existing checkpoint
- **Purpose**: Explore alternative execution paths while maintaining conversation context
- **Behavior**: Creates a new checkpoint with `source=fork` that continues from the parent
- **Tree Structure**: Shows as parallel branches in the checkpoint tree
- **Use Cases**: What-if scenarios, A/B testing, debugging, exploring different paths

## Implementation Details

### Checkpoint Storage
- Uses in-memory saver for demonstration (can use SQLite for persistence)
- Automatic deep copying prevents external state modification
- Atomic operations ensure consistency

### State Recovery
- Restores complete graph state from checkpoint
- Rebuilds execution frontier using version information
- Handles interrupted workflows with resume capability

### Integration with GraphAgent
- Simple configuration: `graphagent.WithCheckpointSaver(saver)`
- Automatic checkpoint creation during execution
- No additional code needed in node implementations

## Advanced Usage Scenarios

### Resuming from Initial Checkpoint

You can now resume from a fork of the initial checkpoint (step -1) to re-run the entire workflow with different parameters:

```bash
# Get the initial checkpoint ID (step -1) from the list
🔐 checkpoint> list my-workflow
# Find the checkpoint with Source: input, Step: -1

# Branch from the initial checkpoint
🔐 checkpoint> branch my-workflow <initial-checkpoint-id>

# Resume - this will execute all nodes from the beginning
🔐 checkpoint> resume my-workflow
```

### Parallel Execution Paths

Create multiple branches from the same checkpoint to explore different paths:

```bash
# Create first branch
🔐 checkpoint> branch workflow-1 <checkpoint-id>
# Note the branched checkpoint ID

# Create second branch from the same parent
🔐 checkpoint> branch workflow-1 <checkpoint-id>
# Note this branched checkpoint ID

# Resume both branches independently
🔐 checkpoint> resume workflow-1 <first-branch-id>
🔐 checkpoint> resume workflow-1 <second-branch-id>
```

## Environment Variables

Set these environment variables before running:
- `OPENAI_API_KEY`: API key for the LLM provider
- `OPENAI_BASE_URL`: Base URL for the API endpoint (optional)

## Advanced Features Demonstrated

1. **Automatic Checkpointing**: Checkpoints created at each execution step
2. **State Persistence**: Complete graph state saved and restored
3. **Time-Travel Debugging**: Resume from any historical checkpoint
4. **Workflow Branching**: Create alternative execution paths
5. **Lineage Management**: Organize related checkpoints
6. **State Inspection**: View complete state at any checkpoint
7. **History Tracking**: Audit trail of all execution steps

## Notes

- The example defaults to in-memory storage for simplicity; SQLite support is available
- Checkpoints are automatically created by the executor - no manual checkpoint code needed in nodes
- Each checkpoint gets a unique UUID for identification
- Branching enables powerful debugging and experimentation capabilities
- All checkpoint operations are atomic to ensure consistency
- Checkpoints are listed newest first (by timestamp)
- The initial checkpoint (step -1) is created before any nodes execute
- Resume from initial checkpoint fork now works correctly (executes all nodes)

## Recent Improvements

1. **Initial Checkpoint Resume**: Fixed issue where resuming from a fork of the initial checkpoint (step -1) would not execute any nodes. The system now correctly handles NextNodes for initial checkpoints.

2. **Step Counter Continuity**: Checkpoint step counters now correctly continue from the resumed checkpoint's step value instead of resetting to 0.

3. **Parent-Child Relationships**: Fixed checkpoint parent tracking to ensure proper tree structure display.

4. **Checkpoint Display**: Checkpoints now show their actual UUIDs instead of placeholder values.

## Known Limitations

1. **SQLite Schema**: The SQLite checkpoint saver may require schema updates if you encounter "no such column: seq" errors. The schema needs to be updated to match the latest checkpoint structure.

2. **Concurrent Access**: The in-memory checkpoint saver uses maps that may have concurrent access issues in high-throughput scenarios. Use proper synchronization if needed.
