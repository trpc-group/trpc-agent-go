# Streaming Graph Example

This example demonstrates how to build a graph-based agent with streaming capabilities using the trpc-adk-go framework. The example consists of a server that processes user queries through a graph of nodes including an OpenAI model, and a client that can interact with the server.

## Features

- **OpenAI Integration**: Uses OpenAI's streaming models for real-time response generation
- **Graph-based Architecture**: Implements a graph with multiple node types (preprocessing, model, tools, etc.)
- **Streaming Responses**: Provides real-time streaming of model outputs
- **Tool Support**: Includes a sample weather tool to demonstrate tool usage
- **Interactive CLI Client**: Provides a command-line interface for interacting with the graph

## Prerequisites

- Go 1.18 or later
- An OpenAI API key

## Setup

1. Set the required environment variables:

```bash
export OPENAI_API_KEY=your_openai_api_key
export OPENAI_MODEL=gpt-4o           # Optional, defaults to gpt-4o
export OPENAI_API_URL=https://api.openai.com/v1  # Optional, defaults to OpenAI's API
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

The example implements a graph with the following nodes:

1. **Preprocessing Node**: Logs requests and adds metadata
2. **System Prompt Node**: Provides system instructions to the model
3. **Tool Calling Node**: Handles tool execution
4. **Model Node**: Processes inputs using the OpenAI model
5. **Postprocessing Node**: Adds a signature to the response

## API Endpoints

The server exposes the following HTTP endpoints:

- `POST /query`: For non-streaming queries
- `POST /stream`: For streaming queries
- `GET /health`: Health check endpoint

## Example Queries

Try these example queries:

- "What are the key principles of software architecture?"
- "Can you explain the Model-View-Controller pattern with a simple example?"
- "What's the weather like in San Francisco?" (demonstrates tool usage)
- "Compare microservices vs. monolithic architecture"

## Customization

You can customize the example by:

1. Adding more tools to the `buildGraph` function
2. Modifying the system prompt
3. Changing the graph structure
4. Implementing additional node types 