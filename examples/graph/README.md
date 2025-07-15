# Graph Agent Document Processing Workflow

This example demonstrates a comprehensive document processing workflow using the GraphAgent with the Runner package. It showcases how to build complex multi-agent workflows that can process documents through various stages including validation, analysis, summarization, and enhancement.

## Overview

The example implements a sophisticated document processing pipeline that:

- **Validates** document format and content quality
- **Analyzes** document type, complexity, and themes using AI agents with tools
- **Routes** processing based on document complexity (simple vs. complex paths)
- **Summarizes** complex documents using specialized AI agents
- **Assesses** content quality and enhances low-quality content
- **Formats** final output with comprehensive metadata

## Architecture

### Graph Structure

```
Start → Preprocess → Validate → Analyze → Route by Complexity
                                              ↓
                                    ┌─── Simple Process
                                    │
                                    └─── Summarize (Complex)
                                              ↓
                                    Assess Quality → Route by Quality
                                              ↓
                                    ┌─── Format Output (High Quality)
                                    │
                                    └─── Enhance → Format Output
                                              ↓
                                            End
```

### Components

1. **GraphAgent**: Main orchestrator that manages the workflow
2. **Sub-Agents**: Specialized AI agents for different tasks
   - **Validator Agent**: Document validation and quality checks
   - **Analyzer Agent**: Document analysis with complexity and theme extraction tools
   - **Summarizer Agent**: Creates comprehensive summaries for complex documents
   - **Enhancer Agent**: Improves content quality and readability
3. **Function Nodes**: Custom processing logic for routing and formatting
4. **Runner**: Manages execution, session handling, and streaming responses

## Prerequisites

### Environment Variables

Set up your API key for the deepseek-chat model:

```bash
export OPENAI_API_KEY="your-deepseek-api-key"
```

### Dependencies

The example uses the following tRPC Agent Go packages:
- `agent/graphagent` - Graph-based agent implementation
- `agent/llmagent` - LLM agent with tool support
- `graph` - Core graph functionality
- `runner` - Execution and session management
- `model/openai` - OpenAI-compatible model interface
- `tool/function` - Function tool implementation

## Usage

### Command Line Options

```bash
go run examples/graph/main.go [options]
```

**Options:**
- `-model string`: Model name to use (default: "deepseek-chat")
- `-interactive`: Run in interactive mode (default: false)

### Batch Mode (Default)

Processes predefined example documents:

```bash
go run examples/graph/main.go
```

**Example documents processed:**
1. **Simple Business Report**: Quarterly performance data
2. **Complex Technical Document**: Microservices architecture analysis
3. **Research Abstract**: AI workplace productivity study

### Interactive Mode

Process your own documents interactively:

```bash
go run examples/graph/main.go -interactive
```

**Available commands:**
- `help` - Show available commands
- `exit` or `quit` - Exit the application
- Enter any text to process it as a document

## Example Output

```
🚀 Document Processing Workflow with GraphAgent
Model: deepseek-chat
Interactive: false
==================================================
✅ Document workflow ready! Session: workflow-session-1704067200

🔄 Processing: Simple Business Report
----------------------------------------
🤖 Workflow: 
🔄 Stage 2: Executing node: Preprocess Input (preprocess)
🔄 Stage 3: Executing node: Validate Document (validate)
Document validation completed successfully. The quarterly report structure is clear and contains essential performance metrics...

🔄 Stage 4: Executing node: Analyze Document (analyze)
Document analysis complete. This is a business report with simple complexity level...

🔄 Stage 5: Executing node: Route by Complexity (route_complexity)
🔄 Stage 6: Executing node: Simple Processing (simple_process)
🔄 Stage 7: Executing node: Assess Quality (assess_quality)
🔄 Stage 8: Executing node: Route by Quality (route_quality)
🔄 Stage 9: Executing node: Format Output (format_output)

✅ Completed: Simple Business Report
```

## Workflow Details

### 1. Preprocessing
- Validates input format and length
- Calculates document metrics (length, word count)
- Sets preprocessing timestamp

### 2. Document Validation
- Uses AI agent to check document structure
- Identifies potential issues or inconsistencies
- Assesses content completeness
- Provides validation feedback

### 3. Document Analysis
- Classifies document type and genre
- Assesses complexity level using analysis tools
- Extracts key themes and topics
- Identifies main concepts and relationships

**Analysis Tools:**
- `analyze_complexity`: Analyzes document complexity level
- `extract_themes`: Extracts key themes from document content

### 4. Complexity-Based Routing
- **Simple Path**: Basic processing for straightforward documents
- **Complex Path**: Advanced summarization for sophisticated content

### 5. Quality Assessment
- Evaluates content based on multiple factors:
  - Length and structure
  - Word variety and richness
  - Formatting quality
- Assigns quality score (0.0 - 1.0)

### 6. Quality-Based Enhancement
- **High Quality**: Direct to output formatting
- **Low Quality**: Enhancement through AI agent for improved clarity and readability

### 7. Output Formatting
- Creates comprehensive final output with metadata:
  - Processed content
  - Workflow version and processing mode
  - Quality score and completion timestamp
  - Processing status

## Configuration

### Model Configuration
```go
genConfig := model.GenerationConfig{
    MaxTokens:   intPtr(1500),
    Temperature: floatPtr(0.4),
    Stream:      true,
}
```

### Graph Agent Configuration
```go
graphAgent, err := graphagent.New("document-processor", workflowGraph,
    graphagent.WithDescription("Comprehensive document processing workflow"),
    graphagent.WithSubAgents([]agent.Agent{...}),
    graphagent.WithChannelBufferSize(512),
    graphagent.WithInitialState(graph.State{
        "workflow_version":  "2.0",
        "processing_mode":   "comprehensive",
        "quality_threshold": 0.75,
    }),
)
```

## Customization

### Adding New Agents
```go
customAgent := llmagent.New("custom-processor",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Custom document processor"),
    llmagent.WithInstruction("Your custom instructions..."),
    llmagent.WithTools([]tool.Tool{customTool}),
)
```

### Adding New Function Nodes
```go
customProcessing := func(ctx context.Context, state graph.State) (graph.State, error) {
    // Your custom processing logic
    state["custom_result"] = "processed"
    return state, nil
}

// Add to graph builder
AddFunctionNode("custom", "Custom Processing", "Custom description", customProcessing)
```

### Adding New Tools
```go
customTool := function.NewFunctionTool(
    func(args CustomArgs) CustomResult {
        // Tool implementation
        return CustomResult{Result: "custom output"}
    },
    function.WithName("custom_tool"),
    function.WithDescription("Custom tool description"),
)
```

## Error Handling

The workflow includes comprehensive error handling:

- **Validation Errors**: Invalid input format or content
- **Processing Errors**: Agent execution failures
- **Timeout Handling**: Context cancellation and timeouts
- **Quality Issues**: Automatic enhancement for low-quality content

## Performance Considerations

- **Streaming**: All agents use streaming for real-time response
- **Buffer Sizes**: Configurable channel buffer sizes (default: 512)
- **Timeouts**: Proper context handling for long-running operations
- **Memory**: Efficient state management throughout the workflow

## Integration Examples

### With Session Management
```go
sessionService := inmemory.NewSessionService()
runner := runner.NewRunner(appName, graphAgent,
    runner.WithSessionService(sessionService),
)
```

### With Custom Models
```go
customModel := openai.New("custom-model", openai.Options{
    BaseURL: "https://custom-api-endpoint.com",
    ChannelBufferSize: 1024,
})
```

## Troubleshooting

### Common Issues

1. **API Key Not Set**
   ```
   Error: failed to create validator agent: API key required
   ```
   **Solution**: Set the `OPENAI_API_KEY` or `DEEPSEEK_API_KEY` environment variable

2. **Model Not Available**
   ```
   Error: model "deepseek-chat" not found
   ```
   **Solution**: Use `-model` flag with available model name

3. **Timeout Issues**
   ```
   Error: context deadline exceeded
   ```
   **Solution**: Increase timeout or check network connectivity

### Debug Mode

Enable verbose logging by modifying the log level:
```go
import "trpc.group/trpc-go/trpc-agent-go/log"

log.SetLevel(log.DebugLevel)
```

## Related Examples

- `examples/runner/main.go` - Basic runner usage with LLM agents
- `examples/llmagent/main.go` - LLM agent with tools
- `examples/multiagent/` - Multi-agent coordination patterns
