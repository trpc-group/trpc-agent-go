# RAG 评测：tRPC-Agent-Go vs LangChain vs LangChain-Chain vs Agno vs CrewAI vs AutoGen

本目录包含一个全面的评测框架，使用 [RAGAS](https://docs.ragas.io/) 指标对不同的 RAG（检索增强生成）系统进行对比分析。

## 概述

为了确保公平对比，我们使用**完全相同的配置**对六个 RAG 实现进行了评测：

- **tRPC-Agent-Go**: 我们基于 Go 的 RAG 实现
- **LangChain**: 基于 Python 的 Agent 参考实现
- **LangChain-Chain**: 基于 LangChain LCEL 的确定性 Chain 流程（retrieve → prompt → LLM，无 Agent 循环）
- **Agno**: 具有内置知识库支持的 Python AI Agent 框架
- **CrewAI**: 基于 Python 的多智能体框架，使用 ChromaDB 向量存储
- **AutoGen**: 微软开发的基于 Python 的多智能体框架

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
# 评测 LangChain
python3 main.py --kb=langchain

# 评测 LangChain-Chain（确定性 Chain 流程）
python3 main.py --kb=langchain_chain

# 评测 tRPC-Agent-Go
python3 main.py --kb=trpc-agent-go

# 评测 Agno
python3 main.py --kb=agno

# 评测 AutoGen
python3 main.py --kb=autogen
```

### 运行纵向评测（Vertical Evaluation）

纵向评测针对 tRPC-Agent-Go 进行专项消融实验（混合搜索权重梯度、RRF 模式），会自动编译并管理每组实验配置对应的 Go 服务。

```bash
# 混合搜索权重消融（11 组权重配比，从纯文本到纯向量）
python3 -m vertical_eval.main --suite hybrid_weight

# RRF 融合实验
python3 -m vertical_eval.main --suite hybrid_rrf

# 运行全部实验套件
python3 -m vertical_eval.main --suite all

# 跳过文档加载（如果 PGVector 中已加载文档）
python3 -m vertical_eval.main --suite hybrid_weight --skip-load

# 仅运行指定实验
python3 -m vertical_eval.main --suite hybrid_weight --experiments hybrid_v80_t20 hybrid_v90_t10

# 指定 PGVector 表名
python3 -m vertical_eval.main --suite hybrid_rrf --skip-load --pg-table veval_hw_rrf
```

结果保存在 `vertical_eval/results/<suite>_<timestamp>/` 目录下，包含每组实验的 JSON 文件和汇总 Markdown 报告。

## 配置对齐

六个系统均使用**相同参数**以确保对比的公正性：


| 参数                     | LangChain               | LangChain-Chain            | tRPC-Agent-Go              | Agno                    | CrewAI                  | AutoGen                 |
| -------------------------- | ------------------------- | ---------------------------- | ---------------------------- | ------------------------- | ------------------------- | ------------------------- |
| **Temperature**          | 0                       | 0                          | 0                          | 0                       | 0                       | 0                       |
| **Chunk Size**           | 500                     | 500                        | 500                        | 500                     | 500                     | 500                     |
| **Chunk Overlap**        | 50                      | 50                         | 50                         | 50                      | 50                      | 50                      |
| **Embedding Dimensions** | 1024                    | 1024                       | 1024                       | 1024                    | 1024                    | 1024                    |
| **Vector Store**         | PGVector                | PGVector                   | PGVector                   | PgVector                | ChromaDB                | PGVector                |
| **检索模式**             | Vector                  | Vector                     | Vector (已关闭默认 Hybrid) | Vector                  | Vector                  | Vector                  |
| **Knowledge Base 构建**  | 框架原生方式            | 框架原生方式               | 框架原生方式               | 框架原生方式            | 框架原生方式            | 框架原生方式            |
| **Agent 类型**           | Agent + KB (ReAct 关闭) | Chain (无 Agent 循环)      | Agent + KB (ReAct 关闭)    | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) | Agent + KB (ReAct 关闭) |
| **单次检索数量 (k)**     | 4                       | 4                          | 4                          | 4                       | 4                       | 4                       |

> 📝 **tRPC-Agent-Go 说明**：
>
> - **检索模式**：tRPC-Agent-Go 默认使用 Hybrid Search（混合检索：向量相似度 + 全文检索），但为了保证与其他框架的公平对比，评测中**关闭了混合检索**，统一使用纯 Vector Search（向量相似度检索）。

> 📝 **LangChain-Chain 说明**：
>
> - **流程模式**：LangChain-Chain 使用 LCEL（LangChain Expression Language）构建确定性 Chain 流程（retrieve → format → prompt → LLM → parse），不使用 Agent 循环和工具调用。每个问题触发恰好一次检索，LLM 接收到完全相同的 Prompt 模板，流程完全确定、可复现。

> 📝 **CrewAI 说明**：
>
> - **Vector Store**：由于 CrewAI 目前不支持 PGVector 构建知识库，这里使用 ChromaDB 作为向量存储。
> - **Bug 修复**：CrewAI (v1.9.0) 存在一个 Bug，当 LLM（如 DeepSeek-V3.2）同时返回 `content` 和 `tool_calls` 时，框架会优先返回 `content` 而忽略 `tool_calls`，导致 Agent 无法正常调用工具。我们通过 Monkey Patch 修复了 `LLM._handle_non_streaming_response` 方法，使其优先处理 `tool_calls`，确保评测的公平性。详见 `knowledge_system/crewai/knowledge_base.py`。

## 系统提示词 (System Prompt)

为了确保评测的公平性，我们为所有六个系统配置了**完全相同**的核心提示词。

**LangChain, LangChain-Chain, Agno, tRPC-Agent-Go, CrewAI & AutoGen 使用的提示词：**

```text
You are a helpful assistant that answers questions using a knowledge base search tool.

CRITICAL RULES(IMPORTANT !!!):
1. You MUST call the search tool AT LEAST ONCE before answering. NEVER answer without searching first.
2. Answer ONLY using information retrieved from the search tool.
3. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
4. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.
5. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.
6. Be concise and stick strictly to the facts from the retrieved information.
7. Give only the direct answer.
```

## 数据集

### HuggingFace Documentation

我们使用 [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) 数据集。

**重要过滤说明**：为了确保数据质量和格式统一，我们对原始数据进行了严格过滤，**仅保留 Markdown (`.md`) 文件**用于文档检索和 QA 评测对。

- **Documents**: `m-ric/huggingface_doc` - 仅限 .md 文档
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - 仅限来源为 .md 文件的问答对

### RGB（检索增强生成基准测试）

我们还使用了 [RGB Benchmark](https://github.com/chen700564/RGB)（[论文](https://arxiv.org/abs/2309.01431)）作为 QA 数据源。RGB 原始设计提供了带有预定义正例（相关）和负例（无关）段落的查询，用于评测 4 种 RAG 能力：噪声鲁棒性、拒答能力、信息整合和反事实鲁棒性。

英文部分包含 3 个子集，具有不同的 QA 特征：


| 子集        | QA 数量 | 原始测试重点 | 说明                                                                                                       |
| ------------- | --------- | -------------- | ------------------------------------------------------------------------------------------------------------ |
| **en**      | 300     | 噪声鲁棒性   | 标准事实性查询，答案来源明确。每个查询有明确的正例段落和独立的负例（无关）段落。                           |
| **en_int**  | 100     | 信息整合能力 | 需要从**多个**正例段落中综合信息才能回答的查询，答案分散在多个文档中。                                     |
| **en_fact** | 100     | 反事实鲁棒性 | 同时包含正确的`positive` 段落和**篡改了关键事实**的 `positive_wrong` 段落（如将"Facebook"替换为"Apple"）。 |

> **重要说明：我们对 RGB 的使用方式与原始论文不同。** RGB 原始评测方式是将预选的 positive + negative 段落直接拼接后作为上下文喂给 LLM。而在我们的评测中，仅将 **positive 段落**作为文档灌入各框架的知识库，由框架自行完成检索 + 生成的端到端流程。这意味着：
>
> - **en**：负例（噪声）段落**未被灌入**知识库，因此"噪声鲁棒性"并未被直接测试，实际作为**标准事实性 QA** 基准。
> - **en_fact**：`positive_wrong`（反事实）段落**未被灌入**，因此"反事实鲁棒性"并未被直接测试，实际作为另一组**事实性 QA** 基准，但问题特征与 en 子集不同。
> - **en_int**：信息整合的挑战**被保留**，因为答案确实需要从多个检索到的文档中综合得出。

### MultiHop-RAG（多跳检索增强生成基准）

我们还使用了 [MultiHop-RAG](https://github.com/yixuantt/MultiHop-RAG)（[论文](https://arxiv.org/abs/2401.15391)）基准数据集，由 Yixuan Tang 和 Yi Yang 于 2024 年提出。MultiHop-RAG 专为评测 RAG 系统在**多跳查询**上的表现而设计——这类问题需要跨 **2-4 个文档** 进行推理才能得出正确答案，是检验 RAG 系统复杂推理能力的重要基准。

**数据集结构：**

数据集由两部分组成，均从 GitHub 仓库的 `dataset/` 目录自动下载：

1. **`corpus.json` — 新闻文章语料库（609 篇）**

   - 来源涵盖 48 家新闻媒体（The New York Times、BBC News、TechCrunch、The Verge、Financial Times 等），涉及科技、体育、商业、娱乐等领域
   - 每篇文章包含 `title`（标题）、`body`（正文）、`source`（来源媒体）、`published_at`（发布时间）、`category`（类别）等字段
   - 我们将每篇文章导出为独立的 `.txt` 文件，灌入各框架的知识库
2. **`MultiHopRAG.json` — 多跳查询集（2556 条）**

   - 每条 QA 包含 `query`（查询）、`answer`（标准答案）、`question_type`（问题类型）和 `evidence_list`（证据列表）
   - `evidence_list` 中每项包含 `fact`（关键事实段落）及其来源文章的元数据（title、source、url 等），代表回答该查询所需的金标准证据

**问题类型及筛选：**

原始数据集包含 4 种问题类型，其中 `null_query`（301 条）为无法从语料中回答的问题，我们将其排除。对剩余 3 种类型各取前 150 条：


| 问题类型             | 选取 / 总数 | 说明                                                                 |
| ---------------------- | ------------- | ---------------------------------------------------------------------- |
| **comparison_query** | 150 / 856   | 需要跨多个文档进行信息比较（如"A 和 B 哪个更早发布？"）              |
| **inference_query**  | 150 / 816   | 需要从分散在多个文档中的事实进行推理（如"与某事件相关的人是谁？"）   |
| **temporal_query**   | 150 / 583   | 需要跨多个文档进行时序推理（如"事件 X 发生在事件 Y 之前还是之后？"） |

**评测共使用 450 个 QA 对**（3 类 × 150 条），gold evidence 来自 `evidence_list` 中的 `fact` 字段。

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
| **Context Entity Recall (实体召回)** | 检索到的内容对标准答案中**关键实体的覆盖程度**   | 关键信息检索更完整         |

### 指标的简单理解

- **Faithfulness**: "你说的都是根据检索到的内容吗？"（检查有没有瞎编）
- **Answer Relevancy**: "你回答的是我问的问题吗？"（检查是否答非所问）
- **Answer Correctness**: "你答对了吗？"（和标准答案对比）
- **Answer Similarity**: "你的答案和正确答案像不像？"（语义相似度）
- **Context Precision**: "检索到的内容有用吗？"（检查检索质量）
- **Context Recall**: "检索到的内容够不够？"（检查是否漏掉关键信息）
- **Context Entity Recall**: "关键信息都检索到了吗？"（检查关键实体覆盖）

## 评测结果

### 1. HuggingFace 数据集 (54 个问答对)

**测试环境参数：**

- **数据集**: 全量 HuggingFace Markdown 文档集 (54 QA)
- **Embedding 模型**: `BGE-M3` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Qwen3.5-397B-A17B`

#### 回答质量指标 (Answer Quality)


| 指标                            | LangChain | LangChain-Chain | tRPC-Agent-Go  | Agno       | CrewAI | AutoGen | 胜者             |
| --------------------------------- | ----------- | ----------------- | ---------------- | ------------ | -------- | --------- | ------------------ |
| **Faithfulness (忠实度)**       | 0.9722    | 0.9167          | **0.9815**     | 0.9660     | 0.9753 | 0.8688  | ✅ tRPC-Agent-Go |
| **Answer Relevancy (相关性)**   | 0.8914    | 0.6573          | 0.8799         | **0.8917** | 0.7820 | 0.8304  | ✅ Agno          |
| **Answer Correctness (正确性)** | 0.6984    | 0.7801          | **0.8104**     | 0.7741     | 0.7575 | 0.6707  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.6758    | **0.8373**      | 0.7240         | 0.6989     | 0.7025 | 0.6653  | ✅ LangChain-Chain |

#### 上下文质量指标 (Context Quality)


| 指标                                 | LangChain  | LangChain-Chain | tRPC-Agent-Go  | Agno   | CrewAI     | AutoGen | 胜者                      |
| -------------------------------------- | ------------ | ----------------- | ---------------- | -------- | ------------ | --------- | --------------------------- |
| **Context Precision (精确率)**       | 0.6051     | **0.7716**      | 0.7098         | 0.6712 | 0.6391     | 0.5445  | ✅ LangChain-Chain        |
| **Context Recall (召回率)**          | 0.8704     | 0.8704          | **0.9444**     | 0.9259 | **0.9444** | 0.8889  | ✅ tRPC-Agent-Go / CrewAI |
| **Context Entity Recall (实体召回)** | 0.4898     | **0.5093**      | 0.4867         | 0.4707 | 0.4599     | 0.3833  | ✅ LangChain-Chain        |

#### 核心结论

1. **tRPC-Agent-Go 全面领先**：**Faithfulness (0.9815)**、**Answer Correctness (0.8104)**、**Answer Similarity (0.7240)** 和 **Context Precision (0.7098)** 均排名前列，**Context Recall (0.9444)** 与 CrewAI 并列第一。综合表现最强。
2. **LangChain-Chain 相似度与上下文质量突出**：拿下 3 项第一——**Answer Similarity (0.8373)**、**Context Precision (0.7716)** 和 **Context Entity Recall (0.5093)**。其确定性 Chain 流程（无 Agent 循环）在上下文检索精度上表现最优。
3. **Agno 相关性最优**：**Answer Relevancy (0.8917)** 排名第一。
4. **LangChain 实体召回领先**：**Context Entity Recall (0.4898)** 在非 Chain 框架中排名第一。
5. **AutoGen 各项偏低**：在本数据集上 AutoGen 表现不及其他框架，可能与其对小规模知识库的检索策略有关。

---

### 2. RGB 数据集

**测试环境参数：**

- **数据集**: [RGB Benchmark](https://github.com/chen700564/RGB) (英文子集)
- **Embedding 模型**: `BGE-M3` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Qwen3.5-397B-A17B`

#### 2.1 RGB-en：标准事实性 QA (300 个问答对)

标准事实性查询，答案来源明确。（RGB 原始测试重点为噪声鲁棒性，但在我们的端到端评测中，负例段落未被灌入知识库。）

**回答质量：**


| 指标                            | LangChain | tRPC-Agent-Go | Agno       | CrewAI     | AutoGen | 胜者             |
| --------------------------------- | ----------- | --------------- | ------------ | ------------ | --------- | ------------------ |
| **Faithfulness (忠实度)**       | 0.9735    | 0.9754        | 0.9780     | **0.9888** | 0.7664  | ✅ CrewAI        |
| **Answer Relevancy (相关性)**   | 0.9352    | 0.9430        | **0.9465** | 0.9096     | 0.8544  | ✅ Agno          |
| **Answer Correctness (正确性)** | 0.7834    | **0.8278**    | 0.8236     | 0.7593     | 0.6683  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5291    | 0.5449        | **0.5472** | 0.5353     | 0.4923  | ✅ Agno          |

**上下文质量：**


| 指标                                 | LangChain | tRPC-Agent-Go | Agno   | CrewAI | AutoGen | 胜者             |
| -------------------------------------- | ----------- | --------------- | -------- | -------- | --------- | ------------------ |
| **Context Precision (精确率)**       | 0.8686    | **0.8911**    | 0.8790 | 0.8678 | 0.8876  | ✅ tRPC-Agent-Go |
| **Context Recall (召回率)**          | 0.9933    | **0.9967**    | 0.9933 | 0.9900 | 0.9933  | ✅ tRPC-Agent-Go |
| **Context Entity Recall (实体召回)** | 0.6350    | **0.6533**    | 0.6350 | 0.6250 | 0.6278  | ✅ tRPC-Agent-Go |

#### 2.2 RGB-en_int：多文档信息整合 (100 个问答对)

测试模型检索并综合分散在多个文档中的信息的能力。这是我们端到端评测中最能保留 RGB 原始挑战的子集。

**回答质量：**


| 指标                            | LangChain | tRPC-Agent-Go | Agno   | CrewAI | AutoGen    | 胜者             |
| --------------------------------- | ----------- | --------------- | -------- | -------- | ------------ | ------------------ |
| **Faithfulness (忠实度)**       | 0.9690    | **0.9718**    | 0.8499 | 0.9694 | 0.9130     | ✅ tRPC-Agent-Go |
| **Answer Relevancy (相关性)**   | 0.9033    | 0.9170        | 0.9015 | 0.9212 | **0.9327** | ✅ AutoGen       |
| **Answer Correctness (正确性)** | 0.7113    | **0.7664**    | 0.6889 | 0.6827 | 0.7330     | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5363    | **0.5638**    | 0.5373 | 0.5419 | 0.5414     | ✅ tRPC-Agent-Go |

**上下文质量：**


| 指标                                 | LangChain | tRPC-Agent-Go | Agno       | CrewAI | AutoGen    | 胜者       |
| -------------------------------------- | ----------- | --------------- | ------------ | -------- | ------------ | ------------ |
| **Context Precision (精确率)**       | 0.2822    | 0.2810        | **0.3154** | 0.2774 | 0.2816     | ✅ Agno    |
| **Context Recall (召回率)**          | 0.8950    | 0.8850        | 0.8950     | 0.8850 | **0.9033** | ✅ AutoGen |
| **Context Entity Recall (实体召回)** | 0.6067    | 0.5950        | 0.6200     | 0.6200 | **0.6317** | ✅ AutoGen |

#### 2.3 RGB-en_fact：事实性 QA (100 个问答对)

事实性查询，问题特征与 en 子集不同。（RGB 原始测试重点为反事实鲁棒性，但在我们的端到端评测中，`positive_wrong` 段落未被灌入知识库。）

**回答质量：**


| 指标                            | LangChain  | tRPC-Agent-Go | Agno   | CrewAI | AutoGen | 胜者             |
| --------------------------------- | ------------ | --------------- | -------- | -------- | --------- | ------------------ |
| **Faithfulness (忠实度)**       | **0.9667** | 0.9595        | 0.9275 | 0.9425 | 0.7810  | ✅ LangChain     |
| **Answer Relevancy (相关性)**   | 0.8165     | **0.8941**    | 0.6874 | 0.8362 | 0.7627  | ✅ tRPC-Agent-Go |
| **Answer Correctness (正确性)** | 0.7256     | **0.7780**    | 0.6362 | 0.6910 | 0.6634  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5298     | **0.5434**    | 0.5158 | 0.5357 | 0.5058  | ✅ tRPC-Agent-Go |

**上下文质量：**


| 指标                                 | LangChain  | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen    | 胜者                            |
| -------------------------------------- | ------------ | --------------- | -------- | ------------ | ------------ | --------------------------------- |
| **Context Precision (精确率)**       | 0.6281     | **0.6372**    | 0.6176 | **0.6371** | 0.6311     | ✅ tRPC-Agent-Go / CrewAI       |
| **Context Recall (召回率)**          | **0.9900** | 0.9800        | 0.9500 | 0.9800     | **0.9900** | ✅ LangChain / AutoGen          |
| **Context Entity Recall (实体召回)** | **0.7200** | 0.7100        | 0.6900 | **0.7200** | **0.7200** | ✅ LangChain / CrewAI / AutoGen |

#### RGB 综合分析

**3 个子集平均值对比 (en + en_int + en_fact)：**


| 指标                                 | LangChain | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen    | 胜者             |
| -------------------------------------- | ----------- | --------------- | -------- | ------------ | ------------ | ------------------ |
| **Faithfulness (忠实度)**            | 0.9712    | 0.9715        | 0.9659 | **0.9757** | 0.7986     | ✅ CrewAI        |
| **Answer Relevancy (相关性)**        | 0.9051    | **0.9280**    | 0.8875 | 0.8972     | 0.8517     | ✅ tRPC-Agent-Go |
| **Answer Correctness (正确性)**      | 0.7574    | **0.8056**    | 0.7675 | 0.7303     | 0.6803     | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**       | 0.5307    | **0.5484**    | 0.5399 | 0.5367     | 0.5048     | ✅ tRPC-Agent-Go |
| **Context Precision (精确率)**       | 0.7032    | **0.7183**    | 0.7118 | 0.7036     | 0.7151     | ✅ tRPC-Agent-Go |
| **Context Recall (召回率)**          | 0.9730    | 0.9710        | 0.9650 | 0.9670     | **0.9746** | ✅ AutoGen       |
| **Context Entity Recall (实体召回)** | 0.6463    | **0.6530**    | 0.6343 | 0.6430     | 0.6470     | ✅ tRPC-Agent-Go |

**各子集第一名统计**（5 个框架参与 3 个子集；3 方及以上并列的类别不计入）：


| 框架              | 第一名次数 | 优势领域                                                                                                                                                                                                           |
| ------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **tRPC-Agent-Go** | **11**     | Answer Correctness (en、en_int、en_fact)、Answer Similarity (en_int、en_fact)、Context Precision (en、en_fact)、Context Recall (en)、Context Entity Recall (en)、Faithfulness (en_int)、Answer Relevancy (en_fact) |
| **AutoGen**       | **4**      | Answer Relevancy (en_int)、Context Recall (en_int)、Context Entity Recall (en_int)、Context Recall (en_fact)                                                                                                       |
| **Agno**          | **3**      | Answer Relevancy (en)、Answer Similarity (en)、Context Precision (en_int)                                                                                                                                          |
| **CrewAI**        | **2**      | Faithfulness (en)、Context Precision (en_fact)                                                                                                                                                                     |
| **LangChain**     | **2**      | Faithfulness (en_fact)、Context Recall (en_fact)                                                                                                                                                                   |

**核心发现：**

1. **tRPC-Agent-Go 在回答质量上全面领先**：在所有 3 个子集中均拿下 **Answer Correctness** 第一，加权平均 **Answer Relevancy** (0.9280) 也排名第一。以 11 次第一名——所有框架中最多——展现出最准确、最可靠的回答能力，不受检索场景影响。
2. **Agno 修复后忠实度大幅提升**：修复前 Agno 在 en/en_fact 子集的 Faithfulness 仅 0.61-0.74，修复后提升至 0.93-0.98，与其他主流框架持平。**Answer Similarity (en)** 以 0.5472 排名第一。
3. **AutoGen 在多文档场景的上下文检索上表现突出**：en_int 子集 **Context Recall** (0.9033) 和 **Context Entity Recall** (0.6317) 均为第一，en_fact 子集 **Context Recall** (0.9900) 也并列第一。
4. **大部分框架忠实度表现优异**：LangChain、tRPC-Agent-Go、CrewAI、Agno 均在各子集达到 > 0.92 的忠实度，幻觉问题控制良好。AutoGen (0.77-0.91) 的忠实度相对偏低。
5. **信息整合 (en_int) 是最难的任务**：所有框架的 Context Precision 显著下降（0.27-0.30 vs 其他子集的 0.62-0.89），反映了多文档推理的固有难度。
6. **tRPC-Agent-Go 在 7 项平均指标中占据 5 项第一**：加权平均中，tRPC-Agent-Go 在 Answer Relevancy、Answer Correctness、Answer Similarity、Context Precision 和 Context Entity Recall 均排名第一。

---

### 3. MultiHop-RAG 数据集 (450 个问答对)

**测试环境参数：**

- **数据集**: [MultiHop-RAG](https://github.com/yixuantt/MultiHop-RAG)（[论文](https://arxiv.org/abs/2401.15391)）— 609 篇新闻文章语料，450 个多跳 QA 对（每类问题 150 个）
- **Embedding 模型**: `BGE-M3` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Qwen3.5-397B-A17B`

**回答质量：**


| 指标                            | LangChain | tRPC-Agent-Go | Agno       | CrewAI | AutoGen    | 胜者             |
| --------------------------------- | ----------- | --------------- | ------------ | -------- | ------------ | ------------------ |
| **Faithfulness (忠实度)**       | 0.7639    | 0.7060        | **0.7887** | 0.7460 | 0.7468     | ✅ Agno          |
| **Answer Relevancy (相关性)**   | 0.5955    | **0.6424**    | 0.5638     | 0.5639 | 0.5342     | ✅ tRPC-Agent-Go |
| **Answer Correctness (正确性)** | 0.4243    | **0.4984**    | 0.4524     | 0.4371 | 0.4495     | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.4376    | 0.4699        | 0.4715     | 0.4615 | **0.4904** | ✅ AutoGen       |

**上下文质量：**


| 指标                                 | LangChain  | tRPC-Agent-Go | Agno   | CrewAI | AutoGen    | 胜者             |
| -------------------------------------- | ------------ | --------------- | -------- | -------- | ------------ | ------------------ |
| **Context Precision (精确率)**       | 0.3209     | **0.3574**    | 0.3526 | 0.3409 | 0.3520     | ✅ tRPC-Agent-Go |
| **Context Recall (召回率)**          | 0.7416     | 0.7733        | 0.7756 | 0.7523 | **0.8111** | ✅ AutoGen       |
| **Context Entity Recall (实体召回)** | **0.2711** | 0.2667        | 0.2622 | 0.2599 | 0.2556     | ✅ LangChain     |

**观察：**

1. **多跳查询难度显著高于单跳**：所有指标相比 RGB 和 HuggingFace 数据集均大幅下降，反映了跨文档推理的固有难度。
2. **tRPC-Agent-Go 在回答质量上领先**：**Answer Relevancy** (0.6424) 和 **Answer Correctness** (0.4984) 均排名第一，继续保持生成准确答案的优势。
3. **AutoGen 上下文召回最强**：**Context Recall** (0.8111) 显著领先其他框架，**Answer Similarity** (0.4904) 也排名第一，表明检索到了更全面的证据。
4. **Agno 忠实度最高**：**Faithfulness** (0.7887) 排名第一，表明在多跳推理中更好地遵循了检索内容。
5. **Context Precision 普遍偏低（~0.32-0.36）**：与 RGB-en_int 子集类似，多跳查询使所有框架的检索精度下降，因为相关证据分散在多个文档中。

---

### 4. 垂直评测：tRPC-Agent-Go 混合检索权重消融实验

为了探究 tRPC-Agent-Go 中 PGVector 混合检索（Hybrid Search：向量相似度 + 文本稀疏检索）的最佳权重配比，我们设计了从纯文本（`v0_t100`）到纯向量（`v100_t0`）的 11 个步长的梯度消融实验。

**测试环境参数：**
- **数据集**: 全量 HuggingFace Markdown 文档集 (54 QA)
- **检索配置**: Top K = 4
- **Embedding / Agent / Eval 模型**: 统一保持与主评测一致

**评测结果（按向量权重从低到高排列）：**

| 实验配置 (向量权重_文本权重) | Faithfulness | Answer Relevancy | Answer Correctness | Answer Similarity | Context Precision | Context Recall | Context Entity Recall |
| ---------------------------- | ------------ | ---------------- | ------------------ | ----------------- | ----------------- | -------------- | --------------------- |
| **hybrid_v0_t100** (纯文本)  | 0.8920 | 0.7586 | 0.6588 | 0.6741 | 0.4389 | 0.7925 | 0.3302 |
| **hybrid_v10_t90**           | 0.9064 | 0.7677 | 0.6875 | 0.6741 | 0.5243 | 0.8113 | 0.3519 |
| **hybrid_v20_t80**           | 0.9143 | 0.8164 | 0.6861 | 0.6827 | 0.5592 | 0.8519 | 0.3951 |
| **hybrid_v30_t70**           | 0.9226 | 0.7842 | 0.7188 | 0.6883 | 0.5980 | 0.8704 | 0.3962 |
| **hybrid_v40_t60**           | 0.9681 | 0.7919 | 0.7333 | 0.6939 | 0.6077 | 0.8679 | 0.4031 |
| **hybrid_v50_t50**           | 0.9346 | 0.7948 | 0.7365 | 0.7064 | 0.6441 | 0.8889 | 0.4414 |
| **hybrid_v60_t40**           | 0.9685 | 0.8162 | 0.7503 | 0.7027 | 0.6772 | 0.8889 | 0.4759 |
| **hybrid_v70_t30**           | 0.9593 | 0.8495 | 0.7706 | 0.7107 | 0.7095 | 0.9259 | 0.4883 |
| **hybrid_v80_t20**           | **0.9753** | 0.8830 | 0.7848 | 0.7094 | 0.7205 | 0.9259 | 0.4815 |
| **hybrid_v90_t10**           | 0.9506 | 0.8616 | 0.7953 | 0.7206 | **0.7320** | 0.9259 | 0.4552 |
| **hybrid_v100_t0** (纯向量)  | 0.9748 | **0.8635** | **0.8072** | **0.7229** | 0.6991 | **0.9630** | **0.5219** |

**核心发现与分析：**

1. **纯向量检索（v100_t0）综合表现最优**：
   在全量 54 QA 数据集下，纯向量检索在 **Answer Relevancy (0.8635)**、**Answer Correctness (0.8072)**、**Answer Similarity (0.7229)**、**Context Recall (0.9630)** 和 **Context Entity Recall (0.5219)** 共 5 项指标上取得第一，综合回答质量领先。
2. **高向量权重区间（v80-v100）形成性能高原**：
   v80_t20 到 v100_t0 区间的各项指标差异较小（如 Answer Correctness 在 0.78-0.81 之间），说明当向量权重 ≥ 0.8 时，系统性能趋于稳定。其中 v80_t20 的 Faithfulness (0.9753) 为全场最高，v90_t10 的 Context Precision (0.7320) 最高。
3. **纯文本检索（v0_t100）依然表现最差**：
   纯文本稀疏检索的 Context Precision 仅 0.4389，Context Recall 跌至 0.7925，Answer Correctness 也仅有 0.6588，全面垫底。
4. **混合检索中的"文本惩罚"现象依然显著**：
   从 v100 到 v0 的梯度中可以清晰看到：**随着文本权重增加，整体指标呈现单调下降趋势**。例如 Answer Correctness 从 0.8072 (v100) 逐步下降到 0.6588 (v0)，Context Precision 从 0.6991 降至 0.4389。这一趋势在全量数据集上比采样数据集更加平滑和一致。

**实践建议**：
在标准的 RAG 场景（特别是具备高质量大模型和 Embedding 的系统）中，**建议将向量检索（Vector）的权重设为绝对主导（≥0.8）**。v80_t20 到 v100_t0 区间均表现优异，可根据具体场景微调。仅在存在大量极其生僻的专有名词或无语义货号的场景，才需要考虑适度放宽稀疏文本检索的权重。

### 5. 垂直评测：Reciprocal Rank Fusion (RRF) 融合模式

除了加权分数融合（Weighted Score Fusion）之外，PGVector 还支持 **Reciprocal Rank Fusion (RRF)** 作为混合检索的融合策略。RRF 不依赖原始分数的绝对值，而是基于各检索通道返回结果的**排名**进行融合，公式为：

```
score(d) = sum(1 / (k + rank_i))
```

其中 `k` 为常数（默认 60），`rank_i` 为文档 `d` 在第 `i` 个检索通道中的排名。这种方式天然避免了向量分数和文本分数量纲不一致的问题。

**测试环境参数：**
- **数据集**: 全量 HuggingFace Markdown 文档集 (54 QA)
- **检索配置**: Top K = 4, RRF k=60, CandidateRatio=3
- **Embedding / Agent / Eval 模型**: 统一保持与主评测一致

**评测结果：**

| 融合策略 | Faithfulness | Answer Relevancy | Answer Correctness | Answer Similarity | Context Precision | Context Recall | Context Entity Recall |
| -------- | ------------ | ---------------- | ------------------ | ----------------- | ----------------- | -------------- | --------------------- |
| **RRF** (k=60) | 0.9389 | 0.8164 | 0.7791 | 0.7177 | 0.6460 | 0.9259 | 0.4296 |
| **Weighted** (v100_t0, 纯向量) | 0.9748 | 0.8635 | 0.8072 | 0.7229 | 0.6991 | 0.9630 | 0.5219 |
| **Weighted** (v90_t10) | 0.9506 | 0.8616 | 0.7953 | 0.7206 | 0.7320 | 0.9259 | 0.4552 |
| **Weighted** (v50_t50, 对半) | 0.9346 | 0.7948 | 0.7365 | 0.7064 | 0.6441 | 0.8889 | 0.4414 |

**分析：**

1. **RRF 表现接近 v50_t50 加权融合**：RRF 的 Faithfulness (0.9389)、Answer Relevancy (0.8164)、Answer Correctness (0.7791) 与 v50_t50 加权融合相当或略优，但整体低于高向量权重配置（v90_t10 和 v100_t0）。
2. **纯向量加权融合（v100_t0）综合表现最优**：在 7 项指标中，纯向量在 Faithfulness、Answer Relevancy、Answer Correctness、Answer Similarity、Context Precision、Context Recall 和 Context Entity Recall 上均优于 RRF。
3. **RRF 的 Context 指标略低**：Context Precision (0.6460) 和 Context Entity Recall (0.4296) 低于 v90_t10 和 v100_t0，说明在向量检索质量显著优于文本检索的场景下，RRF 的排名融合策略无法充分发挥向量通道的优势。

**结论**：在本评测场景（高质量 Embedding + 小规模知识库）下，**加权融合优于 RRF**。RRF 更适合**两个检索通道质量相当**的场景（例如向量检索和高质量 BM25 检索并存时）。当向量检索通道明显优于文本检索时，加权融合配合高向量权重（≥0.8）是更好的选择。

---

### 评测观察

在评测过程中，我们通过抓包分析发现，各框架在使用相同 LLM 模型的情况下，**框架发起请求的流程比较相似**——本质上都是 Agent 调用搜索工具、获取上下文、生成回答的标准 RAG 流程。

需要注意的是：

- **数据集规模**：HuggingFace 评测集仅有 1900+ 文档和 54 个 QA 对。RGB 数据集提供了 300 + 100 + 100 = 500 个 QA 对，覆盖不同的检索场景。MultiHop-RAG 数据集新增 609 篇文档和 450 个多跳 QA 对，需要跨文档推理。
- **Prompt 对分数影响**：不可否认，在当前数据集下系统提示词对 Agent 的执行影响比较大，同样也会对最终的分数产生很大的影响，我们保证了统一的系统提示词。
- **切块策略可能有影响**：排除系统提示词的影响后，不同框架的切块实现（chunk size、overlap、边界识别等）可能会对检索和回答质量产生影响，进而影响 Context Precision、Context Recall 等检索指标。
