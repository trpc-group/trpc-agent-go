# 使用与配置

## 与 Agent 集成

使用**两步方法**将 Memory Service 集成到 Agent：

1. **注册工具**：使用 `llmagent.WithTools(memoryService.Tools())` 向 Agent 注册记忆工具
2. **设置服务**：使用 `runner.WithMemoryService(memoryService)` 在 Runner 中设置记忆服务

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 步骤 1：创建记忆服务
memoryService := memoryinmemory.NewMemoryService()

// 步骤 2：创建 Agent 并注册记忆工具
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("具有记忆能力的智能助手"),
    llmagent.WithTools(memoryService.Tools()), // 显式注册工具
)

// 步骤 3：创建 Runner 并设置记忆服务
appRunner := runner.NewRunner(
    "memory-chat",
    llmAgent,
    runner.WithMemoryService(memoryService), // 在 Runner 层设置服务
)
```

## 记忆服务 (Memory Service)

记忆服务支持多种存储后端（InMemory、SQLite、SQLiteVec、Redis、MySQL、MySQLVec、PostgreSQL、pgvector、ChromaDB），可根据场景选择。

### 配置示例

```go
import (
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
    memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

// 1. 内存存储（开发/测试）
memService := memoryinmemory.NewMemoryService()

// 2. Redis 存储（生产环境 - 高性能）
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
if err != nil {
    // 处理错误
}

// 3. MySQL 存储（生产环境 - ACID 保证）
mysqlDSN := "user:password@tcp(localhost:3306)/dbname?parseTime=true"
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN(mysqlDSN),
    memorymysql.WithSoftDelete(true), // 可选：启用软删除
)
if err != nil {
    // 处理错误
}

// 4. PostgreSQL 存储（生产环境 - JSONB 支持）
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithHost("localhost"),
    memorypostgres.WithPort(5432),
    memorypostgres.WithUser("postgres"),
    memorypostgres.WithPassword("password"),
    memorypostgres.WithDatabase("dbname"),
    memorypostgres.WithSoftDelete(true), // 可选：启用软删除
)
if err != nil {
    // 处理错误
}
```

**快速选择指南**：

| 场景                 | 推荐后端         | 原因                             |
| -------------------- | ---------------- | -------------------------------- |
| 本地开发             | InMemory         | 零配置，快速启动                 |
| 高并发读写           | Redis            | 内存级性能，支持分布式           |
| 需要复杂查询         | MySQL/PostgreSQL | 关系型数据库，SQL 支持           |
| 需要 JSON 高级操作   | PostgreSQL       | JSONB 类型，高效 JSON 查询       |
| 需要审计追踪         | MySQL/PostgreSQL | 支持软删除，可恢复数据           |
| MySQL 向量检索       | MySQLVec         | MySQL 余弦与混合检索             |
| PostgreSQL 向量检索  | pgvector         | PostgreSQL 余弦与混合检索        |
| 独立向量数据库服务   | ChromaDB         | REST 余弦检索与客户端混合结果融合 |

## 记忆工具配置

记忆服务提供 6 个工具。工具驱动模式下默认启用常用工具，危险操作需手动启用；自动提取模式下，提取器可用的操作和对 Agent 暴露的工具分开控制。

### 工具清单

| 工具            | 功能       | 工具驱动模式 | 自动提取模式 | 说明                            |
| --------------- | ---------- | ------------ | ------------ | ------------------------------- |
| `memory_add`    | 添加新记忆 | ✅ 默认启用  | ✅ 提取器默认可用；默认不暴露给 Agent | 创建新记忆条目                  |
| `memory_update` | 更新记忆   | ✅ 默认启用  | ✅ 提取器默认可用；默认不暴露给 Agent | 修改现有记忆                    |
| `memory_search` | 搜索记忆   | ✅ 默认启用  | ✅ 默认启用并暴露 | 根据关键词查找                  |
| `memory_load`   | 加载记忆   | ✅ 默认启用  | ⚙️ 默认禁用；启用后暴露 | 加载最近的记忆                  |
| `memory_delete` | 删除记忆   | ⚙️ 可配置    | ✅ 提取器默认可用；默认不暴露给 Agent | 删除单条记忆                    |
| `memory_clear`  | 清空记忆   | ⚙️ 可配置    | ⚙️ 默认禁用  | 删除所有记忆                    |

**说明**：

- **工具驱动模式**：Agent 主动调用工具管理记忆，所有工具均可配置
  - 默认启用工具：`memory_add`、`memory_update`、`memory_search`、`memory_load`
  - 默认禁用工具：`memory_delete`、`memory_clear`
- **自动提取模式**：LLM 提取器在后台管理已启用的写入操作；`Tools()` 默认暴露搜索工具，`memory_load` 启用后暴露，也可通过 `WithAutoMemoryExposedTools()` 选择性暴露已启用的写工具
  - 默认启用工具：`memory_add`、`memory_update`、`memory_delete`、`memory_search`
  - 默认禁用工具：`memory_load`、`memory_clear`
  - 默认启用但不通过 `Tools()` 暴露给 Agent：`memory_add`、`memory_update`、`memory_delete`
- **默认启用**：创建服务时自动可用，无需额外配置
- **可配置**：可以通过 `WithToolEnabled()` 启用或禁用；在 Auto 模式下，可通过 `WithAutoMemoryExposedTools()` 控制哪些已启用写工具对 Agent 暴露

### 启用/禁用工具

提示：`WithToolEnabled()` 控制记忆操作是否可用，`WithAutoMemoryExposedTools()` 控制
Auto 模式下哪些已启用工具会通过 `Tools()` 暴露给 Agent。写工具默认隐藏，只有显式暴露后 Agent 才能主动调用。

```go
// 场景 1：用户可管理（允许删除单条记忆）
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
)

// 场景 2：管理员权限（允许清空所有记忆）
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)

// 场景 3：只读助手（只允许查询）
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
    memoryinmemory.WithToolEnabled(memory.UpdateToolName, false),
)

// 场景 4：Auto + 主动写记忆混合模式
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    memoryinmemory.WithAutoMemoryExposedTools(memory.AddToolName),
)
```

## 覆盖语义（ID 与重复）

- 记忆 ID 基于「内容 + appName + userID + 规范化事件元数据」生成；主题不参与 ID。对同一用户重复添加相同内容与身份元数据是幂等的：会覆盖原有记录（非追加），并刷新 topics 与 UpdatedAt。如果该规范 ID 对应软删除记录，`AddMemory` 会将其重新激活。
- 如需“允许重复/只返回已存在/忽略重复”等策略，可通过自定义工具或扩展服务策略配置实现。

## 更新语义与 ID 轮转

`UpdateMemory` 会先应用新的内容、topics 和事件元数据，再重新计算规范 Memory
ID。topics 不参与 ID 计算，因此只修改 topics 时 ID 保持不变。

更新遵循以下状态机：

| 应用更新后的状态 | 结果 |
| ---------------- | ---- |
| source 不存在或已软删除 | 返回 not-found 错误，且不修改 `UpdateResult` |
| 规范 ID 不变 | 原地更新 active source |
| newID 不存在 | 创建 target，再淘汰 source |
| newID 是软删除记录 | 重新激活 target；硬删除模式会替换旧 tombstone |
| newID 已是 active | 返回冲突错误，source 和 target 都不修改 |

启用软删除时，成功的 ID 轮转会把旧 source 保留为 tombstone；硬删除模式则移除旧
source。SQL 后端会原子地完成 target 准备和 source 淘汰。

SQL 后端的时间戳语义保持一致：

- 新插入的 target 继承 source 的 `CreatedAt`。
- 重新激活的 target 保留自己的 `CreatedAt`。
- 硬删除模式替换旧 target tombstone 时，replacement 继承 source 的 `CreatedAt`。
- 每次成功更新都会刷新 `UpdatedAt`。

成功时，`UpdateResult.MemoryID` 返回最终生效的规范 ID；失败时，调用方传入的
result 保持不变。

## 自定义工具实现

提示：在 Auto 模式下，`Tools()` 默认暴露 `memory_search`；`memory_load` 在启用后可暴露，
其他已启用工具需配合 `WithAutoMemoryExposedTools()` 显式暴露。像 `memory_clear` 这类危险操作通常更适合由业务侧直接控制。

你可以用自定义实现覆盖默认工具。参考 [memory/tool/tool.go](https://github.com/trpc-group/trpc-agent-go/blob/main/memory/tool/tool.go) 了解如何实现自定义工具：

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    toolmemory "trpc.group/trpc-go/trpc-agent-go/memory/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// 自定义清空工具，使用调用上下文中的 MemoryService 与会话信息。
func customClearMemoryTool() tool.Tool {
    clearFunc := func(ctx context.Context, _ *toolmemory.ClearMemoryRequest) (*toolmemory.ClearMemoryResponse, error) {
        // 从调用上下文获取 MemoryService 与用户信息。
        memSvc, err := toolmemory.GetMemoryServiceFromContext(ctx)
        if err != nil {
            return nil, fmt.Errorf("custom clear tool: %w", err)
        }
        appName, userID, err := toolmemory.GetAppAndUserFromContext(ctx)
        if err != nil {
            return nil, fmt.Errorf("custom clear tool: %w", err)
        }

        if err := memSvc.ClearMemories(ctx, memory.UserKey{AppName: appName, UserID: userID}); err != nil {
            return nil, fmt.Errorf("custom clear tool: failed to clear memories: %w", err)
        }
        return &toolmemory.ClearMemoryResponse{Message: "🎉 所有记忆已成功清空！"}, nil
    }

    return function.NewFunctionTool(
        clearFunc,
        function.WithName(memory.ClearToolName),
        function.WithDescription("清空用户的所有记忆。"),
    )
}

// 在内存实现上注册自定义工具。
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
)
```

## 完整示例

以下是一个完整的交互式对话示例，展示了记忆功能的实际使用。

### 运行示例

```bash
# 查看帮助
cd examples/memory/simple
go run main.go -h

# 使用默认配置（inmemory + 流式输出）
go run main.go

# 使用 Redis 存储
export REDIS_ADDR=localhost:6379
go run main.go -memory redis

# 使用 MySQL 存储（带软删除）
export MYSQL_HOST=localhost
export MYSQL_PASSWORD=password
go run main.go -memory mysql -soft-delete

# 使用 MySQL Vector 存储
export MYSQLVEC_HOST=localhost
export MYSQLVEC_PASSWORD=password
go run main.go -memory mysqlvec -soft-delete

# 使用 PostgreSQL 存储
export PG_HOST=localhost
export PG_PASSWORD=password
go run main.go -memory postgres -soft-delete

# 使用 pgvector 存储
export PGVECTOR_HOST=localhost
export PGVECTOR_PASSWORD=password
go run main.go -memory pgvector -soft-delete

# 非流式输出模式
go run main.go -streaming=false
```

### 交互演示

```bash
$ go run main.go
🧠 Simple Memory Chat
Model: deepseek-v4-flash
Memory Service: inmemory
In-memory
Streaming: true
Available tools: memory_add, memory_update, memory_search, memory_load
(memory_delete, memory_clear disabled by default, and can be enabled or customized)
==================================================
✅ Memory chat ready! Session: memory-session-1765504743

💡 Special commands:
   /memory   - Show user memories
   /new      - Start a new session
   /exit     - End the conversation

👤 You: 你好，我是张三，我喜欢 Go 语言编程
🤖 Assistant: 你好张三！很高兴认识你！👋 我了解到你喜欢 Go 语言编程，这真是太棒了！Go 语言确实是一门很优秀的编程语言，以其简洁、高效和并发特性而闻名。

让我把这个信息记下来，这样我就能更好地了解你的兴趣和背景了。
🔧 Memory tool calls initiated:
   • memory_add (ID: call_00_FqOJ5GTRxaxWLVDxcfjhogIA)
     Args: {"memory": "张三喜欢 Go 语言编程", "topics": ["programming", "go", "interests", "technology"]}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_00_FqOJ5GTRxaxWLVDxcfjhogIA): {"message":"Memory added successfully","memory":"张三喜欢 Go 语言编程","topics":["programming","go","interests","technology"]}
太好了！我已经记住了你喜欢 Go 语言编程。作为一个 Go 语言爱好者，你平时主要用它做什么项目呢？是后端开发、系统工具，还是其他类型的项目？我很想了解更多关于你的编程经历和兴趣！

👤 You: /new
🆕 Started new memory session!
   Previous: memory-session-1765504743
   Current:  memory-session-1765504766
   (Conversation history has been reset, memories are preserved)

👤 You:  我喜欢什么?
🤖 Assistant: 我来查看一下记忆中关于您的喜好信息。
🔧 Memory tool calls initiated:
   • memory_search (ID: call_00_BsjZNVCjxxMXOsWmMfXfLZ1z)
     Args: {"query": "喜欢 爱好 兴趣 偏好"}

🔄 Executing memory tools...
✅ Memory tool response (ID: call_00_BsjZNVCjxxMXOsWmMfXfLZ1z): {"query":"喜欢 爱好 兴趣 偏好","results":[{"id":"a4b1d02cef09bd21ecc8b44832d1ed7f1b33014f9c3dfd11e72259bf14e900a9","memory":"张三喜欢 Go 语言编程","topics":["programming","go","interests","technology"],"created":"2025-12-12T09:59:16.300377171+08:00"}],"count":1}
根据我的记忆，您喜欢 **Go 语言编程**。这是目前我记录的唯一关于您喜好的信息。

如果您还有其他喜欢的事物，比如：
- 音乐类型
- 电影或书籍
- 运动或活动
- 食物或饮料
- 旅行目的地
- 其他兴趣爱好

请告诉我，我可以帮您记住这些信息，这样下次您问"我喜欢什么"时，我就能给您更全面的回答了！

👤 You: /exit
👋 Goodbye!
```

### 代码示例

完整代码请参考 [examples/memory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)，核心实现：

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
    memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    var (
        memType    = flag.String("memory", "inmemory", "记忆服务类型")
        streaming  = flag.Bool("streaming", true, "是否启用流式输出")
        softDelete = flag.Bool("soft-delete", false, "启用软删除")
        modelName  = flag.String("model", "deepseek-v4-flash", "模型名称")
    )
    flag.Parse()

    ctx := context.Background()

    // 1. 创建记忆服务
    memoryService, err := createMemoryService(*memType, *softDelete)
    if err != nil {
        log.Fatalf("Failed to create memory service: %v", err)
    }

    // 2. 创建模型
    modelInstance := openai.New(*modelName)

    // 3. 创建 Agent
    genConfig := model.GenerationConfig{
        MaxTokens:   intPtr(2000),
        Temperature: floatPtr(0.7),
        Stream:      *streaming,
    }

    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription(
            "具有记忆能力的智能助手。我可以记住关于你的重要信息，"+
            "并在需要时回忆起来。",
        ),
        llmagent.WithGenerationConfig(genConfig),
        llmagent.WithTools(memoryService.Tools()),
    )

    // 4. 创建 Runner
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService),
    )
    defer appRunner.Close()

    // 5. 运行对话
    log.Println("🧠 开始记忆对话...")
    // ... 处理用户输入和响应
}

func createMemoryService(memType string, softDelete bool) (
    memory.Service, error) {

    switch memType {
    case "redis":
        redisAddr := os.Getenv("REDIS_ADDR")
        if redisAddr == "" {
            redisAddr = "localhost:6379"
        }
        return memoryredis.NewService(
            memoryredis.WithRedisClientURL(
                fmt.Sprintf("redis://%s", redisAddr),
            ),
            memoryredis.WithToolEnabled(memory.DeleteToolName, false),
        )

    case "mysql":
        dsn := buildMySQLDSN()
        return memorymysql.NewService(
            memorymysql.WithMySQLClientDSN(dsn),
            memorymysql.WithSoftDelete(softDelete),
            memorymysql.WithToolEnabled(memory.DeleteToolName, false),
        )

    case "postgres":
        return memorypostgres.NewService(
            memorypostgres.WithHost(getEnv("PG_HOST", "localhost")),
            memorypostgres.WithPort(getEnvInt("PG_PORT", 5432)),
            memorypostgres.WithUser(getEnv("PG_USER", "postgres")),
            memorypostgres.WithPassword(getEnv("PG_PASSWORD", "")),
            memorypostgres.WithDatabase(getEnv("PG_DATABASE", "trpc-agent-go-pgmemory")),
            memorypostgres.WithSoftDelete(softDelete),
            memorypostgres.WithToolEnabled(memory.DeleteToolName, false),
        )

    default: // inmemory
        return memoryinmemory.NewMemoryService(
            memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
        ), nil
    }
}

func buildMySQLDSN() string {
    host := getEnv("MYSQL_HOST", "localhost")
    port := getEnv("MYSQL_PORT", "3306")
    user := getEnv("MYSQL_USER", "root")
    password := getEnv("MYSQL_PASSWORD", "")
    database := getEnv("MYSQL_DATABASE", "trpc_agent_go")

    return fmt.Sprintf(
        "%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4",
        user, password, host, port, database,
    )
}

func getEnv(key, defaultVal string) string {
    if val := os.Getenv(key); val != "" {
        return val
    }
    return defaultVal
}

func intPtr(i int) *int             { return &i }
func floatPtr(f float64) *float64   { return &f }
```

## 高级配置

### 自动提取模式配置选项

| 选项                       | 说明                            | 默认值      |
| -------------------------- | ------------------------------- | ----------- |
| `WithExtractor(extractor)` | 使用 LLM 提取器启用自动提取模式 | nil（禁用） |
| `WithAsyncMemoryNum(n)`    | 后台 worker goroutine 数量      | 1           |
| `WithMemoryQueueSize(n)`   | 记忆任务队列大小                | 10          |
| `WithMemoryJobTimeout(d)`  | 每个提取任务的超时时间          | 30s         |

### 提取检查器（Extraction Checkers）

检查器（Checker）用于控制何时触发记忆提取。默认情况下，每轮对话都会触发提取。使用检查器可以优化提取频率，降低 LLM 调用成本。

#### 可用的检查器

| 检查器                  | 说明                               | 示例                                           |
| ----------------------- | ---------------------------------- | ---------------------------------------------- |
| `CheckMessageThreshold` | 当累积消息数超过阈值时触发         | `CheckMessageThreshold(5)` - 消息数 > 5 时触发 |
| `CheckTimeInterval`     | 当距上次提取超过指定时间间隔时触发 | `CheckTimeInterval(3*time.Minute)` - 每 3 分钟 |
| `ChecksAll`             | 组合多个检查器，使用 AND 逻辑      | 所有检查器都通过才触发                         |
| `ChecksAny`             | 组合多个检查器，使用 OR 逻辑       | 任一检查器通过即触发                           |

#### 检查器配置示例

```go
// 示例 1：消息数 > 5 或每 3 分钟提取一次（OR 逻辑）。
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithCheckersAny(
        extractor.CheckMessageThreshold(5),
        extractor.CheckTimeInterval(3*time.Minute),
    ),
)

// 示例 2：消息数 > 10 且每 5 分钟提取一次（AND 逻辑）。
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithChecker(extractor.CheckMessageThreshold(10)),
    extractor.WithChecker(extractor.CheckTimeInterval(5*time.Minute)),
)
```

#### 模型回调（Before/After Model）

提取器也支持通过 `model.Callbacks` 注入 before/after 回调（仅支持 structured），用于埋点、改写请求，或在测试中短路模型调用。

```go
callbacks := model.NewCallbacks().RegisterBeforeModel(
    func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // You can modify args.Request or return CustomResponse.
        return nil, nil
    },
).RegisterAfterModel(
    func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // You can inspect/override args.Response.
        return nil, nil
    },
)

memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithModelCallbacks(callbacks),
)
```

#### ExtractionContext

`ExtractionContext` 为检查器提供决策所需的上下文信息：

```go
type ExtractionContext struct {
    UserKey       memory.UserKey  // 用户标识。
    Messages      []model.Message // 自上次提取以来累积的消息。
    LastExtractAt *time.Time      // 上次提取时间戳，首次提取时为 nil。
}
```

**注意**：`Messages` 包含自上次成功提取以来累积的所有消息。当检查器返回 `false` 时，消息会被累积，并在下次提取时一并处理。这确保了使用轮数或时间检查器时不会丢失对话上下文。

### 工具控制

在自动提取模式下，`WithToolEnabled` 控制工具是否可用。`memory_search`
默认会通过 `Tools()` 暴露给 Agent，启用 `memory_load` 后也会暴露；
`WithAutoMemoryExposedTools` 则用于选择性暴露已启用的写工具，支持
Hybrid 用法。

**前端工具**（通过 `Tools()` 暴露给 Agent 调用；默认状态指 Agent 侧暴露状态）：

| 工具            | Agent 侧默认 | 说明                              |
| --------------- | ------------ | --------------------------------- |
| `memory_search` | ✅ 暴露      | 按查询搜索记忆                    |
| `memory_load`   | ❌ 不暴露    | 加载全部或最近 N 条记忆；启用后暴露 |

**后端操作**（提取器可用性与 Agent 侧暴露状态分开控制）：

| 工具            | 操作默认 | Agent 侧默认 | 说明                         |
| --------------- | -------- | ------------ | ---------------------------- |
| `memory_add`    | ✅ 开    | ❌ 隐藏      | 添加新记忆                   |
| `memory_update` | ✅ 开    | ❌ 隐藏      | 更新现有记忆                 |
| `memory_delete` | ✅ 开    | ❌ 隐藏      | 删除记忆                     |
| `memory_clear`  | ❌ 关    | ❌ 隐藏      | 清空用户所有记忆（危险操作） |

**配置示例**：

```go
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    // 前端：启用 memory_load 供 Agent 调用。
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    // Hybrid：暴露 memory_add，便于 Agent 立即持久化明确提示的长期信息。
    memoryinmemory.WithAutoMemoryExposedTools(memory.AddToolName),
    // 后端：禁用 memory_delete，提取器将无法删除记忆。
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
    // 后端：启用 memory_clear 供提取器使用（谨慎使用）。
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

**注意**：`WithToolEnabled` 和 `WithAutoMemoryExposedTools` 都可以在
`WithExtractor` 之前或之后调用，顺序不影响结果。

### 两种模式对比

| 工具            | 工具驱动模式（无提取器）            | 自动提取模式（有提取器）            |
| --------------- | ----------------------------------- | ----------------------------------- |
| `memory_add`    | ✅ Agent 通过 `Tools()` 调用        | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；提取器也会在后台使用 |
| `memory_update` | ✅ Agent 通过 `Tools()` 调用        | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；提取器也会在后台使用 |
| `memory_search` | ✅ Agent 通过 `Tools()` 调用        | ✅ Agent 通过 `Tools()` 调用        |
| `memory_load`   | ✅ Agent 通过 `Tools()` 调用        | ⚙️ 启用后 Agent 通过 `Tools()` 调用 |
| `memory_delete` | ⚙️ 启用后 Agent 通过 `Tools()` 调用 | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；提取器也会在后台使用 |
| `memory_clear`  | ⚙️ 启用后 Agent 通过 `Tools()` 调用 | ⚙️ 暴露后 Agent 可通过 `Tools()` 调用；启用后提取器也会在后台使用 |

### 记忆预加载

两种模式都支持将记忆预加载到系统提示词中：

```go
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),
    // 预加载选项：
    // llmagent.WithPreloadMemory(0),   // 禁用预加载（默认）。
    // llmagent.WithPreloadMemory(10),  // 自适应预加载预算 10。
    //                                  // 记忆总量 <= 10 时全量注入，
    //                                  // 否则注入当前问题相关的前 10 条检索结果。
    // llmagent.WithPreloadMemory(-1),  // 加载全部。
    //                                  // ⚠️ 警告：全量加载可能显著增加 token 使用量和 API 成本，
    //                                  //     特别是对于存储了大量记忆的用户。生产环境建议使用正数预算。
)
```

启用预加载后，记忆会自动注入到系统提示词中，让 Agent 无需显式工具调用就能获得用户上下文。

当 `WithPreloadMemory(N)` 使用正数时，框架会先探测用户当前的 memory 总量。
如果总量不超过 `N`，则直接全量注入；如果总量超过 `N`，则在框架内部切换为
基于当前用户问题的 `memory_search` 语义，只注入最相关的前 `N` 条结果。
如果当前 `query` 为空、检索报错，或检索结果为空，则会回退为直接加载最多
`N` 条记忆。

**注入机制**：预加载的记忆会**合并**到现有的系统提示词中，而不是作为独立的 system message 插入。这确保了请求中始终只有一个 system message，兼容某些对多个 system message 支持不完善的模型（如 Qwen3.5 系列可能会返回 "System message must be at the beginning" 错误）。

**⚠️ 重要提示**：配置为 `-1` 会加载所有记忆，这可能会显著增加**Token 使用量**和**API 成本**。默认情况下预加载是禁用的（`0`），推荐使用正数预算（如 `10-50`）来平衡性能和成本。

### 混合方案

你可以结合两种方式：

1. 使用自动提取模式进行被动学习（后台提取）
2. 启用搜索工具进行显式记忆查询
3. 预加载记忆获得即时上下文

```go
// 自动提取 + 搜索工具 + 预加载。
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(extractor),
)

llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),  // 默认只有 search（load 可选）。
    llmagent.WithPreloadMemory(10),             // 自适应预加载预算。
)
```
