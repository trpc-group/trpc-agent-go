# Document Processing Workflow Example

This example demonstrates how to build and execute a small document-processing workflow using the trpc-agent-go graph package with GraphAgent and Runner. It showcases:

- Building graphs with `StateGraph`
- Creating Function nodes、LLM nodes、Tools nodes
- Implementing conditional routing (`AddConditionalEdges` / `AddToolsConditionalEdges`)
- Using schema-backed state management (`MessagesStateSchema` + reducers)
- Creating `GraphAgent` from a compiled `Graph`
- Running with `Runner` and streaming responses

## Features

The example implements a document processing pipeline that:

1. **Preprocesses** input documents
2. **Analyzes** document complexity using LLM
3. **Routes** based on complexity (simple vs complex)
4. **Processes** documents differently based on complexity
5. **Assesses** quality of processed content
6. **Enhances** low-quality content using LLM
7. **Formats** final output with statistics

## State Model

This example uses the message-oriented schema returned by `graph.MessagesStateSchema()`, which includes the following keys:

- `messages` (managed by LLM/tools nodes through reducers)
- `user_input`
- `last_response`
- `node_responses` (per-node textual outputs)

For the example workflow we also track:

- `document_length`, `word_count`, `complexity_level`
- `node_execution_history` (added via callbacks for formatting stats)

## Architecture

The workflow uses a Graph + GraphAgent + Runner architecture:

- **Graph**: nodes/edges, compiled by `StateGraph`
- **GraphAgent**: wraps the graph + executor
- **Runner**: sessions, event streaming, persistence
- **Function/LLM/Tools nodes**: core processing primitives
- **Conditional routing**: route via `AddConditionalEdges`/`AddToolsConditionalEdges`

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

## Runtime State Keys

Important keys used by the example:

- `messages`, `user_input`, `last_response`
- `document_length`, `word_count`, `complexity_level`
- `node_execution_history` (for stats), `error_count` (optional)

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

1. Add nodes by implementing `NodeFunc`
2. Modify conditional routing functions
3. Extend `StateSchema` with custom fields/reducers
4. Adjust prompts for LLM nodes
5. Add tools with `function.NewFunctionTool`

## Requirements

- Go 1.21 or later
- Valid API key and base URL for your model provider (OpenAI-compatible)
- Network connectivity for LLM calls

Tip: if `OPENAI_API_KEY` is not set, the example prints a hint.
