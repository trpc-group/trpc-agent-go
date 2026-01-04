# Embedder

Embedder 负责将文本转换为向量表示，是 Knowledge 系统的核心组件。

## 支持的 Embedding 模型

trpc-agent-go 支持多种 Embedding 模型平台：

| 平台类型 | 包路径 | 说明 |
|---------|--------|------|
| OpenAI | `knowledge/embedder/openai` | OpenAI embedding 模型（text-embedding-3-small 等），支持兼容 OpenAI Embedding API 的所有平台 |
| Gemini | `knowledge/embedder/gemini` | Google Gemini embedding 模型 |
| Ollama | `knowledge/embedder/ollama` | 本地 Ollama embedding 模型，适合离线部署 |
| HuggingFace | `knowledge/embedder/huggingface` | HuggingFace Text Embedding Inference 模型 |

## OpenAI Embedder

OpenAI Embedder 是最常用的选择，支持 OpenAI 官方服务及兼容 OpenAI API 的第三方服务：

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// OpenAI Embedder 配置
embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // embedding 模型，也可通过 OPENAI_EMBEDDING_MODEL 环境变量设置
)

// 传递给 Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
)
```

## Gemini Embedder

```go
import (
    geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
)

embedder, err := geminiembedder.New(context.Background())
if err != nil {
    log.Fatalf("Failed to create gemini embedder: %v", err)
}
```

## Ollama Embedder

```go
import (
    ollamaembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
)

embedder, err := ollamaembedder.New()
if err != nil {
    log.Fatalf("Failed to create ollama embedder: %v", err)
}
```

## HuggingFace Embedder

```go
import (
    huggingfaceembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/huggingface"
)

embedder := huggingfaceembedder.New()
```

## 环境变量配置

```bash
# OpenAI API 配置（当使用 OpenAI embedder 时必选，会被 OpenAI SDK 自动读取）
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"

# Google Gemini API 配置（当使用 Gemini embedder 时）
export GOOGLE_API_KEY="your-google-api-key"
```

