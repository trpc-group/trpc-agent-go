# trpc-agent-go DSL System

A clean, powerful DSL (Domain-Specific Language) system for building AI graphs with trpc-agent-go.

## 🎯 Design Philosophy

### Core Principles

1. **Frontend-Driven**: JSON DSL designed for visual drag-and-drop editors
2. **Zero Schema Definition**: Automatic State Schema inference from components
3. **Component-Based**: Reusable components registered in a central registry
4. **Type-Safe**: Strong typing with Go's type system
5. **Extensible**: Support for built-in, custom, and code executor components

### Key Features

- ✅ **Automatic Schema Inference** - No need to manually define State Schema
- ✅ **Component Registry** - Central place for all components (built-in + custom)
- ✅ **Multi-Level Validation** - Structure, semantics, components, topology
- ✅ **Clean Compilation** - DSL → StateGraph → Executable
- ✅ **Code Executor Support** - Dynamic code execution (planned)

## 📐 Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Frontend (Future)                     │
│              Drag-and-Drop Visual Editor                │
└────────────────────┬────────────────────────────────────┘
                     │ JSON DSL
                     ▼
┌─────────────────────────────────────────────────────────┐
│                   DSL Processing Layer                   │
│  ┌──────────┐  ┌───────────┐  ┌──────────────────────┐ │
│  │  Parser  │→ │ Validator │→ │ Schema Inference     │ │
│  └──────────┘  └───────────┘  └──────────────────────┘ │
│                                          ↓               │
│                                  ┌──────────────┐       │
│                                  │   Compiler   │       │
│                                  └──────────────┘       │
└────────────────────┬────────────────────────────────────┘
                     │ StateGraph
                     ▼
┌─────────────────────────────────────────────────────────┐
│              trpc-agent-go Graph Engine                  │
│                  (Execution Layer)                       │
└─────────────────────────────────────────────────────────┘
                     ▲
                     │
┌────────────────────┴────────────────────────────────────┐
│              Component Registry                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │   Built-in   │  │    Custom    │  │     Code     │  │
│  │  Components  │  │  Components  │  │   Executor   │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────┘
```

## 🚀 Quick Start

### 1. Define a Graph in JSON

```json
{
  "version": "1.0",
  "name": "simple_llm_graph",
  "nodes": [
    {
      "id": "llm",
      "component": {
        "type": "builtin",
        "ref": "builtin.llm"
      },
      "config": {
        "instruction": "You are a helpful assistant",
        "temperature": 0.7
      }
    }
  ],
  "edges": [],
  "start_node_id": "llm"
}
```

### 2. Load and Execute

```go
package main

import (
    "trpc.group/trpc-go/trpc-agent-go/dsl"
    "trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
    "trpc.group/trpc-go/trpc-agent-go/dsl/validator"
    _ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
)

func main() {
    // Parse DSL
    parser := dsl.NewParser()
    graphDef, _ := parser.ParseFile("graph.json")

    // Validate
    v := validator.New()
    v.Validate(graphDef)

    // Compile to StateGraph
    comp := compiler.New()
    compiledGraph, _ := comp.Compile(graphDef)

    // Execute
    executor, _ := graph.NewExecutor(compiledGraph)
    eventChan, _ := executor.Execute(ctx, initialState, nil)

    // Process events
    for evt := range eventChan {
        // Handle events
    }
}
```

## 📦 Package Structure

```
dsl/
├── types.go              # DSL type definitions (Graph, Node, Edge)
├── parser.go             # JSON DSL parser
├── validator.go          # Multi-level DSL validator
├── compiler.go           # DSL → StateGraph compiler
├── schema_inference.go   # Automatic State Schema inference
│
└── registry/             # Component registry
    ├── component.go      # Component interface and metadata
    ├── registry.go       # Component registration and lookup
    └── builtin/          # Built-in components
        ├── llm.go        # LLM component
        └── passthrough.go # Passthrough component
```

## 🧩 Component System

### Component Interface

```go
type Component interface {
    Metadata() ComponentMetadata
    Execute(ctx context.Context, config ComponentConfig, state graph.State) (graph.State, error)
}
```

### Component Metadata

Components declare their inputs, outputs, and config schema:

```go
ComponentMetadata{
    Name: "builtin.llm",
    Inputs: []ParameterSchema{
        {Name: "messages", Type: "[]model.Message", Required: true},
    },
    Outputs: []ParameterSchema{
        {Name: "messages", Type: "[]model.Message"},
    },
    ConfigSchema: []ParameterSchema{
        {Name: "temperature", Type: "float64", Default: 0.7},
    },
}
```

### Creating Custom Components

```go
type MyComponent struct{}

func (c *MyComponent) Metadata() registry.ComponentMetadata {
    return registry.ComponentMetadata{
        Name: "custom.my_component",
        // ... metadata
    }
}

func (c *MyComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (graph.State, error) {
    // Your logic here
    return graph.State{"result": "processed"}, nil
}

// Register at init time
func init() {
    registry.MustRegister(&MyComponent{})
}
```

## 🔄 State Schema Inference

**Key Innovation**: Users don't need to define State Schema manually!

The system automatically infers the schema from components:

1. Collect all input/output parameters from all components
2. Merge parameters with the same name (check type compatibility)
3. Determine appropriate reducers
4. Generate final StateSchema

Example:
- Component A outputs `messages: []model.Message` with reducer `message`
- Component B inputs `messages: []model.Message`
- **Inferred Schema**: `messages: []model.Message` with `MessageReducer`

## ✅ Validation Levels

1. **Structure Validation**
   - Required fields present
   - No duplicate node IDs
   - Valid component references

2. **Component Validation**
   - Components exist in registry
   - Config matches component schema
   - Required config parameters present

3. **Topology Validation**
   - All nodes reachable from entry point
   - No dangling edges
   - Valid conditional routes

## 🎨 DSL Format

See [examples/dsl/basic/workflow.json](../examples/dsl/basic/workflow.json) for a complete example.

## 🔮 Future Enhancements

- [ ] Expression language for conditional edges
- [ ] Code executor integration (Python/JavaScript)
- [ ] HTTP request component
- [ ] MCP (Model Context Protocol) component
- [ ] Subgraph support
- [ ] Loop support
- [ ] Frontend API server
- [ ] Visual editor integration

## 📚 Examples

- [Basic Example](../examples/dsl/basic/) - Simple LLM graph
- More examples coming soon!

## 🤝 Contributing

To add a new built-in component:

1. Create a new file in `registry/builtin/`
2. Implement the `Component` interface
3. Register in `init()` function
4. Add tests and documentation

## 📄 License

Same as trpc-agent-go main project.
