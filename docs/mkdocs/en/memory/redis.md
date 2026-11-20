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
- `WithKeyPrefix(prefix)`: Set a prefix for all Redis keys. When set, every memory key is prefixed with `prefix:`. A prefix already ending in `:` does not add another colon. Default is empty (no prefix). This is useful for sharing a single Redis instance across multiple environments or services
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to Redis client

**Note**: `WithRedisClientURL` takes priority over `WithRedisInstance`

**Redis ACL requirement**: The default per-user memory limit is `1000`. When the
configured limit is positive, `AddMemory` uses a server-side Lua script to
atomically check the capacity and write the memory. This script calls `HGET`,
`HLEN`, and `HSET`; for escaped identities, it also uses `HVALS` when checking
capacity in a potentially shared legacy hash. `UpdateMemory` always uses Lua
with `HGET`, `HEXISTS`, `HSET`, and `HDEL` to validate ownership and rotate IDs.
For escaped identities, `DeleteMemory` and `ClearMemories` also use Lua to
validate ownership before deletion, using `HGET` or `HGETALL` and `HDEL`.

ACL users must be allowed to run `EVALSHA` and `EVAL` (`EVAL` is required when
a script is not yet cached), the commands used by these scripts, and access the
configured memory-key pattern. Do not remove `EVAL` after warm-up because the
Redis script cache can be cleared by a restart or `SCRIPT FLUSH`. Redis-compatible
backends must support server-side Lua for these scripted paths.

`WithMemoryLimit(0)` keeps `AddMemory` on the direct `HSET` path for ordinary
identities. Escaped identities still require Lua to avoid overwriting another
identity's legacy record. `UpdateMemory` always uses Lua.

**Storage key format**:

Memory hashes use `mem:{<app>:<user>}`, where `<app>` and `<user>` are
independently escaped: `%` becomes `%25`, `:` becomes `%3A`, `{` becomes `%7B`,
and `}` becomes `%7D`. Other characters, including Unicode, remain unchanged.
For example, `AppName="app"` and `UserID="user"` use `mem:{app:user}`;
`AppName="a:b"` and `UserID="c"` use `mem:{a%3Ab:c}`. Escaping `%` also
keeps a literal `%3A` distinct from a colon. Ordinary names do not grow, and each
reserved character adds two bytes. Both escaped fields remain inside the Redis
Cluster hash tag. Memory IDs and entry JSON are unchanged.

Reads and searches check each entry's `AppName` and `UserID`. Mutations on
escaped keys check ownership atomically, and foreign entries do not consume
the requesting user's quota. This protects legacy records whose raw keys
happen to overlap another identity's escaped key.

**Upgrading existing deployments**: Escaping changes keys for identities
containing these reserved characters; their old records will no longer be
found at the new keys. Ordinary identities keep their existing keys.

For affected deployments, pause all memory writers and back up Redis before
upgrading. If existing memories must remain accessible, copy each entry to its
escaped destination based on the entry's `AppName` and `UserID`, retaining its
hash field ID and JSON. Do not infer ownership by splitting a legacy key.
Review invalid entries and destination conflicts rather than overwriting
them. Verify per-identity reads and counts before resuming writers. Avoid
mixed-version writers, which use different keys. For rollback, pause writers
again and reconcile post-upgrade writes with the backup before restoring the
old version; simply restarting it will not expose those new writes.

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
