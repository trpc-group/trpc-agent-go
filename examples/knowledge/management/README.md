# Knowledge Base Management Example

This example demonstrates a comprehensive knowledge base management system that supports multiple document sources and vector stores.

## Features

- **Multiple Vector Store Support**: Elasticsearch, TCVector, PGVector, InMemory
- **Multiple Embedder Support**: OpenAI, Gemini embedding models
- **File Source Management**: Automatic document parsing, chunking, and metadata extraction
- **Real-time Synchronization**: Detects file changes and updates knowledge base
- **Metadata Filtering**: Advanced filtering by document metadata and tags
- **Smart Cleanup**: Automatic removal of orphaned documents
- **Interactive CLI**: User-friendly console interface
- **Batch Processing**: High-performance document ingestion with concurrency control

## Data Directory Structure

```
examples/knowledge/management/
├── data/
│   ├── golang.md      # Default Golang documentation
│   ├── llm.md         # Default LLM documentation  
│   └── other.md       # Additional documentation
├── main.go            # Main application
└── README.md          # This file
```

## Environment Configuration

### Required Environment Variables

```bash
# OpenAI Embedding
export OPENAI_BASE_URL="your-openai-base-url"         # Required for OpenAI model and embedder
export OPENAI_API_KEY=your_openai_api_key
export OPENAI_EMBEDDING_MODEL=text-embedding-3-small  # optional

# Elasticsearch (if using)
export ELASTICSEARCH_HOSTS=http://localhost:9200
export ELASTICSEARCH_USERNAME=elastic
export ELASTICSEARCH_PASSWORD=your_password
export ELASTICSEARCH_INDEX_NAME=trpc_agent_documents

# PGVectors (if using)  
export PGVECTOR_HOST=localhost
export PGVECTOR_PORT=5432
export PGVECTOR_USER=root
export PGVECTOR_PASSWORD=your_password
export PGVECTOR_DATABASE=vectordb

# TCVector (if using)
export TCVECTOR_URL=your_tcvector_url
export TCVECTOR_USERNAME=your_username
export TCVECTOR_PASSWORD=your_password
```

## Usage

### Installation & Setup

```bash
cd examples/knowledgemanage
go run main.go [options]
```

### Command Line Options

```bash
-embedder string
    Embedding provider (default "openai")
-vectorstore string
    Vector store backend: elasticsearch, tcvector, pgvector, inmemory (default "inmemory")
-source_sync
    Enable source sync for incremental sync (default true)
```

### Load Options

The system supports several load options that serve different purposes and users:

#### 1. **`WithRecreate(recreate bool)`** - **For System Administrators/Developers**
   - **Target Users**: Scenarios requiring complete knowledge base rebuild
   - **Function**: Forces complete clearance of vector store and re-imports all documents
   - **Use Cases**: 
     - Initial knowledge base setup
     - Major structural changes to source files
     - Complete knowledge base reset required

   **Example Code**:
   ```go
   // Completely rebuild knowledge base (clear all content and re-import)
   err := knowledge.Load(ctx,
       knowledge.WithRecreate(true),     // Clear and rebuild
       knowledge.WithShowProgress(true),
   )
   ```

#### 2. **`WithEnableSourceSync(enable bool)`** - **For End Users/Operations Staff**  
   - **Target Users**: Scenarios requiring continuous synchronization and maintenance
   - **Function**: Incremental synchronization with smart change detection and updates
   - **Use Cases**:
     - Daily document updates and maintenance
     - Avoid redundant processing of unchanged content
     - Automatic orphan document cleanup

   **Example Code**:
   ```go
   // Enable incremental synchronization (enabled by default)
   kb := knowledge.New(
       knowledge.WithEmbedder(embedder),
       knowledge.WithVectorStore(vectorStore),
       knowledge.WithEnableSourceSync(true),  // Incremental sync
   )
   
   // New sources are automatically synchronized when added
   err := kb.AddSource(ctx, newSource)
   ```

**Key Differences**:
- `WithRecreate`: Destructive operation for initialization/reset scenarios
- `WithEnableSourceSync`: Non-destructive operation for daily maintenance scenarios
- Can be combined: Rebuild first then enable sync for optimal performance

### Example Output

```bash
❯ go run main.go -vectorstore=tcvector
2025/09/09 19:32:18 [Warning] Jieba will use default file for stopwords, which is /tmp/tencent/vectordatabase/data/default_stopwords.txt
2025/09/09 19:32:18 Load the stop word dictionary: "/tmp/tencent/vectordatabase/data/default_stopwords.txt" 
2025/09/09 19:32:18 Dict files path:  []
2025/09/09 19:32:18 Warning: dict files is nil.
2025/09/09 19:32:18 Gse dictionary loaded finished.
2025/09/09 19:32:27 Load the stop word dictionary: "/tmp/tencent/vectordatabase/data/default_stopwords.txt" 
Knowledge base initialized (Embedder: openai, Vector Store: tcvector)

=== Auto-loading default sources ===
2025-09-09T19:32:27+08:00       INFO    knowledge/default.go:856        Found 47 existing documents in vector store
2025-09-09T19:32:27+08:00       INFO    knowledge/default.go:600        Loading source 2/2: GolangDocSource (type: file)
2025-09-09T19:32:27+08:00       INFO    knowledge/default.go:600        Loading source 1/2: LLMDocSource (type: file)
2025-09-09T19:32:27+08:00       INFO    knowledge/default.go:606        Fetched 5 document(s) from source GolangDocSource
2025-09-09T19:32:27+08:00       INFO    knowledge/default.go:606        Fetched 25 document(s) from source LLMDocSource
2025-09-09T19:32:27+08:00       INFO    loader/aggregator.go:101        Processed 25/25 doc(s) | source LLMDocSource
2025-09-09T19:32:27+08:00       INFO    loader/aggregator.go:101        Processed 20/25 doc(s) | source LLMDocSource
2025-09-09T19:32:27+08:00       INFO    loader/aggregator.go:101        Processed 10/25 doc(s) | source LLMDocSource
2025-09-09T19:32:27+08:00       INFO    knowledge/default.go:611        Successfully loaded source LLMDocSource
2025-09-09T19:32:29+08:00       INFO    loader/aggregator.go:101        Processed 5/5 doc(s) | source GolangDocSource
2025-09-09T19:32:29+08:00       INFO    knowledge/default.go:611        Successfully loaded source GolangDocSource
2025-09-09T19:32:29+08:00       INFO    loader/stats.go:76      Document statistics - total: 30, avg: 471.6 B, min: 154 B, max: 889 B
2025-09-09T19:32:29+08:00       INFO    loader/stats.go:87        [0, 256): 3 document(s)
2025-09-09T19:32:29+08:00       INFO    loader/stats.go:87        [256, 512): 15 document(s)
2025-09-09T19:32:29+08:00       INFO    loader/stats.go:87        [512, 1024): 12 document(s)
2025-09-09T19:32:29+08:00       INFO    knowledge/default.go:926        Starting orphan document cleanup...
2025-09-09T19:32:29+08:00       INFO    knowledge/default.go:941        Cleaning up 22 orphan/outdated documents
2025-09-09T19:32:29+08:00       INFO    knowledge/default.go:946        Successfully deleted 22 documents
2025-09-09T19:32:29+08:00       INFO    knowledge/default.go:856        Found 30 existing documents in vector store
Default sources loaded successfully!

=== Current Sources Information ===
Total documents in vector store: 30

Source Name: GolangDocSource
  Total Documents: 5
  URIs: 1 unique URI(s)
    URI: file:///data/home/xxx/workspace/trpc-agent-go/examples/knowledge/management/data/golang.md (5 documents)
  Source Metadata:
    tag: golang

Source Name: LLMDocSource
  Total Documents: 25
  URIs: 1 unique URI(s)
    URI: file:///data/home/xxx/workspace/trpc-agent-go/examples/knowledge/management/data/llm.md (25 documents)
  Source Metadata:
    tag: llm

Note: Use option 4 to reload all sources or option 6 to view current status
╔════════════════════════════════════╗
║  Knowledge Base Management System  ║
╠════════════════════════════════════╣
║  1. Add Source (AddSource)         ║
║  2. Remove Source (RemoveSource)   ║
║  3. Reload Source (ReloadSource)   ║
║  4. Load All Sources (Load)        ║
║  5. Search Knowledge (Search)      ║
║  6. Show Current Sources           ║
║  7. Exit                           ║
╚════════════════════════════════════╝

➤ Select operation (1-7): 1

──────────────────────────────────────────────────
Enter source name: other
Enter file path (relative to data/ folder): ../exampledata/file/other.md
Enter metadata (key1 value1 key2 value2 ... or press enter to skip): tag cpp
2025-09-09T19:32:59+08:00       INFO    knowledge/default.go:600        Loading source 1/1: other (type: file)
2025-09-09T19:32:59+08:00       INFO    knowledge/default.go:606        Fetched 22 document(s) from source other
2025-09-09T19:33:00+08:00       INFO    loader/aggregator.go:101        Processed 10/22 doc(s) | source other
2025-09-09T19:33:01+08:00       INFO    loader/aggregator.go:101        Processed 20/22 doc(s) | source other
2025-09-09T19:33:01+08:00       INFO    loader/aggregator.go:101        Processed 22/22 doc(s) | source other
2025-09-09T19:33:02+08:00       INFO    knowledge/default.go:611        Successfully loaded source other
2025-09-09T19:33:02+08:00       INFO    loader/stats.go:76      Document statistics - total: 22, avg: 282.8 B, min: 42 B, max: 736 B
2025-09-09T19:33:02+08:00       INFO    loader/stats.go:87        [0, 256): 10 document(s)
2025-09-09T19:33:02+08:00       INFO    loader/stats.go:87        [256, 512): 11 document(s)
2025-09-09T19:33:02+08:00       INFO    loader/stats.go:87        [512, 1024): 1 document(s)
Successfully added source: other with metadata: map[tag:cpp]
──────────────────────────────────────────────────
Operation completed
══════════════════════════════════════════════════

╔════════════════════════════════════╗
║  Knowledge Base Management System  ║
╠════════════════════════════════════╣
║  1. Add Source (AddSource)         ║
║  2. Remove Source (RemoveSource)   ║
║  3. Reload Source (ReloadSource)   ║
║  4. Load All Sources (Load)        ║
║  5. Search Knowledge (Search)      ║
║  6. Show Current Sources           ║
║  7. Exit                           ║
╚════════════════════════════════════╝

➤ Select operation (1-7): 6

──────────────────────────────────────────────────

=== Current Sources Information ===
Total documents in vector store: 52

Source Name: GolangDocSource
  Total Documents: 5
  URIs: 1 unique URI(s)
    URI: file:///data/home/xxx/workspace/trpc-agent-go/examples/knowledge/management/data/golang.md (5 documents)
  Source Metadata:
    tag: golang

Source Name: other
  Total Documents: 22
  URIs: 1 unique URI(s)
    URI: file:///data/home/xxx/workspace/trpc-agent-go/examples/knowledge/management/data/other.md (22 documents)
  Source Metadata:
    tag: cpp

Source Name: LLMDocSource
  Total Documents: 25
  URIs: 1 unique URI(s)
    URI: file:///data/home/xxx/workspace/trpc-agent-go/examples/knowledge/management/data/llm.md (25 documents)
  Source Metadata:
    tag: llm
──────────────────────────────────────────────────
Operation completed
══════════════════════════════════════════════════

╔════════════════════════════════════╗
║  Knowledge Base Management System  ║
╠════════════════════════════════════╣
║  1. Add Source (AddSource)         ║
║  2. Remove Source (RemoveSource)   ║
║  3. Reload Source (ReloadSource)   ║
║  4. Load All Sources (Load)        ║
║  5. Search Knowledge (Search)      ║
║  6. Show Current Sources           ║
║  7. Exit                           ║
╚════════════════════════════════════╝

➤ Select operation (1-7): 5

──────────────────────────────────────────────────
Enter search query: golang
Enter max results (default 1): 
2025-09-09T19:33:17+08:00       DEBUG   tcvector/tcvector.go:784        tcvectordb search result: score 0.63449 id a298c2aa5abaae6e7bf7b66f6e592646 searchMode 0

=== Search Results ===
Score: 0.634
Content: # Go Programming Language

Go, also known as Golang, is an open-source programming language developed by Google in 2007 and released to the public in 2009. Created by Robert Griesemer, Rob Pike, and Ken Thompson, Go was designed to address the challenges of modern software development at scale.
Source: GolangDocSource
──────────────────────────────────────────────────
Operation completed
══════════════════════════════════════════════════

╔════════════════════════════════════╗
║  Knowledge Base Management System  ║
╠════════════════════════════════════╣
║  1. Add Source (AddSource)         ║
║  2. Remove Source (RemoveSource)   ║
║  3. Reload Source (ReloadSource)   ║
║  4. Load All Sources (Load)        ║
║  5. Search Knowledge (Search)      ║
║  6. Show Current Sources           ║
║  7. Exit                           ║
╚════════════════════════════════════╝

➤ Select operation (1-7): 
```

### Interactive Menu

The system provides an interactive CLI with the following options:

1. **Add Source** - Add a new document source
2. **Remove Source** - Remove an existing source
3. **Reload Source** - Reload a specific source
4. **Load All Sources** - Reload all sources (supports `WithRecreate` option for complete rebuild)
5. **Search Knowledge** - Query the knowledge base
6. **Show Current Sources** - Display current sources and statistics
7. **Exit** - Exit the application

### Adding a New Source

When adding a source, you'll be prompted for:
- **Source name**: Unique identifier for the source
- **File path**: Relative path to the data file (e.g., `data/other.md`)
- **Metadata**: Key-value pairs for filtering (e.g., `tag cpp`)

**Note**: When `source_sync` is enabled (default), newly added sources are automatically synchronized with the knowledge base using incremental updates.

### Search Example

```bash
➤ Select operation (1-7): 5
Enter search query: golang
Enter max results (default 1): 

=== Search Results ===
Score: 0.634
Content: # Go Programming Language

Go, also known as Golang, is an open-source programming language...
Source: GolangDocSource
```

### Default Sources

The application automatically loads these default sources from the `data/` directory:
- `golang.md`: Go programming language documentation
- `llm.md`: Large language model concepts and terminology

The system supports any text-based formats including:
- Markdown (.md)
- Text files (.txt) 
- Other text-based formats with proper parsing
