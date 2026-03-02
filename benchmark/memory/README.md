# Memory Evaluation Benchmark

This benchmark evaluates the long-term conversational memory capabilities of
trpc-agent-go using the LoCoMo dataset.

## Overview

Based on:

- [LoCoMo: Long-Context Conversational Memory](https://arxiv.org/abs/2402.17753)
- [Memory in the Age of AI Agents](https://arxiv.org/abs/2512.13564)

## Reports

| File | Description |
|------|-------------|
| [REPORT.md](results/REPORT.md) | Full evaluation report (English) |
| [REPORT.zh_CN.md](results/REPORT.zh_CN.md) | Full evaluation report (Chinese) |

## Key Results

**Configuration**: Model=gpt-4o-mini, 10 samples, 1,986 QA pairs.

**Overall Results (No History Injection)**:

| Scenario | Backend | F1 | LLM Score |
|----------|---------|----:|----------:|
| Long-Context | - | **0.472** | **0.523** |
| Auto | pgvector | 0.357 | 0.366 |
| Auto | MySQL | 0.347 | 0.362 |
| Agentic | pgvector | 0.294 | 0.287 |
| Agentic | MySQL | 0.286 | 0.285 |

**History Injection Effect (Auto pgvector)**:

| History | F1 | LLM Score | Adversarial F1 | Open-domain LLM |
|---------|----:|----------:|---------------:|----------------:|
| None | **0.357** | 0.366 | **0.771** | 0.355 |
| +300 turns | 0.296 | 0.414 | 0.514 | 0.539 |
| +700 turns | 0.288 | **0.464** | 0.418 | **0.685** |

**Key Insights**:
1. Auto extraction with pgvector achieves the best memory-based F1 (75.6%
   of long-context baseline).
2. History injection improves semantic quality (LLM Score +0.10~0.18) but
   hurts token-level precision (F1 -0.02~0.07) due to adversarial
   robustness degradation.
3. Structured memory extraction outperforms brute-force history injection
   for factual recall tasks.
4. pgvector > MySQL for retrieval quality; gap vanishes with history
   injection.

## SQLite vs SQLiteVec (Subset)

This is a small subset run to compare local SQLite keyword matching (`sqlite`)
vs sqlite-vec semantic search (`sqlitevec`).

**Configuration**: Model=gpt-4o-mini, scenario=auto, sample=locomo10_1,
category=temporal (13 QA), LLM Judge disabled.

| Backend | F1 | Prompt Tokens | Avg Prompt/QA |
|---------|---:|--------------:|--------------:|
| sqlite | 0.116 | 80,184 | 6,168 |
| sqlitevec | 0.116 | 26,483 | 2,037 |

## Evaluation Metrics

Aligned with LoCoMo paper and industry standards (Mem0, MemMachine):

| Metric     | Description                      |
| ---------- | -------------------------------- |
| F1 Score   | Token-level F1 (LoCoMo standard) |
| BLEU Score | N-gram overlap                   |
| LLM-score  | LLM-as-Judge evaluation          |

## QA Categories

| Category    | Description                                        |
| ----------- | -------------------------------------------------- |
| single-hop  | Single-hop questions from one conversation segment |
| multi-hop   | Multi-hop questions requiring multiple segments    |
| temporal    | Temporal reasoning questions                       |
| open-domain | Open-domain questions requiring world knowledge    |
| adversarial | Adversarial questions testing robustness           |

## Evaluation Scenarios

### 1. Long-Context (Baseline)

Full conversation as context, evaluates model's native long-context ability.

```bash
go run main.go -scenario long_context
```

### 2. Agentic (Memory Tools)

Agent uses memory tools to add and search memories. The agent processes each
conversation session separately and decides what to store.

```bash
go run main.go -scenario agentic
```

### 3. Auto (Memory Extractor + Search)

Auto mode uses the built-in memory extractor to generate memories in the
background. The QA stage only performs memory search.

```bash
go run main.go -scenario auto
```

Memory backends apply to `agentic` and `auto` scenarios.
Auto mode uses the built-in extractor provided by the memory service.

### 4. All Scenarios

Run all scenarios for comparison.

```bash
go run main.go -scenario all

# Run all scenarios on both backends.
go run main.go -scenario all -memory-backend inmemory,pgvector
```

### 5. Comma-Separated Scenarios

Run specific combinations of scenarios.

```bash
# Run agentic and auto only.
go run main.go -scenario agentic,auto -memory-backend pgvector,mysql
```

## Command-Line Options

| Option              | Default                | Description                            |
| ------------------- | ---------------------- | -------------------------------------- |
| `-model`            | gpt-4o-mini            | Model name                             |
| `-eval-model`       | same as model          | Evaluation model for LLM judge         |
| `-dataset`          | ../data                | Dataset directory                      |
| `-data-file`        | locomo10.json          | Dataset file name                      |
| `-output`           | ../results             | Output directory                       |
| `-scenario`         | long_context           | Evaluation scenario (comma-separated)  |
| `-memory-backend`   | inmemory               | Memory backend (comma-separated)       |
| `-pgvector-dsn`     | (env)                  | PostgreSQL DSN for pgvector            |
| `-mysql-dsn`        | (env)                  | MySQL DSN for mysql backend            |
| `-embed-model`      | text-embedding-3-small | Embedding model for vector backends    |
| `-qa-history-turns` | 0                      | Inject N conversation turns as context |
| `-sample-id`        |                        | Filter by sample ID                    |
| `-max-tasks`        | 0                      | Maximum tasks (0=all)                  |
| `-llm-judge`        | false                  | Enable LLM-as-Judge                    |
| `-verbose`          | false                  | Verbose output                         |
| `-resume`           | false                  | Resume from checkpoint                 |

## Environment Variables

| Variable                    | Description                               |
| --------------------------- | ----------------------------------------- |
| `MODEL_NAME`                | Default model name                        |
| `EVAL_MODEL_NAME`           | Evaluation model name                     |
| `OPENAI_API_KEY`            | OpenAI API key                            |
| `PGVECTOR_DSN`              | PostgreSQL DSN for pgvector backend       |
| `MYSQL_DSN`                 | MySQL DSN for mysql backend               |
| `SQLITE_DSN`                | SQLite DSN for sqlite backend (optional)  |
| `SQLITEVEC_DSN`             | SQLite DSN for sqlitevec backend (optional) |
| `EMBED_MODEL_NAME`          | Embedding model for vector backends       |
| `OPENAI_EMBEDDING_API_KEY`  | API key for embedding model (optional)    |
| `OPENAI_EMBEDDING_BASE_URL` | Base URL for embedding API (optional)     |

## Dataset Setup

1. Download the LoCoMo dataset:

```bash
git clone https://github.com/snap-research/locomo.git
cp locomo/data/locomo10/*.json ../data/
```

2. Or use the sample data for testing:

```bash
# Sample data should be in ../data/locomo_sample.json.
```

## Running the Benchmark

```bash
cd benchmark/memory/trpc-agent-go-impl

# Install dependencies.
go mod tidy

# Run with default settings (long_context + inmemory).
go run main.go

# Run with LLM judge enabled.
go run main.go -llm-judge -model gpt-4o

# Run agentic evaluation with pgvector backend.
export PGVECTOR_DSN="postgres://user:password@localhost:5432/memory_eval\
?sslmode=disable"
go run main.go -scenario agentic -memory-backend pgvector

# Run auto evaluation with sqlite backend.
go run main.go -scenario auto -memory-backend sqlite

# Run auto evaluation with sqlitevec backend (requires embeddings).
go run main.go -scenario auto -memory-backend sqlitevec

# Run all scenarios.
go run main.go -scenario all -output ../results/full_eval

# Run with history injection (300 turns).
go run main.go \
  -scenario agentic,auto \
  -memory-backend pgvector,mysql \
  -qa-history-turns 300 \
  -llm-judge \
  -output ../results/history300
```

## Output Format

Results are saved in JSON format:

```json
{
  "metadata": {
    "framework": "trpc-agent-go",
    "model": "gpt-4o-mini",
    "scenario": "agentic",
    "memory_backend": "pgvector"
  },
  "summary": {
    "total_questions": 200,
    "overall_f1": 0.412,
    "overall_bleu": 0.156
  },
  "by_category": {
    "single-hop": { "count": 60, "f1": 0.523, "bleu": 0.182 },
    "multi-hop": { "count": 50, "f1": 0.384, "bleu": 0.145 }
  }
}
```

## Comparison with Baselines

| System             | F1   | LLM-score |
| ------------------ | ---- | --------- |
| GPT-4 (4K context) | 32.1 | -         |
| GPT-3.5-16K        | 37.8 | -         |
| Mem0               | -    | 0.80      |
| MemMachine         | 91.2 | 0.91      |

## Memory Backend Comparison

| Backend  | Pros                               | Cons                                         |
| -------- | ---------------------------------- | -------------------------------------------- |
| inmemory | Fast, no setup required            | No vector similarity, keyword-based matching |
| pgvector | Vector similarity search, scalable | Requires PostgreSQL setup                    |
| mysql    | Full-text search, widely deployed  | Requires MySQL setup                         |

### Expected Results

- **pgvector** should outperform **inmemory** for semantic retrieval tasks.
- For exact-match questions, both backends may perform similarly.
- pgvector is recommended for production and realistic evaluation.
- With history injection, backend differences diminish.
