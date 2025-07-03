
This example demonstrates the **ChainAgent** implementation, showcasing how multiple specialized agents work together in sequence.

## Architecture

```
User Input → Planning Agent → Research Agent → Writing Agent → Response
```

**Chain Flow:**
1. **📋 Planning Agent** - Analyzes requests and creates structured plans
2. **🔍 Research Agent** - Gathers information using tools (web_search, knowledge_base)  
3. **✍️ Writing Agent** - Composes final responses based on planning and research

## Key Features

- 🔗 Sequential agent processing
- 🌊 Streaming output with agent transitions
- 🔧 Tool integration (simulated web search and knowledge base)
- 📊 Visual agent indicators
- 💾 Session management

## Prerequisites

- Go 1.23+
- OpenAI API key

## Usage

```bash
cd examples/multiagent/chain
export OPENAI_API_KEY="your-api-key"
go run main.go
```

### Command Options

```bash
go run main.go -model gpt-4o  # Use specific model
```

## Example Session

```
🔗 Multi-Agent Chain Demo
Chain: Planning → Research → Writing
==================================================

👤 You: Explain renewable energy benefits

📋 Planning Agent: I'll create a structured analysis plan...

🔍 Research Agent: 
🔧 Using tools:
   • web_search (ID: call_123)
🔄 Executing...
✅ Tool result: Recent renewable energy data...

✍️ Writing Agent: Based on planning and research:
[Comprehensive structured response]
```

## Tools Available

- **web_search**: Simulates web search for current information
- **knowledge_base**: Simulates internal knowledge queries

## Environment Variables

| Variable | Required | Default |
|----------|----------|---------|
| `OPENAI_API_KEY` | Yes | - |
| `OPENAI_BASE_URL` | No | `https://api.openai.com/v1` |

## Customization

Modify the chain by:
- Adding/removing agents in sequence
- Changing agent instructions and prompts
- Adding new tools for research agent
- Adjusting model parameters
 