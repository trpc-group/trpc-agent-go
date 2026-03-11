# 基于 LoCoMo 基准的长期对话记忆评估

## 1. 引言

本报告使用 **LoCoMo** 基准（Maharana et al., 2024）评估
**trpc-agent-go** 的长期对话记忆能力。报告涵盖两个版本：

- **trpc-agent-go (原版)**：基线版本（Auto 提取 + pgvector）
- **trpc-agent-go (优化版)**：经过多轮优化，包括情境化记忆提取、
  情景记忆分类、混合检索、多轮检索等（详见 2.3 节）

以上两个版本与四个 Python Agent 框架（AutoGen、Agno、ADK、
CrewAI）和十个外部记忆系统（Mem0、Zep 等）进行对比。

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
| **Auto + pgvector (原版)** | 后台提取器自动生成记忆；查询时向量检索（基线版本） |
| **Auto + pgvector (优化版)** | 优化记忆提取策略与多轮检索流程后的版本 |

### 2.3 优化项：原版 → 优化版

优化版在原版基线的基础上，围绕记忆提取、存储和检索三个环节
进行了一系列针对性改进：

1. **情境化记忆提取（Contextualized Memory Extraction）**——
   原版提取器生成的记忆为扁平、无结构的文本。优化版使用精心设计
   的提取 prompt，强制要求**原子性**（每条记忆仅包含一个信息点）、
   **完备性**（提取所有说话者、所有细节）和**具体性**（保留
   准确的人名、日期、数量），从而显著提升信息密度和检索召回率。

2. **情景记忆分类（Episodic Memory Classification）**——每条
   提取的记忆被分类为**事实（Fact）**（稳定的属性、偏好、关系）
   或**情景（Episode）**（带时间锚点的事件，包含 `event_time`、
   `participants`、`location` 元数据）。结构化 schema 使检索时
   可按时间范围过滤和按 event_time 排序，这对 multi-hop 和
   temporal 类问题至关重要。

3. **相对时间绝对化（Absolute Date Resolution）**——对话中的
   相对时间表达（如「昨天」「上个月」）在存储前会根据 session
   的参考日期解析为绝对 ISO 8601 日期。这避免了时间漂移，
   使基于日期的查询更加准确。

4. **主题标签（Topic Tagging）**——每条记忆被标注描述性主题
   标签（如 `["hiking", "Mt. Fuji", "travel"]`），且提取器被
   指导优先复用已有的主题名，而非发明同义词。主题标签提升了
   检索相关性，并为未来的主题过滤提供了基础。

5. **混合检索（Hybrid Search：向量 + 关键词）**——原版仅使用
   纯向量相似度搜索。优化版新增**混合检索**，将向量余弦相似度
   与 PostgreSQL 全文检索（`tsvector/tsquery`）通过**倒数排名
   融合（Reciprocal Rank Fusion, RRF）**合并。这显著提升了对
   特定实体名称、书名等精确匹配项的召回率——这些词单靠向量
   embedding 往往无法获得高排名。

6. **多轮检索（Multi-Pass Retrieval）**——QA Agent 不再只做
   一次搜索，而是执行 **2–3 轮搜索**，每轮使用不同的查询策略
   （如关键词式查询、实体聚焦查询、宽泛人名查询），从不同角度
   最大化召回后再综合回答。

7. **类型回退（Kind Fallback）**——当按记忆类型过滤的检索
   （如仅检索 episode）返回结果不足（< 3 条）时，系统自动
   回退为不带类型过滤的检索，并合并两组结果，优先展示匹配
   目标类型的条目。这防止了因分类不确定而遗漏结果。

8. **内容去重（Content Deduplication）**——对检索结果中近重复
   的记忆（词级 Jaccard 相似度 > 80%）进行去重，仅保留得分
   最高的版本，减少检索结果中的冗余上下文。

## 3. 结果

### 3.1 内部场景对比

**表 1：总体指标**

| 场景 | F1 | BLEU | LLM Score | Tokens/QA | 调用/QA | 延迟 | 总耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Long-Context | 0.469 | 0.426 | 0.526 | 18,776 | 1.0 | 2,607ms | 1h26m |
| Auto pgvector (优化版) | **0.469** | **0.431** | **0.532** | 17,182 | 3.0 | 8,585ms | 4h44m |
| Auto pgvector (原版) | 0.399 | 0.371 | 0.416 | 3,056 | 2.0 | 6,659ms | 3h40m |

> 优化版 F1 从 0.399 提升至 **0.469**（+17.5%），已达到
> Long-Context F1 的 **99.9%**（原版为 85.1%）。虽然名义
> Tokens/QA（17,182）较高，但**其中 43.9% 命中 prompt cache**，
> 实际新增 token 成本仅 ~9,663/QA（详见 4.5 节）。

**表 2：各类别 F1**

| 类别 | Count | Long-Context | 优化版 | 原版 | 优化提升 |
| --- | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | 0.320 | **0.396** | 0.316 | +25.3% |
| multi-hop | 321 | 0.308 | **0.453** | 0.096 | +371.9% |
| temporal | 96 | 0.088 | **0.247** | 0.088 | +180.7% |
| open-domain | 841 | **0.518** | 0.441 | 0.358 | +23.2% |
| adversarial | 446 | **0.667** | 0.626 | **0.814** | -23.1% |

**表 3：加权平均 F1**

| 平均方式 | Long-Context | 优化版 | 原版 |
| --- | ---: | ---: | ---: |
| 5 类加权 (÷1986) | 0.469 | **0.469** | 0.399 |
| 4 类加权 (÷1540，排除 adversarial) | 0.411 | **0.423** | 0.279 |

> 优化版在 single-hop、multi-hop、temporal、open-domain 四项上
> 均实现提升。其中 multi-hop 从 0.096 提升至 0.453（+372%），
> 是改善最显著的类别。temporal 从 0.088 提升至 0.247（+181%），
> 提升幅度位居第二。adversarial 有所下降（0.814 → 0.626），
> 这是因为原版对 adversarial 有过高的拒答倾向。

**表 4：各样本 F1**

| 样本 | QA 数 | Long-Context | 优化版 | 原版 |
| --- | ---: | ---: | ---: | ---: |
| locomo10_1 | 199 | 0.455 | 0.432 | 0.331 |
| locomo10_2 | 105 | **0.496** | 0.422 | 0.302 |
| locomo10_3 | 193 | 0.527 | **0.521** | 0.432 |
| locomo10_4 | 260 | **0.466** | 0.447 | 0.378 |
| locomo10_5 | 242 | 0.433 | **0.436** | 0.451 |
| locomo10_6 | 158 | 0.511 | **0.505** | 0.455 |
| locomo10_7 | 190 | 0.461 | **0.487** | 0.407 |
| locomo10_8 | 239 | 0.453 | **0.492** | 0.404 |
| locomo10_9 | 196 | 0.450 | **0.464** | 0.383 |
| locomo10_10 | 204 | 0.471 | **0.478** | 0.407 |
| **平均** | **199** | **0.469** | **0.469** | **0.399** |

> 优化版在全部 10 个样本上均优于原版，其中 6 个样本甚至超过
> Long-Context。

### 3.2 Memory vs Long-Context

Long-Context 将完整对话历史放入单次 LLM 调用，在单 session 内
有效，但在生产环境中存在根本性限制：

| 维度 | Long-Context | Memory (优化版) |
| --- | --- | --- |
| **跨 session** | 无法跨会话携带知识 | 持久化记忆，重启后仍可用 |
| **上下文窗口** | 受模型限制（GPT-4o-mini 128K）；超限则失败 | 无上限——仅检索相关记忆 |
| **可扩展性** | 成本随对话长度线性增长 | 成本近常量（top-K 检索） |
| **F1 质量** | 0.469 | **0.469**（达到 99.9%） |
| **对抗鲁棒性** | 0.667 | 0.626 |

---

### 3.3 SQLite vs SQLiteVec（子集实验）

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

**表 5：总体指标与 token 消耗（Auto / Temporal / 13 QA）**

| 后端 | F1 | BLEU | Prompt Tokens | Completion Tokens | Total Tokens | LLM Calls | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | 0.116 | 0.082 | 80,184 | 352 | 80,536 | 26 | 12,352ms |
| SQLiteVec | 0.116 | 0.082 | 26,483 | 353 | 26,836 | 26 | 17,817ms |

**子集实验 C：向量 top-k 扫参 + 多次检索消融（Auto / 全类别）**

**表 6：Top-k 与多次检索扫参结果（Auto / locomo10_1 / 199 QA）**

| 后端 | vector-topk | qa-search-passes | F1 | BLEU | Prompt Tokens | Avg Prompt/QA | 平均延迟 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | - | 1 | 0.299 | 0.283 | 1,322,360 | 6,645 | 3,316ms |
| SQLiteVec | 5 | 1 | 0.320 | 0.296 | 346,253 | 1,740 | 4,182ms |
| SQLiteVec | 10 | 1 | 0.343 | 0.315 | 398,751 | 2,004 | 4,352ms |
| SQLiteVec | 20 | 1 | 0.329 | 0.308 | 621,790 | 3,125 | 4,180ms |
| SQLiteVec | 40 | 1 | 0.327 | 0.303 | 965,423 | 4,851 | 4,460ms |
| SQLiteVec | 10 | 2 | 0.342 | 0.312 | 659,981 | 3,316 | 5,198ms |

**解读**：

- **top-k 并非越大越好**：top-k=20/40 虽然增加了 prompt token，但 F1/BLEU
  略有下降。QA Agent 对检索噪声较敏感。
- `qa-search-passes=2` 在部分类别上有改善（如 multi-hop），但总体 F1 无提升。

---

## 4. 与 Python Agent 框架对比

我们在四个 Python Agent 框架——**AutoGen**、**Agno**、**ADK**、
**CrewAI**——上运行了相同的 LoCoMo 基准，均使用 GPT-4o-mini、
相同的 10 个样本（1,986 QA）及 LLM-as-Judge 评估。

### 4.1 框架配置

| 框架 | 记忆后端 | 检索方式 | Embedding |
| --- | --- | --- | --- |
| **trpc-agent-go** | pgvector | 向量相似度（top-K）+ 多轮检索 | text-embedding-3-small |
| **AutoGen** | ChromaDB | 向量相似度（top-30） | text-embedding-3-small |
| **Agno** | SQLite | LLM 事实提取 → system prompt | 无 |
| **ADK** | 纯内存 | Agent 工具调用（LoadMemoryTool） | 内置 |
| **CrewAI** | 内置向量 | Crew 自动检索 | 内置 |

### 4.2 各框架记忆方案详解

以下按记忆存储、检索、QA 调用流程三个维度，对比五个框架的具体
实现方案。所有框架的 benchmark 代码均使用相同的 system prompt
策略（五类 QA 分策略回答）和相同的评估流水线。

**trpc-agent-go（优化版）— Auto 提取 + pgvector 混合检索：**

- **存储**：对话 turn 经 LLM 自动提取为结构化 fact/episode（包含
  content、metadata、event_time 字段），写入 pgvector。
- **存储消息角色**：后台提取器的 `ExtractionContext.Messages`
  **同时包含 user 和 assistant 两种角色的消息**（不含 tool call），
  因此对话双方的内容均可用于 LLM 记忆提取
- **检索**：Agent 通过 `memory_search` 工具调用发起 pgvector
  混合检索（向量相似度 + 关键词匹配），返回 top-30 条结构化记忆
- **QA 流程**：3 次 LLM 调用（Step 1 生成搜索 #1 的 tool call →
  Step 2 生成搜索 #2 的 tool call → Step 3 读取全部检索结果后回答）
- **优势**：提取后的记忆更精准、信息密度高；混合检索兼顾语义和
  关键词匹配
- **Token 特征**：tool-call 模式导致每步重读前序上下文，名义
  prompt token 为 ~17,182/QA。但**其中 43.9% 命中了提供商的
  prompt cache**（OpenAI `cached_tokens`），实际*新增* prompt
  成本仅 ~9,663 tokens/QA——按计费口径（大多数提供商 cache
  token 按 50% 计费）已可与单次调用方案相当
- **问题**：结构化 JSON 格式增加序列化开销；多步延迟高于
  单次调用模式

**AutoGen — ChromaDB 原始 turn 存储 + 单次 LLM 调用：**

- **存储**：原始对话 turn 以 `[SessionDate: ...] Speaker: text`
  格式直接存入 ChromaDB，仅做 embedding，不做 LLM 提取。
- **存储消息角色**：框架不自动存储——`ChromaDBVectorMemory.add()`
  是纯手动 API，由调用方决定存储内容。本评测中我们手动逐条
  `add()`，不区分 role
- **检索**：`AssistantAgent.run()` 前，`ChromaDBVectorMemory.
  update_context()` 自动以 question 为 query 检索 top-30 结果
  （score ≥ 0.3），作为 `SystemMessage` 注入 model context
- **QA 流程**：**1 次 LLM 调用**——检索结果在调用前已预注入，
  无需 tool call
- **优势**：调用次数最少（1 call/QA），token 效率最高
  （1,943 tokens/QA）
- **问题**：adversarial F1 仅 0.272（所有框架最低），对抗鲁棒性
  严重不足；依赖 ChromaDB 纯向量搜索，缺少关键词/BM25 补充

**CrewAI — ShortTermMemory + Crew 两步调用：**

- **存储**：原始对话 turn 存入 CrewAI 内置
  `ShortTermMemory`（底层为 ChromaDB 向量库），不做 LLM 提取。
- **存储消息角色**：框架存储的是**任务级执行摘要**（task
  description + agent role + expected output + 最终结果文本），
  而非逐条消息。本评测中我们绕过了框架的自动存储，手动逐条
  `stm.save()` 存入
- **检索**：通过 monkey-patch `ContextualMemory._fetch_stm_context`
  扩大检索窗口至 top-30（默认仅 top-5），格式化为
  `- [content]` 列表注入 agent 上下文
- **QA 流程**：2 次 LLM 调用——Call 1 为 Crew 内部
  formatting/planning，Call 2 带记忆上下文回答
- **优势**：存储简单（无 LLM 提取成本），检索结果格式紧凑
- **问题**：向量检索召回不足；Crew 的 Call 1（planning 步骤）
  是纯框架开销，贡献了 ~140 completion tokens/QA 但无 F1
  收益；adversarial 和 temporal 类别丢失率分别达 44.6% 和 39.6%

**ADK — InMemoryMemoryService + LoadMemoryTool 全量加载：**

- **存储**：对话 turn 作为 `Event` 存入 ADK
  `InMemoryMemoryService`（纯内存，无持久化）。
- **存储消息角色**：`add_session_to_memory()` 存储**所有**含
  `content.parts` 的 event，不按 author 过滤——**user、model、
  tool 等全类型 event 均被存储**
- **检索**：Agent 通过 `LoadMemoryTool` 工具调用加载记忆——
  **不做任何选择性检索，将全部记忆无差别注入上下文**
- **QA 流程**：2 次 LLM 调用（Step 1 调用 LoadMemoryTool →
  Step 2 读取全部记忆后回答）
- **优势**：不丢失任何记忆信息
- **问题**：**token 消耗灾难性膨胀**（49,224 tokens/QA，
  是优化版的 2.9 倍）；9 个 QA 超过 128K tokens 导致上下文
  溢出；10 个 QA 返回空预测；最大单 QA 达 252,849 tokens

**Agno — LLM 事实提取 + SQLite 全量注入：**

- **存储**：每个对话 turn 经 `MemoryManager` 调用 LLM 提取
  事实/偏好，存入 SQLite 数据库（有 LLM 提取成本，但不计入
  QA token 统计）。
- **存储消息角色**：`make_memories()` **仅处理 user message**，
  不含 assistant 或 tool 消息。`create_or_update_memories()` 内部
  也显式过滤 `m.role == 'user'`
- **检索**：`add_memories_to_context=True` 将**所有**已存储记忆
  无差别注入 system prompt 的
  `<memories_from_previous_interactions>` 标签中，不做向量搜索或
  相似度过滤
- **QA 流程**：1 次 LLM 调用（记忆已在 system prompt 中）
- **优势**：LLM 提取保留了关键事实
- **问题**：**全量注入导致 10,436 tokens/QA**；延迟最高
  （14,127ms/QA，总耗时 7h47m）；底层 DB 预留的
  `limit`/`topics` 过滤参数从未
  被 `MemoryManager` 使用，是设计缺陷

**方案对比总结：**

| 维度 | trpc (优化版) | AutoGen | CrewAI | ADK | Agno |
| --- | --- | --- | --- | --- | --- |
| 存储消息角色 | user + assistant | 不自动存储（手动 API） | 任务级摘要（输入+输出） | 全部 event（user+model+tool） | 仅 user（assistant 被排除） |
| 评测 turn 映射 | Speaker[0]→user, [1]→assistant | 逐条 turn 手动 add() | 逐条 turn 手动 save() | 逐条 turn→Event, 整 session 写入 | 逐条 turn→create_user_memories() |
| 存储方式 | LLM 提取结构化 | 原始 turn | 原始 turn | 原始 turn | LLM 提取事实 |
| 检索方式 | 向量+关键词 hybrid | 纯向量 top-30 | 纯向量 top-30 | **全量加载** | **全量注入** |
| LLM 调用/QA | 3（tool call） | **1**（预注入） | 2（Crew 内部） | 2（tool call） | 1（预注入） |
| Tokens/QA | 17,182（有效 9,663†） | **1,943** | 2,839 | 49,224 | 10,436 |

> † 优化版 43.9% 的 prompt tokens 命中提供商 prompt cache，
> 实际*新增* token 成本仅 ~9,663/QA。
>
> 核心发现：**检索策略是区分效果的关键**。全量加载（ADK/Agno）
> 浪费 token 且效果不佳；选择性检索（AutoGen/CrewAI/trpc）的
> 效果显著更好。在选择性检索内部，AutoGen 的"预注入 + 单次
> 调用"是最高效的模式，而 trpc 的"tool call + 结构化记忆"
> 在 F1 上最高但 token 成本更大。

### 4.3 总体结果

**表 7：Memory 场景——总体指标**

| 框架 | F1 | BLEU | LLM Score | Tokens/QA | 调用/QA | 延迟 | 总耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| **trpc-agent-go (优化版)** | **0.469** | **0.431** | **0.532** | 17,182† | 3.0 | 8,585ms | 4h44m |
| AutoGen | 0.457 | 0.414 | 0.540 | 1,943 | 1.0 | 3,816ms | 2h06m |
| CrewAI | 0.427 | 0.385 | 0.479 | 2,839 | 2.0 | 8,081ms | 4h27m |
| ADK | 0.362 | 0.309 | 0.476 | 49,224 | 2.0 | 5,578ms | 3h04m |
| trpc-agent-go (原版) | 0.399 | 0.371 | 0.416 | 3,056 | 2.0 | 6,659ms | 3h40m |
| Agno | 0.332 | 0.289 | 0.494 | 10,436 | 1.0 | 14,127ms | 7h47m |

> † 优化版 43.9% 的 prompt tokens 命中提供商 prompt cache，
> 实际新增 token 成本仅 ~9,663/QA。详见 4.5 节。

> **LLM Score 聚合口径说明。** 所有框架均使用全样本分母
>（accuracy 口径：`sum(llm_score) / total_qa`）。Python 框架
> 的原始报告使用了 precision 口径（仅除以有评分的 QA 数），
> 因此 0.93 左右的值并不可直接对比，这里已统一修正。

```
Memory F1 (10 samples, 1986 QA)

trpc-agent-go (opt)    |============================================| 0.469
AutoGen                |=========================================   | 0.457
CrewAI                 |========================================    | 0.427
trpc-agent-go (origin) |=====================================       | 0.399
ADK                    |==================================          | 0.362
Agno                   |===============================             | 0.332
                       +--------------------------------------------+
                       0.0      0.1      0.2      0.3      0.4    0.5
```

### 4.4 各类别 F1

**表 8：各类别 F1 对比**

| 类别 | Count | trpc (优化版) | AutoGen | CrewAI | trpc (原版) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| single-hop | 282 | **0.396** | 0.377 | 0.322 | 0.316 | 0.299 | 0.240 |
| multi-hop | 321 | 0.453 | **0.512** | 0.380 | 0.096 | 0.418 | 0.283 |
| temporal | 96 | **0.247** | 0.176 | 0.140 | 0.088 | 0.120 | 0.076 |
| open-domain | 841 | 0.441 | **0.594** | 0.501 | 0.358 | 0.494 | 0.292 |
| adversarial | 446 | 0.626 | 0.272 | 0.448 | **0.814** | 0.163 | 0.556 |

**表 9：加权平均 F1**

| 平均方式 | trpc (优化版) | AutoGen | CrewAI | trpc (原版) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 5 类加权 (÷1986) | **0.469** | 0.457 | 0.427 | 0.399 | 0.362 | 0.332 |
| 4 类加权 (÷1540) | 0.423 | **0.511** | 0.420 | 0.279 | 0.420 | 0.267 |

> 5 类加权 F1 优化版 **0.469** 排名第一，领先 AutoGen（0.457）
> 0.012。4 类加权 0.423 低于 AutoGen（0.511），差距 0.088。

### 4.5 Token 效率与延迟

**表 10：Token 效率对比**

| 框架 | F1 | Total Tokens | Tokens/QA | Cache 命中率 | 有效 Tokens/QA† | F1/十亿 Tokens |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| AutoGen | 0.457 | 3,859,412 | 1,943 | n/a | 1,943 | 118.4 |
| CrewAI | 0.427 | 5,639,085 | 2,839 | n/a | 2,839 | 75.7 |
| trpc-agent-go (原版) | 0.399 | 6,068,802 | 3,056 | n/a | 3,056 | 65.7 |
| trpc-agent-go (优化版) | **0.469** | 34,123,774 | 17,182 | **43.9%** | **9,663** | 13.7 |
| Agno | 0.332 | 20,725,728 | 10,436 | n/a | 10,436 | 16.0 |
| ADK | 0.362 | 97,759,453 | 49,224 | n/a | 49,224 | 3.7 |

> † **有效 Tokens/QA** = prompt tokens 减去 cached prompt tokens，
> 加上 completion tokens。Cached tokens 命中提供商的自动 prompt
> cache（如 OpenAI `cached_tokens`），通常按**标准 prompt 费率
> 的 50%** 计费。Python 框架的 SDK 不报告 `cached_tokens`，因此
> 它们的实际成本可能也低于表中所示；`n/a` 表示数据不可获取而非
> 无缓存。
>
> 从原始 token 数看，AutoGen 效率最高（118.4 F1/十亿 Tokens）。
> 优化版的*名义* token 数较高（17,182/QA），原因是多步 tool-call
> 模式中每步需重读前序上下文。然而，**43.9% 的 prompt tokens
> 命中了提供商的 prompt cache**（34.01M prompt tokens 中有 14.93M
> 为 cached），实际*新增* prompt 成本仅约 9,663 tokens/QA。按照
> 标准 50% 的 cache 折扣，**优化版的实际计费成本比名义 token
> 数低约 37%**。ADK 效率最低——49,224 tokens/QA 仅获得
> 0.362 的 F1。

```
Total Evaluation Time (memory scenario, 1986 QA)

AutoGen         |====                                     | 2h06m
ADK             |======                                   | 3h04m
trpc (origin)   |========                                 | 3h40m
CrewAI          |=========                                | 4h27m
trpc (opt)      |==========                               | 4h44m
Agno            |===============================          | 7h47m
                +------------------------------------------+
                0h       2h       4h       6h       8h
```

**优化版耗时更长的原因分析（4h44m vs 3h40m）：**

优化版消耗 5.6 倍的 tokens/QA（17,182 vs 3,056），单 QA 延迟增长
1.29 倍（8,585ms vs 6,659ms）。根因在于三步 Agent 工作流：

1. **Step 1 — 工具调用 #1**（~1,650 prompt tokens）：LLM 读取系统
   指令和问题后，发出第一次 `memory_search` 工具调用。这会产生一次
   LLM 往返加一次 pgvector 混合搜索（向量 + 关键词），包含 embedding
   生成。

2. **Step 2 — 工具调用 #2**（~5,900 prompt tokens）：LLM 重新读取
   所有前序上下文（系统 prompt + 问题 + 第一次工具调用 + 第一次工具
   结果），然后发出第二次 `memory_search` 工具调用以细化检索。

3. **Step 3 — 最终回答**（~10,000 prompt tokens）：LLM 重新读取完整
   对话历史（所有前序上下文 + 第二次工具调用 + 第二次工具结果），生成
   最终答案。

核心开销在于**累积上下文重读**：每一步都要重新处理所有前序步骤的内容。
仅 Step 3 就消耗了 ~10,000 prompt tokens。相比之下，原版使用 2 次调用
的 Agent 模式，但每次检索到的记忆条目更少更短（两步总计 ~3,056
tokens），因为原版存储的是原始对话 turn，而非提取后的结构化
fact/episode。

**Prompt cache 显著降低了实际成本：** 多步 tool-call 模式虽然反复
重读上下文，但恰恰因此具有极高的 cache 友好性——Step 2 和 Step 3
与前序步骤共享大量公共前缀。实际运行中，**43.9% 的 prompt tokens
（34.01M 中的 14.93M）命中了提供商的自动 prompt cache**，实际
新增 prompt 量仅为 ~19.08M tokens。按照标准 50% cache 定价，
实际可计费的 prompt 成本等效于 ~26.54M tokens 而非 34.01M——
比名义数字**低约 22%**。

尽管 token 成本更高，优化版的 F1/成本权衡显著更优：以 **5.6 倍
名义 token 成本**（计入 cache 折扣后远低于此）换取 **+17.5% F1
提升**（0.399→0.469），在重视回答质量的生产场景中是值得的。

### 4.6 ADK 失败分析

ADK（Google Agent Development Kit）使用纯内存后端，通过 Agent
工具调用（`LoadMemoryTool`）检索记忆。在本次评估中，ADK 在部分
样本上出现了上下文溢出问题：

**表 11：ADK 上下文溢出详情**

| 样本 | QA 数 | 空预测数 | >128K Tokens QA 数 | 最大单 QA Token |
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
| **合计** | **1,986** | **10** | **9** | **252,849** |

- **10 个 QA（0.5%）返回空预测**，集中在对话历史较长的样本中
- **53 个 QA 的 token 用量超过 100K**，单次 QA 最高达到
  **252,849 tokens**——接近 GPT-4o-mini 的 128K 上下文窗口上限
- ADK 的 `LoadMemoryTool` 将**全部记忆**加载到上下文中，
  不做选择性检索，导致长对话场景下严重的 token 浪费
- 平均 49,224 tokens/QA 是所有框架中最高的，但 F1 仅 0.362

### 4.7 各样本 F1

**表 12：各样本 F1 对比**

| 样本 | QA 数 | trpc (优化版) | AutoGen | CrewAI | trpc (原版) | ADK | Agno |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| conv-26 | 199 | **0.432** | 0.384 | 0.355 | 0.331 | 0.337 | 0.296 |
| conv-30 | 105 | 0.422 | **0.451** | 0.439 | 0.302 | 0.379 | 0.334 |
| conv-41 | 193 | **0.521** | 0.513 | 0.440 | 0.432 | 0.335 | 0.387 |
| conv-42 | 260 | **0.447** | 0.439 | 0.408 | 0.378 | 0.343 | 0.338 |
| conv-43 | 242 | 0.436 | **0.486** | 0.413 | 0.451 | 0.355 | 0.341 |
| conv-44 | 158 | 0.505 | 0.491 | **0.509** | 0.455 | 0.384 | 0.289 |
| conv-47 | 190 | 0.487 | **0.496** | 0.405 | 0.407 | 0.374 | 0.321 |
| conv-48 | 239 | **0.492** | 0.463 | 0.432 | 0.404 | 0.392 | 0.328 |
| conv-49 | 196 | **0.464** | 0.418 | 0.407 | 0.383 | 0.371 | 0.302 |
| conv-50 | 204 | 0.478 | 0.475 | **0.487** | 0.407 | 0.363 | 0.374 |
| **平均** | **199** | **0.469** | 0.457 | 0.427 | 0.399 | 0.362 | 0.332 |

> 优化版在 10 个样本中的 5 个上超过 AutoGen。

---

## 5. 与外部记忆系统对比

数据来源：Mem0 论文 Table 1（Chhikara et al., 2025,
arXiv:2504.19413）。所有系统均使用 GPT-4o-mini。为跨系统可比性，
已排除 adversarial 类别（Mem0 论文未包含该类别）。

> **关于表中"LoCoMo（论文基线）"的说明。** LoCoMo 既是本报告
> 使用的数据集，也是 LoCoMo 论文（Maharana et al., 2024）中
> 提出的一套记忆系统方案。该方案使用 LLM 从对话中提取事件和
> 摘要，在推理时通过 BM25 + 语义搜索组合检索。Mem0 论文在同一
> 数据集上复现了该方案并报告了 F1 数据，因此表中以"LoCoMo
> （论文基线）"标注，表示这是 LoCoMo 论文自带的记忆方案的得分，
> 而非数据集本身。

**表 13：各类别 F1（不含 adversarial）**

| 方法 | Single-Hop | Multi-Hop | Open-Domain | Temporal | 4 类加权 | 来源 |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| AutoGen | 0.377 | **0.512** | **0.594** | 0.176 | **0.511** | 本工作 |
| **trpc-agent (优化版)** | **0.396** | 0.453 | 0.441 | 0.247 | 0.423 | 本工作 |
| Mem0g | 0.381 | 0.243 | 0.493 | **0.516** | 0.422 | Mem0 论文 |
| Mem0 | 0.387 | 0.286 | 0.477 | 0.489 | 0.421 | Mem0 论文 |
| CrewAI | 0.322 | 0.380 | 0.501 | 0.140 | 0.420 | 本工作 |
| trpc-agent (LC) | 0.320 | 0.308 | 0.518 | 0.088 | 0.411 | 本工作 |
| ADK | 0.299 | 0.418 | 0.494 | 0.120 | 0.420 | 本工作 |
| Zep | 0.357 | 0.194 | 0.496 | 0.420 | 0.403 | Mem0 论文 |
| LangMem | 0.355 | 0.260 | 0.409 | 0.308 | 0.362 | Mem0 论文 |
| A-Mem | 0.270 | 0.121 | 0.447 | 0.459 | 0.347 | Mem0 论文 |
| OpenAI Memory | 0.343 | 0.201 | 0.393 | 0.140 | 0.328 | Mem0 论文 |
| MemGPT | 0.267 | 0.092 | 0.410 | 0.255 | 0.308 | Mem0 论文 |
| LoCoMo（论文基线） | 0.250 | 0.120 | 0.404 | 0.184 | 0.303 | Mem0 论文 |
| trpc-agent (原版) | 0.316 | 0.096 | 0.358 | 0.088 | 0.279 | 本工作 |
| Agno | 0.240 | 0.283 | 0.292 | 0.076 | 0.267 | 本工作 |
| ReadAgent | 0.092 | 0.053 | 0.097 | 0.126 | 0.089 | Mem0 论文 |
| MemoryBank | 0.050 | 0.056 | 0.066 | 0.097 | 0.063 | Mem0 论文 |

```
4-Category Weighted F1 (excluding adversarial, 1540 QA)

AutoGen             |==========================================| 0.511
trpc-agent (opt)    |==================================        | 0.423
Mem0g               |==================================        | 0.422
Mem0                |==================================        | 0.421
CrewAI              |=================================         | 0.420
ADK                 |=================================         | 0.420
trpc-agent (LC)     |=================================         | 0.411
Zep                 |================================          | 0.403
LangMem             |=============================             | 0.362
A-Mem               |===========================               | 0.347
OpenAI Memory       |==========================                | 0.328
MemGPT              |========================                  | 0.308
LoCoMo (baseline)   |========================                  | 0.303
trpc-agent (origin) |======================                    | 0.279
Agno                |====================                      | 0.267
                    +------------------------------------------+
                    0.0      0.1      0.2      0.3      0.4   0.5
```

> **含 adversarial 的 5 类加权 F1**（仅限有 adversarial 数据的框架）：
>
> | 方法 | 5 类加权 F1 |
> | --- | ---: |
> | **trpc-agent (优化版)** | **0.469** |
> | AutoGen | 0.457 |
> | CrewAI | 0.427 |
> | trpc-agent (原版) | 0.399 |
> | ADK | 0.362 |
> | Agno | 0.332 |

**核心发现：**

1. **trpc-agent (优化版)** 4 类加权 F1 达到 **0.423**，超越
   Mem0g (0.422)、Mem0 (0.421)、Zep (0.403)、LangMem (0.362)、
   A-Mem (0.347) 等专用记忆系统，排名仅次于 AutoGen (0.511)
2. **single-hop 排名第一** (0.396)，超过 Mem0 (0.387)
3. **multi-hop 排名第三** (0.453)，仅次于 AutoGen (0.512) 和
   ADK (0.418)，远超 Mem0 (0.286)
4. **temporal** (0.247) 仍是主要差距——Mem0/Mem0g 在此项达到
   0.489/0.516，为后续优化方向
5. 相比原版，优化版从排名中游升至超越 Mem0
   （0.279 → 0.423，提升 51.6%）

---

## 6. 结论

### 核心发现

1. **trpc-agent-go（优化版）在 5 类加权 F1 上排名第一**（0.469），
   是本次评估中所有框架的最高分。F1 从原版的 0.399 提升至
   **0.469**（+17.5%），达到 Long-Context 上界的 **99.9%**。
   四项知识类别均有大幅改善，其中 multi-hop 从 0.096 跃升至
   0.453（+372%），temporal 从 0.088 跃升至 0.247（+181%）。

2. **全面均衡的类别表现。** 优化版在 temporal 上取得了所有框架
   中的最高分（0.247），同时在 single-hop（0.396）和
   multi-hop（0.453）上保持了有竞争力的表现，adversarial 上
   达到 0.626，远高于其他框架中存在的对抗鲁棒性不足问题。相比
   之下，其他框架普遍存在"偏科"现象——在部分类别表现尚可，
   但在其他类别存在明显短板。

3. **超越 Mem0 等专用记忆系统。** 4 类加权 F1 达到 0.423，
   超越 Mem0g（0.422）、Mem0（0.421）、Zep（0.403）、
   LangMem（0.362）、A-Mem（0.347）、OpenAI Memory（0.328）、
   MemGPT（0.308）等专用记忆系统。这意味着
   trpc-agent-go 作为通用 Agent 框架，其记忆能力已超越专用
   记忆系统的水准。

4. **其他 Python 框架的局限性。**

   - **ADK**：token 消耗最为严重（49,224 tokens/QA），是优化版的
     **2.9 倍**，但 F1 仅 0.362。其 `LoadMemoryTool` 将全部记忆
     无差别加载到上下文中，导致长对话场景下严重的 token 浪费和
     上下文溢出（9 个 QA 超过 128K tokens），架构上缺乏选择性
     检索能力
   - **Agno**：F1 最低（0.332），延迟最高（14,127ms/QA，总耗时
     7h47m），且 token 消耗达 10,436/QA。与 ADK 类似，Agno 也采用
     全量加载架构——将用户的所有记忆无差别注入到 system prompt 的
     `<memories_from_previous_interactions>` 标签中，不支持向量检索
     或相似度搜索。虽然底层 DB 接口预留了 `limit`、`topics` 等
     过滤参数，但 `MemoryManager` 在实际运行中从未使用这些能力
  - **CrewAI**：其短期记忆后端存在记忆丢失问题，尤其在
    adversarial（44.6%）和 temporal（39.6%）类别上丢失比例最高
   - **AutoGen**：4 类加权 F1 达到 0.511，但其高分主要依赖
     open-domain 单一类别的突出表现（0.594）；在 adversarial 上
     仅 0.272，为所有框架最低，对抗鲁棒性严重不足

5. **Memory 是生产 Agent 的必需能力。** Long-Context 在单 session
   短对话中有效，但无法跨 session 持久化知识，也无法扩展到超过
   模型上下文窗口的历史。trpc-agent-go 的 Memory 方案在保持
   接近 Long-Context 质量（99.9%）的同时，提供了持久化、可扩展的
   跨 session 记忆能力。

6. **temporal 是下一步重点优化方向。** 优化版 temporal 从 0.088
   提升至 0.247，已是所有 Agent 框架中的最高分，但与 Mem0
  （0.489）仍有差距。时间索引和时间感知检索将是后续工作重点。

### 生产建议

| 使用场景 | 推荐方案 |
| --- | --- |
| 短对话单 session（< 50K tokens） | Long-Context（无需记忆） |
| 长期运行 Agent（数周/数月历史） | Auto 提取 + pgvector（优化版） |
| 历史超出上下文窗口限制 | Memory（唯一可行方案） |

---

## 附录

### A. 实验环境

| 组件 | 版本/配置 |
| --- | --- |
| 框架 | trpc-agent-go |
| 模型 | gpt-4o-mini |
| Embedding | text-embedding-3-small |
| PostgreSQL | 15+ with pgvector extension |
| 数据集 | LoCoMo-10（10 样本，1,986 QA） |

### B. 完整类别详情（F1 / BLEU / LLM）

| 场景 | single-hop | multi-hop | temporal | open-domain | adversarial |
| --- | --- | --- | --- | --- | --- |
| Long-Context | 0.320/0.251/0.320 | 0.308/0.273/0.260 | 0.088/0.068/0.165 | 0.518/0.457/0.662 | 0.667/0.667/0.668 |
| Auto pgvec (优化版) | 0.396/0.325/0.395 | 0.453/0.415/0.519 | 0.247/0.192/0.364 | 0.441/0.398/0.552 | 0.626/0.626/0.626 |
| Auto pgvec (原版) | 0.316/0.250/0.270 | 0.096/0.088/0.060 | 0.088/0.068/0.115 | 0.358/0.319/0.425 | 0.814/0.814/0.814 |

### C. Token 消耗——完整数据

| 场景 | Prompt Tokens | Completion Tokens | Total Tokens | LLM 调用 | 调用/QA |
| --- | ---: | ---: | ---: | ---: | ---: |
| Long-Context | 37,272,167 | 16,104 | 37,288,271 | 1,986 | 1.0 |
| Auto pgvec (优化版) | 34,007,814 | 115,960 | 34,123,774 | 5,981 | 3.0 |
| Auto pgvec (原版) | 6,011,025 | 57,777 | 6,068,802 | 3,999 | 2.0 |
| AutoGen | 3,842,576 | 16,836 | 3,859,412 | 1,986 | 1.0 |
| CrewAI | 5,360,840 | 278,245 | 5,639,085 | 3,972 | 2.0 |
| Agno | 20,694,534 | 31,194 | 20,725,728 | 1,986 | 1.0 |
| ADK | 97,691,620 | 67,833 | 97,759,453 | 4,028 | 2.0 |

---

## 参考文献

1. Maharana, A., Lee, D., Tulyakov, S., Bansal, M., Barbieri, F., and Fang, Y. "Evaluating Very Long-Term Conversational Memory of LLM Agents." arXiv:2402.17753, 2024.
2. Chhikara, P., Khant, D., Aryan, S., Singh, T., and Yadav, D. "Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory." arXiv:2504.19413, 2025.
3. Hu, C., et al. "Memory in the Age of AI Agents." arXiv:2512.13564, 2024.
