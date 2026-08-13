# Redis Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, high concurrency, distributed deployment

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
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

**Redis ACL requirement**: `UpdateMemory` uses a server-side Lua script to
atomically validate and rotate memory IDs. ACL users must be allowed to run
`EVALSHA` and `EVAL` (`EVAL` is required when the script is not yet cached), in
addition to the script's `HEXISTS`, `HSET`, and `HDEL` commands and access to
the configured memory-key pattern. Do not remove `EVAL` after warm-up because
the Redis script cache can be cleared by a restart or `SCRIPT FLUSH`.

**Key prefix example**:

```go
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
```
