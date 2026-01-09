# OneShot By Node Example

This example demonstrates `one_shot_messages_by_node`, a state key that lets
parallel branches prepare different one-shot inputs for different Large Language
Model (LLM) nodes without clobbering each other.

## What problem does this solve?

`one_shot_messages` is a single shared key in graph state. If multiple parallel
branches write it in the same step, the “last write wins”, and one branch can
accidentally overwrite (or later clear) another branch’s one-shot input.

`one_shot_messages_by_node` fixes this by storing a map keyed by node ID:

- A branch writes `by_node["llm1"] = ...`
- Another branch writes `by_node["llm2"] = ...`
- Each LLM node reads only `by_node[its_node_id]` and clears only its own entry

## How to run

```bash
cd trpc-agent-go/examples/graph/oneshot_by_node
go run . -model deepseek-chat
```

Optional flags:

- `-q1`: user prompt for `llm1`
- `-q2`: user prompt for `llm2`

## What you will see

- The run completes after both `llm1` and `llm2` finish.
- The program prints `node_responses` for both nodes.
- It also prints the remaining size of `one_shot_messages_by_node` (should be
  empty after consumption).

## Related example

- `examples/graph/oneshot_by_node_preprocess`: prepares one-shot inputs for
  multiple Large Language Model (LLM) nodes from a single upstream node.
