# ToolPipe Extension

ToolPipe is an agent-scoped extension that injects shell-like result filtering capabilities into selected tools.

## Core Philosophy

**Precision feeding to improve model attention quality.**

When tools return large amounts of data, dumping everything into context not only wastes tokens — more critically, it **dilutes model attention**. ToolPipe's value is not about "saving tokens" per se, but ensuring the model sees **only useful information** within its limited attention window.

Large output philosophy: models should never be overwhelmed by raw large output. ToolPipe automatically windows output (preserving head+tail outline) and enables on-demand precise filtering (shell-like pipes), keeping the model working in a focused, streamlined context.

ToolPipe's unique positioning: **a controlled "result projection" layer for Agents without a local shell environment**. Especially suited for third-party MCP tools, web searches, API responses, and other scenarios where the tool definition cannot be modified and output size is unpredictable.

## Sweet Spot vs Anti-Patterns

### ✅ Ideal Scenarios (Large Data + Small Target + Structurally Filterable)

- **Log debugging**: grep 5 ERROR lines from 10,000 lines of logs
- **API/MCP field extraction**: extract only title + url from search result JSON
- **Web page section lookup**: grep 2KB about authentication from an 80KB page
- **Config/Schema extraction**: view only route list from a large OpenAPI spec
- **Structure extraction**: extract only headings from a large document

Common traits: large data, small target, target describable via grep/jq, no full-text understanding needed.

### ❌ Anti-Patterns

- **Summarize an entire article**: requires full-text understanding
- **Compare two documents**: requires full content
- **Small data sources**: results are already small (< maxOutput), filter adds unnecessary round trips
- **Vague targets / full-text scan needed**: model doesn't know what to grep, will probe repeatedly

### Benchmark Results

Token consumption may increase or decrease depending on scenario and model strategy. **Tokens are not the core metric** — the core metric is signal-to-noise ratio per turn and answer precision.

| Scenario | Token Change | Peak Context Change | Notes |
|----------|-------------|-------------------|-------|
| JSON field extraction (Algolia API) | -88% | -96% | Best case |
| Structure extraction (doc headings) | -99% | -99% | Best case |
| Large page section lookup (defer) | -65% | -93% | Good |
| Needle in haystack (2 fact Q&A) | -34% | -86% | Good |
| Small data vague search | +235% | +86% | Not suitable |

Key observation: even in the "needle in haystack" scenario, toolpipe mode produced **more complete and accurate answers** — because focused context improves model attention. This matters more than token counts.

## How It Works

ToolPipe uses three callbacks (BeforeModel + BeforeTool + AfterTool) with no core framework modifications:

1. **BeforeModel**: appends an optional `result_filter` field to selected tools' InputSchema; injects a system prompt guiding model usage.
2. **BeforeTool**: strips `result_filter` from tool arguments, parses it into a pipeline AST, stores in context.
3. **AfterTool**:
   - Filter present → execute pipeline on result, return filtered content
   - No filter but result exceeds maxOutput → automatic head+tail windowing with `truncated: true` + `total_bytes`
   - No filter and result is small → pass through unchanged

### Windowing Strategy (No Filter)

Uses head+tail truncation: preserves the beginning and end of output, marks omitted bytes in the middle. This gives the model structural overview so it can either work directly or write a precise filter — **without encouraging multi-round full-text reconstruction**.

## Usage

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/extension/toolpipe"

agent := llmagent.New("researcher",
    llmagent.WithTools([]tool.Tool{webFetchTool, mcpSearchTool}),
    llmagent.WithExtensions(
        toolpipe.New(
            toolpipe.WithToolNames("web_fetch", "mcp_search"),
            toolpipe.WithAllowedOps(toolpipe.OpGrep, toolpipe.OpHead, toolpipe.OpTail, toolpipe.OpJQ),
            toolpipe.WithMaxOutputBytes(32<<10), // 32KB window
        ),
    ),
)
```

### Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `WithToolNames(names...)` | Allowlist: which tools get filter capability | empty (must specify) |
| `WithToolScope(fn)` | Dynamic selector (e.g., prefix-match MCP tools) | nil |
| `WithAllowedOps(ops...)` | Allowed filter operations | grep, head, tail |
| `WithMaxOutputBytes(n)` | Window size (truncation threshold when no filter) | 64KB |
| `WithMaxInputBytes(n)` | Max input before filtering | 2MB |
| `WithFilterField(name)` | Injected parameter field name | `"result_filter"` |
| `WithPrompt(text)` | Custom guidance prompt (empty string = disable) | built-in default |

### Supported Filter Operations

Models can write shell-like pipeline syntax:

```sh
grep ERROR                     — match lines
grep -i timeout                — case insensitive
grep -v DEBUG                  — exclude lines
head -20                       — first N lines
tail -10                       — last N lines
jq '.results[] | .title'       — JSON query
jq -r '.content'               — JSON extract as raw text
grep ERROR | head 5            — combined pipeline
jq -r '.items[].name' | grep Go | head 10
```

Uses `mvdan.cc/sh/v3` for shell syntax parsing (parse only, never execute), validates commands against allowlist, rejects redirections, variable assignments, command substitution, and other unsafe constructs.

## Design Principles and Trade-offs

### Design Principles

1. **Allowlist only**: only explicitly selected tools are augmented. Never enable for write operations, approvals, or state-modifying tools.
2. **Same-name augmentation**: does not rename tools or add new tools, only appends an optional field. Zero impact on dispatch, tracing, tool filtering.
3. **Fail safe**: filter parse failure still strips the field (prevents leaking to original tool); tool execution failure passes through error without filtering.
4. **Framework doesn't teach strategy**: prompt only describes capability and format, never prescribes usage. Strategy belongs in user's Instruction.
5. **Framework tools auto-skipped**: the following tool types are automatically excluded, even if in the allowlist:
   - Tools implementing `StreamInner()` or `InnerTextMode()` (e.g., AgentTool)
   - Known framework built-in tools (`transfer_to_agent`, `await_user_reply`)
   - Tools with `LongRunning()` returning true
   - Tools implementing `StateDelta` / `StateDeltaForInvocation` (e.g., todo, artifact, skill tools)
   
   These tools' output is framework-semantic or consumed by framework state machinery, not user-data suitable for grep/jq projection.

### Trade-offs

- **Multi-round**: model may make multiple filter calls for precise extraction, increasing total tokens and latency.
- **Not for full-text tasks**: if the task requires reading the entire text, windowing forces multi-round reconstruction.
- **Unpredictable model behavior**: model may overuse `result_filter` even when data is small.
- **Non-idempotent tool risk**: the "re-call for lookup" pattern assumes tools are idempotent. Tools with side effects or unstable results may have issues.

### When NOT to Use

- Tool results are typically small (< maxOutput)
- Task is "summarize/translate/compare" entire content
- Tool has side effects or is non-idempotent
- Target is vague, requires full-text scan to determine what to extract

## Independent Module

ToolPipe is an independent Go module (`agent/extension/toolpipe/go.mod`), depending on `mvdan.cc/sh/v3` and `github.com/itchyny/gojq`. Users not using toolpipe will not pull in these dependencies.
