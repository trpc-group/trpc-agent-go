# Knowledge Base Management Example

This example demonstrates knowledge base management operations including adding, removing, reloading sources, and searching.

## Features

- **Source Management**: Add, remove, and reload document sources dynamically
- **Incremental Sync**: Smart change detection with automatic orphan cleanup
- **Metadata Support**: Attach custom metadata to sources for filtering
- **Search**: Query the knowledge base with relevance scoring

## Environment Configuration

```bash
export OPENAI_API_KEY=your_openai_api_key
export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional
```

## Usage

```bash
cd examples/knowledge/management
go run main.go
```

## Demo Operations

The demo automatically demonstrates:

1. **Create Knowledge Base** - Initialize with an initial source (LLMDocs)
2. **AddSource** - Dynamically add a new source (GolangDocs)
3. **Search** - Query across all sources
4. **ReloadSource** - Reload a source with updated metadata
5. **RemoveSource** - Remove a source from the knowledge base
6. **Search** - Verify changes after removal

## Example Output

```bash
❯ go run main.go
📚 Knowledge Management Demo
============================

1️⃣ Creating knowledge base with initial source...
   ✅ Initial source loaded
   Sources: 1, Total documents: 23
   - LLMDocs: 23 docs, metadata: map[category:documentation topic:llm]

2️⃣ Adding new source (GolangDocs)...
   ✅ Source added successfully
   Sources: 2, Total documents: 28
   - LLMDocs: 23 docs, metadata: map[category:documentation topic:llm]
   - GolangDocs: 5 docs, metadata: map[category:documentation topic:programming]

3️⃣ Searching for 'machine learning'...
   Found 2 results:
   1. [LLMDocs] score=0.449: ally available, or that the naturally occurring data is of insufficient quality....
   2. [LLMDocs] score=0.433:  which text humans prefer. Then, the LLM can be fine-tuned through reinforcement...

4️⃣ Reloading source (LLMDocs)...
   ✅ Source reloaded with new metadata
   Sources: 2, Total documents: 28
   - LLMDocs: 23 docs, metadata: map[category:documentation topic:llm version:v2]
   - GolangDocs: 5 docs, metadata: map[category:documentation topic:programming]

5️⃣ Removing source (GolangDocs)...
   ✅ Source removed
   Sources: 1, Total documents: 23
   - LLMDocs: 23 docs, metadata: map[category:documentation topic:llm version:v2]

6️⃣ Searching after removal...
   Found 2 results:
   1. [LLMDocs] score=0.302: odel to process relationships between all elements in a sequence simultaneously,...
   2. [LLMDocs] score=0.292: ies inherent in human language corpora, but they also inherit inaccuracies and b...

✅ Demo completed!
```

## Key APIs Demonstrated

- `knowledge.New()` - Create knowledge base instance
- `kb.Load()` - Load all configured sources
- `kb.AddSource()` - Add a new source dynamically
- `kb.ReloadSource()` - Reload a source (with optional metadata update)
- `kb.RemoveSource()` - Remove a source by name
- `kb.Search()` - Search the knowledge base
- `kb.ShowDocumentInfo()` - Get document statistics
