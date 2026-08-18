# 内存存储（InMemory）

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：开发、测试、快速原型

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService()
```

**配置选项**：

- `WithMemoryLimit(limit int)`: 设置每用户记忆数量上限
- `WithMinSearchScore(score float64)`: 过滤低于阈值的关键词搜索结果
  （默认 0.3）
- `WithMaxResults(maxResults int)`: 限制关键词搜索结果数（默认 10；设为 0
  时不截断）
- `WithCustomTool(toolName, creator)`: 注册自定义工具实现
- `WithToolEnabled(toolName, enabled)`: 启用/禁用特定工具
- Auto 模式：`WithExtractor`、`WithAsyncMemoryNum`、`WithMemoryQueueSize`、
  `WithMemoryJobTimeout`

**特点**：零配置，高性能，无持久化
