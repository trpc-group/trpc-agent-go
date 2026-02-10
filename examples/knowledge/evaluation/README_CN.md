# RAG 评测：tRPC-Agent-Go vs LangChain vs Agno vs CrewAI

本目录包含一个全面的评测框架，使用 [RAGAS](https://docs.ragas.io/) 指标对不同的 RAG（检索增强生成）系统进行对比分析。

## 概述

为了确保公平对比，我们使用**完全相同的配置**对四个 RAG 实现进行了评测：

- **tRPC-Agent-Go**: 我们基于 Go 的 RAG 实现
- **LangChain**: 基于 Python 的参考实现
- **Agno**: 具有内置知识库支持的 Python AI Agent 框架
- **CrewAI**: 基于 Python 的多智能体框架，使用 ChromaDB 向量存储

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
# 评测 LangChain（native 模式，默认）
python3 main.py --kb=langchain

# 使用 strict 模式评测（公平基线对比）
python3 main.py --kb=langchain --eval-mode=strict

# 评测 tRPC-Agent-Go
python3 main.py --kb=trpc-agent-go

# 评测 Agno
python3 main.py --kb=agno

# 查看完整日志（包含答案和上下文）
python3 main.py --kb=trpc-agent-go --max-qa=1 --full-log
```

## 配置对齐

四个系统均使用**相同参数**以确保对比的公正性：


| 参数                     | LangChain               | tRPC-Agent-Go           | Agno                    | CrewAI                  |
| -------------------------- | ------------------------- | ------------------------- | ------------------------- | ------------------------- |
| **Temperature**          | 0                       | 0                       | 0                       | 0                       |
| **Chunk Size**           | 500                     | 500                     | 500                     | 500                     |
| **Chunk Overlap**        | 50                      | 50                      | 50                      | 50                      |
| **Embedding Dimensions** | 1024                    | 1024                    | 1024                    | 1024                    |
| **Vector Store**         | PGVector                | PGVector                | PgVector                | ChromaDB                |
| **检索模式 (strict)**    | Vector                  | Vector                  | Vector                  | Vector                  |
| **检索模式 (native)**    | Vector                  | Hybrid (默认)           | Vector                  | Vector                  |
| **Knowledge Base 构建**  | 框架原生方式            | 框架原生方式            | 框架原生方式            | 框架原生方式            |
| **Agent 类型**           | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) |
| **单次检索数量 (k)**     | 4                       | 4                       | 4                       | 4                       |

> 📝 **CrewAI 说明**：
>
> - **Vector Store**：由于 CrewAI 目前不支持 PGVector 构建知识库，这里使用 ChromaDB 作为向量存储。
> - **Bug 修复**：CrewAI (v1.9.0) 存在一个 Bug，当 LLM（如 DeepSeek-V3.2）同时返回 `content` 和 `tool_calls` 时，框架会优先返回 `content` 而忽略 `tool_calls`，导致 Agent 无法正常调用工具。我们通过 Monkey Patch 修复了 `LLM._handle_non_streaming_response` 方法，使其优先处理 `tool_calls`，确保评测的公平性。详见 `knowledge_system/crewai/knowledge_base.py`。

## 评测模式

框架支持两种评测模式，分别对应不同的对比目标：

### `strict` 模式（公平基线）

```bash
python3 main.py --kb=langchain --eval-mode=strict
```

- **上下文采集**：每题固定一次 `search(question, k)` 调用获取上下文，**与 Agent 解耦**。
- **答案生成**：Agent 的 `answer()` 仍然正常调用，但其内部检索到的上下文**不用于评测**。
- **检索模式**：tRPC-Agent-Go 使用**纯向量检索**（非 Hybrid/关键词），与 LangChain、Agno、CrewAI 的纯向量相似度检索一致。
- **用途**：确保所有框架在**完全相同的检索结果**上被评测，消除 Agent 多轮工具调用、Query 改写、混合检索等差异。
- **失败样本**：保留并填充占位值，保证各框架的样本集合完全一致。

### `native` 模式（真实表现）

```bash
python3 main.py --kb=langchain --eval-mode=native
```

- **上下文采集**：上下文来自 Agent 在 `answer()` 过程中的实际工具调用响应。
- **检索模式**：各框架使用**原生检索管线**（如 tRPC-Agent-Go 可能使用混合检索、Query 增强、重排序等）。
- **用途**：衡量各框架完整 RAG 管线的**端到端真实表现**，包括 Agent 行为、多轮检索、框架特有优化。
- **失败样本**：保留并填充占位值。

### 模式选择指南

| 目标 | 模式 |
|------|------|
| 公平横向对比检索+生成质量 | `strict` |
| 衡量生产环境真实表现 | `native` |
| 调试检索管线差异 | `strict`（直接对比上下文） |
| 评估 Agent 工具调用行为 | `native` |

## 系统提示词 (System Prompt)

为了确保评测的公平性，我们为所有四个系统配置了**完全相同**的核心提示词。

**LangChain, Agno, tRPC-Agent-Go & CrewAI 使用的提示词：**

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


| 指标                            | 含义                                     | 越高说明                 |
| --------------------------------- | ------------------------------------------ | -------------------------- |
| **Faithfulness (忠实度)**       | 回答是否**仅基于检索到的上下文**，无幻觉 | 答案更可信，没有编造内容 |
| **Answer Relevancy (相关性)**   | 回答与问题的**相关程度**                 | 答案更切题、更完整       |
| **Answer Correctness (正确性)** | 回答与标准答案的**语义一致性**           | 答案越接近正确答案       |
| **Answer Similarity (相似度)**  | 回答与标准答案的**语义相似程度**         | 答案文本表达越相似       |

### 上下文质量 (Context Quality)


| 指标                                 | 含义                                             | 越高说明                     |
| -------------------------------------- | -------------------------------------------------- | ------------------------------ |
| **Context Precision (精确率)**       | 检索到的文档中**相关内容的密集程度**             | 检索更精准，噪音更少         |
| **Context Recall (召回率)**          | 检索出的内容是否**包含了得出答案所需的全部信息** | 检索更全面，没有遗漏关键信息 |
| **Context Entity Recall (实体召回)** | 检索到的内容对标准答案中**关键实体的覆盖程度**   | 关键信息检索更完整           |

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


| 指标                            | LangChain | tRPC-Agent-Go | Agno   | CrewAI | 胜者      |
| --------------------------------- | ----------- | --------------- | -------- | -------- | ----------- |
| **Faithfulness (忠实度)**       | 0.8978    | 0.8639        | 0.8491 | 0.9027 | ✅ CrewAI |
| **Answer Relevancy (相关性)**   | 0.8921    | 0.9034        | 0.9625 | 0.7680 | ✅ Agno   |
| **Answer Correctness (正确性)** | 0.6193    | 0.6167        | 0.6120 | 0.6941 | ✅ CrewAI |
| **Answer Similarity (相似度)**  | 0.6535    | 0.6468        | 0.6312 | 0.6714 | ✅ CrewAI |

#### 上下文质量指标 (Context Quality)


| 指标                                 | LangChain | tRPC-Agent-Go | Agno   | CrewAI | 胜者             |
| -------------------------------------- | ----------- | --------------- | -------- | -------- | ------------------ |
| **Context Precision (精确率)**       | 0.6267    | 0.6983        | 0.6860 | 0.6942 | ✅ tRPC-Agent-Go |
| **Context Recall (召回率)**          | 0.8889    | 0.9259        | 0.9630 | 0.9259 | ✅ Agno          |
| **Context Entity Recall (实体召回)** | 0.4466    | 0.4846        | 0.4654 | 0.4883 | ✅ CrewAI        |

#### 执行效率 (耗时)

> ⚠️ **重要说明**：各框架的评测是在**不同时间段**分别运行的，模型推理速度会受 API 服务器负载和网络状况影响而有较大波动。**时间指标仅供参考，不宜用于严格的性能对比。**


| 指标             | LangChain | tRPC-Agent-Go | Agno     | CrewAI  |
| ------------------ | ----------- | --------------- | ---------- | --------- |
| **问答总耗时**   | 753.79s   | 795.35s       | 1556.45s | 385.23s |
| **单题平均耗时** | 13.96s    | 14.73s        | 28.82s   | 7.13s   |

### 核心结论

1. **CrewAI 回答质量最高**：在 **Faithfulness (0.9027)**、**Answer Correctness (0.6941)**、**Answer Similarity (0.6714)** 和 **Context Entity Recall (0.4883)** 上均位居第一，但 **Answer Relevancy (0.7680)** 最低。
2. **tRPC-Agent-Go 检索精度最高**：在 **Context Precision (0.6983)** 上位居第一，表明检索到的文档更加精准，各项指标表现均衡。
3. **Agno 检索召回最高**：在 **Answer Relevancy (0.9625)** 和 **Context Recall (0.9630)** 上表现最好，检索能力最强。
4. **LangChain 表现均衡**：各项指标均处于中上水平，是成熟稳定的参考实现。
