# Memory 使用文档

## 概述

Memory 是 tRPC-Agent-Go 框架中的记忆管理系统，为 Agent 提供持久化记忆和上下文管理能力。通过集成记忆服务、会话管理和记忆工具，Memory 系统能够帮助 Agent 记住用户信息、维护对话上下文，并在多轮对话中提供个性化的响应体验。

## ⚠️ 不兼容更新通知

**重要提示**：记忆集成方式已更新，以提供更好的关注点分离和显式控制。这是一个**不兼容更新**，会影响记忆服务与 Agent 的集成方式。

### 变更内容

- **移除**：`llmagent.WithMemory(memoryService)` - 自动记忆工具注册
- **新增**：两步集成方式：
  1. `llmagent.WithTools(memoryService.Tools())` - 手动工具注册
  2. `runner.WithMemoryService(memoryService)` - 在 runner 中管理服务

### 迁移指南

**之前（旧方式）**：

```go
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithMemory(memoryService), // ❌ 不再支持
)
```

**现在（新方式）**：

```go
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithTools(memoryService.Tools()), // ✅ 步骤1：注册工具
)

runner := runner.NewRunner(
    "app",
    llmAgent,
    runner.WithMemoryService(memoryService), // ✅ 步骤2：设置服务
)
```

### 新方式的优势

- **显式控制**：应用程序完全控制注册哪些工具
- **更好的分离**：框架与业务逻辑的清晰分离
- **服务管理**：记忆服务在适当的层级（runner）进行管理
- **无自动注入**：框架不会自动注入工具或提示，可以按需使用

### 使用模式

Memory 系统的使用遵循以下模式：

1. **创建 Memory Service**：配置记忆存储后端（内存或 Redis）
2. **注册记忆工具**：使用 `llmagent.WithTools(memoryService.Tools())` 手动注册记忆工具到 Agent
3. **在 Runner 中设置记忆服务**：使用 `runner.WithMemoryService(memoryService)` 在 Runner 中配置记忆服务
4. **Agent 自动调用**：Agent 通过已注册的记忆工具自动进行记忆管理
5. **会话持久化**：记忆信息在会话间保持，支持多轮对话

这种模式提供了：

- **智能记忆**：基于对话上下文的自动记忆存储和检索
- **多轮对话**：维护对话状态和记忆连续性
- **灵活存储**：支持内存和 Redis 等多种存储后端
- **工具集成**：手动注册记忆管理工具，提供显式控制
- **会话管理**：支持会话创建、切换和重置

### Agent 集成

Memory 系统与 Agent 的集成方式：

- **手动工具注册**：使用 `llmagent.WithTools(memoryService.Tools())` 显式注册记忆工具
- **服务管理**：使用 `runner.WithMemoryService(memoryService)` 在 Runner 层级管理记忆服务
- **工具调用**：Agent 可以调用记忆工具进行信息的存储、检索和管理
- **显式控制**：应用程序完全控制注册哪些工具以及如何使用它们

## 快速开始

### 环境要求

- Go 1.21 或更高版本
- 有效的 LLM API 密钥（OpenAI 兼容接口）
- Redis 服务（可选，用于生产环境）

### 配置环境变量

```bash
# OpenAI API 配置
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
```

### 最简示例

```go
package main

import (
    "context"
    "log"

    // 核心组件
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // 1. 创建记忆服务
    memoryService := memoryinmemory.NewMemoryService()

    // 2. 创建 LLM 模型
    modelInstance := openai.New("deepseek-chat")

    // 3. 创建 Agent 并注册记忆工具
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("具有记忆能力的智能助手"),
        llmagent.WithInstruction("记住用户的重要信息，并在需要时回忆起来。"),
        llmagent.WithTools(memoryService.Tools()), // 注册记忆工具
    )

    // 4. 创建 Runner 并设置记忆服务
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService), // 设置记忆服务
    )

    // 5. 执行对话（Agent 会自动使用记忆工具）
    log.Println("🧠 开始记忆对话...")
    message := model.NewUserMessage("你好，我的名字是张三，我喜欢编程")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 6. 处理响应 ...
}
```

## 核心概念

[memory 模块](https://github.com/trpc-group/trpc-agent-go/tree/main/memory) 是 tRPC-Agent-Go 框架的记忆管理核心，提供了完整的记忆存储和检索能力。该模块采用模块化设计，支持多种存储后端和记忆工具。

```textplain
memory/
├── memory.go          # 核心接口定义
├── inmemory/          # 内存记忆服务实现
├── redis/             # Redis 记忆服务实现
└── tool/              # 记忆工具实现
    ├── tool.go        # 工具接口和实现
    └── types.go       # 工具类型定义
```

## 使用指南

### 与 Agent 集成

使用两步方法将 Memory Service 集成到 Agent：

1. 使用 `llmagent.WithTools(memoryService.Tools())` 向 Agent 注册记忆工具
2. 使用 `runner.WithMemoryService(memoryService)` 在 Runner 中设置记忆服务

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 创建记忆服务
memoryService := memoryinmemory.NewMemoryService()

// 创建 Agent 并注册记忆工具
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("具有记忆能力的智能助手"),
    llmagent.WithInstruction("记住用户的重要信息，并在需要时回忆起来。"),
    llmagent.WithTools(memoryService.Tools()), // 注册记忆工具
)

// 创建 Runner 并设置记忆服务
appRunner := runner.NewRunner(
    "memory-chat",
    llmAgent,
    runner.WithMemoryService(memoryService), // 设置记忆服务
)
```

### 记忆服务 (Memory Service)

记忆服务可在代码中通过选项配置，支持内存、Redis、MySQL 和 PostgreSQL 四种后端：

#### 记忆服务配置示例

```go
import (
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
    memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

// 内存实现，可用于测试和开发
memService := memoryinmemory.NewMemoryService()

// Redis 实现，用于生产环境
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithToolEnabled(memory.DeleteToolName, true), // 启用删除工具
)
if err != nil {
    // 处理错误
}

// MySQL 实现，用于生产环境（关系型数据库）
// 服务初始化时会自动创建表。如果创建失败会 panic。
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysql.WithToolEnabled(memory.DeleteToolName, true), // 启用删除工具
)
if err != nil {
    // 处理错误
}

// PostgreSQL 实现，用于生产环境（关系型数据库）
// 服务初始化时会自动创建表。如果创建失败会 panic。
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresConnString("postgres://user:password@localhost:5432/dbname"),
    memorypostgres.WithSoftDelete(true), // 启用软删除
    memorypostgres.WithToolEnabled(memory.DeleteToolName, true), // 启用删除工具
)
if err != nil {
    // 处理错误
}

// 向 Agent 注册记忆工具
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithTools(memService.Tools()), // 或 redisService.Tools()、mysqlService.Tools() 或 postgresService.Tools()
)

// 在 Runner 中设置记忆服务
runner := runner.NewRunner(
    "app",
    llmAgent,
    runner.WithMemoryService(memService), // 或 redisService、mysqlService 或 postgresService
)
```

### 记忆工具配置

记忆服务默认启用以下工具，其他工具可通过配置启用：

```go
// 默认启用的工具：add, update, search, load
// 默认禁用的工具：delete, clear
memoryService := memoryinmemory.NewMemoryService()

// 启用禁用的工具
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)

// 禁用启用的工具
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
)
```

### 覆盖语义（ID 与重复）

- 记忆 ID 基于「内容 + 主题」生成。对同一用户重复添加相同内容与主题是幂等的：会覆盖原有记录（非追加），并刷新 UpdatedAt。
- 如需“允许重复/只返回已存在/忽略重复”等策略，可通过自定义工具或扩展服务策略配置实现。

### 自定义工具实现

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

以下是一个完整的示例，展示了如何创建具有记忆能力的 Agent：

```go
package main

import (
    "context"
    "flag"
    "log"
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    var (
        memServiceName = flag.String("memory", "inmemory", "记忆服务类型 (inmemory, redis)")
        redisAddr      = flag.String("redis-addr", "localhost:6379", "Redis 服务器地址")
        modelName      = flag.String("model", "deepseek-chat", "要使用的模型名称")
    )

    flag.Parse()

    ctx := context.Background()

    // 1. 创建记忆服务（根据参数选择）
    var memoryService memory.Service
    var err error

    switch *memServiceName {
    case "redis":
        redisURL := fmt.Sprintf("redis://%s", *redisAddr)
        memoryService, err = memoryredis.NewService(
            memoryredis.WithRedisClientURL(redisURL),
            memoryredis.WithToolEnabled(memory.DeleteToolName, true),
            memoryredis.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
        )
        if err != nil {
            log.Fatalf("Failed to create redis memory service: %v", err)
        }
    default: // inmemory
        memoryService = memoryinmemory.NewMemoryService(
            memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
            memoryinmemory.WithCustomTool(memory.ClearToolName, customClearMemoryTool),
        )
    }

    // 2. 创建 LLM 模型
    modelInstance := openai.New(*modelName)

    // 3. 创建 Agent 并注册记忆工具
    genConfig := model.GenerationConfig{
        MaxTokens:   intPtr(2000),
        Temperature: floatPtr(0.7),
        Stream:      true,
    }

    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("具有记忆能力的智能助手。我可以记住关于你的重要信息，并在需要时回忆起来。"),
        llmagent.WithGenerationConfig(genConfig),
        llmagent.WithTools(memoryService.Tools()), // 注册记忆工具
    )

    // 4. 创建 Runner 并设置记忆服务
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService), // 设置记忆服务
    )

    // 5. 执行对话（Agent 会自动使用记忆工具）
    log.Println("🧠 开始记忆对话...")
    message := model.NewUserMessage("你好，我的名字是张三，我喜欢编程")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 6. 处理响应 ...
}

// 自定义清空工具
func customClearMemoryTool() tool.Tool {
    // ... 实现逻辑 ...
}

// 辅助函数
func intPtr(i int) *int { return &i }
func floatPtr(f float64) *float64 { return &f }
```

其中，环境变量配置如下：

```bash
# OpenAI API 配置
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
```

### 命令行参数

```bash
# 运行示例时可以通过命令行参数选择组件类型
go run main.go -memory inmemory
go run main.go -memory redis -redis-addr localhost:6379
go run main.go -memory mysql -mysql-dsn "user:password@tcp(localhost:3306)/dbname?parseTime=true"

# 参数说明：
# -memory: 选择记忆服务类型 (inmemory, redis, mysql, postgres)，默认为 inmemory
# -redis-addr: Redis 服务器地址，默认为 localhost:6379
# -mysql-dsn: MySQL 数据源名称（DSN），使用 MySQL 时必需
# -postgres-dsn: PostgreSQL 连接字符串，使用 PostgreSQL 时必需
# -soft-delete: 为 MySQL/PostgreSQL 记忆服务启用软删除（默认 false）
# -model: 选择模型名称，默认为 deepseek-chat
```

## 存储后端

### 内存存储

内存存储适用于开发和测试环境：

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

// 创建内存记忆服务
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithMemoryLimit(100), // 设置记忆数量限制
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true), // 启用删除工具
)
```

**特点：**

- ✅ 零配置，开箱即用
- ✅ 高性能，无网络开销
- ❌ 数据不持久化，重启后丢失
- ❌ 不支持分布式部署

### Redis 存储

Redis 存储适用于生产环境和分布式应用：

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

// 创建 Redis 记忆服务
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithMemoryLimit(1000), // 设置记忆数量限制
    memoryredis.WithToolEnabled(memory.DeleteToolName, true), // 启用删除工具
)
if err != nil {
    log.Fatalf("Failed to create redis memory service: %v", err)
}
```

**特点：**

- ✅ 数据持久化，支持重启恢复
- ✅ 高性能，适合高并发场景
- ✅ 支持分布式部署
- ✅ 支持集群和哨兵模式
- ⚙️ 需要 Redis 服务器

**Redis 配置选项：**

- `WithRedisClientURL(url string)`: 设置 Redis 连接 URL
- `WithRedisInstance(name string)`: 使用预注册的 Redis 实例
- `WithMemoryLimit(limit int)`: 设置每个用户的最大记忆数量
- `WithToolEnabled(toolName string, enabled bool)`: 启用或禁用特定工具
- `WithCustomTool(toolName string, creator ToolCreator)`: 使用自定义工具实现

### MySQL 存储

MySQL 存储适用于需要关系型数据库的生产环境：

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

// 创建 MySQL 记忆服务
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysql.WithMemoryLimit(1000), // 设置记忆数量限制
    memorymysql.WithTableName("memories"), // 自定义表名（可选）
    memorymysql.WithToolEnabled(memory.DeleteToolName, true), // 启用删除工具
)
if err != nil {
    log.Fatalf("Failed to create mysql memory service: %v", err)
}
```

**特点：**

- ✅ 数据持久化，ACID 事务保证
- ✅ 关系型数据库，支持复杂查询
- ✅ 支持主从复制和集群
- ✅ 自动创建表结构
- ✅ 完善的监控和管理工具
- ⚙️ 需要 MySQL 服务器（5.7+ 或 8.0+）

**MySQL 配置选项：**

- `WithMySQLClientDSN(dsn string)`: 设置 MySQL 数据源名称（DSN）
- `WithMySQLInstance(name string)`: 使用预注册的 MySQL 实例
- `WithTableName(name string)`: 自定义表名（默认 "memories"）。如果表名无效会 panic。
- `WithMemoryLimit(limit int)`: 设置每个用户的最大记忆数量
- `WithSoftDelete(enabled bool)`: 启用软删除（默认 false）。启用后，删除操作设置 `deleted_at`，查询会过滤已软删除的记录。
- `WithToolEnabled(toolName string, enabled bool)`: 启用或禁用特定工具
- `WithCustomTool(toolName string, creator ToolCreator)`: 使用自定义工具实现

**注意：** 服务初始化时会自动创建表。如果表创建失败，服务会 panic。

**DSN 格式：**

```
[username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
```

**常用 DSN 参数：**

- `parseTime=true`: 解析 DATE 和 DATETIME 为 time.Time（必需）
- `charset=utf8mb4`: 字符集
- `loc=Local`: time.Time 值的时区
- `timeout=10s`: 连接超时时间

**表结构：**

MySQL 记忆服务在初始化时会自动创建以下表结构：

```sql
CREATE TABLE IF NOT EXISTS memories (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    INDEX idx_app_user (app_name, user_id),
    UNIQUE INDEX idx_app_user_memory (app_name, user_id, memory_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
```

**使用 Docker 启动 MySQL：**

```bash
# 启动 MySQL 容器
docker run -d --name mysql-memory \
  -e MYSQL_ROOT_PASSWORD=password \
  -e MYSQL_DATABASE=memory_db \
  -p 3306:3306 \
  mysql:8.0

# 等待 MySQL 就绪
docker exec mysql-memory mysqladmin ping -h localhost -u root -ppassword

# 使用 MySQL 记忆服务
go run main.go -memory mysql -mysql-dsn "root:password@tcp(localhost:3306)/memory_db?parseTime=true"
```

**注册 MySQL 实例（可选）：**

```go
import (
    storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
    memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
)

// 注册 MySQL 实例
storage.RegisterMySQLInstance("my-mysql",
    storage.WithClientBuilderDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
)

// 使用注册的实例
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLInstance("my-mysql"),
)
```

### PostgreSQL 存储

PostgreSQL 存储适用于需要关系型数据库和 JSONB 支持的生产环境：

```go
import memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"

// 创建 PostgreSQL 记忆服务
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresConnString("postgres://user:password@localhost:5432/dbname"),
    memorypostgres.WithMemoryLimit(1000), // 设置记忆数量限制
    memorypostgres.WithTableName("memories"), // 自定义表名（可选）
    memorypostgres.WithSoftDelete(true), // 启用软删除
    memorypostgres.WithToolEnabled(memory.DeleteToolName, true), // 启用删除工具
)
if err != nil {
    log.Fatalf("Failed to create postgres memory service: %v", err)
}
```

**特点：**

- ✅ 数据持久化，ACID 事务保证
- ✅ 关系型数据库，支持复杂查询
- ✅ JSONB 支持，高效的 JSON 操作
- ✅ 支持主从复制和集群
- ✅ 自动创建表结构
- ✅ 完善的监控和管理工具
- ✅ 可选的软删除支持
- ⚙️ 需要 PostgreSQL 服务器（12+）

**PostgreSQL 配置选项：**

- `WithPostgresConnString(connString string)`: 设置 PostgreSQL 连接字符串
- `WithPostgresInstance(name string)`: 使用预注册的 PostgreSQL 实例
- `WithTableName(name string)`: 自定义表名（默认 "memories"）。如果表名无效会 panic。
- `WithMemoryLimit(limit int)`: 设置每个用户的最大记忆数量
- `WithSoftDelete(enabled bool)`: 启用软删除（默认 false）。启用后，删除操作设置 `deleted_at`，查询会过滤已软删除的记录。
- `WithToolEnabled(toolName string, enabled bool)`: 启用或禁用特定工具
- `WithCustomTool(toolName string, creator ToolCreator)`: 使用自定义工具实现

**注意：** 服务初始化时会自动创建表。如果表创建失败，服务会 panic。

**连接字符串格式：**

```
postgres://[user[:password]@][netloc][:port][/dbname][?param1=value1&...]
```

**常用连接字符串参数：**

- `sslmode=disable`: 禁用 SSL（用于本地开发）
- `sslmode=require`: 要求 SSL 连接
- `connect_timeout=10`: 连接超时时间（秒）

**表结构：**

PostgreSQL 记忆服务在初始化时会自动创建以下表结构：

```sql
CREATE TABLE IF NOT EXISTS memories (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    CONSTRAINT idx_app_user_memory UNIQUE (app_name, user_id, memory_id)
);

CREATE INDEX IF NOT EXISTS idx_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS idx_deleted_at ON memories(deleted_at);
```

**使用 Docker 启动 PostgreSQL：**

```bash
# 启动 PostgreSQL 容器
docker run -d --name postgres-memory \
  -e POSTGRES_USER=postgres \
  -e POSTGRES_PASSWORD=password \
  -e POSTGRES_DB=memory_db \
  -p 5432:5432 \
  postgres:15-alpine

# 等待 PostgreSQL 就绪
docker exec postgres-memory pg_isready -U postgres

# 使用 PostgreSQL 记忆服务
go run main.go -memory postgres -postgres-dsn "postgres://postgres:password@localhost:5432/memory_db"
```

**注册 PostgreSQL 实例（可选）：**

```go
import (
    storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

// 注册 PostgreSQL 实例
storage.RegisterPostgresInstance("my-postgres",
    storage.WithClientConnString("postgres://user:password@localhost:5432/dbname"),
)

// 使用注册的实例
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresInstance("my-postgres"),
)
```

### 存储后端对比

| 特性       | 内存存储  | Redis 存储 | MySQL 存储 | PostgreSQL 存储 |
| ---------- | --------- | ---------- | ---------- | --------------- |
| 数据持久化 | ❌        | ✅         | ✅         | ✅              |
| 分布式支持 | ❌        | ✅         | ✅         | ✅              |
| 事务支持   | ❌        | 部分       | ✅ (ACID)  | ✅ (ACID)       |
| 查询能力   | 简单      | 中等       | 强大 (SQL) | 强大 (SQL)      |
| JSON 支持  | ❌        | 部分       | ✅ (JSON)  | ✅ (JSONB)      |
| 性能       | 极高      | 高         | 中高       | 中高            |
| 配置复杂度 | 低        | 中         | 中         | 中              |
| 适用场景   | 开发/测试 | 生产环境   | 生产环境   | 生产环境        |
| 监控工具   | 无        | 丰富       | 非常丰富   | 非常丰富        |

**选择建议：**

- **开发/测试**：使用内存存储，快速迭代
- **生产环境（高性能）**：使用 Redis 存储，适合高并发场景
- **生产环境（数据完整性）**：使用 MySQL 存储，需要 ACID 保证和复杂查询
- **生产环境（PostgreSQL）**：使用 PostgreSQL 存储，需要 JSONB 支持和高级 PostgreSQL 特性
- **混合部署**：可以根据不同应用场景选择不同的存储后端
