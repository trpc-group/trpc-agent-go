# Evaluation Results

This directory stores memory benchmark evaluation results.

## Reports

| File | Description |
|------|-------------|
| [REPORT.md](REPORT.md) | Full evaluation report (English) |
| [REPORT.zh_CN.md](REPORT.zh_CN.md) | Full evaluation report (Chinese) |

## LoCoMo Benchmark Evaluation Summary

**Configuration**:
- Model: gpt-4o-mini
- Samples: 10 (full LoCoMo-10)
- Total Questions: 1,986

**Key Results (No History Injection)**:

| Scenario | Backend | F1 | LLM Score |
|----------|---------|----:|----------:|
| Long-Context | - | **0.472** | **0.523** |
| Auto | pgvector | 0.357 | 0.366 |
| Auto | MySQL | 0.347 | 0.362 |
| Agentic | pgvector | 0.294 | 0.287 |
| Agentic | MySQL | 0.286 | 0.285 |

**History Injection Impact (Auto pgvector)**:

| Variant | F1 | LLM Score | Adversarial F1 |
|---------|----:|----------:|---------------:|
| No history | **0.357** | 0.366 | **0.771** |
| +300 turns | 0.296 | 0.414 | 0.514 |
| +700 turns | 0.288 | **0.464** | 0.418 |

**Key Insights**:
1. Memory extraction (Auto) achieves 75.6% of the long-context gold
   standard.
2. History injection trades F1 precision for semantic quality (LLM Score).
3. Adversarial robustness degrades with history injection (model attempts
   to answer unanswerable questions).
4. Open-domain LLM Score improves dramatically with history (+92.9%).

## Directory Structure

```
results/
+-- REPORT.md                            # English evaluation report.
+-- REPORT.zh_CN.md                      # Chinese evaluation report.
+-- data_nohistory_gpt4omini/            # No-history baseline.
|   +-- long_context/
|   +-- auto_pgvector/
|   +-- auto_mysql/
|   +-- agentic_pgvector/
|   +-- agentic_mysql/
|   +-- rag_observation_pgvector/
|   +-- rag_observation_mysql/
+-- data_history300_gpt4omini/           # +300 turns history injection.
|   +-- auto_pgvector/
|   +-- auto_mysql/
|   +-- agentic_pgvector/
|   +-- agentic_mysql/
+-- data_history700_gpt4omini/           # +700 turns history injection.
|   +-- auto_pgvector/
|   +-- auto_mysql/
|   +-- agentic_pgvector/
|   +-- agentic_mysql/
+-- log_nohistory_gpt4omini.log          # No-history run log.
+-- log_history300_gpt4omini.log         # +300 turns run log.
+-- log_history700_gpt4omini.log         # +700 turns run log.
```

## Result Format

Each `results.json` contains:

```json
{
  "metadata": {
    "framework": "trpc-agent-go",
    "model": "gpt-4o-mini",
    "scenario": "agentic",
    "memory_backend": "pgvector"
  },
  "summary": {
    "total_samples": 10,
    "total_questions": 1986,
    "overall_f1": 0.294,
    "overall_bleu": 0.279,
    "overall_llm_score": 0.287
  },
  "by_category": {
    "single-hop": {"count": 282, "f1": 0.146},
    "multi-hop": {"count": 321, "f1": 0.178},
    "temporal": {"count": 96, "f1": 0.091},
    "open-domain": {"count": 841, "f1": 0.126},
    "adversarial": {"count": 446, "f1": 0.830}
  }
}
```
