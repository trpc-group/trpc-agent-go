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

> **重要说明（当前实现）**：我们对 RGB 的使用方式与原始论文不同。RGB 原始评测通常是按样本拼接指定外部段落后直接喂给模型；而在本项目中，我们先把文档灌入知识库，再由各框架进行端到端检索 + 生成。并且在灌库阶段，统一加载 `positive`、`negative`、`positive_wrong`（若样本存在）三类段落。

#### RGB 实际加载策略（与代码一致）

- 文档来源：`en / en_int / en_fact` 的原始 JSON 文件。
- 灌库口径：对每条样本同时读取 `positive`、`negative`、`positive_wrong` 三类段落（若字段存在）。
- 去重规则：按段落文本全局去重后写入知识库文档目录，避免重复片段反复入库。
- 评测含义：在同一知识库中同时存在相关信息、噪声信息和反事实信息，更接近真实 RAG 检索环境。

#### RGB 文档 + QA 示例（每个英文子集各 1 条，不截断）

以下示例来自 RGB 原始数据文件首条样本（`en_refine.json`、`en_int.json`、`en_fact.json`），文档文本按原始字段展示，不做截断。

`en`
Q: `When is the premiere of 'Carole King & James Taylor: Just Call Out My Name'?`
A: `January 2 2022`

`positive` 文档：
```text
However, the concert tour took place in honor of the 40th anniversary. The two might have aged since they first performed together but neither Carole King nor James Taylor have lost a beat in all these years!The concert film includes the following songs:(You Make Me Feel Like) A Natural WomanSomething in the Way She MovesSo Far AwayCarolina in My MindCountry RoadSmackwater JackWhere You Lead (lyrics changed up as the city they’re playing in replaces New York)Your Smiling FaceBeautifulShower The PeopleWay Over YonderSweet Baby James (this kicks off the second half of the film)Up on the RoofIt’s Too LateFire and RainI Feel the Earth MoveYou’ve Got a FriendHow Sweet It Is (To Be Loved by You)You Can Close Your EyesMexico (end credits)DIRECTOR: Frank MarshallFEATURING: Carole King, James Taylor, Danny Kortchmar, Peter Asher, Russ Kunkel, Leland SklarADDITIONAL MUSICIANS: Andrea Zonn, Arnold McCuller, Kate Markowitz, Robbie KondorCarole King & James Taylor: Just Call Out My Name premiered January 2, 2022, at 9:00pm ET/PT on CNN. The film will be available on demand via cable/satellite systems, CNNgo platforms, and CNN mobile apps, beginning Monday, January 3, through Sunday, January 16.
```

`negative` 文档：
```text
Feb 10, 2022                                 Runtime:                                   2h 0m                                Carole King                                                                                          Self                                                             James Taylor                                                                                          Self                                                             Danny Kortchmar                                                                                          Self                                                             Russ Kunkel                                                                                          Self                                                             Lee Sklar                                                                                          Self                                                             Frank Marshall                                                                                          Director                                                             Frank Marshall                                                                                          Producer                                                             Aly Parker                                                                                          Producer                                                                              There are no featured reviews for Carole King & James Taylor: Just Call Out My Name because the movie has not released yet ().              Vote Now: The Best Movies & TV Shows Since 1998! Daily Tomato - Can you guess the movie of the day? What to Watch - In theaters & streaming TV and Streaming News Join The Newsletter Get the freshest reviews, news, and more delivered right to your inbox! Follow Us Copyright © Fandango. All rights reserved.
```

`en_int`
Q: `Who is the director of 'Carole King & James Taylor: Just Call Out My Name' and when is its premiere?`
A: `Frank Marshall; January 2 2022`

`positive` 文档：
```text
Details · Release date · January 2, 2022 (United States) · Country of origin. United States · Language. English · Also known as. Just Call Out My Name · Filming ...
```

`en_fact`
Q: `Super Bowl 2021 location`
A: `Tampa, Florida`

`positive` 文档：
```text
The game was played on February 7, 2021, at Raymond James Stadium in Tampa, Florida, the home stadium of the Buccaneers, marking the first time a team played a ...
```

`positive_wrong` 文档：
```text
The game was played on February 7, 2021, at Raymond James Stadium in Glendale, Arizona, the home stadium of the Buccaneers, marking the first time a team played a ...
```

> 说明：`en_fact` 中的 `positive_wrong` 是刻意构造的干扰片段。当前实现会将 `positive`、`negative`、`positive_wrong`（若存在）统一灌入知识库。

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

标准事实性查询，答案来源明确。（RGB 原始测试重点为噪声鲁棒性；在我们的端到端评测中，`negative` 也会与 `positive` 一同灌入知识库，因此该子集仍包含噪声干扰。）

**回答质量：**


| 指标                            | LangChain | tRPC-Agent-Go | Agno       | CrewAI     | AutoGen | 胜者             |
| --------------------------------- | ----------- | --------------- | ------------ | ------------ | --------- | ------------------ |
| **Faithfulness (忠实度)**       | 0.9677    | 0.9861        | 0.9797     | **0.9925** | 0.7664  | ✅ CrewAI        |
| **Answer Relevancy (相关性)**   | 0.9385    | **0.9543**    | 0.9485     | 0.9073     | 0.8544  | ✅ tRPC-Agent-Go |
| **Answer Correctness (正确性)** | 0.7901    | **0.8379**    | 0.8072     | 0.7608     | 0.6683  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5329    | **0.5421**    | 0.5421     | 0.5305     | 0.4923  | ✅ tRPC-Agent-Go / Agno |

**上下文质量：**


| 指标                                 | LangChain  | tRPC-Agent-Go  | Agno   | CrewAI | AutoGen | 胜者             |
| -------------------------------------- | ------------ | ---------------- | -------- | -------- | --------- | ------------------ |
| **Context Precision (精确率)**       | 0.8777     | **0.8973**     | 0.8691 | 0.8665 | 0.8876  | ✅ tRPC-Agent-Go |
| **Context Recall (召回率)**          | 0.9965     | **1.0000**     | 0.9792 | 0.9967 | 0.9933  | ✅ tRPC-Agent-Go |
| **Context Entity Recall (实体召回)** | **0.6467** | 0.6461         | 0.6416 | 0.6350 | 0.6278  | ✅ LangChain     |

#### 2.2 RGB-en_int：多文档信息整合 (100 个问答对)

测试模型检索并综合分散在多个文档中的信息的能力。这是我们端到端评测中最能保留 RGB 原始挑战的子集。

> **LangChain-Chain 作为单次检索基线**：在该子集中，我们额外加入 **LangChain-Chain**（确定性 Chain 流程，每个查询仅触发一次检索）作为基线，用于验证 **Agentic 多步检索**相比**单次检索**在多文档信息整合场景下的优势。由于 en_int 的查询需要综合多篇文档中的信息，能够执行多次迭代搜索的 Agent 框架应显著优于单次检索流程。

**回答质量：**


| 指标                            | LangChain-Chain *（基线）* | LangChain | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen    | 胜者             |
| --------------------------------- | ---------------------------- | ----------- | --------------- | -------- | ------------ | ------------ | ------------------ |
| **Faithfulness (忠实度)**       | 0.9325                     | 0.9647    | 0.9716        | 0.9481 | **0.9740** | 0.9130     | ✅ CrewAI        |
| **Answer Relevancy (相关性)**   | 0.7559                     | 0.9063    | 0.9196        | 0.8754 | 0.9238     | **0.9327** | ✅ AutoGen       |
| **Answer Correctness (正确性)** | 0.6677                     | 0.7244    | **0.7494**    | 0.6960 | 0.6907     | 0.7330     | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5417                     | 0.5401    | **0.5599**    | 0.5411 | 0.5508     | 0.5414     | ✅ tRPC-Agent-Go |

**上下文质量：**


| 指标                                 | LangChain-Chain *（基线）* | LangChain | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen    | 胜者               |
| -------------------------------------- | ---------------------------- | ----------- | --------------- | -------- | ------------ | ------------ | -------------------- |
| **Context Precision (精确率)**       | **0.3136**                 | 0.2718    | 0.2886        | 0.2943 | 0.2925     | 0.2816     | ✅ LangChain-Chain |
| **Context Recall (召回率)**          | 0.7000                     | 0.8776    | 0.8800        | 0.8083 | **0.9150** | 0.9033     | ✅ CrewAI          |
| **Context Entity Recall (实体召回)** | 0.4917                     | 0.5790    | 0.5933        | 0.5833 | 0.6167     | **0.6317** | ✅ AutoGen         |

#### 2.3 RGB-en_fact：事实性 QA + 反事实干扰 (100 个问答对)

事实性查询，问题特征与 en 子集不同。（RGB 原始测试重点为反事实鲁棒性；在我们的端到端评测中，`positive_wrong` 与 `positive` 会一同灌入知识库，因此该子集仍包含反事实干扰。）

**回答质量：**


| 指标                            | LangChain  | tRPC-Agent-Go | Agno   | CrewAI | AutoGen | 胜者             |
| --------------------------------- | ------------ | --------------- | -------- | -------- | --------- | ------------------ |
| **Faithfulness (忠实度)**       | **0.9719** | 0.9608        | 0.9505 | 0.9648 | 0.7810  | ✅ LangChain     |
| **Answer Relevancy (相关性)**   | 0.8370     | **0.9163**    | 0.6748 | 0.8594 | 0.7627  | ✅ tRPC-Agent-Go |
| **Answer Correctness (正确性)** | 0.7226     | **0.8055**    | 0.6148 | 0.6755 | 0.6634  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | 0.5330     | **0.5462**    | 0.5062 | 0.5266 | 0.5058  | ✅ tRPC-Agent-Go |

**上下文质量：**


| 指标                                 | LangChain | tRPC-Agent-Go  | Agno   | CrewAI     | AutoGen    | 胜者                                |
| -------------------------------------- | ----------- | ---------------- | -------- | ------------ | ------------ | ------------------------------------- |
| **Context Precision (精确率)**       | 0.6329    | **0.6466**     | 0.6050 | 0.6308     | 0.6311     | ✅ tRPC-Agent-Go                    |
| **Context Recall (召回率)**          | 0.9796    | **0.9900**     | 0.8700 | **0.9900** | **0.9900** | ✅ tRPC-Agent-Go / CrewAI / AutoGen |
| **Context Entity Recall (实体召回)** | 0.7143    | 0.7100         | 0.6100 | **0.7200** | **0.7200** | ✅ CrewAI / AutoGen                 |

#### RGB 综合分析

> **说明**：LangChain-Chain 不参与 RGB 综合统计，仅在 en_int 子集中作为单次检索基线使用。详见 2.2 节中 Agentic 与单次检索的对比分析。

**3 个子集简单平均值对比 (en + en_int + en_fact)：**


| 指标                                 | LangChain | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen    | 胜者               |
| -------------------------------------- | ----------- | --------------- | -------- | ------------ | ------------ | -------------------- |
| **Faithfulness (忠实度)**            | 0.9681    | 0.9728        | 0.9594 | **0.9771** | 0.8201     | ✅ CrewAI          |
| **Answer Relevancy (相关性)**        | 0.8939    | **0.9301**    | 0.8329 | 0.8968     | 0.8499     | ✅ tRPC-Agent-Go   |
| **Answer Correctness (正确性)**      | 0.7457    | **0.7976**    | 0.7060 | 0.7090     | 0.6882     | ✅ tRPC-Agent-Go   |
| **Answer Similarity (相似度)**       | 0.5353    | **0.5494**    | 0.5298 | 0.5360     | 0.5132     | ✅ tRPC-Agent-Go   |
| **Context Precision (精确率)**       | 0.5941    | **0.6108**    | 0.5895 | 0.5966     | 0.6001     | ✅ tRPC-Agent-Go   |
| **Context Recall (召回率)**          | 0.9512    | 0.9567        | 0.8858 | **0.9672** | 0.9622     | ✅ CrewAI          |
| **Context Entity Recall (实体召回)** | 0.6467    | 0.6498        | 0.6116 | 0.6572     | **0.6598** | ✅ AutoGen         |

**3 个子集加权平均值对比**（en×300 + en_int×100 + en_fact×100，共 500 个 QA 对）：


| 指标                                 | LangChain | tRPC-Agent-Go | Agno   | CrewAI     | AutoGen | 胜者                          |
| -------------------------------------- | ----------- | --------------- | -------- | ------------ | --------- | ------------------------------- |
| **Faithfulness (忠实度)**            | 0.9679    | 0.9781        | 0.9675 | **0.9833** | 0.7986  | ✅ CrewAI                     |
| **Answer Relevancy (相关性)**        | 0.9118    | **0.9398**    | 0.8791 | 0.9010     | 0.8517  | ✅ tRPC-Agent-Go              |
| **Answer Correctness (正确性)**      | 0.7635    | **0.8137**    | 0.7465 | 0.7297     | 0.6803  | ✅ tRPC-Agent-Go              |
| **Answer Similarity (相似度)**       | 0.5344    | **0.5465**    | 0.5347 | 0.5338     | 0.5048  | ✅ tRPC-Agent-Go              |
| **Context Precision (精确率)**       | 0.7076    | **0.7254**    | 0.7013 | 0.7046     | 0.7151  | ✅ tRPC-Agent-Go              |
| **Context Recall (召回率)**          | 0.9693    | 0.9740        | 0.9232 | **0.9790** | 0.9746  | ✅ CrewAI                     |
| **Context Entity Recall (实体召回)** | 0.6467    | **0.6483**    | 0.6236 | **0.6483** | 0.6470  | ✅ tRPC-Agent-Go / CrewAI     |

**各子集第一名统计**（5 个 Agent 框架参与 3 个子集；LangChain-Chain 仅在 en_int 作为基线计入；3 方及以上并列的类别不计入）：


| 框架              | 第一名次数 | 优势领域                                                                                                       |
| ------------------- | ------------ | ---------------------------------------------------------------------------------------------------------------- |
| **tRPC-Agent-Go** | **11**     | Answer Relevancy (en, en_fact)、Answer Correctness (en, en_int, en_fact)、Answer Similarity (en†, en_int, en_fact)、Context Precision (en, en_fact)、Context Recall (en) |
| **CrewAI**        | **4**      | Faithfulness (en, en_int)、Context Recall (en_int)、Context Entity Recall (en_fact†)                              |
| **AutoGen**       | **3**      | Answer Relevancy (en_int)、Context Entity Recall (en_int, en_fact†)                                               |
| **LangChain**     | **2**      | Context Entity Recall (en)、Faithfulness (en_fact)                                                               |
| **Agno**          | **1**      | Answer Similarity (en†)                                                                                          |
| **LangChain-Chain** *（基线）* | **1** | Context Precision (en_int)                                                                                  |

> † = 与其他框架并列

**核心发现：**

1. **tRPC-Agent-Go 在 3 个子集中获得 11 次第一**：平均 Answer Relevancy (0.9301)、Answer Correctness (0.7976)、Answer Similarity (0.5494)、Context Precision (0.6108) 均为最高；en 子集 Context Recall 达到 1.0000。
2. **Agentic 多步检索 vs 单次检索（en_int）**：LangChain-Chain（单次检索）Answer Relevancy 0.7559、Answer Correctness 0.6677、Context Recall 0.7000；所有 Agentic 框架均高于此值（如 tRPC 分别为 0.9196 / 0.7494 / 0.8800）。
3. **CrewAI 获得 4 次第一**：Faithfulness (en: 0.9925, en_int: 0.9740) 和 Context Recall (en_int: 0.9150)。
4. **各框架 Faithfulness 对比**：LangChain、tRPC-Agent-Go、CrewAI、Agno 平均 > 0.93；AutoGen 平均 0.82。
5. **en_int 子集 Context Precision 最低**：所有框架得分 0.27–0.31，其他子集为 0.59–0.90。
6. **加权平均结果**：tRPC-Agent-Go 在 7 项指标中 5 项排名第一（Answer Relevancy 0.9398、Answer Correctness 0.8137、Answer Similarity 0.5465、Context Precision 0.7254、Context Entity Recall 0.6483 与 CrewAI 并列）。

---

### 3. MultiHop-RAG 数据集 (450 个问答对)

**测试环境参数：**

- **数据集**: [MultiHop-RAG](https://github.com/yixuantt/MultiHop-RAG)（[论文](https://arxiv.org/abs/2401.15391)）— 609 篇新闻文章语料，450 个多跳 QA 对（每类问题 150 个）
- **Embedding 模型**: `BGE-M3` (1024 维)
- **Agent 模型**: `DeepSeek-V3.2`
- **评测模型**: `Qwen3.5-397B-A17B`

> **LangChain-Chain 作为单次检索基线**：与 RGB-en_int 子集类似，我们额外加入 LangChain-Chain（确定性 Chain 流程，每个查询仅触发一次检索）作为基线，用于验证 Agentic 多步检索在多跳推理场景下的优势。

**回答质量：**


| 指标                            | LangChain-Chain *（基线）* | LangChain | tRPC-Agent-Go | Agno       | CrewAI | AutoGen | 胜者             |
| --------------------------------- | ---------------------------- | ----------- | --------------- | ------------ | -------- | --------- | ------------------ |
| **Faithfulness (忠实度)**       | 0.4672                     | 0.7639    | 0.7060        | **0.7887** | 0.7460 | 0.7468  | ✅ Agno          |
| **Answer Relevancy (相关性)**   | 0.5213                     | 0.5955    | **0.6424**    | 0.5638     | 0.5639 | 0.5342  | ✅ tRPC-Agent-Go |
| **Answer Correctness (正确性)** | 0.4677                     | 0.4243    | **0.4984**    | 0.4524     | 0.4371 | 0.4495  | ✅ tRPC-Agent-Go |
| **Answer Similarity (相似度)**  | **0.5118**                 | 0.4376    | 0.4699        | 0.4715     | 0.4615 | 0.4904  | ✅ LangChain-Chain |

**上下文质量：**


| 指标                                 | LangChain-Chain *（基线）* | LangChain  | tRPC-Agent-Go | Agno   | CrewAI | AutoGen    | 胜者               |
| -------------------------------------- | ---------------------------- | ------------ | --------------- | -------- | -------- | ------------ | -------------------- |
| **Context Precision (精确率)**       | **0.3820**                 | 0.3209     | 0.3574        | 0.3526 | 0.3409 | 0.3520     | ✅ LangChain-Chain |
| **Context Recall (召回率)**          | 0.5644                     | 0.7416     | 0.7733        | 0.7756 | 0.7523 | **0.8111** | ✅ AutoGen         |
| **Context Entity Recall (实体召回)** | 0.2422                     | **0.2711** | 0.2667        | 0.2622 | 0.2599 | 0.2556     | ✅ LangChain       |

**观察：**

1. **多跳查询难度显著高于单跳**：所有指标相比 RGB 和 HuggingFace 数据集均大幅下降，反映了跨文档推理的固有难度。
2. **tRPC-Agent-Go 在回答质量上领先**：**Answer Relevancy** (0.6424) 和 **Answer Correctness** (0.4984) 均排名第一，继续保持生成准确答案的优势。
3. **AutoGen 上下文召回最强**：**Context Recall** (0.8111) 显著领先其他框架，表明检索到了更全面的证据。
4. **Agno 忠实度最高**：**Faithfulness** (0.7887) 排名第一，表明在多跳推理中更好地遵循了检索内容。
5. **Context Precision 普遍偏低（~0.32-0.38）**：与 RGB-en_int 子集类似，多跳查询使所有框架的检索精度下降，因为相关证据分散在多个文档中。
6. **Agentic 多步检索 vs 单次检索**：LangChain-Chain（单次检索）Faithfulness 仅 0.4672、Context Recall 0.5644，远低于所有 Agentic 框架（Faithfulness 0.70–0.79、Context Recall 0.74–0.81）。但 LangChain-Chain 在 Answer Similarity (0.5118) 和 Context Precision (0.3820) 排名第一，与 RGB-en_int 的规律一致——单次检索返回的上下文更紧凑，精确率更高，但召回率和忠实度显著不足。

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
