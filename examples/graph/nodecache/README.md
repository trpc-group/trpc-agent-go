# Graph Node Cache Example

This example demonstrates per-node caching in a graph workflow using Runner + GraphAgent with an interactive CLI and default streaming output. It shows how to:

- Enable a graph-level cache and default cache policy
- Apply a node-level cache policy with a Time To Live (TTL)
- Observe cache hits via node completion events (`_cache_hit` marker)

## Features

- Interactive CLI (enter integers; repeated inputs hit the cache)
- Default streaming of node events
- Cache TTL control via a flag

## Run

```bash
cd examples/graph/nodecache
go run . --ttl=60   # TTL in seconds (default: 60)
```

What to try:

1. Enter an integer, e.g. `42`. The compute node simulates ~300ms work and returns `out = 2 * n`.
2. Enter the same number again. You should see a cache hit message and an instant result.
3. Optionally, set a small TTL (e.g. `--ttl=2`), wait for it to expire, and try the same input again to see recomputation.

## Flags

- `--ttl int` Cache TTL (seconds) for the compute node (default: `60`)

## What to look for

- On the second run with the same input, node completion events include a `_cache_hit` marker, and the node function is skipped.
- If the TTL expires, the next run recomputes and refreshes the cache entry.

## How it works

At a high level:

1. The graph enables a cache backend and a default cache policy (key function + TTL):
   - Key function: canonical JSON of sanitized input + SHA‑256
   - Backend: in-memory cache (thread-safe; deep copies for isolation)
2. The compute node optionally overrides the default policy with a per-node TTL.
3. The executor flow:
   - Before executing a node, it attempts a cache `Get` using the policy key derived from sanitized input.
   - On hit: it runs after-node callbacks, applies the result to state/writes, and emits a node.complete event with `_cache_hit`.
   - On miss: it runs the node function; after successful writes, it persists the result via `Cache.Set`.

## Code highlights

- Enable graph-level cache and policy: [examples/graph/nodecache/main.go](examples/graph/nodecache/main.go)
- Per-node cache policy with TTL: [examples/graph/nodecache/main.go](examples/graph/nodecache/main.go)
- Reading the `_cache_hit` marker from node.complete events: [examples/graph/nodecache/main.go](examples/graph/nodecache/main.go)
- Minimal schema and compute node: [examples/graph/nodecache/main.go](examples/graph/nodecache/main.go)

## Requirements

- Go 1.21+
- No external Large Language Model (LLM) required — the compute node is a pure function.

## Notes

- Cache is most appropriate for pure function-like nodes (same input → same output, no side effects).
- The in-memory backend is intended for local testing. In production, consider a persistent backend and a sweeper for TTL expiration.
