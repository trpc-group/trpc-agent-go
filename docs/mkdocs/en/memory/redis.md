# Redis Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, high concurrency, distributed deployment

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
if err != nil {
    panic(err)
}
```

**Configuration options**:

- `WithRedisClientURL(url)`: Redis connection URL (recommended)
- `WithRedisInstance(name)`: Use pre-registered Redis instance
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithKeyPrefix(prefix)`: Set a prefix for all Redis keys. When set, every key is prefixed with `prefix:`. For example, if `prefix` is `"myapp"`, the key `mem:{app:user}` becomes `myapp:mem:{app:user}`. Default is empty (no prefix). This is useful for sharing a single Redis instance across multiple environments or services
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to Redis client

**Note**: `WithRedisClientURL` takes priority over `WithRedisInstance`

**Redis ACL requirement**: The default per-user memory limit is `1000`. When the
configured limit is positive, `AddMemory` uses a server-side Lua script to
atomically check the capacity and write the memory. This script calls
`HEXISTS`, `HLEN`, and `HSET`. `UpdateMemory` always uses a Lua script to
atomically validate and rotate memory IDs; its update path uses `HGET`, and its
script calls `HEXISTS`, `HSET`, and `HDEL`.

ACL users must be allowed to run `EVALSHA` and `EVAL` (`EVAL` is required when
a script is not yet cached), the commands used by both scripts, and access the
configured memory-key pattern. Do not remove `EVAL` after warm-up because the
Redis script cache can be cleared by a restart or `SCRIPT FLUSH`. Redis-compatible
backends must support server-side Lua for these scripted paths.

`WithMemoryLimit(0)` keeps `AddMemory` on the direct `HSET` path without a
scripting dependency. `UpdateMemory` still uses Lua.

**Key prefix example**:

```go
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
if err != nil {
    panic(err)
}
```
