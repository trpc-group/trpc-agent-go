# Graph A2A Agent Example

Chinese version: [README.zh.md](./README.zh.md)

This example demonstrates a parent `GraphAgent` calling a remote
`GraphAgent` through an `A2AAgent` sub-agent.

The remote graph includes a real LLM node. After the LLM produces its reply,
the remote graph writes both:

- a textual reply into graph state
- a structured payload with transport metadata into graph state

The parent graph reads those fields back through
`WithSubgraphOutputMapper(...)`, which means the example validates:

- the remote agent emits a terminal `graph.execution` event
- the event carries `state_delta`
- the A2A transport preserves that `state_delta`
- the parent graph can reconstruct the remote final state and use it in later nodes

## Structure

The example has three layers:

1. A local in-process A2A server that exposes the remote graph agent.
2. An `A2AAgent` that talks to that server like a remote sub-agent.
3. A parent `GraphAgent` that treats the `A2AAgent` as one of its graph nodes.

At runtime the call path looks like this:

```text
user input
  -> parent GraphAgent
  -> A2AAgent
  -> in-process A2A server
  -> remote GraphAgent
  -> graph.execution + state_delta
  -> A2AAgent
  -> parent GraphAgent output mapper
  -> parent finalize node
```

## Remote Graph

The remote graph is intentionally small and has three steps:

```text
stash_remote_input -> remote_reply -> capture_remote_state
```

Node responsibilities:

1. `stash_remote_input`
   Saves the original `user_input` into `remote_original_input`.
   This is there so the example can verify state handoff using the exact
   original input, instead of depending on intermediate graph state.
2. `remote_reply`
   A real LLM node created with `AddLLMNode(...)`.
   It returns a short reply that starts with `Remote agent:`.
3. `capture_remote_state`
   Reads the LLM reply from `last_response`, reads the cached original input,
   then writes two remote output fields:
   - `remote_child_value`: the remote agent's reply text
   - `remote_child_payload`: structured metadata such as `echo`, `model`,
     `source_agent`, `transport`, and `reply_chars`

When this remote graph finishes, it emits a terminal `graph.execution` event.
That final event carries the remote `state_delta`.

## Parent Graph

The parent graph is also small:

```text
remote_graph -> finalize
```

Node responsibilities:

1. `remote_graph`
   This is not a normal function node.
   It is an `AddAgentNode(...)` backed by the `A2AAgent`, so running this node
   means "call the remote graph through A2A".
2. `finalize`
   Runs after the remote subgraph returns.
   It checks that the remote reply, echoed input, and raw `state_delta` were
   all recovered successfully, then writes a final confirmation message into
   `last_response`.

The key part is `WithSubgraphOutputMapper(mapRemoteFinalState)`.
That mapper receives the remote subgraph result and converts remote state into
parent state:

- `remote_child_value` -> `value_from_remote`
- `remote_child_payload.echo` -> `echo_from_remote`
- `remote_child_payload` -> `remote_state_payload`
- raw `state_delta` presence -> `raw_state_delta_present`

## Why `state_delta` matters here

This example is not just checking that the remote model produced text.
It is checking that remote graph state survives the full round trip:

1. The remote graph writes state in `capture_remote_state`.
2. The remote graph ends with a terminal `graph.execution`.
3. The A2A server converts that event into A2A messages and preserves
   `state_delta`.
4. The `A2AAgent` converts the A2A response back into graph events.
5. The parent graph's output mapper rebuilds the remote final state from that
   event.
6. The parent `finalize` node proves the data is usable by reading the mapped
   values from parent state.

If any step in that chain breaks, the example exits with an error.

## Why `runOnce` looks for the parent completion event

Both the remote graph and the parent graph emit `graph.execution`.
So the example cannot simply take the first completion event it sees.

`runOnce(...)` prefers the completion event that already contains parent-side
keys such as:

- `value_from_remote`
- `remote_state_payload`
- `echo_from_remote`

That ensures the final printed output comes from the parent graph's terminal
state, not from the remote child graph's terminal state.

## Prerequisites

Provide an OpenAI-compatible model configuration:

```bash
export OPENAI_API_KEY=...
export OPENAI_BASE_URL=...
export MODEL_NAME=deepseek-v4-flash
```

Or pass flags directly.

## Run

```bash
cd examples/graph
go run ./a2a_agent
```

You can also force unary mode:

```bash
go run ./a2a_agent -streaming=false
```

Or override model settings:

```bash
go run ./a2a_agent \
  -model deepseek-v4-flash \
  -base-url "$OPENAI_BASE_URL" \
  -api-key "$OPENAI_API_KEY"
```

For slower providers, increase timeout:

```bash
go run ./a2a_agent -timeout=120s
```

If your model/provider has weak streaming support, disable remote model
streaming while still keeping A2A streaming enabled:

```bash
go run ./a2a_agent -streaming=true -model-streaming=false
```

## Expected output

The example prints:

- the in-process A2A host
- whether streaming is enabled
- whether the remote model call is streaming
- the user input sent into the parent graph
- a remote graph event trace derived from node execution events
- the remote agent's LLM reply
- the transferred remote state payload
- whether the parent mapper saw raw `state_delta`
- the parent graph's final confirmation message

If `state_delta` is not preserved through A2A, or if the parent graph cannot
recover the remote state, the example exits with an error.
