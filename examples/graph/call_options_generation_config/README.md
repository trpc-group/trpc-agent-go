# Call Options: Per-Run `GenerationConfig` Overrides

This example shows how to override LLM sampling parameters **per invocation**
without rebuilding a graph.

It demonstrates three targeting modes:

- **Global**: apply a patch to all LLM nodes in the current graph scope.
- **By node**: apply a patch to a specific node ID (`DesignateNode`).
- **By subgraph path**: apply a patch to a node inside a nested subgraph
  (`DesignateNodeWithPath` + `NodePath`).

## What to Expect

The program runs the same parent graph twice:

1. **Default run**: only compile-time `graph.WithGenerationConfig(...)` applies.
2. **Call-options run**: per-run overrides apply on top of compile-time config.

It uses a stub model that prints the effective `GenerationConfig` it receives,
so no real model API key is required.

## Run

```bash
cd examples/graph/call_options_generation_config
go run .
```

