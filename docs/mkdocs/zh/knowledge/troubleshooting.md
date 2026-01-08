# 故障排除

## 常见问题与处理建议

### 1. Create embedding failed/HTTP 4xx/5xx

**可能原因**：
- API Key 无效或缺失
- BaseURL 配置错误
- 网络访问受限
- 文本过长
- 所配置的 BaseURL 不提供 Embeddings 接口或不支持所选的 embedding 模型（例如返回 404 Not Found）

**排查步骤**：
1. 确认 `OPENAI_API_KEY` 已设置且可用
2. 如使用兼容网关，显式设置 `WithBaseURL(os.Getenv("OPENAI_BASE_URL"))`
3. 确认 `WithModel("text-embedding-3-small")` 或你所用服务实际支持的 embedding 模型名称
4. 使用最小化样例调用一次 embedding API 验证连通性
5. 用 curl 验证目标 BaseURL 是否实现 `/v1/embeddings` 且模型存在：
   ```bash
   curl -sS -X POST "$OPENAI_BASE_URL/embeddings" \
     -H "Authorization: Bearer $OPENAI_API_KEY" \
     -H "Content-Type: application/json" \
     -d '{"model":"text-embedding-3-small","input":"ping"}'
   ```
   若返回 404/模型不存在，请更换为支持 Embeddings 的 BaseURL 或切换到该服务提供的有效 embedding 模型名
6. 逐步缩短文本，确认非超长输入导致

**参考代码**：
```go
import (
    "log"
    "os"

    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
    openaiembedder.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
    openaiembedder.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
)
if _, err := embedder.GetEmbedding(ctx, "ping"); err != nil {
    log.Fatalf("embed check failed: %v", err)
}
```


### 2. PDF 文件读取支持

**说明**：由于 PDF reader 依赖第三方库，为避免主模块引入不必要的依赖，PDF reader 采用独立 `go.mod` 管理。

**使用方式**：如需支持 PDF 文件读取，需在代码中手动引入 PDF reader 包进行注册：
```go
import (
    // 引入 PDF reader 以支持 .pdf 文件解析
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)
```


## 运行示例所需的环境变量

> 以下环境变量用于运行 [examples/knowledge](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge) 目录下的示例程序。

```bash
# OpenAI API 配置（当使用 OpenAI embedder 时必选，会被 OpenAI SDK 自动读取）
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
# OpenAI embedding 模型配置（可选，需要在代码中手动读取）
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small"

# Google Gemini API 配置（当使用 Gemini embedder 时）
export GOOGLE_API_KEY="your-google-api-key"

# PostgreSQL + pgvector 配置（当使用 -vectorstore=pgvector 时必填）
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="postgres"
export PGVECTOR_PASSWORD="your-password"
export PGVECTOR_DATABASE="vectordb"

# TcVector 配置（当使用 -vectorstore=tcvector 时必填）
export TCVECTOR_URL="https://your-tcvector-endpoint"
export TCVECTOR_USERNAME="your-username"
export TCVECTOR_PASSWORD="your-password"

# Elasticsearch 配置（当使用 -vectorstore=elasticsearch 时必填）
export ELASTICSEARCH_HOSTS="http://localhost:9200"
export ELASTICSEARCH_USERNAME=""
export ELASTICSEARCH_PASSWORD=""
export ELASTICSEARCH_API_KEY=""
export ELASTICSEARCH_INDEX_NAME="trpc_agent_documents"

# Milvus 配置（当使用 -vectorstore=milvus 时必填）
export MILVUS_ADDRESS="localhost:19530"
export MILVUS_USERNAME=""
export MILVUS_PASSWORD=""
export MILVUS_DB_NAME=""
export MILVUS_COLLECTION="trpc_agent_go"
```
