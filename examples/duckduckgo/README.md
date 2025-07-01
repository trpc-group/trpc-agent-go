# DuckDuckGo Search Tool Example

This example demonstrates how to use the DuckDuckGo search tool with an AI agent for interactive conversations. The tool uses DuckDuckGo's Instant Answer API to provide factual, encyclopedic information about entities, definitions, and calculations (not real-time data).

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work. The AI agent will use the DuckDuckGo search tool to provide factual, encyclopedic information.

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `deepseek-chat` |

## Features

### 🔍 DuckDuckGo Search Tool (`duckduckgo_search`)

The tool provides factual information using DuckDuckGo's Instant Answer API:

**Input:**
```json
{
  "query": "string"
}
```

**Output:**
```json
{
  "query": "string",
  "results": [
    {
      "title": "string",
      "url": "string", 
      "description": "string"
    }
  ],
  "summary": "string"
}
```

**What Works (Factual/Static Information):**
- **Entity Information**: People, companies, places ("Steve Jobs", "Tesla company", "Microsoft Corporation")
- **Definitions**: Technical terms, concepts ("algorithm", "photosynthesis", "machine learning")  
- **Mathematical Operations**: Calculations, unit conversions ("2+2", "100 feet to meters")
- **Historical Facts**: Static information from reliable sources like Wikipedia
- **Abstract Information**: Encyclopedic content with source attribution
- **Related Topics**: Suggestions for further exploration

**What Doesn't Work (Real-time/Dynamic Information):**
- **Current Events**: Latest news, recent developments, breaking news
- **Live Data**: Current weather, stock prices, cryptocurrency prices
- **Time-sensitive Information**: "Today's news", "current situation", "latest updates"
- **Personal Data**: Location-based queries, user-specific information
- **Recent Content**: Information requiring real-time web crawling

## Running the Example

### Using environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # Optional
go run main.go
```

### Using custom model:

```bash
export OPENAI_API_KEY="your-api-key-here"
go run main.go -model gpt-4o-mini
```

### Example with different base URL (for OpenAI-compatible APIs):

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
go run main.go -model deepseek-chat
```

## Example Session

```
🚀 DuckDuckGo Search Chat Demo
Model: deepseek-chat
Type 'exit' to end the conversation
Available tools: duckduckgo_search
==================================================
✅ Search chat ready! Session: search-session-1735123456

💡 Try asking questions like:
   - Search for information about Steve Jobs
   - Find details about Tesla company
   - Look up Albert Einstein
   - Search for Microsoft Corporation
   - What is photosynthesis?
   - Convert 100 feet to meters

ℹ️  Note: Works best for factual/encyclopedic info, not real-time data

👤 You: Search for information about Steve Jobs

🔍 DuckDuckGo search initiated:
   • duckduckgo_search (ID: call_abc123)
     Query: {"query":"Steve Jobs"}

🔄 Searching the web...
✅ Search results (ID: call_abc123): {"query":"Steve Jobs","results":[{"title":"Steve Jobs Category","url":"https://duckduckgo.com/c/Steve_Jobs",...}],"summary":"Abstract: Steven Paul Jobs was an American businessman, inventor, and investor best known for co-founding the technology company Apple Inc..."}

🤖 Assistant: Steve Jobs was an influential American businessman, inventor, and investor, best known as the co-founder of Apple Inc. Here are some key points about him...

👤 You: Find details about Tesla company

🔍 DuckDuckGo search initiated:
   • duckduckgo_search (ID: call_def456)
     Query: {"query":"Tesla company"}

🔄 Searching the web...
✅ Search results (ID: call_def456): {"query":"Tesla company","results":[{"title":"Tesla, Inc.","url":"https://duckduckgo.com/Tesla%2C_Inc.",...}],"summary":"Tesla, Inc. is an American multinational automotive and clean energy company..."}

🤖 Assistant: Here are some key details about Tesla company:

👤 You: exit
👋 Goodbye!
```

## How It Works

1. **Setup**: The example creates an LLM agent with access to the DuckDuckGo search tool
2. **User Input**: Users can ask any question that might benefit from web search
3. **Tool Detection**: The AI automatically decides when to use the search tool based on the query
4. **Search Execution**: The DuckDuckGo tool performs the web search and returns structured results
5. **Response Generation**: The AI uses the search results to provide informed, up-to-date responses

## API Design & Limitations

### Why These Limitations Exist

The DuckDuckGo Instant Answer API is **intentionally designed** for static, curated information rather than real-time data:

1. **Curated Knowledge Base**: Uses pre-processed data from reliable sources like Wikipedia
2. **Privacy Focus**: Avoids tracking and personalization that real-time APIs often require  
3. **Performance**: Static data enables faster, more reliable responses
4. **Quality Control**: Curated information ensures higher accuracy than live web scraping

### What Works vs. What Doesn't

**✅ Excellent For:**
- **Entity Information**: People, companies, places (Steve Jobs, Tesla, Microsoft)
- **Definitions**: Technical terms, concepts, scientific topics  
- **Mathematical Operations**: Calculations, unit conversions
- **Historical Facts**: Static information from reliable sources like Wikipedia
- **Reference Material**: Links to authoritative sources
- **Related Topics**: Suggestions for further exploration

**❌ Not Suitable For:**
- **Real-time Data**: Current weather, live stock prices, cryptocurrency values
- **Recent News**: Latest events, breaking news, current developments
- **Time-sensitive Queries**: "Today's news", "current situation", "latest updates"
- **Dynamic Content**: User-generated content, social media, live feeds
- **Personal Data**: Location-based queries, user-specific information

This design makes the API perfect for educational content, reference material, and factual information, but not for current events or live data.

## Interactive Features

- **Streaming Response**: Real-time display of search process and results
- **Tool Visualization**: Clear indication when searches are performed
- **Multi-turn Conversation**: Maintains context across multiple searches
- **Error Handling**: Graceful handling of search failures or empty results

This example showcases how AI agents can be enhanced with factual search capabilities to provide accurate, encyclopedic information from reliable sources. 