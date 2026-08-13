# Advanced Configuration

## Auto Mode Configuration Options

| Option                     | Description                            | Default        |
| -------------------------- | -------------------------------------- | -------------- |
| `WithExtractor(extractor)` | Enable auto mode with LLM extractor    | nil (disabled) |
| `WithAsyncMemoryNum(n)`    | Number of background worker goroutines | 1              |
| `WithMemoryQueueSize(n)`   | Size of memory job queue               | 10             |
| `WithMemoryJobTimeout(d)`  | Timeout for each extraction job        | 30s            |

## Extraction Checkers

Checkers control when memory extraction should be triggered. By default, extraction happens on every conversation turn. Use checkers to optimize extraction frequency and reduce LLM costs.

### Available Checkers

| Checker                 | Description                                               | Example                                          |
| ----------------------- | --------------------------------------------------------- | ------------------------------------------------ |
| `CheckMessageThreshold` | Triggers when accumulated messages exceed threshold       | `CheckMessageThreshold(5)` - when messages > 5   |
| `CheckTimeInterval`     | Triggers when time since last extraction exceeds interval | `CheckTimeInterval(3*time.Minute)` - every 3 min |
| `ChecksAll`             | Combines checkers with AND logic                          | All checkers must pass                           |
| `ChecksAny`             | Combines checkers with OR logic                           | Any checker passing triggers extraction          |

### Checker Configuration Examples

```go
// Example 1: Extract when messages > 5 OR every 3 minutes (OR logic).
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithCheckersAny(
        extractor.CheckMessageThreshold(5),
        extractor.CheckTimeInterval(3*time.Minute),
    ),
)

// Example 2: Extract when messages > 10 AND every 5 minutes (AND logic).
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithChecker(extractor.CheckMessageThreshold(10)),
    extractor.WithChecker(extractor.CheckTimeInterval(5*time.Minute)),
)
```

### Model callbacks (before/after)

The extractor also supports injecting before/after model callbacks via `model.Callbacks` (structured only). This is useful for tracing, request rewriting, or short-circuiting the model call in tests.

```go
callbacks := model.NewCallbacks().RegisterBeforeModel(
    func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // You can modify args.Request or return CustomResponse.
        return nil, nil
    },
).RegisterAfterModel(
    func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // You can inspect/override args.Response.
        return nil, nil
    },
)

memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithModelCallbacks(callbacks),
)
```

### ExtractionContext

The `ExtractionContext` provides information for checker decisions:

```go
type ExtractionContext struct {
    UserKey       memory.UserKey  // User identifier.
    Messages      []model.Message // Accumulated messages since last extraction.
    LastExtractAt *time.Time      // Last extraction timestamp, nil if never extracted.
}
```

**Note**: `Messages` contains all accumulated messages since the last successful extraction. When a checker returns `false`, messages are accumulated and will be included in the next extraction. This ensures no conversation context is lost when using turn-based or time-based checkers.

## Tool Control

In auto extraction mode, `WithToolEnabled` controls whether each tool is
available. `memory_search` is exposed through `Tools()` by default,
`memory_load` is exposed once enabled, and `WithAutoMemoryExposedTools`
selectively exposes enabled write tools for hybrid usage.

**Front-end Tools** (agent-facing tools returned by `Tools()`):

| Tool            | Agent-facing default | Description                   |
| --------------- | -------------------- | ----------------------------- |
| `memory_search` | ✅ Exposed           | Search memories by query      |
| `memory_load`   | ❌ Not exposed       | Load all or recent N memories; exposed once enabled |

**Back-end Operations** (operation availability for the extractor):

| Tool            | Operation default | Agent-facing default | Description                            |
| --------------- | ----------------- | -------------------- | -------------------------------------- |
| `memory_add`    | ✅ On             | ❌ Hidden            | Add new memories                       |
| `memory_update` | ✅ On             | ❌ Hidden            | Update existing memories               |
| `memory_delete` | ✅ On             | ❌ Hidden            | Delete memories                        |
| `memory_clear`  | ❌ Off            | ❌ Hidden            | Clear all user memories (dangerous)    |

**Configuration Examples**:

```go
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
    // Front-end: enable memory_load for agent to call.
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    // Hybrid: expose memory_add so the agent can store critical facts immediately.
    memoryinmemory.WithAutoMemoryExposedTools(memory.AddToolName),
    // Back-end: disable memory_delete so extractor cannot delete.
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, false),
    // Back-end: enable memory_clear for extractor (use with caution).
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

**Note**: `WithToolEnabled` and `WithAutoMemoryExposedTools` can be called before or after
`WithExtractor` - the order does not matter.

## Comparison: Agentic Mode vs Auto Mode

| Tool            | Agentic Mode (no extractor)             | Auto Mode (with extractor)                 |
| --------------- | --------------------------------------- | ------------------------------------------ |
| `memory_add`    | ✅ Agent calls via `Tools()`            | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background |
| `memory_update` | ✅ Agent calls via `Tools()`            | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background |
| `memory_search` | ✅ Agent calls via `Tools()`            | ✅ Agent calls via `Tools()`               |
| `memory_load`   | ✅ Agent calls via `Tools()`            | ⚙️ Agent calls via `Tools()` if enabled    |
| `memory_delete` | ⚙️ Agent calls via `Tools()` if enabled | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background |
| `memory_clear`  | ⚙️ Agent calls via `Tools()` if enabled | ⚙️ Agent calls via `Tools()` if exposed; extractor uses in background if enabled |

## Memory Preloading

Both modes support preloading memories into the system prompt:

```go
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),
    // Preload options:
    // llmagent.WithPreloadMemory(0),   // Disable preloading (default).
    // llmagent.WithPreloadMemory(10),  // Adaptive preload budget 10.
    //                                  // Loads all memories when count <= 10,
    //                                  // otherwise injects top 10 search results.
    // llmagent.WithPreloadMemory(-1),  // Load all.
    //                                  // ⚠️ WARNING: Loading all memories may significantly
    //                                  //     increase token usage and API costs, especially
    //                                  //     for users with many stored memories. Consider
    //                                  //     using a positive budget for production use.
    // llmagent.WithPreloadMemory(10),  // Recommended production setting.
)
```

When preloading is enabled, memories are automatically injected into the
system prompt, giving the Agent context about the user without explicit
tool calls.

When `WithPreloadMemory(N)` uses a positive value, the framework first probes
how many memories the user has. If the count is at most `N`, it injects all
memories. If the count is larger than `N`, it switches to query-aware
`memory_search` behavior internally and injects only the top `N` relevant
results for the current user message. If query extraction is empty, the
search fails, or the search returns no matches, it falls back to directly
loading up to `N` memories.

**Injection Mechanism**: Preloaded memories are **merged** into the existing
system prompt rather than inserted as a separate system message. This ensures
the request always contains a single system message, maintaining compatibility
with models that have limited support for multiple system messages (e.g.,
Qwen3.5 series may return "System message must be at the beginning" error).

**⚠️ Important Note**: Setting the configuration to `-1` loads all memories,
which may significantly increase **Token Usage** and **API Costs**. By default,
preloading is disabled (`0`), and we recommend using positive budgets (e.g., `10-50`)
to balance performance and cost.

## Hybrid Approach

You can combine both approaches:

1. Use Auto mode for passive learning (background extraction)
2. Enable search tool for explicit memory queries
3. Preload memories for immediate context

```go
// Auto extraction + search tool + preloading.
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(extractor),
)

llmAgent := llmagent.New(
    "assistant",
    llmagent.WithModel(model),
    llmagent.WithTools(memoryService.Tools()),  // Search by default; Load is optional.
    llmagent.WithPreloadMemory(10),             // Adaptive preload budget.
)
```
