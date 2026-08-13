# 完整示例

以下是一个完整的交互式对话示例，展示了记忆功能的实际使用。

## 运行示例

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

## 交互演示

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

## 代码示例

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
