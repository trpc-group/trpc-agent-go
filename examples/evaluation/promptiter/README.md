# PromptIter Examples

This directory contains six PromptIter examples:

- [syncrun](./syncrun): runs the full PromptIter optimization loop directly through `engine.Run` against a single-agent sports recap candidate. Its initial instruction intentionally stays simple so that PromptIter, rather than a strong hand-written seed, drives the gain.
- [tooldesc](./tooldesc): runs the same synchronous engine flow against a single-agent travel candidate while optimizing only the candidate tool-description surface.
- [asyncrun](./asyncrun): runs the single-agent sports recap workflow through `manager.Start/Get`.
- [server](./server): exposes the single-agent sports recap workflow through the HTTP control-plane service in `server/promptiter`.
- [multinode](./multinode): runs PromptIter against a multinode sports recap agent with regular function nodes, AgentNode fan-out/fan-in, and multiple optimized instruction surfaces.
- [remote](./remote): runs PromptIter locally while executing the candidate through a remote `server/trpcagent` service, including Go, ADK Python, and LangGraph candidate server examples.

All examples evaluate against fixed static gold answers stored directly in the eval sets, so repeated runs stay comparable instead of regenerating expected answers online.

The optimization examples keep their own command entrypoint and data layout:

- `main.go`
- `agent.go`
- `README.md`
- `data/`
- `output/`

Choose `syncrun` when you want the simplest synchronous single-agent instruction example. Choose `tooldesc` when you want the simplest synchronous tool-description example. Choose `asyncrun` when you want asynchronous run lifecycle management without HTTP. Choose `server` when you want to serve PromptIter over HTTP and trigger runs remotely. Choose `multinode` when you want to see PromptIter optimize several AgentNode instruction surfaces inside a richer candidate graph. Choose `remote` when you want PromptIter to optimize an agent served by another process or framework through `server/trpcagent`.
