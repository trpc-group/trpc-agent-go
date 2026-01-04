# Structured Output (JSON Schema) Example

This example demonstrates a minimal, real-world usage of structured output with `LLMAgent`.

- Model-enforced JSON schema for responses (auto-generated from a Go type).
- Interactive runner-style CLI, same feel as `examples/runner`.

## Quick start

```bash
cd trpc-agent-go/examples/structuredoutput
go build -o structuredoutput main.go
./structuredoutput -model deepseek-chat
```

Then try:

```
Find me a great cafe in Beijing that is quiet for working. Return the result in the structured format.
```

Type `exit` to quit.

## Core API shown

- `llmagent.WithStructuredOutputJSON(examplePtr any, strict bool, description string)`
  - Pass a typed pointer like `new(PlaceRecommendation)`; the framework infers the type,
    auto-generates a JSON schema, configures the model with native structured output
    (when supported), and automatically unmarshals the final JSON into that type.

## Notes

- Tools remain allowed with structured output.
- The final typed value is exposed on the emitted event as an in-memory payload for
  immediate consumption, and the raw JSON is still persisted under `output_key` (if set).

## Structured Output Methods Comparison

The framework provides two complementary structured output options:

### WithStructuredOutputJSON (This Example - Type-Safe)

**Best for:** Compile-time type safety, clean Go code integration

```go
type PlaceRecommendation struct {
    Name    string  `json:"name"`
    Address string  `json:"address"`
    Rating  float64 `json:"rating"`
}

agent := llmagent.New(
    "agent",
    llmagent.WithStructuredOutputJSON(
        new(PlaceRecommendation),  // Auto-generates schema
        true,                       // Strict mode
        "A place recommendation",   // Description
    ),
)

// Access typed output
for event := range eventCh {
    if event.StructuredOutput != nil {
        place := event.StructuredOutput.(*PlaceRecommendation)  // Type assertion
        fmt.Printf("Place: %s, Rating: %.1f\n", place.Name, place.Rating)
    }
}
```

**Advantages:**
- ✅ Type-safe at compile time
- ✅ Auto-generates JSON schema from struct
- ✅ Clean unmarshaling to Go types
- ✅ Works with tools

### WithStructuredOutputJSONSchema (Flexible Schema)

**Best for:** Dynamic schemas, working with external schema definitions, gradual typing

```go
schema := map[string]any{
    "type": "object",
    "properties": map[string]any{
        "name": map[string]any{
            "type": "string",
            "description": "Place name",
        },
        "rating": map[string]any{
            "type": "number",
            "minimum": 0,
            "maximum": 5,
        },
    },
    "required": []string{"name", "rating"},
}

agent := llmagent.New(
    "agent",
    llmagent.WithStructuredOutputJSONSchema(
        "place_output",      // Name
        schema,              // JSON schema
        true,                // Strict mode
        "Place information", // Description
    ),
    llmagent.WithTools([]tool.Tool{searchTool, calculatorTool}), // Tools are allowed
)

// Access untyped output
for event := range eventCh {
    if event.StructuredOutput != nil {
        data := event.StructuredOutput.(map[string]any)  // Untyped map
        name := data["name"].(string)
        rating := data["rating"].(float64)
        fmt.Printf("Place: %s, Rating: %.1f\n", name, rating)
    }
}
```

**Advantages:**
- ✅ Flexible schema definition (no struct required)
- ✅ Works with external JSON schemas
- ✅ Easier for prototyping/dynamic use cases
- ✅ Works with tools

### When to Use Each

| Scenario | Recommended Method |
|----------|-------------------|
| You have well-defined Go structs | `WithStructuredOutputJSON` |
| You need compile-time type safety | `WithStructuredOutputJSON` |
| Working with external API schemas | `WithStructuredOutputJSONSchema` |
| Rapid prototyping / dynamic schemas | `WithStructuredOutputJSONSchema` |
| Schema from configuration/database | `WithStructuredOutputJSONSchema` |
| Need complex JSON schema features | `WithStructuredOutputJSONSchema` |

Both methods:
- ✅ Allow tool usage
- ✅ Support streaming
- ✅ Provide structured output in events
- ✅ Work with all model providers
