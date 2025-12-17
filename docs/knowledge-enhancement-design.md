# Knowledge 模块增强设计文档：Reranker 与 QueryEnhancer

## 1. 背景与目标

当前 `knowledge` 模块中的 `Reranker` 和 `QueryEnhancer` 仅提供了基础实现（`TopK` 和 `Passthrough`）。为了提升 RAG（Retrieval-Augmented Generation）系统的检索质量和准确性，需要引入更高级的检索增强和重排序策略。

本设计文档旨在规划一系列新的 `Reranker` 和 `QueryEnhancer` 实现，利用 LLM 和外部模型服务来优化检索流程，同时保持现有接口的兼容性。

## 2. Query Enhancer 扩展设计

`QueryEnhancer` 的目标是在检索前对用户的原始查询进行优化，使其更适合向量检索或关键词检索。

### 2.1 现有接口回顾

```go
type Enhancer interface {
    EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error)
}
```

### 2.2 新增实现方案

#### A. LLM Rewrite Enhancer (基于 LLM 的重写增强器)

利用 LLM 将包含代词、上下文依赖的用户 Query 重写为独立、完整的查询语句（De-contextualization）。

*   **实现结构**: `LLMRewriteEnhancer`
*   **依赖**: `model.Model`
*   **配置**:
    *   `Model`: 用于生成的 LLM 模型实例。
    *   `PromptTemplate`: 用于指导 LLM 重写 Query 的提示词模板。
*   **工作流程**:
    1.  构造 Prompt，包含对话历史和当前 Query。
    2.  调用 LLM 生成重写后的 Query。
    3.  提取关键词（可选，也可由 LLM 输出）。

#### B. HyDE Enhancer (假设性文档嵌入增强器)

HyDE (Hypothetical Document Embeddings) 策略通过让 LLM 生成一个假设性的“理想文档”，然后基于该文档的向量进行检索，从而提高语义匹配度。

*   **实现结构**: `HyDEEnhancer`
*   **依赖**: `model.Model`
*   **配置**:
    *   `Model`: 用于生成的 LLM 模型实例。
    *   `PromptTemplate`: 用于生成假设性文档的提示词。
*   **工作流程**:
    1.  构造 Prompt，要求 LLM 生成一段能回答用户问题的文本。
    2.  将生成的假设性文档作为 `Enhanced.Enhanced` 返回（或者结合原始 Query）。
    3.  检索阶段将使用这段文本生成向量。

#### C. Multi-Query Enhancer (多查询增强器)

将一个复杂的用户 Query 拆解或扩展为多个相关的 Query，并行检索以覆盖不同的语义角度。

*   **实现结构**: `MultiQueryEnhancer`
*   **依赖**: `model.Model`
*   **配置**:
    *   `Model`: LLM 模型实例。
    *   `MaxQueries`: 最大生成的查询数量。
*   **工作流程**:
    1.  让 LLM 生成 N 个相关 Query。
    2.  返回的 `Enhanced` 结构体可能需要扩展以支持多 Query，或者在 `Enhanced` 字段中用特定分隔符连接，由调用方处理（注意：这可能需要微调 `Knowledge.Search` 逻辑以支持多路检索，或者在 Enhancer 内部做一些 trick）。
    *   *注：为了保持接口兼容，目前 `Enhanced` 只有一个 string 字段。建议在 `Enhanced` 结构体中增加 `Variations []string` 字段，或者让 Knowledge 层识别特定格式。更稳健的做法是扩展 `Enhanced` 结构体。*

## 3. Reranker 扩展设计

`Reranker` 的目标是在初步检索（Retrieval）之后，对候选文档列表进行精细化排序。

### 3.1 现有接口回顾

```go
type Reranker interface {
    Rerank(ctx context.Context, query *Query, results []*Result) ([]*Result, error)
}
```

### 3.2 新增实现方案

#### A. LLM Reranker (Listwise/Pairwise)

直接利用 LLM 的理解能力对文档相关性进行打分或排序。

*   **实现结构**: `LLMReranker`
*   **依赖**: `model.Model`
*   **策略**:
    *   **Listwise**: 将 Query 和所有候选文档一次性（或分批）放入 Prompt，让 LLM 输出排序后的 ID 列表。
    *   **Pointwise**: 对每个 [Query, Document] 对让 LLM 打分（0-10分）。
*   **配置**:
    *   `Model`: LLM 模型实例。
    *   `Strategy`: "listwise" | "pointwise"
    *   `TopN`: 最终保留的文档数。

#### B. Cross-Encoder Reranker (外部 API 适配)

对接专业的 Rerank 模型服务（如 Cohere Rerank, BGE Rerank, Jina Rerank）。这些模型通常是 Cross-Encoder 架构，比向量距离更精准。

*   **实现结构**: `CrossEncoderReranker` (或具体命名为 `CohereReranker` 等)
*   **依赖**: HTTP Client / SDK
*   **配置**:
    *   `APIKey`: 服务端 Key。
    *   `Endpoint`: API 地址。
    *   `ModelName`: 使用的模型名称 (e.g., "rerank-english-v3.0")。
*   **工作流程**:
    1.  提取所有候选文档的文本。
    2.  构造 API 请求（Query + Documents）。
    3.  解析 API 返回的分数。
    4.  更新 `Result.Score` 并重新排序。

#### C. Weighted Reranker (加权重排)

如果引入了混合检索（关键词+向量），可以使用此 Reranker 调整不同权重的分数。或者基于文档的元数据（如时间新旧）进行加权。

*   **实现结构**: `WeightedReranker`
*   **配置**:
    *   `VectorWeight`: 向量分数权重。
    *   `KeywordWeight`: 关键词分数权重（如果支持）。
    *   `RecencyWeight`: 时间衰减权重。

#### D. RRF Reranker (Reciprocal Rank Fusion)

主要用于合并多路检索的结果（例如 Multi-Query Enhancer 产生的多路结果，或者 向量+全文 检索的结果）。

*   **实现结构**: `RRFReranker`
*   **原理**: $score = 1 / (k + rank)$
*   **适用场景**: 当输入 `results` 实际上是拼接了来自不同源的未合并结果时，或者在 `Knowledge` 内部实现多路召回后的合并阶段。

## 4. 目录结构建议

建议在 `trpc-agent-go/knowledge` 下组织新的实现文件：

```text
knowledge/
├── query/
│   ├── enhancer.go          (接口定义)
│   ├── passthrough.go       (现有)
│   ├── llm_rewrite.go       (新增: LLM 重写)
│   ├── hyde.go              (新增: HyDE)
│   └── multi_query.go       (新增: 多查询)
├── reranker/
│   ├── reranker.go          (接口定义)
│   ├── topk.go              (现有)
│   ├── llm_reranker.go      (新增: LLM 打分)
│   ├── cohere.go            (新增: Cohere 适配)
│   └── rrf.go               (新增: RRF 算法)
```

## 5. 接口微调建议 (可选)

为了更好地支持 `MultiQueryEnhancer`，建议扩展 `knowledge/query/query.go` 中的 `Enhanced` 结构体：

```go
type Enhanced struct {
    Enhanced string
    Keywords []string
    // 新增字段，支持多路查询变体
    Variations []string 
}
```

并在 `Knowledge.Search` 实现中，如果发现 `Variations` 不为空，可以选择执行多次检索并合并结果。

## 6. 使用示例

### 初始化带有 LLM 增强和 Rerank 的 Knowledge

```go
// 1. 初始化 LLM
llmModel, _ := openai.New(openai.WithToken("..."))

// 2. 创建 Enhancer
enhancer := query.NewLLMRewriteEnhancer(
    query.WithModel(llmModel),
    query.WithPrompt(customPrompt),
)

// 3. 创建 Reranker
reranker := reranker.NewLLMReranker(
    reranker.WithModel(llmModel),
    reranker.WithTopN(5),
)

// 4. 初始化 Knowledge
k := knowledge.New(
    knowledge.WithEnhancer(enhancer),
    knowledge.WithReranker(reranker),
    // ... 其他配置
)
```

## 7. 总结

通过引入上述扩展，`knowledge` 模块将具备构建生产级 RAG 应用的能力，能够处理复杂的 Query 理解和高精度的文档排序需求。
