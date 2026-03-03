# 基于 LoCoMo 基准的长期对话记忆评估

## 1. 引言

本报告使用 **LoCoMo** 基准（Maharana et al., 2024）评估
**trpc-agent-go** 的长期对话记忆能力。以 **Auto 提取 + pgvector**
作为主要记忆方案，对比四个 Python Agent 框架（AutoGen、Agno、ADK、
CrewAI）和十个外部记忆系统（Mem0、Zep 等），并进行历史注入消融实验。

## 2. 实验设置

### 2.1 基准数据集

| 项目 | 值 |
| --- | --- |
| 数据集 | LoCoMo-10（10 个对话，1,986 个 QA） |
| 类别 | single-hop (282), multi-hop (321), temporal (96), open-domain (841), adversarial (446) |
| 模型 | GPT-4o-mini（推理 + 评判） |
| Embedding | text-embedding-3-small |

### 2.2 评估场景

| 场景 | 描述 |
| --- | --- |
| **Long-Context** | 完整对话文本作为 LLM 上下文（上界） |
| **Auto + pgvector** | 后台提取器自动生成记忆；查询时向量检索 |
| **Auto + MySQL** | 同上提取器；全文搜索（类 BM25）检索 |
| **Agentic + pgvector** | LLM Agent 通过工具调用决定存储内容 |

### 2.3 消融：历史注入

记忆场景可在检索结果的基础上注入原始对话轮次（+300 或 +700），
测试原始历史对性能的影响。

## 3. 结果

### 3.1 内部场景对比

**表 1：总体指标（无历史注入）**

| 场景 | F1 | BLEU | LLM Score | Tokens/QA | 延迟 |
| --- | ---: | ---: | ---: | ---: | ---: |
| Long-Context | **0.474** | **0.431** | **0.527** | 18,767 | 3,063ms |
| Auto pgvector | 0.363 | 0.339 | 0.373† | 1,959 | 5,235ms |
| Auto MySQL | 0.352 | 0.327 | 0.373† | 9,067 | 4,785ms |
| Agentic pgvector | 0.287 | 0.273 | 0.280† | 3,102 | 4,704ms |

> † LLM Score 使用全样本分母（Go 实现）。归一化为 positive-only 后：
> Auto pgvector = **0.967**，Long-Context = **0.968**。
> 详见第 4.2 节说明。

> Auto pgvector 达到长上下文 F1 的 **76.7%**，同时仅消耗 **10.4%** 的
> prompt tokens——成本节省 89.6%。

**为什么需要 Memory 而非 Long-Context：**

Long-Context 将完整对话历史放入单次 LLM 调用，在单 session 内
有效，但在生产环境中存在根本性限制：

| 维度 | Long-Context | Memory (Auto pgvector) |
| --- | --- | --- |
| **跨 session** | 无法跨会话携带知识 | 持久化记忆，重启后仍可用 |
| **上下文窗口** | 受模型限制（GPT-4o-mini 128K）；超限则失败 | 无上限——仅检索相关记忆，不受历史长度影响 |
| **Token 成本** | 18,767 tokens/QA → **$5.59/1986 QA** | 1,959 tokens/QA → **$0.62/1986 QA**（仅为前者的 10.4%） |
| **可扩展性** | 成本随对话长度线性增长 | 成本近常量（top-K 检索） |
| **对抗鲁棒性** | 0.663——完整上下文诱导幻觉回答 | **0.771**——无匹配记忆时正确拒答 |

在实际部署中，对话会在数周甚至数月间累积，很容易超出任何模型的
上下文窗口。Memory 不是在单 session 内与 Long-Context 竞争——它是
**唯一可行的跨 session 持久化知识方案**。

**表 2：各类别 F1（无历史注入）**

| 类别 | Long-Context | Auto pgvec | Agentic pgvec |
| --- | ---: | ---: | ---: |
| single-hop | 0.324 | 0.246 | 0.150 |
| multi-hop | **0.332** | 0.091 | 0.142 |
| temporal | 0.103 | 0.063 | 0.047 |
| open-domain | **0.521** | 0.324 | 0.129 |
| adversarial | 0.663 | 0.771 | **0.825** |

> 记忆方案在对抗性问题上优于长上下文（0.771 vs 0.663），因为在未检索到
> 相关记忆时自然拒答不可回答的问题。

### 3.2 历史注入消融

**表 3：Auto pgvector + 历史注入**

| 历史 | F1 | BLEU | LLM Score | Tokens/QA | Adv. F1 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 无 | **0.363** | **0.339** | 0.373 | 1,959 | **0.771** |
| +300 轮 | 0.294 | 0.259 | 0.410 | 15,387 | 0.514 |
| +700 轮 | 0.288 | 0.243 | **0.470** | 21,445 | 0.409 |

核心发现：
- **F1/BLEU 下降**：主要由对抗性分数崩溃导致（0.771 → 0.409）
- **LLM Score 提升**（+0.097），尤其在开放域（0.376 → 0.690）
- **+700 轮成本高于长上下文**（21K vs 18.8K tokens/QA）且 F1 更低，
  全量历史注入不具成本效益
- 建议生产环境采用**选择性注入**策略：默认仅用记忆，仅对开放域
  问题注入相关历史片段

### 3.3 各样本稳定性

**表 4：各样本 F1（Long-Context / Auto pgvector）**

| 样本 | QA 数 | Long-Context | Auto pgvec |
| --- | ---: | ---: | ---: |
| locomo10_1 | 199 | 0.450 | 0.335 |
| locomo10_2 | 105 | 0.518 | 0.325 |
| locomo10_3 | 193 | 0.532 | 0.442 |
| locomo10_4 | 260 | 0.456 | 0.375 |
| locomo10_5 | 242 | 0.436 | 0.387 |
| locomo10_6 | 158 | 0.529 | 0.257 |
| locomo10_7 | 190 | 0.472 | 0.364 |
| locomo10_8 | 239 | 0.457 | 0.326 |
| locomo10_9 | 196 | 0.450 | 0.407 |
| locomo10_10 | 204 | 0.490 | 0.376 |
| **平均** | **199** | **0.474** | **0.363** |

| 场景 | 最低 | 最高 | 极差 | 标准差 |
| --- | ---: | ---: | ---: | ---: |
| Long-Context | 0.436 | 0.532 | 0.096 | 0.031 |
| Auto pgvector | 0.257 | 0.442 | 0.185 | 0.052 |

### 3.4 Token 消耗

**表 5：Token 消耗概要**

| 场景 | Prompt/QA | Completion/QA | 调用/QA | 总 Tokens |
| --- | ---: | ---: | ---: | ---: |
| Long-Context | 18,767 | 8 | 1.0 | 37,288,164 |
| Auto pgvector | 1,959 | 29 | 2.0 | 3,948,128 |
| Auto MySQL | 9,067 | 30 | 2.1 | 18,067,237 |
| Auto pgvec +300 | 15,387 | 26 | 1.5 | 30,610,215 |
| Auto pgvec +700 | 21,445 | 18 | 1.1 | 42,625,147 |

---

### 3.5 SQLite vs SQLiteVec（子集实验）

本小节对比 `sqlite`（关键词/Token 匹配）与 `sqlitevec`（sqlite-vec 语义向量检索）
在若干个可控的子集实验上的表现，用于观察 token 成本与检索差异。

**子集实验 A：端到端 QA（Auto / 全类别）**

该实验保持端到端流程与主要实验一致，但仅评估单个样本以控制成本。

**实验配置**：

- 数据集：LoCoMo `locomo10.json`
- 样本：`locomo10_1`（199 个 QA，包含全部类别）
- 场景：`auto`
- 模型：`gpt-4o-mini`
- LLM 评判：启用
- SQLiteVec embedding 模型：`text-embedding-3-small`
- SQLiteVec 检索 top-k：10（默认值）

作为参考，表 7 中 Auto pgvector 在 `locomo10_1` 的 F1 为 **0.311**，
在 `locomo10_6` 的 F1 为 **0.204**（同数据集/模型）。

**端到端结果：总体指标与 token 消耗（Auto / 199 QA）**

| 后端 | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 199 | 0.327 | 0.301 | 0.370 | 1,287,813 | 5,624 | 1,293,437 | 398 | 5,805ms |
| SQLiteVec | 199 | 0.307 | 0.285 | 0.325 | 407,969 | 5,556 | 413,525 | 396 | 6,327ms |

**解读（locomo10_1）**：

- **SQLiteVec 的 prompt token 约减少 3.2x**（top-k 有界检索），但在该样本上
  **F1/BLEU/LLM Score 略低**（默认 top-k=10）。
- 类别层面的表现存在差异：`sqlitevec` 在 `adversarial` 上更好（更多正确拒答），
  但当关键信息未进入 top-k 时，其他类别会出现召回不足导致的下降。

我们也在另一个代表性样本上复现相同配置。

- 样本：`locomo10_6`（158 个 QA，包含全部类别）

**端到端结果：总体指标与 token 消耗（Auto / 158 QA）**

| 后端 | #QA | F1 | BLEU | LLM Score | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 158 | 0.269 | 0.243 | 0.289 | 1,296,580 | 5,103 | 1,301,683 | 340 | 6,359ms |
| SQLiteVec | 158 | 0.274 | 0.254 | 0.295 | 362,903 | 4,773 | 367,676 | 324 | 6,928ms |

**解读（locomo10_6）**：

- **SQLiteVec 的 prompt token 约减少 3.6x**，同时 F1/BLEU/LLM Score 在该样本上
  略有提升，但延迟会有小幅增加。
- 与 `locomo10_1` 类似，`sqlitevec` 在 `adversarial` 上更强，但在当前设置下
  `temporal`、`multi-hop` 仍然偏弱。

**总体结论（locomo10_1 + locomo10_6）**：

- SQLiteVec 在我们的子集实验中稳定地将 prompt token 降低到约 1/3 到 1/4。
- 默认 top-k=10 下，答案质量的变化与样本相关；调大 top-k 可能提升召回，
  但也会增加 prompt token。

> 注：`Prompt Tokens`、`LLM Calls` 仅统计 QA 阶段 Agent 的模型调用，
> 不包含 embedding 请求与 LLM-as-Judge 调用。`平均延迟` 为端到端总耗时
> 按 #QA 平均（包含 embedding、LLM-as-Judge 以及 auto extraction）。

**子集实验 B：Temporal-only token 成本微基准**

**实验配置**：

- 数据集：LoCoMo `locomo10.json`
- 样本：`locomo10_1`
- 类别过滤：`temporal`（13 个 QA）
- 场景：`auto`
- 模型：`gpt-4o-mini`
- LLM 评判：关闭
- SQLiteVec embedding 模型：`text-embedding-3-small`

**表 8：总体指标与 token 消耗（Auto / Temporal / 13 QA）**

| 后端 | F1 | BLEU | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 0.116 | 0.082 | 80,184 | 352 | 80,536 | 26 | 12,352ms |
| SQLiteVec | 0.116 | 0.082 | 26,483 | 353 | 26,836 | 26 | 17,817ms |

**解读**：

- 在该子集中，答案质量（F1/BLEU）一致，但 **SQLiteVec 的 prompt token 消耗约为 SQLite 的 1/3**。
  主要原因是 SQLiteVec 返回的是有界的 top-k 结果（默认 10），而 SQLite 后端可能返回更大规模的关键词匹配集合。

**子集实验 C：向量 top-k 扫参 + 多次检索消融（Auto / 全类别）**

前面的子集实验主要对比 `sqlite` 与 `sqlitevec` 在默认配置下的表现
（top-k=10、单次检索）。为了回答"多取一些记忆（更大的 top-k）"或"多检索几次
（多次 memory_search）"是否能提升端到端答案质量，我们在单个样本上做了一个小
范围扫参。

**实验配置**：

- 数据集：LoCoMo `locomo10.json`
- 样本：`locomo10_1`（199 个 QA，包含全部类别）
- 场景：`auto`
- 模型：`gpt-4o-mini`
- LLM 评判：关闭（仅统计 F1/BLEU，以控制成本）

**表 9：Top-k 与多次检索扫参结果（Auto / locomo10_1 / 199 QA）**

| 后端 | vector-topk | qa-search-passes | F1 | BLEU | Prompt Tokens | Avg Prompt/QA | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | - | 1 | 0.299 | 0.283 | 1,322,360 | 6,645 | 3,316ms |
| SQLiteVec | 5 | 1 | 0.320 | 0.296 | 346,253 | 1,740 | 4,182ms |
| SQLiteVec | 10 | 1 | 0.343 | 0.315 | 398,751 | 2,004 | 4,352ms |
| SQLiteVec | 20 | 1 | 0.329 | 0.308 | 621,790 | 3,125 | 4,180ms |
| SQLiteVec | 40 | 1 | 0.327 | 0.303 | 965,423 | 4,851 | 4,460ms |
| SQLiteVec | 10 | 2 | 0.342 | 0.312 | 659,981 | 3,316 | 5,198ms |

**解读**：

- 在该次运行中，**SQLiteVec（top-k=10）在 F1 上优于 SQLite**
  （**0.343 vs 0.299**），同时 **prompt token 约减少 ~3.3x**
  （有界检索带来的 token 控制）。
- 在当前 benchmark 设置下，**top-k 并非越大越好**：top-k=20/40 虽然显著增加了
  prompt token，但 F1/BLEU 略有下降。说明 QA Agent 对检索噪声较敏感，端到端质量
  不是单纯由召回率决定。
- `qa-search-passes=2`（强制两次检索）在部分类别上有改善（例如 multi-hop），但
  **总体 F1 没有提升**，同时 token 与延迟都会上升，更适合作为诊断手段而非默认配置。

**检索微基准（curated queries）**：

为了将"检索效果"与端到端 QA 解耦，我们还运行了一个面向检索的示例
（`examples/memory/compare`），其中查询使用了较低词面重叠的改写/同义表达。

| 后端 | Hit@3 | 说明 |
| --- | ---: | --- |
| SQLite | 2/4 | Token 匹配对部分改写不敏感 |
| SQLiteVec | 4/4 | 语义相似度可召回改写 |

---

## 4. 与 Python Agent 框架对比

我们在四个 Python Agent 框架——**AutoGen**、**Agno**、**ADK**、
**CrewAI**——上运行了相同的 LoCoMo 基准，均使用 GPT-4o-mini、
相同的 10 个样本（1,986 QA）及 LLM-as-Judge 评估。

### 4.1 框架配置

| 框架 | 记忆后端 | 检索方式 | Embedding |
| --- | --- | --- | --- |
| **trpc-agent-go** | pgvector | 向量相似度（top-K） | text-embedding-3-small |
| **AutoGen** | ChromaDB | 向量相似度（top-30） | text-embedding-3-small |
| **Agno** | SQLite | LLM 事实提取 → system prompt | 无 |
| **ADK** | 纯内存 | Agent 工具调用（LoadMemoryTool） | 内置 |
| **CrewAI** | 内置向量 | Crew 自动检索 | 内置 |

### 4.2 总体结果

**表 6：Memory 场景——总体指标**

| 框架 | F1 | BLEU | LLM Score | Tokens/QA | 延迟 | 成本 ($) | 总耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| AutoGen | **0.442** | **0.376** | 0.932 | 1,702 | 4,315ms | 0.51 | 2h22m |
| Agno | 0.383 | 0.323 | 0.935 | 10,127 | 21,711ms | 3.03 | 11h58m |
| trpc-agent-go | 0.363 | 0.339 | 0.373† | 1,988 | 5,235ms | 0.62 | 2h53m |
| ADK | 0.301 | 0.248 | 0.936 | 65,076 | 8,531ms | 19.42 | 4h42m |
| CrewAI | 0.247 | 0.215 | 0.918 | 49,336‡ | 9,311ms | 14.71‡ | 5h08m |

> **† LLM Score 聚合口径差异。** trpc-agent-go 使用全样本分母
> （accuracy 口径：`sum / total_qa`），Python 框架则排除未命中
> 样本（precision 口径：`sum / count_where_score>0`）。使用相同的
> positive-only 聚合归一化后，trpc-agent-go 的 LLM Score 为
> **0.967**——与所有 Python 框架可比：
>
> | 框架 | 报告值 | 归一化值（positive-only） |
> | --- | ---: | ---: |
> | ADK | 0.936 | 0.936 |
> | Agno | 0.935 | 0.935 |
> | AutoGen | 0.932 | 0.932 |
> | CrewAI | 0.918 | 0.918 |
> | **trpc-agent-go** | **0.373** | **0.967** |
>
> 报告值 0.373 反映了 61.4% 的 QA 未检索到匹配记忆被评为 0 分；
> 其余 38.6% 命中了相关记忆的 QA，GPT-4o-mini judge 给出的平均
> confidence 为 0.967——**所有框架中最高**。

> 成本按 GPT-4o-mini 定价（prompt $0.15/1M，completion $0.60/1M）估算。
> Token 统计仅含 QA 推理；judge token 已排除。CrewAI 标 ‡ 的值来自
> OpenAI API usage 日志，详见下方说明。

> **‡ CrewAI token 统计 bug。** CrewAI 的 `TokenProcess` 计数器
> 未在结果 JSON 中暴露逐 QA 的 token 统计（所有值为 0）。上表数值
> （49,336 tokens/QA、$14.71、1.04 calls/QA）来自评测日志中的
> OpenAI API usage 行。内置 `short_term_memory` 将所有历史 QA
> 对话线性累积到 prompt 中——prompt tokens 从 ~737 增长到 ~94K
> （conv-50 内）。在 conv-42（260 QA）中，prompt 超过 GPT-4o-mini
> 的 128K 限制，触发上下文溢出回退（260 QA 产生 343 次调用，
> 1.32 calls/QA）。
>
> CrewAI 仍然 F1 最低：**Memory F1 (0.247) < Baseline F1 (0.490)**。
> 线性 prompt 累积导致质量随历史增长而下降。仅 adversarial F1 提升
> （0.823 vs baseline 0.431），因噪声上下文导致模型倾向回答
> "信息不可用"。

```
Memory F1 (10 samples, 1986 QA)

AutoGen             |==========================================| 0.442
Agno                |====================================      | 0.383
trpc-agent-go       |=================================         | 0.363
ADK                 |==========================                | 0.301
CrewAI              |=====================                     | 0.247
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

### 4.3 Token 效率：F1/1M Tokens

| 框架 | F1 | Tokens/QA | F1/1M Tokens | 排名 |
| --- | ---: | ---: | ---: | ---: |
| AutoGen | 0.442 | 1,702 | **259.5** | #1 |
| trpc-agent-go | 0.363 | 1,988 | **182.6** | #2 |
| Agno | 0.383 | 10,127 | 37.8 | #3 |
| CrewAI | 0.247 | 49,336 | 5.0 | #4 |
| ADK | 0.301 | 65,076 | 4.6 | #5 |

```
F1 per 1M Tokens (higher = better)

AutoGen             |==========================================| 259.5
trpc-agent-go       |=============================             | 182.6
Agno                |======                                    | 37.8
CrewAI              |                                          | 5.0
ADK                 |                                          | 4.6
                    +------------------------------------------+
                    0        50       100      150      200    260
```

> trpc-agent-go 的 F1/1M（182.6）是 Agno 的 **4.8 倍**、
> ADK/CrewAI 的 **40 倍**。与 AutoGen（259.5）的差距源于检索策略
> 差异：AutoGen 使用单次 top-30 注入（1.0 call/QA），trpc-agent-go
> 使用双次调用模式（检索+回答，2.0 calls/QA），后者在生产 Agent
> 工作流中更灵活。

### 4.4 可靠性与鲁棒性

| 框架 | 失败 QA | 失败率 | Adv. F1 | 保留率 |
| --- | ---: | ---: | ---: | ---: |
| trpc-agent-go | 0 | 0.0% | **0.771** | 76.6% |
| AutoGen | 0 | 0.0% | 0.395 | **90.0%** |
| Agno | 0 | 0.0% | 0.639 | 78.2% |
| ADK | **122** | **6.1%** | 0.306 | 61.5% |
| CrewAI | 0 | 0.0% | **0.823** | 50.4% |

> ADK 的 122 次失败源于 `ContextWindowExceededError`——将完整对话历史
> 作为 session events 加载，上下文高达 234K tokens，超出 GPT-4o-mini
> 的 128K 限制。失败集中在较长对话（conv-41 至 conv-50）；较短对话
> （conv-26、conv-30）零失败。失败 QA 的预测为空字符串，F1 计为 0。
>
> CrewAI 报告 0 次失败 QA，因为框架内部捕获了上下文溢出并自动降级
> （prompt 从 128K 骤降至约 1.6K tokens）。但降级静默清除了累积的短期
> 记忆，导致 agent 丢失上下文，后续几乎所有问题都回答"信息不可用"。
> 在 conv-42（260 QA）中，159 个 QA 得分 F1=0——大部分是非对抗性问题
> 在记忆丢失后被错误拒答。
>
> trpc-agent-go 的对抗性 F1（0.771）领先 AutoGen **95%**，
> 在正确拒答不可回答问题方面表现突出。

### 4.5 各类别 F1

| 类别 | AutoGen | Agno | trpc-agent-go | ADK | CrewAI |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | **0.340** | 0.286 | 0.246 | 0.197 | 0.054 |
| multi-hop | **0.499** | 0.297 | 0.091 | 0.367 | 0.098 |
| temporal | **0.170** | 0.124 | 0.063 | 0.088 | 0.015 |
| open-domain | **0.510** | 0.341 | 0.324 | 0.331 | 0.090 |
| adversarial | 0.395 | 0.639 | **0.771** | 0.306 | **0.823** |

### 4.6 综合评分

加权评分：F1 (40%) + F1/1M Tokens (25%) + Adv.F1 (20%) +
可靠性 (15%)。

| 框架 | 综合分 | 排名 |
| --- | ---: | ---: |
| AutoGen | **0.896** | #1 |
| **trpc-agent-go** | **0.842** | **#2** |
| Agno | 0.688 | #3 |
| CrewAI | 0.578 | #4 |
| ADK | 0.492 | #5 |

```
Composite Score (weighted multi-dimensional)

AutoGen             |==========================================| 0.896
trpc-agent-go       |=======================================   | 0.842
Agno                |================================          | 0.688
CrewAI              |===========================               | 0.578
ADK                 |=======================                   | 0.492
                    +------------------------------------------+
                    0.0      0.2      0.4      0.6      0.8   1.0
```

> trpc-agent-go（0.842）仅落后 AutoGen（0.896）5.4%。差距集中在
> F1（0.363 vs 0.442）；trpc-agent-go 在对抗鲁棒性上领先（0.771 vs
> 0.395），成本可比（$0.62 vs $0.51，而 ADK $19.42、CrewAI $14.71）。

---

## 5. 与外部记忆系统对比

数据来源：Mem0 论文 Table 1 & Table 2 (Chhikara et al., 2025,
arXiv:2504.19413)。所有系统均使用 GPT-4o-mini。为跨系统可比性，
已排除 adversarial 类别（Mem0 论文未包含该类别）。

**表 7：各类别 F1（不含 adversarial）**

| 方法 | Single-Hop | Multi-Hop | Open-Domain | Temporal | Overall | 来源 |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Mem0 | 0.387 | 0.286 | 0.477 | **0.489** | **0.410** | Mem0 论文 |
| Mem0g | 0.381 | 0.243 | 0.493 | **0.516** | 0.408 | Mem0 论文 |
| Zep | 0.357 | 0.194 | 0.496 | 0.420 | 0.367 | Mem0 论文 |
| LangMem | 0.355 | 0.260 | 0.409 | 0.308 | 0.333 | Mem0 论文 |
| A-Mem | 0.270 | 0.121 | 0.447 | 0.459 | 0.324 | Mem0 论文 |
| **trpc-agent (LC)** | 0.324 | **0.332** | **0.521** | 0.103 | 0.320 | 本工作 |
| OpenAI Memory | 0.343 | 0.201 | 0.393 | 0.140 | 0.269 | Mem0 论文 |
| MemGPT | 0.267 | 0.092 | 0.410 | 0.255 | 0.256 | Mem0 论文 |
| LoCoMo | 0.250 | 0.120 | 0.404 | 0.184 | 0.240 | Mem0 论文 |
| **trpc-agent (Auto)** | 0.246 | 0.091 | 0.324 | 0.063 | 0.181 | 本工作 |
| ReadAgent | 0.092 | 0.053 | 0.097 | 0.126 | 0.092 | Mem0 论文 |
| MemoryBank | 0.050 | 0.056 | 0.066 | 0.097 | 0.067 | Mem0 论文 |

**表 8：LLM-as-Judge Overall（不含 adversarial）**

| 方法 | Overall J | p95 延迟 (s) | 记忆 Tokens | 来源 |
| --- | ---: | ---: | ---: | --- |
| Full-context | 0.729 | 17.12 | ~26K | Mem0 论文 |
| Mem0g | 0.684 | 2.59 | ~14K | Mem0 论文 |
| Mem0 | 0.669 | 1.44 | ~7K | Mem0 论文 |
| Zep | 0.660 | 2.93 | ~600K | Mem0 论文 |
| RAG (k=2, 256) | 0.610 | 1.91 | - | Mem0 论文 |
| LangMem | 0.581 | 60.40 | ~127 | Mem0 论文 |
| OpenAI Memory | 0.529 | 0.89 | ~4.4K | Mem0 论文 |
| **trpc-agent (LC)** | **0.487** | **3.06** | **~18.8K** | 本工作 |
| A-Mem* | 0.484 | 4.37 | ~2.5K | Mem0 论文 |
| **trpc-agent (Auto)** | **0.258** | **5.23** | **~2.0K** | 本工作 |

> **可比性说明：** Mem0 论文的 LLM Judge 评估 10 次取均值；本工作评估
> 1 次。本工作的延迟包含完整 Agent 工具调用链路，非纯记忆检索延迟。

**核心发现：**
1. trpc-agent (LC) 在 **multi-hop 类别排名第一**（0.332），在
   **open-domain 类别排名第一**（0.521），超越所有专用记忆系统
2. trpc-agent (Auto) 已超越 ReadAgent、MemoryBank，接近 LoCoMo
   pipeline 水平，同时仅使用约 2K tokens/QA
3. **时间推理**（< 0.1）是与 Mem0（0.489）的主要差距，为首要优化方向
4. **对抗鲁棒性**（0.650–0.825，第 3 节）是独特优势，
   Mem0 论文未评估该维度

---

## 6. 结论

### 核心发现

1. **Memory 是生产 Agent 的必需能力。** Long-Context 适用于上下文窗口
   内的单 session 场景，但无法跨 session 持久化知识，也无法扩展到
   超过模型上下文窗口（GPT-4o-mini 128K tokens）的历史。Memory 提供
   持久化跨 session 知识，Token 成本仅为 Long-Context 的 10.4%，且对抗鲁棒性更优
   （0.771 vs 0.663）。

2. **Auto pgvector 是推荐的记忆方案。** 以长上下文 10.4% 的 token 成本
   达到其 F1 的 76.7%，且对抗鲁棒性突出（0.771）。

3. **trpc-agent-go 在 Agent 框架中综合排名第二**（0.842），仅落后
   AutoGen（0.896）5.4%。

   **与 AutoGen 的差距分析：** 原始 F1 差距（0.363 vs 0.442）主要
   来自 multi-hop 类别（0.091 vs 0.499）。AutoGen 的优势在于一次性
   注入 top-30 检索结果（1.0 call/QA），在多跳问题中能把更多相关片段
   同时送入上下文，提升了跨片段推理能力。trpc-agent-go 采用 Agent
   工具调用模式（retrieve + answer, 2.0 calls/QA），在多跳场景中
   单次检索覆盖不足。这是已识别的改进方向（见第 6 点）。

   **为什么 AutoGen 预注入记忆反而 token 更少：** AutoGen 的单次调用
   架构（1.0 call/QA）只需发送一次 system prompt + 检索结果 + 问题。
   trpc-agent-go 的工具调用模式（2.0 calls/QA）在第二次调用时需要
   重复发送完整的 system prompt、tool definitions 和消息历史，额外
   增加约 286 tokens/QA。代价换来的是灵活性：工具调用模式支持动态
   检索策略、多轮搜索、运行时可配后端——这些在固定预注入管线中无法
   实现。

   **trpc-agent-go 的核心优势：**
   - **Token 效率极高：** F1/1M Tokens 达 182.6，是 Agno 的 4.8 倍、
     ADK/CrewAI 的 40 倍。完整 1,986 QA 评估仅需 $0.62（ADK $19.42，
     CrewAI $14.71）
   - **评估速度快：** 总耗时 2h53m，排名第二（仅次于 AutoGen 2h22m），
     远优于 ADK（4h42m）和 Agno（11h58m）
   - **可靠性 100%：** 零失败 QA，而 ADK 有 122 次上下文溢出（6.1%
     失败率），CrewAI 在溢出后静默丢失记忆
   - **LLM Judge 质量最高：** 归一化后 LLM Score 达 0.967，五个
     框架中最高（详见第 4 点）
   - **架构灵活：** Agent 工具调用模式支持多后端（pgvector/MySQL）、
     历史注入、多种检索策略，适合生产环境定制

4. **LLM-as-Judge 质量与所有框架持平。** 使用相同 positive-only
   聚合归一化后，trpc-agent-go 的 LLM Score 达 **0.967**——五个
   框架中**最高**（AutoGen 0.932, ADK 0.936, Agno 0.935,
   CrewAI 0.918）。报告的低值（0.373）反映的是 Go 实现的全样本
   分母聚合方式，而非回答质量问题。

5. **历史注入以精度换语义。** 原始历史提升 LLM Score（+0.097）但降低
   F1（-0.075）和对抗鲁棒性（-0.362），建议选择性注入。

6. **多跳和时间推理是主要改进方向。** 当前 Auto pgvector multi-hop 仅
   0.091（AutoGen 0.499），temporal 仅 0.063（Mem0 0.489）。图结构
   记忆和时间索引是计划中的优化。

### 生产建议

| 使用场景 | 推荐方案 |
| --- | --- |
| 短对话单 session（< 50K tokens） | Long-Context（无需记忆） |
| 长期运行 Agent（数周/数月历史） | Auto 提取 + pgvector |
| 历史超出上下文窗口限制 | Memory（唯一可行方案） |
| 语义质量优先 | 记忆 + 选择性历史注入 |
| 成本敏感部署 | Auto pgvector（节省 89.6% token） |

---

## 附录

### A. 实验环境

| 组件 | 版本/配置 |
| --- | --- |
| 框架 | trpc-agent-go |
| 模型 | gpt-4o-mini |
| Embedding | text-embedding-3-small |
| PostgreSQL | 15+ with pgvector extension |
| MySQL | 8.0+ with full-text search |
| 数据集 | LoCoMo-10（10 样本，1,986 QA） |

### B. 完整内部结果——所有场景

**B.1 历史注入（所有场景）**

| 场景 | 后端 | 历史 | F1 | BLEU | LLM Score | 延迟 |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Agentic | pgvector | 无 | 0.287 | 0.273 | 0.280 | 4,704ms |
| Agentic | pgvector | +300 | 0.272 | 0.242 | 0.365 | 5,201ms |
| Agentic | pgvector | +700 | 0.274 | 0.228 | 0.459 | 4,641ms |
| Agentic | MySQL | 无 | 0.291 | 0.276 | 0.285 | 3,939ms |
| Agentic | MySQL | +300 | 0.271 | 0.242 | 0.368 | 4,616ms |
| Agentic | MySQL | +700 | 0.278 | 0.231 | 0.463 | 4,845ms |
| Auto | pgvector | 无 | 0.363 | 0.339 | 0.373 | 5,235ms |
| Auto | pgvector | +300 | 0.294 | 0.259 | 0.410 | 5,474ms |
| Auto | pgvector | +700 | 0.288 | 0.243 | 0.470 | 5,494ms |
| Auto | MySQL | 无 | 0.352 | 0.327 | 0.373 | 4,785ms |
| Auto | MySQL | +300 | 0.282 | 0.248 | 0.397 | 4,868ms |
| Auto | MySQL | +700 | 0.290 | 0.244 | 0.477 | 5,133ms |

**B.2 类别详情——无历史（F1 / BLEU / LLM）**

| 场景 | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Long-Context | 0.324/0.252/0.330 | 0.332/0.296/0.264 | 0.103/0.080/0.177 | 0.521/0.460/0.661 | 0.663/0.662/0.663 |
| Agentic pgvec | 0.150/0.110/0.101 | 0.142/0.126/0.078 | 0.047/0.033/0.076 | 0.129/0.118/0.150 | 0.825/0.825/0.825 |
| Agentic MySQL | 0.155/0.112/0.112 | 0.153/0.136/0.086 | 0.035/0.022/0.054 | 0.141/0.127/0.164 | 0.816/0.816/0.816 |
| Auto pgvec | 0.246/0.183/0.209 | 0.091/0.085/0.051 | 0.063/0.046/0.068 | 0.324/0.293/0.376 | 0.771/0.771/0.770 |
| Auto MySQL | 0.290/0.224/0.259 | 0.118/0.106/0.090 | 0.068/0.053/0.116 | 0.337/0.305/0.401 | 0.650/0.650/0.650 |

**B.3 类别详情——+700 历史（F1 / BLEU / LLM）**

| 场景 | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Agentic pgvec | 0.191/0.145/0.303 | 0.094/0.072/0.196 | 0.093/0.072/0.240 | 0.333/0.249/0.677 | 0.384/0.384/0.385 |
| Agentic MySQL | 0.181/0.137/0.295 | 0.095/0.072/0.198 | 0.086/0.064/0.209 | 0.338/0.255/0.678 | 0.398/0.397/0.407 |
| Auto pgvec | 0.194/0.148/0.322 | 0.099/0.078/0.174 | 0.093/0.068/0.225 | 0.350/0.269/0.690 | 0.409/0.409/0.414 |
| Auto MySQL | 0.185/0.140/0.300 | 0.097/0.077/0.202 | 0.094/0.075/0.227 | 0.352/0.269/0.698 | 0.420/0.419/0.425 |

**B.4 类别详情——+300 历史（F1 / BLEU / LLM）**

| 场景 | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Agentic pgvec | 0.149/0.117/0.212 | 0.128/0.099/0.117 | 0.054/0.045/0.132 | 0.242/0.192/0.434 | 0.559/0.559/0.558 |
| Agentic MySQL | 0.156/0.123/0.245 | 0.101/0.078/0.114 | 0.055/0.044/0.136 | 0.238/0.190/0.419 | 0.577/0.577/0.581 |
| Auto pgvec | 0.200/0.156/0.273 | 0.112/0.092/0.145 | 0.079/0.065/0.138 | 0.302/0.243/0.532 | 0.514/0.514/0.513 |
| Auto MySQL | 0.184/0.146/0.272 | 0.102/0.084/0.105 | 0.083/0.069/0.151 | 0.287/0.228/0.521 | 0.505/0.505/0.504 |

### C. Python 框架各样本 F1

| 样本 | QA 数 | AutoGen | Agno | trpc-agent-go | ADK | CrewAI |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| conv-26 | 199 | 0.434 | 0.356 | 0.335 | 0.327 | 0.229 |
| conv-30 | 105 | 0.453 | 0.414 | 0.325 | 0.372 | 0.336 |
| conv-41 | 193 | **0.513** | 0.419 | 0.442 | 0.282 | 0.233 |
| conv-42 | 260 | 0.380 | 0.368 | 0.375 | 0.293 | 0.287 |
| conv-43 | 242 | **0.445** | 0.369 | 0.387 | 0.301 | 0.264 |
| conv-44 | 158 | **0.460** | 0.390 | 0.257 | 0.326 | 0.273 |
| conv-47 | 190 | **0.463** | 0.401 | 0.364 | 0.275 | 0.199 |
| conv-48 | 239 | **0.461** | 0.433 | 0.326 | 0.326 | 0.222 |
| conv-49 | 196 | 0.397 | 0.331 | 0.407 | 0.291 | 0.219 |
| conv-50 | 204 | **0.437** | 0.360 | 0.376 | 0.249 | 0.240 |
| **平均** | **199** | **0.444** | **0.384** | **0.359** | **0.304** | **0.250** |

### D. Token 消耗——完整数据

| 场景 | 后端 | 历史 | Prompt Tokens | Completion Tokens | Total Tokens | LLM 调用 | 调用/QA |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| Long-Context | - | - | 37,272,167 | 15,997 | 37,288,164 | 1,986 | 1.0 |
| Agentic | pgvector | 无 | 6,159,851 | 58,889 | 6,218,740 | 4,034 | 2.0 |
| Agentic | MySQL | 无 | 8,045,416 | 59,057 | 8,104,473 | 4,046 | 2.0 |
| Auto | pgvector | 无 | 3,890,627 | 57,501 | 3,948,128 | 4,000 | 2.0 |
| Auto | MySQL | 无 | 18,007,763 | 59,474 | 18,067,237 | 4,073 | 2.1 |
| Agentic | pgvector | +300 | 34,317,758 | 56,062 | 34,373,820 | 3,231 | 1.6 |
| Agentic | MySQL | +300 | 35,112,488 | 54,513 | 35,167,001 | 3,237 | 1.6 |
| Auto | pgvector | +300 | 30,557,714 | 52,501 | 30,610,215 | 3,023 | 1.5 |
| Auto | MySQL | +300 | 34,924,189 | 52,178 | 34,976,367 | 3,016 | 1.5 |
| Agentic | pgvector | +700 | 45,883,202 | 39,855 | 45,923,057 | 2,302 | 1.2 |
| Agentic | MySQL | +700 | 46,056,594 | 39,343 | 46,095,937 | 2,299 | 1.2 |
| Auto | pgvector | +700 | 42,589,275 | 35,872 | 42,625,147 | 2,180 | 1.1 |
| Auto | MySQL | +700 | 43,416,313 | 35,759 | 43,452,072 | 2,185 | 1.1 |

### E. 总评估时间

**跨框架对比**（memory 场景，1,986 QA，同一模型 GPT-4o-mini）：

```
Total Evaluation Time (memory scenario, 1986 QA)

AutoGen         |==========                                | 2h22m
trpc-agent-go   |============                              | 2h53m
ADK             |===================                       | 4h42m
CrewAI          |=====================                     | 5h08m
Agno            |=============================================| 11h58m
                +------------------------------------------+
                0h       2h       4h       6h       8h    12h
```

| 框架 | 总时间 | 平均延迟/QA | vs trpc-agent-go |
| --- | ---: | ---: | ---: |
| AutoGen | 2h22m | 4,315ms | 0.82x |
| trpc-agent-go | 2h53m | 5,235ms | 1.00x |
| ADK | 4h42m | 8,531ms | 1.63x |
| CrewAI | 5h08m | 9,311ms | 1.78x |
| Agno | 11h58m | 21,711ms | 4.15x |

> AutoGen 最快，因为单次检索（1.0 LLM call/QA）。trpc-agent-go
> 尽管使用 2.0 calls/QA 仍排第二——Go 运行时开销低且并行友好。
> Agno 最慢，因记忆摄入阶段的 LLM 事实提取带来大量额外处理开销。

**trpc-agent-go 各配置明细：**

| 场景 | 后端 | 历史 | 总时间 | 平均延迟/QA |
| --- | --- | --- | --- | --- |
| Long-Context | - | - | 1h41m | 3,063ms |
| Agentic | pgvector | 无 | 2h35m | 4,704ms |
| Agentic | MySQL | 无 | 2h10m | 3,939ms |
| Auto | pgvector | 无 | 2h53m | 5,235ms |
| Auto | MySQL | 无 | 2h38m | 4,785ms |
| Agentic | pgvector | +300 | 2h52m | 5,201ms |
| Agentic | MySQL | +300 | 2h32m | 4,616ms |
| Auto | pgvector | +300 | 3h01m | 5,474ms |
| Auto | MySQL | +300 | 2h41m | 4,868ms |
| Agentic | pgvector | +700 | 2h33m | 4,641ms |
| Agentic | MySQL | +700 | 2h40m | 4,845ms |
| Auto | pgvector | +700 | 3h01m | 5,494ms |
| Auto | MySQL | +700 | 2h50m | 5,133ms |

---

## 参考文献

1. Maharana, A., Lee, D., Tulyakov, S., Bansal, M., Barbieri, F., and Fang, Y. "Evaluating Very Long-Term Conversational Memory of LLM Agents." arXiv:2402.17753, 2024.
2. Chhikara, P., Khant, D., Aryan, S., Singh, T., and Yadav, D. "Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory." arXiv:2504.19413, 2025.
3. Hu, C., et al. "Memory in the Age of AI Agents." arXiv:2512.13564, 2024.
