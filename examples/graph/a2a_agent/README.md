# Graph A2A Agent Example

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

## Prerequisites

Provide an OpenAI-compatible model configuration:

```bash
export OPENAI_API_KEY=...
export OPENAI_BASE_URL=...
export MODEL_NAME=deepseek-chat
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
  -model deepseek-chat \
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
- the remote agent's LLM reply
- the transferred remote state payload
- whether the parent mapper saw raw `state_delta`
- the parent graph's final confirmation message

If `state_delta` is not preserved through A2A, or if the parent graph cannot
recover the remote state, the example exits with an error.
