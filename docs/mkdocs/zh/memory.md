# Memory 使用文档

## 概述

Memory 是 tRPC-Agent-Go 框架中的记忆管理系统，为 Agent 提供持久化记忆和上下文管理能力。通过集成记忆服务、会话管理和记忆工具，Memory 系统能够帮助 Agent 记住用户信息、维护对话上下文，并在多轮对话中提供个性化的响应体验。

### 定位

Memory 用于管理与用户相关的长期信息，隔离维度为 `<appName, userID>`，可以理解为围绕单个用户逐步积累的“个人档案”。

在跨会话场景中，Memory 使系统依然能够保留当前用户的关键信息，避免每个会话都从零开始重复获取用户信息。

它适合记录稳定、可复用的事实，例如“用户姓名是张三”、“职业是后端工程师”、“偏好简短回答”、“常用语言是英文”等用户信息，并在后续多次交互中直接使用这些信息。

### 两种记忆模式

Memory 支持两种模式来创建和管理记忆，根据你的场景选择合适的模式：

自动提取模式（Auto）在配置了 Extractor 后可用，且推荐作为默认选择。

| 维度         | 工具驱动模式（Agentic）      | 自动提取模式（Auto）                     |
| ------------ | ---------------------------- | ---------------------------------------- |
| **工作方式** | Agent 决定何时调用记忆工具   | 系统自动从对话中提取记忆                 |
| **用户体验** | 可见 - 用户可见工具调用过程  | 透明 - 后台静默创建记忆                  |
| **控制权**   | Agent 完全控制记什么         | 提取器根据对话分析决定                   |
| **可用工具** | 全部 6 个工具                | 默认暴露 `memory_search`；`memory_load` 可配置；已启用写工具可显式暴露 |
| **处理方式** | 同步 - 响应生成过程中        | 异步 - 响应后由后台 worker 处理          |
| **适用场景** | 精确控制、用户主导的记忆管理 | 自然对话、无感知的记忆积累               |

**选择建议**：

- **工具驱动模式**：Agent 会根据对话内容自动判断是否需要调用记忆工具（如用户提到个人信息、偏好等），用户可见工具调用过程，适合需要精确控制记忆内容的场景
- **自动提取模式（推荐）**：希望自然对话流、系统被动学习用户信息、简化用户体验

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

### 工具驱动模式配置（Agentic Mode，可选）

工具驱动模式下，Agent 会根据对话内容自动判断是否需要调用记忆工具来管理记忆。配置分为三步：

```go
package main

import (
    "context"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // 步骤 1：创建记忆服务
    memoryService := memoryinmemory.NewMemoryService()

    // 步骤 2：创建 Agent 并注册记忆工具
    modelInstance := openai.New("deepseek-chat")
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("具有记忆能力的智能助手"),
        llmagent.WithInstruction("记住用户的重要信息，并在需要时回忆起来。"),
        llmagent.WithTools(memoryService.Tools()), // 注册记忆工具。
    )

    // 步骤 3：创建 Runner 并设置记忆服务
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService), // 设置记忆服务
    )
    defer appRunner.Close()

    // 执行对话（Agent 会自动使用记忆工具）
    log.Println("🧠 开始记忆对话...")
    message := model.NewUserMessage("你好，我的名字是张三，我喜欢编程")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
    // 处理响应 ...
    _ = eventChan
}
```

**对话示例**：

```
用户：我叫张三，在腾讯工作。

Agent：你好张三！很高兴认识你。我会记住你在腾讯工作。

🔧 工具调用：memory_add
   参数：{"memory": "用户叫张三，在腾讯工作", "topics": ["姓名", "工作"]}
✅ 记忆添加成功。

Agent：我已经保存了这些信息。今天有什么可以帮你的？
```

### 自动提取模式配置（Auto Mode，推荐）

自动提取模式下，基于 LLM 的提取器分析对话并自动创建记忆。**与工具驱动模式的区别仅在步骤 1：多配置一个 Extractor**。

```go
package main

import (
    "context"
    "log"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // 步骤 1：创建记忆服务（配置 Extractor 启用自动提取模式）
    extractorModel := openai.New("deepseek-chat")
    memExtractor := extractor.NewExtractor(extractorModel)
    memoryService := memoryinmemory.NewMemoryService(
        memoryinmemory.WithExtractor(memExtractor), // 关键：配置提取器
        // 可选：配置异步 worker
        memoryinmemory.WithAsyncMemoryNum(1), // 配置记忆提取任务异步 worker 数量
        memoryinmemory.WithMemoryQueueSize(10), // 配置记忆提取任务队列大小
        memoryinmemory.WithMemoryJobTimeout(30*time.Second), // 配置记忆提取任务超时时间
    )
    defer memoryService.Close()

    // 步骤 2：创建 Agent 并注册记忆工具
    // 注意：配置了 Extractor 后，默认只暴露 search 工具，load 可显式开启。
    chatModel := openai.New("deepseek-chat")
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(chatModel),
        llmagent.WithDescription("具有自动记忆能力的智能助手"),
        llmagent.WithTools(memoryService.Tools()), // 默认只有 search 工具（load 可选）。
    )

    // 步骤 3：创建 Runner 并设置记忆服务
    // Runner 会在响应后自动触发记忆提取。
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService),
    )
    defer appRunner.Close()

    // 执行对话（系统自动在后台提取记忆）
    log.Println("🧠 开始自动记忆对话...")
    message := model.NewUserMessage("你好，我的名字是张三，我喜欢编程")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
    // 处理响应 ...
    _ = eventChan
}
```

**对话示例**：

```
用户：我叫张三，在腾讯工作。

Agent：你好张三！很高兴认识腾讯的朋友。今天有什么可以帮你的？

（后台：提取器分析对话并自动创建记忆，用户无感知）
```

### 两种模式配置对比

| 步骤         | 工具驱动模式（Agentic）             | 自动提取模式（Auto）                   |
| ------------ | ----------------------------------- | -------------------------------------- |
| **步骤 1**   | `NewMemoryService()`                | `NewMemoryService(WithExtractor(ext))` |
| **步骤 2**   | `WithTools(memoryService.Tools())`  | `WithTools(memoryService.Tools())`     |
| **步骤 3**   | `WithMemoryService(memoryService)`  | `WithMemoryService(memoryService)`     |
| **可用工具** | add/update/delete/clear/search/load | 默认 search；load 可配置；已启用写工具可显式暴露 |
| **记忆创建** | Agent 主动调用工具                  | 后台自动提取                           |

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
│  • SQLite: 本地文件数据库（单机持久化）                     │
│  • Redis: 高性能缓存（生产环境）                            │
│  • MySQL: 关系型数据库（ACID 保证）                        │
│  • PostgreSQL: 关系型数据库（JSONB 支持）                  │
│  • pgvector: PostgreSQL + 向量检索（语义搜索）              │
└─────────────────────────────────────────────────────────────┘
```

**工作流程**：

1. Agent 通过 Memory Tools 与 Memory Service 交互
2. Memory Service 管理记忆的生命周期（CRUD 操作）
3. 记忆以 Entry 形式存储，包含内容、主题、时间戳等
4. Memory ID 通过内容 + 主题的 SHA256 哈希生成，确保幂等性
5. Storage Backends 提供持久化，支持多种存储选项

### 核心组件

| 组件                | 描述                                      | 技术细节                                           |
| ------------------- | ----------------------------------------- | -------------------------------------------------- |
| **Memory Service**  | 核心记忆管理服务，提供 CRUD 能力          | 实现统一 Service 接口，支持多种存储后端            |
| **UserKey**         | 用户标识符，由 `appName` 和 `userID` 组成 | 记忆隔离的最小单位，确保应用/用户间记忆不干扰      |
| **Entry**           | 记忆条目，包含完整记忆信息                | 包括 ID、内容、主题、created_at、updated_at 字段   |
| **Memory ID**       | 记忆的唯一标识符                          | 基于内容 + 主题的 SHA256 哈希，相同内容产生相同 ID |
| **Topics**          | 记忆的主题标签                            | 用于分类和检索，支持多个标签                       |
| **Memory Tools**    | Agent 可调用的记忆操作工具                | 包括 add、update、delete、search、load、clear      |
| **Storage Backend** | 存储后端实现                              | 支持 InMemory、SQLite、SQLiteVec、Redis、MySQL、PostgreSQL、pgvector |

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
│ 3. 存储记忆   │  Entry → Storage Backend（InMemory/SQLite/SQLiteVec/Redis/MySQL/PostgreSQL/pgvector）
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
// 生成逻辑（伪代码，省略错误处理）
content := "memory:" + 记忆内容
if len（）) > 0 {
    topics = sort(topics)
    content += "|topics:" + join(topics, ",")
}
content += "|app:" + appName
content += "|user:" + userID
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

记忆服务支持多种存储后端（InMemory、SQLite、SQLiteVec、Redis、MySQL、PostgreSQL、pgvector），可根据场景选择。

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

| 场景               | 推荐后端         | 原因                       |
| ------------------ | ---------------- | -------------------------- |
| 本地开发           | InMemory         | 零配置，快速启动           |
| 高并发读写         | Redis            | 内存级性能，支持分布式     |
| 需要复杂查询       | MySQL/PostgreSQL | 关系型数据库，SQL 支持     |
| 需要 JSON 高级操作 | PostgreSQL       | JSONB 类型，高效 JSON 查询 |
| 需要审计追踪       | MySQL/PostgreSQL | 支持软删除，可恢复数据     |

### 记忆工具配置

记忆服务提供 6 个工具，默认启用常用工具，危险操作需手动启用。

#### 工具清单

| 工具            | 功能       | 工具驱动模式 | 自动提取模式 | 说明                            |
| --------------- | ---------- | ------------ | ------------ | ------------------------------- |
| `memory_add`    | 添加新记忆 | ✅ 默认启用  | ⚙️ 默认隐藏  | 创建新记忆条目                  |
| `memory_update` | 更新记忆   | ✅ 默认启用  | ⚙️ 默认隐藏  | 修改现有记忆                    |
| `memory_search` | 搜索记忆   | ✅ 默认启用  | ✅ 默认启用  | 根据关键词查找                  |
| `memory_load`   | 加载记忆   | ✅ 默认启用  | ⚙️ 可配置    | 加载最近的记忆                  |
| `memory_delete` | 删除记忆   | ⚙️ 可配置    | ⚙️ 默认隐藏  | 删除单条记忆                    |
| `memory_clear`  | 清空记忆   | ⚙️ 可配置    | ⚙️ 默认禁用  | 删除所有记忆                    |

**说明**：

- **工具驱动模式**：Agent 主动调用工具管理记忆，所有工具均可配置
  - 默认启用工具：`memory_add`、`memory_update`、`memory_search`、`memory_load`
  - 默认禁用工具：`memory_delete`、`memory_clear`
- **自动提取模式**：LLM 提取器在后台管理写入操作，默认只暴露搜索工具；可启用 `memory_load`，也可通过 `WithAutoMemoryExposedTools()` 选择性暴露已启用的写工具
  - 默认启用工具：`memory_add`、`memory_update`、`memory_delete`、`memory_search`
  - 默认禁用工具：`memory_load`、`memory_clear`
  - 默认不暴露工具：`memory_add`、`memory_update`、`memory_delete`
- **默认启用**：创建服务时自动可用，无需额外配置
- **可配置**：可以通过 `WithToolEnabled()` 启用或禁用；在 Auto 模式下，可通过 `WithAutoMemoryExposedTools()` 控制哪些已启用写工具对 Agent 暴露

#### 启用/禁用工具

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

### 覆盖语义（ID 与重复）

- 记忆 ID 基于「内容 + 排序后的主题 + appName + userID」生成。对同一用户重复添加相同内容与主题是幂等的：会覆盖原有记录（非追加），并刷新 UpdatedAt。
- 如需“允许重复/只返回已存在/忽略重复”等策略，可通过自定义工具或扩展服务策略配置实现。

### 自定义工具实现

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

### SQLite 存储

**适用场景**：本地持久化、单机部署、Demo

SQLite 将数据保存在单个文件中，适用于不想运维 MySQL/PostgreSQL/Redis
但希望进程重启后仍能保留记忆数据的场景。

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
)

db, err := sql.Open("sqlite3", "file:memories.db?_busy_timeout=5000")
if err != nil {
    // 处理错误
}

memoryService, err := memorysqlite.NewService(
    db,
    memorysqlite.WithSoftDelete(true),
    memorysqlite.WithMemoryLimit(200),
)
if err != nil {
    // 处理错误
}
defer memoryService.Close()
```

**配置选项**：

- `WithTableName(name)`: 表名（默认 "memories"）
- `WithSoftDelete(enabled)`: 软删除（默认 false）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithSkipDBInit(skip)`: 跳过表初始化
- Auto 模式：`WithExtractor`、`WithAsyncMemoryNum`、`WithMemoryQueueSize`、`WithMemoryJobTimeout`
- 工具：`WithCustomTool`、`WithToolEnabled`

**注意事项**：

- 该后端使用 `github.com/mattn/go-sqlite3`，需要 CGO。
- `NewService` 会在 `Close()` 时关闭传入的 `*sql.DB`。

### SQLiteVec（sqlite-vec）存储

**适用场景**：本地持久化 + 语义检索（单机）

SQLiteVec 将记忆保存在 SQLite 文件中，并通过 `sqlite-vec` 提供向量相似度
检索（语义检索）。相比普通 SQLite 后端，它需要配置 **embedder** 来为
记忆和查询生成 embedding。

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

db, err := sql.Open("sqlite3", "file:memories_vec.db?_busy_timeout=5000")
if err != nil {
    // 处理错误
}

emb := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

memoryService, err := memorysqlitevec.NewService(
    db,
    memorysqlitevec.WithEmbedder(emb),
    memorysqlitevec.WithSoftDelete(true),
    memorysqlitevec.WithMemoryLimit(200),
)
if err != nil {
    // 处理错误
}
defer memoryService.Close()
```

**配置选项**：

- `WithTableName(name)`: 表名（默认 "memories"）
- `WithEmbedder(embedder)`: 文本 embedder（必填）
- `WithIndexDimension(dim)`: 向量维度（默认与 embedder 维度一致）
- `WithMaxResults(limit)`: 搜索返回的最大条数（默认 10）
- `WithSoftDelete(enabled)`: 软删除（默认 false）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithSkipDBInit(skip)`: 跳过表初始化
- Auto 模式：`WithExtractor`、`WithAsyncMemoryNum`、`WithMemoryQueueSize`、
  `WithMemoryJobTimeout`
- 工具：`WithCustomTool`、`WithToolEnabled`

**注意事项**：

- 该后端使用 `github.com/mattn/go-sqlite3`，需要 CGO。
- `sqlite-vec` 扩展通过 Go 绑定在进程内编译与注册，运行时无需额外下载
  `.so/.dylib` 文件。

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
- `WithKeyPrefix(prefix)`: 设置 Redis key 前缀。设置后所有 key 都会以 `prefix:` 开头。例如 `prefix` 为 `"myapp"` 时，key `mem:{app:user}` 变为 `myapp:mem:{app:user}`。默认为空（无前缀）。适用于多环境或多服务共享同一 Redis 实例的场景
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 Redis 客户端的额外选项

**注意**：`WithRedisClientURL` 优先级高于 `WithRedisInstance`

**Key 前缀示例**：

```go
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
```

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
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (app_name, user_id, memory_id),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer mysqlService.Close()
```

### MySQL Vector（mysqlvec）存储

**适用场景**：生产环境、MySQL 向量相似度搜索

MySQL Vector 将记忆存储在 MySQL 中，通过 embedding 向量提供语义相似度搜索。
MySQL 9.0+ 使用原生 `VECTOR` 类型，旧版本自动降级为 `BLOB` 存储 + Go 侧余弦相似度计算。

**MySQL 版本要求**：

- **MySQL 5.7.8+**：支持（BLOB 降级路径，Go 侧暴力余弦相似度）
- **MySQL 8.x**：支持（BLOB 降级路径）
- **MySQL 9.0+**：完整支持，使用原生 VECTOR 类型进行数据库侧相似度搜索

```go
import memorymysqlvec "trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

mysqlvecService, err := memorymysqlvec.NewService(
    memorymysqlvec.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysqlvec.WithEmbedder(embedder),
    memorymysqlvec.WithSoftDelete(true),
)
```

**配置选项**：

- `WithMySQLClientDSN(dsn)`: MySQL DSN 连接字符串（推荐，必需 `parseTime=true`）
- `WithMySQLInstance(name)`: 使用预注册的 MySQL 实例
- `WithEmbedder(embedder)`: 文本嵌入器，用于生成向量（必需）
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithIndexDimension(dim)`: 向量维度（默认 1536）
- `WithMaxResults(limit)`: 最大搜索结果数（默认 15）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 MySQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）

**注意**：需要 MySQL 5.7.8+（JSON 列类型）。MySQL 9.0+ 使用原生 VECTOR 支持；MySQL 5.7/8.x 自动降级为 BLOB + Go 侧余弦相似度。不需要额外的向量库。

**表结构**（自动创建，MySQL 9.0+）：

```sql
CREATE TABLE memories (
    memory_id VARCHAR(64) PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_content TEXT NOT NULL,
    topics JSON,
    embedding VECTOR(1536),
    memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP(6) NULL,
    participants JSON,
    location VARCHAR(1024) NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_updated_at (updated_at DESC),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer mysqlvecService.Close()
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
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- 性能索引
CREATE INDEX IF NOT EXISTS memories_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS memories_updated_at ON memories(updated_at DESC);
CREATE INDEX IF NOT EXISTS memories_deleted_at ON memories(deleted_at);
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer postgresService.Close()
```

### pgvector 存储

**适用场景**：生产环境、向量相似度搜索

```go
import memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

pgvectorService, err := memorypgvector.NewService(
    memorypgvector.WithHost("localhost"),
    memorypgvector.WithPort(5432),
    memorypgvector.WithUser("postgres"),
    memorypgvector.WithPassword("password"),
    memorypgvector.WithDatabase("dbname"),
    memorypgvector.WithEmbedder(embedder),
    memorypgvector.WithSoftDelete(true),
)
```

**配置选项**：

- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: 连接参数
- `WithSSLMode(mode)`: SSL 模式（默认 "disable"）
- `WithPostgresInstance(name)`: 使用预注册的 PostgreSQL 实例
- `WithEmbedder(embedder)`: 文本嵌入器，用于生成向量（必需）
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithSchema(schema)`: 指定数据库 schema（默认为 public）
- `WithIndexDimension(dim)`: 向量维度（默认 1536）
- `WithMaxResults(limit)`: 最大搜索结果数（默认 10）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 PostgreSQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）
- `WithHNSWIndexParams(params)`: HNSW 索引参数，用于向量搜索

**注意**：直接连接参数优先级高于 `WithPostgresInstance`。需要 PostgreSQL 中安装 pgvector 扩展。

**表结构**（自动创建）：

```sql
CREATE TABLE memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_content TEXT NOT NULL,
    topics TEXT[],
    embedding vector(1536),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- 性能索引
CREATE INDEX ON memories(app_name, user_id);
CREATE INDEX ON memories(updated_at DESC);
CREATE INDEX ON memories(deleted_at);
CREATE INDEX ON memories USING hnsw (embedding vector_cosine_ops);
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer pgvectorService.Close()
```

### 后端对比与选择

| 特性         | InMemory | SQLite     | SQLiteVec | Redis  | MySQL    | PostgreSQL | pgvector |
| ------------ | -------- | ---------- | -------- | ------ | -------- | ---------- | -------- |
| **持久化**   | ❌       | ✅         | ✅       | ✅     | ✅       | ✅         | ✅       |
| **分布式**   | ❌       | ❌         | ❌       | ✅     | ✅       | ✅         | ✅       |
| **事务**     | ❌       | ✅ ACID    | ✅ ACID  | 部分   | ✅ ACID  | ✅ ACID    | ✅ ACID  |
| **查询**     | 简单     | SQL        | SQL+向量 | 中等   | SQL      | SQL        | SQL+向量 |
| **JSON**     | ❌       | 基础       | 基础     | 基础   | JSON     | JSONB      | JSONB    |
| **性能**     | 极高     | 中高       | 中高     | 高     | 中高     | 中高       | 中高     |
| **配置**     | 零配置   | 简单       | 中等     | 简单   | 中等     | 中等       | 中等     |
| **软删除**   | ❌       | ✅         | ✅       | ❌     | ✅       | ✅         | ✅       |
| **适用场景** | 开发测试 | 本地持久化 | 本地向量 | 高并发 | 企业应用 | 高级特性   | 向量搜索 |

**选择建议**：

```
开发/测试 → InMemory（零配置，快速启动）
本地持久化 → SQLite（单文件数据库，易部署）
本地向量检索 → SQLiteVec（单文件数据库 + embedding）
高并发读写 → Redis（内存级性能）
需要 ACID → MySQL/PostgreSQL（事务保证）
复杂 JSON → PostgreSQL（JSONB 索引和查询）
向量搜索 → pgvector（基于 embedding 的相似度搜索）
审计追踪 → MySQL/PostgreSQL/pgvector/SQLite/SQLiteVec（软删除支持）
```

## 常见问题

### Memory 与 Session 的区别

这是最常见的疑问。Memory 和 Session 解决不同的问题：

| 维度         | Memory（记忆）       | Session（会话）                |
| ------------ | -------------------- | ------------------------------ |
| **定位**     | 长期用户档案         | 临时对话上下文                 |
| **隔离维度** | `<appName, userID>`  | `<appName, userID, sessionID>` |
| **生命周期** | 跨会话持久化         | 单次会话内有效                 |
| **存储内容** | 用户画像、偏好、事实 | 对话历史、消息记录             |
| **数据量**   | 小（几十到几百条）   | 大（几十到几千条消息）         |
| **使用场景** | “记住用户是谁”       | “记住说了什么”                 |

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

Memory ID 基于「内容 + 排序后的主题 + appName + userID」的 SHA256 哈希生成，同一用户下相同内容会产生相同 ID：

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

### 搜索行为说明

搜索行为取决于后端：

- 对 `inmemory` / `redis` / `mysql` / `postgres`：`SearchMemories` 使用**Token 匹配**（不是语义搜索）。
- 对 `pgvector` / `mysqlvec` / `sqlitevec`：`SearchMemories` 使用**向量相似度检索**，并且需要配置 Embedder。

**Token 匹配细节**（非向量后端）：

**英文分词**：转小写 → 过滤停用词（a、the、is 等）→ 空格分割

```go
// 可以找到
记忆："User likes programming"
搜索："programming" ✅ 匹配

// 找不到
记忆："User likes programming"
搜索："coding" ❌ 不匹配（语义相近但词不同）
```

**中文分词**：优先使用 `gse` 词级分词，并补充低权重 CJK
字符 trigram 召回

```go
记忆："用户喜欢编程"
搜索："编程" ✅ 匹配（词级命中）
搜索："写代码" ❌ 不匹配（词不同）
```

**限制**（非向量后端）：

- 这些后端均在**应用层**过滤和排序（\[O(n)\] 复杂度）
- 数据量大时性能受影响
- 不支持语义相似度搜索
- 排序是 **BM25 风格关键词打分 + query coverage + 有序短语加分**，
  仍然属于 lexical search，不是向量语义检索

**建议**：

- 使用明确关键词和主题标签提高命中率
- 如需语义相似度检索，使用 pgvector、mysqlvec 或 sqlitevec 后端

### 软删除的注意事项

**支持情况**：

- ✅ MySQL、PostgreSQL、pgvector、SQLite、SQLiteVec：支持软删除
- ❌ InMemory、Redis：不支持（只有硬删除）

**软删除配置**：

```go
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("..."),
    memorymysql.WithSoftDelete(true), // 启用软删除
)
```

**行为差异**：

| 操作 | 硬删除   | 软删除                               |
| ---- | -------- | ------------------------------------ |
| 删除 | 立即移除 | 设置 `deleted_at` 字段               |
| 查询 | 不可见   | 自动过滤（WHERE deleted_at IS NULL） |
| 恢复 | 无法恢复 | 可手动清除 `deleted_at`              |
| 存储 | 节省空间 | 占用空间                             |

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

**前端工具**（通过 `Tools()` 暴露给 Agent 调用）：

| 工具            | 默认  | 说明                    |
| --------------- | ----- | ----------------------- |
| `memory_search` | ✅ 开 | 按查询搜索记忆          |
| `memory_load`   | ❌ 关 | 加载全部或最近 N 条记忆 |

**后端工具**（默认由提取器在后台使用）：

| 工具            | 默认  | 说明                         |
| --------------- | ----- | ---------------------------- |
| `memory_add`    | ✅ 开 | 添加新记忆（提取器使用）     |
| `memory_update` | ✅ 开 | 更新现有记忆                 |
| `memory_delete` | ✅ 开 | 删除记忆                     |
| `memory_clear`  | ❌ 关 | 清空用户所有记忆（危险操作） |

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

## 外部长时记忆平台集成（`mem0`）

`memory/mem0` 是当前对 [mem0](https://mem0.ai) 的集成实现，适合把长期记忆的提取与存储交给外部托管平台，同时仍然让 Agent 通过标准记忆工具查询结果。

它与上文介绍的内置 Memory 后端不同：`memory/mem0` **不是** 完整的 `memory.Service` 实现，而是采用 ingest-first 模式。Runner 会在每轮对话后把 session transcript 发送给 mem0，由 mem0 在平台侧完成提取，Agent 再通过只读工具读取结果。

**适用场景**：外部长时记忆平台、每轮响应后的后台提取，以及不需要本地 CRUD 写路径的场景。

### 配置示例

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

mem0Svc, err := memorymem0.NewService(
    memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
    memorymem0.WithLoadToolEnabled(true),
)
if err != nil {
    panic(err)
}
defer mem0Svc.Close()

sessionSvc := sessioninmemory.NewSessionService()
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-chat")),
    llmagent.WithTools(mem0Svc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc),
)
defer r.Close()
```

**接入要点**：

- 通过 `llmagent.WithTools(mem0Svc.Tools())` 注册工具
- 通过 `runner.WithSessionIngestor(mem0Svc)` 把 session transcript 交给 mem0
- 不要对该集成使用 `runner.WithMemoryService(...)`

### 为什么用 `WithSessionIngestor(...)`，而不是 `WithMemoryService(...)`

`runner.WithMemoryService(...)` 面向的是实现完整 `memory.Service` 契约的内置 Memory 后端。这个契约除了读接口，还包括 `AddMemory`、`UpdateMemory`、`DeleteMemory`、`ClearMemories`、`EnqueueAutoMemoryJob(...)` 等由框架直接拥有语义的写入与自动提取能力。

`memory/mem0` 的边界不同。它并不把完整的 CRUD 生命周期暴露给框架，而是接收完整的 session transcript，转交给 mem0 做托管提取，然后再通过只读工具把检索能力暴露给 Agent。

使用 `runner.WithSessionIngestor(...)` 可以更准确地表达这层边界：

- Runner 在每轮结束后把完整 session transcript 发送出去
- 记忆提取与存储由 mem0 在服务端完成
- `metadata`、`agent_id`、`run_id` 这类按请求传递的 ingest 字段，可以通过 `session.IngestOption` 透传
- 不会把该集成误解成支持完整框架侧 CRUD 或 preload 的内置后端

简单说，`MemoryService` 表示“框架直接管理记忆”，而 `SessionIngestor` 表示“框架把 transcript 交给外部记忆系统”。`mem0` 属于后者。

### 配置选项

| 选项 | 作用 | 默认值 |
| ---- | ---- | ------ |
| `WithAPIKey(key)` | mem0 API Key，所有请求必需。 | 必填 |
| `WithHost(url)` | 覆盖 mem0 API Host / Base URL。 | `https://api.mem0.ai` |
| `WithOrgProject(orgID, projectID)` | 为 ingest 与读取请求追加 mem0 的 `org_id` / `project_id`。 | 空 |
| `WithAsyncMode(bool)` | 控制 ingest 请求里的 `async_mode`。 | `true` |
| `WithVersion(v)` | 设置 mem0 ingest 请求里的版本字段。 | `v2` |
| `WithTimeout(d)` | HTTP 客户端超时时间。 | `10s` |
| `WithLoadToolEnabled(bool)` | 是否在 `Tools()` 里暴露 `memory_load`。 | `false` |
| `WithAsyncMemoryNum(n)` | 后台 ingest worker 数量。 | `1` |
| `WithMemoryQueueSize(n)` | 每个 worker 的队列长度。 | `10` |
| `WithMemoryJobTimeout(d)` | 队列任务与同步 fallback ingest 的超时时间。 | `30s` |

### 注意事项

- `Tools()` 默认暴露 `memory_search`；`memory_load` 可按需开启。
- 所有读取仍然基于当前 `<appName, userID>` 做隔离。
- Runner 会自动把 session 上下文带入 ingest；如果有需要，也可以通过 `session.WithIngestMetadata`、`session.WithIngestAgentID`、`session.WithIngestRunID` 追加信息。
- 当 mem0 返回结构化 metadata 时，检索结果仍可携带 `Topics`、`Kind`、`EventTime`、`Participants`、`Location` 等字段。
- 使用完成后请调用 `Close()`，确保后台 worker 干净退出。
- 如果你需要完整的 CRUD 工具面，或依赖框架侧 preload，建议优先选择内置 Memory 后端。

## 参考链接

- [Memory 模块源码](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
- [工具驱动模式示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)
- [自动提取模式示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/auto)
- [mem0 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/mem0)
- [生态建设文档](https://github.com/trpc-group/trpc-agent-go/blob/main/docs/mkdocs/zh/ecosystem.md)
- [API 文档](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/memory)
