# Embedder

Embedder 负责将文本转换为向量表示，是 Knowledge 系统的核心组件之一。

## 支持的 Embedding 模型

trpc-agent-go 支持多种 Embedding 模型平台：


| 平台类型    | 包路径                           | 说明                                                                                         |
| ------------- | ---------------------------------- | ---------------------------------------------------------------------------------------------- |
| OpenAI      | `knowledge/embedder/openai`      | OpenAI embedding 模型（text-embedding-3-small 等），支持兼容 OpenAI Embedding API 的所有平台 |
| Gemini      | `knowledge/embedder/gemini`      | Google Gemini embedding 模型                                                                 |
| Ollama      | `knowledge/embedder/ollama`      | 本地 Ollama 模型，适合离线部署                                                               |
| HuggingFace | `knowledge/embedder/huggingface` | HuggingFace Text Embedding Inference 模型                                                    |

## OpenAI Embedder

OpenAI Embedder 是最常用的选择，支持 OpenAI 官方服务及兼容 OpenAI API 的第三方服务。

**环境变量配置**

OpenAI Embedder 会自动从环境变量中读取配置，无需在代码中硬编码敏感信息：

* `OPENAI_API_KEY`: API 密钥（如果在代码中未提供 `WithAPIKey`，则自动读取此环境变量）
* `OPENAI_BASE_URL`: Base URL（可选，用于兼容 OpenAI 接口的第三方服务）

**代码示例**

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// OpenAI Embedder 配置
embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // 默认模型
)

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
)
```

**配置选项 (Options)**


| 选项                             | 说明                                        | 默认值                   |
| ---------------------------------- | --------------------------------------------- | -------------------------- |
| `WithModel(model string)`        | 设置 embedding 模型                         | `text-embedding-3-small` |
| `WithDimensions(dim int)`        | 设置向量维度 (仅 text-embedding-3 系列有效) | `1536`                   |
| `WithEncodingFormat(fmt string)` | 设置编码格式 ("float", "base64")            | `"float"`                |
| `WithUser(user string)`          | 设置用户标识                                | -                        |
| `WithAPIKey(key string)`         | 设置 API Key (优先于环境变量)               | -                        |
| `WithOrganization(org string)`   | 设置组织 ID (优先于环境变量)                | -                        |
| `WithBaseURL(url string)`        | 设置 Base URL                               | -                        |
| `WithRequestOptions(opts...)`    | 设置额外请求选项                            | -                        |
| `WithMaxRetries(n int)`          | 设置最大重试次数                            | `2` |
| `WithRetryBackoff(durations)`    | 设置重试退避时间（每次重试的等待时间），如果重试次数超过 backoff 数组长度，则其余重试使用最后一个值 | `[100ms, 200ms, 400ms, 800ms]` |

## Gemini Embedder

Google Gemini Embedder 使用 Google GenAI SDK。

**环境变量配置**

* `GOOGLE_API_KEY`: API 密钥（如果在代码中未提供 `WithAPIKey`，则自动读取此环境变量）

**代码示例**

```go
import (
    "context"
    "log"

    geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
)

embedder, err := geminiembedder.New(context.Background())
if err != nil {
    log.Fatalf("Failed to create gemini embedder: %v", err)
}
```

**配置选项 (Options)**


| 选项                        | 说明                                               | 默认值                       |
| ----------------------------- | ---------------------------------------------------- | ------------------------------ |
| `WithModel(model string)`   | 设置模型                                           | `gemini-embedding-exp-03-07` |
| `WithDimensions(dim int)`   | 设置维度                                           | `1536`                       |
| `WithTaskType(type string)` | 设置任务类型 (如`RETRIEVAL_QUERY`)                 | `RETRIEVAL_QUERY`            |
| `WithTitle(title string)`   | 设置标题 (仅当 TaskType 为`RETRIEVAL_DOCUMENT` 时) | -                            |
| `WithAPIKey(key string)`    | 设置 API Key (优先于环境变量)                      | -                            |
| `WithRole(role string)`     | 设置角色                                           | `user`                       |
| `WithClientOptions(opts)`   | 设置 GenAI 客户端选项                              | -                            |
| `WithRequestOptions(opts)`  | 设置 GenAI 请求选项                                | -                            |

## Ollama Embedder

本地运行的 Ollama 模型。

**环境变量配置**

* `OLLAMA_HOST`: Ollama 服务地址（如果在代码中未提供 `WithHost`，则自动读取此环境变量，默认为 `http://localhost:11434`）

**代码示例**

```go
import (
    ollamaembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
)

embedder := ollamaembedder.New()
```

**配置选项 (Options)**


| 选项                          | 说明                       | 默认值                      |
| ------------------------------- | ---------------------------- | ----------------------------- |
| `WithModel(model string)`     | 设置模型                   | `llama3.2:latest`           |
| `WithHost(host string)`       | 设置 Host                  | `http://localhost:11434`    |
| `WithDimensions(dim int)`     | 设置维度                   | `1536`                      |
| `WithTruncate(truncate bool)` | 设置是否截断               | -                           |
| `WithUseEmbeddings()`         | 使用`/api/embeddings` 端点 | `false` (使用 `/api/embed`) |
| `WithOptions(opts map)`       | 设置 Ollama 模型参数       | -                           |
| `WithKeepAlive(d Duration)`   | 设置 KeepAlive 时间        | -                           |

## HuggingFace Embedder

HuggingFace Text Embedding Inference (TEI) 服务。

**代码示例**

```go
import (
    huggingfaceembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/huggingface"
)

embedder := huggingfaceembedder.New()
```

**配置选项 (Options)**


| 选项                           | 说明                                | 默认值                  |
| -------------------------------- | ------------------------------------- | ------------------------- |
| `WithBaseURL(url string)`      | 设置 Base URL                       | `http://localhost:8080` |
| `WithDimensions(dim int)`      | 设置维度                            | `1024`                  |
| `WithNormalize(norm bool)`     | 是否归一化                          | -                       |
| `WithPromptName(name string)`  | 设置 Prompt 名称                    | -                       |
| `WithTruncate(trunc bool)`     | 是否截断                            | -                       |
| `WithTruncationDirection(dir)` | 截断方向 (`Left`/`Right`)           | `Right`                 |
| `WithEmbedRoute(route)`        | 设置路由 (`/embed` 或 `/embed_all`) | `/embed`                |
| `WithClient(client)`           | 设置 HTTP Client                    | `http.DefaultClient`    |
