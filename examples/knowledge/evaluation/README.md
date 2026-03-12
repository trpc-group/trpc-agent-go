# RAG Evaluation: tRPC-Agent-Go vs LangChain vs Agno vs CrewAI vs AutoGen

This directory contains a comprehensive evaluation framework for comparing RAG (Retrieval-Augmented Generation) systems using [RAGAS](https://docs.ragas.io/) metrics.

## Overview

We evaluate five RAG implementations with **identical configurations** to ensure a fair comparison:

- **tRPC-Agent-Go**: Our Go-based RAG implementation
- **LangChain**: Python-based reference implementation
- **Agno**: Python-based AI agent framework with built-in knowledge base support
- **CrewAI**: Python-based multi-agent framework with ChromaDB vector store
- **AutoGen**: Microsoft's Python-based multi-agent framework

## Quick Start

### Prerequisites

```bash
# Install Python dependencies
pip install -r requirements.txt

# Set environment variables
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"  # Optional
export MODEL_NAME="deepseek-v3.2"        # Optional, model for RAG
export EVAL_MODEL_NAME="gemini-3-flash"   # Optional, model for evaluation
export EMBEDDING_MODEL="server:274214"  # Optional

# PostgreSQL (PGVector) configuration
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="root"
export PGVECTOR_PASSWORD="123"           # Default password
export PGVECTOR_DATABASE="vector"
```

### Run Evaluation

```bash
# Evaluate with LangChain
python3 main.py --kb=langchain

# Evaluate with tRPC-Agent-Go
python3 main.py --kb=trpc-agent-go

# Evaluate with Agno
python3 main.py --kb=agno

# Evaluate with AutoGen
python3 main.py --kb=autogen
```

## Configuration Alignment

All five systems use **identical parameters** to ensure fair comparison:


| Parameter | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen |
|-----------|-----------|---------------|------|--------|---------|
| **Temperature** | 0 | 0 | 0 | 0 | 0 |
| **Chunk Size** | 500 | 500 | 500 | 500 | 500 |
| **Chunk Overlap** | 50 | 50 | 50 | 50 | 50 |
| **Embedding Dimensions** | 1024 | 1024 | 1024 | 1024 | 1024 |
| **Vector Store** | PGVector | PGVector | PgVector | ChromaDB | PGVector |
| **Search Mode** | Vector | Vector (Hybrid disabled) | Vector | Vector | Vector |
| **Knowledge Base Build** | Native framework method | Native framework method | Native framework method | Native framework method | Native framework method |
| **Agent Type** | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) | Agent + KB (ReAct disabled) |
| **Max Retrieval Results (k)** | 4 | 4 | 4 | 4 | 4 |

> 📝 **tRPC-Agent-Go Notes**:
>
> - **Search Mode**: tRPC-Agent-Go uses Hybrid Search (vector similarity + full-text search) by default, but for fair comparison with other frameworks, **Hybrid Search is disabled** in this evaluation. All frameworks use pure Vector Search (vector similarity only).

> 📝 **CrewAI Notes**:
>
> - **Vector Store**: CrewAI does not currently support PGVector for knowledge base construction, so ChromaDB is used as the vector store.
> - **Bug Fix**: CrewAI (v1.9.0) has a bug where it prioritizes `content` over `tool_calls` when the LLM (e.g., DeepSeek-V3.2) returns both simultaneously, causing the Agent to skip tool invocations. We applied a Monkey Patch to `LLM._handle_non_streaming_response` to prioritize `tool_calls`, ensuring fair evaluation. See `knowledge_system/crewai/knowledge_base.py` for details.

## System Prompt

To ensure fair comparison, all five systems are configured with **identical** instructions.

**Prompt for LangChain, Agno, tRPC-Agent-Go, CrewAI & AutoGen:**

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

## Datasets

### HuggingFace Documentation

We use the [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) dataset.

**Important Filtering**: To ensure data quality and consistency, we specifically **filter and only use Markdown files** (`.md`) for both the source documents and the QA evaluation pairs.

- **Documents**: `m-ric/huggingface_doc` - Filtered for `.md` documentation files
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - Filtered for questions whose source is a `.md` file

### RGB (Retrieval-Augmented Generation Benchmark)

We also use the [RGB Benchmark](https://github.com/chen700564/RGB) ([paper](https://arxiv.org/abs/2309.01431)) as a QA data source. RGB originally provides queries with pre-defined positive (relevant) and negative (irrelevant) passages for evaluating 4 RAG abilities: noise robustness, negative rejection, information integration, and counterfactual robustness.

The English portion includes 3 subsets with different QA characteristics:

| Subset | QA Count | Original Focus | Description |
|--------|----------|----------------|-------------|
| **en** | 300 | Noise robustness | Standard factual queries with clear single-source answers. Each query has well-defined positive passages and separate negative (irrelevant) passages. |
| **en_int** | 100 | Information integration | Queries whose answers require combining facts from **multiple** positive passages. The answer is scattered across several documents. |
| **en_fact** | 100 | Counterfactual robustness | Queries with both correct `positive` passages and `positive_wrong` passages that contain **altered facts** (e.g., replacing "Facebook" with "Apple"). |

> **Important: How we use RGB differs from the original paper.** In the original RGB evaluation, pre-selected positive + negative passages are directly concatenated and fed to the LLM as context. In our evaluation, we only load the **positive passages** into each framework's knowledge base as documents, and let the framework perform its own retrieval + generation pipeline end-to-end. This means:
> - **en**: Negative (noise) passages are **not** loaded into the knowledge base, so the "noise robustness" aspect is not directly tested. Instead, it serves as a **standard factual QA** benchmark.
> - **en_fact**: `positive_wrong` (counterfactual) passages are **not** loaded, so "counterfactual robustness" is not directly tested. Instead, it serves as another **factual QA** benchmark with different question characteristics.
> - **en_int**: The information integration challenge **is** preserved, since answers genuinely require synthesizing multiple retrieved documents.

### MultiHop-RAG

We use the [MultiHop-RAG](https://github.com/yixuantt/MultiHop-RAG) ([paper](https://arxiv.org/abs/2401.15391)) benchmark dataset, proposed by Yixuan Tang and Yi Yang in 2024. MultiHop-RAG is designed to evaluate RAG systems on **multi-hop queries** — questions that require reasoning over **2-4 documents** to arrive at the correct answer, making it a key benchmark for testing complex reasoning in RAG systems.

**Dataset Structure:**

The dataset consists of two parts, both auto-downloaded from the GitHub repository's `dataset/` directory:

1. **`corpus.json` — News Article Corpus (609 articles)**
   - Sourced from 48 news outlets (The New York Times, BBC News, TechCrunch, The Verge, Financial Times, etc.), covering technology, sports, business, entertainment, and more
   - Each article contains `title`, `body`, `source` (outlet name), `published_at`, `category`, and other metadata
   - We export each article as an individual `.txt` file and load them into each framework's knowledge base

2. **`MultiHopRAG.json` — Multi-hop Query Set (2556 queries)**
   - Each QA entry contains `query`, `answer` (ground truth), `question_type`, and `evidence_list`
   - Each item in `evidence_list` includes a `fact` (key evidence passage) along with source article metadata (title, source, url, etc.), representing the gold-standard evidence required to answer the query

**Question Types and Selection:**

The original dataset has 4 question types. We exclude `null_query` (301 entries) — questions that cannot be answered from the corpus — and take the first 150 from each remaining type:

| Question Type | Selected / Total | Description |
|---------------|-----------------|-------------|
| **comparison_query** | 150 / 856 | Cross-document comparison (e.g., "Which was released earlier, A or B?") |
| **inference_query** | 150 / 816 | Inference from facts scattered across documents (e.g., "Who is the person associated with event X?") |
| **temporal_query** | 150 / 583 | Temporal reasoning across documents (e.g., "Did event X happen before or after event Y?") |

**Total: 450 QA pairs** (3 types × 150) used for evaluation. Gold evidence is derived from the `fact` field in `evidence_list`.

## RAGAS Metrics

### Answer Quality


| Metric | Description | Higher value means |
|--------|-------------|-------------------|
| **Faithfulness** | Is the answer faithful to the retrieved context? (no hallucination) | Answer is more trustworthy, no fabricated content |
| **Answer Relevancy** | Is the answer relevant to the question? | Answer is more on-topic and complete |
| **Answer Correctness** | Is the answer correct compared to ground truth? | Answer is closer to correct answer |
| **Answer Similarity** | Semantic similarity to ground truth answer | Answer text expression is more similar |

### Context Quality


| Metric | Description | Higher value means |
|--------|-------------|-------------------|
| **Context Precision** | Are the retrieved documents relevant? | Retrieval is more precise, less noise |
| **Context Recall** | Are all relevant documents retrieved? | Retrieval is more comprehensive, no missing key info |
| **Context Entity Recall** | Are important entities from ground truth retrieved? | Key information retrieval is more complete |

### Simple Understanding

- **Faithfulness**: "Did you answer based only on retrieved content?" (check for hallucinations)
- **Answer Relevancy**: "Did you answer the question I asked?" (check for relevance)
- **Answer Correctness**: "Did you answer correctly?" (compare with ground truth)
- **Answer Similarity**: "Is your answer similar to the correct answer?" (semantic similarity)
- **Context Precision**: "Is the retrieved content useful?" (check retrieval quality)
- **Context Recall**: "Is the retrieved content sufficient?" (check for missing information)
- **Context Entity Recall**: "Are all key information retrieved?" (check entity coverage)

## Evaluation Results

### 1. HuggingFace Dataset (54 QA Pairs)

**Test Configuration:**

- **Dataset**: Full HuggingFace Markdown documentation dataset (54 QA)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Qwen3.5-397B-A17B`

#### Answer Quality Metrics


| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9722 | **0.9815** | 0.9660 | 0.9753 | 0.8688 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | 0.8914 | 0.8799 | **0.8917** | 0.7820 | 0.8304 | ✅ Agno |
| **Answer Correctness** | 0.6984 | **0.8104** | 0.7741 | 0.7575 | 0.6707 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.6758 | **0.7240** | 0.6989 | 0.7025 | 0.6653 | ✅ tRPC-Agent-Go |

#### Context Quality Metrics


| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.6051 | **0.7098** | 0.6712 | 0.6391 | 0.5445 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.8704 | **0.9444** | 0.9259 | **0.9444** | 0.8889 | ✅ tRPC-Agent-Go / CrewAI |
| **Context Entity Recall** | **0.4898** | 0.4867 | 0.4707 | 0.4599 | 0.3833 | ✅ LangChain |

#### Key Conclusions

1. **tRPC-Agent-Go leads comprehensively**: **Faithfulness (0.9815)**, **Answer Correctness (0.8104)**, **Answer Similarity (0.7240)**, and **Context Precision (0.7098)** all rank 1st, with **Context Recall (0.9444)** tied for 1st with CrewAI. Achieves 5 first-place finishes (including 1 tie), demonstrating the strongest overall performance.
2. **Agno leads in relevancy**: **Answer Relevancy (0.8917)** ranks 1st.
3. **LangChain leads in entity recall**: **Context Entity Recall (0.4898)** ranks 1st.
4. **AutoGen underperforms on this dataset**: All metrics are lower than other frameworks, possibly related to its retrieval strategy on small-scale knowledge bases.

---

### 2. RGB Dataset

**Test Configuration:**

- **Dataset**: [RGB Benchmark](https://github.com/chen700564/RGB) (English subsets)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Qwen3.5-397B-A17B`

#### 2.1 RGB-en: Standard Factual QA (300 QA Pairs)

Standard factual queries with clear single-source answers. (Original RGB focus: noise robustness, but negative passages are not loaded into the knowledge base in our end-to-end evaluation.)

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9735 | 0.9754 | 0.7441 | **0.9888** | 0.7664 | ✅ CrewAI |
| **Answer Relevancy** | 0.9352 | 0.9430 | **0.9583** | 0.9096 | 0.8544 | ✅ Agno |
| **Answer Correctness** | 0.7834 | **0.8278** | 0.6991 | 0.7593 | 0.6683 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5291 | **0.5449** | 0.5038 | 0.5353 | 0.4923 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.8686 | **0.8911** | 0.8807 | 0.8678 | 0.8876 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9933 | 0.9967 | **1.0000** | 0.9900 | 0.9933 | ✅ Agno |
| **Context Entity Recall** | 0.6350 | **0.6533** | 0.6450 | 0.6250 | 0.6278 | ✅ tRPC-Agent-Go |

#### 2.2 RGB-en_int: Multi-document Information Integration (100 QA Pairs)

Tests the model's ability to retrieve and synthesize information scattered across multiple documents. This is the subset where the original RGB challenge is best preserved in our end-to-end evaluation.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9690 | **0.9718** | 0.8499 | 0.9694 | 0.9130 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | 0.9033 | 0.9170 | 0.9015 | 0.9212 | **0.9327** | ✅ AutoGen |
| **Answer Correctness** | 0.7113 | **0.7664** | 0.6889 | 0.6827 | 0.7330 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5363 | **0.5638** | 0.5373 | 0.5419 | 0.5414 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.2822 | 0.2810 | **0.3154** | 0.2774 | 0.2816 | ✅ Agno |
| **Context Recall** | 0.8950 | 0.8850 | 0.8950 | 0.8850 | **0.9033** | ✅ AutoGen |
| **Context Entity Recall** | 0.6067 | 0.5950 | 0.6200 | 0.6200 | **0.6317** | ✅ AutoGen |

#### 2.3 RGB-en_fact: Factual QA (100 QA Pairs)

Factual queries with different question characteristics from en subset. (Original RGB focus: counterfactual robustness, but `positive_wrong` passages are not loaded into the knowledge base in our end-to-end evaluation.)

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | **0.9667** | 0.9595 | 0.6140 | 0.9425 | 0.7810 | ✅ LangChain |
| **Answer Relevancy** | 0.8165 | **0.8941** | 0.6812 | 0.8362 | 0.7627 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7256 | **0.7780** | 0.4723 | 0.6910 | 0.6634 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5298 | **0.5434** | 0.4779 | 0.5357 | 0.5058 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.6281 | **0.6372** | 0.6193 | **0.6371** | 0.6311 | ✅ tRPC-Agent-Go / CrewAI |
| **Context Recall** | **0.9900** | 0.9800 | 0.9600 | 0.9800 | **0.9900** | ✅ LangChain / AutoGen |
| **Context Entity Recall** | **0.7200** | 0.7100 | 0.6900 | **0.7200** | **0.7200** | ✅ LangChain / CrewAI / AutoGen |

#### RGB Summary & Analysis

**Average Across All 3 Subsets (en + en_int + en_fact):**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.9712 | 0.9715 | 0.7392 | **0.9757** | 0.7986 | ✅ CrewAI |
| **Answer Relevancy** | 0.9051 | **0.9280** | 0.8915 | 0.8972 | 0.8517 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7574 | **0.8056** | 0.6517 | 0.7303 | 0.6803 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5307 | **0.5484** | 0.5053 | 0.5367 | 0.5048 | ✅ tRPC-Agent-Go |
| **Context Precision** | 0.7032 | **0.7183** | 0.7154 | 0.7036 | 0.7151 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.9730 | 0.9710 | 0.9710 | 0.9670 | **0.9746** | ✅ AutoGen |
| **Context Entity Recall** | 0.6463 | **0.6530** | 0.6490 | 0.6430 | 0.6470 | ✅ tRPC-Agent-Go |

**Cross-subset Winner Count** (all 5 frameworks across 3 subsets; tied categories with 3+ winners are excluded):

| Framework | 1st Place Count | Strongest Areas |
|-----------|----------------|-----------------|
| **tRPC-Agent-Go** | **11** | Answer Correctness (all), Answer Similarity (all), Context Precision (en, en_fact), Context Entity Recall (en), Faithfulness (en_int), Answer Relevancy (en_fact) |
| **AutoGen** | **5** | Answer Relevancy (en_int), Context Recall (en_int), Context Entity Recall (en_int), Context Recall (en_fact), Context Entity Recall (en_fact) |
| **Agno** | **3** | Answer Relevancy (en), Context Recall (en), Context Precision (en_int) |
| **CrewAI** | **3** | Faithfulness (en), Context Precision (en_fact), Context Entity Recall (en_fact) |
| **LangChain** | **3** | Faithfulness (en_fact), Context Recall (en_fact), Context Entity Recall (en_fact) |

**Key Findings:**

1. **tRPC-Agent-Go dominates answer quality**: Ranks 1st in **Answer Correctness** and **Answer Similarity** across all 3 subsets, and leads in **Answer Relevancy** on average (0.9280). With 11 first-place finishes — the most of any framework — it demonstrates the most accurate and reliable answers regardless of retrieval scenario.
2. **AutoGen excels in context retrieval for multi-document scenarios**: Achieves the highest **Context Recall** on en_int (0.9033) and en_fact (0.9900), and leads in **Context Entity Recall** on en_int (0.6317), showing strong retrieval capabilities especially for multi-document scenarios.
3. **Faithfulness is strong across most frameworks**: LangChain, tRPC-Agent-Go, and CrewAI all achieve > 0.94 faithfulness across subsets, indicating minimal hallucination. Agno (0.61-0.85) and AutoGen (0.77-0.91) show relatively higher tendency to generate content beyond retrieved documents.
4. **Information Integration (en_int) is the hardest task**: Context Precision drops significantly for all frameworks (0.27-0.32 vs 0.62-0.89 in other subsets), reflecting the inherent difficulty of multi-document reasoning.
5. **tRPC-Agent-Go leads in 5 of 7 average metrics**: On the weighted average across all subsets, tRPC-Agent-Go ranks 1st in Answer Relevancy, Answer Correctness, Answer Similarity, Context Precision, and Context Entity Recall.

---

### 3. MultiHop-RAG Dataset (450 QA Pairs)

**Test Configuration:**

- **Dataset**: [MultiHop-RAG](https://github.com/yixuantt/MultiHop-RAG) ([paper](https://arxiv.org/abs/2401.15391)) — 609 news-article corpus, 450 multi-hop QA pairs (150 per question type)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Qwen3.5-397B-A17B`

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.7639 | 0.7060 | **0.7887** | 0.7460 | 0.7468 | ✅ Agno |
| **Answer Relevancy** | 0.5955 | **0.6424** | 0.5638 | 0.5639 | 0.5342 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.4243 | **0.4984** | 0.4524 | 0.4371 | 0.4495 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.4376 | 0.4699 | 0.4715 | 0.4615 | **0.4904** | ✅ AutoGen |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.3209 | **0.3574** | 0.3526 | 0.3409 | 0.3520 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.7416 | 0.7733 | 0.7756 | 0.7523 | **0.8111** | ✅ AutoGen |
| **Context Entity Recall** | **0.2711** | 0.2667 | 0.2622 | 0.2599 | 0.2556 | ✅ LangChain |

**Observations:**

1. **Multi-hop queries are significantly harder than single-hop**: All metrics drop substantially compared to RGB and HuggingFace datasets, reflecting the inherent difficulty of reasoning across multiple documents.
2. **tRPC-Agent-Go leads in answer quality**: Ranks 1st in **Answer Relevancy** (0.6424) and **Answer Correctness** (0.4984), continuing its advantage in generating accurate answers.
3. **AutoGen has the strongest context recall**: **Context Recall** (0.8111) significantly outperforms all other frameworks, and **Answer Similarity** (0.4904) also ranks 1st, indicating more comprehensive evidence retrieval.
4. **Agno has the highest faithfulness**: **Faithfulness** (0.7887) ranks 1st, indicating better adherence to retrieved content for multi-hop reasoning.
5. **Context Precision is universally low (~0.32-0.36)**: Similar to the RGB-en_int subset, multi-hop queries push all frameworks' retrieval precision down, as relevant evidence is scattered across multiple documents.

---

### Evaluation Observations

Through packet capture analysis during evaluation, we found that all frameworks follow **fairly similar request flows** when using the same LLM model — essentially the standard RAG pipeline of agent calling search tool, retrieving context, and generating answers.

Key considerations:

- **Dataset scale**: The HuggingFace evaluation dataset contains only 1900+ documents and 54 QA pairs. The RGB dataset provides 300 + 100 + 100 = 500 QA pairs with controlled retrieval scenarios. The MultiHop-RAG dataset adds 609 documents and 450 multi-hop QA pairs requiring cross-document reasoning.
- **Prompt sensitivity**: It is undeniable that system prompts have a significant impact on agent execution under the current dataset, which in turn greatly affects the final scores. We have ensured unified system prompts across all frameworks.
- **Chunking strategy may have an impact**: After controlling for system prompt differences, different frameworks' chunking implementations (chunk size, overlap, boundary detection, etc.) may affect retrieval and answer quality, which in turn could influence Context Precision, Context Recall, and other retrieval metrics.
