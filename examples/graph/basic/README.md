# Document Processing Workflow Example

This example demonstrates how to build and execute complex workflows using the trpc-agent-go graph package with GraphAgent and Runner. It showcases:

- Building graphs with StateGraph and Builder
- Creating function nodes and LLM nodes  
- Implementing conditional routing
- **Type-safe state management with constants and helpers**
- Using state management with schemas
- **Creating GraphAgent from compiled graphs**
- **Using Runner for session management and execution**
- Executing workflows with streaming responses

## Features

The example implements a document processing pipeline that:

1. **Preprocesses** input documents
2. **Analyzes** document complexity using LLM
3. **Routes** based on complexity (simple vs complex)
4. **Processes** documents differently based on complexity
5. **Assesses** quality of processed content
6. **Enhances** low-quality content using LLM
7. **Formats** final output with statistics

## State Management Improvements

### **Type-Safe State Keys**

This example demonstrates improved state management inspired by LangGraph's approach. Instead of hardcoded strings, we use:

#### **Before (Hardcoded Strings)**
```go
// ❌ Error-prone, no IDE support
state := graph.State{
    "user_input":      input,           // Typo-prone
    "document_length": len(input),      // No validation
    "complexity_level": "simple",       // No type safety
}

// ❌ Runtime type assertions needed
if userInput, ok := state["user_input"].(string); ok {
    // Process input
}
```

#### **After (Constants + Helpers)**
```go
// ✅ Type-safe constants
const (
    StateKeyUserInput       = "user_input"
    StateKeyDocumentLength  = "document_length"
    StateKeyComplexityLevel = "complexity_level"
)

const (
    ComplexitySimple  = "simple"
    ComplexityComplex = "complex"
)

// ✅ StateBuilder for clean construction
return NewStateBuilder().
    SetUserInput(input).
    SetDocumentLength(len(input)).
    SetComplexityLevel(ComplexitySimple).
    Build()

// ✅ StateHelper for safe access
helper := NewStateHelper(state)
input, err := helper.GetUserInput()
if err != nil {
    return nil, fmt.Errorf("failed to get user input: %w", err)
}
```

### **Benefits**

1. **IDE Support**: Autocomplete, refactoring, go-to-definition
2. **Compile-time Safety**: Wrong key names caught before runtime  
3. **Type Validation**: Clear error messages for wrong types
4. **Self-Documenting**: Constants show available state fields
5. **Maintainability**: Easy to rename fields across the codebase

### **State Schema Documentation**

The `state.go` file provides:
- **Complete state schema documentation** in `DocumentWorkflowState`
- **StateHelper** for type-safe field access with error handling
- **StateBuilder** for fluent state construction
- **Validation functions** for enum values
- **Convenience methods** like `IsSimple()`, `IsComplex()`

## Architecture

The workflow uses a **GraphAgent and Runner architecture**:

- **Graph**: Defines the workflow structure with nodes and edges
- **GraphAgent**: Wraps the graph with agent capabilities and state management
- **Runner**: Manages execution, session handling, and streaming responses
- **Function Nodes**: Pure Go functions for preprocessing, quality assessment, and formatting
- **LLM Nodes**: AI-powered nodes for document analysis, summarization, and enhancement
- **Conditional Routing**: Dynamic path selection based on document complexity and quality

## Usage

### Run with default examples:
```bash
go run . 
```

### Run in interactive mode:
```bash
go run . -interactive
```

### Use a different model:
```bash
go run . -model "gpt-4"
```

## State Schema

The example uses a custom state schema with these fields:

- `messages`: Conversation history with the LLM (managed by GraphAgent)
- `input`: Original document content (provided by Runner)
- `user_input`: Processed document content  
- `document_length`: Character count
- `word_count`: Word count
- `complexity_level`: Simple, moderate, or complex
- `quality_score`: Quality assessment score (0.0-1.0)
- `processing_type`: Type of processing applied

## Example Workflow

```
User Input (via Runner)
      ↓
   Preprocess
      ↓
   Analyze (LLM)
      ↓
 Route by Complexity
     ↙     ↘
Simple     Complex
Process   Summarize (LLM)
     ↘     ↙
   Assess Quality
      ↓
 Route by Quality
     ↙     ↘
  Good     Poor
    ↓     Enhance (LLM)
    ↓        ↓
   Format Output
      ↓
   Final Result
```

## Interactive Mode

In interactive mode, you can:

- Process custom documents by pasting content
- See real-time workflow execution
- View processing statistics
- Type `help` for available commands
- Type `exit` to quit

## Customization

To customize the workflow:

1. **Add new node types**: Implement `NodeFunc` functions
2. **Modify routing logic**: Update conditional functions
3. **Extend state schema**: Add custom fields with reducers
4. **Change LLM prompts**: Modify system prompts for different behavior
5. **Add tools**: Create function tools for LLM nodes

## Requirements

- Go 1.21+
- Valid OpenAI API key (set via environment or configuration)
- Network connectivity for LLM calls
