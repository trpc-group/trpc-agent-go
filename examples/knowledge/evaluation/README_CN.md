# RAG 评测：tRPC-Agent-Go vs LangChain vs Agno

本目录包含一个全面的评测框架，使用 [RAGAS](https://docs.ragas.io/) 指标对不同的 RAG（检索增强生成）系统进行对比分析。

## 概述

为了确保公平对比，我们使用**完全相同的配置**对三个 RAG 实现进行了评测：

- **tRPC-Agent-Go**: 我们基于 Go 的 RAG 实现
- **LangChain**: 基于 Python 的参考实现
- **Agno**: 具有内置知识库支持的 Python AI Agent 框架

## 快速开始

### 环境准备

```bash
# 安装 Python 依赖
pip install -r requirements.txt

# 设置环境变量
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"  # 可选
export MODEL_NAME="deepseek-v3.2"        # 可选，用于 RAG 的模型
export EVAL_MODEL_NAME="gemini-2.5-flash" # 可选，用于评测的模型
export EMBEDDING_MODEL="server:274214"  # 可选

# PostgreSQL (PGVector) 配置
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="root"
export PGVECTOR_PASSWORD="123"           # 默认密码
export PGVECTOR_DATABASE="vector"
```

### 运行评测

```bash
# 评测 LangChain
python3 main.py --kb=langchain

# 评测 tRPC-Agent-Go
python3 main.py --kb=trpc-agent-go

# 评测 Agno
python3 main.py --kb=agno

# 查看完整日志（包含答案和上下文）
python3 main.py --kb=trpc-agent-go --max-qa=1 --full-log
```

## 配置对齐

三个系统均使用**相同参数**以确保对比的公正性：

| 参数 | LangChain | tRPC-Agent-Go | Agno |
|-----------|-----------|---------------|------|
| **Temperature** | 0 | 0 | 0 |
| **Chunk Size** | 500 | 500 | 500 |
| **Chunk Overlap** | 50 | 50 | 50 |
| **Embedding Dimensions** | 1024 | 1024 | 1024 |
| **Vector Store** | PGVector | PGVector | PgVector |
| **Agent 类型** | Tool-calling Agent | LLM Agent with Tools | Agno Agent |
| **单次检索数量 (k)** | 4 | 4 | 4 |

## 系统提示词 (System Prompt)

为了确保评测的公平性，我们为所有三个系统配置了**完全相同**的核心提示词。

**LangChain, Agno & tRPC-Agent-Go 使用的提示词：**
```text
You are a helpful assistant that answers questions using a knowledge base search tool.

CRITICAL RULES:
1. Answer ONLY using information retrieved from the search tool.
2. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
3. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.
4. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.
5. Be concise and stick strictly to the facts from the retrieved information.

If the search results don't contain the answer, say "I cannot find this information in the knowledge base" instead of making up an answer.
```

## 数据集

我们使用 [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) 数据集。

**重要过滤说明**：为了确保数据质量和格式统一，我们对原始数据进行了严格过滤，**仅保留 Markdown (`.md`) 文件**用于文档检索和 QA 评测对。

- **Documents**: `m-ric/huggingface_doc` - 仅限 .md 文档
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - 仅限来源为 .md 文件的问答对

## 评测指标说明

### 回答质量 (Answer Quality)

| 指标 | 含义 | 越高说明 |
|------|------|---------|
| **Faithfulness (忠实度)** | 回答是否**仅基于检索到的上下文**，无幻觉 | 答案更可信，没有编造内容 |
| **Answer Relevancy (相关性)** | 回答与问题的**相关程度** | 答案更切题、更完整 |
| **Answer Correctness (正确性)** | 回答与标准答案的**语义一致性** | 答案越接近正确答案 |
| **Answer Similarity (相似度)** | 回答与标准答案的**语义相似程度** | 答案文本表达越相似 |

### 上下文质量 (Context Quality)

| 指标 | 含义 | 越高说明 |
|------|------|---------|
| **Context Precision (精确率)** | 检索到的文档中**相关内容的密集程度** | 检索更精准，噪音更少 |
| **Context Recall (召回率)** | 检索出的内容是否**包含了得出答案所需的全部信息** | 检索更全面，没有遗漏关键信息 |
| **Context Entity Recall (实体召回)** | 检索到的内容对标准答案中**关键实体的覆盖程度** | 关键信息检索更完整 |

### 指标的简单理解

- **Faithfulness**: "你说的都是根据检索到的内容吗？"（检查有没有瞎编）
- **Answer Relevancy**: "你回答的是我问的问题吗？"（检查是否答非所问）
- **Answer Correctness**: "你答对了吗？"（和标准答案对比）
- **Answer Similarity**: "你的答案和正确答案像不像？"（语义相似度）
- **Context Precision**: "检索到的内容有用吗？"（检查检索质量）
- **Context Recall**: "检索到的内容够不够？"（检查是否漏掉关键信息）
- **Context Entity Recall**: "关键信息都检索到了吗？"（检查关键实体覆盖）

## 评测结果

### 全量数据评测 (54 个问答对)

**测试环境参数：**
- **数据集**: 全量 HuggingFace Markdown 文档集 (54 QA)
- **Embedding 模型**: `server:274214` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Gemini 2.5 Flash`

#### 回答质量指标 (Answer Quality)

| 指标 | LangChain | tRPC-Agent-Go | Agno | 胜者 |
|--------|-----------|---------------|------|------|
| **Faithfulness (忠实度)** | 0.8978 | 0.8639 | 0.8491 | ✅ LangChain |
| **Answer Relevancy (相关性)** | 0.8921 | 0.9034 | 0.9625 | ✅ Agno |
| **Answer Correctness (正确性)** | 0.6193 | 0.6167 | 0.6120 | ✅ LangChain |
| **Answer Similarity (相似度)** | 0.6535 | 0.6468 | 0.6312 | ✅ LangChain |

#### 上下文质量指标 (Context Quality)

| 指标 | LangChain | tRPC-Agent-Go | Agno | 胜者 |
|--------|-----------|---------------|------|------|
| **Context Precision (精确率)** | 0.6267 | 0.6983 | 0.6860 | ✅ tRPC-Agent-Go |
| **Context Recall (召回率)** | 0.8889 | 0.9259 | 0.9630 | ✅ Agno |
| **Context Entity Recall (实体召回)** | 0.4466 | 0.4846 | 0.4654 | ✅ tRPC-Agent-Go |

#### 执行效率 (耗时)

| 指标 | LangChain | tRPC-Agent-Go | Agno |
|--------|-----------|---------------|------|
| **问答总耗时** | 753.79s | 795.35s | 1556.45s |
| **单题平均耗时** | 13.96s | 14.73s | 28.82s |

### 核心结论

1.  **tRPC-Agent-Go 表现亮眼**：在 **Context Precision (0.6983)** 和 **Context Entity Recall (0.4846)** 上位居第一。这表明我们的 Go 实现检索到的文档更加精准，且对关键信息的覆盖面更广。
2.  **响应速度极快**：tRPC-Agent-Go 的单题响应耗时（14.7s）与 LangChain 几乎持平，但远快于 Agno（28.8s，快了近 2 倍）。
3.  **Agno 检索召回最高**：Agno 在相关性和召回率上表现最好，但代价是极高的执行延迟。
4.  **LangChain 回答质量稳定**：在忠实度和相似度上表现更佳。
