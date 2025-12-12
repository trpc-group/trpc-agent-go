# AG-UI Servers

This directory shows AG-UI servers that can talk to the AG-UI client examples.

## Available Servers

- [`default/`](default/) – Minimal AG-UI server that wires the `tRPC-Agent-Go` runner.
- [`event_emitter/`](event_emitter/) – Demonstrates Node EventEmitter for emitting custom events, progress updates, and streaming text from NodeFunc.
- [`react/`](react/) – The server showcases how React planner tags are streamed as custom AG-UI events.
- [`langfuse/`](langfuse/) – This example shows how AG-UI Server customizes reporting through TranslateCallback and connects to the langfuse observability platform.
- [`report/`](report/) – Report-focused LLMAgent that delivers answers as structured reports in a dedicated view for easy consumption.
- [`thinkaggregator/`](thinkaggregator/) – Surfaces model reasoning ("think") as custom events and aggregates them per session before persistence.
