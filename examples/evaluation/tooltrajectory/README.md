# Tool Trajectory Evaluation Example

This example demonstrates `tool_trajectory_avg_score` with multiple tools (weather, news, time, ticket) and order-insensitive matching.

## Data Layout

```
data/
└── tooltrajectory-app/
    ├── tooltrajectory-basic.evalset.json    # Use case: travel to Shanghai, check weather/news/time/tickets
    └── tooltrajectory-basic.metrics.json    # orderInsensitive with strict default matching
```

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent model (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for OpenAI-compatible APIs | `https://api.openai.com/v1` |

## Run

```bash
cd examples/evaluation/tooltrajectory
OPENAI_API_KEY=sk-... go run . \
  -data-dir "./data" \
  -output-dir "./output" \
  -model "gpt-4o-mini" \
  -eval-set "tooltrajectory-basic"
```

## What It Shows

