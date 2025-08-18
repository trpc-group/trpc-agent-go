# Output Key Chain Example

This example demonstrates how to use ChainAgent with output keys and placeholder variables to create a research and content creation pipeline.

## Features

- **Chain Agent**: Uses a single runner with a chain agent containing two sub-agents
- **Research Agent**: Conducts thorough research and stores findings using `output_key` functionality
- **Writer Agent**: Creates engaging content summaries based on research findings using placeholder variables
- **Session Management**: Automatic state variable injection in agent instructions
- **Interactive CLI**: Command-line interface for research and content creation
- **Streaming Output**: Real-time streaming responses from agents
- **Placeholder Variables**: Uses `{research_findings}` placeholders in instructions for state access

## Architecture

```
User Query → Research Agent → Session State → Writer Agent → Final Content
                ↓                    ↓              ↓
         Stores findings      Placeholder access   Creates content
         with output_key      {research_findings}   based on research
```

### Agents

1. **Research Agent**: Conducts comprehensive research on user topics and stores findings
2. **Writer Agent**: Creates engaging, well-structured content based on research findings

### Placeholder Variables

The writer agent uses `{research_findings}` placeholder in its instruction, which gets automatically replaced with the research data when the agent runs.

## Usage

### Building

```bash
go build -o output_key_chain main.go
```

### Running

```bash
./output_key_chain [flags]
```

### Flags

- `-model`: Name of the model to use (default: "deepseek-chat")

### Interactive Commands

- `exit`: Exit the application

## Key Implementation Details

### Research Agent Setup

The research agent is configured to conduct thorough research and store findings:

```go
researchAgent := llmagent.New(
    "research-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A research assistant that finds and extracts key information from user queries"),
    llmagent.WithInstruction("You are a skilled research assistant. When users ask questions, "+
        "conduct thorough research and extract the most important facts and information. "+
        "Focus on accuracy and provide comprehensive details that would be useful for "+
        "creating informative content. Be thorough but concise in your findings."),
    llmagent.WithGenerationConfig(genConfig),
    llmagent.WithOutputKey("research_findings"),
)
```

### Writer Agent Setup

The writer agent uses placeholder variables to access research findings:

```go
writerAgent := llmagent.New(
    "writer-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A content writer that creates engaging summaries based on research findings"),
    llmagent.WithInstruction("You are an experienced content writer. Based on the research findings: {research_findings}, "+
        "create an engaging and informative summary. Make it interesting to read, well-structured, "+
        "and accessible to a general audience. Include key facts while maintaining a conversational tone."),
    llmagent.WithGenerationConfig(genConfig),
)
```

### Chain Agent Setup

The chain agent is configured with two sub-agents without any tools:

```go
chainAgent := chainagent.New(
    "output-key-chain",
    chainagent.WithSubAgents([]agent.Agent{researchAgent, writerAgent}),
)
```

### Single Runner Pattern

All agents use a single runner with the chain agent:

```go
c.runner = runner.NewRunner(
    appName,
    chainAgent,
    runner.WithSessionService(sessionService),
)
```

## Real-World Use Case

This implementation demonstrates a practical research and content creation workflow:

1. **User asks a question** about any topic (e.g., "What are the benefits of renewable energy?")
2. **Research Agent** conducts comprehensive research and stores key findings with `output_key="research_findings"`
3. **Writer Agent** accesses the research via `{research_findings}` placeholder and creates engaging content
4. **User receives** a well-researched, engaging summary of their topic

## Example Queries

The demo includes example queries that showcase the research and writing capabilities:

- "What are the latest developments in quantum computing?"
- "Explain the impact of AI on healthcare in 2024"
- "What are the environmental benefits of electric vehicles?"
- "How does blockchain technology work and what are its applications?"
- "What are the emerging trends in renewable energy?"

## Session State Access

The framework automatically handles state variable injection in agent instructions. When an agent runs, any `{key_name}` placeholders in the instruction are replaced with the corresponding values from the session state.

## Streaming Response Handling

The implementation includes comprehensive event handling for streaming responses:

- Agent transitions with visual indicators (🔬 Research Agent, ✍️ Writer Agent)
- Streaming content display
- Simplified event processing without tool handling

This provides a practical demonstration of how output key functionality can be used in real-world applications for research and content creation workflows.
