# Tool Example

This example demonstrates how to use the Tool system with AI models, showing how to create function-based tools that can be called by language models during conversations. The example includes both streaming and non-streaming implementations with weather and population tools.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `MODEL_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

**Note**: The `OPENAI_API_KEY` is required for the example to work. The model will use the tools to provide more accurate and contextual responses.


## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `gpt-4o-mini` |

## Directory Structure

```
examples/tool/
├── README.md           # This file
├── main.go            # Main entry point and configuration
├── functool.go        # Tool function implementations
├── non-streaming.go   # Non-streaming example with tool usage
├── streaming.go       # Streaming example with multiple tools
└── run.sh            # Script to run with predefined configuration
```

## Available Tools

### 1. Weather Tool (`get_weather`)

Returns weather information for a given location.

**Input:**
```json
{
  "location": "string"
}
```

**Output:**
```json
{
  "weather": "string"
}
```

### 2. Population Tool (`get_population`)

Returns population information for a given city.

**Input:**
```json
{
  "city": "string"
}
```

**Output:**
```json
{
  "population": "number"
}
```

## Running the Examples

### Using custom environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"

cd examples/tool
go run . -model="gpt-4o-mini"
```


## Example Output

The example demonstrates two scenarios:

### 1. Non-streaming Example

```
=== Non-streaming Example ===
Response: Based on the weather tool, the current weather in New York City is Sunny with a temperature of 25°C. It's a beautiful day!

Tool Calls:
- get_weather called with: {"location": "New York City"}
- Tool result: {"weather": "Sunny, 25°C"}

Usage: 150 tokens (prompt: 50, completion: 100)
Finish Reason: stop
```

### 2. Streaming Input Example

```
=== Streaming Example ===
Streaming response: 
Let me check the weather and population for London...

[Tool Call: get_weather]
Arguments: {"location": "London City"}
Result: {"weather": "Sunny, 25°C"}

[Tool Call: get_population]  
Arguments: {"city": "London City"}
Result: {"population": 8000000}

Based on the information I retrieved:
- Weather in London: Sunny, 25°C
- Population of London: approximately 8,000,000 people

Usage: 180 tokens (prompt: 60, completion: 120)
```

### 2. Streaming Output Example

The data results of Streaming Output are similar to Non-streaming, Streaming Output merges all the stream data

```
=== Non-streaming Example ===
Response: Based on the weather tool, the current weather in New York City is Sunny with a temperature of 25°C. It's a beautiful day!

Tool Calls:
- get_weather called with: {"location": "New York City"}
- Tool result: {"weather": "Sunny, 25°C"}

Usage: 150 tokens (prompt: 50, completion: 100)
Finish Reason: stop
```

