# Filter Functionality

> **Example Code**: [examples/knowledge/features/metadata-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/metadata-filter) and [examples/knowledge/features/agentic-filter](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/features/agentic-filter)


The Knowledge system provides powerful filter functionality, allowing precise search based on document metadata. This includes static filters and intelligent filters.

## Configuring Metadata Sources

For filter functionality to work properly, metadata needs to be added when creating document sources:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

sources := []source.Source{
    // File source with metadata
    filesource.New(
        []string{"./docs/api.md"},
        filesource.WithName("API Documentation"),
        filesource.WithMetadataValue("category", "documentation"),
        filesource.WithMetadataValue("topic", "api"),
        filesource.WithMetadataValue("service_type", "gateway"),
        filesource.WithMetadataValue("protocol", "trpc-go"),
        filesource.WithMetadataValue("version", "v1.0"),
    ),

    // Directory source with metadata
    dirsource.New(
        []string{"./tutorials"},
        dirsource.WithName("Tutorials"),
        dirsource.WithMetadataValue("category", "tutorial"),
        ...
    ),

    // URL source with metadata
    urlsource.New(
        []string{"https://example.com/wiki/rpc"},
        urlsource.WithName("RPC Wiki"),
        urlsource.WithMetadataValue("category", "encyclopedia"),
        ...
    ),
}
```

> **Tip**: For more document source configuration details, see [Document Source Configuration](source.md).


## Basic Filters

> **Important: Filter Field Naming Convention**
>
> When using filters, **metadata fields require the `metadata.` prefix**:
> - The `metadata.` prefix distinguishes metadata fields from system fields (like `id`, `name`, `content`, etc.)
> - Whether using `llmagent.WithKnowledgeConditionedFilter()`, `knowledgetool.WithConditionedFilter()`, or `searchfilter.Equal()`, metadata fields need the `metadata.` prefix
> - If you customize the metadata field name via `WithMetadataField()`, still use the `metadata.` prefix; the framework will automatically convert to the actual field name
> - Custom table fields added via `WithDocBuilder` (like `status`, `priority`, etc.) use the field name directly without prefix

Basic filters support two ways of setting: Agent-level fixed filters and Runner-level runtime filters.

### Agent-Level Filters

Preset fixed search filter conditions when creating an Agent:

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// Create Agent with fixed filters
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.category", "documentation"),
            searchfilter.Equal("metadata.topic", "api"),
        ),
    ),
)
```

### Runner-Level Filters

Dynamically pass filters when calling `runner.Run()`, suitable for scenarios requiring filtering based on different request contexts:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

// Pass filters at runtime
eventCh, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.category", "tutorial"),
            searchfilter.Equal("metadata.difficulty", "beginner"),
            searchfilter.Equal("metadata.language", "zh"),
        ),
    ),
)
```

### Filter Merge Rules

Agent-level and Runner-level filters are combined using **AND logic**:

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// Agent-level filter
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.category", "documentation"),
            searchfilter.Equal("metadata.source_type", "web"),
        ),
    ),
)

// Runner-level filter
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeConditionedFilter(
        searchfilter.Equal("metadata.topic", "api"),
    ),
)

// Final effective filter (AND combination):
// metadata.category = "documentation" AND
// metadata.source_type = "web" AND
// metadata.topic = "api"
```

## Intelligent Filters (Agentic Filter)


> **Important: Filter Field Naming Convention**
>
> When using filters, **metadata fields require the `metadata.` prefix**:
> - The `metadata.` prefix distinguishes metadata fields from system fields (like `id`, `name`, `content`, etc.)
> - Whether using `llmagent.WithKnowledgeConditionedFilter()`, `knowledgetool.WithConditionedFilter()`, or `searchfilter.Equal()`, metadata fields need the `metadata.` prefix
> - If you customize the metadata field name via `WithMetadataField()`, still use the `metadata.` prefix; the framework will automatically convert to the actual field name
> - Custom table fields added via `WithDocBuilder` (like `status`, `priority`, etc.) use the field name directly without prefix


Intelligent filters are an advanced feature of the Knowledge system, allowing LLM Agents to dynamically select appropriate filter conditions based on user queries.

### Enabling Intelligent Filters

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// Get metadata information from all sources
sourcesMetadata := source.GetAllMetadata(sources)

// Create Agent with intelligent filtering support
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithEnableKnowledgeAgenticFilter(true),           // Enable intelligent filters
    llmagent.WithKnowledgeAgenticFilterInfo(sourcesMetadata), // Provide available filter information
)
```

## Filter Hierarchy

The Knowledge system supports multiple filter levels, all filters are implemented using FilterCondition and combined using **AND logic**. The system doesn't distinguish priority; all levels of filters are merged equally.

**Filter Hierarchy**:

1. **Agent-Level Filters**:
   - Set conditioned filters via `llmagent.WithKnowledgeConditionedFilter()`

2. **Tool-Level Filters**:
   - Set conditioned filters via `knowledgetool.WithConditionedFilter()`
   - Note: Agent-level filters are actually implemented via Tool-level filters

3. **Runner-Level Filters**:
   - Pass conditioned filters via `agent.WithKnowledgeConditionedFilter()` when calling `runner.Run()`

4. **LLM Intelligent Filters**:
   - Filter conditions dynamically generated by LLM based on user queries

> **Important Note**:
> - All filters are combined using **AND logic**, meaning all filter conditions from all levels must be satisfied simultaneously

### Filter Combination Example

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// 1. Agent-level filter
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.source_type", "web"),
            searchfilter.Equal("metadata.category", "documentation"),
            searchfilter.Equal("metadata.protocol", "trpc-go"),
        ),
    ),
)

// 2. Runner-level filter
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.language", "zh"),
            searchfilter.Equal("metadata.version", "v1.0"),
        ),
    ),
)

// 3. LLM intelligent filter (dynamically generated by LLM)
// Example: User asks "Find API related documents", LLM might generate {"field": "metadata.topic", "value": "api"}

// Final effective filter conditions (all conditions combined with AND):
// metadata.source_type = "web" AND
// metadata.category = "documentation" AND
// metadata.protocol = "trpc-go" AND
// metadata.language = "zh" AND
// metadata.version = "v1.0" AND
// metadata.topic = "api"
//
// Meaning: All conditions from all levels must be satisfied simultaneously
```

## Complex Condition Filters

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// Manually create Tool with conditioned filter
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithConditionedFilter(
        searchfilter.And(
            searchfilter.Equal("metadata.source_type", "web"),
            searchfilter.Or(
                searchfilter.Equal("metadata.topic", "programming"),
                searchfilter.Equal("metadata.topic", "api"),
            ),
        ),
    ),
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(searchTool),  // Manually pass Tool
)

// Final filter condition:
// metadata.source_type = "web" AND (metadata.topic = "programming" OR metadata.topic = "api")
// Meaning: Must be web source, and topic is either programming or API
```

## Common Filter Helper Functions

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// Comparison operators (Note: metadata fields need metadata. prefix)
searchfilter.Equal("metadata.topic", value)                  // metadata.topic = value
searchfilter.NotEqual("metadata.category", value)            // metadata.category != value
searchfilter.GreaterThan("metadata.version", value)          // metadata.version > value
searchfilter.GreaterThanOrEqual("metadata.version", value)   // metadata.version >= value
searchfilter.LessThan("metadata.version", value)             // metadata.version < value
searchfilter.LessThanOrEqual("metadata.version", value)      // metadata.version <= value
searchfilter.In("metadata.category", values...)              // metadata.category IN (...)
searchfilter.NotIn("metadata.topic", values...)              // metadata.topic NOT IN (...)
searchfilter.Like("metadata.protocol", pattern)              // metadata.protocol LIKE pattern
searchfilter.Between("metadata.version", min, max)           // metadata.version BETWEEN min AND max

// Custom table fields (extra columns added via WithDocBuilder) don't need prefix
searchfilter.NotEqual("status", "deleted")                   // status != "deleted"
searchfilter.GreaterThanOrEqual("priority", 3)               // priority >= 3

// Logical operators
searchfilter.And(conditions...)               // AND combination
searchfilter.Or(conditions...)                // OR combination

// Nested example: (metadata.category = 'documentation') AND (metadata.topic = 'api' OR metadata.topic = 'rpc')
searchfilter.And(
    searchfilter.Equal("metadata.category", "documentation"),
    searchfilter.Or(
        searchfilter.Equal("metadata.topic", "api"),
        searchfilter.Equal("metadata.topic", "rpc"),
    ),
)
```

## Multiple Document Return

Knowledge Search Tool supports returning multiple relevant documents, configurable via `WithMaxResults(n)` option:

```go
import knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"

// Create search tool, limit to at most 5 documents
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithMaxResults(5),
)

// Or use intelligent filter search tool
agenticSearchTool := knowledgetool.NewAgenticFilterSearchTool(
    kb,
    sourcesMetadata,
    knowledgetool.WithMaxResults(10),
)
```

Each returned document contains text content, metadata, and relevance score, sorted by score in descending order.
