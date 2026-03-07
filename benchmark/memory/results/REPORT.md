# Evaluating Long-Term Conversational Memory on LoCoMo Benchmark

## 1. Introduction

This report evaluates the long-term conversational memory of
**trpc-agent-go** using the **LoCoMo** benchmark (Maharana et al.,
2024). It covers two versions:

- **trpc-agent-go (original)**: Baseline version (Auto extraction + pgvector)
- **trpc-agent-go (optimized)**: After multiple rounds of memory
  extraction and retrieval optimization

Both versions are compared against four Python agent frameworks
(AutoGen, Agno, ADK, CrewAI) and ten external memory systems
(Mem0, Zep, etc.).

## 2. Experimental Setup

### 2.1 Benchmark

| Item | Value |
| --- | --- |
| Dataset | LoCoMo-10 (10 conversations, 1,986 QA) |
| Categories | single-hop (282), multi-hop (321), temporal (96), open-domain (841), adversarial (446) |
| Model | GPT-4o-mini (inference + judge) |
| Embedding | text-embedding-3-small |

### 2.2 Scenarios

| Scenario | Description |
| --- | --- |
| **Long-Context** | Full transcript as LLM context (upper bound) |
| **Auto + pgvector (original)** | Background extractor writes memories; vector retrieval at query time (baseline) |
| **Auto + pgvector (optimized)** | Optimized memory extraction strategy and multi-pass retrieval |

## 3. Results

### 3.1 Internal Scenario Comparison

**Table 1: Overall Metrics**

| Scenario | F1 | BLEU | LLM Score | Tokens/QA | Calls/QA | Latency | Total Time |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Long-Context | **0.474** | **0.431** | **0.527** | 18,776 | 1.0 | 3,063ms | 1h41m |
| Auto pgvector (optimized) | 0.458 | 0.422 | 0.513 | 16,641 | 3.0 | 8,601ms | 4h44m |
| Auto pgvector (original) | 0.363 | 0.339 | 0.373 | 1,988 | 2.0 | 5,234ms | 2h53m |

> The optimized version's F1 improved from 0.363 to **0.458**
> (+26.2%), reaching **96.6%** of Long-Context F1 (up from 76.6%
> for original).

**Table 2: F1 by Category**

| Category | Count | Long-Context | optimized | original | improvement |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | 0.324 | **0.404** | 0.246 | +64.2% |
| multi-hop | 321 | 0.332 | **0.450** | 0.092 | +389.1% |
| temporal | 96 | 0.103 | **0.200** | 0.063 | +217.5% |
| open-domain | 841 | **0.521** | 0.439 | 0.324 | +35.5% |
| adversarial | 446 | 0.663 | 0.590 | **0.771** | -23.5% |

**Table 3: Weighted Average F1**

| Average | Long-Context | optimized | original |
| --- | ---: | ---: | ---: |
| 5-category weighted (÷1986) | **0.474** | 0.458 | 0.363 |
| 4-category weighted (÷1540, excl. adversarial) | **0.420** | **0.420** | 0.245 |

> The optimized version achieves improvements across all four
> knowledge categories. Multi-hop improved from 0.092 to 0.450
> (+389%), the most significant gain. Adversarial decreased
> (0.771 → 0.590) as the original had an overly aggressive
> refusal tendency.

**Table 4: Per-Sample F1**

| Sample | #QA | Long-Context | optimized | original |
| --- | ---: | ---: | ---: | ---: |
| locomo10_1 | 199 | 0.450 | **0.461** | 0.335 |
| locomo10_2 | 105 | **0.518** | 0.428 | 0.325 |
| locomo10_3 | 193 | **0.532** | 0.481 | 0.442 |
| locomo10_4 | 260 | **0.456** | 0.439 | 0.375 |
| locomo10_5 | 242 | 0.436 | **0.486** | 0.387 |
| locomo10_6 | 158 | **0.529** | 0.474 | 0.257 |
| locomo10_7 | 190 | **0.472** | 0.439 | 0.364 |
| locomo10_8 | 239 | 0.457 | **0.466** | 0.326 |
| locomo10_9 | 196 | 0.450 | **0.456** | 0.407 |
| locomo10_10 | 204 | **0.490** | 0.439 | 0.376 |
| **Average** | **199** | **0.474** | **0.458** | **0.363** |

> The optimized version improves on all 10 samples vs original, and
> surpasses Long-Context on 3 samples.

### 3.2 Memory vs Long-Context

Long-Context places the full transcript into a single LLM call.
It is effective but has fundamental limitations in production:

| Dimension | Long-Context | Memory (optimized) |
| --- | --- | --- |
| **Cross-session** | Cannot carry knowledge across sessions | Persistent memory survives restarts |
| **Context window** | Bounded by model limit (128K for GPT-4o-mini) | Unbounded — retrieves only relevant memories |
| **Scaling** | Cost grows linearly with conversation length | Cost stays near-constant (top-K retrieval) |
| **F1 quality** | 0.474 | **0.458** (achieves 96.6%) |
| **Adversarial robustness** | 0.663 | 0.590 |

---

### 3.3 SQLite vs SQLiteVec (Subset Run)

This subsection compares `sqlite` (keyword matching) and `sqlitevec`
(semantic vector search via sqlite-vec) on a few controlled subset runs.

**Subset run A: End-to-end QA (Auto / Full categories)**

This run keeps the same end-to-end pipeline and evaluation settings as the
main experiments, but limits to a single sample to control cost.

**Configuration**:

- Dataset: LoCoMo `locomo10.json`
- Sample: `locomo10_1` (199 QA, all categories)
- Scenario: `auto`
- Model: `gpt-4o-mini`
- LLM Judge: enabled
- Embedding model (SQLiteVec): `text-embedding-3-small`
- SQLiteVec retrieval top-k: 10 (default)

**End-to-end results: Overall Metrics and Token Usage (Auto / 199 QA)**

| Backend | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 199 | 0.327 | 0.301 | 0.370 | 1,287,813 | 5,624 | 1,293,437 | 398 | 5,805ms |
| SQLiteVec | 199 | 0.307 | 0.285 | 0.325 | 407,969 | 5,556 | 413,525 | 396 | 6,327ms |

**Interpretation (locomo10_1)**:

- **SQLiteVec reduces prompt tokens by ~3.2x** (bounded top-k retrieval),
  but **F1/BLEU/LLM Score are slightly lower** on this sample at the
  default top-k=10 setting.
- Category-level behavior differs: `sqlitevec` improves `adversarial`
  (more correct refusals), but underperforms on other categories when the
  needed evidence is not retrieved within top-k.

We also rerun the same configuration on another representative sample.

- Sample: `locomo10_6` (158 QA, all categories)

**End-to-end results: Overall Metrics and Token Usage (Auto / 158 QA)**

| Backend | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 158 | 0.269 | 0.243 | 0.289 | 1,296,580 | 5,103 | 1,301,683 | 340 | 6,359ms |
| SQLiteVec | 158 | 0.274 | 0.254 | 0.295 | 362,903 | 4,773 | 367,676 | 324 | 6,928ms |

**Overall takeaway (locomo10_1 + locomo10_6)**:

- SQLiteVec consistently reduces prompt tokens by ~3x-4x in our runs.
- Answer quality changes are sample-dependent at the default top-k=10;
  increasing top-k can improve recall but will also increase prompt tokens.

> Note: `Prompt Tokens`, `LLM Calls` count only the QA agent model calls.
> They exclude embedding requests and LLM-as-Judge calls. `Avg Latency`
> reflects end-to-end time averaged by #QA (including embeddings, judge,
> and auto extraction).

**Subset run B: Temporal-only token-cost micro-run**

**Configuration**:

- Dataset: LoCoMo `locomo10.json`
- Sample: `locomo10_1`
- Category filter: `temporal` (13 QA)
- Scenario: `auto`
- Model: `gpt-4o-mini`
- LLM Judge: disabled
- Embedding model (SQLiteVec): `text-embedding-3-small`

**Table 5: Overall Metrics and Token Usage (Auto / Temporal / 13 QA)**

| Backend | F1 | BLEU | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 0.116 | 0.082 | 80,184 | 352 | 80,536 | 26 | 12,352ms |
| SQLiteVec | 0.116 | 0.082 | 26,483 | 353 | 26,836 | 26 | 17,817ms |

**Subset run C: Vector top-k sweep + multi-search ablation (Auto / Full categories)**

**Table 6: Top-k and Multi-search Sweep (Auto / locomo10_1 / 199 QA)**

| Backend | vector-topk | qa-search-passes | F1 | BLEU | Prompt Tokens | Avg Prompt/QA | Avg Latency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | - | 1 | 0.299 | 0.283 | 1,322,360 | 6,645 | 3,316ms |
| SQLiteVec | 5 | 1 | 0.320 | 0.296 | 346,253 | 1,740 | 4,182ms |
| SQLiteVec | 10 | 1 | 0.343 | 0.315 | 398,751 | 2,004 | 4,352ms |
| SQLiteVec | 20 | 1 | 0.329 | 0.308 | 621,790 | 3,125 | 4,180ms |
| SQLiteVec | 40 | 1 | 0.327 | 0.303 | 965,423 | 4,851 | 4,460ms |
| SQLiteVec | 10 | 2 | 0.342 | 0.312 | 659,981 | 3,316 | 5,198ms |

**Interpretation**:

- **Increasing top-k does not monotonically improve quality**: top-k=20/40
  increases prompt tokens but slightly lowers F1/BLEU. The QA agent can
  be sensitive to noise in retrieved memories.
- `qa-search-passes=2` improves some categories (e.g. multi-hop) but does
  not improve overall F1, and increases both tokens and latency.

---

## 4. Comparison with Python Agent Frameworks

We ran the same LoCoMo benchmark on four Python agent frameworks —
**AutoGen**, **Agno**, **ADK**, **CrewAI** — all using GPT-4o-mini,
the same 10 samples (1,986 QA), and LLM-as-Judge evaluation.

### 4.1 Framework Configurations

| Framework | Memory Backend | Retrieval | Embedding |
| --- | --- | --- | --- |
| **trpc-agent-go** | pgvector | Vector similarity (top-K) + multi-pass | text-embedding-3-small |
| **AutoGen** | ChromaDB | Vector similarity (top-30) | text-embedding-3-small |
| **Agno** | SQLite | LLM fact extraction → system prompt | N/A |
| **ADK** | In-memory | Agent tool call (LoadMemoryTool) | Internal |
| **CrewAI** | Built-in vector | Auto-retrieve by Crew | Internal |

### 4.2 Overall Results

**Table 7: Memory Scenario — Overall Metrics**

| Framework | F1 | BLEU | LLM Score | Tokens/QA | Calls/QA | Latency | Total Time |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| **trpc-agent-go (optimized)** | **0.458** | **0.422** | 0.513 | 16,641 | 3.0 | 8,601ms | 4h44m |
| AutoGen | 0.457 | 0.414 | 0.540 | 1,943 | 1.0 | 3,816ms | 2h06m |
| CrewAI | 0.420 | 0.379 | 0.475 | ‡ | ‡ | 10,819ms | 5h58m |
| trpc-agent-go (original) | 0.363 | 0.339 | 0.373 | 1,988 | 2.0 | 5,234ms | 2h53m |
| ADK | 0.362 | 0.309 | 0.476 | 49,224 | 2.0 | 5,578ms | 3h04m |
| Agno | 0.332 | 0.289 | 0.494 | 10,436 | 1.0 | 14,127ms | 7h47m |

> **LLM Score aggregation note.** All frameworks now use the same
> all-sample denominator (accuracy-style: `sum(llm_score) / total_qa`).
> Python frameworks originally reported precision-style scores
> (~0.93) that excluded non-scored QAs from the denominator; those
> values have been recalculated here for fair cross-framework
> comparison.

> **‡ CrewAI token accounting bug.** CrewAI's `TokenProcess`
> counter does not expose per-QA token counts in the results JSON
> (all values are 0).

```
Memory F1 (10 samples, 1986 QA)

trpc-agent-go (opt) |==========================================| 0.458
AutoGen             |=========================================| 0.457
CrewAI              |=====================================     | 0.420
ADK                 |================================         | 0.362
trpc-agent-go (original)|=================================        | 0.363
Agno                |==============================           | 0.332
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

### 4.3 Category-Level F1

**Table 8: F1 by Category**

| Category | Count | trpc (opt) | AutoGen | CrewAI | trpc (original) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | **0.404** | 0.377 | 0.333 | 0.246 | 0.299 | 0.240 |
| multi-hop | 321 | 0.450 | **0.512** | 0.391 | 0.092 | 0.418 | 0.283 |
| temporal | 96 | **0.200** | 0.176 | 0.147 | 0.063 | 0.120 | 0.076 |
| open-domain | 841 | 0.439 | **0.594** | 0.496 | 0.324 | 0.494 | 0.292 |
| adversarial | 446 | 0.590 | 0.272 | 0.409 | **0.771** | 0.163 | 0.556 |

**Table 9: Weighted Average F1**

| Average | trpc (opt) | AutoGen | CrewAI | trpc (original) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 5-category weighted (÷1986) | **0.458** | 0.457 | 0.420 | 0.363 | 0.362 | 0.332 |
| 4-category weighted (÷1540) | 0.420 | **0.511** | 0.423 | 0.245 | 0.420 | 0.267 |

> 5-category weighted F1: optimized **0.458** ranks first,
> on par with AutoGen (0.457). 4-category weighted 0.420 is
> below AutoGen (0.511), with a gap of 0.091.

### 4.4 Token Efficiency and Latency

**Table 10: Token Efficiency Comparison**

| Framework | F1 | Total Tokens | Tokens/QA | F1/Million Tokens |
| --- | ---: | ---: | ---: | ---: |
| AutoGen | 0.457 | 3,859,412 | 1,943 | 118.4 |
| trpc-agent-go (optimized) | **0.458** | 33,049,494 | 16,641 | 13.9 |
| trpc-agent-go (original) | 0.363 | 3,948,128 | 1,988 | 91.9 |
| Agno | 0.332 | 20,725,728 | 10,436 | 16.0 |
| ADK | 0.362 | 97,759,453 | 49,224 | 3.7 |
| CrewAI | 0.420 | — | — | — |

> AutoGen has the best token efficiency (118.4 F1/million tokens),
> achieving 0.457 F1 with minimal token consumption. The optimized
> version trades more tokens (16,641/QA) for the highest F1
> (0.458). ADK has the worst efficiency — 49,224 tokens/QA for
> only 0.362 F1.

```
Total Evaluation Time (memory scenario, 1986 QA)

AutoGen         |====                                     | 2h06m
trpc (original) |======                                   | 2h53m
ADK             |======                                   | 3h04m
trpc (opt)      |==========                               | 4h44m
CrewAI          |============                             | 5h58m
Agno            |===============================          | 7h47m
                +------------------------------------------+
                0h       2h       4h       6h       8h
```

### 4.5 ADK Failure Analysis

ADK (Google Agent Development Kit) uses an in-memory backend with
agent tool calls (`LoadMemoryTool`) for memory retrieval. In this
evaluation, ADK encountered context overflow issues on some samples:

**Table 11: ADK Context Overflow Details**

| Sample | #QA | Empty Predictions | QA with >128K Tokens | Max Tokens |
| --- | ---: | ---: | ---: | ---: |
| conv-26 | 199 | 0 | 0 | 43,887 |
| conv-30 | 105 | 0 | 0 | 59,458 |
| conv-41 | 193 | 4 | 4 | 252,849 |
| conv-42 | 260 | 1 | 1 | 180,603 |
| conv-43 | 242 | 2 | 2 | 162,249 |
| conv-44 | 158 | 1 | 0 | 123,063 |
| conv-47 | 190 | 0 | 0 | 114,912 |
| conv-48 | 239 | 1 | 0 | 105,680 |
| conv-49 | 196 | 0 | 1 | 166,597 |
| conv-50 | 204 | 1 | 1 | 219,026 |
| **Total** | **1,986** | **10** | **9** | **252,849** |

- **10 QA (0.5%) returned empty predictions**, concentrated in
  samples with longer conversation histories
- **53 QA exceeded 100K tokens**, with the single highest reaching
  **252,849 tokens** — approaching GPT-4o-mini's 128K context
  window limit
- ADK's `LoadMemoryTool` loads **all memories** into context
  without selective retrieval, causing severe token waste on
  longer conversations
- Average 49,224 tokens/QA (highest among all frameworks) for
  only 0.362 F1

### 4.6 CrewAI Failure Analysis

CrewAI uses a short-term memory backend (`short_term_memory`) with
Crew's built-in vector retrieval mechanism.

- **397 QA (20.0%) returned "The information is not available
  in my memory"** — indicating some memories are lost after
  ingestion
- **Token tracking bug**: `TokenProcess` counter reports all zeros
  in the results JSON, making actual token consumption unmeasurable

| Category | "not available" Responses | Proportion |
| --- | ---: | ---: |
| adversarial | 182 | 40.8% |
| temporal | 35 | 36.5% |
| multi-hop | 58 | 18.1% |
| single-hop | 36 | 12.8% |
| open-domain | 86 | 10.2% |

### 4.7 Per-Sample F1

**Table 12: Per-Sample F1 Comparison**

| Sample | #QA | trpc (opt) | AutoGen | CrewAI | trpc (original) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| conv-26 | 199 | **0.461** | 0.384 | 0.336 | 0.335 | 0.337 | 0.296 |
| conv-30 | 105 | 0.428 | **0.451** | 0.399 | 0.325 | 0.379 | 0.334 |
| conv-41 | 193 | 0.481 | **0.513** | 0.435 | 0.442 | 0.335 | 0.387 |
| conv-42 | 260 | **0.439** | 0.409 | 0.416 | 0.375 | 0.343 | 0.338 |
| conv-43 | 242 | **0.486** | 0.486 | 0.417 | 0.387 | 0.355 | 0.341 |
| conv-44 | 158 | 0.474 | **0.491** | 0.475 | 0.257 | 0.384 | 0.289 |
| conv-47 | 190 | 0.439 | **0.496** | 0.435 | 0.364 | 0.374 | 0.321 |
| conv-48 | 239 | **0.466** | 0.463 | 0.399 | 0.326 | 0.392 | 0.328 |
| conv-49 | 196 | **0.456** | 0.418 | 0.412 | 0.407 | 0.371 | 0.302 |
| conv-50 | 204 | 0.439 | **0.475** | 0.479 | 0.376 | 0.363 | 0.374 |
| **Average** | **199** | **0.458** | 0.457 | 0.420 | 0.363 | 0.362 | 0.332 |

> The optimized version beats AutoGen on 5 out of 10 samples.

---

## 5. Comparison with External Memory Systems

Source: Mem0 Table 1 (Chhikara et al., 2025, arXiv:2504.19413).
All systems use GPT-4o-mini. Adversarial category excluded for
cross-system comparability (Mem0 paper does not include it).

**Table 13: F1 by Category (Excluding Adversarial)**

| Method | Single-Hop | Multi-Hop | Open-Domain | Temporal | 4-cat Weighted | Source |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| AutoGen | 0.377 | **0.512** | **0.594** | 0.176 | **0.511** | This work |
| Mem0g | 0.381 | 0.243 | 0.493 | **0.516** | 0.422 | Mem0 paper |
| CrewAI | 0.333 | 0.391 | 0.496 | 0.147 | 0.423 | This work |
| Mem0 | 0.387 | 0.286 | 0.477 | 0.489 | 0.421 | Mem0 paper |
| **trpc-agent (optimized)** | **0.404** | 0.450 | 0.439 | 0.200 | 0.420 | This work |
| trpc-agent (LC) | 0.324 | 0.332 | 0.521 | 0.103 | 0.420 | This work |
| ADK | 0.299 | 0.418 | 0.494 | 0.120 | 0.420 | This work |
| Zep | 0.357 | 0.194 | 0.496 | 0.420 | 0.403 | Mem0 paper |
| LangMem | 0.355 | 0.260 | 0.409 | 0.308 | 0.362 | Mem0 paper |
| A-Mem | 0.270 | 0.121 | 0.447 | 0.459 | 0.347 | Mem0 paper |
| OpenAI Memory | 0.343 | 0.201 | 0.393 | 0.140 | 0.328 | Mem0 paper |
| MemGPT | 0.267 | 0.092 | 0.410 | 0.255 | 0.308 | Mem0 paper |
| LoCoMo | 0.250 | 0.120 | 0.404 | 0.184 | 0.303 | Mem0 paper |
| trpc-agent (original) | 0.246 | 0.092 | 0.324 | 0.063 | 0.245 | This work |
| Agno | 0.240 | 0.283 | 0.292 | 0.076 | 0.267 | This work |
| ReadAgent | 0.092 | 0.053 | 0.097 | 0.126 | 0.089 | Mem0 paper |
| MemoryBank | 0.050 | 0.056 | 0.066 | 0.097 | 0.063 | Mem0 paper |

```
4-Category Weighted F1 (excluding adversarial, 1540 QA)

AutoGen             |==========================================| 0.511
Mem0g               |==================================        | 0.422
CrewAI              |==================================        | 0.423
Mem0                |==================================        | 0.421
trpc-agent (opt)    |=================================         | 0.420
trpc-agent (LC)     |=================================         | 0.420
ADK                 |=================================         | 0.420
Zep                 |================================          | 0.403
LangMem             |=============================             | 0.362
A-Mem               |===========================               | 0.347
OpenAI Memory       |==========================                | 0.328
MemGPT              |========================                  | 0.308
LoCoMo              |========================                  | 0.303
trpc-agent (original)|==================                      | 0.245
Agno                |====================                      | 0.267
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

> **5-category weighted F1** (for frameworks with adversarial data):
>
> | Method | 5-cat Weighted F1 |
> | --- | ---: |
> | **trpc-agent (optimized)** | **0.458** |
> | AutoGen | 0.457 |
> | CrewAI | 0.420 |
> | trpc-agent (original) | 0.363 |
> | ADK | 0.362 |
> | Agno | 0.332 |

**Key takeaways:**

1. **trpc-agent (optimized)** achieves a 4-category weighted F1 of
   **0.420**, surpassing Zep (0.403), LangMem (0.362), A-Mem (0.347),
   and other dedicated memory systems. On par with Mem0 (0.421) and
   Mem0g (0.422).
2. **Single-hop ranks #1** (0.404) across all frameworks and memory
   systems.
3. **Multi-hop ranks #3** (0.450), behind AutoGen (0.512) and
   ADK (0.418), far ahead of Mem0 (0.286).
4. **Temporal reasoning** (0.200) remains the primary gap — Mem0/Mem0g
   reach 0.489/0.516 in this category. This is the next optimization
   target.
5. Compared to the original, the optimized version rose from near-bottom to
   **on par with Mem0** (0.245 → 0.420, a 71.4% improvement).

---

## 6. Conclusion

### Key Findings

1. **Memory is essential for production agents.** Long-Context works
   well for single sessions under the context window limit, but
   cannot persist knowledge across sessions or scale beyond the
   model's context window. Memory provides persistent, cross-session
   knowledge.

2. **Optimized Auto pgvector reaches 96.6% of Long-Context.** F1
   improved from original's 0.363 to **0.458**, approaching Long-Context's
   0.474. All four knowledge categories show improvements, with
   multi-hop improving from 0.092 to 0.450 (+389%).

3. **trpc-agent-go (optimized) ranks #1 in 5-category weighted F1**
   (0.458), on par with AutoGen (0.457). In 4-category weighted F1,
   it achieves 0.420, on par with Mem0/Mem0g (0.421-0.422).

4. **The gap with AutoGen has narrowed dramatically:**

   | Metric | original vs AutoGen | optimized vs AutoGen |
   | --- | --- | --- |
   | F1 gap | 0.363 vs 0.457 (-20.6%) | 0.458 vs 0.457 (**+0.2%**) |
   | multi-hop | 0.092 vs 0.512 (-82.0%) | 0.450 vs 0.512 (-12.1%) |
   | single-hop | 0.246 vs 0.377 (-34.7%) | 0.404 vs 0.377 (**+7.2%**) |
   | temporal | 0.063 vs 0.176 (-64.2%) | 0.200 vs 0.176 (**+13.6%**) |

5. **CrewAI performed better than expected.** In this evaluation round,
   CrewAI achieved F1 of 0.420 (4-category weighted 0.423), on par
   with Mem0. However, 20.0% of QA still returned "memory not
   available" responses, and the token tracking bug remains unfixed.

6. **ADK's token consumption remains the highest.** At 49,224 tokens/QA
   (highest among all frameworks) for only 0.362 F1, ADK's
   `LoadMemoryTool` architecture of loading all memories into context
   continues to be problematic.

7. **Temporal reasoning is the primary improvement area.** The
   optimized temporal improved from 0.063 to 0.200, but a significant
   gap remains vs Mem0 (0.489). Temporal indexing and time-aware
   retrieval are the next focus areas.

### Production Recommendations

| Use Case | Recommended Approach |
| --- | --- |
| Short single-session (< 50K tokens) | Long-context (no memory needed) |
| Long-running agents (weeks/months) | Auto extraction + pgvector (optimized) |
| History exceeding context window | Memory (only viable option) |

---

## Appendix

### A. Experimental Environment

| Component | Version/Config |
| --- | --- |
| Framework | trpc-agent-go |
| Model | gpt-4o-mini |
| Embedding | text-embedding-3-small |
| PostgreSQL | 15+ with pgvector extension |
| Dataset | LoCoMo-10 (10 samples, 1,986 QA) |

### B. Full Category Breakdown (F1 / BLEU / LLM)

| Scenario | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Long-Context | 0.324/0.252/0.330 | 0.332/0.296/0.264 | 0.103/0.080/0.177 | 0.521/0.460/0.661 | 0.663/0.662/0.663 |
| Auto pgvec (optimized) | 0.404/0.335/0.358 | 0.450/0.412/0.484 | 0.200/0.158/0.334 | 0.439/0.396/0.555 | 0.590/0.590/0.590 |
| Auto pgvec (original) | 0.246/0.183/0.209 | 0.092/0.085/0.051 | 0.063/0.046/0.068 | 0.324/0.293/0.376 | 0.771/0.771/0.770 |

### C. Token Usage — Full Breakdown

| Scenario | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | Calls/QA |
| --- | ---: | ---: | ---: | ---: | ---: |
| Long-Context | 37,272,167 | 15,997 | 37,288,164 | 1,986 | 1.0 |
| Auto pgvec (optimized) | 32,933,287 | 116,207 | 33,049,494 | 5,998 | 3.0 |
| Auto pgvec (original) | 3,890,627 | 57,501 | 3,948,128 | 4,000 | 2.0 |
| AutoGen | 3,842,576 | 16,836 | 3,859,412 | 1,986 | 1.0 |
| Agno | 20,694,534 | 31,194 | 20,725,728 | 1,986 | 1.0 |
| ADK | 97,691,620 | 67,833 | 97,759,453 | 4,028 | 2.0 |

---

## References

1. Maharana, A., Lee, D., Tulyakov, S., Bansal, M., Barbieri, F., and Fang, Y. "Evaluating Very Long-Term Conversational Memory of LLM Agents." arXiv:2402.17753, 2024.
2. Chhikara, P., Khant, D., Aryan, S., Singh, T., and Yadav, D. "Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory." arXiv:2504.19413, 2025.
3. Hu, C., et al. "Memory in the Age of AI Agents." arXiv:2512.13564, 2024.
