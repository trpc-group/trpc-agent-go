# TRPC Agent Go - Document Processing System

A comprehensive document processing system for TRPC Agent Go, featuring multiple chunking strategies and document readers.

## Features

- **Multiple Document Readers**: Support for TXT, PDF, Markdown, JSON, CSV, and DOCX files
- **Advanced Chunking Strategies**: 8 different chunking approaches for various use cases
- **Functional Options Pattern**: Clean, extensible configuration
- **Batch Processing**: Process multiple files and URLs efficiently
- **Custom Chunking**: Easy integration of custom chunking strategies

## Document Readers

### Supported Formats

- **Text (.txt)**: Plain text files
- **PDF (.pdf)**: PDF documents using UniPDF
- **Markdown (.md)**: Markdown files with structure-aware processing
- **JSON (.json)**: JSON documents
- **CSV (.csv)**: CSV files with raw content reading
- **DOCX (.docx)**: Microsoft Word documents using UniOffice

### Reader Interface

All readers implement the `Reader` interface:

```go
type Reader interface {
    ReadFromReader(name string, r io.Reader) ([]*document.Document, error)
    ReadFromFile(filePath string) ([]*document.Document, error)
    ReadFromURL(url string) ([]*document.Document, error)
    Name() string
}
```

## Chunking Strategies

### 1. Fixed Size Chunking
Simple character-based chunking with optional overlap.

```go
strategy := chunking.NewFixedSizeChunking(
    chunking.WithChunkSize(1000),
    chunking.WithOverlap(100),
)
```

### 2. Sliding Window Chunking
Chunking with natural break point detection (newlines, periods).

```go
strategy := chunking.NewSlidingWindowChunking(
    chunking.WithSlidingWindowChunkSize(1000),
    chunking.WithSlidingWindowOverlap(100),
)
```

### 3. Recursive Chunking
True recursive chunking using separator hierarchy.

```go
strategy := chunking.NewRecursiveChunking(
    chunking.WithRecursiveChunkSize(1000),
    chunking.WithRecursiveOverlap(100),
)
```

**Language-Specific Recursive Chunking:**
```go
strategy := chunking.NewRecursiveChunkingForLanguage("go",
    chunking.WithRecursiveChunkSize(1000),
    chunking.WithRecursiveOverlap(100),
)
```

### 4. Semantic Chunking
Uses embeddings to find semantic boundaries.

```go
embedder := &MyEmbedder{} // Implement chunking.Embedder
strategy := chunking.NewSemanticChunking(
    chunking.WithSemanticEmbedder(embedder),
    chunking.WithSemanticSimilarityThreshold(0.5),
)
```

### 5. Token-Based Chunking
Respects LLM token limits.

```go
tokenizer := &MyTokenizer{} // Implement chunking.Tokenizer
strategy := chunking.NewTokenChunking(
    chunking.WithTokenizer(tokenizer),
    chunking.WithTokenChunkSize(1000),
    chunking.WithTokenOverlap(100),
)
```

### 6. Hierarchical Chunking
Creates tree-structured chunks with multiple levels.

```go
levels := []chunking.HierarchicalLevel{
    {
        Name:      "sections",
        Strategy:  chunking.NewMarkdownChunking(),
        MaxChunks: 10,
    },
    {
        Name:      "paragraphs",
        Strategy:  chunking.NewSlidingWindowChunking(),
        MaxChunks: 20,
    },
}
strategy := chunking.NewHierarchicalChunking(
    chunking.WithHierarchicalLevels(levels),
)
```

**Pre-configured Hierarchical Chunking:**
```go
// For Markdown
strategy := chunking.NewMarkdownHierarchicalChunking()

// For Code
strategy := chunking.NewCodeHierarchicalChunking("go")
```

### 7. Agentic Chunking
Uses LLM to determine natural breakpoints.

```go
model := &MyLLMModel{} // Implement chunking.LLMModel
strategy := chunking.NewAgenticChunking(
    chunking.WithAgenticModel(model),
    chunking.WithAgenticMaxChunkSize(2000),
)
```

### 8. Markdown Chunking
Structure-aware markdown chunking with header preservation.

```go
strategy := chunking.NewMarkdownChunking(
    chunking.WithMarkdownChunkSize(1000),
    chunking.WithMarkdownOverlap(100),
)
```

## Usage Examples

### Basic Document Processing

```go
// Create processor with default settings
processor := processor.New()

// Process a single file
documents, err := processor.ProcessFile("document.txt")
if err != nil {
    log.Fatal(err)
}

// Process multiple files
documents, err := processor.ProcessFiles([]string{"doc1.txt", "doc2.md", "doc3.pdf"})
```

### Custom Chunking Strategies

```go
// Fixed-size chunking
documents, err := processor.ProcessWithFixedSizeChunking("document.txt", 500, 50)

// Sliding window chunking
documents, err := processor.ProcessWithSlidingWindowChunking("document.txt", 500, 50)

// Recursive chunking
documents, err := processor.ProcessWithRecursiveChunking("document.txt", 500, 50)

// Language-specific recursive chunking
documents, err := processor.ProcessWithRecursiveChunkingForLanguage("code.go", "go", 500, 50)

// Semantic chunking
embedder := &MyEmbedder{}
documents, err := processor.ProcessWithSemanticChunking("document.txt", embedder, 0.5)

// Token-based chunking
tokenizer := &MyTokenizer{}
documents, err := processor.ProcessWithTokenChunking("document.txt", tokenizer, 1000, 100)

// Hierarchical chunking
levels := []chunking.HierarchicalLevel{...}
documents, err := processor.ProcessWithHierarchicalChunking("document.md", levels)

// Markdown hierarchical chunking
documents, err := processor.ProcessWithMarkdownHierarchicalChunking("document.md")

// Code hierarchical chunking
documents, err := processor.ProcessWithCodeHierarchicalChunking("code.go", "go")

// Agentic chunking
model := &MyLLMModel{}
documents, err := processor.ProcessWithAgenticChunking("document.txt", model, 2000)

// Markdown chunking
documents, err := processor.ProcessWithMarkdownChunking("document.md")
```

### URL Processing

```go
// Process a single URL
documents, err := processor.ProcessURL("https://example.com/document.txt")

// Process multiple URLs
documents, err := processor.ProcessURLs([]string{
    "https://example.com/doc1.txt",
    "https://example.com/doc2.md",
})
```

### Custom Chunking Strategy

```go
// Create custom strategy
customStrategy := chunking.NewSlidingWindowChunking(
    chunking.WithSlidingWindowChunkSize(300),
    chunking.WithSlidingWindowOverlap(30),
)

// Use with processor
documents, err := processor.ProcessWithCustomChunking("document.txt", customStrategy)
```

## Reader Configuration

### Functional Options

All readers support functional options for configuration:

```go
// Text reader with custom chunking
reader := text.New(
    text.WithChunking(true),
    text.WithChunkingStrategy(customStrategy),
)

// PDF reader with custom settings
reader := pdf.New(
    pdf.WithChunking(true),
    pdf.WithChunkingStrategy(customStrategy),
)

// Markdown reader
reader := markdown.New(
    markdown.WithChunking(true),
    markdown.WithChunkingStrategy(customStrategy),
)
```

### Direct Reader Usage

```go
// Create reader
reader := text.New()

// Read from file
documents, err := reader.ReadFromFile("document.txt")

// Read from io.Reader (e.g., HTTP response)
response, err := http.Get("https://example.com/document.txt")
if err != nil {
    log.Fatal(err)
}
defer response.Body.Close()

documents, err := reader.ReadFromReader("http_document", response.Body)

// Read from URL
documents, err := reader.ReadFromURL("https://example.com/document.txt")
```

## Supported Languages for Recursive Chunking

- **Go**: Functions, variables, constants, types, control structures
- **Python**: Functions, classes, control structures, exception handling
- **JavaScript**: Functions, variables, control structures
- **Java**: Classes, methods, control structures

## Dependencies

- **UniPDF**: For PDF processing
- **UniOffice**: For DOCX processing

## Installation

```bash
go get trpc.group/trpc-go/trpc-agent-go
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## License

[License information]
