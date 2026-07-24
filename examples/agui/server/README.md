# AG-UI Servers

This directory shows AG-UI servers that can talk to the AG-UI client examples.

## Available Servers

- [`default/`](default/) – Minimal AG-UI server that wires the `tRPC-Agent-Go` runner.
- [`skill_artifacts/`](skill_artifacts/) – Demonstrates `skill_run` output artifacts surfaced as `CustomEvent("tool.artifacts")`.
- [`event_emitter/`](event_emitter/) – Demonstrates Node EventEmitter for emitting custom events, progress updates, and streaming text from NodeFunc.
- [`finishresult/`](finishresult/) – Demonstrates populating `RUN_FINISHED.result` by wrapping the default translator.
- [`runner_factory/`](runner_factory/) – Demonstrates wrapping the built-in AG-UI runner with `agui.WithRunnerFactory`.
- [`externaltool/llmagent/`](externaltool/llmagent/) – Demonstrates `llmagent + agui + WithExternalTools` with two internal tools and two dynamically declared external tools in the same conversation.
- [`externaltool/graphagent/`](externaltool/graphagent/) – Demonstrates a `GraphAgent` interrupt workflow with two internal tools and two external tools in the same turn.
- [`externaltool/agentnode_llmagent/`](externaltool/agentnode_llmagent/) – Demonstrates AgentNode child `LLMAgent` external tools with AG-UI interrupt and resume.
- [`externaltool/agentnode_graphagent/`](externaltool/agentnode_graphagent/) – Demonstrates a parent `GraphAgent` with two `AgentNode` children, where the first child `GraphAgent` interrupts internally and the second child `LLMAgent` runs after resume.
- [`externaltool/agenttool_graphagent_graphagent/`](externaltool/agenttool_graphagent_graphagent/) – Demonstrates a parent `GraphAgent` `ToolsNode` calling an `AgentTool` whose child `GraphAgent` interrupts and resumes through AG-UI.
- [`externaltool/agentnode_handoff_agenttool/`](externaltool/agentnode_handoff_agenttool/) – Demonstrates an outer `AgentNode` producing a `handoff_task` external tool call that a normal graph node executes through a dynamically selected `AgentTool` child `GraphAgent`.
- [`streamtool/`](streamtool/) – Demonstrates a minimal `StreamableTool` that uses `agui.WithStreamingToolResultActivityEnabled(true)` to stream tool progress as `ACTIVITY_SNAPSHOT` / `ACTIVITY_DELTA` while preserving a final `TOOL_CALL_RESULT`.
- [`runhook/`](runhook/) – Demonstrates `agui.WithRunHook` for server-side background UI events that stream to SSE and persist to AG-UI history without entering normal session events.
- [`graph_progress/`](graph_progress/) – Demonstrates `GraphAgent` nodes using `aguirunner.RunFromContext` to proactively emit AG-UI progress percentages.
- [`heartbeat/`](heartbeat/) – Demonstrates SSE heartbeat keepalive frames with `agui.WithHeartbeatInterval`.
- [`graph/`](graph/) – Demonstrates graph node start activity events via `ACTIVITY_DELTA`.
- [`react/`](react/) – The server showcases how React planner tags are streamed as custom AG-UI events.
- [`langfuse/`](langfuse/) – This example shows how AG-UI Server customizes reporting through TranslateCallback and connects to the langfuse observability platform.
- [`report/`](report/) – Report-focused LLMAgent that delivers answers as structured reports in a dedicated view for easy consumption.
- [`follow/`](follow/) – Enables `MessagesSnapshot` follow mode so `/history` continues streaming persisted events until the run finishes.
