# Advanced Interrupt & Resume Example

This example demonstrates comprehensive interrupt and resume functionality
using the graph package, GraphAgent, and Runner. It showcases how to build
graph-based agents that can be interrupted at specific points and resumed
with user input, implementing a real-world two-stage approval workflow pattern.

## Overview

The example implements an interactive command-line application that:

- Uses **Runner** for orchestration and session management
- Uses **GraphAgent** for graph-based execution with checkpoint support
- Provides an **interactive CLI** with comprehensive commands
- Demonstrates **real-world two-stage approval workflows**
- Supports **multiple interrupt points** with independent handling

### Workflow Nodes

1. **increment** - Increments a counter from 0 to 10 (simulates data processing)
2. **request_approval** - First interrupt point for initial approval
3. **second_approval** - Second interrupt point for additional verification
4. **process_approval** - Processes the final approval decision
5. **finalize** - Completes the workflow with final state

## Features

### Core Capabilities

- **Interactive Command-Line Interface** - Rich CLI with help and command history
- **Multi-Stage Interrupts** - Sequential interrupt points with independent handling
- **Dynamic Resume Logic** - Uses TaskID from checkpoint for automatic key mapping
- **Checkpoint Management** - List, view, tree visualization, and deletion
- **Session Persistence** - Maintains state across interrupts and resumes

### Advanced Features

- **Checkpoint Tree Visualization** - Visual parent-child checkpoint relationships
- **Interrupt Status Tracking** - Detailed interrupt context and available actions
- **Execution History** - Timeline view with interrupt markers
- **Namespace Support** - Parallel execution branches within lineages
- **Multiple Storage Backends** - Memory and SQLite support

## Prerequisites

- Go 1.21 or later
- tRPC-Agent-Go framework

## Usage

### Quick Start

Run the interactive mode (default):

```bash
go run .
```

### Command-Line Flags

- `-model` (string): Model to use (default: "deepseek-chat")
- `-storage` (string): Storage type: "memory" or "sqlite" (default: "memory")
- `-db` (string): SQLite database path (default: "interrupt-checkpoints.db")
- `-verbose` (bool): Enable verbose logging (default: false)
- `-interactive` (bool): Enable interactive CLI mode (default: true)

### Interactive Commands

Once in interactive mode, the following commands are available:

#### Workflow Execution

| Command                       | Description                                  | Example                |
| ----------------------------- | -------------------------------------------- | ---------------------- |
| `run [lineage-id]`            | Execute workflow normally (skips interrupts) | `run my-workflow`      |
| `interrupt [lineage-id]`      | Run until interrupt point                    | `interrupt test-flow`  |
| `resume <lineage-id> <input>` | Resume from interrupt                        | `resume test-flow yes` |

#### Checkpoint Management

| Command                | Description                    | Example             |
| ---------------------- | ------------------------------ | ------------------- |
| `list [lineage-id]`    | List all checkpoints           | `list test-flow`    |
| `tree [lineage-id]`    | Display checkpoint tree        | `tree test-flow`    |
| `history [lineage-id]` | Show execution history         | `history test-flow` |
| `latest [lineage-id]`  | Show latest checkpoint details | `latest test-flow`  |
| `status [lineage-id]`  | Show interrupt status          | `status test-flow`  |
| `delete <lineage-id>`  | Delete lineage checkpoints     | `delete test-flow`  |

#### Other Commands

| Command          | Description                     |
| ---------------- | ------------------------------- |
| `demo`           | Run comprehensive demonstration |
| `help`           | Show all available commands     |
| `exit` or `quit` | Exit the application            |

## Execution Modes

### 1. Normal Execution (`run`)

Executes the complete workflow without interruptions:

```
🔐 interrupt> run xx

🚀 Running workflow normally (lineage: xx)...
2025-09-05T12:05:11+08:00  INFO  Saving checkpoint ID=b2b0ecc5, Source=loop, Step=0
2025-09-05T12:05:11+08:00  INFO  Saving checkpoint ID=d5b1bab3, Source=loop, Step=1
2025-09-05T12:05:11+08:00  INFO  Saving checkpoint ID=403ccd33, Source=loop, Step=2
✅ Workflow execution finished
   Last node: finalize, events: 38, duration: 4.095875ms
```

### 2. Interrupt Mode with Two-Stage Approval

#### First Interrupt

```
🔐 interrupt> interrupt yy

🔄 Running workflow until interrupt (lineage: yy)...
⚡ Executing: increment
⚡ Executing: request_approval
⚠️  Interrupt detected
💾 Execution interrupted, checkpoint saved
   Use 'resume yy <yes/no>' to continue
```

#### Resume to Second Interrupt

```
🔐 interrupt> resume yy yes

⏪ Resuming workflow from lineage: yy with input: yes
2025-09-05T12:05:35+08:00  INFO  Loaded checkpoint - NextNodes=[request_approval]
⚠️  Workflow interrupted again
   Use 'resume yy <yes/no>' to continue
```

#### Final Resume to Completion

```
🔐 interrupt> resume yy yes

⏪ Resuming workflow from lineage: yy with input: yes
2025-09-05T12:05:46+08:00  INFO  Loaded checkpoint - NextNodes=[second_approval]
✅ Workflow completed successfully!
   Total events: 24
```

### 3. Checkpoint Listing

```
🔐 interrupt> list yy

📋 Checkpoints for lineage: yy
--------------------------------------------------------------------------------
 1. ID: 7e8ef18f-dea8-4ecd-974e-4fc64d5f7ff4
    Namespace:
    Created: 12:05:37 | Source: interrupt | Step: 3
    State: counter=10, steps=2, last_node=request_approval
    🔴 INTERRUPTED at node: second_approval
    💬 Message: This requires a second approval (yes/no):
    🔗 Node ID: second_approval
 2. ID: 7c97c495-55e1-4f94-bd20-6ce4cf894973
    Namespace:
    Created: 12:05:37 | Source: loop | Step: 2
    State: counter=10, steps=2, last_node=request_approval
    ✅ Completed checkpoint
--------------------------------------------------------------------------------
```

### 4. Tree Visualization

```
🔐 interrupt> tree yy

🌳 Interrupt Checkpoint Tree for lineage: yy
--------------------------------------------------------------------------------
Total checkpoints: 8
Interrupted checkpoints: 2
Branch points: 7

📍 da75af0a (counter=0, node=, 12:05:58)
  └─📍 ebeb801c (counter=10, node=increment, 12:05:58)
    └─🔴 eb961247 (counter=10, node=increment, 12:05:58) [INTERRUPTED]
      └─📍 7c97c495 (counter=10, node=request_approval, 12:05:58)
        └─🔴 7e8ef18f (counter=10, node=request_approval, 12:05:58) [INTERRUPTED]
          └─📍 f7c845ab (counter=10, node=second_approval, 12:05:58)
            └─📍 2d407492 (counter=10, node=process_approval, 12:05:58)
              └─📍 abd8354b (counter=10, node=finalize, 12:05:58)
--------------------------------------------------------------------------------
```

## Implementation Details

### Architecture

```
┌─────────────────┐
│     Runner      │  Orchestration & Session Management
└────────┬────────┘
         │
┌────────▼────────┐
│   GraphAgent    │  Graph-based Agent with Checkpoint Support
└────────┬────────┘
         │
┌────────▼────────┐
│  StateGraph     │  Workflow Definition with Interrupt Points
└────────┬────────┘
         │
┌────────▼────────┐
│ CheckpointMgr   │  State Persistence & Tree Management
└─────────────────┘
```

### State Schema

The workflow maintains the following state fields:

| Field        | Type     | Description                                |
| ------------ | -------- | ------------------------------------------ |
| `counter`    | int      | Value incremented by increment node (0→10) |
| `messages`   | []string | Operation log and execution history        |
| `user_input` | string   | User's approval input                      |
| `approved`   | bool     | Approval status for decisions              |
| `step_count` | int      | Total execution steps counter              |
| `last_node`  | string   | Last executed node ID                      |

### Interrupt Flow

#### 1. First Interrupt (Request Approval)

```go
// In request_approval node
interruptValue := map[string]any{
    "message":    "Please approve the current state (yes/no):",
    "counter":    getInt(s, "counter"),
    "messages":   getStrs(s, "messages"),
    "step_count": stepCount,
    "node_id":    nodeRequestApproval,
}
// Use node ID as interrupt key for consistency with executor
resumeValue, err := graph.Interrupt(ctx, s, nodeRequestApproval, interruptValue)
```

#### 2. Second Interrupt (Second Approval)

```go
// In second_approval node (only if first was approved)
interruptValue := map[string]any{
    "message":    "This requires a second approval (yes/no):",
    "counter":    getInt(s, "counter"),
    "messages":   getStrs(s, "messages"),
    "step_count": stepCount,
    "node_id":    nodeSecondApproval,
}
resumeValue, err := graph.Interrupt(ctx, s, nodeSecondApproval, interruptValue)
```

#### 3. Dynamic Resume Handling

```go
// Extract TaskID from checkpoint for automatic key mapping
latest, err := w.manager.Latest(ctx, lineageID, namespace)
if err == nil && latest != nil && latest.Checkpoint.IsInterrupted() {
    // Use the TaskID from the interrupt state as the key
    taskID := latest.Checkpoint.InterruptState.TaskID
    cmd.ResumeMap[taskID] = userInput
}
```

### Real-World Use Cases

This pattern is ideal for:

- **Multi-Stage Approvals** - Financial transactions, deployment pipelines
- **Quality Gates** - Code review → security review → deployment
- **Human-in-the-Loop AI** - Initial AI decision → human verification → final check
- **Compliance Workflows** - Multiple regulatory approval requirements
- **Long-Running Processes** - Pausable data pipelines with checkpoints
- **Escalation Workflows** - Manager approval → director approval

## Advanced Features

### Checkpoint Tree Structure

- Visual parent-child relationships
- Interrupt markers (🔴) vs normal checkpoints (📍)
- State tracking at each checkpoint
- Branch point counting

### Interrupt Status Monitoring

```
🔍 Interrupt Status for lineage: test-flow
------------------------------------------------------------
🔴 STATUS: INTERRUPTED
   Node: request_approval
   Task ID: request_approval
   Created: 11:53:56

📋 Context:
   step_count: 1
   node_id: request_approval
   message: Please approve the current state (yes/no):
   counter: 10

💡 Actions:
   resume test-flow yes   - Approve and continue
   resume test-flow no    - Reject and stop
------------------------------------------------------------
```

## Key Implementation Insights

1. **TaskID Consistency** - Interrupt keys match node IDs for executor compatibility
2. **Dynamic Resume** - Resume values mapped based on checkpoint state
3. **Sequential Interrupts** - Each interrupt handled independently
4. **State Preservation** - Complete state maintained across interrupts
5. **Tree Visualization** - Clear parent-child checkpoint relationships

## Notes

- Uses in-memory checkpoint saver by default (use SQLite for persistence)
- Lineage IDs enable multiple concurrent workflow instances
- Namespace support allows parallel execution branches
- All commands provide clear feedback with emojis and formatting
- The demo mode showcases all features automatically

## Troubleshooting

### Resume fails with "no active interrupt found"

- Ensure the workflow was interrupted first
- Check that the lineage ID matches exactly
- Verify the checkpoint is in interrupted state using `status`

### Second interrupt not triggering

- Confirm first approval was "yes" (rejection skips second approval)
- Check logs with `-verbose` flag for detailed execution flow

### Tree visualization issues

- Ensure terminal supports UTF-8 for emoji display
- Use `list` command as alternative for checkpoint information

### Checkpoint not found

- Verify lineage ID spelling
- Check storage backend is accessible
- Ensure workflow has been run at least once
