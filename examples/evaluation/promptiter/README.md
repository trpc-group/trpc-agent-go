# PromptIter Examples

This directory contains four PromptIter examples:

- [syncrun](./syncrun): runs the full PromptIter optimization loop directly through `engine.Run`. Its initial candidate instruction intentionally stays simple so that PromptIter, rather than a strong hand-written seed, drives the gain.
- [asyncrun](./asyncrun): runs the same PromptIter workflow through `manager.Start/Get`.
- [server](./server): exposes the same PromptIter workflow through the HTTP control-plane service in `server/promptiter`.
- [multinode](./multinode): runs PromptIter against a multinode sports recap agent with regular function nodes, AgentNode fan-out/fan-in, and multiple optimized instruction surfaces.

All examples evaluate against fixed static gold answers stored directly in the eval sets, so repeated runs stay comparable instead of regenerating expected answers online.

The optimization examples keep their own command entrypoint and data layout:

- `main.go`
- `agent.go`
- `README.md`
- `data/`
- `output/`

Choose `syncrun` when you want the simplest synchronous single-agent engine example. Choose `asyncrun` when you want asynchronous run lifecycle management without HTTP. Choose `server` when you want to serve PromptIter over HTTP and trigger runs remotely. Choose `multinode` when you want to see PromptIter optimize several AgentNode instruction surfaces inside a richer candidate graph.
