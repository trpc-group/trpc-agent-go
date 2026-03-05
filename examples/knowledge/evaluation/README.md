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

# Full log output (show complete answers and contexts)
python3 main.py --kb=trpc-agent-go --full-log
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

We also use the [RGB Benchmark](https://github.com/chen700564/RGB) ([paper](https://arxiv.org/abs/2309.01431)) for evaluating RAG systems across different retrieval scenarios. RGB provides queries with pre-defined positive (relevant) and negative (irrelevant) passages, enabling controlled noise-rate evaluation.

The English portion includes 3 subsets testing different RAG capabilities:

| Subset | QA Count | Focus | Description |
|--------|----------|-------|-------------|
| **en** (Noise Robustness) | 300 | Noise robustness | Standard queries with positive/negative passages. Tests whether the model can correctly answer when retrieved context contains irrelevant noise documents. |
| **en_int** (Information Integration) | 100 | Information integration | Queries that require combining facts from **multiple** positive passages. Tests the model's ability to synthesize information scattered across documents. |
| **en_fact** (Counterfactual Robustness) | 100 | Counterfactual robustness | Includes `positive_wrong` passages that look similar to real answers but contain **altered facts** (e.g., replacing "Facebook" with "Apple"). Tests whether the model can identify and reject counterfactual information. |

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
- **Evaluation Model**: `Gemini 3 Flash`

#### Answer Quality Metrics


| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Faithfulness** | 0.8614 | **0.9853** | 0.7213 | 0.9655 | 0.9113 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | 0.8529 | 0.8890 | 0.9013 | 0.8383 | **0.9040** | ✅ AutoGen |
| **Answer Correctness** | 0.6912 | **0.8299** | 0.6916 | 0.8101 | 0.7725 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.6740 | **0.7251** | 0.6772 | 0.6948 | 0.6830 | ✅ tRPC-Agent-Go |

#### Context Quality Metrics


| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | AutoGen | Best |
|--------|-----------|---------------|------|--------|---------|------|
| **Context Precision** | 0.6314 | **0.7278** | 0.7046 | 0.6673 | 0.6142 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.8333 | 0.9259 | 0.9259 | **0.9444** | **0.9444** | ✅ CrewAI / AutoGen |
| **Context Entity Recall** | 0.4138 | **0.5034** | 0.4331 | 0.3922 | 0.2902 | ✅ tRPC-Agent-Go |

#### Key Conclusions

1. **tRPC-Agent-Go achieves the best overall performance**: Ranks 1st in 5 out of 7 metrics — **Faithfulness (0.9853)**, **Answer Correctness (0.8299)**, **Answer Similarity (0.7251)**, **Context Precision (0.7278)**, and **Context Entity Recall (0.5034)**, leading in both answer quality and retrieval precision.
2. **AutoGen leads in relevancy**: **Answer Relevancy (0.9040)** ranks 1st (close to Agno's 0.9013), showing the best on-topic answers. Also ties for 1st in **Context Recall (0.9444)**.
3. **CrewAI has the highest recall**: **Context Recall (0.9444)** ties for 1st, indicating the most comprehensive retrieval recall.
4. **Agno excels in relevancy**: **Answer Relevancy (0.9013)** ranks 2nd, demonstrating excellent on-topic answers.
5. **Each framework has its strengths**: LangChain provides a stable and balanced baseline, with each framework excelling in different dimensions.

---

### 2. RGB Dataset

**Test Configuration:**

- **Dataset**: [RGB Benchmark](https://github.com/chen700564/RGB) (English subsets)
- **Embedding Model**: `BGE-M3` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Gemini 3 Flash`

> **Note**: AutoGen results are pending (evaluation in progress).

#### 2.1 RGB-en: Noise Robustness (300 QA Pairs)

Tests whether the model can correctly answer questions when retrieved documents contain irrelevant noise.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | 0.9783 | 0.9872 | 0.7554 | **0.9948** | ✅ CrewAI |
| **Answer Relevancy** | 0.9493 | 0.9534 | **0.9612** | 0.9125 | ✅ Agno |
| **Answer Correctness** | 0.7969 | **0.8462** | 0.7141 | 0.7680 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5308 | **0.5401** | 0.5040 | 0.5327 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Context Precision** | 0.9407 | **0.9539** | 0.9452 | 0.9393 | ✅ tRPC-Agent-Go |
| **Context Recall** | **1.0000** | **1.0000** | **1.0000** | **1.0000** | Tied |
| **Context Entity Recall** | 0.6378 | 0.6478 | **0.6583** | 0.6467 | ✅ Agno |

#### 2.2 RGB-en_int: Information Integration (100 QA Pairs)

Tests the model's ability to synthesize information scattered across multiple documents.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | 0.9523 | **0.9743** | 0.8615 | 0.9623 | ✅ tRPC-Agent-Go |
| **Answer Relevancy** | **0.9301** | 0.9061 | 0.9146 | 0.9094 | ✅ LangChain |
| **Answer Correctness** | 0.7258 | **0.8059** | 0.7203 | 0.7277 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5441 | **0.5683** | 0.5447 | 0.5546 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Context Precision** | 0.2868 | 0.3118 | **0.3244** | 0.3069 | ✅ Agno |
| **Context Recall** | 0.9133 | 0.9233 | **0.9300** | 0.9250 | ✅ Agno |
| **Context Entity Recall** | 0.6317 | **0.6500** | 0.6350 | 0.6417 | ✅ tRPC-Agent-Go |

#### 2.3 RGB-en_fact: Counterfactual Robustness (100 QA Pairs)

Tests whether the model can identify and reject counterfactual (altered facts) information in retrieved documents.

**Answer Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Faithfulness** | 0.9529 | 0.9533 | 0.6966 | **0.9653** | ✅ CrewAI |
| **Answer Relevancy** | 0.9204 | **0.9471** | 0.9317 | 0.8753 | ✅ tRPC-Agent-Go |
| **Answer Correctness** | 0.7936 | **0.8467** | 0.6816 | 0.7334 | ✅ tRPC-Agent-Go |
| **Answer Similarity** | 0.5499 | **0.5672** | 0.5171 | 0.5500 | ✅ tRPC-Agent-Go |

**Context Quality:**

| Metric | LangChain | tRPC-Agent-Go | Agno | CrewAI | Best |
|--------|-----------|---------------|------|--------|------|
| **Context Precision** | 0.8652 | 0.8641 | 0.8495 | **0.8694** | ✅ CrewAI |
| **Context Recall** | **0.9900** | **0.9900** | 0.9700 | **0.9900** | Tied |
| **Context Entity Recall** | 0.7300 | **0.7400** | 0.7100 | 0.7300 | ✅ tRPC-Agent-Go |

#### RGB Summary & Analysis

**Cross-subset Winner Count** (1st place across all 3 subsets x 7 metrics = 21 comparisons):

| Framework | 1st Place Count | Strongest Areas |
|-----------|----------------|-----------------|
| **tRPC-Agent-Go** | **11** | Answer Correctness, Answer Similarity, Faithfulness, Context Entity Recall |
| **Agno** | **4** | Answer Relevancy, Context Precision (en_int), Context Recall (en_int) |
| **CrewAI** | **3** | Faithfulness (en, en_fact), Context Precision (en_fact) |
| **LangChain** | **2** | Answer Relevancy (en_int), Context Precision (en_fact) |

**Key Findings:**

1. **tRPC-Agent-Go consistently leads in answer quality**: Ranks 1st in **Answer Correctness** and **Answer Similarity** across all 3 subsets, demonstrating the most accurate and reliable answers regardless of retrieval scenario.
2. **Faithfulness is strong across frameworks**: All frameworks (except Agno) achieve > 0.95 faithfulness, indicating minimal hallucination. CrewAI edges ahead in the noise robustness and counterfactual subsets.
3. **Information Integration (en_int) is the hardest task**: Context Precision drops significantly for all frameworks (0.28-0.32 vs 0.85-0.95 in other subsets), reflecting the inherent difficulty of multi-document reasoning. Agno performs relatively better here.
4. **All frameworks achieve near-perfect Context Recall on en**: Context Recall = 1.0 for all frameworks on the noise robustness subset, suggesting the retrieval step is highly effective when documents are straightforward.
5. **Agno shows weaker faithfulness**: Consistently lower Faithfulness (0.69-0.86) across all subsets indicates a higher tendency to generate content beyond retrieved documents.

### 3. Vertical Evaluation: tRPC-Agent-Go Hybrid Search Weight Ablation

To find the optimal weight ratio for PGVector Hybrid Search (vector similarity + sparse text retrieval) in tRPC-Agent-Go, we designed a gradient ablation experiment with 11 steps ranging from pure text (`v0_t100`) to pure vector (`v100_t0`).

**Test Configuration:**
- **Dataset**: HuggingFace Documentation QA subset (10 sampled QA pairs)
- **Retrieval Configuration**: Top K = 4
- **Embedding / Agent / Eval Models**: Same as the main evaluation

**Results (sorted by vector weight from low to high):**

| Config (vector_weight\_text_weight) | Faithfulness | Answer Relevancy | Answer Correctness | Answer Similarity | Context Precision | Context Recall | Context Entity Recall |
| ----------------------------------- | ------------ | ---------------- | ------------------ | ----------------- | ----------------- | -------------- | --------------------- |
| **hybrid_v0_t100** (pure text)      | 0.7625 | 0.6862 | 0.5830 | 0.6785 | 0.4046 | 0.6000 | 0.3500 |
| **hybrid_v10_t90**                  | 0.8417 | 0.6090 | 0.6260 | 0.6840 | 0.5358 | 0.8000 | 0.5500 |
| **hybrid_v20_t80**                  | 0.8500 | 0.6804 | 0.5279 | 0.6691 | 0.5258 | 0.8000 | 0.5000 |
| **hybrid_v30_t70**                  | 0.9750 | 0.6744 | 0.4706 | 0.6622 | 0.5624 | 0.8000 | 0.4500 |
| **hybrid_v40_t60**                  | 0.8800 | 0.7348 | 0.5657 | 0.6963 | 0.6109 | 0.9000 | 0.5000 |
| **hybrid_v50_t50**                  | 0.8667 | 0.7296 | 0.5921 | 0.6817 | 0.5795 | 0.8000 | 0.5500 |
| **hybrid_v60_t40**                  | 0.9000 | 0.8126 | 0.6955 | 0.7086 | 0.6223 | 0.9000 | 0.5500 |
| **hybrid_v70_t30**                  | 0.9000 | 0.7929 | 0.6240 | 0.7045 | 0.6787 | 0.9000 | 0.4500 |
| **hybrid_v80_t20**                  | 0.9000 | 0.8044 | 0.6305 | 0.7021 | 0.7018 | 0.9000 | 0.4700 |
| **hybrid_v90_t10**                  | 1.0000 | 0.8544 | 0.6543 | 0.7232 | 0.7257 | 0.9000 | **0.5750** |
| **hybrid_v100_t0** (pure vector)    | **1.0000** | **0.8787** | **0.7648** | **0.7493** | **0.7665** | **1.0000** | 0.5500 |

**Key Findings & Analysis:**

1. **Pure vector retrieval (v100_t0) achieves overwhelming advantage**:
   With the current tech stack (high-quality `BGE-M3` 1024-dim embedding model) and Markdown QA dataset, **pure vector retrieval ranks 1st in 6 out of 7 metrics (all except Entity Recall)**. This indicates that semantic representations are sufficient to capture the relevance between documents and queries.
2. **Pure text retrieval (v0_t100) performs worst**:
   When degraded to pure sparse text retrieval, Context Recall drops sharply to 0.6000 and Context Precision is only 0.4046, resulting in the lowest Answer Correctness across all configurations.
3. **"Text penalty" phenomenon in hybrid retrieval**:
   Observing the intermediate gradients (v10 to v90) reveals a clear trend: **as text weight increases, overall metrics decline**. For example, at equal weights (v50_t50), Answer Correctness drops to 0.5921; only when vector weight reaches 0.9 (v90_t10) do metrics recover to near pure-vector levels. This suggests that in the current scenario, the "literal matching" benefit from text keyword retrieval is far outweighed by the noise it introduces.

**Practical Recommendations**:
For standard RAG scenarios (especially systems with high-quality LLMs and embeddings), **it is recommended to maximize the vector retrieval weight or set it as the dominant factor (>0.9)**. Only consider increasing sparse text retrieval weight in scenarios with highly specialized jargon or non-semantic identifiers (e.g., product codes).

### 4. Vertical Evaluation: Reciprocal Rank Fusion (RRF) Mode

In addition to Weighted Score Fusion, PGVector also supports **Reciprocal Rank Fusion (RRF)** as a hybrid search fusion strategy. RRF does not rely on the absolute values of raw scores but instead fuses results based on the **ranking** from each retrieval channel:

```
score(d) = sum(1 / (k + rank_i))
```

where `k` is a constant (default 60) and `rank_i` is the rank of document `d` in the `i`-th retrieval channel. This approach naturally avoids the issue of inconsistent score scales between vector and text scores.

**Test Configuration:**
- **Dataset**: HuggingFace Documentation QA subset (10 sampled QA pairs)
- **Retrieval Configuration**: Top K = 4, RRF k=60, CandidateRatio=3
- **Embedding / Agent / Eval Models**: Same as the main evaluation

**Results:**

| Fusion Strategy | Faithfulness | Answer Relevancy | Answer Correctness | Answer Similarity | Context Precision | Context Recall | Context Entity Recall |
| --------------- | ------------ | ---------------- | ------------------ | ----------------- | ----------------- | -------------- | --------------------- |
| **RRF** (k=60) | 1.0000 | 0.7502 | 0.5439 | 0.6755 | 0.5957 | 0.8000 | 0.4000 |
| **Weighted** (v100_t0, pure vector) | 1.0000 | 0.8787 | 0.7648 | 0.7493 | 0.7665 | 1.0000 | 0.5500 |
| **Weighted** (v90_t10) | 1.0000 | 0.8544 | 0.6543 | 0.7232 | 0.7257 | 0.9000 | 0.5750 |
| **Weighted** (v50_t50, equal) | 0.8667 | 0.7296 | 0.5921 | 0.6817 | 0.5795 | 0.8000 | 0.5500 |

**Analysis:**

1. **RRF underperforms pure vector weighted fusion in the current scenario**: RRF's Answer Correctness (0.5439) and Context Precision (0.5957) are significantly lower than pure vector mode (0.7648 / 0.7665), and Context Recall also drops from 1.0 to 0.8.
2. **RRF is comparable to medium-weight fusion**: RRF's metrics are close to v50_t50, suggesting that under the current dataset, RRF's rank-based fusion is roughly equivalent to equal-weight fusion between vector and text channels.
3. **Root cause**: With the current HuggingFace Markdown documents + BGE-M3 high-quality embeddings, the vector retrieval channel quality is far superior to the sparse text retrieval channel. RRF gives relatively equal weight to both channels' rankings (both use `1/(k+rank)`), which effectively **amplifies the influence of the lower-quality text channel**, diluting the advantage of the high-quality vector channel. Weighted fusion, on the other hand, can suppress text channel noise by setting a very high vector weight (e.g., 0.9 or 1.0).

**Conclusion**: RRF is better suited for scenarios where **both retrieval channels are of comparable quality** (e.g., when high-quality vector retrieval and BM25 retrieval coexist). When one channel is clearly superior to the other, weighted fusion with appropriate weight tuning is the better choice.

---

### Evaluation Observations

Through packet capture analysis during evaluation, we found that all frameworks follow **fairly similar request flows** when using the same LLM model — essentially the standard RAG pipeline of agent calling search tool, retrieving context, and generating answers.

Key considerations:

- **Small dataset size**: The HuggingFace evaluation dataset contains only 1900+ documents and 54 QA pairs. The RGB dataset provides 300 + 100 + 100 = 500 QA pairs with controlled retrieval scenarios.
- **Prompt sensitivity**: It is undeniable that system prompts have a significant impact on agent execution under the current dataset, which in turn greatly affects the final scores. We have ensured unified system prompts across all frameworks.
- **Chunking strategy may have an impact**: After controlling for system prompt differences, different frameworks' chunking implementations (chunk size, overlap, boundary detection, etc.) may affect retrieval and answer quality, which in turn could influence Context Precision, Context Recall, and other retrieval metrics.
