# Checkpointing Example

This example demonstrates how to use the checkpoint utilities in the `trpc-agent-go/graph` package. It shows how to:

- Enable checkpointing on a graph executor.
- Run a graph and automatically create checkpoints with atomic storage.
- List and filter checkpoints with proper sorting and filtering.
- Resume from the latest checkpoint.
- Resume from a specific checkpoint.
- Create manual checkpoints with metadata.
- Delete all checkpoints for a thread.
- Use atomic checkpoint storage to ensure data consistency.

## What this example runs

A simple linear workflow graph with three nodes:

```
inc1 -> inc2 -> inc3
```

Each node increments a `counter` field and appends a message to `messages`. After each step, a checkpoint is created automatically when checkpointing is enabled on the executor.

## Modes

Select a mode with `-mode`:

- `run`: Execute the workflow N times to create checkpoints.
- `list`: List checkpoints (optionally limited and/or filtered by metadata).
- `resume`: Resume from the latest checkpoint for the thread.
- `goto`: Resume from a specific checkpoint ID.
- `manual`: Create a manual checkpoint (optionally tagged with a category).
- `filter`: List checkpoints filtered by metadata (e.g., `category`).
- `delete`: Delete all checkpoints for a thread.
- `demo`: Run a full demo: run -> list -> resume -> manual -> filter -> delete.

## CLI

```bash
# Run once and create checkpoints
go run . -mode run -thread my-thread -steps 3

# List checkpoints
go run . -mode list -thread my-thread

# Resume from latest checkpoint
go run . -mode resume -thread my-thread

# Resume from a specific checkpoint
go run . -mode goto -thread my-thread -checkpoint <checkpoint-id>

# Create a manual checkpoint and tag it
go run . -mode manual -thread my-thread -category important

# Filter checkpoints by metadata (category)
go run . -mode filter -thread my-thread -category important

# Delete all checkpoints for a thread
go run . -mode delete -thread my-thread

# End-to-end demo
go run . -mode demo -thread my-thread -steps 3
```

### Flags

- `-mode`: One of `run|list|resume|goto|manual|filter|delete|demo` (default: `run`).
- `-thread`: Thread ID for checkpointing. If not provided, a new one will be generated.
- `-steps`: Number of runs to execute in `run`/`demo` modes (default: 3).
- `-checkpoint`: Checkpoint ID for `goto` mode.
- `-limit`: Limit results in `list`/`filter` modes.
- `-category`: Metadata category used in `manual` and `filter` modes.

## How it works

- The executor is created with a `CheckpointSaver`:
  ```go
  saver := inmemory.NewSaver()
  exec, _ := graph.NewExecutor(g, graph.WithCheckpointSaver(saver))
  ```
- When the graph runs, checkpoints are created initially and after each step using atomic storage.
- The executor uses `PutFull` to atomically save checkpoints with their pending writes.
- You can use `CheckpointManager` to list, filter, and delete checkpoints.
- Checkpoint lists are automatically sorted by timestamp (newest first) and support filtering.
- To resume from a specific checkpoint, load it and execute the graph with the loaded state.

## State Schema

The example uses a simple state schema with two fields:

- `counter` (int): incremented by each node.
- `messages` ([]string): log of operations.

## Notes

- This example uses the in-memory checkpoint saver for simplicity. In production,
  use a persistent backend implementation.
- Thread IDs define independent checkpoint timelines. Use meaningful thread IDs so
  you can reliably resume execution later.
- The checkpoint system now uses atomic storage to ensure data consistency between
  checkpoints and their associated pending writes.
- Deep copy is implemented using JSON marshaling/unmarshaling for safety across
  all data types.
- Step and node timeouts are supported for better execution control.

