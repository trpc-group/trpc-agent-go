# Output Schema Example

This example demonstrates how to use `WithOutputSchema` functionality with LLMAgent to constrain and validate agent responses to specific JSON schemas.

## Overview

The example implements a weather information agent that:
1. **Analyzes user queries** about weather conditions
2. **Returns structured JSON** with predefined schema validation
3. **Ensures consistent output format** for downstream processing

## Key Features

- **Structured Output**: Agent responses are constrained to a specific JSON schema
- **Schema Validation**: Automatic validation ensures output format consistency
- **Tool Restrictions**: When `WithOutputSchema` is used, agents cannot use tools (function calls, RAG, etc.)
- **Real-world Use Case**: Weather data collection with standardized format
- **Interactive CLI**: Command-line interface for testing schema validation

## Architecture

```
User Query → Weather Agent → Schema Validation → Structured JSON Output
                ↓                    ↓                    ↓
         Analyzes weather      Validates against      Returns formatted
         information          predefined schema      weather data
```

## Output Schema

The example uses a comprehensive weather schema that includes:

```json
{
  "type": "object",
  "properties": {
    "city": {
      "type": "string",
      "description": "The city name"
    },
    "temperature": {
      "type": "number",
      "description": "Temperature in Celsius"
    },
    "condition": {
      "type": "string",
      "description": "Weather condition",
      "enum": ["sunny", "cloudy", "rainy", "snowy", "foggy", "windy"]
    },
    "humidity": {
      "type": "number",
      "description": "Humidity percentage (0-100)"
    },
    "wind_speed": {
      "type": "number",
      "description": "Wind speed in km/h"
    },
    "description": {
      "type": "string",
      "description": "Human-readable weather description"
    },
    "recommendations": {
      "type": "array",
      "description": "List of recommendations based on weather",
      "items": {
        "type": "string"
      }
    }
  },
  "required": ["city", "temperature", "condition", "description"]
}
```

## Usage

### Building

```bash
go build -o outputschema main.go
```

### Running

```bash
./outputschema [flags]
```

### Flags

- `-model`: Name of the model to use (default: "deepseek-chat")

### Interactive Commands

- `exit`: Exit the application

## Example Queries

Try these example queries to see the schema validation in action:

- "What's the weather like in Beijing today?"
- "Tell me about the weather in Shanghai"
- "How's the weather in Guangzhou?"
- "Weather forecast for Shenzhen"
- "What's the climate like in Chengdu?"

## Expected Output Format

The agent will respond with structured JSON like this:

```json
{
  "city": "Beijing",
  "temperature": 22.5,
  "condition": "sunny",
  "humidity": 45,
  "wind_speed": 12,
  "description": "Beautiful sunny day in Beijing with comfortable temperatures and light breeze",
  "recommendations": [
    "Perfect weather for outdoor activities",
    "Consider wearing light clothing",
    "Don't forget sunscreen"
  ]
}
```

## Key Implementation Details

### Weather Agent Setup

The weather agent is configured with output schema validation:

```go
weatherAgent := llmagent.New(
    "weather-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A weather information agent that provides structured weather data"),
    llmagent.WithInstruction("You are a weather information specialist. When users ask about weather, "+
        "analyze their query and provide comprehensive weather information in a structured format. "+
        "Extract the city name from their query and provide realistic weather data including temperature, "+
        "conditions, humidity, wind speed, and helpful recommendations. Always respond with valid JSON "+
        "that matches the required schema."),
    llmagent.WithGenerationConfig(genConfig),
    llmagent.WithOutputSchema(weatherSchema),
)
```

### Schema Definition

The weather schema is defined as a Go map that gets converted to JSON schema:

```go
weatherSchema := map[string]interface{}{
    "type": "object",
    "properties": map[string]interface{}{
        "city": map[string]interface{}{
            "type":        "string",
            "description": "The city name",
        },
        "temperature": map[string]interface{}{
            "type":        "number",
            "description": "Temperature in Celsius",
        },
        // ... more properties
    },
    "required": []string{"city", "temperature", "condition", "description"},
}
```

### Runner Setup

The example uses a single runner with the weather agent:

```go
c.runner = runner.NewRunner(
    appName,
    weatherAgent,
    runner.WithSessionService(sessionService),
)
```

## Schema Validation Benefits

### 1. **Consistent Data Format**
- All responses follow the same structure
- Easy to parse and process programmatically
- Reduces downstream integration complexity

### 2. **Data Quality Assurance**
- Required fields are always present
- Data types are validated (string, number, array)
- Enum values ensure consistent terminology

### 3. **API Integration Ready**
- Structured output is perfect for API responses
- JSON format is widely supported
- Schema can be used for API documentation

### 4. **Error Prevention**
- Invalid responses are caught early
- Schema violations are handled gracefully
- Reduces runtime errors in production

## Limitations

When using `WithOutputSchema`:

- **No Tool Usage**: Agents cannot use function tools, RAG, or other tools
- **Response Only**: Agents can only provide responses, not perform actions
- **Schema Constraints**: All responses must conform to the defined schema

## Real-World Applications

This pattern is useful for:

- **Data Collection**: Standardized data gathering from user queries
- **API Endpoints**: Consistent response formats for web services
- **Data Processing Pipelines**: Structured input for downstream systems
- **Form Filling**: Automated form completion with validation
- **Report Generation**: Standardized report formats

## Comparison with Other Examples

| Feature | Output Schema | Output Key | Output Key State |
|---------|---------------|------------|------------------|
| **Tool Usage** | ❌ Disabled | ✅ Enabled | ✅ Enabled |
| **Schema Validation** | ✅ Required | ❌ None | ❌ None |
| **Data Flow** | Direct Response | Session State | Session State + Tools |
| **Use Case** | Structured Data | Data Storage | Complex Workflows |

This example demonstrates the power of schema validation for ensuring consistent, reliable data output from LLM agents.
