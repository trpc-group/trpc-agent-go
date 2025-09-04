# Interrupt & Resume Example

This example demonstrates comprehensive interrupt and resume functionality
using the graph package, GraphAgent, and Runner. It showcases how to build
graph-based agents that can be interrupted at specific points and resumed
with user input, following a real-world approval workflow pattern.

## Overview

The example implements an interactive command-line application that:
- Uses **Runner** for orchestration and session management
- Uses **GraphAgent** for graph-based execution
- Provides an **interactive CLI** similar to examples/runner
- Demonstrates **real-world approval workflows**

### Workflow Nodes

1. **increment** - Increments a counter (simulates processing)
2. **request_approval** - Interrupts and waits for user approval
3. **process_approval** - Processes the approval decision
4. **finalize** - Completes the workflow

## Features

### Core Capabilities
- **Interactive Command-Line Interface** - User-friendly commands and help
- **Interrupt Mechanism** - Using `graph.Interrupt()` helper
- **Resume with User Input** - Via ResumeMap for key-based resume
- **Checkpoint Management** - List, view, and track checkpoints
- **Session Persistence** - Maintains state across interrupts

### Advanced Features
- **GraphAgent Integration** - Full graph-based agent capabilities
- **Runner Orchestration** - Professional session and event handling
- **Multiple Execution Modes** - Normal, interrupt, resume, demo
- **Detailed Event Tracking** - Node execution visibility
- **Error Handling** - Robust error recovery and reporting

## Prerequisites

- Go 1.21+
- tRPC-Agent-Go framework

## Usage

### Quick Start

Run the interactive mode (default):
```bash
go run .
```

### Command-Line Flags

- `-interactive` (bool): Enable interactive CLI mode (default: true)
- `-lineage` (string): Custom lineage ID (default: auto-generated)

### Interactive Commands

Once in interactive mode, the following commands are available:

| Command | Description | Example |
|---------|-------------|---------|
| `run` | Execute workflow normally | `run` |
| `interrupt` | Run until interrupt point | `interrupt` |
| `resume <input>` | Resume with user input | `resume yes` |
| `list` | List all checkpoints | `list` |
| `demo` | Run complete demonstration | `demo` |
| `help` | Show command help | `help` |
| `exit` | Exit the application | `exit` |

## Execution Modes

### 1. Normal Execution (`run`)
Executes the complete workflow without interrupt handling:
```
🔐 Interrupt> run
▶️  Running normal execution...
✅ Normal execution completed (11 events)
```

### 2. Interrupt Mode (`interrupt`)
Runs until the approval point and saves checkpoint:
```
🔐 Interrupt> interrupt
🔄 Running until interrupt...
⚡ Executing: increment
⚡ Executing: request_approval
⚠️  Interrupt detected
💾 Execution interrupted, checkpoint saved
   Use 'resume <yes/no>' to continue
```

### 3. Resume Mode (`resume`)
Continue from interrupt with approval decision:
```
🔐 Interrupt> resume yes
⏪ Resuming with input: yes
✅ Resume completed (30 events)
```

### 4. List Checkpoints (`list`)
View all saved checkpoints:
```
🔐 Interrupt> list
📜 Available Checkpoints:
----------------------------------------------------------------------
 1. ID: abc-123-def
    Step: 1 | Source: interrupt
    ⚠️  INTERRUPTED at node: request_approval
----------------------------------------------------------------------
```

### 5. Demo Mode (`demo`)
Runs a complete demonstration sequence:
```
🔐 Interrupt> demo
🎬 Running Complete Demo...
1️⃣  Running until interrupt...
2️⃣  Listing checkpoints...
3️⃣  Resuming with approval...
4️⃣  Running another cycle with rejection...
5️⃣  Final checkpoint list...
✅ Demo completed!
```

## Implementation Details

### Architecture

```
┌─────────────────┐
│     Runner      │  Orchestration layer
└────────┬────────┘
         │
┌────────▼────────┐
│   GraphAgent    │  Graph-based agent
└────────┬────────┘
         │
┌────────▼────────┐
│  StateGraph     │  Workflow definition
└────────┬────────┘
         │
┌────────▼────────┐
│  Checkpoints    │  State persistence
└─────────────────┘
```

### State Schema

The workflow maintains four state fields:

| Field | Type | Description |
|-------|------|-------------|
| `counter` | int | Incremented value |
| `messages` | []string | Operation log |
| `user_input` | string | User's input |
| `approved` | bool | Approval status |

### Interrupt Flow

1. **Interrupt Creation**:
```go
interruptValue := map[string]any{
    "message":  "Please approve the current state (yes/no):",
    "counter":  currentCounter,
    "messages": messageHistory,
}
resumeValue, err := graph.Interrupt(ctx, s, "approval", interruptValue)
```

2. **Resume Handling**:
```go
cmd := &graph.Command{
    ResumeMap: map[string]any{
        "approval": userInput,
    },
}
```

### Real-World Use Cases

This pattern is ideal for:
- **Approval Workflows** - Budget approvals, deployment gates
- **Human-in-the-Loop AI** - Manual verification steps
- **Long-Running Processes** - Pausable data pipelines
- **Quality Gates** - Manual testing checkpoints
- **Compliance Workflows** - Regulatory approval requirements

## Expected Output

### Complete Session Example
```
🔄 Interrupt & Resume Interactive Demo
Lineage: interrupt-demo-1234567890
Session: session-1234567890
==================================================
✅ Workflow ready! Type 'help' for commands.

🔐 Interrupt> interrupt
🔄 Running until interrupt...
⚡ Executing: increment
⚡ Executing: request_approval
⚠️  Interrupt detected
💾 Execution interrupted, checkpoint saved
   Use 'resume <yes/no>' to continue

🔐 Interrupt> resume yes
⏪ Resuming with input: yes
✅ Resume completed (30 events)

🔐 Interrupt> list
📜 Available Checkpoints:
----------------------------------------------------------------------
 1. ID: checkpoint-123
    Step: 1 | Source: interrupt
    ⚠️  INTERRUPTED at node: request_approval
 2. ID: checkpoint-456
    Step: 2 | Source: loop
----------------------------------------------------------------------
```

## Key Differences from Basic Examples

1. **Full Framework Integration** - Uses Runner and GraphAgent
2. **Interactive CLI** - Professional command-line interface
3. **Session Management** - Persistent sessions across executions
4. **Production Patterns** - Error handling, logging, state management
5. **Real-World Focus** - Practical approval workflow implementation

## Notes

- Uses in-memory checkpoint saver (production should use persistent storage)
- Lineage IDs enable multiple concurrent workflows
- Sessions maintain conversation context
- All commands provide clear feedback and error messages
- The demo mode showcases all features automatically

## Troubleshooting

### No checkpoints found
- Ensure you've run `interrupt` before listing
- Check that lineage ID matches if using custom values

### Resume fails
- Verify the workflow was interrupted first
- Ensure input is provided (yes/no)

### Execution doesn't interrupt
- Confirm using `interrupt` command, not `run`
- Check that the workflow reaches the approval node