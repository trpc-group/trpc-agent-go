# Wikipedia Search Tool

## Overview

The Wikipedia Search Tool for trpc-agent-go provides detailed article information and metadata.

## Features

- **Comprehensive Search**: Search within Wikipedia articles with detailed results
- **Rich Metadata**: Get article statistics, timestamps, and structured information
- **Multi-language Support**: Support for different Wikipedia language editions (Chinese, English, Spanish, etc.)
- **Flexible Configuration**: Customize search parameters and behavior

## Installation

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"
```

## Quick Start

```go
// Create Wikipedia tool set
wikipediaToolSet, err := wikipedia.NewToolSet(
    wikipedia.WithLanguage("zh"),  // Use Chinese Wikipedia
    wikipedia.WithMaxResults(5),
)
if err != nil {
    // Handle error
}
```

- See example: `trpc-agent-go/examples/wiki/main.go`

## Configuration Options

### WithLanguage(language string)
Set the Wikipedia language edition to search.

```go
// English Wikipedia (default)
wikipedia.WithLanguage("en")

// Chinese Wikipedia
wikipedia.WithLanguage("zh")

// Spanish Wikipedia
wikipedia.WithLanguage("es")
```

### WithMaxResults(maxResults int)
Set the maximum number of search results to return.

```go
// Return up to 10 results
wikipedia.WithMaxResults(10)
```

### WithTimeout(timeout time.Duration)
Set the HTTP request timeout.

```go
// 30 second timeout
wikipedia.WithTimeout(30 * time.Second)
```

### WithUserAgent(userAgent string)
Set a custom User-Agent string.

```go
wikipedia.WithUserAgent("MyApp/1.0")
```

## Tool Input Parameters

The Wiki search tool accepts the following JSON parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | Yes | Wikipedia search query keywords |
| `limit` | int | No | Maximum number of results (default: 5) |
| `include_all` | bool | No | Include all available metadata |

### Input Example
```json
{
  "query": "artificial intelligence",
  "limit": 3,
  "include_all": true
}
```

## Tool Output Format

The tool returns a comprehensive response with the following structure:

```json
{
  "query": "artificial intelligence",
  "results": [
    {
      "title": "Artificial intelligence",
      "url": "https://en.wikipedia.org/wiki/Artificial_intelligence",
      "description": "Artificial intelligence (AI) is intelligence demonstrated by machines...",
      "page_id": 18985062,
      "word_count": 12543,
      "size_bytes": 156789,
      "last_modified": "2024-11-15T10:30:00Z",
      "namespace": 0
    }
  ],
  "total_hits": 1247,
  "summary": "Found 3 results (total: 1247)",
  "search_time": "45.23ms"
}
```

### Output Fields Explanation

| Field | Description |
|-------|-------------|
| `query` | Original search query |
| `results` | Array of search results |
| `total_hits` | Total number of matching articles |
| `summary` | Human-readable search summary |
| `search_time` | Search execution time |

#### Result Item Fields

| Field | Description |
|-------|-------------|
| `title` | Article title |
| `url` | Direct link to Wikipedia article |
| `description` | Article excerpt/snippet |
| `page_id` | Unique Wikipedia page identifier |
| `word_count` | Number of words in the article |
| `size_bytes` | Article size in bytes |
| `last_modified` | Last modification timestamp |
| `namespace` | Wikipedia namespace (0=main articles) |

## Use Cases

### 1. Basic Information Retrieval
Suitable for quickly obtaining basic facts and information about any topic.

**Agent Query**: "*What is quantum computing?*"  
**Tool Usage**: Search for "quantum computing" and return comprehensive information.

### 2. Research and Analysis
Suitable for in-depth research with detailed metadata.

**Agent Query**: "*Compare the article lengths of different programming languages*"  
**Tool Usage**: Search for multiple programming languages and compare word count statistics.

### 3. Fact Checking
Verify information and get authoritative sources.

**Agent Query**: "*When was the theory of relativity proposed?*"  
**Tool Usage**: Search for "theory of relativity" and extract historical information.

### 4. Educational Content
Provide detailed explanations for learning purposes.

**Agent Query**: "*Explain the concept of machine learning*"  
**Tool Usage**: Search for machine learning and return structured information.

## Example Usage in Agent

```go
package main

import (
    "context"
    "fmt"
    
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"
)

func main() {
    // Create model
    model := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))
    
    // Create Wikipedia tool set
    wikipediaToolSet, err := wikipedia.NewToolSet(
        wikipedia.WithLanguage("zh"),  // Use Chinese edition
        wikipedia.WithMaxResults(3),
    )
    if err != nil {
        // Handle error
    }
    
    // Create Agent
    agent := llmagent.New(
        "wikipedia-agent",
        llmagent.WithModel(model),
        llmagent.WithDescription("AI assistant with Wikipedia access"),
        llmagent.WithInstruction("Use Wikipedia to provide accurate information"),
        llmagent.WithToolSets([]tool.ToolSet{wikipediaToolSet}),
    )
    
    // Use agent...
}
```

## Error Handling

- **Network errors**: Returns error with descriptive message
- **Invalid queries**: Returns empty results with error summary
- **API rate limits**: Returns appropriate error response
- **Timeout errors**: Configurable timeout with clear error messages

## Best Practices

1. **Query Quality**: Use specific, well-formed queries for best results
2. **Result Limits**: Set appropriate limits to avoid excessive data transfer
3. **Language Consistency**: Choose the appropriate language edition for your use case
4. **Error Handling**: Always handle potential errors in Agent logic
5. **Rate Limiting**: Be mindful of Wikipedia's usage policies

## Supported Languages

The tool supports all Wikipedia language editions. Common ones include:

- `en` - English (default)
- `zh` - Chinese
- `es` - Spanish
- `fr` - French
- `de` - German
- `ja` - Japanese
- `ru` - Russian
- `pt` - Portuguese
- `it` - Italian
- `ar` - Arabic

## How It Works

### Data Flow

1. **User Query** → Agent receives user message
2. **Tool Call** → LLM decides to call wiki_search tool
3. **API Request** → Tool sends search request to Wikipedia API
4. **Data Return** → Retrieve structured search results (JSON format)
5. **LLM Processing** → Model generates natural language response based on tool's returned data
6. **Streaming Output** → Real-time display of LLM generated content

### Key Points

- **Tool returns raw data**: JSON structured data returned by Wikipedia API
- **LLM performs intelligent processing**: Model converts structured data into fluent natural language
- **RAG Architecture**: Tool provides accurate facts, LLM provides language capability and reasoning

Example:
```
Tool returns: {"title": "Artificial intelligence", "word_count": 12543, ...}
LLM generates: "Artificial intelligence is a complex discipline, the related article on Wikipedia contains 12,543 words..."
```

## Technical Architecture

```
User Input
  ↓
LLM Agent (Decision Layer)
  ↓
Wiki Search Tool (Tool Layer)
  ↓
Wikipedia API Client (HTTP Layer)
  ↓
Wikipedia MediaWiki API
  ↓
Return Structured Data
  ↓
LLM Generates Natural Language Response
  ↓
Streaming Output to User
```

## Common Usage Scenarios

### Scenario 1: Knowledge Q&A
```
User: "What is deep learning?"
Tool call: wiki_search(query="deep learning")
Returns: Definition, applications, history of deep learning
Output: Detailed explanation based on Wikipedia data
```

### Scenario 2: Comparative Analysis
```
User: "Compare the article lengths of Python and Java"
Tool calls: 
  - wiki_search(query="Python")
  - wiki_search(query="Java")
Returns: word_count metadata for both articles
Output: Comparative analysis results
```

### Scenario 3: Historical Query
```
User: "When was Newton's law of universal gravitation discovered?"
Tool call: wiki_search(query="universal gravitation")
Returns: Article excerpt containing historical information
Output: Extracted and summarized historical information
```

## License

This tool is part of the trpc-agent-go framework and is licensed under the Apache License Version 2.0.

## Contributing

For contribution guidelines, please refer to the main trpc-agent-go repository.

## Related Resources

- [trpc-agent-go Documentation](https://trpc.group/trpc-go/trpc-agent-go)
- [Wikipedia API Documentation](https://www.mediawiki.org/wiki/API:Main_page)
- [Example Code](../examples/wiki/)

## Version History

- v1.0 - Initial release with comprehensive search and rich metadata
