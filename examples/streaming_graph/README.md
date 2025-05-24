# Streaming Graph Example

This example demonstrates how to build a graph-based agent with streaming capabilities using the trpc-adk-go framework. The example consists of a server that processes user queries through a graph of nodes including an OpenAI model, and a client that can interact with the server.

## Features

- **OpenAI Integration**: Uses OpenAI's streaming models for real-time response generation
- **Graph-based Architecture**: Implements a graph with multiple node types and conditional edges
- **Tool Calling Loop**: Demonstrates a complete tool calling cycle with proper conversation management
- **Streaming Responses**: Provides real-time streaming of model outputs
- **Comprehensive Tool Suite**: Includes seven realistic tool implementations:
  - **Weather Tool**: Get current weather and 3-day forecasts for any location
  - **Search Tool**: Simulate web search functionality with customizable result count
  - **Calculator**: Perform arithmetic operations with detailed explanations
  - **Word Counter**: Analyze text statistics including words, characters, and sentences
  - **URL Shortener**: Create shortened URLs for long web addresses
  - **Unit Converter**: Convert between various units of measurement (length, weight, etc.)
  - **Currency Converter**: Convert amounts between different currencies
- **Interactive CLI Client**: Provides a command-line interface for interacting with the graph

## Prerequisites

- Go 1.18 or later
- An OpenAI API key

## Setup

1. Set the required environment variables:

```bash
export OPENAI_API_KEY=your_openai_api_key
export OPENAI_MODEL_NAME=gpt-4o           # Optional, defaults to gpt-4o
export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional, defaults to OpenAI's API
export PORT=8080                      # Optional, defaults to 8080
```

2. Build the server and client:

```bash
cd examples/streaming_graph
go build -o server .
cd cmd/client
go build -o client .
```

## Usage

### Running the Server

Start the server:

```bash
./server
```

The server will start and listen on the specified port (default: 8080).

### Running the Client

In a separate terminal, start the client:

```bash
./client
```

By default, the client connects to `http://localhost:8080`. You can change this by setting the `SERVER_URL` environment variable:

```bash
export SERVER_URL=http://your-server-url:8080
./client
```

### Client Commands

- Type any message to send a query to the server
- Type `stream` to enable streaming mode (default)
- Type `nostream` to disable streaming mode
- Type `exit` or `quit` to exit the client

## Graph Structure

The example implements an advanced graph with the following nodes and workflow:

1. **Preprocessing Node**: Logs requests and adds metadata
2. **Conversation Manager Node**: Maintains conversation history and integrates system instructions
3. **Model Node**: Processes inputs using the OpenAI model
4. **Response Selector Node**: Determines whether to continue with tool calling or produce final response
5. **Tool Calling Node**: Handles execution of multiple tools
6. **Postprocessing Node**: Adds signature to the final response

### Graph Workflow

The graph implements a complete agent workflow:

```
┌─────────────┐     ┌─────────────────────┐     ┌───────────┐     ┌───────────────────┐
│ preprocess  │────►│ conversation_manager │────►│   model   │────►│ response_selector │
└─────────────┘     └─────────────────────┘     └───────────┘     └─────────┬─────────┘
                              ▲                                             │
                              │                                             │
                              │                                             ▼
                    ┌─────────┴─────────┐                   Needs tools?  ◄─┐
                    │    tool_calling    │◄───────────────── Yes ───────────┘
                    └───────────────────┘                      │
                                                               │ No
                                                               ▼
                                                     ┌───────────────────┐
                                                     │   postprocessing  │
                                                     └───────────────────┘
```

The workflow supports:
- **Multi-turn conversations**: Conversation history is maintained between turns
- **Tool calling loop**: The model can use tools multiple times before producing a final response
- **Conditional execution**: Edges between nodes depend on runtime conditions

## API Endpoints

The server exposes the following HTTP endpoints:

- `POST /query`: For non-streaming queries
- `POST /stream`: For streaming queries
- `GET /health`: Health check endpoint

## Example Queries

Try these example queries to test different capabilities:

### Software Architecture Information
- "What are the key principles of software architecture?"
- "Can you explain the Model-View-Controller pattern with a simple example?"
- "Compare microservices vs. monolithic architecture"

### Weather Tool
- "What's the weather like in San Francisco?"
- "Check the weather forecast for Tokyo in Fahrenheit"
- "Tell me about the current conditions in New York City"

### Search Tool
- "Search for information about quantum computing"
- "Find recent news about artificial intelligence"
- "Look up recipes for vegetarian pasta dishes"

### Calculator Tool
- "Calculate 235 * 89"
- "What's the square root of 529?"
- "Calculate 5 raised to the power of 3"

### Word Counter Tool
- "Count the words in this sentence: The quick brown fox jumps over the lazy dog"
- "Analyze the text statistics for the first paragraph of The Hobbit"
- "How many sentences are in 'Hello world. This is a test. How are you today?'"

### URL Shortener Tool
- "Create a short URL for https://example.com/very/long/path/to/something/important"
- "Shorten this URL: https://en.wikipedia.org/wiki/Artificial_intelligence"

### Unit Converter Tool
- "Convert 5 kilometers to miles"
- "What is 72 degrees Fahrenheit in Celsius?"
- "Convert 250 grams to ounces"

### Currency Converter Tool
- "Convert 100 USD to EUR"
- "What's 1000 Japanese Yen in US Dollars?"
- "Convert 50 British Pounds to Canadian Dollars"

### Multi-turn and Mixed Queries
- "I'm planning a trip to Paris. What's the weather like there, and what are the top attractions?"
- "Tell me about the Python programming language and check if it's going to rain in Seattle tomorrow"
- (After a response) "Can you tell me more about the weather forecast for the weekend?"
- "I need to convert 150 euros to dollars and then calculate how many 15-dollar meals I can buy"

## Available Tools

### Weather Tool
The Weather Tool provides current weather conditions and 3-day forecasts for any location worldwide. It supports both metric (Celsius) and imperial (Fahrenheit) units.

### Search Tool
The Search Tool simulates web search functionality, allowing queries on any topic with customizable result count (up to 5 results).

### Calculator Tool
The Calculator Tool performs basic arithmetic operations and provides detailed explanations:
- Operations: add, subtract, multiply, divide, power, square root
- Includes formatted explanations with the calculation process

### Word Counter Tool
The Word Counter Tool analyzes text and provides detailed statistics:
- Word count
- Character count (with and without spaces)
- Sentence count
- Paragraph count
- Line count

### URL Shortener Tool
The URL Shortener Tool creates shortened URLs for long web addresses (simulated).

### Unit Converter Tool
The Unit Converter Tool handles conversions between various units:
- Length: mm, cm, m, km, inches, feet, yards, miles
- Weight: mg, g, kg, ounces, pounds, stones, tons
- Volume: ml, liters, m³, fluid ounces, cups, pints, quarts, gallons
- Temperature: Celsius, Fahrenheit, Kelvin

### Currency Converter Tool
The Currency Converter Tool handles conversions between 20 common currencies, including:
- USD, EUR, GBP, JPY, AUD, CAD, CHF, CNY
- Provides current exchange rates and formatted results

## Customization

You can customize the example by:

1. Adding more tools to the `buildGraph` function
2. Modifying the system prompt
3. Changing the graph structure by adding or modifying nodes and edges
4. Implementing additional node types for specialized processing
5. Extending the existing tools with additional functionality
6. Adding memory systems to maintain long-term conversation history 