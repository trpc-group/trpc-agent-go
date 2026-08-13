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

**Redis ACL 要求**：`UpdateMemory` 使用服务端 Lua 脚本，以原子方式校验并
轮换记忆 ID。除 `HGET`、脚本使用的 `HEXISTS`、`HSET`、`HDEL` 命令和对应记忆 key
访问权限外，ACL 用户还必须具有 `EVALSHA` 和 `EVAL` 权限；脚本尚未缓存时
需要 `EVAL`。Redis 重启或执行 `SCRIPT FLUSH` 后脚本缓存可能被清除，因此
不能只在预热阶段临时授予 `EVAL`。

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
