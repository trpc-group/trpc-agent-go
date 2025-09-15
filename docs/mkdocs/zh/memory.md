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

记忆服务可在代码中通过选项配置，支持内存和 Redis 两种后端：

#### 记忆服务配置示例

```go
import (
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
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

// 向 Agent 注册记忆工具
llmAgent := llmagent.New(
    "memory-assistant",
    llmagent.WithTools(memService.Tools()), // 或 redisService.Tools()
)

// 在 Runner 中设置记忆服务
runner := runner.NewRunner(
    "app",
    llmAgent,
    runner.WithMemoryService(memService), // 或 redisService
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

# 参数说明：
# -memory: 选择记忆服务类型 (inmemory, redis)，默认为 inmemory
# -redis-addr: Redis 服务器地址，默认为 localhost:6379
# -model: 选择模型名称，默认为 deepseek-chat
```
