# RAG Evaluation: tRPC-Agent-Go vs LangChain vs Agno

This directory contains a comprehensive evaluation framework for comparing RAG (Retrieval-Augmented Generation) systems using [RAGAS](https://docs.ragas.io/) metrics.

## Overview

We evaluate three RAG implementations with **identical configurations** to ensure a fair comparison:

- **tRPC-Agent-Go**: Our Go-based RAG implementation
- **LangChain**: Python-based reference implementation
- **Agno**: Python-based AI agent framework with built-in knowledge base support

## Quick Start

### Prerequisites

```bash
# Install Python dependencies
pip install -r requirements.txt

# Set environment variables
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"  # Optional
export MODEL_NAME="deepseek-v3.2"       # Optional, model for knowledge/RAG
export EVAL_MODEL_NAME="gemini-2.5-flash"  # Optional, model for evaluation (default: gemini-2.5-flash)
export EMBEDDING_MODEL="server:274214"  # Optional

# Optional: Use different API endpoint for evaluation
# export EVAL_API_KEY="your-eval-api-key"     # Default: same as OPENAI_API_KEY
# export EVAL_BASE_URL="your-eval-base-url"   # Default: same as OPENAI_BASE_URL

# PostgreSQL (PGVector) configuration
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="root"
export PGVECTOR_PASSWORD="123"           # Default password
export PGVECTOR_DATABASE="vector"
```

### Run Evaluation

```bash
# Evaluate with LangChain knowledge base
python3 main.py --evaluator=ragas --kb=langchain

# Evaluate with tRPC-Agent-Go knowledge base
python3 main.py --evaluator=ragas --kb=trpc-agent-go

# Evaluate with Agno knowledge base
python3 main.py --evaluator=ragas --kb=agno

# With custom retrieval k value
python3 main.py --evaluator=ragas --kb=langchain --k=10

# Full log output (show complete answers and contexts)
python3 main.py --evaluator=ragas --kb=trpc-agent-go --max-qa=1 --full-log

# Save results to JSON file
python3 main.py --evaluator=ragas --kb=langchain --output=results.json
```

### Command Line Options

| Option | Default | Description |
|--------|---------|-------------|
| `--kb` | langchain | Knowledge base: `langchain`, `trpc-agent-go`, or `agno` |
| `--evaluator` | ragas | Evaluator to use (currently only `ragas`) |
| `--k` | 4 | Number of documents to retrieve per query |
| `--max-qa` | all | Maximum QA items for evaluation |
| `--max-docs` | all | Maximum documents to load |
| `--load` | false | Force reload documents into knowledge base |
| `--skip-load` | false | Skip loading documents |
| `--full-log` | false | Print full answer results for each question |
| `--output` | none | Output file path to save results as JSON |
| `--workers` | 10 | Number of concurrent workers for evaluation |
| `--timeout` | 600 | Timeout in seconds for evaluation |

## Configuration Alignment

All three systems use **identical parameters** to ensure fair comparison:

| Parameter | LangChain | tRPC-Agent-Go | Agno |
|-----------|-----------|---------------|------|
| **Temperature** | 0 | 0 | 0 |
| **Chunk Size** | 500 | 500 | 500 |
| **Chunk Overlap** | 50 | 50 | 50 |
| **Embedding Dimensions** | 1024 | 1024 | 1024 |
| **Vector Store** | PGVector | PGVector | PgVector |
| **Agent Type** | Tool-calling Agent | LLM Agent with Tools | Agno Agent |
| **Max Retrieval Results (k)** | 4 | 4 | 4 |

## System Prompt

To ensure fair comparison, all three systems are configured with **identical** instructions.

**Prompt for LangChain, Agno & tRPC-Agent-Go:**
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

## Dataset

We use the [HuggingFace Documentation](https://huggingface.co/datasets/m-ric/huggingface_doc) datasets. 

**Important Filtering**: To ensure data quality and consistency, we specifically **filter and only use Markdown files** (`.md`) for both the source documents and the QA evaluation pairs.

- **Documents**: `m-ric/huggingface_doc` - Filtered for `.md` documentation files
- **QA Pairs**: `m-ric/huggingface_doc_qa_eval` - Filtered for questions whose source is a `.md` file

## RAGAS Metrics

We evaluate 7 metrics across 3 categories:

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

### Latest Evaluation (54 QA items)

**Test Configuration:**
- **Dataset**: Full LangChain HuggingFace documentation dataset (54 QA items)
- **Embedding Model**: `server:274214` (1024 dimensions)
- **Agent Model**: `DeepSeek-V3.2`
- **Evaluation Model**: `Gemini 2.5 Flash`
- **Retrieval k**: 4 documents per query
- **Chunk Size**: 500 characters
- **Chunk Overlap**: 50 characters

#### Answer Quality Metrics

| Metric | LangChain | tRPC-Agent-Go | Agno | Best |
|--------|-----------|---------------|------|------|
| **Faithfulness** | 0.8978 | 0.8639 | 0.8491 | ✅ LangChain |
| **Answer Relevancy** | 0.8921 | 0.9034 | 0.9625 | ✅ Agno |
| **Answer Correctness** | 0.6193 | 0.6167 | 0.6120 | ✅ LangChain |
| **Answer Similarity** | 0.6535 | 0.6468 | 0.6312 | ✅ LangChain |

#### Context Quality Metrics

| Metric | LangChain | tRPC-Agent-Go | Agno | Best |
|--------|-----------|---------------|------|------|
| **Context Precision** | 0.6267 | 0.6983 | 0.6860 | ✅ tRPC-Agent-Go |
| **Context Recall** | 0.8889 | 0.9259 | 0.9630 | ✅ Agno |
| **Context Entity Recall** | 0.4466 | 0.4846 | 0.4654 | ✅ tRPC-Agent-Go |

#### Execution Time

| Metric | LangChain | tRPC-Agent-Go | Agno |
|--------|-----------|---------------|------|
| **Q&A Total Time** | 753.79s | 795.35s | 1556.45s |
| **Avg Time per Question** | 13.96s | 14.73s | 28.82s |
| **Evaluation Time** | 2049.52s | 1962.01s | 2031.81s |
| **Total Time** | 2803.31s | 2757.36s | 3588.26s |

### Summary

| Category | LangChain | tRPC-Agent-Go | Agno |
|----------|-----------|---------------|------|
| **Faithfulness** | ✅ Best (0.8978) | 2nd | 3rd |
| **Answer Relevancy** | 3rd | 2nd | ✅ Best (0.9625) |
| **Answer Correctness** | ✅ Best (0.6193) | 2nd | 3rd |
| **Answer Similarity** | ✅ Best (0.6535) | 2nd | 3rd |
| **Context Precision** | 3rd | ✅ Best (0.6983) | 2nd |
| **Context Recall** | 3rd | 2nd | ✅ Best (0.9630) |
| **Context Entity Recall** | 3rd | ✅ Best (0.4846) | 2nd |
| **Speed (Q&A)** | ✅ Fastest | 2nd | 3rd |

**Key Observations**:

1. **LangChain** excels in answer quality metrics (Faithfulness, Correctness, Similarity) and execution speed.

2. **tRPC-Agent-Go** achieves the best Context Precision and Context Entity Recall, with competitive speed.

3. **Agno** leads in Answer Relevancy and Context Recall, but is significantly slower (2x slower than LangChain/tRPC-Agent-Go).

4. **Speed**: LangChain and tRPC-Agent-Go have similar performance (~14s/question), while Agno takes ~29s/question.



## Project Structure

```
evaluation/
├── main.py                   # Main entry point for evaluation
├── evaluator/
│   ├── base.py               # Base evaluator interface
│   └── ragas/                # RAGAS evaluator implementation
│       └── evaluator.py
├── dataset/
│   ├── base.py               # Base dataset interface
│   └── huggingface/          # HuggingFace dataset loader
│       ├── loader.py
│       └── hf_docs/          # Cached documents
├── knowledge_system/
│   ├── base.py               # Base knowledge system interface
│   ├── langchain/            # LangChain implementation
│   │   └── knowledge_base.py
│   ├── trpc_agent_go/        # tRPC-Agent-Go implementation
│   │   ├── knowledge_base.py # Python client
│   │   └── trpc_knowledge/   # Go server
│   │       ├── knowledge.go
│   │       └── main.go
│   └── agno/                 # Agno implementation
│       └── knowledge_base.py
├── util.py                   # Configuration utilities
└── requirements.txt          # Python dependencies
```

## Architecture

### Evaluator Interface

The evaluation framework uses an abstract `Evaluator` interface to support multiple evaluation methods:

```python
class Evaluator(ABC):
    @abstractmethod
    def evaluate(self, samples: List[EvaluationSample]) -> str:
        """Evaluate samples and return formatted result string."""
        pass
```

### Current Implementations

- **RAGASEvaluator**: Uses RAGAS metrics for comprehensive RAG evaluation

### Adding New Evaluators

To add a new evaluator:

1. Create a new directory under `evaluator/` (e.g., `evaluator/custom/`)
2. Implement the `Evaluator` interface:
   ```python
   from evaluator.base import Evaluator, EvaluationSample
   
   class CustomEvaluator(Evaluator):
       def evaluate(self, samples: List[EvaluationSample]) -> str:
           # Your evaluation logic
           return formatted_result_string
   ```
3. Update `main.py` to support the new evaluator:
   ```python
   if args.evaluator == "custom":
       from evaluator.custom.evaluator import CustomEvaluator
       evaluator = CustomEvaluator()
   ```
