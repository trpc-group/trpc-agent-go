# Parallel Execution Graph Example

This example demonstrates parallel execution in graph workflows using the `trpc-agent-go` library. It shows how to create graphs with multiple edges from the same node and execute nodes in parallel.

## Overview

The parallel execution workflow processes text input through multiple parallel paths:

1. **Preprocessing** - Validates and prepares input text
2. **Analysis** - Analyzes text content using LLM and tools
3. **Parallel Processing** - Routes to three parallel nodes:
   - **Summarize** - Creates concise summaries
   - **Enhance** - Improves content quality
   - **Classify** - Categorizes content
4. **Aggregation** - Collects results from parallel nodes
5. **Output Formatting** - Formats final results

## Key Features

### 🔄 Multiple Edges from Same Node
The example demonstrates how to create multiple edges from a single routing node:
```go
// Add multiple edges from the routing node to parallel nodes
stateGraph.AddEdge("route_to_parallel", "summarize")
stateGraph.AddEdge("route_to_parallel", "enhance") 
stateGraph.AddEdge("route_to_parallel", "classify")
```

### 📊 Parallel Execution Tracking
- Tracks execution order of nodes
- Monitors performance of parallel nodes
- Aggregates results from multiple paths

### 🔍 Enhanced Debugging
- Comprehensive logging for parallel execution
- Tool input/output display
- Model execution metadata
- Performance monitoring

## Graph Structure

```
preprocess → analyze → tools → route_to_parallel
                                    ↓
                    ┌───────────────┼───────────────┐
                    ↓               ↓               ↓
                summarize       enhance        classify
                    ↓               ↓               ↓
                    └───────────────┼───────────────┘
                                    ↓
                              aggregate → format_output
```

## Usage

### Prerequisites
- Go 1.21 or later
- Access to an LLM model (default: deepseek-chat)

### Running the Example

```bash
# Navigate to the parallel example directory
cd trpc-agent-go/examples/graph/parallel

# Run with default model
go run main.go

# Run with specific model
go run main.go -model deepseek-chat
```

### Interactive Mode

The example runs in interactive mode where you can:

1. **Enter text** - Process any text through the parallel workflow
2. **View execution** - See real-time execution of parallel nodes
3. **Monitor performance** - Track execution times and order
4. **Analyze results** - View aggregated results from all parallel paths

### Example Commands

```
📄 Text: This is a sample text for parallel processing
```

The workflow will:
- Preprocess the input
- Analyze content using LLM and tools
- Route to three parallel processing nodes
- Execute summarize, enhance, and classify simultaneously
- Aggregate all results
- Display formatted output

## Expected Output

```
🚀 Parallel Execution Workflow Example
Model: deepseek-chat
==================================================
✅ Parallel workflow ready! Session: parallel-session-1234567890

💡 Interactive Parallel Processing Mode
   Enter your text content (or 'exit' to quit)
   Type 'help' for available commands

📄 Text: Sample text for testing

🔄 Processing input: Sample text for testing
------------------------------------------------------------

🚀 Entering node: preprocess (function)
🔧 [NODE] Preprocessing input text...
📝 [NODE] Preprocessed text length: 25 characters
✅ Completed node: preprocess (function)

🚀 Entering node: analyze (llm)
   🤖 Using model: deepseek-chat
   📝 Model Input: Sample text for testing
🤖 [MODEL] Starting: deepseek-chat (Node: analyze)
   📝 Input: You are a text analysis expert...
✅ [MODEL] Completed: deepseek-chat (Node: analyze) in 2.5s
✅ Completed node: analyze (llm)

🚀 Entering node: route_to_parallel (function)
🔄 [NODE] Routing to parallel processing nodes...
🚀 [NODE] Routing to parallel nodes: [summarize enhance classify]
✅ Completed node: route_to_parallel (function)

🚀 Entering node: summarize (llm)
🚀 Entering node: enhance (llm)  
🚀 Entering node: classify (llm)
   🤖 Using model: deepseek-chat
   🤖 Using model: deepseek-chat
   🤖 Using model: deepseek-chat
🤖 [MODEL] Starting: deepseek-chat (Node: summarize)
🤖 [MODEL] Starting: deepseek-chat (Node: enhance)
🤖 [MODEL] Starting: deepseek-chat (Node: classify)
✅ [MODEL] Completed: deepseek-chat (Node: summarize) in 1.8s
✅ [MODEL] Completed: deepseek-chat (Node: enhance) in 2.1s
✅ [MODEL] Completed: deepseek-chat (Node: classify) in 1.9s
✅ Completed node: summarize (llm)
✅ Completed node: enhance (llm)
✅ Completed node: classify (llm)

🚀 Entering node: aggregate (function)
🔗 [NODE] Aggregating results from parallel nodes...
📈 [NODE] Execution order: [preprocess analyze route_to_parallel summarize enhance classify aggregate]
📄 [NODE] Found summary result
✨ [NODE] Found enhancement result
🏷️ [NODE] Found classification result
📊 [NODE] Aggregated 3 parallel results
✅ Completed node: aggregate (function)

🚀 Entering node: format_output (function)
🎨 [NODE] Formatting final output...
✅ [NODE] Output formatting complete
✅ Completed node: format_output (function)

╔══════════════════════════════════════════════════════════════════╗
║                    PARALLEL PROCESSING RESULTS                   ║
╚══════════════════════════════════════════════════════════════════╝

📈 Execution Order: [preprocess analyze route_to_parallel summarize enhance classify aggregate format_output]

🔄 Parallel Processing Results:
   Total Results: 3

📋 Summary:
   A concise summary of the sample text...

📋 Enhanced:
   An improved version of the sample text...

📋 Classification:
   Content type: informational, complexity: simple...

╔══════════════════════════════════════════════════════════════════╗
║                         PROCESSING DETAILS                       ║
╚══════════════════════════════════════════════════════════════════╝

📊 Processing Statistics:
   • Total Results: 3
   • Execution Order: [preprocess analyze route_to_parallel summarize enhance classify aggregate format_output]
   • Aggregated At: 2025-01-16 12:34:56

✅ Processing completed successfully!
```

## Key Insights

### Parallel Execution Behavior
- **Concurrent Processing**: Multiple nodes can execute simultaneously
- **Order Independence**: Parallel nodes may complete in different orders
- **Result Aggregation**: All parallel results are collected before proceeding

### Debugging Features
- **Execution Tracking**: Complete order of node execution
- **Performance Monitoring**: Execution times for each node
- **Detailed Logging**: Input/output for tools and models
- **State Management**: Proper state passing between nodes

### Graph Design Patterns
- **Fan-out Pattern**: Single node routes to multiple parallel nodes
- **Fan-in Pattern**: Multiple parallel nodes converge to aggregation
- **Conditional Routing**: Dynamic routing based on analysis results

## Testing Parallel Execution

This example is designed to test:

1. **Multiple Edge Support**: Verify that multiple edges from the same node work correctly
2. **Parallel Execution**: Confirm that nodes can execute in parallel
3. **State Management**: Ensure proper state passing in parallel scenarios
4. **Result Aggregation**: Test collection and combination of parallel results
5. **Error Handling**: Verify error propagation in parallel execution

## Troubleshooting

### Common Issues

1. **Sequential Execution**: If nodes appear to execute sequentially instead of in parallel, check:
   - Graph compilation errors
   - Node dependencies
   - Resource constraints

2. **Missing Results**: If some parallel results are missing:
   - Check node execution logs
   - Verify state key names
   - Ensure proper result aggregation

3. **Performance Issues**: If parallel execution is slow:
   - Monitor individual node performance
   - Check for blocking operations
   - Verify resource availability

### Debug Commands

```bash
# Run with verbose logging
go run main.go -model deepseek-chat

# Check for compilation errors
go build main.go

# Run tests (if available)
go test -v
```

## Contributing

When modifying this example:

1. **Maintain Parallel Structure**: Keep the fan-out/fan-in pattern
2. **Add Debugging**: Include comprehensive logging for parallel execution
3. **Test Edge Cases**: Verify behavior with different input types
4. **Document Changes**: Update this README with new features

## Related Examples

- [Basic Graph Example](../basic/) - Simple linear workflow
- [Document Processing](../main.go) - Complex conditional routing
- [Tool Integration](../basic/) - Tool usage in graphs

## License

This example is part of the `trpc-agent-go` project and is licensed under the Apache License Version 2.0.

