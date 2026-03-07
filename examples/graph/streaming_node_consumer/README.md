# Streaming Between Graph Nodes Example

This example demonstrates **node-to-node streaming inside a GraphAgent**:

- An upstream **LLM node** publishes streaming deltas to a named stream.
- A downstream **consumer node** reads that stream incrementally (line-by-line)
  and can react **before the model call finishes**.

This is useful for workflows like:

LLM (streaming) -> parser -> TTS / UI / file writer

## Key Ideas (Beginner Friendly)

In a graph run, there are three different kinds of data:

1. **State** (durable): keys like `last_response` and `node_responses[nodeID]`.
   This updates **after a node finishes**.
2. **Events** (observable): what `Runner.Run(...)` sends to your `eventCh`.
3. **Streams** (ephemeral): in-memory pipes for **in-graph streaming**.

This example uses Streams.

## How It Works

The graph has 4 nodes:

- `setup`: a small fan-out node so `llm` and `consume` can run in parallel.
- `llm`: an LLM node with `graph.WithStreamOutput("llm:deltas")`.
- `consume`: a function node that calls `graph.OpenStreamReader(...)` and
  parses lines using `bufio.Scanner`.
- `finish`: waits for both branches and writes a summary to `last_response`.

Graph shape:

```
setup
 /  \
llm  consume
 \   /
 finish
```

## Run

Requirements:

- Go 1.21+
- Network access
- `OPENAI_API_KEY` (or your OpenAI-compatible gateway key)

```bash
cd examples/graph/streaming_node_consumer

export OPENAI_BASE_URL="..."  # optional (OpenAI-compatible gateway)
export OPENAI_API_KEY="..."
export MODEL_NAME="gpt-5"  # optional

go run . \
  -prompt "Write a 6-line welcome script for a podcast." \
  -lines 6
```

Optional:

- `-print-llm`: also print raw `chat.completion.chunk` deltas
- `-model`: override `MODEL_NAME` (or `OPENAI_MODEL`)

## What You Should See

As the model streams, the consumer prints each completed line:

```
[consume] <line 1>
[consume] <line 2>
...
done
```

Notes:

- This example makes real model calls and may cost tokens.
- Streams are **ephemeral**: they are for streaming consumption, not for
  checkpointing or durable storage.
