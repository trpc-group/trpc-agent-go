# Multi-Agent Cycle Example

This example demonstrates the **CycleAgent** implementation, showcasing how multiple specialized agents work together in an iterative loop to refine solutions progressively.

## Architecture

```
User Input → Generate Agent → Critic Agent → Repeat Until Quality Met
                    ↑                              ↓
                    └─────── Quality Check Loop ──┘
```

**Cycle Flow:**
1. **🤖 Generate Agent** - Creates content based on user prompts and improvement feedback
2. **👀 Critic Agent** - Evaluates generated content and provides quality assessment

## Key Features

- 🔄 Iterative agent processing with quality-driven loops
- 🌊 Streaming output with iteration tracking
- 🔧 Tool integration (record_score, solution_store)
- 📊 Visual iteration indicators and agent transitions
- 🎯 Configurable max iterations and automatic quality-based stopping
- 💾 Session management

## Prerequisites

- Go 1.23+
- OpenAI API key

## Usage

```bash
cd examples/multiagent/cycle
export OPENAI_API_KEY="your-api-key"
go run .
```

### Command Options

```bash
go run . -model gpt-4o              # Use specific model
go run . -max-iterations 5          # Set maximum iterations
```

## Example Session

```
🔄 Multi-Agent Cycle Demo
Max Iterations: 3
Cycle: Generate → Critique → Improve → Repeat
==================================================

👤 You: write a short joke

🤖 Cycle Response:

🤖 Generate Agent: Why don't skeletons fight each other?  
Because they don't have the guts!

👀 Critic Agent: Here's a short joke for you:
**Why don't skeletons fight each other?**  
Because they don't have the guts!

Now, let me evaluate this joke.
🔧 Using tools:
   • record_score (ID: call_123)
🔄 Executing...
✅ Quality Score: 75/100
⚠️  Needs improvement - continuing iteration

🔄 **Iteration 2**

🤖 Generate Agent: Here's a refined version with a fresh twist:
**Why don't skeletons ever win arguments?**  
Because they always lose their backbone halfway through!

👀 Critic Agent: 
🔧 Using tools:
   • record_score (ID: call_456)  
🔄 Executing...
✅ Quality Score: 85/100
🎉 Quality threshold met - cycle complete

🏁 Cycle completed after 2 iteration(s)
```

## Cycle Behavior

The cycle agent continues iterating until one of these conditions is met:

1. **Maximum iterations reached** (configurable via `-max-iterations`)
2. **Quality threshold met** (determined by record_score tool, score ≥ 82)
3. **Escalation event** (error or explicit completion signal)

## Tools Available

- **record_score**: Assesses solution quality and determines if iteration should continue
- **solution_store**: Stores and tracks solution iterations for comparison

## Quality-Driven Iteration

The critic agent uses the `record_score` tool to:
- Score solution quality (0-100)
- Determine if additional iteration is needed (scores below 82 need improvement)
- Provide specific recommendations for improvement

When quality score ≥ 82, the cycle completes early. Otherwise, it continues refining up to the maximum iterations.

## Environment Variables

| Variable | Required | Default |
|----------|----------|---------|
| `OPENAI_API_KEY` | Yes | - |
| `OPENAI_BASE_URL` | No | `https://api.openai.com/v1` |

## Customization

Modify the cycle by:
- Adjusting quality thresholds in the `qualityEscalationFunc` function
- Adding/removing agents in the cycle sequence  
- Changing max iterations via command line
- Adding new tools for enhanced validation
- Modifying agent instructions for different domains

## Use Cases

Perfect for scenarios requiring iterative refinement:
- **Content creation**: Generate → critique → refine → repeat  
- **Solution optimization**: Create → assess → improve → repeat
- **Problem-solving**: Propose → evaluate → enhance → repeat
- **Quality assurance**: Draft → review → revise → repeat 