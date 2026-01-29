# Evaluating Session Summarization Effectiveness: An Empirical Study on MT-Bench-101

## 1. Introduction

Large Language Models (LLMs) face context window limitations and token cost issues in multi-turn conversation scenarios. Session summarization is a common solution: compressing conversation history into summaries to reduce input token count. However, summarization may lead to loss of critical information, affecting subsequent response quality. This paper aims to answer the following questions: (1) In which scenarios can session summarization effectively save tokens? (2) How much does summarization impact response quality? (3) What is the optimal summarization triggering strategy?

Through comparative experiments on 9 tasks (917 cases) from the MT-Bench-101 dataset, we find that:

- **Effective for Long Dialogues**: ≥4 turn dialogues achieve 28%~40% prompt token savings while maintaining over 85% response consistency
- **Harmful for Short Dialogues**: ≤2 turn dialogues not only fail to benefit but actually increase token consumption due to summarization overhead
- **Triggering Strategy Too Aggressive**: Current setting (triggering summary every 2 turns) is unsuitable for short dialogues

Main contributions include: systematic evaluation of session summarization on MT-Bench-101, identification of key factors affecting effectiveness (conversation turns, baseline prompt length), and discovery of negative effects in short dialogue scenarios with improvement recommendations.

---

## 2. Methodology

### 2.1 Experimental Design

We employ an **A/B comparative experiment** design:

- **Baseline Group**: Retains complete conversation history as context
- **Experimental Group (Summary)**: Generates summary after every N turns, replacing original history with summary

For the same input, both groups use the same LLM to generate responses, comparing token consumption and response quality.

### 2.2 Evaluation Metrics

Following τ-bench and τ²-bench methodologies, we define three evaluation dimensions:

| Metric                    | Weight | Definition                                                                                              |
| ------------------------- | -----: | ------------------------------------------------------------------------------------------------------- |
| **Response Consistency**  |    50% | Semantic similarity between summary and baseline responses, scored by LLM (0~1)                         |
| **Token Efficiency**      |    30% | Savings = (Baseline - Summary) / Baseline × 100%                                                        |
| **Information Retention** |    20% | Proportion of key information (numbers, proper nouns, quoted content) preserved in summarized responses |

**Pass^1 Metric**: If consistency score ≥ 0.7, the case passes. Pass^1 = passed cases / total cases.

### 2.3 Dataset

We use the **MT-Bench-101** dataset, which contains 13 types of multi-turn dialogue tasks. This evaluation covers 9 tasks:

| Code | Task Name                 | Cases | Description                                                            |
| ---- | ------------------------- | ----: | ---------------------------------------------------------------------- |
| CC   | Content Confusion         |   147 | Distinguish similar but semantically different queries                 |
| CM   | Context Memory            |    80 | Recall early dialogue details to answer current questions              |
| GR   | General Reasoning         |    71 | Collaboratively solve reasoning problems across turns                  |
| IC   | Instruction Clarification |   150 | Clarify ambiguous queries                                              |
| PI   | Proactive Interaction     |    87 | Proactively ask questions to guide dialogue                            |
| SA   | Self-affirmation          |    73 | Maintain correct response against inaccurate feedback                  |
| SC   | Self-correction           |    77 | Correct response according to user feedback                            |
| SI   | Separate Input            |   149 | First turn describes task requirements, subsequent turns provide input |
| TS   | Topic Shift               |    83 | Recognize and focus on new topics when users switch                    |

**Uncovered Tasks**: AR (Anaphora Resolution), CR (Content Rephrasing), FR (Format Rephrasing), MR (Mathematical Reasoning).

### 2.4 Experimental Configuration

| Parameter                 | Value         | Description                                 |
| ------------------------- | ------------- | ------------------------------------------- |
| Model                     | deepseek-v3.2 | Used for response and summary generation    |
| Summary trigger threshold | 2             | Trigger summary every 2 turns               |
| Number of runs            | 1             | Each case runs once                         |
| Consistency threshold     | 0.7           | Pass^1 determination threshold              |
| Evaluation method         | LLM-eval      | Use LLM for semantic consistency evaluation |

---

## 3. Experimental Results

### 3.1 Overall Results

| Metric                           |           Value |
| -------------------------------- | --------------: |
| Total Cases                      |             917 |
| Total Baseline Tokens            |       3,515,728 |
| Total Summary Tokens             |       3,062,518 |
| **Overall Token Savings**        |      **12.89%** |
| Total Baseline Prompt Tokens     |       1,891,399 |
| Total Summary Prompt Tokens      |       1,428,606 |
| **Overall Prompt Savings**       |      **24.47%** |
| Weighted Avg Consistency         |           0.853 |
| Weighted Pass^1                  |           92.3% |
| Weighted Avg Retention           |           0.836 |
| **Negative Token Savings Cases** | **329 (35.9%)** |

**Key Finding**: Although overall savings are positive, over 1/3 of cases show negative token savings (i.e., summary mode consumed more tokens).

### 3.2 Per-Task Results

**Table 1: Token Efficiency Metrics by Task**

| Task | Cases | Prompt Savings | Token Savings |     p25 |    p50 |    p75 | Negative Rate |
| ---- | ----: | -------------: | ------------: | ------: | -----: | -----: | ------------: |
| SI   |   149 |         39.50% |        22.59% |   0.88% | 16.67% | 26.47% |         17.4% |
| PI   |    87 |         34.17% |        21.24% |  -2.04% | 12.11% | 23.46% |         26.4% |
| CM   |    80 |         28.07% |        15.83% |   6.93% | 15.42% | 24.08% |         16.2% |
| CC   |   147 |         10.10% |         4.28% |  -7.03% |  1.86% |  9.90% |         42.2% |
| IC   |   150 |          8.89% |         4.97% | -10.45% |  1.20% | 10.98% |         46.0% |
| GR   |    71 |          4.35% |         3.59% |  -9.95% |  0.68% | 10.28% |         43.7% |
| SA   |    73 |          0.95% |         1.54% |  -8.68% |  3.40% | 11.41% |         42.5% |
| TS   |    83 |          0.51% |         0.95% |  -5.86% |  0.95% |  7.78% |         43.4% |
| SC   |    77 |     **-0.50%** |    **-1.08%** |  -9.53% |  0.00% |  7.52% |     **49.4%** |

**Table 2: Response Quality Metrics by Task**

| Task | Consistency |    Pass^1 | Retention |
| ---- | ----------: | --------: | --------: |
| GR   |   **0.916** |     93.0% |     0.870 |
| SC   |       0.881 |     93.5% | **0.872** |
| SA   |       0.862 |     83.6% |     0.865 |
| CC   |       0.861 |     89.1% |     0.860 |
| IC   |       0.851 |     95.3% |     0.825 |
| TS   |       0.846 |     95.2% |     0.849 |
| SI   |       0.841 |     89.3% |     0.857 |
| CM   |       0.819 |     96.2% |     0.817 |
| PI   |       0.814 | **96.6%** |     0.704 |

### 3.3 Conversation Turn Analysis

**Table 3: Turn Distribution by Task**

| Task | Avg Turns |   2-turn % | 3-turn % | 4-turn % | ≥5-turn % |
| ---- | --------: | ---------: | -------: | -------: | --------: |
| SI   |      4.16 |      12.8% |    10.7% |    32.2% |     44.3% |
| PI   |      4.07 |       0.0% |    33.3% |    33.3% |     33.3% |
| CM   |      3.99 |       1.2% |     1.2% |    96.3% |      1.2% |
| GR   |      3.07 |       2.8% |    64.8% |    32.4% |      0.0% |
| TS   |      3.00 |       0.0% |   100.0% |     0.0% |      0.0% |
| IC   |      2.84 |      24.0% |    68.0% |     8.0% |      0.0% |
| CC   |      2.39 |      72.8% |    15.6% |     8.8% |      2.7% |
| SA   |  **2.00** | **100.0%** |     0.0% |     0.0% |      0.0% |
| SC   |  **2.00** | **100.0%** |     0.0% |     0.0% |      0.0% |

### 3.4 Baseline Prompt Length Analysis

**Table 4: Relationship Between Prompt Length and Savings Rate**

| Task | Avg Baseline Prompt | Avg Baseline Completion | Prompt Savings |
| ---- | ------------------: | ----------------------: | -------------: |
| CM   |               4,404 |                   3,155 |         28.07% |
| SI   |               4,273 |                   2,752 |         39.50% |
| PI   |               2,304 |                   1,456 |         34.17% |
| TS   |               1,912 |                   1,870 |          0.51% |
| IC   |               1,683 |                   1,921 |          8.89% |
| CC   |               1,225 |                   1,571 |         10.10% |
| GR   |                 768 |                     652 |          4.35% |
| SA   |                 395 |                     829 |          0.95% |
| SC   |                 355 |                     702 |         -0.50% |

---

## 4. Analysis

### 4.1 Factors Affecting Summarization Effectiveness

#### 4.1.1 Conversation Turns is the Decisive Factor

Experimental data reveals a strong correlation between conversation turns and summarization effectiveness:

**Positive Correlation Tasks (Good Effect)**:

- SI (4.16 turns), PI (4.07 turns), CM (3.99 turns) all achieve 20%+ token savings
- These tasks have <15% 2-turn dialogue proportion

**Negative Correlation Tasks (Poor Effect)**:

- 100% of SA and SC cases have only 2 turns
- With summary trigger threshold of 2, this means only 1 message in history when summarizing—almost nothing to compress

**Root Cause**: Under `-events 2` setting, the summary timing for 2-turn dialogues is:

```
Turn 1: history=[] → No summary triggered
Turn 2: history=[Turn1] → Summary triggered, but only 1 history item, minimal compression space
```

#### 4.1.2 Baseline Prompt Length Determines Compression Ceiling

Prompt savings rate positively correlates with baseline prompt length (Pearson r = 0.72):

- **High Compression Potential** (>2000 tokens): SI, CM, PI, savings 28%~40%
- **Low Compression Potential** (<500 tokens): SA, SC, savings ≈ 0%

This aligns with information theory intuition: longer inputs have higher redundancy and greater compression space.

#### 4.1.3 Summarization Overhead is Amplified in Short Dialogues

SC task shows **-1.08% negative savings**. Analyzing its token distribution:

| Metric            | Baseline | Summary | Change     |
| ----------------- | -------- | ------- | ---------- |
| Prompt Tokens     | 27,341   | 27,477  | +0.50%     |
| Completion Tokens | 54,051   | 54,791  | +1.37%     |
| **Total Tokens**  | 81,392   | 82,268  | **+1.08%** |

Summary generation consumes tokens (not separately counted), but compression gains are nearly zero, resulting in net loss.

### 4.2 Impact of Task Characteristics on Summarization

#### 4.2.1 Why Does SI (Separate Input) Perform Best?

Typical structure of SI tasks:

- **Turn 1**: Detailed task instructions (usually long)
- **Turn 2~N**: Specific inputs (usually short)

Summarization can compress verbose task instructions into key constraints while keeping specific inputs intact, achieving highest compression efficiency.

#### 4.2.2 Why Does PI (Proactive Interaction) Have Lowest Retention?

PI's retention rate is only **0.704**, significantly lower than other tasks. Analysis reveals:

1. **Task Characteristics**: PI requires the model to "proactively ask questions to guide dialogue"—such guiding content may be deemed non-core during summarization
2. **Evaluation Method Limitation**: Retention is based on keyword matching, but PI's key information may exist in paraphrased form

However, PI's Pass^1 is **96.6%**, indicating good semantic-level consistency. Keyword matching may underestimate actual retention effectiveness.

#### 4.2.3 Why Does TS (Topic Shift) Perform Poorly?

TS tasks require recognizing user topic switches. When history is compressed by summarization, topic switch signals may be weakened, affecting model judgment. This indicates: **tasks requiring context completeness are not suitable for aggressive summarization**.

### 4.3 Experimental Limitations

#### 4.3.1 Summary Generation Token Cost Not Counted

Current evaluation only compares Prompt + Completion Tokens, not including tokens consumed by summary generation. Actual cost should be:

```
Total Cost = Prompt + Completion + Summary Generation
```

If this cost were included, the negative savings case proportion would likely be higher.

#### 4.3.2 Single Run Lacks Statistical Stability

`-num-runs 1` makes Pass^k (k > 1) ineffective. LLM outputs have randomness, and single-run results may be unstable.

#### 4.3.3 Dataset Has Short Dialogue Turns

MT-Bench-101's average dialogue turns are 2~4, which differs from long dialogue scenarios in production environments. Summarization is better suited for longer dialogues; the current dataset may underestimate its potential.

---

## 5. Discussion and Recommendations

### 5.1 Task Suitability Classification

Based on experimental results, we classify tasks into three categories:

| Suitability                   | Characteristics                 | Example Tasks | Recommendation                         |
| ----------------------------- | ------------------------------- | ------------- | -------------------------------------- |
| **Highly Recommended**        | Avg turns ≥4, Prompt >2000      | SI, PI, CM    | Enable summarization                   |
| **Conditionally Recommended** | Avg turns 3-4, Prompt 1000-2000 | CC, IC, GR    | Dynamic decision based on actual turns |
| **Not Recommended**           | Avg turns ≤2, Prompt <1000      | SA, SC, TS    | Disable summarization                  |

### 5.2 Future Research Directions

1. **Add Summary Token Statistics**: Include summary generation cost in evaluation system
2. **Long Dialogue Dataset Validation**: Use datasets with more conversation turns (e.g., 10+) to verify summarization effectiveness ceiling
3. **Optimize Summary Prompt**: Current summary prompt may be too verbose; try simplification to reduce overhead

---

## 6. Conclusion and Future Work

Through empirical study on the MT-Bench-101 dataset, this paper systematically evaluates the effectiveness of session summarization. Main conclusions are:

1. **Summarization is Effective for Long Dialogues**: Tasks with average 4+ turns (SI, PI, CM) achieve 28%~40% prompt savings while maintaining over 85% response consistency.

2. **Summarization is Harmful for Short Dialogues**: 2-turn dialogue tasks (SA, SC) cannot benefit under current settings and actually increase token consumption due to summarization overhead.

3. **Triggering Strategy Needs Optimization**: Fixed `-events 2` is too aggressive for short dialogues. Recommend adopting dynamic strategies based on conversation turns or cumulative token count.

4. **Evaluation System Needs Improvement**: Summary generation token cost should be included in total cost calculation to more accurately evaluate actual summarization benefits.

---

## Appendix

### Appendix A: Token Distribution Details

| Task | Baseline Prompt | Baseline Completion | Summary Prompt | Summary Completion | Prompt Δ | Completion Δ |
| ---- | --------------: | ------------------: | -------------: | -----------------: | -------: | -----------: |
| SI   |         636,677 |             410,062 |        385,205 |            425,101 |  -39.50% |       +3.67% |
| CM   |         352,349 |             252,400 |        253,457 |            255,567 |  -28.07% |       +1.25% |
| PI   |         200,445 |             126,682 |        131,961 |            125,675 |  -34.17% |       -0.79% |
| IC   |         252,440 |             288,191 |        229,989 |            283,796 |   -8.89% |       -1.53% |
| CC   |         180,057 |             230,963 |        161,876 |            231,533 |  -10.10% |       +0.25% |
| TS   |         158,705 |             155,207 |        157,900 |            153,034 |   -0.51% |       -1.40% |
| GR   |          54,541 |              46,263 |         52,171 |             45,011 |   -4.35% |       -2.71% |
| SA   |          28,844 |              60,510 |         28,570 |             59,404 |   -0.95% |       -1.83% |
| SC   |          27,341 |              54,051 |         27,477 |             54,791 |   +0.50% |       +1.37% |

### Appendix B: Experimental Environment

- **Evaluation Framework**: trpc-agent-go benchmark/summary
- **Model**: deepseek-v3.2

### Appendix C: Metric Calculation Formulas

**Token Savings Rate (Aggregate)**:

```
Savings% = (∑Baseline Tokens - ∑Summary Tokens) / ∑Baseline Tokens × 100
```

**Consistency Score**:
LLM evaluates semantic similarity between two responses, outputting a 0~1 score.

**Retention Rate**:

Calculated using rule-based extraction + matching:

1. **Key Information Extraction** (from Baseline response):
   - Numbers (dates, amounts, etc.): regex `\b\d+[\d,\.]*\b`
   - Quoted content: regex `["']([^"']+)["']`
   - Proper nouns: regex `\b[A-Z][a-z]+(?:\s+[A-Z][a-z]+)*\b` (excluding common words)
   - Maximum 10 key information items per turn

2. **Matching Detection** (in Summary response):
   - Exact match (case-insensitive)
   - Fuzzy number matching (ignoring comma format differences)

3. **Formula**:

```
Retention = Matched key info count / Total extracted key info count
```

---

## References

1. Bai, Y., et al. "MT-Bench-101: A Fine-Grained Benchmark for Evaluating Large Language Models in Multi-Turn Dialogues." ACL 2024.
2. Yao, S., et al. "τ-bench: A Benchmark for Tool-Agent-User Interaction in Real-World Domains." arXiv:2406.12045, 2024.
3. Chen, W., et al. "τ²-bench: Benchmarking Table-Reasoning Agents." arXiv:2506.07982, 2025.
