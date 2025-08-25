# React Planning Agent Example

This example demonstrates how to use the React planner with trpc-agent-go to create an intelligent agent that uses structured planning to approach complex queries.

## Overview

The React (Reasoning and Acting) planner guides the language model through a structured thinking process:

1. **Planning** (`/*PLANNING*/`): Create a clear plan to answer the user's question
2. **Reasoning** (`/*REASONING*/`): Provide reasoning between tool executions 
3. **Action** (`/*ACTION*/`): Execute tools according to the plan
4. **Replanning** (`/*REPLANNING*/`): Revise the plan if needed based on results
5. **Final Answer** (`/*FINAL_ANSWER*/`): Provide a comprehensive answer

## Features

- **Structured Planning**: The agent creates explicit plans before taking action
- **Visual Formatting**: Different planning sections are visually distinguished in output
- **Tool Integration**: Demonstrates planning with search, calculator, and weather tools
- **Streaming Response**: Real-time visualization of the planning process
- **Session Management**: Maintains conversation context across turns

## Tools Available

- **search**: Search for information on any topic
- **calculator**: Perform mathematical calculations
- **get_weather**: Get weather information for locations

## Building and Running

```bash
# Build the example
cd examples/react
go build -o react-demo .

# Run with default model (deepseek-chat)
./react-demo

# Run with a specific model
./react-demo -model gpt-4o-mini
```

## Environment Setup

Make sure you have the required API keys set:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"  # For deepseek
```

## Example Interactions

Try asking complex questions that benefit from structured planning:

### Population Comparison
```
You: What's the population of Tokyo and how does it compare to New York?
```

The agent will:
1. **Plan** to search for both populations and compare them
2. **Execute** search tools for both cities
3. **Reason** about the results
4. **Provide** a comprehensive comparison

### Financial Calculation
```
You: If I invest $1000 at 5% interest compounded annually, what will it be worth in 10 years?
```

The agent will:
1. **Plan** to use the compound interest formula
2. **Search** for the formula if needed
3. **Calculate** the result
4. **Explain** the calculation process

### Weather Planning
```
You: What's the weather like in Paris and should I pack an umbrella?
```

The agent will:
1. **Plan** to check weather and provide recommendations
2. **Execute** weather lookup
3. **Reason** about the conditions
4. **Recommend** appropriate preparation

## Visual Output Format

The example formats React planning sections with visual indicators:

- 📋 **PLANNING**: Shows the agent's plan
- 🤔 **REASONING**: Displays reasoning between actions
- ⚡ **ACTION**: Indicates tool execution
- 🔄 **REPLANNING**: Shows plan revisions
- 🎯 **FINAL ANSWER**: Presents the final response
- 🔧 **Executing tools**: Shows when tools are being called

## Code Structure

- `main.go`: Complete example with React planner integration
- Demonstrates proper setup of:
  - React planner (`react.New()`)
  - LLM agent with planner option
  - Request/response processors
  - Tool integration
  - Streaming response handling

## Integration with trpc-agent-go

The example shows how to integrate the React planner into the agent flow:

```go
// Create React planner
reactPlanner := react.New()

// Add to LLM agent
llmAgent := llmagent.New(
    agentName,
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Your agent description"),
    llmagent.WithInstruction("Your agent instruction"),
    llmagent.WithPlanner(reactPlanner),
    // ... other options
)
```

The planner automatically:
- Adds planning instructions to requests
- Processes responses to organize content by tags
- Integrates with the existing flow system

## Tips for Best Results

1. **Complex Queries**: React planning works best with multi-step questions
2. **Tool Usage**: Questions requiring multiple tools showcase the planning benefits
3. **Clear Prompts**: Well-defined questions lead to better structured plans
4. **Model Selection**: More capable models (GPT-4, Claude-3.5) produce better planning

## Customization

You can customize the React planner by:
- Modifying the planning instruction templates
- Adding custom formatting for different planning tags
- Integrating additional tools
- Adjusting the response processing logic

See the [planner/react](https://github.com/trpc-group/trpc-agent-go/tree/main/planner/react) package for implementation details. 
