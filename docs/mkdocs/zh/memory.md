# Memory 使用文档

## 概述

Memory 是 tRPC-Agent-Go 框架中的记忆管理系统，为 Agent 提供持久化记忆和上下文管理能力。通过集成记忆服务、会话管理和记忆工具，Memory 系统能够帮助 Agent 记住用户信息、维护对话上下文，并在多轮对话中提供个性化的响应体验。

### 定位

Memory 用于管理与用户相关的长期信息，隔离维度为 `<appName, userID>`，可以理解为围绕单个用户逐步积累的“个人档案”。

在跨会话场景中，Memory 使系统依然能够保留当前用户的关键信息，避免每个会话都从零开始重复获取用户信息。

它适合记录稳定、可复用的事实，例如“用户姓名是张三”、“职业是后端工程师”、“偏好简短回答”、“常用语言是英文”等用户信息，并在后续多次交互中直接使用这些信息。

## 核心价值

- **上下文延续性**：跨会话保留用户历史，避免重复询问和输入。
- **个性化服务**：基于长期用户画像和偏好，提供定制化的响应和建议。
- **知识积累**：将对话中的事实和经验转化为可复用的知识。
- **持久化存储**：支持多种存储后端，确保数据安全可靠。

## 使用场景

Memory 模块适用于需要跨会话保留用户信息和上下文的场景：

### 场景 1：个性化客服 Agent

**需求**：客服 Agent 需要记住用户信息、历史问题和偏好，提供一致性服务。

**实现方式**：
- 首次对话：Agent 使用 `memory_add` 记录姓名、公司、联系方式
- 记录用户偏好如"喜欢简短回答"、"技术背景"
- 后续会话：Agent 使用 `memory_load` 加载用户信息，无需重复询问
- 问题解决后：使用 `memory_update` 更新问题状态

### 场景 2：学习陪伴 Agent

**需求**：教育 Agent 需要追踪学生学习进度、知识掌握情况和兴趣。

**实现方式**：
- 使用 `memory_add` 记录已掌握的知识点
- 使用主题标签分类：`["数学", "几何"]`、`["编程", "Python"]`
- 使用 `memory_search` 查询相关知识，避免重复教学
- 根据记忆调整教学策略，提供个性化学习路径

### 场景 3：项目管理 Agent

**需求**：项目管理 Agent 需要追踪项目信息、团队成员和任务进度。

**实现方式**：
- 记录关键项目信息：`memory_add("项目 X 使用 Go 语言", ["项目", "技术栈"])`
- 记录团队成员角色：`memory_add("张三是后端负责人", ["团队", "角色"])`
- 使用 `memory_search` 快速查找相关信息
- 项目完成后：使用 `memory_clear` 清空临时信息
## 快速开始

### 环境要求

- Go 1.21 或更高版本
- 有效的 LLM API 密钥（OpenAI 兼容接口）
- 存储后端（可选）：
  - **开发/测试**：无需外部依赖（使用内存存储）
  - **生产环境**：Redis、MySQL 或 PostgreSQL 服务

### 配置环境变量

```bash
# LLM API 配置（必需）
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"

# 存储后端配置（可选，根据选择的后端配置）
# Redis
export REDIS_ADDR="localhost:6379"

# MySQL
export MYSQL_HOST="localhost"
export MYSQL_PORT="3306"
export MYSQL_USER="root"
export MYSQL_PASSWORD="password"
export MYSQL_DATABASE="memory_db"

# PostgreSQL
export PG_HOST="localhost"
export PG_PORT="5432"
export PG_USER="postgres"
export PG_PASSWORD="password"
export PG_DATABASE="memory_db"
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

[memory 模块](https://github.com/trpc-group/trpc-agent-go/tree/main/memory) 是 tRPC-Agent-Go 框架的记忆管理核心，提供完整的记忆存储和检索能力。

### 架构设计

Memory 模块采用分层设计，由以下核心组件组成：

```
┌─────────────────────────────────────────────────────────────┐
│                         Agent                                │
│  ┌──────────────────────────────────────────────────────┐   │
│  │          Memory Tools（6 个工具）                     │   │
│  │  add | update | delete | search | load | clear       │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                              │
                              ↓
┌─────────────────────────────────────────────────────────────┐
│                    Memory Service                            │
│  • UserKey: <appName, userID> 隔离                         │
│  • Entry: 记忆条目（ID、内容、主题、时间戳）                │
│  • Operations: Add、Update、Delete、Search、Load、Clear    │
└─────────────────────────────────────────────────────────────┘
                              │
                              ↓
┌─────────────────────────────────────────────────────────────┐
│                   Storage Backends                           │
│  • InMemory: 内存存储（开发/测试）                          │
│  • Redis: 高性能缓存（生产环境）                            │
│  • MySQL: 关系型数据库（ACID 保证）                        │
│  • PostgreSQL: 关系型数据库（JSONB 支持）                  │
└─────────────────────────────────────────────────────────────┘
```

**工作流程**：

1. Agent 通过 Memory Tools 与 Memory Service 交互
2. Memory Service 管理记忆的生命周期（CRUD 操作）
3. 记忆以 Entry 形式存储，包含内容、主题、时间戳等
4. Memory ID 通过内容 + 主题的 SHA256 哈希生成，确保幂等性
5. Storage Backends 提供持久化，支持多种存储选项

### 核心组件

| 组件 | 描述 | 技术细节 |
|------|------|----------|
| **Memory Service** | 核心记忆管理服务，提供 CRUD 能力 | 实现统一 Service 接口，支持多种存储后端 |
| **UserKey** | 用户标识符，由 `appName` 和 `userID` 组成 | 记忆隔离的最小单位，确保应用/用户间记忆不干扰 |
| **Entry** | 记忆条目，包含完整记忆信息 | 包括 ID、内容、主题、created_at、updated_at 字段 |
| **Memory ID** | 记忆的唯一标识符 | 基于内容 + 主题的 SHA256 哈希，相同内容产生相同 ID |
| **Topics** | 记忆的主题标签 | 用于分类和检索，支持多个标签 |
| **Memory Tools** | Agent 可调用的记忆操作工具 | 包括 add、update、delete、search、load、clear |
| **Storage Backend** | 存储后端实现 | 支持 InMemory、Redis、MySQL、PostgreSQL |

### 关键流程

#### 记忆的生命周期

```
┌──────────────┐
│ 1. 创建记忆   │  用户对话 → Agent 判断 → 调用 memory_add
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ 2. 生成 ID   │  SHA256（内容 + 主题） → 唯一标识符
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ 3. 存储记忆   │  Entry → Storage Backend（InMemory/Redis/MySQL/PostgreSQL）
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ 4. 检索记忆   │  memory_load（时间排序）或 memory_search（关键词匹配）
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ 5. 更新记忆   │  相同 ID 覆盖更新，刷新 updated_at
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ 6. 删除记忆   │  硬删除或软删除（取决于配置）
└──────────────┘
```

#### 记忆检索流程

**Load（加载记忆）**：
1. 根据 UserKey 查询该用户的所有记忆
2. 按 `updated_at` 降序排序（最近更新的在前）
3. 返回前 N 条记忆（默认 10 条）

**Search（搜索记忆）**：
1. 将查询文本分词（支持中英文）
2. 过滤停用词（a、the、is、of 等）
3. 对每条记忆的内容和主题进行匹配
4. 返回所有匹配的记忆，按更新时间排序

#### 记忆 ID 生成策略

记忆 ID 基于内容和主题的 SHA256 哈希生成，确保相同内容产生相同 ID：

```go
// 生成逻辑
content := "memory:" + 记忆内容
if len(主题) > 0 {
    content += "|topics:" + join(主题, "，")
}
memoryID := SHA256(content) // 64 位十六进制字符串
```

**特性**：
- **幂等性**：重复添加相同内容不会创建新记忆，而是覆盖更新
- **一致性**：相同内容在不同时间添加产生相同 ID
- **去重**：天然支持去重，避免冗余存储

## 使用指南

### 与 Agent 集成

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

### 记忆服务 (Memory Service)

记忆服务支持四种存储后端，可根据场景选择。

#### 配置示例

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

| 场景 | 推荐后端 | 原因 |
|------|---------|------|
| 本地开发 | InMemory | 零配置，快速启动 |
| 高并发读写 | Redis | 内存级性能，支持分布式 |
| 需要复杂查询 | MySQL/PostgreSQL | 关系型数据库，SQL 支持 |
| 需要 JSON 高级操作 | PostgreSQL | JSONB 类型，高效 JSON 查询 |
| 需要审计追踪 | MySQL/PostgreSQL | 支持软删除，可恢复数据 |

### 记忆工具配置

记忆服务提供 6 个工具，默认启用常用工具，危险操作需手动启用。

#### 工具清单

| 工具 | 功能 | 默认状态 | 说明 |
|------|------|---------|------|
| `memory_add` | 添加新记忆 | ✅ 启用 | 创建新记忆条目 |
| `memory_update` | 更新记忆 | ✅ 启用 | 修改现有记忆 |
| `memory_search` | 搜索记忆 | ✅ 启用 | 根据关键词查找 |
| `memory_load` | 加载记忆 | ✅ 启用 | 加载最近的记忆 |
| `memory_delete` | 删除记忆 | ❌ 禁用 | 删除单条记忆 |
| `memory_clear` | 清空记忆 | ❌ 禁用 | 删除所有记忆 |

#### 启用/禁用工具

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

以下是一个完整的交互式对话示例，展示了记忆功能的实际使用。

### 运行示例

```bash
# 查看帮助
cd examples/memory
go run . -h

# 使用默认配置（内存存储 + 流式输出）
go run .

# 使用 Redis 存储
export REDIS_ADDR=localhost:6379
go run . -memory redis

# 使用 MySQL 存储（带软删除）
export MYSQL_HOST=localhost
export MYSQL_PASSWORD=password
go run . -memory mysql -soft-delete

# 使用 PostgreSQL 存储
export PG_HOST=localhost
export PG_PASSWORD=password
go run . -memory postgres -soft-delete

# 非流式输出模式
go run . -streaming=false
```

### 交互演示

```bash
$ go run .
🧠 Multi Turn Chat with Memory
Model: deepseek-chat
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
   (Memory and conversation history have been reset)

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
        modelName  = flag.String("model", "deepseek-chat", "模型名称")
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
            memorypostgres.WithDatabase(getEnv("PG_DATABASE", "postgres")),
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

## 存储后端

### 内存存储（InMemory）

**适用场景**：开发、测试、快速原型

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService()
```

**配置选项**：
- `WithMemoryLimit(limit int)`: 设置每用户记忆数量上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具实现
- `WithToolEnabled(toolName, enabled)`: 启用/禁用特定工具

**特点**：零配置，高性能，无持久化

### Redis 存储

**适用场景**：生产环境、高并发、分布式部署

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
```

**配置选项**：
- `WithRedisClientURL(url)`: Redis 连接 URL（推荐）
- `WithRedisInstance(name)`: 使用预注册的 Redis 实例
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 Redis 客户端的额外选项

**注意**：`WithRedisClientURL` 优先级高于 `WithRedisInstance`

### MySQL 存储

**适用场景**：生产环境、需要 ACID 保证、复杂查询

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

dsn := "user:password@tcp(localhost:3306)/dbname?parseTime=true"
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN(dsn),
    memorymysql.WithSoftDelete(true),
)
```

**配置选项**：
- `WithMySQLClientDSN(dsn)`: MySQL DSN 连接字符串（推荐，必需 `parseTime=true`）
- `WithMySQLInstance(name)`: 使用预注册的 MySQL 实例
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 MySQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）

**DSN 示例**：
```
root:password@tcp(localhost:3306)/memory_db?parseTime=true&charset=utf8mb4
```

**表结构**（自动创建）：
```sql
CREATE TABLE memories (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    UNIQUE INDEX (app_name, user_id, memory_id)
)
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：
```go
defer mysqlService.Close()
```

### PostgreSQL 存储

**适用场景**：生产环境、需要 JSONB 高级特性

```go
import memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"

postgresService, err := memorypostgres.NewService(
    memorypostgres.WithHost("localhost"),
    memorypostgres.WithPort(5432),
    memorypostgres.WithUser("postgres"),
    memorypostgres.WithPassword("password"),
    memorypostgres.WithDatabase("dbname"),
    memorypostgres.WithSoftDelete(true),
)
```

**配置选项**：
- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: 连接参数
- `WithSSLMode(mode)`: SSL 模式（默认 "disable"）
- `WithPostgresInstance(name)`: 使用预注册的 PostgreSQL 实例
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithSchema(schema)`: 指定数据库 schema（默认为 public）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 PostgreSQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）

**注意**：直接连接参数优先级高于 `WithPostgresInstance`

**表结构**（自动创建）：
```sql
CREATE TABLE memories (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    UNIQUE (app_name, user_id, memory_id)
)
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：
```go
defer postgresService.Close()
```

### 后端对比与选择

| 特性 | InMemory | Redis | MySQL | PostgreSQL |
|------|----------|-------|-------|------------|
| **持久化** | ❌ | ✅ | ✅ | ✅ |
| **分布式** | ❌ | ✅ | ✅ | ✅ |
| **事务** | ❌ | 部分 | ✅ ACID | ✅ ACID |
| **查询** | 简单 | 中等 | SQL | SQL |
| **JSON** | ❌ | 基础 | JSON | JSONB |
| **性能** | 极高 | 高 | 中高 | 中高 |
| **配置** | 零配置 | 简单 | 中等 | 中等 |
| **软删除** | ❌ | ❌ | ✅ | ✅ |
| **适用场景** | 开发测试 | 高并发 | 企业应用 | 高级特性 |

**选择建议**：

```
开发/测试 → InMemory（零配置，快速启动）
高并发读写 → Redis（内存级性能）
需要 ACID → MySQL/PostgreSQL（事务保证）
复杂 JSON → PostgreSQL（JSONB 索引和查询）
审计追踪 → MySQL/PostgreSQL（软删除支持）
```

## 常见问题

### Memory 与 Session 的区别

这是最常见的疑问。Memory 和 Session 解决不同的问题：

| 维度 | Memory（记忆） | Session（会话） |
|------|--------------|---------------|
| **定位** | 长期用户档案 | 临时对话上下文 |
| **隔离维度** | `<appName, userID>` | `<appName, userID, sessionID>` |
| **生命周期** | 跨会话持久化 | 单次会话内有效 |
| **存储内容** | 用户画像、偏好、事实 | 对话历史、消息记录 |
| **数据量** | 小（几十到几百条） | 大（几十到几千条消息） |
| **使用场景** | “记住用户是谁” | “记住说了什么” |

**示例**：

```go
// Memory：跨会话保留
memory.AddMemory(ctx, userKey, "用户是后端工程师", []string{"职业"})

// Session：单次会话有效
session.AddMessage(ctx, sessionKey, userMessage("今天天气怎么样？"))
session.AddMessage(ctx, sessionKey, agentMessage("今天晴天"))

// 新会话：Memory 保留，Session 重置
```

### Memory ID 的幂等性

Memory ID 基于「内容 + 主题」的 SHA256 哈希生成，相同内容会产生相同 ID：

```go
// 第一次添加
memory.AddMemory(ctx, userKey, "用户喜欢编程", []string{"爱好"})
// 生成 ID：abc123...

// 第二次添加相同内容
memory.AddMemory(ctx, userKey, "用户喜欢编程", []string{"爱好"})
// 生成相同 ID：abc123...，覆盖更新，刷新 updated_at
```

**影响**：
- ✅ **天然去重**：避免冗余存储
- ✅ **幂等操作**：重复添加不会创建多条记录
- ⚠️ **覆盖更新**：无法追加相同内容（如需追加，可在内容中加时间戳或序号）

### 搜索功能的局限性

Memory 使用**Token 匹配**，不是语义搜索：

**英文分词**：转小写 → 过滤停用词（a、the、is 等）→ 空格分割

```go
// 可以找到
记忆："User likes programming"
搜索："programming" ✅ 匹配

// 找不到
记忆："User likes programming"
搜索："coding" ❌ 不匹配（语义相近但词不同）
```

**中文分词**：使用双字组（bigram）

```go
记忆："用户喜欢编程"
搜索："编程" ✅ 匹配（"编程"在双字组中）
搜索："写代码" ❌ 不匹配（词不同）
```

**限制**：
- 所有后端均在**应用层**过滤和排序（O(n) 复杂度）
- 数据量大时性能受影响
- 不支持语义相似度搜索

**建议**：
- 使用明确关键词和主题标签提高命中率
- 如需语义搜索，考虑集成向量数据库（需自定义实现）

### 软删除的注意事项

**支持情况**：
- ✅ MySQL、PostgreSQL：支持软删除
- ❌ InMemory、Redis：不支持（只有硬删除）

**软删除配置**：

```go
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("..."),
    memorymysql.WithSoftDelete(true), // 启用软删除
)
```

**行为差异**：

| 操作 | 硬删除 | 软删除 |
|------|-------|--------|
| 删除 | 立即移除 | 设置 `deleted_at` 字段 |
| 查询 | 不可见 | 自动过滤（WHERE deleted_at IS NULL） |
| 恢复 | 无法恢复 | 可手动清除 `deleted_at` |
| 存储 | 节省空间 | 占用空间 |

**迁移陷阱**：
```go
// ⚠️ 从支持软删除的后端迁移到不支持的后端
// 软删除的记录会丢失！

// 从 MySQL（软删除）迁移到 Redis（硬删除）
// 需要手动处理软删除记录
```

## 最佳实践

### 生产环境配置

```go
// ✅ 推荐配置
postgresService, err := memorypostgres.NewService(
    // 使用环境变量管理敏感信息
    memorypostgres.WithHost(os.Getenv("DB_HOST")),
    memorypostgres.WithUser(os.Getenv("DB_USER")),
    memorypostgres.WithPassword(os.Getenv("DB_PASSWORD")),
    memorypostgres.WithDatabase(os.Getenv("DB_NAME")),
    
    // 启用软删除（便于恢复）
    memorypostgres.WithSoftDelete(true),
    
    // 合理限制
    memorypostgres.WithMemoryLimit(1000),
)
```

### 错误处理

```go
// ✅ 完整错误处理
err := memoryService.AddMemory(ctx, userKey, content, topics)
if err != nil {
    if strings.Contains(err.Error(), "limit exceeded") {
        // 超限：清理旧记忆或拒绝添加
        log.Warnf("Memory limit exceeded for user %s", userKey.UserID)
    } else {
        return fmt.Errorf("failed to add memory: %w", err)
    }
}
```

### 工具启用策略

```go
// 场景 1：只读助手
readOnlyService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    memoryinmemory.WithToolEnabled(memory.SearchToolName, true),
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
    memoryinmemory.WithToolEnabled(memory.UpdateToolName, false),
)

// 场景 2：普通用户
userService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    // clear 禁用（防止误删所有记忆）
)

// 场景 3：管理员
adminService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

## 参考链接

- [Memory 模块源码](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
- [完整示例代码](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)
- [API 文档](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/memory)

