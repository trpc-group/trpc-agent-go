# Basic Agent Example

This is a minimal example demonstrating how to implement a simple web server with a React agent that can use tools.

## Features

- Simple Go web server
- React agent implementation with two tools:
  - Calculator: Performs basic arithmetic operations
  - Weather: Gets simulated weather information for a location
- Displays reasoning steps (thought, action, observation) from the agent

## Prerequisites

- Go 1.19 or later
- OpenAI API key 

## Running the Example

1. Set your OpenAI API key as an environment variable (optional):

```bash
export OPENAI_API_KEY=your_openai_api_key_here
```

2. Navigate to the project root and run the example:

```bash
go run examples/basic/main.go \
    --openai-url="https://api.openai.com/v1" \
    --model-name="gpt-3.5-turbo" 
```

## Example Queries

Try asking the agent:

- "What's 25 * 4?"
- "Calculate 15 / 3"
- "What's the weather in Tokyo?"
- "What's the current temperature in New York?"

```shell
$ curl http://127.0.0.1:8080/api/agent -d '{"message": "what is the weather like in beijing"}'
{"message":"The weather in Beijing is cold with a temperature of 10°C.","steps":[{"actions":[{"input":{},"tool":"weather"}],"observations":["{\"condition\":\"Cold\",\"location\":\"\",\"temperature\":10,\"unit\":\"Celsius\"}"],"thought":"Thought process:\n1. The user is asking for the weather in Beijing.\n2. To provide an accurate answer, I need to get the current weather information for Beijing.\n3. The 'weather' tool can be used to fetch the weather details for a specific location.\n4. I will use the 'weather' tool to get the weather information for Beijing.\n\nAction: ."},{"thought":"Final Answer: The weather in Beijing is cold with a temperature of 10°C."}]}
```
