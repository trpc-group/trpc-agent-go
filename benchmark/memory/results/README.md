# Results Directory

This directory contains evaluation results from memory benchmark runs.

## Directory Structure

```
results/
├── long_context/        # Long-context scenario results.
│   ├── results.json     # Full evaluation results.
│   └── checkpoint.json  # Checkpoint for resume.
├── agentic_<backend>/   # Agentic scenario results.
└── auto_<backend>/      # Auto scenario results.
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
    "total_questions": 200,
    "overall_f1": 0.412,
    "overall_bleu": 0.156,
    "overall_llm_score": 0.823
  },
  "by_category": {
    "single-hop": {"count": 60, "f1": 0.523},
    "multi-hop": {"count": 50, "f1": 0.384},
    "temporal": {"count": 40, "f1": 0.298},
    "open-domain": {"count": 30, "f1": 0.356},
    "adversarial": {"count": 20, "f1": 0.612}
  }
}
```
