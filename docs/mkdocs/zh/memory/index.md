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
| **可用工具** | 默认暴露 `memory_add`、`memory_update`、`memory_search`、`memory_load`；delete/clear 可配置 | 默认暴露 `memory_search`；`memory_load` 启用后暴露；已启用的写操作由提取器使用，但不对 Agent 暴露，除非显式配置 |
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
    modelInstance := openai.New("deepseek-v4-flash")
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

```text
用户：我叫张三，在腾讯工作。

Agent：你好张三！很高兴认识你。我会记住你在腾讯工作。

🔧 工具调用：memory_add
   参数：{"memory": "用户叫张三，在腾讯工作", "topics": ["姓名", "工作"]}
✅ 记忆添加成功。

Agent：我已经保存了这些信息。今天有什么可以帮你的？
```

### 自动提取模式配置（Auto Mode，推荐）

自动提取模式下，基于 LLM 的提取器分析对话并自动创建记忆。**关键配置差异在步骤 1：多配置一个 Extractor**。配置 Extractor 后，`Tools()` 会按 Auto 模式规则暴露工具，Runner 会在响应后触发后台记忆提取。

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
    extractorModel := openai.New("deepseek-v4-flash")
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
    chatModel := openai.New("deepseek-v4-flash")
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

```text
用户：我叫张三，在腾讯工作。

Agent：你好张三！很高兴认识腾讯的朋友。今天有什么可以帮你的？

（后台：提取器分析对话并自动创建记忆，用户无感知）
```

### 可选的自动更新策略

内置提取器只有在显式配置策略时才启用新行为。未配置 option 的现有应用
继续使用历史逻辑，不需要迁移：

```go
// 保持原有行为。
memExtractor := extractor.NewExtractor(extractorModel)
```

需要尽量保留长期历史时，可以显式开启 update policy：

```go
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
)
```

Policy 是内置 extractor 的能力，Auto memory worker 在构造时读取并固定该配置。
`Metadata()` 只提供描述信息，不参与运行时控制。透明 decorator 可以实现
`UnwrapMemoryExtractor() extractor.MemoryExtractor`，让 worker 读取被包装的内置
extractor 能力；支持多层合作式 decorator。自定义 extractor、不合作的 decorator、
返回 nil 或形成循环的 unwrap 均安全回退到 Merge Similar。

Update policy 只约束后台 Auto extraction 产生的操作。Agent 或应用显式调用
`memory_update` 时，工具语义保持不变。

| Update policy | Auto extraction 行为 |
| --- | --- |
| `UpdatePolicyMergeSimilar` | 使用现有的相似度 reconcile 逻辑；这是默认值。 |
| `UpdatePolicyPreserveHistory` | 完全重复时不写入；只更新无冲突的增量信息；变化内容单独追加；extractor prompt 仅允许在用户明确请求时选择 delete/clear。 |
| `UpdatePolicyAppendOnly` | 最终只产生非重复 add：update 转为 add，delete/clear 被过滤。 |

Merge Similar 在检索 existing memories 时保持原有的 user-only query。
Preserve History 和 Append Only 使用 user 与 assistant 的对话文本检索候选，
但排除 tool protocol message；该 query 使用 UTF-8 安全的方式限制为 7 KiB。

Preserve History 的候选 reconcile 只比较已经提供给 extractor 的 existing entries。
精确重复检查还会考虑同一次 extraction 中已经接受的 operation，但不会合并不同的
operation。检索分数只能用于候选排序，不能单独决定 update 或丢弃。事件身份、
有意义的旧 token、数值、日期、否定关系、参与者和地点必须兼容；topics 只有在
update 已通过检查后才合并。
方向性 token coverage 的边界（旧记忆 `0.95`、候选记忆 `0.70`）是保守的实现
启发式，并非通过 benchmark 调参得到。无法确认属于安全补充时，该策略会将候选
保留为独立记忆。
例如，同一次且同一日期的访问补充具体时刻可以更新；更换雇主或另一个日期的访问
会追加为新条目。Preserve History 对 Delete 和 Clear 使用与 Merge Similar 相同的
运行时处理：extractor 选中的 operation 会原样通过。策略专用 prompt 要求模型仅在
用户明确提出有范围的遗忘请求时选择 Delete，并仅在用户明确要求遗忘全部存储信息时
选择 Clear。worker 不再使用正则表达式重新解释自然语言中的删除意图。

该 update policy 不会修改 `memory.Service`、`MemoryExtractor`、持久化 JSON、memory ID
或数据库 schema，也不会重写存量记忆。所有 policy 都保持 Auto memory 原有的
best-effort 持久化行为：单个写入失败会记录日志，后续 operation 继续执行；批次处理
完成后推进 session extraction watermark。

回退时删除该 option，或将其设置为 `UpdatePolicyMergeSimilar` 即可，不需要数据迁移。

### 可选的 Assistant Episode 提取

Auto extraction 默认使用标准 fact 和 episode 工具。如果应用还需要在后续对话中
召回 assistant 曾经提供的可复用信息，可以在构造 extractor 时显式开启 assistant
episode 提取：

```go
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithAssistantEpisodeExtraction(),
)
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
)
```

该 option 使用两个彼此隔离的提取阶段。第一阶段继续使用标准 memory tools；如果
配置了 `WithUpdatePolicy` 或 enabled tools，也会应用对应的工具约束。Assistant
消息会作为上下文保留，用于理解指代、确认和简短的 user 回复，但只有 user 消息
能够提供或授权普通 memory operation。收集 session delta 时，功能开启后只使用每个
模型响应事件的 primary choice，不会把同一响应的备选 choice 当作连续的 assistant
回复。随后，提取器会按时间顺序检查当前 extraction delta 中符合条件的
user/assistant pair。确定性预筛选与语言无关，只检查响应形态而不匹配请求关键词：
assistant 响应包含至少两个 Markdown 或编号列表项，或者引入了 user 请求中没有的
数值 token 时，该 pair 会进入第二阶段。第二阶段 prompt 再对结果是否持久、可复用
作最终语义判断。没有列表或新数值的单段 prose 结果目前不会触发第二阶段。
写入生成的 episode 前，确定性护栏会将 ASCII 数字及其紧邻的正负号、货币符号和百分号
与原始回答对照。自然语言单位和货币名称由第二阶段 prompt 负责，prompt 会要求按原文保留。
Eligible pair 按时间顺序进入私有的单次请求 pair 数量和 source 体积预算；超出预算的候选会被忽略，因为
assistant 结果提取采用 best-effort 语义。入选的 pair 会合并到一次第二阶段请求中，
并且只暴露私有的 `memory_assistant_episode` 工具。该工具不会暴露给应用的 Agent。
通过 `WithPrompt` 或 `SetPrompt` 配置的应用提取约束同样适用于第二阶段请求。普通
阶段产生 Clear operation 时，其请求内 `source_user_index` 对应位置及之前的候选 pair
会被排除。Delete operation 只排除请求内 `affected_source_user_indexes` 明确列出的
pair，不影响无关 pair。如果破坏性 operation 的请求内作用范围缺失或非法，提取器会
保守地跳过本次可选阶段。该判断使用请求内的 operation 来源信息，不依赖针对特定
语言的对话意图解析。

Assistant 输出会被记录为“带归属的对话历史”，而不是已经验证的事实或用户偏好。
框架将每个通过校验的调用固定转换为普通 `KindEpisode` add 操作，将 participants
设置为 `User` 和 `Assistant`；如果 extraction context 带有 reference date，则将其
作为 event time。下面的正文前缀只用于描述，不承载可信来源，也不会赋予特殊的
reconcile 行为。例如：

```json
{
  "memory": "Assistant-provided conversation episode: 当用户询问适合紧凑厨房的产品时，assistant 推荐了 Alpha 和 Beta。",
  "kind": "episode",
  "participants": ["User", "Assistant"]
}
```

第二阶段模型只能提供 episode 正文和可选的检索 topics，不能覆盖 memory kind、
participants、event time 或 location。正文为空、超过 4,096 bytes，或者 ASCII
数值的规范化数值及紧邻的正负号、货币符号或百分号无法在当前 conversation pair 中
找到依据时，调用会被拒绝。自然语言单位和货币名称由第二阶段 prompt 约束，不属于
确定性校验器的检查范围。为了限制每条
source message 对可选请求的体积贡献，每条消息都会被表示为确定性的 8,192-byte
摘录，并保留首尾内容。

Assistant 请求使用早于父 extraction deadline 结束的子 deadline，为第一阶段普通
operation 的持久化预留时间。子 deadline 到期、可选请求失败或 callback 失败都只会
记录日志，并保留第一阶段已经生成的普通 operation；只有父 context 真正取消时才会
中止整个提取。工具参数非法、正文过长或数量缺少依据等确定性模型输出拒绝只跳过对应
的 assistant episode；同一响应中某一 pair 的无效调用不会丢弃其他 pair 的有效调用。

该功能与存储后端无关，不会新增 memory kind、字段、数据库列、数据表或迁移。同一
delta 中入选的 pair 共用一次第二阶段模型请求，因此每个 delta 最多调用模型两次：
一次普通提取和一次 assistant 提取。普通对话仍只有一次
extraction 调用。该 option 在 extractor 生命周期内不可修改，并由 Auto memory
worker 在构造时读取和固定；它不会改变 extractor 的描述性 `Metadata()`。需要关闭
时，应重新构造未传入该 option 的 extractor 和 memory service。透明 decorator
可以实现 `UnwrapMemoryExtractor() extractor.MemoryExtractor` 以保留此配置；自定义
extractor 或不合作的 decorator 使用普通单阶段提取。返回 nil 或形成循环的 unwrap
也会安全回退为关闭状态。此前已经
保存的 assistant episode 仍是普通 episodic memory，会继续参与正常检索、提取上下文
和 reconcile。可选阶段产生的 operation 与其他 memory operation 一样，经过所选
update policy 和 reconcile 路径。

### 两种模式配置对比

| 步骤         | 工具驱动模式（Agentic）             | 自动提取模式（Auto）                   |
| ------------ | ----------------------------------- | -------------------------------------- |
| **步骤 1**   | `NewMemoryService()`                | `NewMemoryService(WithExtractor(ext))` |
| **步骤 2**   | `WithTools(memoryService.Tools())`  | `WithTools(memoryService.Tools())`     |
| **步骤 3**   | `WithMemoryService(memoryService)`  | `WithMemoryService(memoryService)`     |
| **可用工具** | 默认 add/update/search/load；delete/clear 可配置 | 默认暴露 search；load 启用后暴露；已启用的写操作由提取器使用，但不对 Agent 暴露，除非显式配置 |
| **记忆创建** | Agent 主动调用工具                  | 后台自动提取                           |

## 核心概念

[memory 模块](https://github.com/trpc-group/trpc-agent-go/tree/main/memory) 是 tRPC-Agent-Go 框架的记忆管理核心，提供完整的记忆存储和检索能力。

### 架构设计

Memory 模块采用分层设计，由以下核心组件组成：

```text
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
│  • SQLiteVec: SQLite + 向量检索（本地语义搜索）             │
│  • Redis: 高性能缓存（生产环境）                            │
│  • MySQL: 关系型数据库（ACID 保证）                        │
│  • MySQLVec: MySQL + 向量检索（语义搜索）                  │
│  • PostgreSQL: 关系型数据库（JSONB 支持）                  │
│  • pgvector: PostgreSQL + 向量检索（语义搜索）              │
│  • ChromaDB: REST 向量数据库（余弦与混合检索）             │
└─────────────────────────────────────────────────────────────┘
```

**工作流程**：

1. Agent 通过 Memory Tools 与 Memory Service 交互
2. Memory Service 管理记忆的生命周期（CRUD 操作）
3. 记忆以 Entry 形式存储，包含内容、主题、时间戳等
4. Memory ID 通过内容、用户维度和规范化事件元数据的 SHA256 哈希生成，确保幂等性
5. Storage Backends 提供持久化，支持多种存储选项

### 核心组件

| 组件                | 描述                                      | 技术细节                                           |
| ------------------- | ----------------------------------------- | -------------------------------------------------- |
| **Memory Service**  | 核心记忆管理服务，提供 CRUD 能力          | 实现统一 Service 接口，支持多种存储后端            |
| **UserKey**         | 用户标识符，由 `appName` 和 `userID` 组成 | 记忆隔离的最小单位，确保应用/用户间记忆不干扰      |
| **Entry**           | 记忆条目，包含完整记忆信息                | 包括 ID、内容、主题、created_at、updated_at 字段   |
| **Memory ID**       | 记忆的唯一标识符                          | 基于内容、用户维度和规范化事件元数据的 SHA256 哈希；主题不参与身份 |
| **Topics**          | 记忆的主题标签                            | 用于分类和检索，支持多个标签                       |
| **Memory Tools**    | Agent 可调用的记忆操作工具                | 包括 add、update、delete、search、load、clear      |
| **Storage Backend** | 存储后端实现                              | 支持 InMemory、SQLite、SQLiteVec、Redis、MySQL、MySQLVec、PostgreSQL、pgvector、ChromaDB |

### 关键流程

#### 记忆的生命周期

```text
┌──────────────┐
│ 1. 创建记忆   │  用户对话 → Agent 判断 → 调用 memory_add
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ 2. 生成 ID   │  SHA256（内容 + 用户维度 + 事件元数据） → 唯一标识符
└──────┬───────┘
       │
       ↓
┌──────────────┐
│ 3. 存储记忆   │  Entry → Storage Backend（InMemory/SQLite/SQLiteVec/Redis/MySQL/MySQLVec/PostgreSQL/pgvector/ChromaDB）
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
4. 返回匹配的记忆，优先按相关性得分排序，分数相同时再按更新时间排序

#### 记忆 ID 生成策略

记忆 ID 基于内容、appName、userID 和规范化事件元数据的 SHA256 哈希生成；主题不参与 ID，因此调整标签不会生成新记忆：

```go
// 生成逻辑（伪代码，省略错误处理）
content := "memory:" + 记忆内容
content += "|app:" + appName
content += "|user:" + userID
if kind 需要参与身份 {
    content += "|kind:" + kind
}
if eventTime != nil {
    content += "|event_time:" + eventTime.UTC().Format(time.RFC3339)
}
if len(participants) > 0 {
    content += "|participants:" + join(排序并去重后的 participants, ",")
}
if location != "" {
    content += "|location:" + strings.TrimSpace(location)
}
memoryID := SHA256(content) // 64 位十六进制字符串
```

**特性**：

- **幂等性**：内容和身份元数据都相同时，重复添加不会创建新记忆，而是覆盖更新
- **一致性**：内容和身份元数据相同，即使记录的创建时间不同也会产生相同 ID；
  如果 `eventTime`、`participants`、`location` 等身份元数据变化，ID 也会变化
- **去重**：天然支持去重，避免冗余存储

## 存储后端

具体配置请查看对应后端文档。通用的 Agent 接入、提取模式和工具行为请参阅
[使用与配置](usage.md)。

- [内存存储](inmemory.md)
- [SQLite](sqlite.md)
- [SQLiteVec](sqlitevec.md)
- [Redis](redis.md)
- [MySQL](mysql.md)
- [MySQL Vector](mysqlvec.md)
- [PostgreSQL](postgres.md)
- [pgvector](pgvector.md)
- [ChromaDB](chromadb.md)

## 后端对比与选择

| 特性         | InMemory | SQLite     | SQLiteVec | Redis  | MySQL    | MySQLVec  | PostgreSQL | pgvector | ChromaDB    |
| ------------ | -------- | ---------- | --------- | ------ | -------- | --------- | ---------- | -------- | ----------- |
| **持久化**   | ❌       | ✅         | ✅        | ✅     | ✅       | ✅        | ✅         | ✅       | ✅          |
| **分布式**   | ❌       | ❌         | ❌        | ✅     | ✅       | ✅        | ✅         | ✅       | ✅          |
| **事务**     | ❌       | ✅ ACID    | ✅ ACID   | 部分   | ✅ ACID  | ✅ ACID   | ✅ ACID    | ✅ ACID  | 尽力保证    |
| **查询**     | 简单     | SQL        | SQL+向量  | 中等   | SQL      | SQL+向量  | SQL        | SQL+向量 | 向量+本地   |
| **JSON**     | ❌       | 基础       | 基础      | 基础   | JSON     | JSON      | JSONB      | JSONB    | Metadata    |
| **性能**     | 极高     | 中高       | 中高      | 高     | 中高     | 中高      | 中高       | 中高     | 高          |
| **配置**     | 零配置   | 简单       | 中等      | 简单   | 中等     | 中等      | 中等       | 中等     | 中等        |
| **软删除**   | ❌       | ✅         | ✅        | ❌     | ✅       | ✅        | ✅         | ✅       | ✅          |
| **适用场景** | 开发测试 | 本地持久化 | 本地向量  | 高并发 | 企业应用 | MySQL 向量 | 高级特性   | 向量搜索 | 向量服务    |

**选择建议**：

```text
开发/测试 → InMemory（零配置，快速启动）
本地持久化 → SQLite（单文件数据库，易部署）
本地向量检索 → SQLiteVec（单文件数据库 + embedding）
高并发读写 → Redis（内存级性能）
需要 ACID → SQLite/SQLiteVec/MySQL/PostgreSQL（事务保证）
复杂 JSON → PostgreSQL（JSONB 索引和查询）
MySQL 向量检索 → MySQLVec（可用时使用原生 VECTOR，否则使用 BLOB 降级路径）
向量搜索 → pgvector（基于 embedding 的相似度搜索）
向量服务 → ChromaDB（基于 REST 的余弦与混合检索）
审计追踪 → MySQL/MySQLVec/PostgreSQL/pgvector/ChromaDB/SQLite/SQLiteVec（软删除支持）
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

Memory ID 基于「内容 + appName + userID + 规范化事件元数据」的 SHA256 哈希生成；主题不参与 ID，因此同一用户下相同内容即使 topics 改变也会产生相同 ID：

```go
// 第一次添加
memory.AddMemory(ctx, userKey, "用户喜欢编程", []string{"爱好"})
// 生成 ID：abc123...

// 第二次添加相同内容，但使用不同 topics
memory.AddMemory(ctx, userKey, "用户喜欢编程", []string{"兴趣"})
// 生成相同 ID：abc123...，覆盖更新，刷新 topics 和 updated_at
```

**影响**：

- ✅ **天然去重**：避免冗余存储
- ✅ **幂等操作**：内容和身份元数据不变时，重复添加不会创建多条记录
- ⚠️ **覆盖更新**：无法追加相同内容（如需追加，可在内容中加时间戳或序号）

### 搜索行为说明

搜索行为取决于后端：

- 对 `inmemory` / `redis` / `mysql` / `postgres`：`SearchMemories` 使用 **BM25 风格 lexical 关键词匹配**（不是语义搜索）。
- 对 `pgvector` / `mysqlvec` / `sqlitevec`：`SearchMemories` 使用**向量相似度检索**，并且需要配置 Embedder。
- 对 `chromadb`：`SearchMemories` 使用 ChromaDB 向量检索，并支持 kind 回退和混合检索。

**Lexical 匹配细节**（非向量后端）：

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
- 如需语义相似度检索，使用 pgvector、mysqlvec、sqlitevec 或 ChromaDB 后端

### 软删除的注意事项

**支持情况**：

- ✅ MySQL、MySQLVec、PostgreSQL、pgvector、SQLite、SQLiteVec、ChromaDB：支持软删除
- ❌ InMemory、Redis：不支持（只有硬删除）

**软删除配置**：

```go
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("..."),
    memorymysql.WithSoftDelete(true), // 启用软删除
)
if err != nil {
    panic(err)
}
```

**行为差异**：

| 操作 | 硬删除   | 软删除                               |
| ---- | -------- | ------------------------------------ |
| 删除 | 立即移除 | 设置 `deleted_at` 字段               |
| 查询 | 不可见   | 自动过滤（WHERE deleted_at IS NULL） |
| 恢复 | 无法恢复 | 重新 Add，或将更新轮转到相同 ID      |
| 存储 | 节省空间 | 占用空间                             |

当新记忆的规范 ID 与 tombstone 相同时，`AddMemory` 会重新激活该记录。
`UpdateMemory` 不能以软删除记录作为 source；只有从 active source 更新后，内容和
身份元数据轮转到软删除 target 的规范 ID 时，才会重新激活该 target。

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
    // 生产环境的 POSTGRES_DSN 应使用 sslmode=verify-full。
    memorypostgres.WithPostgresClientDSN(os.Getenv("POSTGRES_DSN")),

    // 启用软删除（便于恢复）
    memorypostgres.WithSoftDelete(true),

    // 合理限制
    memorypostgres.WithMemoryLimit(1000),
)
if err != nil {
    panic(err)
}
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
- [工具驱动模式示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)
- [自动提取模式示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/auto)
- [mem0 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/mem0)
- [TencentDB Agent Memory 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/tencentdb)
- [TencentDB Agent Memory SDK](https://github.com/TencentCloud/TencentDB-Agent-Memory)
- [生态建设文档](https://github.com/trpc-group/trpc-agent-go/blob/main/docs/mkdocs/zh/ecosystem.md)
- [API 文档](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/memory)
