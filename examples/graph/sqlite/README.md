# SQLite Checkpoint Saver Example

This example demonstrates how to use SQLite as the checkpoint storage
backend, including database initialization, migration, pagination, and
filtering features.

## Features

- **Database initialization**: Automatically creates table schemas for
  checkpoints and writes.
- **Migration support**: Automatically detects and adds the `seq` column.
- **Atomic operations**: Uses `PutFull` to ensure atomicity of checkpoints and
  writes.
- **Pagination**: Supports `Before` and `Limit` based pagination.
- **Metadata filtering**: Filter checkpoints by custom metadata.
- **Sequence support**: Ensures deterministic replay order with a sequence
  number.

## Prerequisites

### Install SQLite driver

```bash
go get github.com/mattn/go-sqlite3
```

### System dependencies

- Linux: `libsqlite3-dev`
- macOS: `brew install sqlite3`
- Windows: download SQLite prebuilt binaries

## Usage

### 1. Run the example

```bash
cd examples/graph/sqlite
go run main.go
```

### 2. Expected output

```
🚀 SQLite Checkpoint Saver Example
==================================
📊 Running database migrations...
  ✅ Migrations completed

📝 Demonstrating checkpoint operations...
  ✅ Checkpoint saved, ID: <checkpoint_id>
  📖 Checkpoint read succeeded:
    - Channel values: map[step:1 user_input:Hello, SQLite!]
    - Pending writes: 2
    - Metadata: map[environment:production version:1.0.0]

📄 Demonstrating pagination and filtering...
  ✅ Created 15 checkpoints

  📖 First page (first 5):
    1. Checkpoint <id1> (step 1)
    2. Checkpoint <id2> (step 2)
    ...

  📖 Next page (Before <last_id>):
    1. Checkpoint <id6> (step 6)
    2. Checkpoint <id7> (step 7)
    ...

  🔍 Filter by batch (batch=1):
    1. Checkpoint <id1> (step 1)
    2. Checkpoint <id2> (step 2)
    ...

✅ SQLite example finished
```

## Database schema

### Checkpoints table (checkpoints)

```sql
CREATE TABLE checkpoints (
    lineage_id TEXT NOT NULL,
    checkpoint_ns TEXT NOT NULL,
    checkpoint_id TEXT NOT NULL,
    parent_checkpoint_id TEXT,
    ts INTEGER NOT NULL,
    checkpoint_json TEXT NOT NULL,
    metadata_json TEXT NOT NULL,
    PRIMARY KEY (lineage_id, checkpoint_ns, checkpoint_id)
);
```

### Writes table (checkpoint_writes)

```sql
CREATE TABLE checkpoint_writes (
    lineage_id TEXT NOT NULL,
    checkpoint_ns TEXT NOT NULL,
    checkpoint_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    idx INTEGER NOT NULL,
    channel TEXT NOT NULL,
    value_json TEXT NOT NULL,
    task_path TEXT NOT NULL,
    seq INTEGER NOT NULL,
    PRIMARY KEY (lineage_id, checkpoint_ns, checkpoint_id, task_id, idx)
);
```

### Indexes

```sql
CREATE INDEX idx_checkpoints_ts ON checkpoints(lineage_id, checkpoint_ns, ts);
CREATE INDEX idx_writes_seq ON checkpoint_writes(lineage_id, checkpoint_ns, checkpoint_id, seq);
```

## Key features

### 1. Atomicity guarantee

Use the `PutFull` method to ensure atomicity of checkpoints and writes:

```go
updatedConfig, err := saver.PutFull(ctx, graph.PutFullRequest{
    Config:        config,
    Checkpoint:    checkpoint,
    Metadata:      metadata,
    PendingWrites: pendingWrites,
})
```

### 2. Sequence support

Each `PendingWrite` has a unique sequence number to ensure replay order:

```go
pendingWrites := []graph.PendingWrite{
    {
        Channel:  "output:result",
        Value:    "Processed: Hello, SQLite!",
        TaskID:   "task_001",
        Sequence: 1,
    },
    {
        Channel:  "output:status",
        Value:    "completed",
        TaskID:   "task_001",
        Sequence: 2,
    },
}
```

### 3. Pagination

Supports timestamp-based pagination to avoid string comparison issues:

```go
checkpoints, err := saver.List(ctx, baseConfig, &graph.CheckpointFilter{
    Before: lastCheckpoint.Config, // filter by timestamp
    Limit:  5,
})
```

### 4. Metadata filtering

Filter checkpoints by custom metadata:

```go
batch1Checkpoints, err := saver.List(ctx, baseConfig, &graph.CheckpointFilter{
    Metadata: map[string]any{
        "batch": 1,
    },
    Limit: 10,
})
```

## Production notes

### 1. Database configuration

- Use a file-backed database instead of an in-memory database.
- Configure an appropriate connection pool size.
- Enable WAL mode to improve concurrency performance.

### 2. Migration strategy

- Back up the database before running migrations in production.
- Wrap migration operations in transactions.
- Test migration scripts for backward compatibility.

### 3. Performance optimization

- Periodically run `VACUUM` to reclaim space.
- Monitor index usage.
- Consider partitioning strategies (by time or namespace).

### 4. Monitoring and alerting

- Monitor database connection count.
- Monitor query performance.
- Set up disk space alerts.

## Troubleshooting

### Common issues

1. **SQLite driver not found**
   ```
   could not import github.com/mattn/go-sqlite3
   ```
   Solution: run `go get github.com/mattn/go-sqlite3`.

2. **Database locked**
   ```
   database is locked
   ```
   Solution: check for other processes holding the database, or enable WAL
   mode.

3. **Insufficient disk space**
   ```
   disk I/O error
   ```
   Solution: clean up old checkpoints, run `VACUUM`, or increase disk space.

### Debugging tips

- Enable SQLite logging: `PRAGMA journal_mode = WAL;`.
- Inspect table schema: `PRAGMA table_info(checkpoints);`.
- Analyze query plan: `EXPLAIN QUERY PLAN SELECT ...;`.

## Related docs

- [Graph package guide](../../../docs/zh/graph.md)
- [Checkpointing](../../../docs/zh/graph.md#检查点机制)
- [Interrupt and resume](../../../docs/zh/graph.md#中断和恢复)
- [Event system](../../../docs/zh/graph.md#事件系统)
