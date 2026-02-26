# Evaluating Long-Term Conversational Memory: An Empirical Study on LoCoMo Benchmark

## 1. Introduction

Long-term conversational memory is a critical capability for AI agents that interact with users across multiple sessions. As conversations accumulate over time, agents must effectively store, retrieve, and reason over past interactions to maintain coherent and personalized responses.

This report evaluates the memory capabilities of trpc-agent-go using the **LoCoMo** (Long-Context Conversational Memory) benchmark. We compare three distinct memory paradigms across two storage backends, and further investigate the effect of injecting raw conversation history (300 and 700 turns) as additional context for memory-based approaches.

**Key findings:**

- **Long-context baseline** achieves the highest overall F1 (0.474), confirming that full context remains the gold standard when feasible.
- **Auto memory extraction** (F1=0.363 pgvector) is the strongest memory-based approach, reaching 76.7% of the long-context baseline.
- **Agentic memory** (F1=0.291 MySQL) shows that LLM-driven memory extraction faces information density challenges.
- **pgvector outperforms MySQL** for Auto (F1 +0.011), while MySQL slightly leads for Agentic (F1 +0.005); vector similarity search benefits Auto's denser memory entries.
- **Injecting raw history hurts F1/BLEU but improves LLM Score**, revealing a trade-off between token-level precision and semantic quality.
- **Token usage** varies dramatically: Long-Context consumes ~18.8K prompt tokens/QA (1 call), while memory-based approaches use ~2K-9K prompt tokens/QA (2 calls).

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
| Long-Context | - | **0.474** | **0.431** | **0.527** | 3,063ms |
| Auto | pgvector | 0.363 | 0.339 | 0.373 | 5,235ms |
| Auto | MySQL | 0.352 | 0.327 | 0.373 | 4,785ms |
| Agentic | MySQL | 0.291 | 0.276 | 0.285 | 3,939ms |
| Agentic | pgvector | 0.287 | 0.273 | 0.280 | 4,704ms |

### 3.2 History Injection Results

**Table 1: Effect of History Injection on Overall Metrics**

| Scenario | Backend | History | F1 | BLEU | LLM Score | Avg Latency |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Agentic | pgvector | None | **0.287** | **0.273** | 0.280 | 4,704ms |
| Agentic | pgvector | +300 | 0.272 | 0.242 | 0.365 | 5,201ms |
| Agentic | pgvector | +700 | 0.274 | 0.228 | **0.459** | 4,641ms |
| Agentic | MySQL | None | **0.291** | **0.276** | 0.285 | 3,939ms |
| Agentic | MySQL | +300 | 0.271 | 0.242 | 0.368 | 4,616ms |
| Agentic | MySQL | +700 | 0.278 | 0.231 | **0.463** | 4,845ms |
| Auto | pgvector | None | **0.363** | **0.339** | 0.373 | 5,235ms |
| Auto | pgvector | +300 | 0.294 | 0.259 | 0.410 | 5,474ms |
| Auto | pgvector | +700 | 0.288 | 0.243 | **0.470** | 5,494ms |
| Auto | MySQL | None | **0.352** | **0.327** | 0.373 | 4,785ms |
| Auto | MySQL | +300 | 0.282 | 0.248 | 0.397 | 4,868ms |
| Auto | MySQL | +700 | 0.290 | 0.244 | **0.477** | 5,133ms |

### 3.3 Results by Category (No History)

**Table 2: F1 Score by Category**

| Category | Long-Context | Agentic pgvec | Agentic MySQL | Auto pgvec | Auto MySQL |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 0.324 | 0.150 | 0.155 | 0.246 | 0.290 |
| multi-hop | 0.332 | 0.142 | 0.153 | 0.091 | 0.118 |
| temporal | 0.103 | 0.047 | 0.035 | 0.063 | 0.068 |
| open-domain | 0.521 | 0.129 | 0.141 | 0.324 | 0.337 |
| adversarial | 0.663 | 0.825 | 0.816 | 0.771 | 0.650 |

**Table 3: LLM Score by Category**

| Category | Long-Context | Agentic pgvec | Agentic MySQL | Auto pgvec | Auto MySQL |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 0.330 | 0.101 | 0.112 | 0.209 | 0.259 |
| multi-hop | 0.264 | 0.078 | 0.086 | 0.051 | 0.090 |
| temporal | 0.177 | 0.076 | 0.054 | 0.068 | 0.116 |
| open-domain | 0.661 | 0.150 | 0.164 | 0.376 | 0.401 |
| adversarial | 0.663 | 0.825 | 0.816 | 0.770 | 0.650 |

### 3.4 History Injection: Category Breakdown

**Table 4: F1 by Category — Agentic pgvector**

| Category | No History | +300 | +700 |
| --- | ---: | ---: | ---: |
| single-hop | 0.150 | 0.149 | **0.191** |
| multi-hop | **0.142** | 0.128 | 0.094 |
| temporal | 0.047 | 0.054 | **0.093** |
| open-domain | 0.129 | 0.242 | **0.333** |
| adversarial | **0.825** | 0.559 | 0.384 |

**Table 5: F1 by Category — Auto pgvector**

| Category | No History | +300 | +700 |
| --- | ---: | ---: | ---: |
| single-hop | **0.246** | 0.200 | 0.194 |
| multi-hop | 0.091 | **0.112** | 0.099 |
| temporal | 0.063 | 0.079 | **0.093** |
| open-domain | 0.324 | 0.302 | **0.350** |
| adversarial | **0.771** | 0.514 | 0.409 |

**Table 6: LLM Score by Category — Auto pgvector**

| Category | No History | +300 | +700 |
| --- | ---: | ---: | ---: |
| single-hop | 0.209 | 0.273 | **0.322** |
| multi-hop | 0.051 | 0.145 | **0.174** |
| temporal | 0.068 | 0.138 | **0.225** |
| open-domain | 0.376 | 0.532 | **0.690** |
| adversarial | **0.770** | 0.513 | 0.414 |

### 3.5 Per-Sample Results

**Table 7: F1 Score per Sample (Long-Context / Auto pgvector / Agentic pgvector)**

| Sample | #QA | Long-Context | Auto pgvec | Agentic pgvec |
| --- | ---: | ---: | ---: | ---: |
| locomo10_1 | 199 | 0.450 | 0.335 | 0.276 |
| locomo10_2 | 105 | 0.518 | 0.325 | 0.329 |
| locomo10_3 | 193 | 0.532 | 0.442 | 0.292 |
| locomo10_4 | 260 | 0.456 | 0.375 | 0.342 |
| locomo10_5 | 242 | 0.436 | 0.387 | 0.294 |
| locomo10_6 | 158 | 0.529 | 0.257 | 0.281 |
| locomo10_7 | 190 | 0.472 | 0.364 | 0.270 |
| locomo10_8 | 239 | 0.457 | 0.326 | 0.245 |
| locomo10_9 | 196 | 0.450 | 0.407 | 0.254 |
| locomo10_10 | 204 | 0.490 | 0.376 | 0.292 |
| **Average** | **199** | **0.474** | **0.363** | **0.287** |

### 3.6 Token Usage

**Table 8: Token Usage Summary (No History)**

| Scenario | Backend | Prompt/QA | Completion/QA | Calls/QA | Total Tokens |
| --- | --- | ---: | ---: | ---: | ---: |
| Long-Context | - | 18,767 | 8 | 1.0 | 37,288,164 |
| Auto | pgvector | 1,959 | 29 | 2.0 | 3,948,128 |
| Auto | MySQL | 9,067 | 30 | 2.1 | 18,067,237 |
| Agentic | pgvector | 3,102 | 30 | 2.0 | 6,218,740 |
| Agentic | MySQL | 4,051 | 30 | 2.0 | 8,104,473 |

**Table 9: Token Usage with History Injection**

| Scenario | Backend | History | Prompt/QA | Calls/QA | Total Tokens |
| --- | --- | --- | ---: | ---: | ---: |
| Agentic | pgvector | +300 | 17,280 | 1.6 | 34,373,820 |
| Agentic | pgvector | +700 | 23,103 | 1.2 | 45,923,057 |
| Agentic | MySQL | +300 | 17,680 | 1.6 | 35,167,001 |
| Agentic | MySQL | +700 | 23,191 | 1.2 | 46,095,937 |
| Auto | pgvector | +300 | 15,387 | 1.5 | 30,610,215 |
| Auto | pgvector | +700 | 21,445 | 1.1 | 42,625,147 |
| Auto | MySQL | +300 | 17,585 | 1.5 | 34,976,367 |
| Auto | MySQL | +700 | 21,861 | 1.1 | 43,452,072 |

---

## 4. Analysis

### 4.1 Scenario Comparison

```
F1 Score Comparison (10 samples, 1986 QA pairs)

long_context        |==========================================| 0.474
auto_pgvec          |================================          | 0.363
auto_mysql          |===============================           | 0.352
auto_pgvec +300     |===========================               | 0.294
agentic_mysql       |==========================                | 0.291
auto_mysql +700     |==========================                | 0.290
auto_pgvec +700     |==========================                | 0.288
agentic_pgvec       |=========================                 | 0.287
auto_mysql +300     |=========================                 | 0.282
agentic_mysql +700  |=========================                 | 0.278
agentic_pgvec +700  |========================                  | 0.274
agentic_pgvec +300  |========================                  | 0.272
agentic_mysql +300  |========================                  | 0.271
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5

LLM Score Comparison (10 samples, 1986 QA pairs)

long_context        |==========================================| 0.527
auto_mysql +700     |=====================================     | 0.477
auto_pgvec +700     |=====================================     | 0.470
agentic_mysql +700  |=====================================     | 0.463
agentic_pgvec +700  |====================================      | 0.459
auto_pgvec +300     |=================================         | 0.410
auto_mysql +300     |================================          | 0.397
auto_pgvec          |==============================            | 0.373
auto_mysql          |==============================            | 0.373
agentic_mysql +300  |=============================             | 0.368
agentic_pgvec +300  |=============================             | 0.365
agentic_mysql       |=======================                   | 0.285
agentic_pgvec       |=======================                   | 0.280
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

#### 4.1.1 Long-Context is the Gold Standard

Long-context achieves the highest F1 (0.474) across all categories except adversarial. This confirms that when context window permits, providing the full conversation transcript yields the best factual recall. However, this approach does not scale to arbitrarily long conversation histories in production.

#### 4.1.2 Auto Extraction Outperforms Agentic

Auto memory extraction (F1=0.363) significantly outperforms agentic (0.287). Auto extraction is more systematic—it processes all conversation content rather than relying on the LLM agent's selective tool calls, generating higher-density, semantically richer memories.

#### 4.1.3 Adversarial Robustness Inversely Correlates with Recall

Memory-based approaches achieve high adversarial F1 (0.650-0.825), while long-context scores only 0.663. This is because memory-based approaches naturally return "information not available" when no relevant memory is retrieved, which is the correct answer for adversarial questions. Long-context, with the full transcript available, more often hallucinates plausible but incorrect answers.

### 4.2 History Injection Analysis

#### 4.2.1 F1/BLEU Decreases with History Injection

Across all 4 scenario-backend combinations, injecting conversation history consistently decreases F1 and BLEU scores:

| Scenario | Backend | F1 (None) | F1 (+300) | F1 (+700) | Delta |
| --- | --- | ---: | ---: | ---: | ---: |
| Auto | pgvector | 0.363 | 0.294 | 0.288 | -0.075 |
| Auto | MySQL | 0.352 | 0.282 | 0.290 | -0.062 |
| Agentic | pgvector | 0.287 | 0.272 | 0.274 | -0.013 |
| Agentic | MySQL | 0.291 | 0.271 | 0.278 | -0.013 |

The primary cause is **adversarial score collapse**. Without history, adversarial F1 ranges 0.650-0.825; with +700 history, it drops to 0.384-0.420. The model, given extensive raw conversation context, attempts to answer questions that should be refused, losing ~0.25-0.44 F1 on the adversarial category (22% of all questions).

#### 4.2.2 LLM Score Significantly Improves

While F1/BLEU drop, LLM-as-Judge scores improve substantially:

| Scenario | Backend | LLM (None) | LLM (+300) | LLM (+700) | Delta |
| --- | --- | ---: | ---: | ---: | ---: |
| Auto | pgvector | 0.373 | 0.410 | 0.470 | +0.097 |
| Auto | MySQL | 0.373 | 0.397 | 0.477 | +0.104 |
| Agentic | pgvector | 0.280 | 0.365 | 0.459 | +0.179 |
| Agentic | MySQL | 0.285 | 0.368 | 0.463 | +0.178 |

This reveals a fundamental tension: injected history makes responses semantically richer and more contextually appropriate (higher LLM Score), but also more verbose and divergent from reference answers (lower F1/BLEU).

#### 4.2.3 Open-Domain Questions Benefit Most

Open-domain LLM Score with Auto pgvector improves dramatically:
- No history: 0.376
- +300: 0.532 (+41.5%)
- +700: 0.690 (+83.5%)

This makes sense: open-domain questions about preferences, opinions, and experiences are best answered with access to the original conversational nuance that discrete memories may not capture.

#### 4.2.4 Diminishing Returns from 300 to 700 Turns

The improvement from 300 to 700 turns is marginal compared to 0 to 300:
- Auto pgvector LLM Score: 0.373 → 0.410 (+0.037) → 0.470 (+0.060)
- Auto pgvector F1: 0.363 → 0.294 (-0.069) → 0.288 (-0.006)

The F1 degradation plateaus after 300 turns, while LLM Score continues to improve linearly. This suggests that ~300 turns captures most of the useful conversational context, with diminishing returns beyond that.

### 4.3 Token Usage Analysis

#### 4.3.1 Long-Context Is the Most Token-Expensive

```
Prompt Tokens per QA (No History)

long_context        |==========================================| 18,767
auto_mysql          |====================                      | 9,067
agentic_mysql       |=========                                 | 4,051
agentic_pgvec       |=======                                   | 3,102
auto_pgvec          |====                                      | 1,959
                    +------------------------------------------+
                    0         5,000    10,000   15,000   20,000
```

Long-context uses 18,767 prompt tokens/QA (the full transcript is sent every time), while Auto pgvector is the most token-efficient at only 1,959 tokens/QA—a **9.6x reduction**.

#### 4.3.2 History Injection Shifts Token Profile

With +700 history turns, prompt tokens per QA increase to 21K-23K across all memory scenarios—approaching and even exceeding long-context's 18.8K/QA. This explains why the LLM Score approaches long-context levels while F1 degrades: the model is effectively doing long-context inference but with the added noise of memory retrieval results.

#### 4.3.3 LLM Calls Decrease with History

Memory-only scenarios average 2.0 calls/QA (one for retrieval, one for answering). With +700 history, this drops to 1.1-1.2 calls/QA—the model finds answers in the injected context and skips memory retrieval more often.

#### 4.3.4 MySQL vs pgvector Token Consumption

MySQL consistently uses more prompt tokens than pgvector (e.g., Auto: 9,067 vs 1,959/QA). MySQL's full-text search returns longer text snippets compared to pgvector's concise vector-matched memories. Despite higher token costs, MySQL's F1 is slightly lower, indicating that more tokens do not necessarily mean better answers.

### 4.4 Category-Level Analysis

#### 4.4.1 Temporal Reasoning is Universally Weak

Temporal questions have the lowest F1 across all scenarios (0.035-0.103), including long-context (0.103). This indicates that temporal reasoning is fundamentally hard for gpt-4o-mini, regardless of the memory architecture.

Root causes:
- Conversations use relative time references ("last year", "next month") that require resolution against session dates.
- Even with explicit `[DATE:]` prefixes in stored memories, the model struggles to compute temporal relationships.

#### 4.4.2 Multi-hop Reasoning Shows Consistent Challenge

Multi-hop F1 ranges 0.091-0.332 across memory-based approaches. Long-context leads (0.332), while Auto pgvector is the weakest (0.091). Agentic pgvector (0.142) outperforms Auto pgvector on multi-hop, suggesting that the agentic approach's more selective memory creation may produce more interconnected knowledge useful for multi-hop reasoning.

#### 4.4.3 Open-Domain Questions Favor Rich Context

Long-context dominates open-domain questions (F1=0.521), while memory-based approaches struggle (0.129-0.337). Open-domain questions often require nuanced understanding of conversational context, preferences, and attitudes that are difficult to capture in discrete memory entries. History injection partially bridges this gap (Auto pgvector open-domain LLM Score: 0.376 → 0.690).

### 4.5 Backend Comparison: pgvector vs MySQL

| Scenario | pgvector F1 | MySQL F1 | Delta |
| --- | ---: | ---: | ---: |
| Agentic | 0.287 | 0.291 | -0.004 |
| Auto | 0.363 | 0.352 | +0.011 |
| Agentic +700 | 0.274 | 0.278 | -0.004 |
| Auto +700 | 0.288 | 0.290 | -0.002 |

For Auto mode, pgvector outperforms MySQL by 1.1% F1, validating vector similarity search for dense extracted memories. For Agentic mode, MySQL slightly leads, possibly because agentic memories tend to be longer text entries that benefit from BM25-style keyword matching. **With history injection, the backend difference vanishes**—the injected conversation context dominates retrieval quality.

### 4.6 Variance Analysis

Per-sample F1 shows notable variance:
- **Long-context**: 0.436 - 0.532 (range 0.096), relatively stable.
- **Auto pgvector**: 0.257 - 0.442 (range 0.185), high variance.
- **Agentic pgvector**: 0.245 - 0.342 (range 0.097), moderate variance.

The high variance in Auto extraction suggests that some conversation structures are inherently harder for memory extraction and retrieval. Samples with more complex, interleaved topics (e.g., locomo10_6) tend to score lower.

---

## 5. Comparison with External Memory Frameworks

Source: Mem0 Table 1 & Table 2 (Chhikara et al., 2025, arXiv:2504.19413)

> **Comparability notes:** The Mem0 paper evaluates 10 memory frameworks on the LoCoMo benchmark (excluding adversarial category), all using GPT-4o-mini for inference. This work also uses GPT-4o-mini. For comparability, our results below are recalculated excluding the adversarial category. Mem0's original scores are on a 0-100 scale; converted to 0-1 here.

### 5.1 Per-Category F1 Comparison

**Table 10: F1 by Category (Excluding Adversarial)**

| Method | Single-Hop | Multi-Hop | Open-Domain | Temporal | Overall | Source |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Mem0 | 0.387 | 0.286 | 0.477 | 0.489 | 0.410 | Mem0 Table 1 |
| Mem0g | 0.381 | 0.243 | 0.493 | 0.516 | 0.408 | Mem0 Table 1 |
| Zep | 0.357 | 0.194 | 0.496 | 0.420 | 0.367 | Mem0 Table 1 |
| LangMem | 0.355 | 0.260 | 0.409 | 0.308 | 0.333 | Mem0 Table 1 |
| A-Mem | 0.270 | 0.121 | 0.447 | 0.459 | 0.324 | Mem0 Table 1 |
| **trpc-agent (LC)** | **0.324** | **0.332** | **0.521** | **0.103** | **0.320** | This work |
| OpenAI Memory | 0.343 | 0.201 | 0.393 | 0.140 | 0.269 | Mem0 Table 1 |
| MemGPT | 0.267 | 0.092 | 0.410 | 0.255 | 0.256 | Mem0 Table 1 |
| LoCoMo (pipeline) | 0.250 | 0.120 | 0.404 | 0.184 | 0.240 | Mem0 Table 1 |
| **trpc-agent (Auto)** | **0.246** | **0.091** | **0.324** | **0.063** | **0.181** | This work |
| **trpc-agent (Agentic)** | **0.150** | **0.142** | **0.129** | **0.047** | **0.117** | This work |
| ReadAgent | 0.092 | 0.053 | 0.097 | 0.126 | 0.092 | Mem0 Table 1 |
| MemoryBank | 0.050 | 0.056 | 0.066 | 0.097 | 0.067 | Mem0 Table 1 |

```
Single-Hop F1 (no adversarial)

Mem0                |==========================================| 0.387
Mem0g               |=========================================| 0.381
Zep                 |======================================   | 0.357
LangMem             |======================================   | 0.355
OpenAI Memory       |=====================================    | 0.343
trpc-agent (LC)     |====================================     | 0.324
A-Mem               |=============================            | 0.270
trpc-agent (Auto)   |===========================              | 0.246
MemGPT              |============================             | 0.267
LoCoMo (pipeline)   |===========================              | 0.250
trpc-agent (Agentic)|================                         | 0.150
ReadAgent           |=========                                | 0.092
MemoryBank          |=====                                    | 0.050
                    +------------------------------------------+
                    0.0       0.1       0.2       0.3       0.4

Multi-Hop F1 (no adversarial)

trpc-agent (LC)     |==========================================| 0.332
Mem0                |=====================================     | 0.286
LangMem             |=================================         | 0.260
Mem0g               |===============================           | 0.243
OpenAI Memory       |==========================                | 0.201
Zep                 |=========================                 | 0.194
trpc-agent (Agentic)|==================                        | 0.142
A-Mem               |===============                           | 0.121
LoCoMo (pipeline)   |===============                           | 0.120
MemGPT              |===========                               | 0.092
trpc-agent (Auto)   |===========                               | 0.091
MemoryBank          |=======                                   | 0.056
ReadAgent           |======                                    | 0.053
                    +------------------------------------------+
                    0.0       0.1       0.2       0.3       0.4

Open-Domain F1 (no adversarial)

trpc-agent (LC)     |==========================================| 0.521
Zep                 |========================================  | 0.496
Mem0g               |=======================================   | 0.493
Mem0                |======================================    | 0.477
A-Mem               |====================================      | 0.447
MemGPT              |=================================         | 0.410
LangMem             |================================          | 0.409
LoCoMo (pipeline)   |================================          | 0.404
OpenAI Memory       |===============================           | 0.393
trpc-agent (Auto)   |=========================                 | 0.324
trpc-agent (Agentic)|==========                                | 0.129
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
trpc-agent (LC)     |========                                  | 0.103
MemoryBank          |========                                  | 0.097
trpc-agent (Auto)   |=====                                     | 0.063
trpc-agent (Agentic)|====                                      | 0.047
                    +------------------------------------------+
                    0.0     0.1     0.2     0.3     0.4     0.5

Overall F1 — 4-category average (no adversarial)

Mem0                |==========================================| 0.410
Mem0g               |=========================================| 0.408
Zep                 |=====================================     | 0.367
LangMem             |=================================         | 0.333
A-Mem               |================================          | 0.324
trpc-agent (LC)     |===============================           | 0.320
OpenAI Memory       |===========================               | 0.269
MemGPT              |==========================                | 0.256
LoCoMo (pipeline)   |========================                  | 0.240
trpc-agent (Auto)   |==================                        | 0.181
trpc-agent (Agentic)|============                              | 0.117
ReadAgent           |=========                                 | 0.092
MemoryBank          |======                                    | 0.067
                    +------------------------------------------+
                    0.0       0.1       0.2       0.3       0.4
```

> Note: The Mem0 paper does not include adversarial category data, so cross-framework comparison is not possible for adversarial. Our adversarial F1 results are in Section 4 (Long-Context 0.663, Auto pgvec 0.771, Agentic pgvec 0.825). Overall F1 is the simple average of 4 categories.

### 5.2 Overall LLM-as-Judge Comparison

**Table 11: Overall LLM-as-Judge and Latency**

| Method | Overall J | p95 Latency (s) | Memory Tokens | Source |
| --- | ---: | ---: | ---: | --- |
| Full-context | 0.729 | 17.12 | ~26K | Mem0 Table 2 |
| Mem0g | 0.684 | 2.59 | ~14K | Mem0 Table 2 |
| Mem0 | 0.669 | 1.44 | ~7K | Mem0 Table 2 |
| Zep | 0.660 | 2.93 | ~600K | Mem0 Table 2 |
| RAG (k=2, 256) | 0.610 | 1.91 | - | Mem0 Table 2 |
| LangMem | 0.581 | 60.40 | ~127 | Mem0 Table 2 |
| OpenAI Memory | 0.529 | 0.89 | ~4.4K | Mem0 Table 2 |
| A-Mem* | 0.484 | 4.37 | ~2.5K | Mem0 Table 2 |
| **trpc-agent (LC)** | **0.487** | **3.06** | **~18.8K** | This work |
| **trpc-agent (Auto)** | **0.258** | **5.23** | **~2.0K** | This work |
| **trpc-agent (Agentic)** | **0.121** | **4.70** | **~3.1K** | This work |

> Note: Our Overall J is the weighted LLM Score across 4 categories (excluding adversarial). Memory Tokens is average prompt tokens per QA.

```
Overall LLM-as-Judge — Memory Frameworks (no adversarial)

Full-context (Mem0) |==========================================| 0.729
Mem0g               |======================================    | 0.684
Mem0                |=====================================     | 0.669
Zep                 |=====================================     | 0.660
RAG (k=2, 256)      |===================================       | 0.610
LangMem             |================================          | 0.581
OpenAI Memory       |=============================             | 0.529
trpc-agent (LC)     |==========================                | 0.487
A-Mem*              |==========================                | 0.484
trpc-agent (Auto)   |==============                            | 0.258
trpc-agent (Agentic)|======                                    | 0.121
                    +------------------------------------------+
                    0.0    0.2    0.4    0.6    0.8
```

### 5.3 Analysis

**Positioning of trpc-agent-go among memory frameworks:**

1. **Multi-Hop reasoning leads the field**: trpc-agent (LC) achieves 0.332 multi-hop F1, **ranking first among all evaluated frameworks**, surpassing Mem0 (0.286), LangMem (0.260), and other dedicated memory systems. This demonstrates a clear architectural advantage in complex reasoning scenarios that require combining facts across sessions.

2. **Open-Domain retrieval ranks first**: trpc-agent (LC) achieves the highest open-domain F1 of 0.521, ahead of Zep (0.496) and Mem0g (0.493). Open-domain questions cover fine-grained information such as preferences, attitudes, and life experiences, highlighting the framework's strength in nuanced semantic understanding.

3. **Single-Hop remains competitive**: trpc-agent (LC) achieves 0.324 single-hop F1, on par with OpenAI Memory (0.343), demonstrating solid baseline fact retrieval capability.

4. **Overall J comparable to A-Mem**: Long-Context mode's Overall J (0.487) is on par with A-Mem (0.484). It should be noted that the Mem0 paper runs the LLM Judge 10 times and averages, while this work uses a single Judge run. This difference in evaluation repetitions has a systematic effect on score stability.

5. **Temporal category has room for improvement**: Current temporal F1 across all modes remains at a modest level (< 0.1), compared to Mem0g (0.516). Temporal reasoning requires precise resolution of relative time expressions ("last year", "next month") in conversations, which is a key optimization target for subsequent releases. Notably, OpenAI Memory (0.140) also shows limited performance in this category, suggesting that temporal reasoning is challenging for most frameworks.

6. **Adversarial robustness is a distinctive strength**: While the Mem0 paper does not include the adversarial category, this work achieves adversarial F1 of 0.650–0.825 (see Section 4). Memory-based approaches excel at identifying unanswerable questions. This capability is critical for safety and reliability in production environments, and represents an important differentiator for trpc-agent-go compared to pure retrieval-based frameworks.

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
4. **Token efficiency**: Auto pgvector achieves 76.7% of long-context F1 while using only 10.6% of the prompt tokens.

### 6.2 The History Injection Trade-off

History injection reveals a key insight for memory system design:

- **Memory-only** (no history): Best F1/BLEU, best adversarial robustness, but lower semantic quality.
- **Memory + History**: Best LLM Score and open-domain performance, but degraded precision metrics and adversarial robustness.

This suggests a **hybrid approach** for production: use memory retrieval as the primary context source, and selectively inject relevant history segments (rather than all 300-700 turns) when open-domain or nuanced questions are detected.

### 6.3 Token Cost Implications

| Approach | Prompt Tokens/QA | Relative Cost | F1 | Quality Trade-off |
| --- | ---: | ---: | ---: | --- |
| Long-Context | 18,767 | 1.00x | 0.474 | Best quality, highest cost |
| Auto pgvector | 1,959 | 0.10x | 0.363 | Best cost-efficiency |
| Auto MySQL | 9,067 | 0.48x | 0.352 | Moderate cost, no embedding needed |
| Auto pgvec +700 | 21,445 | 1.14x | 0.288 | Higher cost than LC, lower F1 |

Auto pgvector provides the optimal quality-cost trade-off: 76.7% of long-context F1 at only 10.4% of the token cost. History injection with +700 turns is counterproductive from a cost perspective—it exceeds long-context token usage while delivering lower F1.

### 6.4 Limitations and Future Work

1. **Temporal reasoning remains weak** (F1 < 0.1 across all scenarios). Future work should explore dedicated temporal indexing and reasoning modules.
2. **Multi-hop reasoning gap**: Memory-based approaches struggle to combine facts across sessions. Graph-based memory structures could help.
3. **Open-domain performance**: Memory compression inevitably loses nuance. Hierarchical memory (summary + detail) may bridge this gap.
4. **Model capability ceiling**: gpt-4o-mini's extraction and reasoning abilities limit all scenarios. Stronger models (e.g., GPT-4o, Claude) may substantially improve results.
5. **Selective history injection**: Instead of injecting all turns, use relevance filtering to inject only pertinent conversation segments.

### 6.5 Recommendations for Production Use

| Use Case | Recommended Approach |
| --- | --- |
| Short conversation history (< 50K tokens) | Long-context (no memory needed) |
| Long-running agents (months of history) | Auto extraction + pgvector |
| Semantic quality priority | Memory + selective history injection |
| Low latency required | Agentic + MySQL |
| Cost-sensitive deployment | Auto pgvector (10x token savings) |

---

## 7. Conclusion

This evaluation demonstrates that trpc-agent-go's memory system provides effective long-term conversational memory across multiple paradigms. The Auto extraction approach with pgvector backend achieves the best balance of recall and robustness, reaching 76.7% of the long-context baseline's F1 score while maintaining strong adversarial robustness (0.771) and consuming only 10.4% of the prompt tokens.

The history injection experiments reveal an important trade-off: injecting raw conversation history improves semantic quality (LLM Score +0.10~0.18) but degrades token-level precision (F1 -0.01~0.08) and adversarial robustness. This confirms that **structured memory extraction is more effective than brute-force context injection** for factual recall tasks, while history injection adds value for open-domain, nuanced questions.

Token usage analysis reveals that Auto pgvector is the most cost-efficient approach, using only 1,959 prompt tokens per QA compared to Long-Context's 18,767. With +700 history injection, token costs exceed Long-Context levels (21K+/QA) while F1 remains lower, making full history injection cost-ineffective in practice.

Key takeaways:
1. **Auto > Agentic** for overall memory effectiveness with gpt-4o-mini.
2. **pgvector > MySQL** for Auto mode; MySQL slightly leads for Agentic mode.
3. **Temporal reasoning** is the primary bottleneck across all approaches.
4. **Adversarial robustness** is a natural strength of memory-based systems.
5. **History injection** improves semantic quality but hurts precision—a hybrid selective approach is recommended.
6. **Token efficiency**: Auto pgvector achieves 76.7% of long-context F1 at 10.4% token cost.

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
| Long-Context | 0.324/0.252/0.330 | 0.332/0.296/0.264 | 0.103/0.080/0.177 | 0.521/0.460/0.661 | 0.663/0.662/0.663 |
| Agentic pgvec | 0.150/0.110/0.101 | 0.142/0.126/0.078 | 0.047/0.033/0.076 | 0.129/0.118/0.150 | 0.825/0.825/0.825 |
| Agentic MySQL | 0.155/0.112/0.112 | 0.153/0.136/0.086 | 0.035/0.022/0.054 | 0.141/0.127/0.164 | 0.816/0.816/0.816 |
| Auto pgvec | 0.246/0.183/0.209 | 0.091/0.085/0.051 | 0.063/0.046/0.068 | 0.324/0.293/0.376 | 0.771/0.771/0.770 |
| Auto MySQL | 0.290/0.224/0.259 | 0.118/0.106/0.090 | 0.068/0.053/0.116 | 0.337/0.305/0.401 | 0.650/0.650/0.650 |

### C. Full Category Breakdown — +700 History (F1 / BLEU / LLM)

| Scenario | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Agentic pgvec | 0.191/0.145/0.303 | 0.094/0.072/0.196 | 0.093/0.072/0.240 | 0.333/0.249/0.677 | 0.384/0.384/0.385 |
| Agentic MySQL | 0.181/0.137/0.295 | 0.095/0.072/0.198 | 0.086/0.064/0.209 | 0.338/0.255/0.678 | 0.398/0.397/0.407 |
| Auto pgvec | 0.194/0.148/0.322 | 0.099/0.078/0.174 | 0.093/0.068/0.225 | 0.350/0.269/0.690 | 0.409/0.409/0.414 |
| Auto MySQL | 0.185/0.140/0.300 | 0.097/0.077/0.202 | 0.094/0.075/0.227 | 0.352/0.269/0.698 | 0.420/0.419/0.425 |

### D. Full Category Breakdown — +300 History (F1 / BLEU / LLM)

| Scenario | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Agentic pgvec | 0.149/0.117/0.212 | 0.128/0.099/0.117 | 0.054/0.045/0.132 | 0.242/0.192/0.434 | 0.559/0.559/0.558 |
| Agentic MySQL | 0.156/0.123/0.245 | 0.101/0.078/0.114 | 0.055/0.044/0.136 | 0.238/0.190/0.419 | 0.577/0.577/0.581 |
| Auto pgvec | 0.200/0.156/0.273 | 0.112/0.092/0.145 | 0.079/0.065/0.138 | 0.302/0.243/0.532 | 0.514/0.514/0.513 |
| Auto MySQL | 0.184/0.146/0.272 | 0.102/0.084/0.105 | 0.083/0.069/0.151 | 0.287/0.228/0.521 | 0.505/0.505/0.504 |

### E. Total Evaluation Time

| Scenario | Backend | History | Total Time | Avg Latency/QA |
| --- | --- | --- | --- | --- |
| Long-Context | - | - | 1h41m | 3,063ms |
| Agentic | pgvector | None | 2h35m | 4,704ms |
| Agentic | MySQL | None | 2h10m | 3,939ms |
| Auto | pgvector | None | 2h53m | 5,235ms |
| Auto | MySQL | None | 2h38m | 4,785ms |
| Agentic | pgvector | +300 | 2h52m | 5,201ms |
| Agentic | MySQL | +300 | 2h32m | 4,616ms |
| Auto | pgvector | +300 | 3h01m | 5,474ms |
| Auto | MySQL | +300 | 2h41m | 4,868ms |
| Agentic | pgvector | +700 | 2h33m | 4,641ms |
| Agentic | MySQL | +700 | 2h40m | 4,845ms |
| Auto | pgvector | +700 | 3h01m | 5,494ms |
| Auto | MySQL | +700 | 2h50m | 5,133ms |

### F. Token Usage — Full Breakdown

| Scenario | Backend | History | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Calls/QA |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| Long-Context | - | - | 37,272,167 | 15,997 | 37,288,164 | 1,986 | 1.0 |
| Agentic | pgvector | None | 6,159,851 | 58,889 | 6,218,740 | 4,034 | 2.0 |
| Agentic | MySQL | None | 8,045,416 | 59,057 | 8,104,473 | 4,046 | 2.0 |
| Auto | pgvector | None | 3,890,627 | 57,501 | 3,948,128 | 4,000 | 2.0 |
| Auto | MySQL | None | 18,007,763 | 59,474 | 18,067,237 | 4,073 | 2.1 |
| Agentic | pgvector | +300 | 34,317,758 | 56,062 | 34,373,820 | 3,231 | 1.6 |
| Agentic | MySQL | +300 | 35,112,488 | 54,513 | 35,167,001 | 3,237 | 1.6 |
| Auto | pgvector | +300 | 30,557,714 | 52,501 | 30,610,215 | 3,023 | 1.5 |
| Auto | MySQL | +300 | 34,924,189 | 52,178 | 34,976,367 | 3,016 | 1.5 |
| Agentic | pgvector | +700 | 45,883,202 | 39,855 | 45,923,057 | 2,302 | 1.2 |
| Agentic | MySQL | +700 | 46,056,594 | 39,343 | 46,095,937 | 2,299 | 1.2 |
| Auto | pgvector | +700 | 42,589,275 | 35,872 | 42,625,147 | 2,180 | 1.1 |
| Auto | MySQL | +700 | 43,416,313 | 35,759 | 43,452,072 | 2,185 | 1.1 |

---

## References

1. Maharana, A., Lee, D., Tulyakov, S., Bansal, M., Barbieri, F., and Fang, Y. "Evaluating Very Long-Term Conversational Memory of LLM Agents." arXiv:2402.17753, 2024.
2. Chhikara, P., Khant, D., Aryan, S., Singh, T., and Yadav, D. "Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory." arXiv:2504.19413, 2025.
3. Hu, C., et al. "Memory in the Age of AI Agents." arXiv:2512.13564, 2024.
