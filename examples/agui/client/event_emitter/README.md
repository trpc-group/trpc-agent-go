# EventEmitter Client

This client connects to the EventEmitter server example and displays custom events, progress updates, and streaming text events with rich formatting.

## Prerequisites

First, start the server:

```bash
cd examples/agui
go run ./server/event_emitter
```

## Running the Client

In a new terminal:

```bash
cd examples/agui
go run ./client/event_emitter
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-endpoint` | `http://127.0.0.1:8080/agui` | AG-UI SSE endpoint |
| `-prompt` | `process my data` | User prompt to send |

### Example with Custom Prompt

```bash
go run ./client/event_emitter -prompt "analyze this dataset"
```

## Expected Output

```
╔══════════════════════════════════════════════════════════════╗
║       EventEmitter Client - Node Custom Events Demo          ║
╚══════════════════════════════════════════════════════════════╝

📡 Connecting to: http://127.0.0.1:8080/agui
📝 Sending prompt: "process my data"

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                         Event Stream
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🚀 [run_started] Run started
   Thread: event-emitter-demo-thread, Run: run-1234567890

🎬 [workflow.started] Workflow initiated
   ⏰ Timestamp: 2024-01-01T12:00:00Z
   📥 User input: "process my data"
   📌 Version: 1.0.0

📊 [process] ██████░░░░░░░░░░░░░░░░░░░░░░░░  20.0% - Processing step 1 of 5
📊 [process] ████████████░░░░░░░░░░░░░░░░░░  40.0% - Processing step 2 of 5
📊 [process] ██████████████████░░░░░░░░░░░░  60.0% - Processing step 3 of 5
📊 [process] ████████████████████████░░░░░░  80.0% - Processing step 4 of 5
📊 [process] ██████████████████████████████ 100.0% - Processing step 5 of 5

📝 [analyze] 📊 Starting analysis...
📝 [analyze] 📝 Input received: "process my data"
📝 [analyze] 🔍 Analyzing patterns...
📝 [analyze] ✅ Pattern analysis complete.
📝 [analyze] 📈 Generating insights...
📝 [analyze] 💡 Key findings:
📝 [analyze]    - Data processed successfully
📝 [analyze]    - No anomalies detected
📝 [analyze]    - Performance metrics within expected range

🎉 [workflow.completed] Workflow finished
   ⏰ Timestamp: 2024-01-01T12:00:03Z
   📤 Result: Analysis completed successfully with no issues found.
   ✅ Status: Success
   ⏱️  Duration: 2500ms
   🔗 Nodes: start → process → analyze → complete

🏁 [run_finished] Run completed
   Thread: event-emitter-demo-thread, Run: run-1234567890

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

✅ Demo completed successfully!
```

## Event Types Displayed

| Event Type | Icon | Description |
|------------|------|-------------|
| `workflow.started` | 🎬 | Custom event when workflow begins |
| `workflow.completed` | 🎉 | Custom event when workflow finishes |
| `node.progress` | 📊 | Progress bar showing operation status |
| `node.text` | 📝 | Streaming text output |
| `run_started` | 🚀 | AG-UI run lifecycle event |
| `run_finished` | 🏁 | AG-UI run lifecycle event |
| `custom` | ⚡ | Generic custom events |

## Understanding the Demo

1. **Start Node** (`workflow.started`): Emits a custom event with workflow metadata
2. **Process Node** (`node.progress`): Shows real-time progress updates with a progress bar
3. **Analyze Node** (`node.text`): Streams text output line by line
4. **Complete Node** (`workflow.completed`): Emits final results with summary
