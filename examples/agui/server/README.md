# AG-UI Servers

This directory shows AG-UI servers that can talk to the AG-UI client examples.

## Available Servers

- [`default/`](default/) – Minimal AG-UI server that wires the `tRPC-Agent-Go` runner.
- [`skill_artifacts/`](skill_artifacts/) – Demonstrates `skill_run` output artifacts surfaced as `CustomEvent("tool.artifacts")`.
- [`event_emitter/`](event_emitter/) – Demonstrates Node EventEmitter for emitting custom events, progress updates, and streaming text from NodeFunc.
- [`finishresult/`](finishresult/) – Demonstrates populating `RUN_FINISHED.result` by wrapping the default translator.
- [`externaltool/`](externaltool/) – Demonstrates a two-call external tool workflow (`role=user` then `role=tool`) backed by `GraphAgent` interrupts.
- [`streamtool/`](streamtool/) – Demonstrates a minimal `StreamableTool` that streams incremental numeric progress as `ACTIVITY_SNAPSHOT` / `ACTIVITY_DELTA` while preserving a final `TOOL_CALL_RESULT`.
- [`graph/`](graph/) – Demonstrates graph node start activity events via `ACTIVITY_DELTA`.
- [`react/`](react/) – The server showcases how React planner tags are streamed as custom AG-UI events.
- [`langfuse/`](langfuse/) – This example shows how AG-UI Server customizes reporting through TranslateCallback and connects to the langfuse observability platform.
- [`report/`](report/) – Report-focused LLMAgent that delivers answers as structured reports in a dedicated view for easy consumption.
- [`follow/`](follow/) – Enables `MessagesSnapshot` follow mode so `/history` continues streaming persisted events until the run finishes.
