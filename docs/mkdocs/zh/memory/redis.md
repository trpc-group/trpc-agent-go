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
- `WithKeyPrefix(prefix)`: 设置 Redis key 前缀，所有记忆 key 都会以 `prefix:` 开头。前缀已经以 `:` 结尾时不会重复添加冒号。默认为空（无前缀）。适用于多环境或多服务共享同一 Redis 实例的场景
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 Redis 客户端的额外选项

**注意**：`WithRedisClientURL` 优先级高于 `WithRedisInstance`

**Redis ACL 要求**：默认的每用户记忆上限为 `1000`。配置的上限为正数时，
`AddMemory` 使用服务端 Lua 脚本，以原子方式检查容量并写入记忆；该脚本使用
`HGET`、`HLEN` 和 `HSET`。对于需要转义的身份，在可能共享的旧 hash 中检查容量时
还会使用 `HVALS`。`UpdateMemory` 始终使用 Lua 脚本，通过 `HGET`、`HEXISTS`、
`HSET` 和 `HDEL` 校验归属并轮换记忆 ID。对于需要转义的身份，`DeleteMemory`
和 `ClearMemories` 也会通过 Lua 在删除前校验归属，使用 `HGET` 或 `HGETALL`
以及 `HDEL`。

ACL 用户必须具有 `EVALSHA` 和 `EVAL` 权限（脚本尚未缓存时需要 `EVAL`）、
这些脚本所用命令的权限，以及对应记忆 key 的访问权限。Redis 重启或执行
`SCRIPT FLUSH` 后脚本缓存可能被清除，因此不能只在预热阶段临时授予 `EVAL`。
在这些脚本路径下使用 Redis 兼容后端时，必须确认后端支持服务端 Lua。

`WithMemoryLimit(0)` 会让普通身份的 `AddMemory` 保持直接执行 `HSET` 的路径。
需要转义的身份仍然依赖 Lua，以避免覆盖其他身份的旧记录。`UpdateMemory` 始终
使用 Lua。

**存储 key 格式**：

记忆 hash 使用 `mem:{<app>:<user>}`，两个身份字段分别进行最小转义：
`%` 转为 `%25`，`:` 转为 `%3A`，`{` 转为 `%7B`，`}` 转为 `%7D`。
其他字符（包括中文）保持原样。例如，`AppName="app"`、`UserID="user"` 对应
`mem:{app:user}`；`AppName="a:b"`、`UserID="c"` 对应 `mem:{a%3Ab:c}`。
转义 `%` 可避免原本就包含 `%3A` 的名称与冒号发生碰撞。普通名称不会膨胀，每个
保留字符增加两个字节。Redis Cluster 的 hash tag 仍包含两个转义后的字段。
MemoryID 和记录 JSON 保持原样。

读取和搜索会逐条校验记录的 `AppName` 和 `UserID`。对转义 key 的修改会原子地
校验归属，其他身份的记录也不会占用当前用户的配额。即使某个身份的旧原始 key
恰好与另一个身份的转义 key 相同，其记录仍受到保护。

**已有部署的升级**：转义会改变含这些保留字符的身份所使用的 key，原有记录不会
出现在新 key 下。普通身份的 key 保持不变。

受影响的部署应先暂停所有记忆写入并备份 Redis。如果需要保留已有记忆的可访问性，
应根据每条记录的 `AppName` 和 `UserID` 将其复制到转义后的目标 key，保留 hash
字段 ID 和 JSON；不要通过拆分旧 key 推断归属。遇到无效记录或目标冲突时应人工
核查，避免覆盖。恢复写入前，应逐个身份核对读取结果和记录数量。避免新旧版本
同时写入，它们使用不同的 key。回滚时应再次暂停写入，结合备份处理升级后的新增
写入，再恢复旧版本；仅重启旧版本无法读取这些新增记录。

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
