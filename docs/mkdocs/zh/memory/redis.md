# Redis 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：生产环境、高并发、分布式部署

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
if err != nil {
    panic(err)
}
```

**配置选项**：

- `WithRedisClientURL(url)`: Redis 连接 URL（推荐）
- `WithRedisInstance(name)`: 使用预注册的 Redis 实例
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithKeyPrefix(prefix)`: 设置 Redis key 前缀。设置后所有 key 都会以 `prefix:` 开头。例如 `prefix` 为 `"myapp"` 时，key `mem:{app:user}` 变为 `myapp:mem:{app:user}`。默认为空（无前缀）。适用于多环境或多服务共享同一 Redis 实例的场景
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 Redis 客户端的额外选项

**注意**：`WithRedisClientURL` 优先级高于 `WithRedisInstance`

**Redis ACL 要求**：默认的每用户记忆上限为 `1000`。配置的上限为正数时，
`AddMemory` 使用服务端 Lua 脚本，以原子方式检查容量并写入记忆；该脚本使用
`HEXISTS`、`HLEN` 和 `HSET`。`UpdateMemory` 始终使用 Lua 脚本，以原子方式
校验并轮换记忆 ID；其更新路径使用 `HGET`，脚本使用 `HEXISTS`、`HSET` 和
`HDEL`。

ACL 用户必须具有 `EVALSHA` 和 `EVAL` 权限（脚本尚未缓存时需要 `EVAL`）、
两份脚本所用命令的权限，以及对应记忆 key 的访问权限。Redis 重启或执行
`SCRIPT FLUSH` 后脚本缓存可能被清除，因此不能只在预热阶段临时授予 `EVAL`。
在这些脚本路径下使用 Redis 兼容后端时，必须确认后端支持服务端 Lua。

`WithMemoryLimit(0)` 会让 `AddMemory` 保持直接执行 `HSET` 的路径，不依赖
Lua 脚本；`UpdateMemory` 仍然使用 Lua。

**Key 前缀示例**：

```go
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
if err != nil {
    panic(err)
}
```
