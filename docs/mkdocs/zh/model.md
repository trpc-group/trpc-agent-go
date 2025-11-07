# Model 模块

## 概述

Model 模块是 tRPC-Agent-Go 框架的大语言模型抽象层，提供了统一的 LLM 接口设计，目前支持 OpenAI 和 Anthropic 兼容的 API 调用。通过标准化的接口设计，开发者可以灵活切换不同的模型提供商，实现模型的无缝集成和调用。该模块已验证兼容公司内外大多数 OpenAI-like 接口。

Model 模块具有以下核心特性：

- **统一接口抽象**：提供标准化的 `Model` 接口，屏蔽不同模型提供商的差异
- **流式响应支持**：原生支持流式输出，实现实时交互体验
- **多模态能力**：支持文本、图像、音频等多模态内容处理
- **完整错误处理**：提供双层错误处理机制，区分系统错误和 API 错误
- **可扩展配置**：支持丰富的自定义配置项，满足不同场景需求

## 快速开始

### 在 Agent 中使用 Model

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

func main() {
    // 1. 创建模型实例
    modelInstance := openai.New("deepseek-chat",
        openai.WithExtraFields(map[string]interface{}{
            "tool_choice": "auto", // 自动选择工具
        }),
    )

    // 2. 配置生成参数
    genConfig := model.GenerationConfig{
        MaxTokens:   intPtr(2000),
        Temperature: floatPtr(0.7),
        Stream:      true, // 启用流式输出
    }

    // 3. 创建 Agent 并集成模型
    agent := llmagent.New(
        "chat-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("一个有用的助手"),
        llmagent.WithInstruction("你是一个智能助手，在需要时使用工具。"),
        llmagent.WithGenerationConfig(genConfig),
        llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
    )

    // 4. 创建 Runner 并运行
    r := runner.NewRunner("app-name", agent)
    eventChan, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("Hello"))
    if err != nil {
        log.Fatal(err)
    }

    // 5. 处理响应事件
    for event := range eventChan {
        // 处理流式响应、工具调用等
    }
}
```

示例代码位于 [examples/runner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)

### 使用方式与平台接入指南

Model 模块支持多种使用方式和平台接入。以下是基于 Runner 示例的常见使用场景：

#### 快速启动

```bash
# 基础使用：通过环境变量配置，直接运行
cd examples/runner
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### 平台接入配置

所有平台的接入方式都遵循相同模式，只需配置不同的环境变量或直接在代码中设置：

**环境变量方式**（推荐）：

```bash
export OPENAI_BASE_URL="平台API地址"
export OPENAI_API_KEY="API密钥"
```

**代码方式**：

```go
model := openai.New("模型名称",
    openai.WithBaseURL("平台API地址"),
    openai.WithAPIKey("API密钥"),
)
```

#### 支持的平台及其配置

以下是各平台的配置示例，分为环境变量配置和代码配置两种方式：

**环境变量配置**

runner 示例中支持通过命令行参数(-model)指定模型名称，实际上是在 `openai.New()` 时传入模型名称。

```bash
# OpenAI 平台
export OPENAI_API_KEY="sk-..."
cd examples/runner
go run main.go -model gpt-4o-mini

# OpenAI API 兼容
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"
cd examples/runner
go run main.go -model deepseek-chat
```

**代码配置方式**

在自己的代码中直接使用 Model 时的配置方式：

```go
model := openai.New("deepseek-chat",
    openai.WithBaseURL("https://api.deepseek.com/v1"),
    openai.WithAPIKey("your-api-key"),
)

// 其他平台配置类似，只需修改模型名称、BaseURL和APIKey，无需额外字段
```

## 核心接口设计

### Model 接口

```go
// Model 是所有语言模型必须实现的接口
type Model interface {
    // 生成内容，支持流式响应
    GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error)

    // 返回模型基本信息
    Info() Info
}

// 模型信息结构
type Info struct {
    Name string // 模型名称
}
```

### Request 结构

```go
// Request 表示发送给模型的请求
type Request struct {
    // 消息列表，包含系统指令、用户输入和助手回复
    Messages []Message `json:"messages"`

    // 生成配置（内联到请求中）
    GenerationConfig `json:",inline"`

    // 工具列表
    Tools map[string]tool.Tool `json:"-"`
}

// GenerationConfig 包含生成参数配置
type GenerationConfig struct {
    // 是否使用流式响应
    Stream bool `json:"stream"`

    // 温度参数 (0.0-2.0)
    Temperature *float64 `json:"temperature,omitempty"`

    // 最大生成令牌数
    MaxTokens *int `json:"max_tokens,omitempty"`

    // Top-P 采样参数
    TopP *float64 `json:"top_p,omitempty"`

    // 停止生成的标记
    Stop []string `json:"stop,omitempty"`

    // 频率惩罚
    FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

    // 存在惩罚
    PresencePenalty *float64 `json:"presence_penalty,omitempty"`

    // 推理努力程度 ("low", "medium", "high")
    ReasoningEffort *string `json:"reasoning_effort,omitempty"`

    // 是否启用思考模式
    ThinkingEnabled *bool `json:"-"`

    // 思考模式的最大令牌数
    ThinkingTokens *int `json:"-"`
}
```

### Response 结构

```go
// Response 表示模型返回的响应
type Response struct {
    // OpenAI 兼容字段
    ID                string   `json:"id,omitempty"`
    Object            string   `json:"object,omitempty"`
    Created           int64    `json:"created,omitempty"`
    Model             string   `json:"model,omitempty"`
    SystemFingerprint *string  `json:"system_fingerprint,omitempty"`
    Choices           []Choice `json:"choices,omitempty"`
    Usage             *Usage   `json:"usage,omitempty"`

    // 错误信息
    Error *ResponseError `json:"error,omitempty"`

    // 内部字段
    Timestamp time.Time `json:"-"`
    Done      bool      `json:"-"`
    IsPartial bool      `json:"-"`
}

// ResponseError 表示 API 级别的错误
type ResponseError struct {
    Message string    `json:"message"`
    Type    ErrorType `json:"type"`
    Param   string    `json:"param,omitempty"`
    Code    string    `json:"code,omitempty"`
}
```

## OpenAI Model

OpenAI Model 用于对接 OpenAI 及其兼容平台，支持流式输出、多模态与高级参数配置，并提供丰富的回调机制、批量处理与重试能力，同时可灵活设置自定义 HTTP Header.

### 配置方式

#### 环境变量方式

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com" # 可选配置，默认为此 BASE URL
```

#### 代码方式

```go
import "trpc.group/trpc-go/trpc-agent-go/model/openai"

m := openai.New(
    "gpt-4o",
    openai.WithAPIKey("your-api-key"),
    openai.WithBaseURL("https://api.openai.com"), // 可选配置，默认为此 BASE URL
)
```

### 直接使用 Model

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func main() {
    // 创建模型实例
    llm := openai.New("deepseek-chat")

    // 构建请求
    temperature := 0.7
    maxTokens := 1000

    request := &model.Request{
        Messages: []model.Message{
            model.NewSystemMessage("你是一个专业的AI助手。"),
            model.NewUserMessage("介绍一下Go语言的并发特性。"),
        },
        GenerationConfig: model.GenerationConfig{
            Temperature: &temperature,
            MaxTokens:   &maxTokens,
            Stream:      false,
        },
    }

    // 调用模型
    ctx := context.Background()
    responseChan, err := llm.GenerateContent(ctx, request)
    if err != nil {
        fmt.Printf("系统错误: %v\n", err)
        return
    }

    // 处理响应
    for response := range responseChan {
        if response.Error != nil {
            fmt.Printf("API错误: %s\n", response.Error.Message)
            return
        }

        if len(response.Choices) > 0 {
            fmt.Printf("回复: %s\n", response.Choices[0].Message.Content)
        }

        if response.Done {
            break
        }
    }
}
```

### 流式输出

```go
// 流式请求配置
request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("你是一个创意故事讲述者。"),
        model.NewUserMessage("写一个关于机器人学习绘画的短故事。"),
    },
    GenerationConfig: model.GenerationConfig{
        Stream: true,  // 启用流式输出
    },
}

// 处理流式响应
responseChan, err := llm.GenerateContent(ctx, request)
if err != nil {
    return err
}

for response := range responseChan {
    if response.Error != nil {
        fmt.Printf("错误: %s", response.Error.Message)
        return
    }

    if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
        fmt.Print(response.Choices[0].Delta.Content)
    }

    if response.Done {
        break
    }
}
```

### 高级参数配置

```go
// 使用高级生成参数
temperature := 0.3
maxTokens := 2000
topP := 0.9
presencePenalty := 0.2
frequencyPenalty := 0.5
reasoningEffort := "high"

request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("你是一个专业的技术文档撰写者。"),
        model.NewUserMessage("解释微服务架构的优缺点。"),
    },
    GenerationConfig: model.GenerationConfig{
        Temperature:      &temperature,
        MaxTokens:        &maxTokens,
        TopP:             &topP,
        PresencePenalty:  &presencePenalty,
        FrequencyPenalty: &frequencyPenalty,
        ReasoningEffort:  &reasoningEffort,
        Stream:           true,
    },
}
```

### 多模态内容

```go
// 读取图像文件
imageData, _ := os.ReadFile("image.jpg")

// 创建多模态消息
request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("你是一个图像分析专家。"),
        {
            Role: model.RoleUser,
            ContentParts: []model.ContentPart{
                {
                    Type: model.ContentTypeText,
                    Text: stringPtr("这张图片中有什么?"),
                },
                {
                    Type: model.ContentTypeImage,
                    Image: &model.Image{
                        Data:   imageData,
                        Format: "jpeg",
                    },
                },
            },
        },
    },
}
```

### 高级功能

#### 1. 回调函数

```go
// 设置请求前回调函数
model := openai.New("deepseek-chat",
    openai.WithChatRequestCallback(func(ctx context.Context, req *openai.ChatCompletionNewParams) {
        // 请求发送前被调用
        log.Printf("发送请求: 模型=%s, 消息数=%d", req.Model, len(req.Messages))
    }),

    // 设置响应回调函数（非流式）
    openai.WithChatResponseCallback(func(ctx context.Context,
        req *openai.ChatCompletionNewParams,
        resp *openai.ChatCompletion) {
        // 收到完整响应时调用
        log.Printf("收到响应: ID=%s, 使用Token=%d",
            resp.ID, resp.Usage.TotalTokens)
    }),

    // 设置流式响应回调函数
    openai.WithChatChunkCallback(func(ctx context.Context,
        req *openai.ChatCompletionNewParams,
        chunk *openai.ChatCompletionChunk) {
        // 收到每个流式响应块时调用
        log.Printf("收到流式块: ID=%s", chunk.ID)
    }),

    // 设置流式完成回调函数
    openai.WithChatStreamCompleteCallback(func(ctx context.Context,
        req *openai.ChatCompletionNewParams,
        acc *openai.ChatCompletionAccumulator,
        streamErr error) {
        // 流式响应完全结束时调用（成功或失败）
        if streamErr != nil {
            log.Printf("流式响应失败: %v", streamErr)
        } else {
            log.Printf("流式响应完成: 原因=%s",
                acc.Choices[0].FinishReason)
        }
    }),
)
```

#### 2. 批量处理（Batch API）

Batch API 是一种异步批量处理技术，用于高效处理大量请求。该功能特别适用于需要处理大规模数据的场景，能够显著降低成本并提高处理效率。

##### 核心特性

- **异步处理**：批量请求异步处理，无需等待即时响应
- **成本优化**：通常比单独请求更具成本效益
- **灵活输入**：支持内联请求和文件输入两种方式
- **完整管理**：提供创建、查询、取消、列表等完整操作
- **结果解析**：自动下载和解析批处理结果

##### 快速开始

**创建批处理任务**：

```go
import (
    openaisdk "github.com/openai/openai-go"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// 创建模型实例
llm := openai.New("gpt-4o-mini")

// 准备批处理请求
requests := []*openai.BatchRequestInput{
    {
        CustomID: "request-1",
        Method:   "POST",
        URL:      string(openaisdk.BatchNewParamsEndpointV1ChatCompletions),
        Body: openai.BatchRequest{
            Messages: []model.Message{
                model.NewSystemMessage("你是一个有用的助手。"),
                model.NewUserMessage("你好"),
            },
        },
    },
    {
        CustomID: "request-2",
        Method:   "POST",
        URL:      string(openaisdk.BatchNewParamsEndpointV1ChatCompletions),
        Body: openai.BatchRequest{
            Messages: []model.Message{
                model.NewSystemMessage("你是一个有用的助手。"),
                model.NewUserMessage("介绍一下 Go 语言"),
            },
        },
    },
}

// 创建批处理任务
batch, err := llm.CreateBatch(ctx, requests,
    openai.WithBatchCreateCompletionWindow("24h"),
)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("批处理任务已创建: %s\n", batch.ID)
```

##### 批处理操作

**查询批处理状态**：

```go
// 获取批处理详情
batch, err := llm.RetrieveBatch(ctx, batchID)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("状态: %s\n", batch.Status)
fmt.Printf("总请求数: %d\n", batch.RequestCounts.Total)
fmt.Printf("已完成: %d\n", batch.RequestCounts.Completed)
fmt.Printf("失败: %d\n", batch.RequestCounts.Failed)
```

**下载和解析结果**：

```go
// 下载输出文件
if batch.OutputFileID != "" {
    text, err := llm.DownloadFileContent(ctx, batch.OutputFileID)
    if err != nil {
        log.Fatal(err)
    }

    // 解析批处理输出
    entries, err := llm.ParseBatchOutput(text)
    if err != nil {
        log.Fatal(err)
    }

    // 处理每个结果
    for _, entry := range entries {
        fmt.Printf("[%s] 状态码: %d\n", entry.CustomID, entry.Response.StatusCode)
        if len(entry.Response.Body.Choices) > 0 {
            content := entry.Response.Body.Choices[0].Message.Content
            fmt.Printf("内容: %s\n", content)
        }
        if entry.Error != nil {
            fmt.Printf("错误: %s\n", entry.Error.Message)
        }
    }
}
```

**取消批处理任务**：

```go
// 取消正在进行的批处理
batch, err := llm.CancelBatch(ctx, batchID)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("批处理任务已取消: %s\n", batch.ID)
```

**列出批处理任务**：

```go
// 列出批处理任务（支持分页）
page, err := llm.ListBatches(ctx, "", 10)
if err != nil {
    log.Fatal(err)
}

for _, batch := range page.Data {
    fmt.Printf("ID: %s, 状态: %s\n", batch.ID, batch.Status)
}
```

##### 配置选项

**全局配置**：

```go
// 在创建模型时配置批处理默认参数
llm := openai.New("gpt-4o-mini",
    openai.WithBatchCompletionWindow("24h"),
    openai.WithBatchMetadata(map[string]string{
        "project": "my-project",
        "env":     "production",
    }),
    openai.WithBatchBaseURL("https://custom-batch-api.com"),
)
```

**请求级配置**：

```go
// 在创建批处理时覆盖默认配置
batch, err := llm.CreateBatch(ctx, requests,
    openai.WithBatchCreateCompletionWindow("48h"),
    openai.WithBatchCreateMetadata(map[string]string{
        "priority": "high",
    }),
)
```

##### 工作原理

Batch API 的执行流程：

```
1. 准备批处理请求（BatchRequestInput 列表）
2. 验证请求格式和 CustomID 唯一性
3. 生成 JSONL 格式的输入文件
4. 上传输入文件到服务端
5. 创建批处理任务
6. 异步处理请求
7. 下载输出文件并解析结果
```

关键设计：

- **CustomID 唯一性**：每个请求必须有唯一的 CustomID 用于匹配输入输出
- **JSONL 格式**：批处理使用 JSONL（JSON Lines）格式存储请求和响应
- **异步处理**：批处理任务在后台异步执行，不阻塞主流程
- **完成窗口**：可配置批处理的完成时间窗口（如 24h）

##### 使用场景

- **大规模数据处理**：需要处理数千或数万条请求
- **离线分析**：非实时的数据分析和处理任务
- **成本优化**：批量处理通常比单独请求更经济
- **定时任务**：定期执行的批量处理作业

##### 使用示例

完整的交互式示例请参考 [examples/model/batch](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/batch)。

#### 3. 重试机制（Retry）

重试机制是一种自动错误恢复技术，用于在请求失败时自动重试。该功能由底层 OpenAI SDK 提供，框架通过配置选项将重试参数传递给 SDK。

##### 核心特性

- **自动重试**：SDK 自动处理可重试的错误
- **智能退避**：遵循 API 的 `Retry-After` 头或使用指数退避
- **可配置性**：支持自定义最大重试次数和超时时间
- **零维护**：无需自定义重试逻辑，由成熟的 SDK 处理

##### 快速开始

**基础配置**：

```go
import (
    "time"

    openaiopt "github.com/openai/openai-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// 创建带重试配置的模型实例
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(3),
        openaiopt.WithRequestTimeout(30*time.Second),
    ),
)
```

##### 可重试的错误

OpenAI SDK 自动重试以下错误：

- **408 Request Timeout**：请求超时
- **409 Conflict**：冲突错误
- **429 Too Many Requests**：速率限制
- **500+ Server Errors**：服务器内部错误（5xx）
- **网络连接错误**：无响应或连接失败

**注意**：SDK 默认最大重试次数为 2 次。

##### 重试策略

**标准重试**：

```go
// 适用于大多数场景的标准配置
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(3),
        openaiopt.WithRequestTimeout(30*time.Second),
    ),
)
```

**速率限制优化**：

```go
// 针对速率限制场景的优化配置
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(5),  // 更多重试次数
        openaiopt.WithRequestTimeout(60*time.Second),  // 更长超时
    ),
)
```

**快速失败**：

```go
// 需要快速失败的场景
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(1),  // 最少重试
        openaiopt.WithRequestTimeout(10*time.Second),  // 短超时
    ),
)
```

##### 工作原理

重试机制的执行流程：

```
1. 发送请求到 LLM API
2. 如果请求失败且错误可重试：
   a. 检查是否达到最大重试次数
   b. 根据 Retry-After 头或指数退避计算等待时间
   c. 等待后重新发送请求
3. 如果请求成功或不可重试，返回结果
```

关键设计：

- **SDK 级实现**：重试逻辑完全由 OpenAI SDK 处理
- **配置传递**：框架通过 `WithOpenAIOptions` 传递配置
- **智能退避**：优先使用 API 返回的 `Retry-After` 头
- **透明处理**：对应用层透明，无需额外代码

##### 使用场景

- **生产环境**：提高服务可靠性和容错能力
- **速率限制**：自动处理 429 错误
- **网络不稳定**：应对临时网络故障
- **服务器错误**：处理临时的服务端问题

##### 重要说明

- **无框架重试**：框架本身不实现重试逻辑
- **客户端级重试**：所有重试由 OpenAI 客户端处理
- **配置透传**：使用 `WithOpenAIOptions` 配置重试行为
- **自动处理**：速率限制（429）自动处理，无需额外代码

##### 使用示例

完整的交互式示例请参考 [examples/model/retry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/retry)。

#### 4. 自定义 HTTP Header

在网关、专有平台或代理环境中，请求模型 API 往往需要额外的
HTTP Header（例如组织/租户标识、灰度路由、自定义鉴权等）。Model 模块
提供两种可靠方式为“所有模型请求”添加 Header，适用于普通请求、流式、
文件上传、批处理等全链路。

推荐顺序：

- 通过 OpenAI RequestOption 设置全局 Header（简单、直观）
- 通过自定义 `http.RoundTripper` 注入（进阶、横切能力更强）

上述两种方式同样影响流式请求，因为底层使用的是同一个客户端。

##### 1. 使用 OpenAI RequestOption 设置全局 Header

通过 `WithOpenAIOptions` 配合 `openaiopt.WithHeader` 或
`openaiopt.WithMiddleware`，可为底层 OpenAI 客户端发起的“每个请求”
注入 Header。

```go
import (
    openaiopt "github.com/openai/openai-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

llm := openai.New("deepseek-chat",
    // 若你的平台要求额外头部
    openai.WithOpenAIOptions(
        openaiopt.WithHeader("X-Custom-Header", "custom-value"),
        openaiopt.WithHeader("X-Request-ID", "req-123"),
        // 也可设置 User-Agent 或厂商特定头
        openaiopt.WithHeader("User-Agent", "trpc-agent-go/1.0"),
    ),
)
```

若需要按条件设置（例如仅对某些路径或依赖调用上下文值），可使用中间件：

```go
llm := openai.New("deepseek-chat",
    openai.WithOpenAIOptions(
        openaiopt.WithMiddleware(
            func(r *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
                // 例：按上下文值设置“每次请求”的头部
                if v := r.Context().Value("x-request-id"); v != nil {
                    if s, ok := v.(string); ok && s != "" {
                        r.Header.Set("X-Request-ID", s)
                    }
                }
                // 或仅对对话补全接口生效
                if strings.Contains(r.URL.Path, "/chat/completions") {
                    r.Header.Set("X-Feature-Flag", "on")
                }
                return next(r)
            },
        ),
    ),
)
```

鉴权差异注意事项：

- OpenAI 风格：保留 `openai.WithAPIKey("sk-...")`，底层会设置
  `Authorization: Bearer ...`。
- Azure/部分 OpenAI 兼容：若要求 `api-key` 头部，则不要调用
  `WithAPIKey`，改为使用
  `openaiopt.WithHeader("api-key", "<key>")`。

##### 2. 使用自定义 http.RoundTripper（进阶）

在 HTTP 传输层统一注入 Header，适合同时需要代理、TLS、自定义监控等
能力的场景。

```go
type headerRoundTripper struct{ base http.RoundTripper }

func (rt headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    // 添加或覆盖头部
    req.Header.Set("X-Custom-Header", "custom-value")
    req.Header.Set("X-Trace-ID", "trace-xyz")
    return rt.base.RoundTrip(req)
}

llm := openai.New("deepseek-chat",
    openai.WithHTTPClientOptions(
        openai.WithHTTPClientTransport(headerRoundTripper{base: http.DefaultTransport}),
    ),
)
```

关于“每次请求”的头部：

- Agent/Runner 会把 `ctx` 透传至模型调用；中间件可从
  `req.Context()` 读取值，从而为“本次调用”注入头部。
- 对“对话补全”而言，目前未暴露单次调用级别的 BaseURL 覆盖；如需切
  换，请新建一个使用不同 BaseURL 的模型，或在中间件中修改 `r.URL`。

## Anthropic Model

Anthropic Model 用于对接 Claude 模型及其兼容平台，支持流式输出、思考模式与工具调用，并提供丰富的回调机制，同时可灵活设置自定义 HTTP Header.

### 配置方式

#### 环境变量方式

```bash
export ANTHROPIC_API_KEY="your-api-key"
export ANTHROPIC_BASE_URL="https://api.anthropic.com" # 可选配置，默认为此 BASE URL
```

#### 代码方式

```go
import "trpc.group/trpc-go/trpc-agent-go/model/anthropic"

m := anthropic.New(
    "claude-sonnet-4-0",
    anthropic.WithAPIKey("your-api-key"),
    anthropic.WithBaseURL("https://api.anthropic.com"), // 可选配置，默认为此 BASE URL
)
```

### 直接使用 Model

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

func main() {
	// 创建模型实例
	llm := anthropic.New("claude-sonnet-4-0")
	// 构建请求
	temperature := 0.7
	maxTokens := 1000
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("你是一个专业的AI助手。"),
			model.NewUserMessage("介绍一下Go语言的并发特性。"),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      false,
		},
	}
	// 调用模型
	ctx := context.Background()
	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		fmt.Printf("系统错误: %v\n", err)
		return
	}
	// 处理响应
	for response := range responseChan {
		if response.Error != nil {
			fmt.Printf("API错误: %s\n", response.Error.Message)
			return
		}
		if len(response.Choices) > 0 {
			fmt.Printf("回复: %s\n", response.Choices[0].Message.Content)
		}
		if response.Done {
			break
		}
	}
}
```

### 流式输出

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

func main() {
	// 创建模型实例
	llm := anthropic.New("claude-sonnet-4-0")
	// 流式请求配置
	temperature := 0.7
	maxTokens := 1000
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("你是一个创意故事讲述者。"),
			model.NewUserMessage("写一个关于机器人学习绘画的短故事。"),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      true,
		},
	}
	// 调用模型
	ctx := context.Background()
	// 处理流式响应
	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		fmt.Printf("系统错误: %v\n", err)
		return
	}
	for response := range responseChan {
		if response.Error != nil {
			fmt.Printf("错误: %s", response.Error.Message)
			return
		}
		if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
			fmt.Print(response.Choices[0].Delta.Content)
		}
		if response.Done {
			break
		}
	}
}
```

### 高级参数配置

```go
// 使用高级生成参数
temperature := 0.3
maxTokens := 2000
topP := 0.9
thinking := true
thinkingTokens := 2048

request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("你是一个专业的技术文档撰写者。"),
        model.NewUserMessage("解释微服务架构的优缺点。"),
    },
    GenerationConfig: model.GenerationConfig{
        Temperature:     &temperature,
        MaxTokens:       &maxTokens,
        TopP:            &topP,
        ThinkingEnabled: &thinking,
        ThinkingTokens:  &thinkingTokens,
        Stream:          true,
    },
}
```

### 高级功能

#### 1. 回调函数

```go
import (
    anthropicsdk "github.com/anthropics/anthropic-sdk-go"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

model := anthropic.New(
    "claude-sonnet-4-0",
    anthropic.WithChatRequestCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams) {
        // Log request before sending.
        log.Printf("sending request: model=%s, messages=%d.", req.Model, len(req.Messages))
    }),
    anthropic.WithChatResponseCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams, resp *anthropicsdk.Message) {
        // Log non-streaming response details.
        log.Printf("received response: id=%s, input_tokens=%d, output_tokens=%d.", resp.ID, resp.Usage.InputTokens, resp.Usage.OutputTokens)
    }),
    anthropic.WithChatChunkCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams, chunk *anthropicsdk.MessageStreamEventUnion) {
        // Log streaming event type.
        log.Printf("stream event: %T.", chunk.AsAny())
    }),
    anthropic.WithChatStreamCompleteCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams, acc *anthropicsdk.Message, streamErr error) {
        // Log stream completion or error.
        if streamErr != nil {
            log.Printf("stream failed: %v.", streamErr)
            return
        }
        log.Printf("stream completed: finish_reason=%s, input_tokens=%d, output_tokens=%d.", acc.StopReason, acc.Usage.InputTokens, acc.Usage.OutputTokens)
    }),
)
```

#### 2. 自定义 HTTP Header

在网关、专有平台或代理环境中，请求模型 API 往往需要额外的HTTP Header（例如组织/租户标识、灰度路由、自定义鉴权等）。Model 模块提供两种可靠方式为“所有模型请求”添加 Header，适用于普通请求、流式、文件上传、批处理等全链路。

推荐顺序：

- 通过 Anthropic RequestOption 设置全局 Header（简单、直观）
- 通过自定义 `http.RoundTripper` 注入（进阶、横切能力更强）

上述两种方式同样影响流式请求，因为底层使用的是同一个客户端，

##### 1. 使用 Anthropic RequestOption 设置全局 Header

通过 `WithAnthropicClientOptions` 配合 `anthropicopt.WithHeader` 或 `anthropicopt.WithMiddleware`，可为底层 Anthropic 客户端发起的每个请求注入 Header。

```go
import (
    anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

llm := anthropic.New("claude-sonnet-4-0",
    // 若你的平台要求额外头部
    anthropic.WithAnthropicClientOptions(
        anthropicopt.WithHeader("X-Custom-Header", "custom-value"),
        anthropicopt.WithHeader("X-Request-ID", "req-123"),
        // 也可设置 User-Agent 或厂商特定头
        anthropicopt.WithHeader("User-Agent", "trpc-agent-go/1.0"),
    ),
)
```

若需要按条件设置（例如仅对某些路径或依赖调用上下文值），可使用中间件：

```go
import (
    anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

llm := anthropic.New("claude-sonnet-4-0",
    anthropic.WithAnthropicClientOptions(
        anthropicopt.WithMiddleware(
            func(r *http.Request, next anthropicopt.MiddlewareNext) (*http.Response, error) {
                // 例：按上下文值设置“每次请求”的头部
                if v := r.Context().Value("x-request-id"); v != nil {
                    if s, ok := v.(string); ok && s != "" {
                        r.Header.Set("X-Request-ID", s)
                    }
                }
                // 或仅对对话补全接口生效
                if strings.Contains(r.URL.Path, "v1/messages") {
                    r.Header.Set("X-Feature-Flag", "on")
                }
                return next(r)
            },
        ),
    ),
)
```

##### 2. 使用自定义 http.RoundTripper

在 HTTP 传输层统一注入 Header，适合同时需要代理、TLS、自定义监控等能力的场景。

```go
import (
    anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

type headerRoundTripper struct{ base http.RoundTripper }

func (rt headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    // 添加或覆盖头部
    req.Header.Set("X-Custom-Header", "custom-value")
    req.Header.Set("X-Trace-ID", "trace-xyz")
    return rt.base.RoundTrip(req)
}

llm := anthropic.New("claude-sonnet-4-0",
    anthropic.WithHTTPClientOptions(
        anthropic.WithHTTPClientTransport(headerRoundTripper{base: http.DefaultTransport}),
    ),
)
```

关于“每次请求”的头部：

- Agent/Runner 会把 `ctx` 透传至模型调用；中间件可从 `req.Context()` 读取值，从而为“本次调用”注入头部。
- 对“对话补全”而言，目前未暴露单次调用级别的 BaseURL 覆盖；如需切换，请新建一个使用不同 BaseURL 的模型，或在中间件中修改 `r.URL`。

## 高级功能

### 1. 模型切换（Model Switching）

模型切换允许在运行时动态更换 Agent 使用的 LLM 模型。框架提供两种方式：Agent 级别切换（影响所有后续请求）和请求级别切换（仅影响单次请求）。

#### Agent 级别切换

Agent 级别切换会改变 Agent 的默认模型，影响所有后续请求。

##### 方式一：直接设置模型实例

通过 `SetModel` 方法直接传入模型实例：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// 创建 Agent
agent := llmagent.New("my-agent",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
)

// 切换到其他模型
agent.SetModel(openai.New("gpt-4o"))
```

**使用场景**：

```go
// 根据任务复杂度选择模型
if isComplexTask {
    agent.SetModel(openai.New("gpt-4o"))  // 使用强大模型
} else {
    agent.SetModel(openai.New("gpt-4o-mini"))  // 使用快速模型
}
```

##### 方式二：按名称切换模型

通过 `WithModels` 预注册多个模型，然后使用 `SetModelByName` 按名称切换：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// 创建多个模型实例
gpt4 := openai.New("gpt-4o")
gpt4mini := openai.New("gpt-4o-mini")
deepseek := openai.New("deepseek-chat")

// 创建 Agent 时注册所有模型
agent := llmagent.New("my-agent",
    llmagent.WithModels(map[string]model.Model{
        "smart": gpt4,
        "fast":  gpt4mini,
        "cheap": deepseek,
    }),
    llmagent.WithModel(gpt4mini), // 指定初始模型
    llmagent.WithInstruction("你是一个智能助手。"),
)

// 运行时按名称切换模型
err := agent.SetModelByName("smart")
if err != nil {
    log.Fatal(err)
}

// 切换到其他模型
err = agent.SetModelByName("cheap")
if err != nil {
    log.Fatal(err)
}
```

**使用场景**：

```go
// 根据用户等级选择模型
modelName := "fast" // 默认使用快速模型
if user.IsPremium() {
    modelName = "smart" // 付费用户使用高级模型
}
if err := agent.SetModelByName(modelName); err != nil {
    log.Printf("切换模型失败: %v", err)
}

// 根据时间段选择模型（成本优化）
hour := time.Now().Hour()
if hour >= 22 || hour < 8 {
    // 夜间使用便宜模型
    agent.SetModelByName("cheap")
} else {
    // 白天使用快速模型
    agent.SetModelByName("fast")
}
```

#### 请求级别切换

请求级别切换允许为单次请求临时指定模型，不影响 Agent 的默认模型和其他请求。这对于需要针对特定任务使用不同模型的场景非常有用。

##### 方式一：使用 WithModel 选项

通过 `agent.WithModel` 为单次请求指定模型实例：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// 为这次请求使用特定模型
eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithModel(openai.New("gpt-4o")),
)
```

##### 方式二：使用 WithModelName 选项（推荐）

通过 `agent.WithModelName` 为单次请求指定预注册的模型名称：

```go
// 创建 Agent 时预注册多个模型
agent := llmagent.New("my-agent",
    llmagent.WithModels(map[string]model.Model{
        "smart": openai.New("gpt-4o"),
        "fast":  openai.New("gpt-4o-mini"),
        "cheap": openai.New("deepseek-chat"),
    }),
    llmagent.WithModel(openai.New("gpt-4o-mini")), // 默认模型
)

runner := runner.NewRunner("app", agent)

// 为这次请求临时使用 "smart" 模型
eventChan, err := runner.Run(ctx, userID, sessionID, message,
    agent.WithModelName("smart"),
)

// 下一次请求仍然使用默认模型 "gpt-4o-mini"
eventChan2, err := runner.Run(ctx, userID, sessionID, message2)
```

**使用场景**：

```go
// 根据消息复杂度动态选择模型
var opts []agent.RunOption
if isComplexQuery(message) {
    opts = append(opts, agent.WithModelName("smart")) // 复杂查询使用强大模型
}

eventChan, err := runner.Run(ctx, userID, sessionID, message, opts...)

// 为推理任务使用专门的推理模型
eventChan, err := runner.Run(ctx, userID, sessionID, reasoningMessage,
    agent.WithModelName("deepseek-reasoner"),
)
```

#### 配置说明

**WithModels 选项**：

- 接受一个 `map[string]model.Model`，key 为模型名称，value 为模型实例
- 如果同时设置了 `WithModel` 和 `WithModels`，`WithModel` 指定初始模型
- 如果只设置了 `WithModels`，将使用 map 中的第一个模型作为初始模型（注意：map 遍历顺序不确定，建议明确指定初始模型）
- 保留名称：`__default__` 是框架内部使用的保留名称，建议不要使用

**SetModelByName 方法**：

- 参数：模型名称（字符串）
- 返回：如果模型名称不存在，返回错误
- 模型必须是通过 `WithModels` 预先注册的

**请求级别选项**：

- `agent.RunOptions.Model`：直接指定模型实例
- `agent.RunOptions.ModelName`：指定预注册的模型名称
- 优先级：`Model` > `ModelName` > Agent 默认模型
- 如果 `ModelName` 指定的模型不存在，将回退到 Agent 默认模型

#### Agent 级别 vs 请求级别对比

| 特性     | Agent 级别切换              | 请求级别切换                   |
| -------- | --------------------------- | ------------------------------ |
| 影响范围 | 所有后续请求                | 仅当前请求                     |
| 使用方式 | `SetModel`/`SetModelByName` | `RunOptions.Model`/`ModelName` |
| 状态变化 | 改变 Agent 默认模型         | 不改变 Agent 状态              |
| 适用场景 | 全局策略调整                | 特定任务临时需求               |
| 并发影响 | 影响所有并发请求            | 不影响其他请求                 |
| 典型用例 | 用户等级、时间段策略        | 复杂查询、推理任务             |

#### Agent 级别切换方式对比

| 特性     | SetModel         | SetModelByName       |
| -------- | ---------------- | -------------------- |
| 使用方式 | 传入模型实例     | 传入模型名称         |
| 预注册   | 不需要           | 需要通过 WithModels  |
| 错误处理 | 无               | 返回 error           |
| 适用场景 | 简单切换         | 复杂场景，多模型管理 |
| 代码维护 | 需要持有模型实例 | 只需要记住名称       |

#### 重要说明

**Agent 级别切换**：

- **即时生效**：调用 `SetModel` 或 `SetModelByName` 后，下一次请求立即使用新模型
- **会话保持**：切换模型不会清除会话历史
- **配置独立**：每个模型保留自己的配置（温度、最大 token 等）
- **并发安全**：两种切换方式都是并发安全的

**请求级别切换**：

- **临时覆盖**：仅影响当前请求，不改变 Agent 的默认模型
- **优先级高**：请求级别的模型设置优先于 Agent 默认模型
- **无副作用**：不影响其他并发请求或后续请求
- **灵活组合**：可以与 Agent 级别切换配合使用

#### 使用示例

完整的交互式示例请参考 [examples/model/switch](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/switch)，该示例演示了 Agent 级别和请求级别两种切换方式。

### 2. Token 裁剪（Token Tailoring）

Token Tailoring 是一种智能的消息管理技术，用于在消息超出模型上下文窗口限制时自动裁剪消息，确保请求能够成功发送到 LLM API。该功能特别适用于长对话场景，能够在保留关键上下文的同时，将消息列表控制在模型的 token 限制内。

#### 核心特性

- **双模式配置**：支持自动模式（automatic）和高级模式（advanced）
- **智能保留**：自动保留系统消息和最后一轮对话
- **多种策略**：提供 MiddleOut、HeadOut、TailOut 三种裁剪策略
- **高效算法**：使用前缀和与二分查找，时间复杂度 O(n)
- **实时统计**：显示裁剪前后的消息数和 token 数

#### 快速开始

**自动模式（推荐）**：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// 只需启用 token tailoring，其他参数自动配置
model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
)
```

自动模式会：

- 自动检测模型的上下文窗口大小
- 计算最佳的 `maxInputTokens`（扣除协议开销和输出预留）
- 使用默认的 `SimpleTokenCounter` 和 `MiddleOutStrategy`

**高级模式**：

```go
// 自定义 token 限制和策略
model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),               // 必需：启用 token tailoring
    openai.WithMaxInputTokens(10000),                    // 自定义 token 限制
    openai.WithTokenCounter(customCounter),              // 可选：自定义计数器
    openai.WithTailoringStrategy(customStrategy),        // 可选：自定义策略
)
```

#### 裁剪策略

框架提供三种内置策略，适用于不同场景：

**MiddleOutStrategy（默认）**：

从中间移除消息，保留头部和尾部：

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

counter := model.NewSimpleTokenCounter()
strategy := model.NewMiddleOutStrategy(counter)

model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithMaxInputTokens(10000),
    openai.WithTailoringStrategy(strategy),
)
```

- **适用场景**：需要保留对话开始和最近上下文的场景
- **保留内容**：系统消息 + 早期消息 + 最近消息 + 最后一轮对话

**HeadOutStrategy**：

从头部移除消息，优先保留最近的消息：

```go
strategy := model.NewHeadOutStrategy(counter)

model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithMaxInputTokens(10000),
    openai.WithTailoringStrategy(strategy),
)
```

- **适用场景**：聊天应用，最近的上下文更重要
- **保留内容**：系统消息 + 最近消息 + 最后一轮对话

**TailOutStrategy**：

从尾部移除消息，优先保留早期的消息：

```go
strategy := model.NewTailOutStrategy(counter)

model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithMaxInputTokens(10000),
    openai.WithTailoringStrategy(strategy),
)
```

- **适用场景**：RAG 应用，初始指令和上下文更重要
- **保留内容**：系统消息 + 早期消息 + 最后一轮对话

#### Token 计数器

**SimpleTokenCounter（默认）**：

基于字符数的快速估算：

```go
counter := model.NewSimpleTokenCounter()
```

- **优点**：快速，无外部依赖，适合大多数场景
- **缺点**：准确度略低于 tiktoken

**TikToken Counter（可选）**：

使用 OpenAI 官方 tokenizer 精确计数：

```go
import "trpc.group/trpc-go/trpc-agent-go/model/tiktoken"

tkCounter, err := tiktoken.New("gpt-4o")
if err != nil {
    // 处理错误
}

model := openai.New("gpt-4o-mini",
    openai.WithEnableTokenTailoring(true),
    openai.WithTokenCounter(tkCounter),
)
```

- **优点**：准确匹配 OpenAI API 的 token 计数
- **缺点**：需要额外依赖，性能略低

#### 工作原理

Token Tailoring 的执行流程：

```
1. 检查是否通过 WithEnableTokenTailoring(true) 启用 token tailoring
2. 计算当前消息的总 token 数
3. 如果超出限制：
   a. 标记必须保留的消息（系统消息 + 最后一轮对话）
   b. 应用选定的策略裁剪中间消息
   c. 确保结果在 token 限制内
4. 返回裁剪后的消息列表
```

**重要说明**：Token tailoring 只有在设置 `WithEnableTokenTailoring(true)` 时才会激活。`WithMaxInputTokens()` 选项仅设置 token 限制，但本身不会启用 tailoring 功能。

关键设计：

- **不修改原始消息**：原始消息列表保持不变
- **智能保留**：自动保留系统消息和最后完整的用户-助手对话对
- **高效算法**：使用前缀和（O(n)）+ 二分查找（O(log n)）

#### 模型上下文注册

对于框架不认识的自定义模型，可以注册其上下文窗口大小以启用自动模式：

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

// 注册单个模型
model.RegisterModelContextWindow("my-custom-model", 8192)

// 批量注册多个模型
model.RegisterModelContextWindows(map[string]int{
    "my-model-1": 4096,
    "my-model-2": 16384,
    "my-model-3": 32768,
})

// 之后可以使用自动模式
m := openai.New("my-custom-model",
    openai.WithEnableTokenTailoring(true), // 自动检测 context window
)
```

**使用场景**：

- 使用私有部署或自定义模型
- 覆盖框架内置的 context window 配置
- 适配新发布的模型版本

#### 使用示例

完整的交互式示例请参考 [examples/tailor](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/tailor)。

### 3. Provider

随着多个大模型供应商的出现，一些供应商定义了各自的 API 规范。目前，框架已接入 OpenAI 和 Anthropic 的 API，并以 Model 的形式提供，用户可以通过 `openai.New` 和 `anthropic.New` 来使用不同供应商的模型。

然而，不同供应商在实例化方式和配置项上存在差异，开发者在切换供应商时往往需要修改大量代码，增加了切换成本。

为了解决这一问题，Provider 提供了一个统一的模型实例化入口。开发者只需指定供应商和模型名称，其他配置项通过统一的 `Option` 管理，从而简化了供应商切换的复杂度。

Provider 支持以下 `Option`：

| Option                                                                                            | 说明                                |
| ------------------------------------------------------------------------------------------------- | --------------------------------- |
| `WithAPIKey` / `WithBaseURL`                                                                      | 设置模型的 API Key 和 Base URL                      |
| `WithHTTPClientName` / `WithHTTPClientTransport`                                                  | 配置 HTTP 客户端属性                     |
| `WithChannelBufferSize`                                                                           | 调整响应 channel 缓冲区容量                |
| `WithCallbacks`                                                                                   | 配置 OpenAI / Anthropic 的请求、响应、流式回调 |
| `WithExtraFields`                                                                                 | 配置请求体自定义字段                        |
| `WithEnableTokenTailoring` / `WithMaxInputTokens`<br>`WithTokenCounter` / `WithTailoringStrategy` | Token 裁剪相关参数                      |
| `WithOpenAI` / `WithAnthropic`                                                                    | 透传供应商原生 Option                    |

#### 使用示例

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/provider"
)

gpt, err := provider.Model("openai", "gpt-4.1-mini", provider.WithChannelBufferSize(512))
if err != nil {
    log.Fatalf("create openai model: %v", err)
}

claude, err := provider.Model("anthropic", "claude-sonnet-4.0", provider.WithChannelBufferSize(512))
if err != nil {
    log.Fatalf("create anthropic model: %v", err)
}

// 根据业务选择模型实例
agent := llmagent.New("chat-assistant", llmagent.WithModel(gpt))
```

完整代码可参见 [examples/runner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)。

#### 注册自定义 Provider

框架支持通过注册自定义 Provider 来接入其他大模型供应商或自定义实现的模型。

通过 `provider.Register` 可以定义根据 Options 创建自定义模型实例的方法。

```go
import "trpc.group/trpc-go/trpc-agent-go/model/provider"

provider.Register("custom-provider", func(opts *provider.Options) (model.Model, error) {
    return newCustomModel(opts.ModelName, WithAPIKey(opts.APIKey)), nil
})

customModel, err := provider.Model("custom-provider", "custom-model")
```
