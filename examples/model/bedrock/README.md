# AWS Bedrock 模型使用示例

本示例演示如何使用 trpc-agent-go 框架的 AWS Bedrock 模型适配器，涵盖从基础对话到高级功能的完整使用场景。

## 前置条件

1. 安装 Go 1.24+
2. 配置 AWS 凭证（以下任一方式）：
   - 环境变量：`AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY`
   - AWS 配置文件：`~/.aws/credentials`
   - IAM Role（EC2/ECS/Lambda 等 AWS 环境）
3. 在 [AWS Bedrock 控制台](https://console.aws.amazon.com/bedrock/) 开通对应模型的访问权限

## 概述

本示例通过 6 大类、18 个子示例，全面展示 Bedrock 模型适配器的使用方式：

| 类别 | 文件 | 示例数 | 说明 |
|------|------|--------|------|
| 基础对话 | `basic.go` | 3 | 单轮对话、系统消息、推理配置 |
| 流式调用 | `streaming.go` | 3 | 文本流式、工具调用流式、混合内容流式 |
| 多轮对话 | `multiturn.go` | 3 | 上下文保持、长对话管理、状态重置 |
| 工具调用 | `toolcall.go` | 4 | 简单/复杂工具、结果处理、错误处理 |
| 高级功能 | `advanced.go` | 2 | 图片处理、并发调用 |
| 错误处理 | `errorhandling.go` | 3 | API 错误、超时控制、重试机制 |

## 支持的模型

本示例支持所有 AWS Bedrock 上可用的模型，包括但不限于：

| 模型 | Model ID | 说明 |
|------|----------|------|
| Mistral Large | `mistral.mistral-large-3-675b-instruct` | 默认模型，支持工具调用 |
| Claude Sonnet 4 | `us.anthropic.claude-sonnet-4-20250514-v1:0` | 支持视觉、工具调用 |
| Claude Haiku | `anthropic.claude-3-haiku-20240307-v1:0` | 快速响应，成本低 |
| Amazon Nova | `amazon.nova-pro-v1:0` | Amazon 自研模型 |
| Meta Llama | `meta.llama3-1-70b-instruct-v1:0` | 开源大模型 |

## 环境变量

| 变量 | 说明 | 必需 |
|------|------|------|
| `AWS_ACCESS_KEY_ID` | AWS 访问密钥 ID | 是（或使用其他凭证方式） |
| `AWS_SECRET_ACCESS_KEY` | AWS 秘密访问密钥 | 是（或使用其他凭证方式） |
| `AWS_SESSION_TOKEN` | AWS 会话令牌（临时凭证） | 否 |
| `AWS_REGION` | AWS 区域（也可通过 `-region` 参数指定） | 否 |

## 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-model` | Bedrock 模型 ID | `mistral.mistral-large-3-675b-instruct` |
| `-region` | AWS 区域 | `us-east-1` |
| `-demo` | 运行的示例类别 | `all` |

## 运行方式

### 运行所有示例

```bash
cd examples/model/bedrock
go run . -demo all
```

### 运行特定类别

```bash
# 基础对话
go run . -demo basic

# 流式调用
go run . -demo streaming

# 多轮对话
go run . -demo multiturn

# 工具调用
go run . -demo tool

# 高级功能
go run . -demo advanced

# 错误处理
go run . -demo error
```

### 组合运行多个类别

```bash
go run . -demo basic,streaming,tool
```

### 指定模型和区域

```bash
# 使用 Mistral Large（美东 1 区）
go run . -model mistral.mistral-large-3-675b-instruct -region us-east-1

# 使用 Claude Sonnet 4（美东 1 区）
go run . -model us.anthropic.claude-sonnet-4-20250514-v1:0 -region us-east-1
```

## 示例详解

### 1. 基础对话 (`basic.go`)

#### 1.1 简单单轮对话

最基本的使用方式，发送一条消息获取回复：

```go
ch, err := m.GenerateContent(ctx, &model.Request{
    Messages: []model.Message{
        model.NewUserMessage("What is AWS Bedrock?"),
    },
})
```

#### 1.2 带系统消息的对话

通过系统消息设定模型角色和行为约束：

```go
ch, err := m.GenerateContent(ctx, &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("You are a senior Go developer."),
        model.NewUserMessage("How to implement graceful shutdown?"),
    },
})
```

#### 1.3 推理配置对比

调整 Temperature、TopP、MaxTokens 等参数控制生成行为：

```go
ch, err := m.GenerateContent(ctx, &model.Request{
    Messages: messages,
    GenerationConfig: model.GenerationConfig{
        Temperature: float64Ptr(0.7),
        TopP:        float64Ptr(0.9),
        MaxTokens:   intPtr(256),
    },
})
```

### 2. 流式调用 (`streaming.go`)

#### 2.1 文本流式对话

实时逐字显示模型回复，提升用户体验：

```go
ch, err := m.GenerateContent(ctx, &model.Request{
    Messages: messages,
    GenerationConfig: model.GenerationConfig{
        Stream: true, // 开启流式
    },
})

for resp := range ch {
    if resp.IsPartial {
        fmt.Print(resp.Choices[0].Delta.Content) // 增量内容
    }
    if resp.Done {
        // 流结束，获取完整响应
    }
}
```

#### 2.2 流式工具调用

在流式模式下处理工具调用，工具参数也是增量返回的：

```go
ch, err := m.GenerateContent(ctx, &model.Request{
    Messages: messages,
    GenerationConfig: model.GenerationConfig{Stream: true},
    Tools:    tools,
})
```

#### 2.3 混合内容流式

同时处理文本和工具调用的混合流式响应。

### 3. 多轮对话 (`multiturn.go`)

#### 3.1 上下文保持

每轮对话将完整消息历史传给模型，实现上下文记忆：

```go
messages := []model.Message{
    model.NewSystemMessage("You are a math tutor."),
}

// 第一轮
messages = append(messages, model.NewUserMessage("What is Pythagorean theorem?"))
// ... 获取回复 ...
messages = append(messages, model.NewAssistantMessage(reply))

// 第二轮（模型能记住之前的内容）
messages = append(messages, model.NewUserMessage("Give me an example."))
```

#### 3.2 长对话历史管理

当对话过长时，截断历史避免超出上下文窗口：

```go
// 保留系统消息 + 最近 N 轮对话
requestMessages := buildTruncatedHistory(allMessages, systemMsg, maxTurns)
```

#### 3.3 对话状态重置

清除历史消息，切换话题或开始新会话。

### 4. 工具调用 (`toolcall.go`)

#### 4.1 简单函数调用

模型判断需要调用工具，客户端执行后返回结果：

```go
tools := map[string]tool.Tool{
    "get_weather": &weatherTool{},
}

// 第一轮：模型返回工具调用请求
ch, err := m.GenerateContent(ctx, &model.Request{
    Messages: messages,
    Tools:    tools,
})

// 执行工具
result, _ := executeTool(ctx, tools, tc.Function.Name, tc.Function.Arguments)

// 第二轮：将工具结果返回模型
messages = append(messages, model.NewToolMessage(tc.ID, tc.Function.Name, result))
```

#### 4.2 复杂多工具并行

模型在一次回复中返回多个工具调用请求，客户端并行执行。

#### 4.3 工具结果处理

解析和展示不同格式的工具返回结果（JSON 对象、数组、嵌套结构）。

#### 4.4 工具调用错误处理

工具执行失败时，将错误信息返回给模型，让模型决定如何处理（重试/换方式/告知用户）。

### 5. 高级功能 (`advanced.go`)

#### 5.1 图片处理对话

发送图片给模型进行视觉分析（需要支持视觉的模型，如 Claude 3 系列）：

```go
userMsg := model.Message{Role: model.RoleUser, Content: "Describe this image."}
userMsg.AddImageData(imgData, "auto", "png")
```

#### 5.2 并发调用

同时发送多个独立请求，提高吞吐量：

```go
var wg sync.WaitGroup
for _, q := range questions {
    wg.Add(1)
    go func(question string) {
        defer wg.Done()
        ch, _ := m.GenerateContent(ctx, &model.Request{...})
        // 处理响应
    }(q)
}
wg.Wait()
```

### 6. 错误处理 (`errorhandling.go`)

#### 6.1 API 错误处理

处理各种 API 错误场景（空消息、无效模型 ID、参数异常）：

```go
for resp := range ch {
    if resp.Error != nil {
        log.Printf("API 错误: [%s] %s", resp.Error.Type, resp.Error.Message)
        // 根据错误类型决定处理方式
    }
}
```

#### 6.2 超时控制

通过 `context.WithTimeout` 设置请求超时：

```go
ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
defer cancel()

ch, err := m.GenerateContent(ctx, request)
```

#### 6.3 重试机制

实现指数退避重试策略，应对临时性错误：

```go
for attempt := 0; attempt <= maxRetries; attempt++ {
    if attempt > 0 {
        delay := baseDelay * time.Duration(1<<(attempt-1))
        time.Sleep(delay)
    }
    ch, err := m.GenerateContent(ctx, request)
    // ...
}
```

## 工具实现 (`tools.go`)

示例中包含以下模拟工具实现：

| 工具 | 说明 | 参数 |
|------|------|------|
| `get_weather` | 天气查询 | `city`（必需）、`unit`（可选） |
| `calculator` | 四则运算 | `operation`、`a`、`b`（均必需） |
| `web_search` | 网页搜索 | `query`（必需）、`type`、`time_range`、`language`、`max_results`（可选） |
| `unreliable_service` | 不稳定服务（用于错误处理演示） | `action`（必需） |

## 项目结构

```
examples/model/bedrock/
├── main.go            # 入口文件，命令行参数解析和示例调度
├── basic.go           # 基础对话示例（单轮、系统消息、推理配置）
├── streaming.go       # 流式调用示例（文本流式、工具调用流式、混合内容）
├── multiturn.go       # 多轮对话示例（上下文保持、长对话管理、状态重置）
├── toolcall.go        # 工具调用示例（简单/复杂工具、结果处理、错误处理）
├── advanced.go        # 高级功能示例（图片处理、并发调用）
├── errorhandling.go   # 错误处理示例（API 错误、超时控制、重试机制）
├── tools.go           # 工具实现（天气、计算器、搜索、不稳定服务）
├── helpers.go         # 辅助函数（指针创建、格式化打印）
├── go.mod             # 模块依赖定义
└── go.sum             # 依赖校验和
```

## 快速开始

```go
package main

import (
    "context"
    "fmt"

    awsconfig "github.com/aws/aws-sdk-go-v2/config"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/bedrock"
)

func main() {
    ctx := context.Background()

    // 1. 加载 AWS 配置
    cfg, _ := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"))

    // 2. 创建 Bedrock 模型
    m := bedrock.New("mistral.mistral-large-3-675b-instruct", bedrock.WithAWSConfig(cfg))

    // 3. 发送请求
    ch, _ := m.GenerateContent(ctx, &model.Request{
        Messages: []model.Message{
            model.NewSystemMessage("You are a helpful assistant."),
            model.NewUserMessage("Hello!"),
        },
        GenerationConfig: model.GenerationConfig{
            MaxTokens: intPtr(256),
        },
    })

    // 4. 处理响应
    for resp := range ch {
        if resp.Error != nil {
            fmt.Printf("错误: %s\n", resp.Error.Message)
            return
        }
        if resp.Done && len(resp.Choices) > 0 {
            fmt.Println(resp.Choices[0].Message.Content)
        }
    }
}

func intPtr(i int) *int { return &i }
```

## 注意事项

- **凭证安全**：切勿将 AWS 凭证提交到版本控制系统
- **区域选择**：确保所选区域已开通对应模型的访问权限
- **模型兼容性**：不同模型支持的功能不同（如视觉功能仅部分模型支持）
- **费用控制**：注意 `MaxTokens` 设置，避免产生意外费用
- **并发限制**：注意 Bedrock 的 API 调用频率限制（RPM/TPM）
