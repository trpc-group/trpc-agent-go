# Agent Transfer Example

This example demonstrates the agent transfer functionality in trpc-agent-go, showing how to create a coordinator agent that can delegate tasks to specialized sub-agents using the `transfer_to_agent` tool.

## Overview

The transfer system enables intelligent task delegation across multiple specialized agents:

1. **Coordinator Agent**: Main agent that analyzes requests and delegates to specialists
2. **Sub-Agents**: Specialized agents with domain-specific tools and expertise
3. **Transfer Tool**: Built-in `transfer_to_agent` tool for seamless handoffs
4. **Visual Flow**: Clear indication of which agent is handling each part of the conversation

## Architecture

```
┌─────────────────────┐
│  Coordinator Agent  │ ← Main entry point
│  🎯                 │
└─────────┬───────────┘
          │ transfer_to_agent()
          ▼
┌─────────────────────┬─────────────────────┬─────────────────────┐
│   Math Agent        │   Weather Agent     │  Research Agent     │
│   🧮                │   🌤️                │  🔍                 │
│   - calculate tool  │   - get_weather     │   - search tool     │
└─────────────────────┴─────────────────────┴─────────────────────┘
```

## Sub-Agents

### Math Agent (`math-agent`)
- **Purpose**: Mathematical calculations and numerical problems
- **Tools**: `calculate` - performs arithmetic operations
- **Specialization**: Step-by-step mathematical reasoning
- **Temperature**: 0.3 (precision-focused)

### Weather Agent (`weather-agent`)
- **Purpose**: Weather information and recommendations
- **Tools**: `get_weather` - retrieves weather data for locations
- **Specialization**: Weather analysis and activity recommendations
- **Temperature**: 0.5 (balanced)

### Research Agent (`research-agent`)
- **Purpose**: Information gathering and research
- **Tools**: `search` - finds information on topics
- **Specialization**: Comprehensive research and structured answers
- **Temperature**: 0.7 (creative)

## Building and Running

```bash
# Build the example
cd examples/transfer
go build -o transfer-demo .

# Run with default model (deepseek-chat)
./transfer-demo

# Run with a specific model
./transfer-demo -model gpt-4o-mini
```

## Environment Setup

Set the required API keys:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"  # For DeepSeek
```

## Example Interactions

### Mathematical Task Transfer
```
You: Calculate compound interest on $5000 at 6% for 8 years

🎯 Coordinator: I'll transfer this to our math specialist for accurate calculation.
🔄 Initiating transfer...
🔄 Transfer Event: Transferring control to agent: math-agent
🧮 Math Specialist: I'll help you calculate the compound interest step by step.
🔧 🧮 executing tools:
   • calculate ({"operation":"power","a":1.06,"b":8})
   ✅ Tool completed
🔧 🧮 executing tools:
   • calculate ({"operation":"multiply","a":5000,"b":1.593})
   ✅ Tool completed

The compound interest calculation:
- Principal: $5,000
- Rate: 6% annually
- Time: 8 years
- Result: $7,969.24 (approximately $2,969.24 in interest)
```

### Weather Information Transfer
```
You: What's the weather like in Tokyo today?

🎯 Coordinator: Let me transfer you to our weather specialist for current conditions.
🔄 Initiating transfer...
🔄 Transfer Event: Transferring control to agent: weather-agent
🌤️ Weather Specialist: I'll get the current weather information for Tokyo.
🔧 🌤️ executing tools:
   • get_weather ({"location":"Tokyo"})
   ✅ Tool completed

Current weather in Tokyo, Japan:
- Temperature: 22.5°C
- Condition: Partly Cloudy
- Humidity: 65%
- Recommendation: Perfect weather for outdoor activities
```

### Research Task Transfer
```
You: Tell me about renewable energy trends

🎯 Coordinator: This is a research question - I'll transfer to our research specialist.
🔄 Initiating transfer...
🔄 Transfer Event: Transferring control to agent: research-agent
🔍 Research Specialist: I'll gather comprehensive information about renewable energy trends.
🔧 🔍 executing tools:
   • search ({"query":"renewable energy trends"})
   ✅ Tool completed

Based on current research, here are key renewable energy trends:
1. Renewable energy capacity increased by 295 GW in 2022
2. Solar and wind power account for 90% of new renewable capacity
3. Global investment in renewable energy reached $1.8 trillion
4. Renewable energy costs have decreased by 85% since 2010
```

## Key Features

### Intelligent Delegation
- The coordinator analyzes request content and context
- Automatically selects the most appropriate specialist
- Explains the reasoning for each transfer

### Visual Transfer Flow
- 🎯 Coordinator Agent responses
- 🧮 Math Agent responses  
- 🌤️ Weather Agent responses
- 🔍 Research Agent responses
- 🔄 Transfer events and tool executions

### Seamless Context Passing
- Complete conversation history is maintained
- Each specialist has full context of the request
- No information is lost during transfers

### Flexible Architecture
- Easy to add new sub-agents
- Configurable agent personalities and parameters
- Modular tool assignments

## Implementation Details

### Setting Up Sub-Agents

```go
// Create specialized agents
mathAgent := createMathAgent(modelInstance)
weatherAgent := createWeatherAgent(modelInstance)
researchAgent := createResearchAgent(modelInstance)

// Configure coordinator with sub-agents
coordinatorAgent := llmagent.New(
    "coordinator-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithSubAgents([]agent.Agent{
        mathAgent,
        weatherAgent, 
        researchAgent,
    }),
    // ... other options
)
```

### Transfer Tool Usage

The `transfer_to_agent` tool is automatically available when sub-agents are configured:

```json
{
  "name": "transfer_to_agent",
  "description": "Transfer control to another agent",
  "parameters": {
    "agent_name": "math-agent",
    "message": "Calculate compound interest",
    "end_invocation": true
  }
}
```

### Event Flow Processing

The example demonstrates handling transfer events:

```go
// Handle agent transfers
if event.Object == model.ObjectTypeTransfer {
    fmt.Printf("🔄 Transfer Event: %s\n", 
               event.Response.Choices[0].Message.Content)
    // Update current agent context
    currentAgent = getAgentFromTransfer(event)
}
```

## Best Practices

### Agent Specialization
- Give each agent a clear, focused domain
- Configure appropriate tools for each specialty
- Adjust temperature based on task requirements

### Coordinator Instructions
- Provide clear guidelines for when to transfer
- List available agents and their capabilities
- Include transfer reasoning in responses

### Tool Design
- Keep tools focused on specific domains
- Provide clear descriptions and parameters
- Handle edge cases gracefully

### Error Handling
- Check for failed transfers
- Provide fallback options
- Maintain conversation flow during errors

## Extending the Example

### Adding New Agents
1. Create agent with specialized tools
2. Add to sub-agents list
3. Update coordinator instructions
4. Add display formatting

### Custom Tools
1. Implement tool function
2. Create tool declaration
3. Add to appropriate agent
4. Test integration

### Advanced Features
- Multi-step agent chains
- Conditional transfers
- Agent-to-agent communication
- Transfer with custom messages

## Troubleshooting

### Common Issues
- **Agent not found**: Check agent names match exactly
- **Transfer fails**: Verify sub-agents are properly configured
- **Missing tools**: Ensure tools are added to correct agents
- **API errors**: Check environment variables and API keys

### Debug Tips
- Enable debug logging to trace transfers
- Check event.Author to track active agent
- Verify tool availability in each agent
- Test individual agents before integration

This example provides a complete foundation for building sophisticated multi-agent systems with intelligent task delegation in trpc-agent-go. 