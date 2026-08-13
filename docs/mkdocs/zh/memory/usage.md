# 使用指南

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
