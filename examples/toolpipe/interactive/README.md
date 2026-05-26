# ToolPipe Interactive Demo

Interactive chat demonstrating the toolpipe extension with `web_fetch` and `duckduckgo_search`.

## Run

```bash
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_API_KEY="your-key"

go run . -model="gpt-4o"
```

## What it does

The agent has two tools augmented with `result_filter`. You can ask questions and observe how the model uses shell-like pipelines to extract specific content from large tool outputs.

Try:
```text
Fetch https://go.dev/doc/effective_go and show me only the section headings
Search for "Rust programming language" and just show titles
What's on Hacker News about AI?
```
