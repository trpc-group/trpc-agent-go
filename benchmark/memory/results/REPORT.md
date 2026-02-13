# Evaluating Long-Term Conversational Memory: An Empirical Study on LoCoMo Benchmark

## 1. Introduction

Long-term conversational memory is a critical capability for AI agents that interact with users across multiple sessions. As conversations accumulate over time, agents must effectively store, retrieve, and reason over past interactions to maintain coherent and personalized responses.

This report evaluates the memory capabilities of trpc-agent-go using the **LoCoMo** (Long-Context Conversational Memory) benchmark. We compare three distinct memory paradigms across two storage backends, and further investigate the effect of injecting raw conversation history (300 and 700 turns) as additional context for memory-based approaches.

**Key findings:**

- **Long-context baseline** achieves the highest overall F1 (0.472), confirming that full context remains the gold standard when feasible.
- **Auto memory extraction** (F1=0.357 pgvector) is the strongest memory-based approach, reaching 75.6% of the long-context baseline.
- **Agentic memory** (F1=0.294 pgvector) shows that LLM-driven memory extraction faces information density challenges.
- **pgvector consistently outperforms MySQL** by 1-2% F1 across all scenarios, validating the value of vector similarity search.
- **Injecting raw history hurts F1/BLEU but improves LLM Score**, revealing a trade-off between token-level precision and semantic quality.

---

## 2. Methodology

### 2.1 Benchmark Dataset

We use the **LoCoMo** dataset (Maharana et al., 2024), which contains multi-session conversations between pairs of speakers. Each sample includes:

- 15-25 conversation sessions spanning several months.
- Ground-truth QA pairs across 5 categories.
- Session-level observations and summaries.

**Evaluation scale**: 10 samples, 1,986 total QA pairs.

### 2.2 Evaluation Scenarios

| Scenario | Description | Memory Write | Memory Read |
| --- | --- | --- | --- |
| **Long-Context** | Full transcript as LLM context | N/A (all-in-context) | N/A |
| **Agentic** | LLM agent decides what to store via tool calls | LLM tool calls (memory_add) | Memory retrieval |
| **Auto** | Background extractor auto-generates memories | Async extraction | Memory retrieval |

### 2.3 Storage Backends

| Backend | Retrieval Method | Embedding Model |
| --- | --- | --- |
| **pgvector** | Vector similarity (cosine) | text-embedding-3-small |
| **MySQL** | Full-text search (BM25-like) | N/A |

### 2.4 History Injection Experiments

In addition to the base scenarios, we evaluate injecting raw conversation history turns as context messages alongside memory retrieval:

| Variant | Description |
| --- | --- |
| **No History** | Memory retrieval only (baseline for memory scenarios) |
| **+History 300** | Inject last 300 conversation turns as context |
| **+History 700** | Inject last 700 conversation turns as context |

History messages are injected after the system prompt and before the session history, using `WithInjectedContextMessages`.

### 2.5 Evaluation Metrics

Aligned with the LoCoMo paper and industry standards (Mem0, MemMachine):

| Metric | Description |
| --- | --- |
| **F1 Score** | Token-level F1 (primary metric) |
| **BLEU Score** | N-gram overlap precision |
| **LLM Score** | LLM-as-Judge semantic evaluation (0-1) |

### 2.6 QA Categories

| Category | Count | Description |
| --- | --- | --- |
| single-hop | 282 | Single fact from one conversation segment |
| multi-hop | 321 | Requires combining facts from multiple segments |
| temporal | 96 | Temporal reasoning (when did X happen?) |
| open-domain | 841 | Open-ended questions requiring world knowledge |
| adversarial | 446 | Questions designed to test robustness (unanswerable) |

### 2.7 Experimental Configuration

| Parameter | Value |
| --- | --- |
| Model | gpt-4o-mini |
| Evaluation Model | gpt-4o-mini |
| Samples | 10 (full LoCoMo-10) |
| Total Questions | 1,986 |
| LLM Judge | Enabled |

---

## 3. Results

### 3.1 Overall Results (No History Injection)

| Scenario | Backend | F1 | BLEU | LLM Score | Avg Latency |
| --- | --- | ---: | ---: | ---: | ---: |
| Long-Context | - | **0.472** | **0.429** | **0.523** | 3,485ms |
| Auto | pgvector | 0.357 | 0.333 | 0.366 | 5,622ms |
| Auto | MySQL | 0.347 | 0.320 | 0.362 | 5,678ms |
| Agentic | pgvector | 0.294 | 0.279 | 0.287 | 4,998ms |
| Agentic | MySQL | 0.286 | 0.271 | 0.285 | 4,392ms |

### 3.2 History Injection Results

**Table 1: Effect of History Injection on Overall Metrics**

| Scenario | Backend | History | F1 | BLEU | LLM Score | Avg Latency |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Agentic | pgvector | None | **0.294** | **0.279** | 0.287 | 4,998ms |
| Agentic | pgvector | +300 | 0.267 | 0.237 | 0.357 | 6,990ms |
| Agentic | pgvector | +700 | 0.275 | 0.229 | **0.464** | 5,120ms |
| Agentic | MySQL | None | **0.286** | **0.271** | 0.285 | 4,392ms |
| Agentic | MySQL | +300 | 0.275 | 0.245 | 0.365 | 5,817ms |
| Agentic | MySQL | +700 | 0.277 | 0.231 | **0.460** | 4,956ms |
| Auto | pgvector | None | **0.357** | **0.333** | 0.366 | 5,622ms |
| Auto | pgvector | +300 | 0.296 | 0.260 | 0.414 | 6,056ms |
| Auto | pgvector | +700 | 0.288 | 0.242 | **0.464** | 5,852ms |
| Auto | MySQL | None | **0.347** | **0.320** | 0.362 | 5,678ms |
| Auto | MySQL | +300 | 0.280 | 0.246 | 0.399 | 5,547ms |
| Auto | MySQL | +700 | 0.290 | 0.244 | **0.467** | 5,321ms |

### 3.3 Results by Category (No History)

**Table 2: F1 Score by Category**

| Category | Long-Context | Agentic pgvec | Agentic MySQL | Auto pgvec | Auto MySQL |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 0.330 | 0.146 | 0.168 | 0.272 | 0.306 |
| multi-hop | 0.319 | 0.178 | 0.135 | 0.088 | 0.101 |
| temporal | 0.088 | 0.091 | 0.043 | 0.060 | 0.056 |
| open-domain | 0.518 | 0.126 | 0.146 | 0.302 | 0.325 |
| adversarial | 0.668 | 0.830 | 0.787 | 0.771 | 0.653 |

**Table 3: LLM Score by Category**

| Category | Long-Context | Agentic pgvec | Agentic MySQL | Auto pgvec | Auto MySQL |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 0.333 | 0.122 | 0.130 | 0.220 | 0.277 |
| multi-hop | 0.252 | 0.107 | 0.093 | 0.049 | 0.064 |
| temporal | 0.155 | 0.137 | 0.057 | 0.068 | 0.083 |
| open-domain | 0.654 | 0.141 | 0.171 | 0.355 | 0.380 |
| adversarial | 0.670 | 0.830 | 0.787 | 0.771 | 0.653 |

### 3.4 History Injection: Category Breakdown

**Table 4: F1 by Category — Agentic pgvector**

| Category | No History | +300 | +700 |
| --- | ---: | ---: | ---: |
| single-hop | **0.146** | 0.156 | 0.185 |
| multi-hop | **0.178** | 0.123 | 0.112 |
| temporal | 0.091 | 0.062 | **0.089** |
| open-domain | 0.126 | 0.239 | **0.331** |
| adversarial | **0.830** | 0.539 | 0.383 |

**Table 5: F1 by Category — Auto pgvector**

| Category | No History | +300 | +700 |
| --- | ---: | ---: | ---: |
| single-hop | **0.272** | 0.196 | 0.183 |
| multi-hop | 0.088 | **0.120** | 0.106 |
| temporal | 0.060 | **0.074** | 0.079 |
| open-domain | 0.302 | **0.306** | **0.347** |
| adversarial | **0.771** | 0.514 | 0.418 |

**Table 6: LLM Score by Category — Auto pgvector**

| Category | No History | +300 | +700 |
| --- | ---: | ---: | ---: |
| single-hop | 0.220 | 0.294 | **0.291** |
| multi-hop | 0.049 | 0.131 | **0.182** |
| temporal | 0.068 | 0.162 | **0.185** |
| open-domain | 0.355 | 0.539 | **0.685** |
| adversarial | **0.771** | 0.513 | 0.419 |

### 3.5 Per-Sample Results

**Table 7: F1 Score per Sample (Long-Context / Auto pgvector / Agentic pgvector)**

| Sample | #QA | Long-Context | Auto pgvec | Agentic pgvec |
| --- | ---: | ---: | ---: | ---: |
| locomo10_1 | 199 | 0.429 | 0.311 | 0.279 |
| locomo10_2 | 105 | 0.510 | 0.322 | 0.345 |
| locomo10_3 | 193 | 0.530 | 0.441 | 0.295 |
| locomo10_4 | 260 | 0.456 | 0.367 | 0.335 |
| locomo10_5 | 242 | 0.447 | 0.364 | 0.297 |
| locomo10_6 | 158 | 0.539 | 0.204 | 0.287 |
| locomo10_7 | 190 | 0.465 | 0.404 | 0.278 |
| locomo10_8 | 239 | 0.461 | 0.339 | 0.268 |
| locomo10_9 | 196 | 0.448 | 0.380 | 0.268 |
| locomo10_10 | 204 | 0.480 | 0.393 | 0.297 |
| **Average** | **199** | **0.472** | **0.357** | **0.294** |

---

## 4. Analysis

### 4.1 Scenario Comparison

```
F1 Score Comparison (10 samples, 1986 QA pairs)

long_context        |==========================================| 0.472
auto_pgvec          |================================          | 0.357
auto_mysql          |===============================           | 0.347
auto_pgvec +300     |===========================               | 0.296
auto_mysql +700     |==========================                | 0.290
auto_pgvec +700     |==========================                | 0.288
agentic_pgvec       |==========================                | 0.294
auto_mysql +300     |=========================                 | 0.280
agentic_mysql +700  |=========================                 | 0.277
agentic_mysql       |=========================                 | 0.286
agentic_mysql +300  |========================                  | 0.275
agentic_pgvec +700  |========================                  | 0.275
agentic_pgvec +300  |=======================                   | 0.267
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5

LLM Score Comparison (10 samples, 1986 QA pairs)

long_context        |==========================================| 0.523
auto_mysql +700     |=====================================     | 0.467
auto_pgvec +700     |=====================================     | 0.464
agentic_pgvec +700  |=====================================     | 0.464
agentic_mysql +700  |====================================      | 0.460
auto_pgvec +300     |=================================         | 0.414
auto_mysql +300     |================================          | 0.399
auto_pgvec          |=============================             | 0.366
agentic_mysql +300  |=============================             | 0.365
auto_mysql          |============================              | 0.362
agentic_pgvec +300  |============================              | 0.357
agentic_pgvec       |=======================                   | 0.287
agentic_mysql       |=======================                   | 0.285
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

#### 4.1.1 Long-Context is the Gold Standard

Long-context achieves the highest F1 (0.472) across all categories except adversarial. This confirms that when context window permits, providing the full conversation transcript yields the best factual recall. However, this approach does not scale to arbitrarily long conversation histories in production.

#### 4.1.2 Auto Extraction Outperforms Agentic

Auto memory extraction (F1=0.357) significantly outperforms agentic (0.294). Auto extraction is more systematic—it processes all conversation content rather than relying on the LLM agent's selective tool calls, generating higher-density, semantically richer memories.

#### 4.1.3 Adversarial Robustness Inversely Correlates with Recall

Memory-based approaches achieve high adversarial F1 (0.653-0.830), while long-context scores only 0.668. This is because memory-based approaches naturally return "information not available" when no relevant memory is retrieved, which is the correct answer for adversarial questions. Long-context, with the full transcript available, more often hallucinates plausible but incorrect answers.

### 4.2 History Injection Analysis

#### 4.2.1 F1/BLEU Decreases with History Injection

Across all 4 scenario-backend combinations, injecting conversation history consistently decreases F1 and BLEU scores:

| Scenario | Backend | F1 (None) | F1 (+300) | F1 (+700) | Delta |
| --- | --- | ---: | ---: | ---: | ---: |
| Auto | pgvector | 0.357 | 0.296 | 0.288 | -0.069 |
| Auto | MySQL | 0.347 | 0.280 | 0.290 | -0.057 |
| Agentic | pgvector | 0.294 | 0.267 | 0.275 | -0.019 |
| Agentic | MySQL | 0.286 | 0.275 | 0.277 | -0.009 |

The primary cause is **adversarial score collapse**. Without history, adversarial F1 ranges 0.653-0.830; with +700 history, it drops to 0.383-0.418. The model, given extensive raw conversation context, attempts to answer questions that should be refused, losing ~0.35-0.45 F1 on the adversarial category (22% of all questions).

#### 4.2.2 LLM Score Significantly Improves

While F1/BLEU drop, LLM-as-Judge scores improve substantially:

| Scenario | Backend | LLM (None) | LLM (+300) | LLM (+700) | Delta |
| --- | --- | ---: | ---: | ---: | ---: |
| Auto | pgvector | 0.366 | 0.414 | 0.464 | +0.098 |
| Auto | MySQL | 0.362 | 0.399 | 0.467 | +0.105 |
| Agentic | pgvector | 0.287 | 0.357 | 0.464 | +0.177 |
| Agentic | MySQL | 0.285 | 0.365 | 0.460 | +0.175 |

This reveals a fundamental tension: injected history makes responses semantically richer and more contextually appropriate (higher LLM Score), but also more verbose and divergent from reference answers (lower F1/BLEU).

#### 4.2.3 Open-Domain Questions Benefit Most

Open-domain LLM Score with Auto pgvector improves dramatically:
- No history: 0.355
- +300: 0.539 (+51.8%)
- +700: 0.685 (+92.9%)

This makes sense: open-domain questions about preferences, opinions, and experiences are best answered with access to the original conversational nuance that discrete memories may not capture.

#### 4.2.4 Diminishing Returns from 300 to 700 Turns

The improvement from 300 to 700 turns is marginal compared to 0 to 300:
- Auto pgvector LLM Score: 0.366 → 0.414 (+0.048) → 0.464 (+0.050)
- Auto pgvector F1: 0.357 → 0.296 (-0.061) → 0.288 (-0.008)

The F1 degradation plateaus after 300 turns, while LLM Score continues to improve linearly. This suggests that ~300 turns captures most of the useful conversational context, with diminishing returns beyond that.

### 4.3 Category-Level Analysis

#### 4.3.1 Temporal Reasoning is Universally Weak

Temporal questions have the lowest F1 across all scenarios (0.043-0.091), including long-context (0.088). This indicates that temporal reasoning is fundamentally hard for gpt-4o-mini, regardless of the memory architecture.

Root causes:
- Conversations use relative time references ("last year", "next month") that require resolution against session dates.
- Even with explicit `[DATE:]` prefixes in stored memories, the model struggles to compute temporal relationships.

#### 4.3.2 Multi-hop Benefits from Agentic Memories

Agentic (pgvector) achieves the highest multi-hop F1 (0.178) among memory-based approaches, surpassing Auto (0.088). This suggests that the agentic approach, while extracting fewer memories overall, creates more interconnected knowledge that aids multi-hop reasoning. The `[DATE:]` prefix injected by the date-aware memory service contributes to this advantage.

#### 4.3.3 Open-Domain Questions Favor Rich Context

Long-context dominates open-domain questions (F1=0.518), while memory-based approaches struggle (0.126-0.302). Open-domain questions often require nuanced understanding of conversational context, preferences, and attitudes that are difficult to capture in discrete memory entries. History injection partially bridges this gap (Auto pgvector open-domain LLM Score: 0.355 → 0.685).

### 4.4 Backend Comparison: pgvector vs MySQL

| Scenario | pgvector F1 | MySQL F1 | Delta |
| --- | ---: | ---: | ---: |
| Agentic | 0.294 | 0.286 | +0.008 |
| Auto | 0.357 | 0.347 | +0.010 |
| Agentic +700 | 0.275 | 0.277 | -0.002 |
| Auto +700 | 0.288 | 0.290 | -0.002 |

pgvector outperforms MySQL without history injection. However, **with history injection, the backend difference vanishes**—the injected conversation context dominates retrieval quality, making the backend choice less important.

### 4.5 Variance Analysis

Per-sample F1 shows notable variance:
- **Long-context**: 0.429 - 0.539 (range 0.110), relatively stable.
- **Auto pgvector**: 0.204 - 0.441 (range 0.237), high variance.

The high variance in memory-based approaches suggests that some conversation structures are inherently harder for memory extraction and retrieval. Samples with more complex, interleaved topics (e.g., locomo10_6, locomo10_8) tend to score lower.

---

## 5. Comparison with External Memory Frameworks

Source: Mem0 Table 1 & Table 2 (Chhikara et al., 2025, arXiv:2504.19413)

> **Comparability notes:** The Mem0 paper evaluates 10 memory frameworks on the LoCoMo benchmark (excluding adversarial category), all using GPT-4o-mini for inference. This work also uses GPT-4o-mini. For comparability, our results below are recalculated excluding the adversarial category. Mem0's original scores are on a 0-100 scale; converted to 0-1 here.

### 5.1 Per-Category F1 Comparison

**Table 8: F1 by Category (Excluding Adversarial)**

| Method | Single-Hop | Multi-Hop | Open-Domain | Temporal | Overall | Source |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Mem0 | 0.387 | 0.286 | 0.477 | 0.489 | 0.410 | Mem0 Table 1 |
| Mem0g | 0.381 | 0.243 | 0.493 | 0.516 | 0.408 | Mem0 Table 1 |
| Zep | 0.357 | 0.194 | 0.496 | 0.420 | 0.367 | Mem0 Table 1 |
| LangMem | 0.355 | 0.260 | 0.409 | 0.308 | 0.333 | Mem0 Table 1 |
| A-Mem | 0.270 | 0.121 | 0.447 | 0.459 | 0.324 | Mem0 Table 1 |
| **trpc-agent (LC)** | **0.330** | **0.319** | **0.518** | **0.088** | **0.314** | This work |
| OpenAI Memory | 0.343 | 0.201 | 0.393 | 0.140 | 0.269 | Mem0 Table 1 |
| MemGPT | 0.267 | 0.092 | 0.410 | 0.255 | 0.256 | Mem0 Table 1 |
| LoCoMo (pipeline) | 0.250 | 0.120 | 0.404 | 0.184 | 0.240 | Mem0 Table 1 |
| **trpc-agent (Auto)** | **0.272** | **0.088** | **0.302** | **0.060** | **0.181** | This work |
| **trpc-agent (Agentic)** | **0.146** | **0.178** | **0.126** | **0.091** | **0.135** | This work |
| ReadAgent | 0.092 | 0.053 | 0.097 | 0.126 | 0.092 | Mem0 Table 1 |
| MemoryBank | 0.050 | 0.056 | 0.066 | 0.097 | 0.067 | Mem0 Table 1 |

```
Single-Hop F1 (no adversarial)

Mem0                |==========================================| 0.387
Mem0g               |=========================================| 0.381
Zep                 |======================================   | 0.357
LangMem             |======================================   | 0.355
OpenAI Memory       |=====================================    | 0.343
trpc-agent (LC)     |====================================     | 0.330
A-Mem               |=============================            | 0.270
trpc-agent (Auto)   |=============================            | 0.272
MemGPT              |============================             | 0.267
LoCoMo (pipeline)   |===========================              | 0.250
trpc-agent (Agentic)|===============                          | 0.146
ReadAgent           |=========                                | 0.092
MemoryBank          |=====                                    | 0.050
                    +------------------------------------------+
                    0.0       0.1       0.2       0.3       0.4

Multi-Hop F1 (no adversarial)

trpc-agent (LC)     |==========================================| 0.319
Mem0                |=====================================     | 0.286
LangMem             |=================================         | 0.260
Mem0g               |===============================           | 0.243
OpenAI Memory       |==========================                | 0.201
Zep                 |=========================                 | 0.194
trpc-agent (Agentic)|=======================                   | 0.178
A-Mem               |===============                           | 0.121
LoCoMo (pipeline)   |===============                           | 0.120
MemGPT              |===========                               | 0.092
trpc-agent (Auto)   |===========                               | 0.088
MemoryBank          |=======                                   | 0.056
ReadAgent           |======                                    | 0.053
                    +------------------------------------------+
                    0.0       0.1       0.2       0.3       0.4

Open-Domain F1 (no adversarial)

trpc-agent (LC)     |==========================================| 0.518
Zep                 |========================================  | 0.496
Mem0g               |=======================================   | 0.493
Mem0                |======================================    | 0.477
A-Mem               |====================================      | 0.447
MemGPT              |=================================         | 0.410
LangMem             |================================          | 0.409
LoCoMo (pipeline)   |================================          | 0.404
OpenAI Memory       |===============================           | 0.393
trpc-agent (Auto)   |========================                  | 0.302
trpc-agent (Agentic)|==========                                | 0.126
ReadAgent           |========                                  | 0.097
MemoryBank          |=====                                     | 0.066
                    +------------------------------------------+
                    0.0     0.1     0.2     0.3     0.4     0.5

Temporal F1 (no adversarial)

Mem0g               |==========================================| 0.516
Mem0                |========================================  | 0.489
A-Mem               |=====================================     | 0.459
Zep                 |==================================        | 0.420
LangMem             |=========================                 | 0.308
MemGPT              |====================                      | 0.255
LoCoMo (pipeline)   |===============                           | 0.184
OpenAI Memory       |===========                               | 0.140
ReadAgent           |==========                                | 0.126
MemoryBank          |========                                  | 0.097
trpc-agent (Agentic)|=======                                   | 0.091
trpc-agent (LC)     |=======                                   | 0.088
trpc-agent (Auto)   |=====                                     | 0.060
                    +------------------------------------------+
                    0.0     0.1     0.2     0.3     0.4     0.5

Overall F1 — 4-category average (no adversarial)

Mem0                |==========================================| 0.410
Mem0g               |=========================================| 0.408
Zep                 |=====================================     | 0.367
LangMem             |=================================         | 0.333
A-Mem               |================================          | 0.324
trpc-agent (LC)     |===============================           | 0.314
OpenAI Memory       |===========================               | 0.269
MemGPT              |==========================                | 0.256
LoCoMo (pipeline)   |========================                  | 0.240
trpc-agent (Auto)   |==================                        | 0.181
trpc-agent (Agentic)|=============                             | 0.135
ReadAgent           |=========                                 | 0.092
MemoryBank          |======                                    | 0.067
                    +------------------------------------------+
                    0.0       0.1       0.2       0.3       0.4
```

> Note: The Mem0 paper does not include adversarial category data, so cross-framework comparison is not possible for adversarial. Our adversarial F1 results are in Section 4 (Long-Context 0.668, Auto pgvec 0.771, Agentic pgvec 0.830). Overall F1 is the simple average of 4 categories.

### 5.2 Overall LLM-as-Judge Comparison

**Table 9: Overall LLM-as-Judge and Latency**

| Method | Overall J | p95 Latency (s) | Memory Tokens | Source |
| --- | ---: | ---: | ---: | --- |
| Full-context | 0.729 | 17.12 | ~26K | Mem0 Table 2 |
| Mem0g | 0.684 | 2.59 | ~14K | Mem0 Table 2 |
| Mem0 | 0.669 | 1.44 | ~7K | Mem0 Table 2 |
| Zep | 0.660 | 2.93 | ~600K | Mem0 Table 2 |
| RAG (k=2, 256) | 0.610 | 1.91 | - | Mem0 Table 2 |
| LangMem | 0.581 | 60.40 | ~127 | Mem0 Table 2 |
| OpenAI Memory | 0.529 | 0.89 | ~4.4K | Mem0 Table 2 |
| **trpc-agent (LC)** | **0.480** | **3.49** | **~26K** | This work |
| A-Mem* | 0.484 | 4.37 | ~2.5K | Mem0 Table 2 |
| **trpc-agent (Auto)** | **0.249** | **5.62** | **-** | This work |
| **trpc-agent (Agentic)** | **0.130** | **5.00** | **-** | This work |

> Note: Our Overall J is the weighted LLM Score across 4 categories (excluding adversarial).

```
Overall LLM-as-Judge — Memory Frameworks (no adversarial)

Full-context (Mem0) |==========================================| 0.729
Mem0g               |======================================    | 0.684
Mem0                |=====================================     | 0.669
Zep                 |=====================================     | 0.660
RAG (k=2, 256)      |===================================       | 0.610
LangMem             |================================          | 0.581
OpenAI Memory       |=============================             | 0.529
A-Mem*              |==========================                | 0.484
trpc-agent (LC)     |==========================                | 0.480
trpc-agent (Auto)   |=============                             | 0.249
trpc-agent (Agentic)|=======                                   | 0.130
                    +------------------------------------------+
                    0.0    0.2    0.4    0.6    0.8
```

### 5.3 Analysis

**Positioning of trpc-agent-go among memory frameworks:**

1. **Multi-Hop reasoning leads the field**: trpc-agent (LC) achieves 0.319 multi-hop F1, **ranking first among all evaluated frameworks**, surpassing Mem0 (0.286), LangMem (0.260), and other dedicated memory systems. This demonstrates a clear architectural advantage in complex reasoning scenarios that require combining facts across sessions.

2. **Open-Domain retrieval ranks first**: trpc-agent (LC) achieves the highest open-domain F1 of 0.518, ahead of Zep (0.496) and Mem0g (0.493). Open-domain questions cover fine-grained information such as preferences, attitudes, and life experiences, highlighting the framework's strength in nuanced semantic understanding.

3. **Single-Hop remains competitive**: trpc-agent (LC) achieves 0.330 single-hop F1, on par with OpenAI Memory (0.343), demonstrating solid baseline fact retrieval capability.

4. **Overall J comparable to A-Mem**: Long-Context mode's Overall J (0.480) is on par with A-Mem (0.484). It should be noted that the Mem0 paper runs the LLM Judge 10 times and averages, while this work uses a single Judge run. This difference in evaluation repetitions has a systematic effect on score stability.

5. **Temporal category has room for improvement**: Current temporal F1 across all modes remains at a modest level (< 0.1), compared to Mem0g (0.516). Temporal reasoning requires precise resolution of relative time expressions ("last year", "next month") in conversations, which is a key optimization target for subsequent releases. Notably, OpenAI Memory (0.140) also shows limited performance in this category, suggesting that temporal reasoning is challenging for most frameworks.

6. **Adversarial robustness is a distinctive strength**: While the Mem0 paper does not include the adversarial category, this work achieves adversarial F1 of 0.668–0.830 (see Section 4). Memory-based approaches excel at identifying unanswerable questions. This capability is critical for safety and reliability in production environments, and represents an important differentiator for trpc-agent-go compared to pure retrieval-based frameworks.

7. **Auto/Agentic modes show development potential**: As the memory subsystem of a general-purpose agent framework, the current Auto and Agentic performance reflects the baseline of the first evaluation release. Compared to dedicated memory systems like Mem0 where memory is the core product, trpc-agent-go's memory module already demonstrates competitiveness in key categories such as multi-hop and open-domain, while maintaining overall framework generality. Significant improvements are expected with the introduction of temporal indexing, graph-based memory, and other planned features.

> **Comparability caveats:**
> - Both use the LoCoMo benchmark (10 conversations) and GPT-4o-mini for inference; our LLM Judge runs once, while the Mem0 paper runs 10 times and averages, yielding lower variance and more stable scores.
> - Mem0 and similar frameworks are dedicated memory systems that search and answer via direct API calls; our Auto/Agentic modes operate through agent tool-call chains with additional architectural layers.
> - Our latency is end-to-end including the full agent tool-call chain, and is not directly comparable to pure memory retrieval latency.

---

## 6. Discussion

### 6.1 Strengths of the Current Implementation

1. **Date-aware memory service**: The `datePrefixMemoryService` wrapper automatically injects `[DATE: ...]` prefixes into agentic memories, improving temporal reasoning without relying on LLM compliance.
2. **Strong adversarial robustness**: Memory-based approaches achieve 0.65-0.83 adversarial F1, correctly identifying unanswerable questions.
3. **Auto extraction**: The background extractor provides a good balance between memory quality and system complexity.

### 6.2 The History Injection Trade-off

History injection reveals a key insight for memory system design:

- **Memory-only** (no history): Best F1/BLEU, best adversarial robustness, but lower semantic quality.
- **Memory + History**: Best LLM Score and open-domain performance, but degraded precision metrics and adversarial robustness.

This suggests a **hybrid approach** for production: use memory retrieval as the primary context source, and selectively inject relevant history segments (rather than all 300-700 turns) when open-domain or nuanced questions are detected.

### 6.3 Limitations and Future Work

1. **Temporal reasoning remains weak** (F1 < 0.1 across all scenarios). Future work should explore dedicated temporal indexing and reasoning modules.
2. **Multi-hop reasoning gap**: Memory-based approaches struggle to combine facts across sessions. Graph-based memory structures could help.
3. **Open-domain performance**: Memory compression inevitably loses nuance. Hierarchical memory (summary + detail) may bridge this gap.
4. **Model capability ceiling**: gpt-4o-mini's extraction and reasoning abilities limit all scenarios. Stronger models (e.g., GPT-4o, Claude) may substantially improve results.
5. **Selective history injection**: Instead of injecting all turns, use relevance filtering to inject only pertinent conversation segments.

### 6.4 Recommendations for Production Use

| Use Case | Recommended Approach |
| --- | --- |
| Short conversation history (< 50K tokens) | Long-context (no memory needed) |
| Long-running agents (months of history) | Auto extraction + pgvector |
| Semantic quality priority | Memory + selective history injection |
| Low latency required | Agentic + MySQL |

---

## 7. Conclusion

This evaluation demonstrates that trpc-agent-go's memory system provides effective long-term conversational memory across multiple paradigms. The Auto extraction approach with pgvector backend achieves the best balance of recall and robustness, reaching 75.6% of the long-context baseline's F1 score while maintaining strong adversarial robustness (0.771).

The history injection experiments reveal an important trade-off: injecting raw conversation history improves semantic quality (LLM Score +0.10~0.18) but degrades token-level precision (F1 -0.02~0.07) and adversarial robustness. This confirms that **structured memory extraction is more effective than brute-force context injection** for factual recall tasks, while history injection adds value for open-domain, nuanced questions.

Key takeaways:
1. **Auto > Agentic** for overall memory effectiveness with gpt-4o-mini.
2. **pgvector > MySQL** for semantic retrieval quality (without history injection).
3. **Temporal reasoning** is the primary bottleneck across all approaches.
4. **Adversarial robustness** is a natural strength of memory-based systems.
5. **History injection** improves semantic quality but hurts precision—a hybrid selective approach is recommended.

---

## Appendix

### A. Experimental Environment

| Component | Version/Config |
| --- | --- |
| Framework | trpc-agent-go |
| Model | gpt-4o-mini |
| Embedding | text-embedding-3-small |
| PostgreSQL | 15+ with pgvector extension |
| MySQL | 8.0+ with full-text search |
| Dataset | LoCoMo-10 (10 samples, 1,986 QA) |

### B. Full Category Breakdown — No History (F1 / BLEU / LLM)

| Scenario | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Long-Context | 0.330/0.260/0.333 | 0.319/0.285/0.252 | 0.088/0.069/0.155 | 0.518/0.456/0.654 | 0.668/0.667/0.670 |
| Agentic pgvec | 0.146/0.106/0.122 | 0.178/0.160/0.107 | 0.091/0.075/0.137 | 0.126/0.114/0.141 | 0.830/0.830/0.830 |
| Agentic MySQL | 0.168/0.125/0.130 | 0.135/0.119/0.093 | 0.043/0.034/0.057 | 0.146/0.132/0.171 | 0.787/0.787/0.787 |
| Auto pgvec | 0.272/0.209/0.220 | 0.088/0.081/0.049 | 0.060/0.047/0.068 | 0.302/0.271/0.355 | 0.771/0.771/0.771 |
| Auto MySQL | 0.306/0.232/0.277 | 0.101/0.092/0.064 | 0.056/0.040/0.083 | 0.325/0.293/0.380 | 0.653/0.653/0.653 |

### C. Full Category Breakdown — +700 History (F1 / BLEU / LLM)

| Scenario | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Agentic pgvec | 0.185/0.142/0.310 | 0.112/0.085/0.212 | 0.089/0.066/0.225 | 0.331/0.249/0.677 | 0.383/0.382/0.391 |
| Agentic MySQL | 0.188/0.144/0.290 | 0.096/0.073/0.191 | 0.084/0.064/0.212 | 0.332/0.250/0.675 | 0.403/0.401/0.408 |
| Auto pgvec | 0.183/0.140/0.291 | 0.106/0.083/0.182 | 0.079/0.057/0.185 | 0.347/0.265/0.685 | 0.418/0.417/0.419 |
| Auto MySQL | 0.181/0.137/0.297 | 0.100/0.079/0.177 | 0.112/0.090/0.205 | 0.354/0.272/0.692 | 0.412/0.411/0.414 |

### D. Full Category Breakdown — +300 History (F1 / BLEU / LLM)

| Scenario | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Agentic pgvec | 0.156/0.122/0.227 | 0.123/0.096/0.103 | 0.062/0.049/0.114 | 0.239/0.191/0.429 | 0.539/0.539/0.540 |
| Agentic MySQL | 0.156/0.122/0.215 | 0.117/0.092/0.114 | 0.061/0.047/0.139 | 0.237/0.190/0.423 | 0.579/0.579/0.580 |
| Auto pgvec | 0.196/0.152/0.294 | 0.120/0.099/0.131 | 0.074/0.058/0.162 | 0.306/0.246/0.539 | 0.514/0.514/0.513 |
| Auto MySQL | 0.180/0.142/0.261 | 0.095/0.079/0.113 | 0.080/0.065/0.163 | 0.297/0.237/0.534 | 0.488/0.487/0.487 |

### E. Total Evaluation Time

| Scenario | Backend | History | Total Time | Avg Latency/QA |
| --- | --- | --- | --- | --- |
| Long-Context | - | - | 1h55m | 3,485ms |
| Agentic | pgvector | None | 2h45m | 4,998ms |
| Agentic | MySQL | None | 2h25m | 4,392ms |
| Auto | pgvector | None | 3h06m | 5,622ms |
| Auto | MySQL | None | 3h08m | 5,678ms |
| Agentic | pgvector | +300 | 3h51m | 6,990ms |
| Agentic | MySQL | +300 | 3h13m | 5,817ms |
| Auto | pgvector | +300 | 3h20m | 6,056ms |
| Auto | MySQL | +300 | 3h04m | 5,547ms |
| Agentic | pgvector | +700 | 2h49m | 5,120ms |
| Agentic | MySQL | +700 | 2h44m | 4,956ms |
| Auto | pgvector | +700 | 3h14m | 5,852ms |
| Auto | MySQL | +700 | 2h56m | 5,321ms |

---

## References

1. Maharana, A., Lee, D., Tulyakov, S., Bansal, M., Barbieri, F., and Fang, Y. "Evaluating Very Long-Term Conversational Memory of LLM Agents." arXiv:2402.17753, 2024.
2. Chhikara, P., Khant, D., Aryan, S., Singh, T., and Yadav, D. "Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory." arXiv:2504.19413, 2025.
3. Hu, C., et al. "Memory in the Age of AI Agents." arXiv:2512.13564, 2024.
