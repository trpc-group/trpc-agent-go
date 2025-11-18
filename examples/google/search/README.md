# Google Search Tool Example

This example demonstrates how to use the Google Search tool with an AI agent for interactive conversations. The tool uses Google's Custom Search API to search for real-time web content, news, and information across all topics.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example requires the following environment variables:

| Variable | Description | Required | Default Value |
|----------|-------------|----------|---------------|
| `GOOGLE_API_KEY` | API key for Google Custom Search API | **Yes** | `` |
| `GOOGLE_SEARCH_ENGINE_ID` | Search Engine ID for Google Custom Search | **Yes** | `` |
| `OPENAI_API_KEY` | API key for the model service | **Yes** | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | No | `https://api.openai.com/v1` |

**Note**: Both `GOOGLE_API_KEY` and `GOOGLE_SEARCH_ENGINE_ID` are required for the Google Search tool to work. You need to:

1. Create a Google Cloud project and enable the Custom Search API
2. Create API credentials (API key)
3. Create a Custom Search Engine at [Google Programmable Search Engine](https://developers.google.com/custom-search/v1)
4. Get your Search Engine ID from the control panel

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `deepseek-chat` |

## Features

### 🔍 Google Search Tool (`search`)

The tool provides access to real-time web search results from Google:

**Input:**
```json
{
  "query": "string",
  "searchSize": 5,
  "searchOffset": 0,
  "searchLang": "en"
}
```

**Output:**
```json
{
  "query": "string",
  "items": [
    {
      "link": "string",
      "title": "string",
      "snippet": "string",
      "desc": "string"
    }
  ]
}
```

**What Works (Real-time Web Search):**
- **Keyword Search**: Search for any topic, news, or information
- **Real-time Results**: Access current web content and latest information
- **Multi-language Support**: Search in different languages (en, ja, zh-CN, etc.)
- **Pagination**: Support for result offset and size control
- **Rich Metadata**: Get titles, snippets, and page descriptions
- **Source Verification**: Cite verifiable sources for accurate information

## Running the Example

### Basic Usage:

```bash
export GOOGLE_API_KEY="your-google-api-key"
export GOOGLE_SEARCH_ENGINE_ID="your-search-engine-id"
export OPENAI_API_KEY="your-openai-api-key"

go run main.go
```

### Using Custom Model:

```bash
export GOOGLE_API_KEY="your-google-api-key"
export GOOGLE_SEARCH_ENGINE_ID="your-search-engine-id"
export OPENAI_API_KEY="your-openai-api-key"

go run main.go -model gpt-4o-mini
```

### Using Different Base URL (for OpenAI-compatible APIs):

```bash
export GOOGLE_API_KEY="your-google-api-key"
export GOOGLE_SEARCH_ENGINE_ID="your-search-engine-id"
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://generativelanguage.googleapis.com/v1beta/"

go run main.go -model gemini-2.5-flash
```

## Example Session

```
🚀 Google Search Chat Demo
Model: gemini-2.5-flash
Type 'exit' to end the conversation
Available tools: google_search
==================================================
✅ Google search chat ready! Session: google-session-1763222795

💡 Try asking questions like:
   - What's the latest news about artificial intelligence?
   - Search for current weather in New York
   - Find recent developments in quantum computing
   - Look up information about the latest iPhone release
   - Search for current stock prices of tech companies
   - Find information about upcoming tech conferences

ℹ️  Note: Google Search provides real-time web content and current information

👤 You: Find recent developments in quantum computing
🤖 Assistant: 🔍 Google Search initiated:
   • google_search (ID: function-call-10658533461459928312)
     Query: {"query":"recent developments in quantum computing","size":5,"lang":"en"}

🔄 Searching Google...
✅ Search results (ID: function-call-10658533461459928312): {"query":"recent developments in quantum computing","items":[{"link":"https://pme.uchicago.edu/news/world-quantum-day-2024-latest-developments-quantum-science-and-technology","title":"World Quantum Day 2024: The latest developments in quantum ...","snippet":"Apr 12, 2024 ... Secure communication through metropolitan-scale entangled quantum networks, quantum machine clusters for high-end computation, and quantum ..."},{"link":"https://www.wevolver.com/article/breakthroughs-in-quantum-computing","title":"Breakthroughs in Quantum Computing","snippet":"Aug 19, 2024 ... IBM's recent unveiling of the Condor processor represented a monumental leap in quantum computing with its 1,121 superconducting qubits, ...","desc":"Article #2 of Engineering the Quantum Future Series: Recent breakthroughs in quantum computing made substantial strides, driving closer to practical applications that could revolutionize industries by solving complex computational problems more efficiently."},{"link":"https://www.openaccessgovernment.org/the-latest-developments-in-quantum-computing-a-transformative-frontier/187748/","title":"The latest developments in quantum computing: A transformative ...","snippet":"Jan 21, 2025 ... One major breakthrough involves the development of hypercube network technologies, which enhance the scalability and performance of quantum ...","desc":"Quantum computing has been at the forefront of technological advancements, holding the potential to revolutionise fields ranging from cryptography to materials science"},{"link":"https://thequantuminsider.com/2025/01/08/2025-will-see-huge-advances-in-quantum-computing-so-what-is-a-quantum-chip-and-how-does-it-work/","title":"2025 Will See Huge Advances in Quantum Computing","snippet":"Jan 8, 2025 ... Quantum Computing Advancements in 2025 and Beyond ... In the next few years, quantum chips will continue to scale up. Importantly, the next ...","desc":"Many experts are expecting big advance in quantum computing in 2025, but what is a quantum chip and how does it work?"},{"link":"https://microtime.com/quantum-computing-in-2024-breakthroughs-challenges-and-what-lies-ahead/","title":"Quantum Computing: Breakthroughs, Challenges \u0026 What's Ahead","snippet":"Sep 5, 2024 ... 1. Increased Qubit Stability and Error Correction · 2. Quantum Supremacy Milestones · 3. Advancements in Quantum Algorithms · 4. Commercial Quantum ...","desc":"Quantum computing has rapidly evolved into one of the most exciting and promising fields in technology. Let's explore the latest developments."}]}

🤖 Assistant: Recent developments in quantum computing include:

*   **Increased Qubit Stability and Error Correction:** Significant progress is being made in making qubits more stable and developing methods to correct errors, which is crucial for building reliable quantum computers.
*   **Quantum Supremacy Milestones:** Researchers continue to achieve "quantum supremacy," demonstrating that quantum computers can solve certain problems much faster than even the most powerful classical computers.
*   **Advancements in Quantum Algorithms:** New and improved quantum algorithms are being developed, which will unlock more applications for quantum computing in various fields.
*   **Commercial Quantum Computing:** Companies are making strides in developing and offering commercial quantum computing solutions, making the technology more accessible.
*   **Hypercube Network Technologies:** The development of hypercube network technologies is enhancing the scalability and performance of quantum computers.
*   **Metropolitan-scale Entangled Quantum Networks:** Progress is being made in secure communication through entangled quantum networks that span metropolitan areas.
*   **Quantum Machine Clusters:** The development of quantum machine clusters is aimed at high-end computation.
*   **IBM's Condor Processor:** IBM unveiled its Condor processor, featuring 1,121 superconducting qubits, representing a major leap in quantum computing.

Many experts anticipate significant advancements in quantum computing, particularly in the scaling up of quantum chips, in 2025 and beyond.

Sources:
*   [World Quantum Day 2024: The latest developments in quantum ...](https://pme.uchicago.edu/news/world-quantum-day-2024-latest-developments-quantum-science-and-technology)
*   [Breakthroughs in Quantum Computing](https://www.wevolver.com/article/breakthroughs-in-quantum-computing)
*   [The latest developments in quantum computing: A transformative ...](https://www.openaccessgovernment.org/the-latest-developments-in-quantum-computing-a-transformative-frontier/187748/)
*   [2025 Will See Huge Advances in Quantum Computing](https://thequantuminsider.com/2025/01/08/2025-will-see-huge-advances-in-quantum-computing-so-what-is-a-quantum-chip-and-how-does-it-work/)
*   [Quantum Computing: Breakthroughs, Challenges & What's Ahead](https://microtime.com/quantum-computing-in-2024-breakthroughs-challenges-and-what-lies-ahead/)

👤 You: exit
👋 Goodbye!
```

## How It Works

1. **Setup**: The example creates an LLM agent with access to the Google Search tool
2. **User Input**: Users can ask questions about any topic requiring current information
3. **Tool Detection**: The AI automatically decides when to use the search tool based on the query
4. **Search Execution**: The Google Search tool performs real-time web search and returns structured results
5. **Response Generation**: The AI uses the search results to provide informed, up-to-date responses

## API Design & Capabilities

### Google Search Coverage

Google Search provides access to the entire indexed web, including:

**Content Types:**
- **News Articles**: Current events and breaking news
- **Weather Information**: Real-time weather data and forecasts
- **Stock Prices**: Current financial market information
- **Product Information**: Latest product releases and reviews
- **Technical Documentation**: Up-to-date technical information
- **Academic Research**: Recent publications and studies
- **Government Information**: Official announcements and data

### Search Capabilities

**Basic Search Features:**
- **Real-time Results**: Access to current web content
- **Multi-language Support**: Search in various languages
- **Result Pagination**: Control over result count and offset
- **Rich Snippets**: Enhanced result display with metadata

**Advanced Features:**
- **Source Verification**: Ability to cite and verify information sources
- **Current Events**: Access to breaking news and recent developments
- **Local Information**: Location-based search results
- **Trend Analysis**: Access to trending topics and popular searches

## Interactive Features

- **Streaming Response**: Real-time display of search process and results
- **Tool Visualization**: Clear indication when searches are performed
- **Multi-turn Conversation**: Maintains context across multiple searches
- **Error Handling**: Graceful handling of search failures or API limits
- **Source Citation**: Transparent about information sources

## Use Cases

### Information Retrieval
- **Current Events**: Get latest news and developments
- **Weather Updates**: Real-time weather information
- **Market Data**: Stock prices and financial information
- **Product Research**: Latest product releases and reviews

### Research and Learning
- **Academic Research**: Find recent studies and publications
- **Technical Information**: Up-to-date technical documentation
- **Educational Content**: Current educational resources
- **Professional Development**: Industry trends and developments

### Professional Applications
- **Business Intelligence**: Market research and competitive analysis
- **News Monitoring**: Track industry news and developments
- **Fact Checking**: Verify information with current sources
- **Decision Support**: Informed decision-making with current data

This example showcases how AI agents can be enhanced with real-time web search capabilities to provide accurate, current information from the entire web, making it valuable for applications requiring up-to-date knowledge and information verification.