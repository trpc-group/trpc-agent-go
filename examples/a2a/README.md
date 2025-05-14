# A2A with ReAct Agent Example

This example demonstrates how to implement an A2A (Agent-to-Agent) server that uses a Gemini ReAct agent to process tasks. It shows how to integrate the A2A protocol with a reasoning agent that can use both local tools and MCP (Microservice Control Platform) tools.

## Features

- **A2A Server**: Implements a protocol-compliant agent server
- **ReAct Agent**: Uses Google's Gemini model for reasoning and acting
- **Local Tools**: Includes calculator, translator, unit converter, and text analysis tools
- **MCP Integration**: Connects to MCP services for additional tools:
  - Weather lookup with geolocation
  - Currency conversion with market info
  - Data analysis with statistics and trend detection
- **Schema-Based Parameter Handling**: Intelligently processes tool parameters using JSON schemas
- **Thought Process**: Captures and exposes the agent's reasoning process as artifacts
- **Streaming API**: Shows how to use the streaming API for real-time updates

## Prerequisites

- Go 1.19 or later
- Google API Key for Gemini (https://makersuite.google.com/app/apikey)

## Setting Up

1. Set your Google API key as an environment variable:

```bash
export GOOGLE_API_KEY=your_api_key_here
```

2. Build the example:

```bash
cd examples/a2a
go build -o a2a_example
```

## Running the Example

### Start the Server

```bash
# Start the A2A server with the MCP integration
./run_with_mcp.sh
```

The server will start listening on localhost:8080 by default. It will also start a mock MCP server on localhost:3000 that provides additional tools.

### Run the Client

To send a task using the regular polling API:

```bash
# Send a predefined message with MCP tool queries
./run_client.sh

# Or run manually with a custom message
./a2a_example -server=false -message="What's the weather like in Tokyo? Calculate 25 * 16 and translate 'hello' to Spanish."
```

To use the streaming API for real-time updates:

```bash
# Stream with a custom message
./a2a_example -server=false -stream -message="What's the weather in New York today? Convert 100 USD to EUR."
```

## Available Tools

### Local Tools

1. **Calculator**: Performs mathematical calculations
   - Example: "Calculate the square root of 225"

2. **Translator**: Translates text between languages
   - Example: "Translate 'hello' to Spanish"

3. **Unit Converter**: Converts between different units of measurement
   - Example: "Convert 5 kilometers to miles"

4. **Text Analysis**: Analyzes text for statistics, sentiment, and key phrases
   - Example: "Analyze the sentiment of 'I love this example, it's fantastic!'"

### MCP Tools

1. **Weather Lookup**: Gets detailed weather information for a location
   - Example: "What's the weather like in Tokyo?"
   - Provides temperature, humidity, wind speed, conditions, and forecast

2. **Currency Converter**: Converts amounts between different currencies
   - Example: "Convert 100 USD to EUR"
   - Includes exchange rates and market information

3. **Data Analyzer**: Performs statistical analysis on data series
   - Example: "Analyze this data series: 10, 15, 12, 18, 20"
   - Provides statistics (mean, median, min, max, etc.) and trend analysis

## How It Works

### Server Implementation

The server uses the following components:

1. **A2A Server**: Handles HTTP requests and JSON-RPC messaging
2. **Task Manager**: Manages task lifecycle and notifications 
3. **ReAct Agent**: Processes tasks using reasoning and tool use
4. **Tool Registry**: Maintains both local and MCP-based tools

### ReAct Agent Flow

The ReAct agent follows these steps:

1. **Thinking**: The agent considers the problem and formulates a plan
2. **Acting**: It selects a tool and constructs appropriate parameters
3. **Observing**: It processes the tool's result
4. **Repeating**: It continues the cycle until the task is complete
5. **Responding**: It generates a final comprehensive response

The thought process is captured and made available as an artifact, providing transparency into the agent's reasoning.

### Parameter Handling

The example includes robust parameter handling for both local and MCP tools:

1. **Schema Extraction**: Parameters are defined with types and descriptions
2. **Parameter Inference**: Identifies primary parameters when formatting is ambiguous
3. **Type Conversion**: Automatically converts parameter values to expected types

## Code Structure

- `main.go`: Contains server, client, ReAct agent, and tool implementations
- `run_with_mcp.sh`: Script for running the server with MCP integration
- `run_client.sh`: Script for testing the client with MCP tools

## Extending the Example

You can extend this example by:

1. Adding more sophisticated tools (e.g., database access, file manipulation)
2. Enhancing the agent prompts for better reasoning
3. Adding authentication and authorization
4. Implementing user feedback mechanisms
5. Creating more specialized MCP tools for domain-specific tasks 