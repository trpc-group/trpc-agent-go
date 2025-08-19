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
