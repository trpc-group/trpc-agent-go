# Memory Usage Guide

## Overview

The Memory module is the memory management system in the tRPC-Agent-Go
framework, providing Agents with persistent memory and context management
capabilities. By integrating memory services, session management, and memory
tools, the Memory system helps Agents remember user information, maintain
dialog context, and provide personalized response experiences across multiple
conversations.

### Positioning

Memory manages long-term user information with isolation dimension
`<appName, userID>`. It can be understood as a "personal profile" gradually
accumulated around a single user.

In cross-session scenarios, Memory enables the system to retain key user
information, avoiding repetitive information gathering in each session.

It is suitable for recording stable, reusable facts such as "user name is
John", "occupation is backend engineer", "prefers concise answers", "commonly
used language is English", and directly using this information in subsequent
interactions.

### Two Memory Modes

Memory supports two modes for creating and managing memories. Choose based on your scenario:

Auto Mode is available when an Extractor is configured and is recommended as the default choice.

| Aspect              | Agentic Mode (Tools)                           | Auto Mode (Extractor)                                     |
| ------------------- | ---------------------------------------------- | --------------------------------------------------------- |
| **How it works**    | Agent decides when to call memory tools        | System extracts memories automatically from conversations |
| **User experience** | Visible - user sees tool calls                 | Transparent - memories created silently in background     |
| **Control**         | Agent has full control over what to remember   | Extractor decides based on conversation analysis          |
| **Available tools** | `memory_add`, `memory_update`, `memory_search`, `memory_load` by default; delete/clear configurable | `memory_search` exposed by default; `memory_load` exposed once enabled; enabled write operations are used by the extractor and hidden from the agent unless explicitly exposed |
| **Processing**      | Synchronous - during response generation       | Asynchronous - background workers after response          |
| **Best for**        | Precise control, user-driven memory management | Natural conversations, hands-off memory building          |

**Selection Guide**:

- **Agentic Mode**: Agent automatically decides when to call memory tools based on conversation content (e.g., when user mentions personal information or preferences), user sees tool calls, suitable for scenarios requiring precise control over memory content
- **Auto Mode (recommended)**: Natural conversation flow, system passively learns about users, simplified UX

## Core Values

- **Context Continuity**: Maintain user history across sessions, avoiding
  repetitive questioning and input.
- **Personalized Service**: Provide customized responses and suggestions based
  on long-term user profiles and preferences.
- **Knowledge Accumulation**: Transform facts and experiences from
  conversations into reusable knowledge.
- **Persistent Storage**: Support multiple storage backends to ensure data
  safety and reliability.

## Use Cases

The Memory module is suitable for scenarios requiring cross-session user
information and context retention:

### Use Case 1: Personalized Customer Service Agent

**Requirement**: Customer service Agent needs to remember user information,
historical issues, and preferences for consistent service.

**Implementation**:

- First conversation: Agent uses `memory_add` to record name, company, contact
- Record user preferences like "prefers concise answers", "technical
  background"
- Subsequent sessions: Agent uses `memory_load` to load user info, no repeated
  questions needed
- After resolving issues: Use `memory_update` to update issue status

### Use Case 2: Learning Companion Agent

**Requirement**: Educational Agent needs to track student learning progress,
knowledge mastery, and interests.

**Implementation**:

- Use `memory_add` to record mastered knowledge points
- Use topic tags for categorization: `["math", "geometry"]`,
  `["programming", "Python"]`
- Use `memory_search` to query related knowledge, avoid repeated teaching
- Adjust teaching strategies based on memories, provide personalized learning
  paths

### Use Case 3: Project Management Agent

**Requirement**: Project management Agent needs to track project information,
team members, and task progress.

**Implementation**:

- Record key project info: `memory_add("Project X uses Go language",
["project", "tech-stack"])`
- Record team member roles: `memory_add("John Doe is backend lead",
["team", "role"])`
- Use `memory_search` to quickly find relevant information
- After project completion: Use `memory_clear` to clear temporary information

## Quick Start

### Requirements

- Go 1.21 or later.
- A valid LLM API key (OpenAI-compatible endpoint).
- Redis service (optional for production).

### Environment Variables

```bash
# OpenAI API configuration
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
```

### Agentic Mode Configuration (Optional)

In Agentic mode, the Agent automatically decides when to call memory tools
based on conversation content to manage memories. Configuration involves three steps:

```go
package main

import (
    "context"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // Step 1: Create memory service.
    memoryService := memoryinmemory.NewMemoryService()

    // Step 2: Create Agent and register memory tools.
    modelInstance := openai.New("deepseek-v4-flash")
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("An assistant with memory capabilities."),
        llmagent.WithInstruction(
            "Remember important user info and recall it when needed.",
        ),
        llmagent.WithTools(memoryService.Tools()), // Register memory tools.
    )

    // Step 3: Create Runner with memory service.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService), // Set memory service.
    )
    defer appRunner.Close()

    // Run a dialog (the Agent uses memory tools automatically).
    log.Println("🧠 Starting memory-enabled chat...")
    message := model.NewUserMessage(
        "Hi, my name is John, and I like programming",
    )
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
    // Handle responses ...
    _ = eventChan
}
```

**Conversation example**:

```
User: My name is Alice and I work at TechCorp.

Agent: Nice to meet you, Alice! I'll remember that you work at TechCorp.

🔧 Tool call: memory_add
   Args: {"memory": "User's name is Alice, works at TechCorp", "topics": ["name", "work"]}
✅ Memory added successfully.

Agent: I've saved that information. How can I help you today?
```

### Auto Mode Configuration (Recommended)

In Auto mode, an LLM-based extractor analyzes conversations and automatically
creates memories. **The key setup difference is in Step 1: add an Extractor**.
With an Extractor configured, `Tools()` follows Auto mode exposure rules and
Runner triggers background extraction after responses.

```go
package main

import (
    "context"
    "log"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
    memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // Step 1: Create memory service (configure Extractor to enable auto mode).
    extractorModel := openai.New("deepseek-v4-flash")
    memExtractor := extractor.NewExtractor(extractorModel)
    memoryService := memoryinmemory.NewMemoryService(
        memoryinmemory.WithExtractor(memExtractor), // Key: configure extractor.
        // Optional: configure async workers.
        memoryinmemory.WithAsyncMemoryNum(1), // Configure number of async memory worker.
        memoryinmemory.WithMemoryQueueSize(10), // Configure memory queue size.
        memoryinmemory.WithMemoryJobTimeout(30*time.Second), // Configure memory extraction job timeout.
    )
    defer memoryService.Close()

    // Step 2: Create Agent and register memory tools.
    // Note: With Extractor configured, Tools() exposes Search by default.
    // Load can be enabled explicitly.
    chatModel := openai.New("deepseek-v4-flash")
    llmAgent := llmagent.New(
        "memory-assistant",
        llmagent.WithModel(chatModel),
        llmagent.WithDescription("An assistant with automatic memory."),
        llmagent.WithTools(memoryService.Tools()), // Search by default; Load is optional.
    )

    // Step 3: Create Runner with memory service.
    // Runner triggers auto extraction after responses.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "memory-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
        runner.WithMemoryService(memoryService),
    )
    defer appRunner.Close()

    // Run a dialog (system extracts memories automatically in background).
    log.Println("🧠 Starting auto memory chat...")
    message := model.NewUserMessage(
        "Hi, my name is John, and I like programming",
    )
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
    // Handle responses ...
    _ = eventChan
}
```

**Conversation example**:

```
User: My name is Alice and I work at TechCorp.

Agent: Nice to meet you, Alice! It's great to connect with someone from TechCorp.
       How can I help you today?

(Background: Extractor analyzes conversation and creates memory automatically)
```

### Opt-in Auto Update Policy

The built-in extractor keeps its historical behavior unless a policy is
explicitly configured. Existing applications therefore require no migration:

```go
// Merge Similar is the default and preserves the historical behavior.
memExtractor := extractor.NewExtractor(extractorModel)
```

For applications that prefer preserving long-term history, enable the update
policy explicitly:

```go
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
)
```

The policy is a built-in extractor capability captured when the Auto memory
worker is constructed. `Metadata()` remains descriptive and does not control
runtime behavior. A transparent decorator can preserve built-in capabilities
by implementing `UnwrapMemoryExtractor() extractor.MemoryExtractor`; nested
cooperating decorators are supported. A custom extractor or non-cooperating
decorator uses Merge Similar. Nil unwrap results and unwrap cycles also fall
back to Merge Similar.

The update policies affect only operations produced by background Auto
extraction. An agent or application explicitly calling `memory_update` keeps
the existing tool semantics.

| Update policy | Auto extraction behavior |
| --- | --- |
| `UpdatePolicyMergeSimilar` | Uses the existing similarity-based reconciliation. This is the default. |
| `UpdatePolicyPreserveHistory` | Drops exact duplicates, updates only for non-conflicting enrichment, and keeps changes as separate entries. Its extractor prompt reserves delete/clear for explicit user requests. |
| `UpdatePolicyAppendOnly` | Emits only non-duplicate adds: updates become adds, while delete and clear operations are filtered. |

Merge Similar retains the historical user-only query when retrieving existing
memories. Preserve History and Append Only use user and assistant conversation
text, excluding tool protocol messages, to retrieve the entries evaluated by
their policy rules. This query is bounded to 7 KiB with UTF-8-safe truncation.

Preserve History candidate reconciliation compares only the existing entries
already supplied to the extractor. Exact duplicate checks also consider earlier
operations from the same extraction batch, but distinct operations are not
merged. Retrieval scores rank candidates but cannot by themselves authorize an
update or drop. Event identity, meaningful old tokens, numbers, dates, negation,
participants, and locations must remain compatible. Topics are merged only
after an update has passed these checks.
The directional token-coverage bounds (`0.95` for the existing memory and
`0.70` for the candidate) are conservative implementation heuristics, not
values selected by benchmark tuning. When the checks cannot establish a safe
enrichment, the policy keeps the candidate as a separate memory.
For example, adding a time to the same dated visit may update that visit;
changing an employer or describing a visit on another date creates a new
entry. Preserve History uses the same runtime handling for Delete and Clear as
Merge Similar: operations selected by the extractor pass through unchanged.
The policy-specific extractor prompt instructs the model to use Delete only for
an explicit scoped forget request and Clear only for an explicit request to
forget all stored information. The worker does not attempt to reinterpret
natural-language deletion intent with regular expressions.

The update policy does not change `memory.Service`, `MemoryExtractor`, the stored
JSON representation, memory IDs, or database schemas. It does not rewrite
existing entries. All policies retain Auto memory's historical best-effort
persistence behavior: an individual write failure is logged, later operations
continue, and the extraction watermark advances after the batch is processed.

To roll back, remove the option or set it to `UpdatePolicyMergeSimilar`. No data
migration is required.

### Opt-in Assistant Episode Extraction

Auto extraction normally uses the standard fact and episode tools. Applications
that also need to recall reusable information from earlier assistant responses
can enable assistant episode extraction when constructing the extractor:

```go
memExtractor := extractor.NewExtractor(
    extractorModel,
    extractor.WithAssistantEpisodeExtraction(),
)
memoryService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithExtractor(memExtractor),
)
```

The option uses two isolated extraction stages. The first stage keeps the
standard memory tools, restricted by `WithUpdatePolicy` and enabled-tool
configuration when present. It retains assistant turns as context for
interpreting references, confirmations, and short user replies, but only user
messages can supply or authorize ordinary memory operations. When collecting a
session delta, the enabled option uses only
the primary choice from each model response event; alternative choices are not
treated as consecutive assistant replies. The extractor then considers
eligible user/assistant pairs in the extraction delta in chronological order.
The deterministic prefilter is language-neutral and examines response shape,
not request keywords: it selects an assistant response containing at least two
Markdown or numbered list items, or one that introduces a numeric token not
already present in the user request. The second-stage prompt makes the final
semantic decision about whether the result is durable and reusable. A prose
result without a list or a newly introduced number does not currently trigger
the second stage. Before storing a generated episode, a deterministic guard
checks ASCII numbers and adjacent signs, currency symbols, and percent signs
against the source response. Natural-language units and currency labels remain
the responsibility of the second-stage prompt, which requires preserving them
exactly. Eligible pairs are considered in chronological order within
a private per-request pair and source-size budget. Candidates past that budget
are omitted because assistant-result extraction is best effort.
The selected pairs are combined into one second request that exposes only the
private `memory_assistant_episode` tool. This tool is never visible to the
application Agent. An application policy configured through `WithPrompt` or
`SetPrompt` also constrains the second-stage request. When the ordinary stage
produces a Clear operation, candidate pairs at or before its request-local
`source_user_index` are excluded. A Delete operation excludes only the pairs
explicitly listed in its request-local `affected_source_user_indexes`;
unrelated pairs remain eligible. If a destructive operation has missing or
invalid request-local scope, the extractor conservatively skips the optional
stage for that call. This decision uses request-local operation provenance
rather than a language-specific interpretation of the conversation.

Assistant output is stored as attributed conversation history rather than as a
verified fact or user preference. The framework converts every accepted call
to an ordinary `KindEpisode` add operation, fixes the participants to `User`
and `Assistant`, and uses the extraction reference date as the event time when
one is present. The textual prefix shown below is descriptive only; it does not
grant special reconciliation behavior or carry trusted provenance. For example:

```json
{
  "memory": "Assistant-provided conversation episode: When the user asked for compact-kitchen products, the assistant recommended Alpha and Beta.",
  "kind": "episode",
  "participants": ["User", "Assistant"]
}
```

The second-stage model supplies only the episode text and optional retrieval
topics. It cannot override the memory kind, participants, event time, or
location. The framework rejects empty text, text over 4,096 bytes, and ASCII
numeric values whose normalized value or adjacent sign, currency symbol, or
percent sign is not grounded in the selected conversation pair. Natural-language
units and currency labels are enforced by the second-stage prompt rather than
the deterministic validator. To bound each
source's contribution to the optional request, every source message is
represented by a deterministic 8,192-byte excerpt that preserves its beginning
and end.

The assistant request uses a child deadline that ends before the parent
extraction deadline, reserving time to persist ordinary first-stage operations.
If that child deadline expires, or if the optional request or its callbacks
fail, the failure is logged and the ordinary operations are preserved. A true
parent-context cancellation still aborts the complete extraction. Deterministic
model-output rejections, such as invalid tool arguments, oversized text, or an
ungrounded quantity, skip only the affected assistant episode. A rejected call
for one pair does not discard valid calls for other pairs in the same response.

This feature is backend-neutral. It does not add a memory kind, field, database
column, table, or migration. Selected pairs in the same delta share one
second-stage model request, so extraction uses at most two model calls per
delta: one ordinary request and one assistant request. The option is fixed for
the lifetime of the extractor and is captured by the Auto memory worker when it
is constructed; it does not alter the extractor's descriptive `Metadata()`.
To disable it, construct a new extractor and memory service without the option.
A transparent decorator can preserve this setting by implementing
`UnwrapMemoryExtractor() extractor.MemoryExtractor`. A custom extractor or
non-cooperating decorator keeps ordinary single-stage extraction. Nil unwrap
results and unwrap cycles also fall back to the disabled setting.
Previously stored assistant episodes remain ordinary episodic memories and
continue to participate in normal retrieval, extraction context, and
reconciliation. Operations produced by the optional stage pass through the
same selected update policy and reconciliation path as other memory operations.

### Configuration Comparison

| Step                | Agentic Mode                        | Auto Mode                              |
| ------------------- | ----------------------------------- | -------------------------------------- |
| **Step 1**          | `NewMemoryService()`                | `NewMemoryService(WithExtractor(ext))` |
| **Step 2**          | `WithTools(memoryService.Tools())`  | `WithTools(memoryService.Tools())`     |
| **Step 3**          | `WithMemoryService(memoryService)`  | `WithMemoryService(memoryService)`     |
| **Available tools** | add/update/search/load by default; delete/clear configurable | search exposed by default; load exposed once enabled; enabled write operations are used by the extractor and hidden from the agent unless explicitly exposed |
| **Memory creation** | Agent actively calls tools          | Background auto extraction             |

## Core Concepts

The [memory module](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
is the core of tRPC-Agent-Go's memory management. It provides complete memory
storage and retrieval capabilities with a modular design that supports
multiple storage backends and memory tools.

```textplain
memory/
├── memory.go          # Core interface definitions.
├── inmemory/          # In-memory memory service implementation.
├── redis/             # Redis memory service implementation.
└── tool/              # Memory tools implementation.
    ├── tool.go        # Tool interfaces and implementations.
    └── types.go       # Tool type definitions.
```

## Best Practices

### Production Environment Configuration

```go
// ✅ Recommended configuration
postgresService, err := memorypostgres.NewService(
    // Use environment variables for sensitive info
    memorypostgres.WithHost(os.Getenv("DB_HOST")),
    memorypostgres.WithUser(os.Getenv("DB_USER")),
    memorypostgres.WithPassword(os.Getenv("DB_PASSWORD")),
    memorypostgres.WithDatabase(os.Getenv("DB_NAME")),

    // Enable soft delete (for recovery)
    memorypostgres.WithSoftDelete(true),

    // Reasonable limit
    memorypostgres.WithMemoryLimit(1000),
)
```

### Error Handling

```go
// ✅ Complete error handling
err := memoryService.AddMemory(ctx, userKey, content, topics)
if err != nil {
    if strings.Contains(err.Error(), "limit exceeded") {
        // Handle limit: clean old memories or reject
        log.Warnf("Memory limit exceeded for user %s", userKey.UserID)
    } else {
        return fmt.Errorf("failed to add memory: %w", err)
    }
}
```

### Tool Enabling Strategy

```go
// Scenario 1: Read-only assistant
readOnlyService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.LoadToolName, true),
    memoryinmemory.WithToolEnabled(memory.SearchToolName, true),
    memoryinmemory.WithToolEnabled(memory.AddToolName, false),
    memoryinmemory.WithToolEnabled(memory.UpdateToolName, false),
)

// Scenario 2: Regular user
userService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    // clear disabled (prevent accidental deletion)
)

// Scenario 3: Admin
adminService := memoryinmemory.NewMemoryService(
    memoryinmemory.WithToolEnabled(memory.DeleteToolName, true),
    memoryinmemory.WithToolEnabled(memory.ClearToolName, true),
)
```

## References

- [Memory Module Source](https://github.com/trpc-group/trpc-agent-go/tree/main/memory)
- [Agentic Mode Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory)
- [Auto Mode Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/auto)
- [mem0 Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/mem0)
- [TencentDB Agent Memory Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/memory/tencentdb)
- [TencentDB Agent Memory SDK](https://github.com/TencentCloud/TencentDB-Agent-Memory)
- [Ecosystem Guide](https://github.com/trpc-group/trpc-agent-go/blob/main/docs/mkdocs/en/ecosystem.md)
- [API Documentation](https://pkg.go.dev/trpc.group/trpc-go/trpc-agent-go/memory)
