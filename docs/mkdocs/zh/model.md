# Model 模块

## 概述

Model 模块是 tRPC-Agent-Go 框架的大语言模型抽象层，提供了统一的 LLM 接口设计，目前支持 OpenAI 兼容的 API 调用。通过标准化的接口设计，开发者可以灵活切换不同的模型提供商，实现模型的无缝集成和调用。该模块已验证兼容公司内外大多数 OpenAI-like 接口。

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

## 高级功能

### 1. 回调函数

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
