# Evaluation Results

This directory stores session summarization benchmark evaluation results.

## Reports

| File | Description |
|------|-------------|
| [REPORT.md](REPORT.md) | Full evaluation report (English) |
| [REPORT.zh_CN.md](REPORT.zh_CN.md) | Full evaluation report (Chinese) |

## MT-Bench-101 Evaluation Summary

**Configuration**:
- Model: deepseek-v3.2
- Summary Trigger: Every 2 turns (`-events 2`)
- Total Cases: 917 (9 tasks)

**Key Results**:

| Metric | Value |
|--------|------:|
| Overall Prompt Savings | 24.47% |
| Overall Token Savings | 12.89% |
| Weighted Consistency | 0.853 |
| Pass^1 Rate | 92.3% |
| Negative Token Cases | 35.9% |

**Task Suitability**:

| Suitability | Tasks | Avg Turns | Prompt Savings |
|-------------|-------|----------:|---------------:|
| ✅ Highly Recommended | SI, PI, CM | 4.0+ | 28%~40% |
| ⚠️ Conditional | CC, IC, GR | 2.4~3.1 | 4%~10% |
| ❌ Not Recommended | SA, SC, TS | 2.0~3.0 | -0.5%~1% |

**Key Insights**:
1. Summarization works well for long dialogues (≥4 turns) with long prompts (>2000 tokens).
2. Summarization harms short dialogues (≤2 turns) due to overhead > compression gains.
3. Current `-events 2` setting is too aggressive for short dialogues.

## Regenerate Statistics

```bash
python3 analyze_mtbench101.py > mt-bench-101/_summary.json
```
