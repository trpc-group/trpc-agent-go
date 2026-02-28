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

## SQLite vs SQLiteVec (Subset Runs)

We also run focused subset experiments comparing local SQLite keyword
matching (`sqlite`) vs sqlite-vec semantic search (`sqlitevec`).

**End-to-end QA subset (Auto / locomo10_1 / 199 QA, LLM Judge enabled)**:

| Backend | F1 | LLM Score | Prompt Tokens | Avg Prompt/QA |
|---------|---:|----------:|--------------:|--------------:|
| sqlite | 0.327 | 0.370 | 1,287,813 | 6,471 |
| sqlitevec | 0.307 | 0.325 | 407,969 | 2,050 |

**End-to-end QA subset (Auto / locomo10_6 / 158 QA, LLM Judge enabled)**:

| Backend | F1 | LLM Score | Prompt Tokens | Avg Prompt/QA |
|---------|---:|----------:|--------------:|--------------:|
| sqlite | 0.269 | 0.289 | 1,296,580 | 8,206 |
| sqlitevec | 0.274 | 0.295 | 362,903 | 2,297 |

Note: token usage above counts QA agent model calls only; it excludes
embedding requests and LLM-as-Judge calls. See `REPORT.md` for full
configuration and breakdown.

**Top-k sweep (Auto / locomo10_1 / LLM Judge disabled)**:

To understand how `sqlitevec` quality changes with retrieval size, we run a
small sweep on `locomo10_1` (199 QA). In this run, `sqlitevec` achieves the
best quality at the default top-k=10; increasing top-k increases tokens but
does not improve F1.

| Backend | vector-topk | qa-search-passes | F1 | Prompt Tokens | Avg Prompt/QA |
|---------|------------:|-----------------:|---:|--------------:|--------------:|
| sqlite | - | 1 | 0.299 | 1,322,360 | 6,645 |
| sqlitevec | 5 | 1 | 0.320 | 346,253 | 1,740 |
| sqlitevec | 10 | 1 | 0.343 | 398,751 | 2,004 |
| sqlitevec | 20 | 1 | 0.329 | 621,790 | 3,125 |
| sqlitevec | 40 | 1 | 0.327 | 965,423 | 4,851 |
| sqlitevec | 10 | 2 | 0.342 | 659,981 | 3,316 |

## Directory Structure

Note: `data_*` and `log_*.log` are large, machine-generated artifacts and are
ignored by git (see `.gitignore`).

```
results/
+-- .gitignore                           # Ignore data/log/pdf/tmp artifacts.
+-- README.md                            # This file.
+-- REPORT.md                            # English evaluation report.
+-- REPORT.zh_CN.md                      # Chinese evaluation report.
+-- tools/
|   +-- extract_paper_locomo_tables.py   # Extract external baselines.
+-- tmp/                                 # Paper text dumps (ignored).
+-- data_*/                              # Evaluation outputs (ignored).
+-- log_*.log                            # Run logs (ignored).
+-- *.pdf                                # Papers (ignored).
```

## External Baselines (From Papers)

To extract LoCoMo baseline tables reported by external papers and generate
Markdown snippets for `REPORT.md` and `REPORT.zh_CN.md`:

- Prepare paper text dumps under `tmp/`:
  - `tmp/2402.17753v1.txt` (LoCoMo paper).
  - `tmp/2504.19413.txt` (Mem0 paper).
- Run:
  - `python3 tools/extract_paper_locomo_tables.py --format md`

The script parses the tables and converts percentage-point metrics to the
0-1 range for consistent reporting.

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
