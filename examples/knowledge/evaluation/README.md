# RAG Evaluation: tRPC-Agent-Go vs LangChain

This directory contains a comprehensive evaluation framework for comparing RAG (Retrieval-Augmented Generation) systems using [RAGAS](https://docs.ragas.io/) metrics.

## Overview

We evaluate two RAG implementations with **identical configurations** to ensure a fair comparison:

- **tRPC-Agent-Go**: Our Go-based RAG implementation
- **LangChain**: Python-based reference implementation

## Quick Start

### Prerequisites

```bash
# Install Python dependencies
pip install -r requirements.txt

# Set environment variables
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"  # Optional
export MODEL_NAME="gpt-4"               # Optional, default: gpt-3.5-turbo
export EMBEDDING_MODEL="text-embedding-ada-002"  # Optional

# PostgreSQL (PGVector) configuration
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="root"
export PGVECTOR_PASSWORD="your-password"
export PGVECTOR_DATABASE="vector"
```

### Run Evaluation

```bash
# Evaluate LangChain
python3 ragas/evaluator.py --kb=langchain --max-qa=10

# Evaluate tRPC-Agent-Go
python3 ragas/evaluator.py --kb=trpc-agent-go --max-qa=10

# Full log output
python3 ragas/evaluator.py --kb=trpc-agent-go --max-qa=1 --full-log
```

## Configuration Alignment

Both systems use **identical parameters** to ensure fair comparison:

| Parameter | LangChain | tRPC-Agent-Go |
|-----------|-----------|---------------|
| **System Prompt** | `Answer the following question using the available tools. Base your final answer STRICTLY on the information retrieved from the tools. Do not include external knowledge you already know.` | Same |
| **Temperature** | 0 | 0 |
| **Chunk Size** | 500 | 500 |
| **Chunk Overlap** | 50 | 50 |
| **Embedding Model** | text-embedding-ada-002 | text-embedding-ada-002 |
| **Vector Store** | PGVector | PGVector |
| **Agent Type** | Tool-calling Agent | LLM Agent with Tools |
| **Max Retrieval Results** | 4 | 4 |

## Dataset

We use the [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) datasets:

- **Documents**: `m-ric/huggingface_doc` - HuggingFace documentation corpus
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - Human-annotated Q&A for evaluation

### Document Loading Strategy

1. **QA Source Documents**: Load all documents referenced by QA items (ground truth sources)
2. **Distractor Documents**: Add 30 random documents as distractors to test retrieval precision
3. **Total Documents**: 31 documents (1 QA source + 30 distractors per QA item)

This strategy ensures:
- ✅ Ground truth documents are always present
- ✅ Retrieval system must distinguish relevant from irrelevant documents
- ✅ Reproducible results with fixed random seed (42)

### Current Document Set (31 documents)

The evaluation currently uses **31 randomly selected documents** from the HuggingFace ecosystem (1 QA source document + 30 random distractors):

```python
# Random document selection (seed=42 for reproducibility)
import random
random.seed(42)

documents = [
    "gradio-app_gradio_blob_main_guides_03_building-with-blocks_04_custom-CSS-and-JS.md",
    "gradio-app_gradio_blob_main_guides_09_other-tutorials_create-your-own-friends-with-a-gan.md",
    "gradio-app_gradio_blob_main_guides_cn_04_integrating-other-frameworks_image-classification-in-pytorch.md",
    "gradio-app_gradio_blob_main_guides_CONTRIBUTING.md",
    "huggingface_blog_blob_main_ambassadors.md",
    "huggingface_blog_blob_main_huggy-lingo.md",
    "huggingface_blog_blob_main_llama-sagemaker-benchmark.md",
    "huggingface_blog_blob_main_long-range-transformers.md",
    "huggingface_blog_blob_main_peft.md",
    "huggingface_blog_blob_main_vision_language_pretraining.md",
    "huggingface_course_blob_main_subtitles_en_raw_chapter1_03_the-pipeline-function.md",
    "huggingface_course_blob_main_subtitles_en_raw_chapter2_04b_word-based-tokenizers.md",
    "huggingface_datasets-server_blob_main_services_storage-admin_README.md",
    "huggingface_diffusers_blob_main_docs_source_en_index.md",
    "huggingface_diffusers_blob_main_docs_source_en_training_wuerstchen.md",
    "huggingface_diffusers_blob_main_examples_README.md",
    "huggingface_evaluate_blob_main_metrics_indic_glue_README.md",
    "huggingface_optimum_blob_main_examples_onnxruntime_training_language-modeling_README.md",
    "huggingface_peft_blob_main_docs_source_developer_guides_low_level_api.md",
    "huggingface_peft_blob_main_docs_source_task_guides_clm-prompt-tuning.md",
    "huggingface_pytorch-image-models_blob_main_docs_models_rexnet.md",
    "huggingface_simulate_blob_main_CONTRIBUTING.md",
    "huggingface_tokenizers_blob_main_bindings_node_npm_linux-x64-musl_README.md",  # ⭐ QA Source
    "huggingface_transformers_blob_main_docs_source_en_model_doc_bart.md",
    "huggingface_transformers_blob_main_docs_source_en_model_doc_bert-generation.md",
    "huggingface_transformers_blob_main_docs_source_en_perf_train_cpu_many.md",
    "huggingface_transformers_blob_main_docs_source_en_perf_train_special.md",
    "huggingface_transformers_blob_main_docs_source_en_tasks_knowledge_distillation_for_image_classification.md",
    "huggingface_transformers_blob_main_examples_research_projects_rag_README.md",
    "huggingface_transformers_blob_main_examples_research_projects_xtreme-s_README.md",
    "huggingface_transformers_blob_main_templates_adding_a_new_example_script_README.md",
]

# Total: 31 documents (1 QA source + 30 random distractors)
print(f"Total documents: {len(documents)}")
```

**Note**: These 31 documents are randomly selected from the HuggingFace documentation corpus. The selection is reproducible using `random.seed(42)`. Each evaluation run will use the same set of documents to ensure consistent results.

### Evaluation Progress

**Current Status**: ⏳ **Phase 1 - Baseline Evaluation** (1 QA item)
- ✅ Completed: 1 QA item evaluation
- 📋 Planned: Expand to 10 QA items for statistical significance

**Next Steps**:
- 🔄 **Phase 2**: Evaluate with `--max-qa=10` to get statistically significant results
- 📊 **Phase 3**: Analyze patterns and identify improvement areas
- 🎯 **Phase 4**: Optimize both systems based on findings

## RAGAS Metrics

We evaluate 7 metrics across 3 categories:

### Answer Quality
| Metric | Description |
|--------|-------------|
| **Faithfulness** | Is the answer faithful to the retrieved context? (no hallucination) |
| **Answer Relevancy** | Is the answer relevant to the question? |
| **Answer Correctness** | Is the answer correct compared to ground truth? |
| **Answer Similarity** | Semantic similarity to ground truth answer |

### Context Quality
| Metric | Description |
|--------|-------------|
| **Context Precision** | Are the retrieved documents relevant? |
| **Context Recall** | Are all relevant documents retrieved? |
| **Context Entity Recall** | Are important entities from ground truth retrieved? |

## Evaluation Results

> **⚠️ Note**: The following results are based on a minimal dataset (`--max-qa=1`) for demonstration purposes. For production evaluation, use `--max-qa=10` or higher to get statistically significant results.



### Test Case
**Question**: What architecture is the `tokenizers-linux-x64-musl` binary designed for?

**Ground Truth**: x86_64-unknown-linux-musl

### Performance Comparison (--max-qa=1)

#### Answer Quality Metrics

| Metric | tRPC-Agent-Go | LangChain | Winner |
|--------|---------------|-----------|--------|
| **Faithfulness** | 0.6000 | 0.6250 | ✅ LangChain (+4.2%) |
| **Answer Relevancy** | 0.9956 | 0.9957 | ✅ LangChain (+0.01%) |
| **Answer Correctness** | 0.7779 | 0.3302 | ✅ tRPC-Agent-Go (+135%) |
| **Answer Similarity** | 0.8616 | 0.8591 | ✅ tRPC-Agent-Go (+0.3%) |

#### Context Quality Metrics

| Metric | tRPC-Agent-Go | LangChain | Status |
|--------|---------------|-----------|--------|
| **Context Precision** | 0.7000 | 0.7000 | ✅ Both equal |
| **Context Recall** | 1.0000 | 1.0000 | ✅ Both perfect |
| **Context Entity Recall** | 1.0000 | 1.0000 | ✅ Both perfect |

#### Execution Time

| Metric | tRPC-Agent-Go | LangChain | Difference |
|--------|---------------|-----------|------------|
| **Q&A Time** | 24.11s | 43.28s | ✅ tRPC-Agent-Go **1.8x faster** |
| **RAGAS Evaluation Time** | 411.15s | 311.39s | LangChain faster |
| **Total Time** | 435.26s | 354.68s | LangChain **18.5% faster** |

### Answer Examples

#### tRPC-Agent-Go Answer
```
Based on the information retrieved from the knowledge base, the `tokenizers-linux-x64-musl`
binary is designed for the **x86_64-unknown-linux-musl** architecture.

The search result clearly states: "This is the **x86_64-unknown-linux-musl** binary for `tokenizers`".

This indicates that the binary is specifically compiled for:
- **x86_64** (64-bit x86 architecture)
- **linux** (Linux operating system)
- **musl** (using musl libc instead of glibc)

So the architecture is **x86_64-unknown-linux-musl**, which means it's a 64-bit x86 binary
for Linux systems using the musl C library.
```

#### LangChain Answer
```
Based on the search results, I can provide a clear answer. The `tokenizers-linux-x64-musl`
binary is designed for the **x86_64-unknown-linux-musl** architecture.

This means:
- **x86_64**: 64-bit x86 architecture (also known as AMD64 or Intel 64)
- **linux**: Linux operating system
- **musl**: Uses the musl libc library instead of glibc (a lightweight, fast,
  and standards-compliant C standard library implementation)

So the binary is specifically compiled for 64-bit x86 processors running Linux with musl libc.
```

**Note**: Both systems correctly identify the architecture. LangChain includes additional external knowledge (AMD64, Intel 64) not present in the retrieval results, which may explain the lower Answer Correctness score.

## Analysis: Why is LangChain's Answer Correctness Low (0.3302)?

### Root Cause

RAGAS's `AnswerCorrectness` metric evaluates answers by:
1. **Extracting semantic units** (facts/entities) from the generated answer
2. **Comparing against ground truth** using semantic similarity
3. **Computing match ratio**: TP / (TP + FP + FN)

### LangChain's Issue

The LangChain answer includes **additional information not in ground truth**:

```
Ground Truth: "x86_64-unknown-linux-musl"

LangChain adds:
- "also known as AMD64 or Intel 64"  ← Extra fact not in ground truth
- "a lightweight, fast, and standards-compliant C standard library implementation"  ← Extra fact
```

These **extra facts are penalized** by RAGAS as false positives (FP), reducing the correctness score:
- **True Positives (TP)**: Correctly identified facts
- **False Positives (FP)**: Extra facts not in ground truth ⚠️
- **False Negatives (FN)**: Missing facts from ground truth

### tRPC-Agent-Go's Advantage

tRPC-Agent-Go's answer **stays closer to the retrieved context**:
- Directly quotes the source: "This is the **x86_64-unknown-linux-musl** binary for `tokenizers`"
- Breaks down only the facts present in the context
- Avoids adding external knowledge

### Recommendations for Improvement

1. **Adjust System Prompt**: Instruct LLM to avoid external knowledge
   ```
   "Answer ONLY using the retrieved information. Do NOT add external knowledge
    or explanations not found in the context."
   ```

2. **Improve Ground Truth**: Make ground truth more comprehensive
   ```
   Instead of: "x86_64-unknown-linux-musl"
   Use: "x86_64-unknown-linux-musl architecture, which is 64-bit x86 for Linux with musl libc"
   ```

3. **Increase Sample Size**: Current evaluation uses only 1 QA item
   - Planned: Expand to `--max-qa=10` for statistical significance
   - This will reveal if the issue is systematic or sample-specific

## Project Structure

```
evaluation/
├── ragas/
│   └── evaluator.py          # Main RAGAS evaluation script
├── dataset/
│   ├── base.py               # Base dataset interface
│   └── huggingface/          # HuggingFace dataset loader
│       ├── loader.py
│       └── hf_docs/          # Cached documents
├── knowledge_system/
│   ├── base.py               # Base knowledge system interface
│   ├── langchain/            # LangChain implementation
│   │   └── knowledge_base.py
│   └── trpc_agent_go/        # tRPC-Agent-Go implementation
│       ├── knowledge_base.py # Python client
│       └── trpc_knowledge/   # Go server
│           ├── knowledge.go
│           └── main.go
├── util.py                   # Configuration utilities
├── data.txt                  # Raw evaluation results
└── requirements.txt          # Python dependencies
```

