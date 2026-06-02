# Query Enhancer 查询增强

> **示例代码**: [examples/knowledge/query-enhancer](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/query-enhancer)

Query Enhancer 用于在检索前对用户查询进行改写和优化，提升多轮对话场景下的检索质量。通过 `knowledge.WithQueryEnhancer` 注入。

## 为什么需要 Query Enhancer

在多轮对话中，用户的后续提问经常包含指代词或省略，直接用于向量检索效果很差：

| 轮次 | 用户问题 | 检索器实际看到的 |
|------|---------|-----------------|
| 1 | "什么是大语言模型？" | `什么是大语言模型？` — 正常 |
| 2 | "**它**是如何处理上下文长度的？" | `它是如何处理上下文长度的？` — "它"无法被检索器理解 |
| 3 | "把**上面的**和传统搜索引擎对比一下" | `把上面的和传统搜索引擎对比一下` — "上面的"丢失了语义 |

Query Enhancer 利用 LLM 和会话历史将这些模糊查询改写为独立的、面向检索优化的查询。

## 什么时候需要 Query Enhancer

在默认的 **Agentic RAG** 场景中（Knowledge 作为 Agent 的工具），Agent 的 LLM 本身会根据对话上下文构造 tool call 参数，已经隐式完成了一定程度的查询改写。此时 Query Enhancer **通常不是必需的**。

Query Enhancer 主要适用于以下场景：

| 场景 | 说明 |
|------|------|
| **非 Agentic 的独立检索** | 直接调用 `kb.Search()` 进行检索，没有 Agent LLM 帮忙改写查询 |
| **Embedding 模型对口语化查询不友好** | 检索用的 embedding 模型偏向关键词/短文本，需要将自然语言转为精练查询 |
| **HyDE 等特殊策略** | 需要先让 LLM 生成假设性答案，再用答案的 embedding 做检索 |
| **Agent 构造的 query 质量不够好** | 某些较弱的 LLM 在构造 tool call 参数时可能丢失上下文 |

简而言之：**如果你在 Agentic RAG 场景下检索质量已经满足需求，不需要额外配置 Query Enhancer。**

## 注入 Knowledge

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge"

kb := knowledge.New(
    knowledge.WithQueryEnhancer(enhancer),
    // ... 其他配置（VectorStore、Embedder、Sources 等）
)
```

## 支持的 Enhancer

### LLMEnhancer（LLM 改写）

使用大语言模型根据会话历史改写查询，解决指代消解、去除对话噪声等问题。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/query"
    openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

llm := openaimodel.New("deepseek-v4-flash")
enhancer := query.NewLLMEnhancer(llm)
```

| 配置项 | 说明 | 必填 |
|--------|------|------|
| `model.Model`（构造函数参数） | 用于查询改写的 LLM 实例 | 是 |
| `WithSystemPrompt(string)` | 自定义改写 prompt | 否 |

#### 自定义 System Prompt

默认 prompt 适用于通用场景。对于特定领域，可以覆盖：

```go
enhancer := query.NewLLMEnhancer(llm, query.WithSystemPrompt(`
将用户查询改写为面向代码搜索引擎的检索语句。
聚焦于函数名、类型名和包名。
只输出改写后的查询，不要输出其他内容。
`))
```

### PassthroughEnhancer（透传）

不做任何改写，直接返回原始查询。这是未配置 enhancer 时的默认行为。

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/query"

enhancer := query.NewPassthroughEnhancer()
```

## 工作原理

Query Enhancer 在 RAG 管道中的位置：

```
用户查询 + 会话历史
        │
        ▼
┌──────────────────┐
│  Query Enhancer   │  改写查询（指代消解、去噪、检索优化）
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│    Embedder       │  对改写后的查询生成向量
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│  Vector Store     │  使用改写后的查询检索
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│  Reranker（可选）  │  使用原始查询重排结果
└────────┬─────────┘
         │
         ▼
      检索结果
```

### 会话历史自动提取

当 Knowledge 作为 Agent 工具使用时，框架会自动从 Session 中提取最近的对话历史（最多 10 条），传递给 Query Enhancer。无需用户手动管理历史。

提取时会过滤掉：

- Partial（流式中间态）消息
- Tool Call / Tool Result 消息
- 空白内容消息
- 非 user/assistant 角色的消息

### Filter-only 场景优化

当查询文本为空且搜索模式为纯 filter 时（`SearchModeFilter`），框架自动跳过 query enhancement，避免不必要的 LLM 调用。

## 自定义 Enhancer

实现 `query.Enhancer` 接口即可创建自定义增强器：

```go
type Enhancer interface {
    EnhanceQuery(ctx context.Context, req *Request) (*Enhanced, error)
}
```

例如，创建一个打印调试信息的装饰器：

```go
type debugEnhancer struct {
    inner query.Enhancer
}

func (d *debugEnhancer) EnhanceQuery(ctx context.Context, req *query.Request) (*query.Enhanced, error) {
    result, err := d.inner.EnhanceQuery(ctx, req)
    if err != nil {
        return nil, err
    }
    if result.Enhanced != req.Query {
        fmt.Printf("查询已改写: %q -> %q\n", req.Query, result.Enhanced)
    }
    return result, nil
}

// 使用：
enhancer := &debugEnhancer{inner: query.NewLLMEnhancer(llm)}
```

## 注意事项

- LLMEnhancer 每次查询都会调用一次 LLM，会增加延迟和成本。
- Query Enhancer 是可选的（opt-in），不配置时等同于 Passthrough 行为。
- Reranker 接收的是**原始查询**而非改写后的查询，以保留用户原始意图用于相关性判断。
