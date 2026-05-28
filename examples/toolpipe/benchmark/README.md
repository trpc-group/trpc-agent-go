# ToolPipe A/B Benchmark

Compares agent performance with and without toolpipe on the same task.

## Run

```bash
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_API_KEY="your-key"

# Run all 5 scenarios
go run . -model="gpt-4o" -mode=both

# Run a single scenario
go run . -model="gpt-4o" -mode=both -task=json-field-extract
```

## Available tasks

| Task | Description |
|------|-------------|
| `needle-in-haystack` | Find 2 specific facts in a large doc |
| `extract-structure` | Extract all headings from a large page |
| `targeted-search` | Find specific topics on HN front page |
| `json-field-extract` | Extract fields from JSON API response |
| `large-page-specific-section` | Extract one section from a large doc |

## Sample results (GPT-5)

Token consumption may increase or decrease depending on the scenario and model strategy. **Token count is not the core metric** — what matters is the signal-to-noise ratio in each model turn and the precision of the final answer.

| Task | Token Δ | Peak Context Δ | Notes |
|------|---------|----------------|-------|
| json-field-extract | -88% | -96% | Best case: structured JSON extraction |
| extract-structure | -99% | -99% | Best case: jq+grep one-shot |
| large-page-specific-section | -65% | -93% | Good: targeted section grep |
| needle-in-haystack | -34% | -86% | Good: specific fact lookup |
| targeted-search ❌ | +235% | +86% | Not suitable: small data + vague target |

Key observation: in the "needle-in-haystack" scenario, the toolpipe answer was **more complete and accurate** — because a focused context improves model attention quality.

## Metrics reported

- Input/Output/Total tokens
- Peak and total context size (bytes)
- Model rounds and tool call count
- Duration
- Answer preview
