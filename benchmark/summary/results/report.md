# Session Summarization Benchmark Report

**Evaluation Date**: January 23-26, 2026  
**Model**: deepseek-v3.2  
**Dataset**: MT-Bench-101 (subset)  
**Total Test Cases**: 627

## Executive Summary

This benchmark evaluates the effectiveness of session summarization in `trpc-agent-go` using multi-turn dialogue data from MT-Bench-101. The evaluation measures three key dimensions:

- **Response Consistency** (50% weight): Whether summarized context produces semantically equivalent responses.
- **Token Efficiency** (30% weight): Token savings achieved through summarization.
- **Information Retention** (20% weight): Preservation of key information from conversation history.

### Key Findings

| Metric | Value |
|--------|-------|
| Overall Consistency Score | 0.85 |
| Pass^1 Rate | 93.9% |
| Average Token Savings | -8.97% |
| Average Prompt Savings | -1.29% |
| Average Retention Rate | 83% |

**Interpretation**: The summarization system maintains **high response consistency** (85% semantic equivalence, 94% pass rate) while demonstrating **variable token efficiency** depending on task type. Information retention averages 83%, indicating most key details are preserved.

## Per-Task Results

| Task | Full Name | Cases | Token Savings | Prompt Savings | Consistency | Retention | Pass^1 |
|------|-----------|------:|-------------:|---------------:|------------:|----------:|-------:|
| CC | Content Confusion | 147 | -27.10% | -17.44% | 0.86 | 0.86 | 89.1% |
| CM | Context Memory | 80 | +13.68% | +25.50% | 0.82 | 0.82 | 96.2% |
| GR | General Reasoning | 3 | +13.95% | +15.20% | 1.00 | 1.00 | 100.0% |
| IC | Instruction Clarification | 150 | -0.63% | -0.44% | 0.85 | 0.82 | 95.3% |
| PI | Proactive Interaction | 87 | -28.32% | -1.43% | 0.81 | 0.70 | 96.6% |
| SC | Self-correction | 77 | -1.78% | -0.74% | 0.88 | 0.87 | 93.5% |
| TS | Topic Shift | 83 | -0.99% | -1.01% | 0.85 | 0.85 | 95.2% |
| **Total** | | **627** | **-8.97%** | **-1.29%** | **0.85** | **0.83** | **93.9%** |

## Analysis by Task Type

### High-Efficiency Tasks (Positive Token Savings)

1. **Context Memory (CM)**: +25.5% prompt savings
   - Summarization effectively compresses long conversation history.
   - High pass rate (96.2%) indicates reliable response quality.

2. **General Reasoning (GR)**: +15.2% prompt savings
   - Small sample size (3 cases), but shows promising results.
   - Perfect consistency and retention scores.

### Challenging Tasks (Negative Token Savings)

1. **Content Confusion (CC)**: -17.44% prompt savings
   - Tasks requiring disambiguation of similar queries benefit from full context.
   - Summarization may lose distinguishing details, leading to longer clarifications.

2. **Proactive Interaction (PI)**: -28.32% token overhead, but only -1.43% prompt overhead
   - The model generates more completion tokens with summarized context.
   - Lower retention rate (70%) suggests some conversational nuances are lost.

### Neutral Tasks (Near-Zero Savings)

- **IC, SC, TS**: Token savings within ±2%, indicating summarization neither helps nor hurts significantly.

## Metrics Explanation

### Response Consistency (Pass^k)

Measures semantic equivalence between baseline (full context) and summarized responses using LLM-based evaluation.

- **Score**: 0-1 scale of semantic similarity.
- **Pass^1**: Percentage of cases where at least 1 run passed the threshold (0.7).

### Token Efficiency

```
Token Savings % = (Baseline Tokens - Summary Tokens) / Baseline Tokens * 100
Prompt Savings % = (Baseline Prompt Tokens - Summary Prompt Tokens) / Baseline Prompt Tokens * 100
```

Negative values indicate the summarized version used MORE tokens.

### Information Retention

Evaluates whether key information from each conversation turn is preserved in the summary.

- Extracted key facts from baseline responses.
- Verified presence in summarized context responses.
- Calculated per-turn and overall retention rates.

## Recommendations

1. **Task-Aware Summarization**: Consider disabling summarization for CC (Content Confusion) tasks where disambiguation is critical.

2. **Threshold Tuning**: The current event threshold (2) may be too aggressive for some task types. Consider adaptive thresholds based on conversation characteristics.

3. **Retention Optimization**: The 70% retention rate for PI (Proactive Interaction) tasks suggests the summarizer may be dropping conversational cues. Consider preserving more context for interaction-heavy dialogues.

4. **Further Evaluation Needed**: GR (General Reasoning) shows excellent results but with only 3 cases. More comprehensive testing is recommended.

## Methodology

### Evaluation Process

1. **Baseline Run**: Execute conversation with full context (no summarization).
2. **Summary Run**: Execute same conversation with summarization enabled (event threshold = 2).
3. **Compare Results**: Measure token usage, response similarity, and information retention.

### Configuration

```
Model: deepseek-v3.2
Event Threshold: 2
Consistency Threshold: 0.70
Retention Threshold: 0.70
K Values: 1, 2, 4
Weights: Consistency 50%, Tokens 30%, Retention 20%
```

## References

- [MT-Bench-101 Paper (ACL 2024)](https://arxiv.org/abs/2402.14762)
- [τ-bench Paper](https://arxiv.org/abs/2406.12045)
- [τ²-bench Paper](https://arxiv.org/abs/2506.07982)
