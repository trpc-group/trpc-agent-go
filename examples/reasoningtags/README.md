# Reasoning Tags (Final Only)

This example shows how to filter and display only the model's final reasoning using the framework’s built‑in tags during streaming.

Highlights

- Streams the assistant response in real time
- Prints reasoning only when the tag is `reasoning.final`
- Continues to display normal visible text as usual

Run

```
go run .
```

Notes

- Reasoning is a provider feature. Enable it with `GenerationConfig.ThinkingEnabled` when available.
- Tags are attached automatically by the framework for streaming events.
- See `main.go` for a minimal filter implementation.

