# Graph Basic Example (DSL Version)

This example is the DSL-based version of `/examples/graph/basic`.  
It shows how to use JSON DSL to recreate the original Graph example:

- document preprocessing
- complexity analysis via LLM + tools
- conditional routing based on complexity
- final result formatting

## Overview

The workflow processes a document through several stages:

- **Preprocess**: clean and normalize the document content  
- **Complexity analysis**: use LLM + tool to analyze document complexity  
- **Conditional routing**:
  - Simple → Summarize
  - Moderate → Summarize
  - Complex → Enhance  
- **Format output**: produce the final human-readable result.

## Key Concepts

### 1. Model Registry

Models are registered at startup and referenced by name in the DSL:

```go
// Register model
modelRegistry := registry.NewModelRegistry()
modelRegistry.MustRegister("deepseek-chat", modelInstance)

// In DSL
{
  "config": {
    "model_name": "deepseek-chat"
  }
}
```

### 2. Tool Registry

Tools are registered at startup and referenced by name in the DSL:

```go
// Register tool
toolRegistry := registry.NewToolRegistry()
toolRegistry.MustRegister("analyze_complexity", complexityTool)

// In DSL
{
  "config": {
    "tools": ["analyze_complexity"]
  }
}
```

### 3. Custom components

This example defines several custom components:

- `custom.preprocess_document` – document preprocessing  
- `custom.route_complexity` – route based on complexity bucket  
- `custom.complexity_condition` – conditional evaluation helper  
- `custom.format_output` – final output formatting  
- `custom.analyze_complexity_tool` – tool wrapper for complexity analysis.

## Files

```text
graph_basic/
├── main.go           # Main program: registers components, models, tools
├── components.go     # Custom component implementations
├── workflow.json     # DSL workflow definition
└── README.md         # This document
```

## Workflow structure

```text
START
  ↓
preprocess (custom.preprocess_document)
  ↓
analyze (builtin.llm + tools)
  ↓
tools (builtin.tools)
  ↓
route (custom.route_complexity)
  ↓
[condition: custom.complexity_condition]
  ├─ simple/moderate → summarize (builtin.llm)
  └─ complex → enhance (builtin.llm)
  ↓
format (custom.format_output)
  ↓
END
```

## Running the example

```bash
# Set environment variables
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"
export MODEL_NAME="deepseek-chat"

# Build
go build -o graph_basic main.go components.go

# Run
./graph_basic
```

## Expected output

```text
🚀 DSL-Based Document Processing Workflow
Model: deepseek-chat
DSL File: workflow.json
==================================================
✅ Custom components registered:
   - custom.preprocess_document
   - custom.route_complexity
   - custom.complexity_condition
   - custom.format_output
   - custom.analyze_complexity_tool

✅ Model registered in ModelRegistry: deepseek-chat (BaseURL: https://api.deepseek.com/v1)
✅ Tool registered in ToolRegistry: analyze_complexity

🔄 Processing workflow...

╔══════════════════════════════════════════════════════════════════╗
║                    DOCUMENT PROCESSING RESULTS                   ║
╚══════════════════════════════════════════════════════════════════╝

[processed document content]

╔══════════════════════════════════════════════════════════════════╗
║                         PROCESSING DETAILS                       ║
╚══════════════════════════════════════════════════════════════════╝

📊 Processing Statistics:
   • Complexity Level: moderate
   • Word Count: 29

✅ Processing completed successfully!
```

## Key code snippets

### 1. Register models and tools

```go
// Create Model Registry
modelRegistry := registry.NewModelRegistry()
modelInstance := openai.New(*modelName,
    openai.WithBaseURL(baseURL),
    openai.WithAPIKey(apiKey),
)
modelRegistry.MustRegister(*modelName, modelInstance)

// Create Tool Registry
toolRegistry := registry.NewToolRegistry()
complexityTool := function.NewFunctionTool(
    AnalyzeComplexity,
    function.WithName("analyze_complexity"),
    function.WithDescription("Analyzes document complexity level"),
)
toolRegistry.MustRegister("analyze_complexity", complexityTool)
```

### 2. Compile the DSL

```go
compiler := dsl.NewCompiler(registry.DefaultRegistry).
    WithModelRegistry(modelRegistry).
    WithToolRegistry(toolRegistry)

compiledGraph, err := compiler.Compile(workflow)
```

### 3. Create the GraphAgent

```go
// Note: you do NOT need to pass models or tools in the initial state.
// They are resolved from the registry at compile time.
graphAgent, err := graphagent.New("document-processor", compiledGraph,
    graphagent.WithDescription("DSL-based document processing workflow"),
)
```

## DSL configuration examples

### LLM node (with tools)

```json
{
  "id": "analyze",
  "component": {
    "type": "component",
    "ref": "builtin.llm"
  },
  "config": {
    "model_name": "deepseek-chat",
    "tools": ["analyze_complexity"],
    "instruction": "Analyze the document using the analyze_complexity tool"
  }
}
```

### Tools node

```json
{
  "id": "tools",
  "component": {
    "type": "component",
    "ref": "builtin.tools"
  },
  "config": {
    "tools": ["analyze_complexity"]
  }
}
```

### Conditional edge

```json
{
  "source": "route",
  "target": "summarize",
  "condition": {
    "type": "component",
    "ref": "custom.complexity_condition"
  },
  "config": {
    "expected_complexity": ["simple", "moderate"]
  }
}
```

