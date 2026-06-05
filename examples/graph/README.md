# Graph Examples

This directory keeps runnable graph examples flat. Interrupt examples are grouped
by where execution is paused.

## Graph Node Interrupts

These examples pause from a node in the graph that is currently executing:

- [`interrupt/`](interrupt/) demonstrates multi-stage HITL interrupts with
  `graph.Interrupt`.
- [`externaltool/`](externaltool/) demonstrates caller-executed tools where a
  graph tool node pauses with `graph.Interrupt`.
- [`agentnode_llmagent_externaltool/`](agentnode_llmagent_externaltool/) demonstrates an
  `AgentNode` whose child `LLMAgent` emits an external tool call. The pause is
  still performed by a following normal graph node.
- [`dag_interrupt/`](dag_interrupt/) demonstrates `graph.Interrupt` with the DAG
  engine.

Static and externally requested pauses use different trigger mechanisms:

- [`static_interrupt/`](static_interrupt/) pauses before or after declared nodes.
- [`external_interrupt/`](external_interrupt/) requests a pause from outside the
  graph with `graph.WithGraphInterrupt`.

## AgentNode Child Interrupts

These examples pause inside a child agent invoked by a graph node:

- [`nested_interrupt/`](nested_interrupt/) demonstrates a child `GraphAgent`
  inside an `AgentNode` calling `graph.Interrupt`. The parent graph checkpoints
  the interruption, and resuming the parent checkpoint resumes the child graph.
- [`a2a_interrupt/`](a2a_interrupt/) demonstrates the same parent/child interrupt
  propagation through an A2A agent boundary.

The main distinction is the source of `graph.Interrupt`: `agentnode_llmagent_externaltool`
uses a parent graph node to pause after an `LLMAgent` tool call, while
`nested_interrupt` pauses because the child `GraphAgent` called
`graph.Interrupt` internally.
