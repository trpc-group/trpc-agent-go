# OneShot By Node (Preprocess) Example

This example demonstrates preparing `one_shot_messages_by_node` in a single
upstream node, using `graph.SetOneShotMessagesByNode(...)`.

## What problem does this solve?

`one_shot_messages_by_node` is stored under a single top-level state key:
`graph.StateKeyOneShotMessagesByNode`.

In Go (Golang), assigning a value to the same `map` key overwrites the previous
value. If you call `graph.SetOneShotMessagesForNode(...)` multiple times inside
one node and then "merge" the returned `graph.State` values with `result[k]=v`,
the last assignment wins and earlier node entries are lost.

`graph.SetOneShotMessagesByNode(...)` lets you build one
`map[nodeID][]model.Message` and write all entries in one return value.

## How to run

```bash
cd trpc-agent-go/examples/graph/oneshot_by_node_preprocess
go run .
```

Optional flags:

- `-q1`: user prompt for `llm1`
- `-q2`: user prompt for `llm2`
- `-user_input`: a fallback user input (the Large Language Model (LLM) nodes will not use it)

## What you will see

- Both `llm1` and `llm2` respond using their own one-shot messages.
- The program prints `node_responses` for both nodes.
- It also prints the remaining size of `one_shot_messages_by_node` (should be
  empty after consumption).

This example uses a small in-process echo model, so it runs without external
model credentials.
