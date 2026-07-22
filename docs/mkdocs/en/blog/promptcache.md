# Reducing Agent Token Cost: Prompt Cache Engineering in tRPC-Agent-Go

> This article focuses on Prompt Cache. Prompt Cache is not a local KV store inside the framework. It is a model-service capability that reuses stable prompt prefixes. The engineering work in tRPC-Agent-Go is to shape Agent requests as "stable prefix + dynamic suffix" whenever possible, improving cache-hit probability and reducing the cost of reusable input tokens.

When LLM applications enter the Agent stage, each request becomes longer.

In a simple Chat application, the model may only see a system prompt and a few conversation turns. In an Agent application, the request prefix often contains tool schemas, structured output schemas, Skills or RAG context, historical tool calls, and tool results. A single user request may trigger multiple model calls. These calls contain a large amount of repeated content, which is where Prompt Cache becomes valuable.

This article covers four topics:

- Why Prompt Cache helps Agent response latency and token cost.
- How to understand `cached_tokens` and `prompt_cache_key` in OpenAI-compatible APIs.
- How tRPC-Agent-Go reduces cache invalidation around system prompts, tools, Skills, summary, time, and post-tool prompts.
- Which options are recommended and which dynamic capabilities should be used carefully.

The configuration and examples mainly use OpenAI-compatible APIs as the reference shape. Anthropic's explicit `cache_control` mechanism is discussed in the appendix. An OpenAI-compatible API means a service that follows the request and response shape of OpenAI Chat Completions or similar APIs. Compatibility with the protocol does not imply that every provider supports the same Prompt Cache behavior. `cached_tokens`, `prompt_cache_key`, cache thresholds, TTL, and pricing rules should always be confirmed with the actual provider.

## 1. Prompt Cache Value for Agents: Lower Latency and Lower Cost

Prompt Cache has two direct values for Agents:

1. When repeated prefixes are reused by the service, the Agent often sees lower time to first token or lower overall response latency.
2. Many model services price cached input tokens lower than uncached input tokens.

For example, the [DeepSeek pricing page](https://api-docs.deepseek.com/zh-cn/quick_start/pricing), checked on 2026-06-09, lists different prices for cached input, uncached input, and output tokens:

| Model | Cached input | Uncached input | Output |
| --- | --- | --- | --- |
| `deepseek-v4-flash` | 0.02 CNY / 1M tokens | 1 CNY / 1M tokens | 2 CNY / 1M tokens |
| `deepseek-v4-pro` | 0.025 CNY / 1M tokens | 3 CNY / 1M tokens | 6 CNY / 1M tokens |

Under this pricing, cached input for `deepseek-v4-flash` is 1/50 of uncached input, and cached input for `deepseek-v4-pro` is 1/120 of uncached input. In Agent scenarios with long system prompts, stable tools, and multi-turn history, reusable prefix tokens can therefore reduce input cost significantly.

Two caveats matter. First, this is only an example pricing structure showing why cached tokens can be cheaper. Other providers may use different discounts and rules. Second, Prompt Cache reduces the cost of reusable input tokens. It does not remove all input cost, and it does not change output token pricing.

## 2. Prompt Cache Is Provider-Side Prefix Reuse

Prompt Cache can be understood from Transformer inference. The [Hugging Face Transformers KV cache documentation](https://huggingface.co/docs/transformers/en/kv_cache) explains that autoregressive generation proceeds token by token and can reuse already computed key/value states.

That explains why reusing processed prefixes is technically meaningful. At the API layer, however, Prompt Cache should not be equated with one specific KV-cache implementation. A more practical definition is:

> Prompt Cache is a model-service capability that reuses already processed prompt prefixes. It may reuse intermediate states or provider-side cache results. The cache medium, matching granularity, retention time, and pricing are provider-specific.

For OpenAI-compatible APIs that support Prompt Cache, the service checks whether the current prompt prefix has been processed before. If it hits, the response may include `usage.prompt_tokens_details.cached_tokens`, which reports how many prompt tokens were reused from cache. [OpenAI Prompt Caching](https://developers.openai.com/api/docs/guides/prompt-caching) also emphasizes a key practice: put static content at the beginning of the prompt and dynamic content later.

This also defines the boundary of `prompt_cache_key`. It is not the cache content itself, and it is not required to enable Prompt Cache. Without `prompt_cache_key`, the service may still cache and reuse prompt prefixes automatically. `prompt_cache_key` is better understood as an auxiliary routing hint. When many requests share the same long prefix category, a stable cache key may help the provider route them in a way that improves reuse. If the key contains request IDs, trace IDs, or timestamps, it reduces reuse opportunities.

[DeepSeek Context Caching](https://api-docs.deepseek.com/guides/kv_cache/) describes the same idea from another OpenAI-compatible service shape: when later requests overlap with earlier prefixes, the overlapping part may be read from cache. Multi-turn chat and long-document Q&A examples are both forms of prefix reuse.

## 3. Why Agents Need Prompt Cache

Agent requests naturally contain large stable prefixes. A typical LLM Agent request may include:

- system prompt, global instruction, and Agent identity.
- tool schema, function declarations, and structured output schema.
- Skills overview, capability descriptions, or fixed workflow instructions.
- session history, previous tool calls, and previous tool results.
- RAG, memory, or workspace context.

Multi-turn conversations naturally form "old context + new message":

```text
turn 1: system + tools + user_1
turn 2: system + tools + user_1 + assistant_1 + user_2
turn 3: system + tools + user_1 + assistant_1 + user_2 + assistant_2 + user_3
```

From the second turn onward, the new request includes most of the previous content. If system messages, tools, and history are not rewritten, the provider has a chance to reuse those prefixes.

Tool call and ReAct flows amplify this. One user request may trigger several model calls: the first call decides which tool to call; the second call receives the tool result and continues reasoning; later calls may call more tools. Every model call repeats system prompts, tools, and accumulated context. The longer and more stable the prefix is, the larger the Prompt Cache opportunity. If dynamic content frequently interrupts the prefix, the hit rate drops.

The Agent framework therefore should not implement model-side caching. Its job is to shape requests for cache-friendly prefix reuse:

```text
recommended shape:
[stable prefix] system / instruction / tools / schema / stable skills overview
              + session history / previous tool call / previous tool result
[dynamic suffix] current user message / current tool result / current time / temporary context

avoid:
system / instruction being rewritten by summary, exact time, RAG snippets, or temporary user state
```

tRPC-Agent-Go Prompt Cache optimizations are built around this shape.

## 4. How tRPC-Agent-Go Shapes Cache-Friendly Requests

tRPC-Agent-Go does not store model KV locally and does not replace provider Prompt Cache. It organizes requests so stable content appears early and remains stable, dynamic content appears later, and ordering is deterministic.

Three terms are easy to mix up:

- `system prompt` means the system rules text, such as Agent identity, response style, or post-tool guidance.
- `system message` means a request message with `role=system`.
- `model request prefix` means the continuous prefix seen by the model service. It may include system messages, instruction, tools, schema, and stable history.

The relationship is: system prompt is usually written into a system message; the system message and other stable structures form the model request prefix. Prompt Cache cares about whether the full prefix is stable, so both content and position matter.

The main handling points are:

| Area | What the framework does | Cache invalidation reduced |
| --- | --- | --- |
| Request processors | Build instruction / system / Skills overview first, then append history, tool results, and time | Avoids dynamic content entering the prefix too early |
| Request prefix | Merge global instruction and instruction into an early prefix | Avoids rule and identity position drift |
| Message ordering | The OpenAI-compatible adapter can move system messages to the front | Keeps stable system messages early for automatic prefix cache |
| Tools | Cache the tools snapshot inside one invocation and sort converted tools by name | Avoids random tool ordering or map iteration effects |
| Post-tool guidance | Inject stable post-tool guidance from the first model request when tools exist | Avoids rewriting the prefix only after the first tool result |
| Skills | Prefer materializing loaded Skill content into tool results | Avoids dynamic Skill content polluting the prefix |
| Summary generation | Cache-safe forking reuses the parent request prefix and appends only a compaction user message | Avoids reorganizing the summary request from scratch |
| Summary injection | Summary can be injected near user/history messages | Avoids rewriting the prefix when summary updates frequently |
| Current time | Date-only context by default; exact time through a tool | Avoids changing the prefix every second |

### Request Processors: Stable Content First

**Risk**: Agent requests are assembled from instruction, Skills, history, tool results, time, and other context. If dynamic content enters the prefix too early, later stable content can no longer be reused effectively.

**Framework behavior**: `LLMAgent` constructs model requests through processors. Instruction runs early and merges global instruction / instruction into the system message. Skills overview and load state are added. Conversation and context history are appended. Post-tool guidance is injected early for tool-enabled Agents. Loaded Skill content is moved into matching tool results when possible. Time processing runs later, and the default current-time behavior uses date-level context while exact time is available through a tool.

**Suggestion**: Applications should follow the same order. Stable rules, identity, tools, and capability descriptions should stay early. Current user input, tool results, temporary retrieval snippets, and precise time should stay later.

### Request Prefix: Stable System Rules

**Risk**: System prompt is usually part of the most valuable cache prefix. If user state, retrieval results, or precise time are written into the system message every turn, later stable history cannot be reused well.

**Framework behavior**: `InstructionRequestProcessor` finds an existing system message or creates one at the beginning of `req.Messages`. It merges system prompt and instruction into that message, keeping global rules early and stable.

**Suggestion**: Treat instruction as long-lived rules, not per-turn context. Per-turn information belongs later in user, history, or tool-result messages.

### Message Ordering: Move System Messages to the Front

**Risk**: If system messages appear in unstable positions or are interleaved with history, automatic prefix cache has less stable content to reuse.

**Framework behavior**: `model/openai` provides `openai.WithOptimizeForCache`. It is enabled by default for `VariantOpenAI`. When enabled, the adapter moves system messages to the front before sending the request.

**Suggestion**: This is not explicit cache creation. It is a cache-friendly request layout for OpenAI-compatible APIs. Keep it enabled unless the application strictly depends on original message order:

```go
llm := openai.New("your-model",
    openai.WithOptimizeForCache(false),
)
```

### Tools: Stable Tool Sets and Ordering

**Risk**: Tool schemas are often large static blocks in Agent requests. Unstable tool ordering, dynamic filtering, or random map iteration can break prefix reuse.

**Framework behavior**: tRPC-Agent-Go stabilizes tools at two layers. The flow layer caches the final visible tool list inside one invocation. Filtered tools are sorted by name. The OpenAI-compatible adapter also sorts tool names before converting them to the OpenAI-compatible `tools` parameter.

**Suggestion**: The framework stabilizes order, but the application still controls the tool surface. Use dynamic filters, `WithRefreshToolSetsOnRun(true)`, and dynamic MCP tool lists carefully. If dynamic tools are required, bucket requests by scenario so similar requests see stable tool sets.

### Post-Tool Guidance: Inject Early and Stably

**Risk**: After a tool call, the model sees a `role=tool` result. A framework may add post-tool guidance to make the final answer natural. If that guidance is appended to the system message only after the first tool result, the prefix changes in the middle of the request flow.

**Framework behavior**: tRPC-Agent-Go injects post-tool guidance stably from the first model request when an Agent has a potential tool surface. Later ReAct or tool-loop rounds do not rewrite the prefix just because a tool result has appeared.

**Suggestion**: Use `llmagent.WithPostToolPrompt("...")` for custom guidance or `llmagent.WithEnablePostToolPrompt(false)` to disable it. Custom prompts should be stable by Agent or version. Per-request temporary requirements should go into tool results or later user/history messages.

### Skills: Put Dynamic Content into Tool Results

**Risk**: Loaded Skill body or selected docs often vary by task. If they are appended to the system message every turn, the request prefix changes frequently.

**Framework behavior**: `llmagent.WithSkillsLoadedContentInToolResults(true)` moves loaded Skill content into matching `skill_load` or `skill_select_docs` tool results when possible, instead of adding it to the system message.

**Suggestion**: This does not reduce context. It puts dynamic context in a better location. For task-specific Skill content, enable this option and control residency with `WithSkillLoadMode(...)` and `WithMaxLoadedSkills(...)`:

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
    llmagent.WithMaxLoadedSkills(8),
)
```

### Summary Generation: Cache-Safe Forking

**Risk**: Long sessions often need summary or compaction. Summary generation itself can destroy cache reuse if it sends a completely separate prompt containing the whole conversation.

**Framework behavior**: tRPC-Agent-Go provides optional cache-safe summary forking. The summary request can clone the parent model request and append only a compaction user message:

```text
parent request:
system + tools + stable context + history_1...history_N + new_user_message

cache-safe compaction fork:
system + tools + stable context + history_1...history_N + compact_user_message
```

**Suggestion**: This is opt-in to preserve compatibility and support cases where the summary model differs from the main Agent model. For long sessions using the same model and sensitive to Prompt Cache, evaluate:

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithContextThreshold(),
    summary.WithMaxSummaryWords(200),
    summary.WithCacheSafeForking(true),
)
```

When customizing the fork prompt with `summary.WithCacheSafeForkPrompt(...)`, do not include `{conversation_text}` or `{previous_summary}` again. The parent request already contains the conversation prefix and any injected summary.

### Summary Injection: Avoid Rewriting the Prefix

**Risk**: After a summary is generated, ordinary requests still need to decide where to inject it. System injection keeps summary in the preserved head, but frequent summary updates rewrite the early prefix.

**Framework behavior**: Cache-sensitive scenarios can use `WithSessionSummaryInjectionMode(SessionSummaryInjectionUser)` to place the summary near the first user/history/current message. This keeps the prefix more stable, at the cost that summary may participate in token-budget trimming.

**Suggestion**: Use system injection when preserved-head authority matters more. Use user/history injection when prefix stability matters more. Cache-safe forking optimizes summary generation; injection mode optimizes ordinary requests after summary exists.

### Current Time: Date-Level Context and Exact-Time Tool

**Risk**: Current time is a common Prompt Cache trap. If every request writes a full timestamp into the system message, the prefix changes every second.

**Framework behavior**: tRPC-Agent-Go keeps the existing `WithAddCurrentTime` entry point but changes the default semantics to date-level context. When tools are available, the model can call the built-in `environment_context_current_time` tool for precise time. Legacy `WithOutputSchema` disables all tools, including this one; if you need both structured output and the exact-time tool, use `WithStructuredOutputJSONSchema` or `WithStructuredOutputJSON`.

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithAddCurrentTime(true),
)
```

**Suggestion**: The default date-only path is more cache-friendly. If you explicitly configure a full time format such as `WithTimeFormat("2006-01-02 15:04:05 MST")`, the old full timestamp behavior is still available, but it changes the request prefix more often.

## 5. What Users Can Configure and Observe

Prompt Cache hits happen on the model-service side, but users can still control request shape, pass provider-specific parameters, and observe cache effects through tRPC-Agent-Go.

### Configure Cache-Friendly Request Shape

With the default `VariantOpenAI` configuration, the OpenAI-compatible adapter optimizes message ordering for cache:

```go
llm := openai.New("your-model")
```

If using another provider variant, check whether `openai.WithOptimizeForCache(true)` should be enabled. If the application depends on original message order, disable it:

```go
llm := openai.New("your-model",
    openai.WithOptimizeForCache(false),
)
```

Dynamic Skills content should go into tool results:

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithSkillsLoadedContentInToolResults(true),
    llmagent.WithSkillLoadMode(llmagent.SkillLoadModeTurn),
    llmagent.WithMaxLoadedSkills(8),
)
```

For long-session summary, consider both summary generation and summary injection:

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithContextThreshold(),
    summary.WithMaxSummaryWords(200),
    summary.WithCacheSafeForking(true),
)
```

Use user/history injection when you want to avoid rewriting the first system message:

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithAddSessionSummary(true),
    llmagent.WithSessionSummaryInjectionMode(llmagent.SessionSummaryInjectionUser),
)
```

Memory preload and session recall preload default to system context for compatibility. Cache-sensitive scenarios can evaluate user/history modes:

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithPreloadMemory(10),
    llmagent.WithPreloadMemoryInjectionMode(llmagent.PreloadMemoryInjectionUser),
    llmagent.WithPreloadSessionRecall(5),
    llmagent.WithPreloadSessionRecallInjectionMode(llmagent.PreloadSessionRecallInjectionUser),
)
```

Post-tool guidance is stable by default when tools exist:

```go
agent := llmagent.New(
    "assistant",
    llmagent.WithPostToolPrompt("Answer naturally based on tool results."),
    // llmagent.WithEnablePostToolPrompt(false),
)
```

### Prompt Cache Impact Quick Reference

Recommended capabilities:

| Feature / option | Main location | Reason |
| --- | --- | --- |
| `openai.WithOptimizeForCache` | message order | Moves system messages to the front under default `VariantOpenAI`, improving automatic prefix cache |
| default post-tool guidance / `llmagent.WithPostToolPrompt` | system message | Injected from the first model request when tools exist; custom prompt should be stable by Agent/version |
| `llmagent.WithSkillsLoadedContentInToolResults(true)` | system message / tool result | Moves loaded Skill content from system message into matching tool results |
| `summary.WithCacheSafeForking(true)` | summary generation request | Clones the parent request and appends a compaction message, reusing the parent prefix |
| `llmagent.WithSessionSummaryInjectionMode(SessionSummaryInjectionUser)` | near history | Reduces prefix rewrite when summary changes frequently, with trimming tradeoff |
| `llmagent.WithAddCurrentTime(true)` default date-only | system message / time tool | Date-level context is stable; precise time is available through `environment_context_current_time` when tools are enabled. Legacy `WithOutputSchema` disables this tool |
| `agent.WithModelRequestExtraFields` with `prompt_cache_key` | request body | A stable key may help providers route similar long-prefix requests |
| default `llmagent.WithReasoningContentMode` | assistant history | Drops historical reasoning content by default, reducing unnecessary input tokens |

Dynamic capabilities to use carefully:

| Feature / option | Main location | Prompt Cache impact | Suggestion |
| --- | --- | --- | --- |
| `llmagent.WithToolFilter` / `agent.WithToolFilter` | tools schema | Different filter results change the tool prefix | Keep filtering deterministic; bucket similar tool sets |
| `llmagent.WithRefreshToolSetsOnRun(true)` | tools schema | Re-fetching tools can change the visible tool surface | Fix the tool surface in cache-sensitive paths when possible |
| `llmagent.WithSkillLoadMode` / `WithMaxLoadedSkills` | loaded Skills context | Affects how long dynamic Skill content stays in context | Control residency and size |
| `llmagent.WithPreloadMemory` / `WithPreloadSessionRecall` | system message or history | Query-dependent recall in system message rewrites the prefix | Prefer user/history injection in cache-sensitive scenarios |
| `llmagent.WithTimeFormat("2006-01-02 15:04:05 MST")` | system message | Full timestamp changes frequently | Use only when exact time must be in system context |
| dynamic `WithPostToolPrompt` | system message | Turns stable guidance into a dynamic prefix | Keep it stable; put temporary instructions later |
| `agent.WithInjectedContextMessages` / `WithLateContextMessages` | near history | Injected messages can appear before stable history | Prefer late injection for temporary context |
| `BeforeModel` / `EventMessageProjector` | any request message | Can rewrite system, tools, or history | Prefer append-only changes near the end |
| dynamic structured output schema | schema | Schema name, order, or description changes affect prefix | Version and stabilize schemas |

### Passing `prompt_cache_key`

If the provider supports `prompt_cache_key`, pass it through request-level extra fields:

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

Request-level extra fields take precedence over model-level extra fields. The OpenAI-compatible adapter merges model-level `extraFields` first and then `request.ExtraFields`, so request-level values override matching keys.

`prompt_cache_key` should describe a reusable prefix category, for example:

```text
app:customer-support:v3:tenant-basic-tools
app:code-review:v5:go-tools
```

Do not use request IDs, trace IDs, or timestamps.

### Observing Cache Hit Rate

For OpenAI-compatible APIs, the main observation field is `cached_tokens`:

```go
cached := usage.PromptTokensDetails.CachedTokens
prompt := usage.PromptTokens
hitRate := float64(cached) / float64(prompt)
```

tRPC-Agent-Go defines provider-agnostic usage fields in the `model` package:

- `model.Usage.PromptTokens`
- `model.Usage.PromptTokensDetails.CachedTokens`
- `model.Usage.PromptTokensDetails.CacheReadTokens`
- `model.Usage.PromptTokensDetails.CacheCreationTokens`

For OpenAI-compatible APIs, `CachedTokens` is the main field. `CacheReadTokens` and `CacheCreationTokens` primarily serve explicit cache mechanisms such as Anthropic Prompt Caching.

Telemetry also splits token types:

- `input`
- `input_cached`
- `input_cache_read`
- `input_cache_creation`
- `output`

A simple aggregate view is:

```text
total_prompt_tokens = sum(usage.prompt_tokens)
total_cached_tokens = sum(usage.prompt_tokens_details.cached_tokens)
requests_with_cache = count(cached_tokens > 0)
overall_hit_rate = total_cached_tokens / total_prompt_tokens
```

Cost savings should be estimated with the actual provider pricing and billing rules.

## 6. Experiment Results and Reproduction

The main repository notes that benchmark suites have moved to [trpc-agent-go-benchmark](https://github.com/trpc-group/trpc-agent-go-benchmark). Suites such as `anthropic_skills` and `summary` cover token usage, Prompt Cache, and summary behavior. For a lightweight mechanism demo, `examples/promptcache/openai` in the main repository is easier to run. To observe current-time context specifically, see `examples/promptcache/timeprocessor`.

The following provider-neutral sample run illustrates how to compare requests with and without `prompt_cache_key`:

```text
case                 requests  requests_with_cache  prompt_tokens  cached_tokens  overall_cache_rate
without cache key    6         4                    13012          8000           61.48%
with cache key       6         5                    13918          10112          72.65%
```

The conclusion is not that "a key always improves by this amount." The useful observations are:

First, Prompt Cache can hit without `prompt_cache_key`. Stable prefixes are still the foundation.

Second, a stable `prompt_cache_key` may improve hit probability. In this run, hit requests increased from 4 to 5 and the overall cache rate increased from 61.48% to 72.65%.

Third, tool calls do not prevent cache hits. Calculator and time-tool turns can still reuse the stable prefix made of system messages, tools, and history.

Reproduction outline:

- Prepare a long system prompt that exceeds the provider's cache threshold.
- Use the same session for 6 conversation turns.
- Mix normal Q&A, calculator tool calls, and time tool calls.
- Record prompt tokens, cached tokens, hit rate, and whether a tool call happened on each turn.
- Aggregate total prompt tokens, total cached tokens, hit requests, and overall hit rate.

Per-turn sample without `prompt_cache_key`:

```text
turn  prompt_tokens  cached_tokens  hit_rate  note
1     1592           0              0.00%     cache warmup
2     2077           0              0.00%     calculator tool
3     2167           1984           91.56%    time tool
4     2105           1792           85.13%    normal QA
5     2574           2048           79.56%    calculator tool
6     2497           2176           87.14%    normal QA
```

Per-turn sample with a stable `prompt_cache_key`:

```text
turn  prompt_tokens  cached_tokens  hit_rate  note
1     1592           0              0.00%     cache warmup
2     2239           1152           51.45%    calculator tool
3     2329           2112           90.68%    time tool
4     2267           2048           90.34%    normal QA
5     2784           2240           80.46%    calculator tool
6     2707           2560           94.57%    normal QA
```

This is still only an example run, not an SLA. Model output length and history may differ between runs, so prompt token counts may not match exactly. Prompt Cache is a best-effort provider capability affected by routing, retention time, request frequency, cache threshold, and exact prefix stability.

## 7. Practice Checklist

### Stable Prefix

- [ ] Keep system prompt as long-lived rules. Do not put per-turn user state, exact time, or retrieval snippets at the front of system messages.
- [ ] Keep tool schemas, tool lists, and structured output schemas stable. Dynamic tool filtering should be deterministic for similar requests.
- [ ] Keep post-tool guidance stable by Agent or version. Do not generate it per request.

### Dynamic Content Later

- [ ] Put current user input, tool results, temporary RAG snippets, and exact current time later in the request.
- [ ] Control loaded Skills residency and prefer `WithSkillsLoadedContentInToolResults(true)`.
- [ ] Use date-level current-time context by default. Use `environment_context_current_time` when precise time is needed and tools are enabled; use `WithStructuredOutputJSONSchema` or `WithStructuredOutputJSON` instead of legacy `WithOutputSchema` if structured output also needs tools.

### Long Sessions

- [ ] Choose summary injection based on preserved-head authority versus cache friendliness.
- [ ] Enable `summary.WithCacheSafeForking(true)` for long sessions that can reuse the parent request prefix.
- [ ] If memory or session recall content changes by query, evaluate user/history injection modes.

### Routing and Observation

- [ ] Make `prompt_cache_key` describe a reusable prefix category, not request uniqueness.
- [ ] Observe `cached_tokens`, requests with cache, and overall hit rate. Estimate savings with provider pricing.
- [ ] Do not overstate benefits for short sessions, low-frequency traffic, or frequently changing context.

### Extension Points

- [ ] Use `BeforeModel`, `EventMessageProjector`, and injected context messages carefully. Prefer append-only changes near the end of the request.

## Appendix: Anthropic Explicit Cache Breakpoints

Anthropic API follows the same principle: stable prefixes are reusable. The API shape is different.

OpenAI-compatible APIs usually provide automatic prefix cache: the service checks prompt prefixes and reports `cached_tokens`, optionally using `prompt_cache_key` as a routing hint.

Anthropic provides explicit `cache_control` breakpoints. [Anthropic Prompt Caching](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching) explains that cache covers the prompt from `tools` and `system` through the marked block in `messages`. Static content should be placed first, with a breakpoint at the end of reusable content.

tRPC-Agent-Go's Anthropic adapter provides related options:

- `anthropic.WithCacheSystemPrompt(true)`
- `anthropic.WithCacheTools(true)`
- `anthropic.WithCacheMessages(true)`

`examples/promptcache/anthropic` can be used as an appendix experiment:

| Phase | Configuration | Observation |
| --- | --- | --- |
| Phase 1 | `WithCacheSystemPrompt(true)` | Whether system prompt is read from cache |
| Phase 2 | `WithCacheSystemPrompt(true)` + `WithCacheTools(true)` | Whether tool definitions enter the cache range |
| Phase 3 | system + tools + `WithCacheMessages(true)` | Whether multi-turn message breakpoints move forward and `cache_read` grows |

The appendix helps explain API differences. The main practice still uses OpenAI-compatible APIs and `cached_tokens` as the primary example shape.

## References

- [Hugging Face Transformers: Cache strategies / KV cache](https://huggingface.co/docs/transformers/en/kv_cache)
- [OpenAI Prompt Caching](https://developers.openai.com/api/docs/guides/prompt-caching)
- [DeepSeek Context Caching](https://api-docs.deepseek.com/guides/kv_cache/)
- [DeepSeek Pricing](https://api-docs.deepseek.com/zh-cn/quick_start/pricing)
- [Lessons from building Claude Code: Prompt caching is everything](https://claude.com/blog/lessons-from-building-claude-code-prompt-caching-is-everything)
- [Anthropic Prompt Caching](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching)
- [Anthropic Prompt Caching Cookbook](https://github.com/anthropics/claude-cookbooks/blob/main/misc/prompt_caching.ipynb)
- [DeepSeek API introduces Context Caching on Disk](https://api-docs.deepseek.com/news/news0802)
- [trpc-agent-go-benchmark](https://github.com/trpc-group/trpc-agent-go-benchmark)
- [tRPC-Agent-Go source](https://github.com/trpc-group/trpc-agent-go) package references:
  - `agent/llmagent`: LLM Agent options, request processors, and Skills-related configuration.
  - `agent`: run options and request-level extra fields.
  - `internal/flow/processor`: instruction, content, post-tool, time, and Skills tool-result request processors.
  - `internal/flow/llmflow`: invocation-level tool snapshots and tool-list stabilization.
  - `model/openai`: OpenAI-compatible adapter, message cache optimization, tool conversion, and extra-field merging.
  - `session/summary`: session summary and cache-safe summary forking.
  - `internal/tool/currenttime`: built-in precise current-time tool.
  - `model`: provider-agnostic usage structures.
  - `internal/telemetry`: token usage metrics.
  - `examples/promptcache/openai`: OpenAI-compatible Prompt Cache example.
  - `examples/promptcache/summaryinjection`: summary and preload injection mode examples.
  - `examples/promptcache/timeprocessor`: current-time context Prompt Cache example.
  - `examples/promptcache/anthropic`: Anthropic Prompt Cache appendix example.
