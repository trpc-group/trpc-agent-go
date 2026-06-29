# 降低 Agent Token 成本：tRPC-Agent-Go 的 Prompt Cache 工程实践

> 本文聚焦 Prompt Cache：它不是框架在本地保存模型 KV，而是模型服务侧对稳定 prompt prefix 的复用能力。tRPC-Agent-Go 的工程实践，是把 Agent 请求尽量组织成“稳定前缀 + 动态后缀”，从而提升缓存命中概率，降低可复用输入 tokens 的成本。

大模型应用进入 Agent 阶段后，一个很直观的变化是：每次请求变长了。

普通 Chat 应用里，模型看到的可能只是 system prompt 加几轮对话。Agent 应用里，请求前缀往往还会包含工具 schema、结构化输出 schema、Skills/RAG 上下文、历史 tool call 和 tool result。一次用户请求如果触发多次工具调用，还会在同一轮里多次请求模型。这些请求之间有大量重复内容，也正是 Prompt Cache 能发挥作用的地方。

读完本文，你会看到四件事：

- Prompt Cache 对 Agent 为什么有响应速度和 token 成本价值。
- OpenAI-compatible API 里的 `cached_tokens`、`prompt_cache_key` 应该如何理解。
- tRPC-Agent-Go 如何在 system、tools、Skills、summary、time、post-tool prompt 等位置减少缓存破坏。
- 哪些 option 推荐使用，哪些动态能力需要谨慎配置。

文中的配置和实验主要使用 OpenAI-compatible API 作为示例口径；Anthropic 的显式 `cache_control` 机制放在附录中作为对照。OpenAI-compatible API 指的是兼容 OpenAI Chat Completions 等请求/响应协议形态的模型服务接口。它可以是 OpenAI 官方接口，也可以是其他模型厂商提供的兼容接口；但“兼容 OpenAI 协议”不等于“所有服务方都支持完全相同的 Prompt Cache 能力”。`cached_tokens`、`prompt_cache_key`、缓存门槛、TTL 和计费规则，都应以具体服务方文档为准。

## 一、Prompt Cache 对 Agent 的价值：更快响应、更低成本

Prompt Cache 对 Agent 的直接价值有两层：

1. 重复前缀被服务端复用后，Agent 调用通常能感受到首 token 或整体响应延迟下降。
2. 不少模型服务会把缓存命中的输入 tokens 按更低价格计费。

以 [DeepSeek 模型价格页](https://api-docs.deepseek.com/zh-cn/quick_start/pricing) 在 2026-06-09 查询到的价格为例，官方价格表按“百万 tokens”为单位区分了输入缓存命中、输入缓存未命中和输出价格：

| 模型 | 输入缓存命中 | 输入缓存未命中 | 输出 |
| --- | --- | --- | --- |
| `deepseek-v4-flash` | 0.02 元 / 百万 tokens | 1 元 / 百万 tokens | 2 元 / 百万 tokens |
| `deepseek-v4-pro` | 0.025 元 / 百万 tokens | 3 元 / 百万 tokens | 6 元 / 百万 tokens |

按这个价格表，`deepseek-v4-flash` 的缓存命中输入价格是未命中输入价格的 1/50；`deepseek-v4-pro` 的缓存命中输入价格是未命中输入价格的 1/120。也就是说，在长 system prompt、稳定 tools、多轮 history 这类重复前缀较多的 Agent 场景中，只要这些输入 tokens 能被服务端判定为 cached tokens，它们对应的输入成本就会显著下降。

这里需要强调两点。第一，这只是 DeepSeek 当前价格页上的示例，用来说明“缓存命中 tokens 通常更便宜”这个成本结构；其他 OpenAI-compatible 服务的价格、折扣和规则需要看各自文档。第二，Prompt Cache 主要降低的是可复用输入 tokens 的成本，不会减少所有输入，也不会影响输出 tokens 的计费方式。

## 二、Prompt Cache 是模型服务侧的前缀复用能力

理解 Prompt Cache，可以先从 Transformer 推理说起。[Hugging Face Transformers 的 KV cache 文档](https://huggingface.co/docs/transformers/en/kv_cache)解释了一个基础事实：自回归生成会逐 token 推进，模型可以缓存 attention 中已经计算过的 key/value 状态，后续 token 不必反复计算前面 token 的 key/value。

这说明“复用已处理前缀的中间状态”在模型推理中有明确技术背景。不过，在 API 层不应把 Prompt Cache 简单等同为某一种具体 KV cache 实现。更准确的说法是：

> Prompt Cache 是模型服务侧对已处理 prompt prefix 的复用能力。它可以理解为复用前缀的中间状态或服务端缓存结果；具体缓存介质、匹配粒度、保留时间和计费规则由服务方实现决定。

对支持 Prompt Cache 的 OpenAI-compatible API，服务端会检查当前 prompt 的 prefix 是否已经被处理过。如果命中，响应中的 `usage.prompt_tokens_details.cached_tokens` 会返回本次从缓存复用的 prompt tokens 数量。[OpenAI Prompt Caching 文档](https://developers.openai.com/api/docs/guides/prompt-caching)也强调了一个关键实践：把静态内容放在 prompt 开头，把每轮变化的内容放在后面。

这也是 `prompt_cache_key` 的语义边界。它不是缓存内容本身，也不是启用 Prompt Cache 的必要条件；不带 `prompt_cache_key` 时，服务端仍然可以按 prompt prefix 自动缓存和命中。`prompt_cache_key` 更像 OpenAI-compatible API 中用于辅助缓存路由的请求参数：当很多请求共享同一类长前缀时，一个稳定的 cache key 可能帮助这些请求落到更容易命中缓存的位置。反过来，如果把它设置成 request id、trace id、时间戳这类每次都不同的值，就会削弱前缀复用的机会。

[DeepSeek Context Caching 文档](https://api-docs.deepseek.com/guides/kv_cache/)从另一个 OpenAI-compatible 服务形态给出了相同思路：后续请求如果与之前请求有重叠前缀，重叠部分就有机会从缓存读取；它的多轮对话和长文档问答示例，本质上都是 prefix reuse。

## 三、Agent 为什么特别需要 Prompt Cache

Agent 请求天然包含大量稳定前缀。一个典型 LLM Agent 请求里，前部通常包括：

- system prompt、global instruction、agent identity。
- tool schema、function declaration、structured output schema。
- Skills overview、能力说明、固定工作流说明。
- 已发生的 session history、tool call、tool result。
- RAG、memory 或 workspace context。

多轮对话天然符合“旧上下文 + 新消息”的形态：

```text
第 1 轮: system + tools + user_1
第 2 轮: system + tools + user_1 + assistant_1 + user_2
第 3 轮: system + tools + user_1 + assistant_1 + user_2 + assistant_2 + user_3
```

从第二轮开始，新请求会带上上一轮的大部分内容。只要前面的 system、tools、历史消息没有被改写，模型服务侧就有机会复用这些前缀。

Tool call / ReAct 场景会进一步放大这一点。一条用户请求可能触发多次 LLM 调用：第一次让模型决定调用什么工具，第二次把 tool result 追加回来继续推理，后续还可能再次调用工具。每一次模型请求都会重复 system、tools 和已经发生的上下文。稳定前缀越长，Prompt Cache 的收益空间越大；前缀被动态内容频繁打断，命中率就会下降。

因此，Agent 框架的职责不是实现模型侧缓存，而是把请求组织成更适合缓存命中的结构：

```text
推荐形态:
[稳定前缀] system / instruction / tools / schema / stable skills overview
         + session history / previous tool call / previous tool result
[动态后缀] current user message / current tool result / current time / temporary context

需要避免:
system / instruction 被 summary、精确时间、RAG 片段、临时用户状态频繁改写
```

tRPC-Agent-Go 的 Prompt Cache 相关优化，正是围绕这个结构展开。

## 四、tRPC-Agent-Go 如何组织缓存友好的请求

tRPC-Agent-Go 不在本地保存模型 KV，也不替代服务方的 Prompt Cache。它做的是请求组织：尽量让稳定内容更早、更稳定地进入请求，让动态内容靠后，并减少工具和上下文顺序的随机变化。

进入框架实现前，先约定三个容易混用的概念：

- `system prompt` 指系统规则文本，例如 Agent 身份、回答风格、工具后回复要求等。
- `system message` 指请求中 `role=system` 的消息对象，它是承载 system prompt 的消息结构。
- `model request prefix` 指模型服务最终看到的请求前缀，也就是 provider 文档中常说的 prompt prefix。它不是 HTTP 请求字段前缀，而是模型输入从开头开始连续的一段内容，可能包含 system message、instruction、tools、schema、稳定 history 等。后文简称 `request prefix`。

三者的关系是：system prompt 通常会被写入 system message；system message 又和 tools、schema、稳定上下文一起构成 model request prefix。Prompt Cache 关注的是完整 request prefix 是否稳定，所以既要关心 system prompt 文本有没有变化，也要关心 system message 在请求里的位置和数量是否稳定。

先用一张表概览这一节要讲的处理点：

| 处理点 | 框架做了什么 | 减少哪类缓存破坏 |
| --- | --- | --- |
| Request processors | 先组装 instruction / system / Skills overview，再追加 history、tool result、time 等动态内容 | 避免动态内容过早进入 request prefix |
| Request prefix | 合并 global instruction / instruction，形成靠前的 request prefix | 避免规则、身份说明在每轮请求中位置漂移 |
| Message ordering | OpenAI-compatible adapter 可将 system messages 前置 | 让最稳定的 system messages 更靠前，更利于自动 prefix cache |
| Tools | Invocation 内缓存 tools snapshot，工具转换时按名称排序 | 避免工具列表顺序或 map 迭代顺序破坏 prefix |
| Post-tool guidance | 有工具面的 Agent 从第一次模型请求起稳定注入工具后提示 | 避免首次 tool result 后才改写 request prefix |
| Skills | 已加载 Skill 内容优先物化到 tool result | 减少动态 Skill 内容污染 request prefix |
| Summary generation | cache-safe forking 复用父请求前缀，只追加 compaction user message | 避免 summary 请求重新组织上下文导致缓存无法复用 |
| Summary injection | 可将 summary 注入 user/history 附近 | 避免频繁更新 summary 时改写 request prefix |
| Current time | 默认只注入日期级上下文，精确时间走工具 | 避免完整时间戳每轮改变 request prefix |

### Request processors：稳定内容先组装

**缓存风险**：Agent 请求不是一次性拼出来的，而是由 instruction、Skills、history、tool result、time 等多类上下文共同组成。如果动态内容过早进入 request prefix，即使后面的 system、tools、history 很稳定，也会因为前缀被打断而降低命中概率。

**框架处理**：`LLMAgent` 会通过一组 request processors 逐步构造模型请求。整体处理顺序是：

- Instruction processor 较早运行，把 global instruction / instruction 合并到 system message。
- Skills processor 注入 Skills overview 和加载状态。
- Content processor 再追加 conversation / context history。
- Post-tool processor 对有工具面的 Agent 预先注入稳定的工具后指导，避免第一次出现 tool result 后再改写 request prefix。
- Skills tool result processor 在 content 之后，把已加载 Skill 内容物化到相关 tool result。
- Time processor 放在更靠后的位置；默认只注入日期级上下文，精确当前时间通过工具按需获取，避免把每秒变化的时间戳写进 system message。

**使用建议**：应用侧也应延续这个顺序：规则、身份、工具、能力说明等相对稳定的内容尽量放前面；当前用户输入、工具结果、临时检索片段、精确时间等更动态的内容尽量放后面。

### Request prefix：系统规则形成可复用前缀

**缓存风险**：system prompt 通常会进入请求前部的 system message，也是最有价值的缓存前缀。如果每轮把用户状态、检索结果、精确时间等动态内容写进 system message，后续再长的稳定上下文也很难复用。

**框架处理**：`InstructionRequestProcessor` 会查找已有 system message。如果不存在，它会创建新的 system message 并插入到 `req.Messages` 头部；如果已经存在，则把 system prompt 和 instruction 合并到这条 system message 中。这让 Agent 的全局规则进入 request prefix 中较靠前且稳定的位置。

**使用建议**：把 instruction 当成“长期规则”而不是“每轮上下文”。每轮变化的用户信息、检索片段、任务临时状态，更适合放到靠后的 user/history/tool result 中。

### Message ordering：system messages 前置

**缓存风险**：如果 system messages 在请求中位置不稳定，或被历史消息夹在中间，服务端自动 prefix cache 更难把最稳定的 system messages 作为可复用前缀。

**框架处理**：`model/openai` 包提供了 `openai.WithOptimizeForCache`。在默认 `VariantOpenAI` 下，这项优化默认开启；如果显式使用其他 provider variant，或者应用严格依赖原始 message 顺序，需要按实际配置确认是否开启。开启后，请求发送前会调用 `optimizeMessagesForCache`，把 system messages 移到 messages 前部。

**使用建议**：这不是显式创建缓存，而是适配 OpenAI-compatible API 的自动前缀缓存。如果走默认 OpenAI variant，通常可以保留这项优化；只有在应用严格依赖原始 message 顺序时，才需要关闭：

```go
llm := openai.New("your-model",
    openai.WithOptimizeForCache(false),
)
```

### Tools：工具集合与顺序稳定

**缓存风险**：Tool schema 是 Agent 请求里常见的大块静态内容，也常常是可缓存前缀的重要组成部分。工具列表顺序不稳定、动态工具过滤结果频繁变化、底层 map 迭代顺序随机，都会让 tools prefix 难以复用。

**框架处理**：tRPC-Agent-Go 做了两层稳定化。第一层在 flow 层，框架会缓存一次 Invocation 内最终可见的工具列表；即使底层 ToolSet 是动态的，一次 Invocation 生命周期内发给模型的工具集合也尽量保持稳定。对于经过过滤的工具，框架会按工具名排序，避免 Go map 迭代顺序带来的随机变化破坏前缀。第二层在 OpenAI-compatible adapter，工具转换时会先抽取 tool names 并排序，再按稳定顺序转换成 OpenAI-compatible `tools` 参数。

**使用建议**：工具顺序框架会尽量稳定，但工具集合本身仍由应用决定。动态工具过滤、`WithRefreshToolSetsOnRun(true)`、动态 MCP tool list 都应谨慎使用；如果确实需要动态工具面，建议按场景分桶，并让同一类请求看到稳定工具集合。

### Post-tool guidance：提前稳定注入

**缓存风险**：Tool call 之后，模型会看到 `role=tool` 的工具结果。为了让工具调用后的回复更自然，框架通常会补一段 post-tool guidance，提醒模型基于工具结果直接回答，不暴露“我调用了某某工具”这类过程描述。如果等请求已经出现 tool result 之后，才把 post-tool prompt append 到 system message，那么第一轮工具结果后的模型请求会在很靠前的位置新增一段 system prompt 内容，导致后面的稳定 history、tool result 等内容失去前缀复用机会。

**框架处理**：tRPC-Agent-Go 把这段逻辑改成稳定注入：只要 Agent 有潜在 tool surface，默认 post-tool guidance 会从第一次模型请求起进入 system message；后续 ReAct / tool-loop 轮次不会再因为第一次出现 tool result 才改写 request prefix。纯文本 Agent 没有工具面时，框架不会为了这个能力额外创建 system message，避免无工具场景增加不必要前缀。

**使用建议**：用户仍然可以通过 `llmagent.WithPostToolPrompt("...")` 自定义这段稳定指导，或通过 `llmagent.WithEnablePostToolPrompt(false)` 关闭。自定义 prompt 应按 Agent 或版本保持稳定，不要按请求动态生成；如果确实需要把“本次工具结果后的临时要求”传给模型，更适合放到 tool result 或靠后的 user/history 消息里。

### Skills：动态内容进入 tool result

**缓存风险**：Skills 是 Agent 能力扩展的重要来源，但已加载的 Skill body 或 selected docs 往往随任务变化。如果这些内容每轮都追加到 system message，request prefix 就会频繁变化。

**框架处理**：tRPC-Agent-Go 提供了 `llmagent.WithSkillsLoadedContentInToolResults(true)`。开启后，已加载 Skill 内容会尽量追加到对应的 `skill_load` / `skill_select_docs` tool result，而不是追加到 system message。Skills tool result processor 会按稳定顺序处理已加载 Skills，优先把内容物化到匹配的 tool result；只有找不到匹配 tool result 时，才回退到 system message。

**使用建议**：这不是减少上下文，而是把动态上下文放到更合适的位置，减少对 request prefix 的污染。Skill 内容随任务变化时，建议开启该选项，并配合 `WithSkillLoadMode(...)`、`WithMaxLoadedSkills(...)` 控制驻留时长和数量：

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
    llmagent.WithMaxLoadedSkills(8),
)
```

### Summary generation：cache-safe forking

**缓存风险**：长会话 Agent 通常需要 summary 或 compaction。这里容易混在一起的有两件事：一是“生成 summary 那次请求”如何组织，二是“summary 已经生成后”如何注入后续普通对话请求。生成 summary 时，如果完全换一套独立 prompt 重新发送会话内容，就很难复用父会话已经建立起来的缓存前缀。

**框架处理**：tRPC-Agent-Go 提供了可选的 cache-safe summary forking 模式。生成 summary 的请求可以克隆父会话的模型请求，并只在尾部追加一条 compaction user message。

可以把它理解成：

```text
父会话请求:
system + tools + stable context + history_1...history_N + new_user_message

cache-safe compaction fork:
system + tools + stable context + history_1...history_N + compact_user_message
```

这样，compaction 本身也变成“父会话前缀 + 新动态后缀”的请求形态，更容易复用父会话已经建立起来的缓存。

**使用建议**：这个能力是 opt-in 的，默认仍保留独立 summary 请求，以兼容已有使用方式以及 summary model 与主 Agent model 不同的场景。长会话、同模型 summary、对 Prompt Cache 敏感的场景，可以优先评估开启：

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithContextThreshold(),
    summary.WithMaxSummaryWords(200),
    summary.WithCacheSafeForking(true),
)
```

如果需要自定义 fork 模式下追加的压缩指令，可以使用 `summary.WithCacheSafeForkPrompt(...)`。这条 prompt 可以包含 `{max_summary_words}`，但不应再包含 `{conversation_text}`，因为父请求本身已经包含完整的对话前缀。

### Summary injection：避免改写 request prefix

**缓存风险**：Summary 生成完成后，后续普通对话请求还需要决定把它放在哪里。默认的 `SessionSummaryInjectionSystem` 会把 summary 注入 system message，兼容“summary 必须留在 preserved head、不参与 sliding-window trimming”的场景；但如果 summary 经常更新，就会改写靠前 request prefix。

**框架处理**：对 Prompt Cache 更敏感的场景，可以显式使用 `WithSessionSummaryInjectionMode(SessionSummaryInjectionUser)`，把 summary 放到第一个 user history/current message 附近，让 request prefix 尽量不变；代价是 summary 会参与 token-budget trimming，长上下文下可能被裁掉。

**使用建议**：如果应用更重视 summary 永远留在 preserved head，可以保留 system 注入；如果更重视 request prefix 稳定，可以评估 user/history 注入。因此，cache-safe forking 优化的是“生成 summary 的请求”，`WithSessionSummaryInjectionMode(...)` 优化的是“普通对话请求如何注入 summary”。两者可以配合使用。

### Current time：日期级上下文 + 精确时间工具

**缓存风险**：Current time 是一个很典型的 Prompt Cache 陷阱。它看起来只是很短的一行上下文，但如果每次都把完整时间戳写进 system message，那么 request prefix 每秒都可能变化，后面的稳定 instruction、tools、history 也更难被服务侧复用。

对比 Codex 和 Claude Code 的做法，可以看到它们都没有把“当前时分秒”当成稳定 prompt 的一部分：它们更偏向把运行环境信息控制在日期、时区这类粗粒度信息上，让高频变化的 clock time 不进入稳定前缀。

**框架处理**：tRPC-Agent-Go 沿用已有的 `WithAddCurrentTime` 入口，但调整默认语义：默认只注入日期级 system context，把它当成“今天是哪一天”这种相对稳定的环境信息。在工具可用时，如果模型需要精确到时分秒的当前时间，可以通过内置的 `environment_context_current_time` 工具按需获取。遗留的 `WithOutputSchema` 会禁用所有工具，包括这个精确时间工具；如果同时需要结构化输出和精确时间工具，应使用 `WithStructuredOutputJSONSchema` 或 `WithStructuredOutputJSON`。

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithAddCurrentTime(true),
)
```

**使用建议**：默认 date-only 路径更 cache-friendly；如果显式配置完整时间格式，例如 `WithTimeFormat("2006-01-02 15:04:05 MST")`，仍然可以得到旧式完整时间戳，但需要接受 request prefix 随时间变化带来的缓存影响。

## 五、用户能配置和观测什么

Prompt Cache 的命中发生在模型服务侧，但用户仍然可以通过 tRPC-Agent-Go 控制请求形状、透传服务方参数，并观测命中效果。

### 配置缓存友好的请求形状

在默认 `VariantOpenAI` 配置下，OpenAI-compatible adapter 会进行 message cache optimization：

```go
llm := openai.New("your-model")
```

如果显式选择了其他 provider variant，可以按服务方行为确认是否需要显式配置 `openai.WithOptimizeForCache(true)`。如果应用严格依赖原始 message 顺序，可以关闭：

```go
llm := openai.New("your-model",
    openai.WithOptimizeForCache(false),
)
```

Skills 动态内容建议进入 tool result：

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
    llmagent.WithMaxLoadedSkills(8),
)
```

长会话 summary 如果关注 Prompt Cache，可以同时关注 summary 生成和 summary 注入两件事。生成 summary 时可以启用 cache-safe forking：

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithContextThreshold(),
    summary.WithMaxSummaryWords(200),
    summary.WithCacheSafeForking(true),
)
```

自定义 fork prompt 时使用 `summary.WithCacheSafeForkPrompt(...)`，不要再把 `{conversation_text}` 放进去。

普通对话请求注入 summary 时，可以使用 user/history 模式避免改写首条 system：

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithAddSessionSummary(true),
    llmagent.WithSessionSummaryInjectionMode(llmagent.SessionSummaryInjectionUser),
)
```

Memory preload 和 session recall preload 默认仍走 system context，以兼容既有行为。Cache-sensitive 场景可以显式切到 user/history 模式：

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithPreloadMemory(10),
    llmagent.WithPreloadMemoryInjectionMode(llmagent.PreloadMemoryInjectionUser),
    llmagent.WithPreloadSessionRecall(5),
    llmagent.WithPreloadSessionRecallInjectionMode(llmagent.PreloadSessionRecallInjectionUser),
)
```

这类 user 模式更利于保持 request prefix 稳定，但对应上下文会参与 token tailoring，极长上下文下可能被裁剪。

Post-tool guidance 默认在有工具面的 Agent 上稳定注入。需要自定义时，可以显式配置一段按 Agent 或版本固定的 prompt；如果应用已经有自己的稳定工具后指导，也可以关闭框架默认注入：

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithPostToolPrompt("Answer naturally based on tool results."),
    // llmagent.WithEnablePostToolPrompt(false),
)
```

### Prompt Cache 影响面速查

从 Prompt Cache 视角看，option 影响的位置比 option 名字更重要。下面先列“推荐优先使用或保留的能力”，再列“需要谨慎使用的动态能力”。

推荐优先使用或保留：

| 功能 / option | 主要影响位置 | 推荐理由 |
| --- | --- | --- |
| `openai.WithOptimizeForCache` | messages 顺序 | 默认 `VariantOpenAI` 开启后会把 system messages 前置，更利于自动 prefix cache；严格依赖原始顺序时再关闭 |
| 默认 post-tool guidance / `llmagent.WithPostToolPrompt` | system message | 有工具面的 Agent 会从第一次模型请求起稳定注入，避免首次 tool result 后再改写 request prefix；自定义 prompt 应按 Agent/版本固定 |
| `llmagent.WithSkillsLoadedContentInToolResults(true)` | system message / tool result | 把已加载 Skill 内容从 system message 移到匹配 tool result，减少 request prefix 污染 |
| `summary.WithCacheSafeForking(true)` | summary 生成请求 | 克隆父请求并追加 compaction user message，让 summary 生成也复用父会话前缀 |
| `llmagent.WithSessionSummaryInjectionMode(SessionSummaryInjectionUser)` | history 附近 | Summary 频繁变化时可减少对 request prefix 的改写，但需要接受可能被 token tailoring 裁剪的取舍 |
| `llmagent.WithAddCurrentTime(true)` 默认 date-only | system message / time tool | 日期级上下文同一天内稳定；工具启用时可通过 `environment_context_current_time` 获取精确时间。遗留的 `WithOutputSchema` 会禁用这个工具 |
| `agent.WithModelRequestExtraFields` 中的 `prompt_cache_key` | request body | 对支持该参数的服务方，稳定 key 可能帮助同类长前缀请求聚合；不要使用 request id / trace id |
| `llmagent.WithReasoningContentMode` 默认模式 | assistant history | 默认丢弃历史 reasoning content，可减少历史体积，避免无谓增加输入 tokens |

需要谨慎使用的动态能力：

| 功能 / option | 主要影响位置 | 对 Prompt Cache 的影响 | 建议 |
| --- | --- | --- | --- |
| `llmagent.WithToolFilter` / `agent.WithToolFilter` | tools schema | 每次过滤结果不同会改变工具列表和 tools prefix | 过滤逻辑保持确定性；同类工具集合配同类 `prompt_cache_key` |
| `llmagent.WithRefreshToolSetsOnRun(true)` | tools schema | 每次 Run 重新拉取 ToolSet，适合动态 MCP，但工具面变化会降低缓存复用 | cache-sensitive 场景尽量固定工具面；动态工具按场景分桶 |
| `llmagent.WithSkillLoadMode` / `WithMaxLoadedSkills` | loaded Skills context | 影响动态 Skill 内容驻留轮次和体积 | 控制驻留时长和数量，避免无关 Skill 长期污染前缀 |
| `llmagent.WithPreloadMemory` / `WithPreloadSessionRecall` | system message 或 history | 召回内容随 query 变化，放 system message 会更容易改写 request prefix | cache-sensitive 场景优先评估 user/history injection mode |
| `llmagent.WithTimeFormat("2006-01-02 15:04:05 MST")` | system message | 完整时间戳会按秒变化，容易破坏 request prefix | 只有明确需要每轮 system message 内含精确时间时才使用 |
| 动态 `WithPostToolPrompt` | system message | 如果按请求生成，会把本来稳定的 post-tool guidance 变成动态前缀 | 按 Agent/版本固定；请求级临时要求放到 tool result 或后置 user/history |
| `agent.WithInjectedContextMessages` / `WithLateContextMessages` | history 附近的临时消息 | 前者在 session history 之前，位置更靠前；后者贴近最新用户回合 | 临时上下文优先 late 注入，避免改写稳定前缀 |
| `BeforeModel` callback / `EventMessageProjector` | 任意 request message | 这是强扩展点，可能重写 system、tools 或 history | 遵守 append-only 思路；尽量只在靠后位置追加动态上下文 |
| Structured output schema 动态变化 | schema | schema 名称、字段顺序和描述变化都会影响前缀 | schema 尽量版本化和稳定化，避免同一类请求频繁变更 |

### 透传 prompt_cache_key

如果 OpenAI-compatible 服务方支持 `prompt_cache_key`，可以通过 request-level extra fields 透传：

```go
events, err := r.Run(
    ctx,
    "user-001",
    "session-001",
    model.NewUserMessage("Hello"),
    agent.WithModelRequestExtraFields(map[string]any{
        "prompt_cache_key": cacheKey,
    }),
)
```

`agent` 包中的 `WithModelRequestExtraFields` 说明了优先级：request-level extra fields 会优先于 model-level extra fields。OpenAI-compatible adapter 在 `model/openai` 包的请求构建阶段会先合并 model-level `extraFields`，再合并 `request.ExtraFields`，因此同名 key 下请求级值会覆盖模型级值。

需要注意，`prompt_cache_key` 不是 Prompt Cache 的开关。固定 system prompt、稳定 tools，并保持 request prefix 不变，本身就可能触发自动 prefix cache；`prompt_cache_key` 的价值是给服务端一个额外的路由提示，帮助相同类别的长前缀请求更稳定地聚合到同一缓存路径上。

`prompt_cache_key` 应表达“可复用前缀类别”，例如：

```text
app:customer-support:v3:tenant-basic-tools
app:code-review:v5:go-tools
```

不要把它设置成请求唯一 ID、trace ID 或时间戳。

### 观测 cache hit rate

OpenAI-compatible API 的主观测字段是 `cached_tokens`：

```go
cached := usage.PromptTokensDetails.CachedTokens
prompt := usage.PromptTokens
hitRate := float64(cached) / float64(prompt)
```

tRPC-Agent-Go 的 provider-agnostic usage 结构定义在 `model` 包中，其中包括：

- `model.Usage.PromptTokens`
- `model.Usage.PromptTokensDetails.CachedTokens`
- `model.Usage.PromptTokensDetails.CacheReadTokens`
- `model.Usage.PromptTokensDetails.CacheCreationTokens`

对 OpenAI-compatible API，`CachedTokens` 是主要字段；`CacheReadTokens` / `CacheCreationTokens` 主要服务于 Anthropic 等显式缓存形态。

Telemetry 层也会拆分 token 类型。`internal/telemetry` 包会记录：

- `input`
- `input_cached`
- `input_cache_read`
- `input_cache_creation`
- `output`

一个简单的统计口径是：

```text
total_prompt_tokens = sum(usage.prompt_tokens)
total_cached_tokens = sum(usage.prompt_tokens_details.cached_tokens)
requests_with_cache = count(cached_tokens > 0)
overall_hit_rate = total_cached_tokens / total_prompt_tokens
```

如果要估算成本节省，应使用服务方当前价格表和计费规则。不要把某个 provider 的缓存门槛、TTL 或折扣写成所有 OpenAI-compatible 服务的统一行为。

## 六、实验结果与复现方式

tRPC-Agent-Go 主仓库中的 benchmark 说明已经把 benchmark suites 迁移到独立的 [trpc-agent-go-benchmark](https://github.com/trpc-group/trpc-agent-go-benchmark) 仓库，其中 `anthropic_skills`、`summary` 等 suite 覆盖了 token usage、prompt cache、summary 等评测方向。对本文这类机制说明，主仓库的 `examples/promptcache/openai` 示例更适合作为轻量复现实验；如果要单独观察当前时间上下文对 Prompt Cache 的影响，可以参考 `examples/promptcache/timeprocessor`，它对比了 baseline、date-only、full-datetime 和 precise-tool 四种模式。

下面是一组 provider-neutral 的示例运行结果，用来说明是否传入 `prompt_cache_key` 的观测方式：

```text
case                 requests  requests_with_cache  prompt_tokens  cached_tokens  overall_cache_rate
without cache key    6         4                    13012          8000           61.48%
with cache key       6         5                    13918          10112          72.65%
```

这组结果最重要的不是“带 key 一定提升多少”，而是三点：

第一，不传 `prompt_cache_key` 也可以命中 Prompt Cache。OpenAI-compatible API 的核心仍然是 prefix cache routing：只要前缀足够稳定，服务端就有机会按 prefix 自动复用。

第二，传入稳定的 `prompt_cache_key` 可能提高命中概率。本次运行中，带 key 的请求从 4 次命中提升到 5 次命中，overall cache rate 从 61.48% 提升到 72.65%。这符合 `prompt_cache_key` 作为缓存路由提示的定位。

第三，tool call 没有阻止缓存命中。无论是否传入 `prompt_cache_key`，计算工具和时间工具调用轮次都出现过 cached tokens，说明 system、tools 和历史上下文中的稳定前缀仍然可以被复用。

实验复现思路：

- 准备一个超过服务方缓存门槛的长 system prompt。
- 使用同一个 session 连续运行 6 轮对话。
- 混合普通问答、calculator tool call、time tool call。
- 每轮记录 prompt tokens、cached tokens、cache hit rate、是否发生 tool call。
- 最后汇总总 prompt tokens、总 cached tokens、命中请求数、overall hit rate。

下面的 per-turn 表格主要用于诊断每轮请求的变化。

不传 `prompt_cache_key` 的单次运行结果：

```text
turn  prompt_tokens  cached_tokens  hit_rate  note
1     1592           0              0.00%     cache warmup
2     2077           0              0.00%     calculator tool
3     2167           1984           91.56%    time tool
4     2105           1792           85.13%    normal QA
5     2574           2048           79.56%    calculator tool
6     2497           2176           87.14%    normal QA
```

传入固定 `prompt_cache_key` 的单次运行结果：

```text
turn  prompt_tokens  cached_tokens  hit_rate  note
1     1592           0              0.00%     cache warmup
2     2239           1152           51.45%    calculator tool
3     2329           2112           90.68%    time tool
4     2267           2048           90.34%    normal QA
5     2784           2240           80.46%    calculator tool
6     2707           2560           94.57%    normal QA
```

这仍然只是示例运行结果，不是 SLA。两组运行的模型输出长度和历史上下文会略有差异，因此 prompt tokens 不完全相同；Prompt Cache 本身也是 best-effort 能力，命中率会受服务端路由、缓存保留时间、请求频率、prompt 是否超过门槛、前缀是否完全一致等因素影响。示例程序会打印估算节省，但实际计费应以服务方当前价格和缓存计费规则为准。

## 七、实践清单

### 稳定前缀

- [ ] System prompt 作为长期规则保持稳定，不把每轮变化的用户信息、精确时间、检索片段塞进 system message 前部。
- [ ] Tool schema、工具列表、structured output schema 尽量稳定；动态工具过滤要保证同类请求过滤结果一致。
- [ ] Post-tool guidance 按 Agent 或版本固定，自定义 `WithPostToolPrompt` 时不要按请求生成。

### 动态后置

- [ ] 当前用户输入、tool result、临时 RAG 片段、精确当前时间等动态内容放到请求后部。
- [ ] Skills 动态加载要控制驻留，优先使用 `WithSkillsLoadedContentInToolResults(true)` 减少 request prefix 变化。
- [ ] 当前时间默认使用日期级上下文；需要精确时间且工具可用时，优先用 `environment_context_current_time` 工具；如果结构化输出也需要工具，使用 `WithStructuredOutputJSONSchema` 或 `WithStructuredOutputJSON`，不要使用遗留的 `WithOutputSchema`。

### 长会话

- [ ] Summary 注入区分 preserved-head 和 cache-friendly；需要保持 request prefix 稳定时，优先评估 `WithSessionSummaryInjectionMode(SessionSummaryInjectionUser)`。
- [ ] Summary / compaction 也要 cache-safe，长会话可以启用 `summary.WithCacheSafeForking(true)`，复用父会话稳定前缀，把压缩指令追加到后面。
- [ ] Memory / session recall preload 开启后，如果召回内容随 query 变化，cache-sensitive 场景优先评估 user/history injection mode。

### 路由与观测

- [ ] `prompt_cache_key` 表达可复用前缀类别，不表达请求唯一性。
- [ ] 观测 `cached_tokens`、requests with cache、overall hit rate，并按服务方价格估算节省；短会话、低频请求、频繁变化上下文不应夸大收益。

### 扩展点

- [ ] `BeforeModel`、`EventMessageProjector`、`WithInjectedContextMessages` 这类扩展点要谨慎使用，尽量 append-only，并把动态内容放到靠后位置。

## 附录：Anthropic API 的显式缓存断点

Anthropic API 与 OpenAI-compatible API 的核心思想相同：稳定 prefix 才能复用。不同点在 API 形态。

OpenAI-compatible API 的典型形态是自动 prefix cache：服务端根据请求 prefix 自动查找和写入缓存，用户通过 `cached_tokens` 观测，通过 `prompt_cache_key` 辅助路由。

Anthropic API 则提供显式 `cache_control` breakpoint。[Anthropic Prompt Caching 文档](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)说明，缓存会引用从 `tools`、`system` 到 `messages` 中被 `cache_control` 标记块为止的完整 prompt；静态内容应放在前面，并在可复用内容末尾设置 breakpoint。

tRPC-Agent-Go 的 Anthropic adapter 提供了三个对应选项：

- `anthropic.WithCacheSystemPrompt(true)`
- `anthropic.WithCacheTools(true)`
- `anthropic.WithCacheMessages(true)`

`examples/promptcache/anthropic` 可以作为附录实验：

| 阶段 | 配置 | 观察重点 |
| --- | --- | --- |
| Phase 1 | `WithCacheSystemPrompt(true)` | system prompt 是否被读取自缓存 |
| Phase 2 | `WithCacheSystemPrompt(true)` + `WithCacheTools(true)` | tool definitions 是否进入缓存范围 |
| Phase 3 | system + tools + `WithCacheMessages(true)` | 多轮消息断点是否随对话推进，`cache_read` 是否增长 |

附录用于理解 API 形态差异。正文实践仍以 OpenAI-compatible API 和 `cached_tokens` 作为主要示例口径。

## 参考资料

- [Hugging Face Transformers: Cache strategies / KV cache](https://huggingface.co/docs/transformers/en/kv_cache)
- [OpenAI Prompt Caching](https://developers.openai.com/api/docs/guides/prompt-caching)
- [DeepSeek Context Caching](https://api-docs.deepseek.com/guides/kv_cache/)
- [DeepSeek 模型 & 价格](https://api-docs.deepseek.com/zh-cn/quick_start/pricing)
- [Lessons from building Claude Code: Prompt caching is everything](https://claude.com/blog/lessons-from-building-claude-code-prompt-caching-is-everything)
- [Anthropic Prompt Caching](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
- [Anthropic Prompt Caching Cookbook](https://github.com/anthropics/claude-cookbooks/blob/main/misc/prompt_caching.ipynb)
- [DeepSeek API introduces Context Caching on Disk](https://api-docs.deepseek.com/news/news0802)
- tRPC-Agent-Go benchmark 说明：[trpc-agent-go-benchmark](https://github.com/trpc-group/trpc-agent-go-benchmark)
- [tRPC-Agent-Go 源码](https://github.com/trpc-group/trpc-agent-go) 包引用：
  - `agent/llmagent`：LLM Agent 选项、request processors 组装、Skills 相关配置。
  - `agent`：Run options 与 request-level extra fields。
  - `internal/flow/processor`：instruction、content、post-tool、time、Skills tool result 等 request processors。
  - `internal/flow/llmflow`：Invocation 内工具快照与工具列表稳定化。
  - `model/openai`：OpenAI-compatible adapter、message cache optimization、tool conversion、extra fields 合并。
  - `session/summary`：session summary、cache-safe summary forking 相关配置。
  - `internal/tool/currenttime`：内置精确当前时间工具。
  - `model`：provider-agnostic usage 结构。
  - `internal/telemetry`：token usage metrics。
  - `examples/promptcache/openai`：OpenAI-compatible Prompt Cache 示例。
  - `examples/promptcache/summaryinjection`：summary / preload 注入模式对 Prompt Cache 的影响示例。
  - `examples/promptcache/timeprocessor`：当前时间上下文的 Prompt Cache 示例。
  - `examples/promptcache/anthropic`：Anthropic Prompt Cache 附录示例。
