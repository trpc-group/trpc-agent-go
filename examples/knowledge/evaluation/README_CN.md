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
export EVAL_MODEL_NAME="gemini-3-flash"   # 可选，用于评测的模型
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
| **检索模式**             | Vector                  | Vector (已关闭默认 Hybrid) | Vector                  | Vector                  |
| **Knowledge Base 构建**  | 框架原生方式            | 框架原生方式            | 框架原生方式            | 框架原生方式            |
| **Agent 类型**           | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) |
| **单次检索数量 (k)**     | 4                       | 4                       | 4                       | 4                       |

> 📝 **tRPC-Agent-Go 说明**：
>
> - **检索模式**：tRPC-Agent-Go 默认使用 Hybrid Search（混合检索：向量相似度 + 全文检索），但为了保证与其他框架的公平对比，评测中**关闭了混合检索**，统一使用纯 Vector Search（向量相似度检索）。

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
- **检索模式**：所有框架统一使用**纯向量检索**（tRPC-Agent-Go 已关闭默认的 Hybrid Search），确保检索条件一致。
- **用途**：确保所有框架在**完全相同的检索结果**上被评测，消除 Agent 多轮工具调用、Query 改写、混合检索等差异。
- **失败样本**：保留并填充占位值，保证各框架的样本集合完全一致。

### `native` 模式（真实表现）

```bash
python3 main.py --kb=langchain --eval-mode=native
```

- **上下文采集**：上下文来自 Agent 在 `answer()` 过程中的实际工具调用响应。
- **检索模式**：各框架使用**原生检索管线**（tRPC-Agent-Go 在此评测中同样关闭了混合检索，使用纯向量检索以保证公平）。
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

CRITICAL RULES(IMPORTANT !!!):
1. You MUST call the search tool AT LEAST ONCE before answering. NEVER answer without searching first.
2. Answer ONLY using information retrieved from the search tool.
3. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
4. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.
5. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.
6. Be concise and stick strictly to the facts from the retrieved information.
7. Give ONLY the direct answer. Don't need external explanation.
8. Do NOT start your answer with "Based on the search results" or any similar prefix. Output the answer directly.
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
- **Embedding 模型**: `BGE-M3` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Gemini 3 Flash`

#### 回答质量指标 (Answer Quality)


| 指标                            | LangChain | tRPC-Agent-Go | Agno   | CrewAI | 胜者              |
| --------------------------------- | ----------- | --------------- | -------- | -------- | ------------------- |
| **Faithfulness (忠实度)**       | 0.9340    | **1.0000**    | 0.9815 | 0.9907 | ✅ tRPC-Agent-Go |
| **Answer Relevancy (相关性)**   | 0.7430    | 0.7909        | 0.7814 | **0.8073** | ✅ CrewAI        |
| **Answer Correctness (正确性)** | 0.7417    | **0.8392**    | 0.8357 | 0.7855 | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.7313    | 0.7663        | **0.7711** | 0.7043 | ✅ Agno          |

#### 上下文质量指标 (Context Quality)


| 指标                                 | LangChain | tRPC-Agent-Go | Agno   | CrewAI | 胜者                    |
| -------------------------------------- | ----------- | --------------- | -------- | -------- | ------------------------- |
| **Context Precision (精确率)**       | 0.6026    | **0.7171**    | 0.6932 | 0.6623 | ✅ tRPC-Agent-Go        |
| **Context Recall (召回率)**          | 0.8704    | **0.9444**    | **0.9444** | **0.9444** | ✅ tRPC-Agent-Go / Agno / CrewAI |
| **Context Entity Recall (实体召回)** | **0.4251** | 0.4179       | 0.4205 | 0.4189 | ✅ LangChain            |

#### 执行效率 (耗时)

> ⚠️ **重要说明**：各框架的评测是在**不同时间段**分别运行的，模型推理速度会受 API 服务器负载和网络状况影响而有较大波动。**时间指标仅供参考，不宜用于严格的性能对比。**


| 指标             | LangChain | tRPC-Agent-Go | Agno     | CrewAI   |
| ------------------ | ----------- | --------------- | ---------- | ---------- |
| **问答总耗时**   | 378.94s   | 583.63s       | 571.68s  | 521.72s  |
| **单题平均耗时** | 7.02s     | 10.81s        | 10.59s   | 9.66s    |

### 核心结论

1. **tRPC-Agent-Go 综合表现相对更优**：在 **Faithfulness (1.0000 满分)**、**Answer Correctness (0.8392)** 和 **Context Precision (0.7171)** 上均位居第一，回答忠实度达到满分，检索精度相对领先。
2. **四个框架各有所长**：CrewAI 在 **Answer Relevancy (0.8073)** 上领先，Agno 在 **Answer Similarity (0.7711)** 上最高，LangChain 在 **Context Entity Recall (0.4251)** 上略优。
3. **Context Recall 三方持平**：tRPC-Agent-Go、Agno、CrewAI 均达到 **0.9444**，表明三者的检索召回能力相当。

### 评测观察

在评测过程中，我们通过抓包分析发现，各框架在使用相同 LLM 模型的情况下，**框架发起请求的流程比较相似**——本质上都是 Agent 调用搜索工具、获取上下文、生成回答的标准 RAG 流程。部分框架（如 CrewAI）会在内部额外注入一些框架级 prompt。

需要注意的是：

- **数据集规模偏小**：当前评测集仅有1900+文档，不算大规模数据
- **Prompt 对分数影响显著**： 不可否认，在当前数据集下系统提示词对Agent的执行影响比较大，同样也会对最终的分数产生很大的影响，我们保证了统一的系统提示词。
- **切块策略是核心差异**：排除系统提示词的影响后，**文档切块（chunking）的质量可能是最终影响检索和回答质量的关键因素**。不同框架的切块实现（chunk size、overlap、边界识别等）会直接影响 Context Precision、Context Recall 等检索指标，进而影响回答的正确性。
