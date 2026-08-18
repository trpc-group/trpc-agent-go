# In-Memory Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Development, testing, rapid prototyping

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService()
```

**Configuration options**:

- `WithMemoryLimit(limit int)`: Set memory limit per user
- `WithMinSearchScore(score float64)`: Filter keyword-search scores below the
  configured threshold (default 0.3)
- `WithMaxResults(maxResults int)`: Limit keyword-search results (default 10;
  use 0 to disable truncation)
- `WithCustomTool(toolName, creator)`: Register custom tool implementation
- `WithToolEnabled(toolName, enabled)`: Enable/disable specific tool
- Auto mode: `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`,
  `WithMemoryJobTimeout`

**Features**: Zero config, high performance, no persistence
