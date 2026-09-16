# Template Trace LLM Judge Evaluation Example

This example runs a local file-backed evaluation using `llm_judge_template` with execution trace variables.

The agent is a small `GraphAgent` with one `weather_llm` node and a dedicated `weather_lookup` `ToolNode`. The LLM node calls the tool, the ToolNode runs `get_weather`, and the graph loops back to the same LLM node for the final answer. The evaluation enables execution trace recording, then the judge model scores whether the final answer is grounded in the selected `ToolNode` input and output snapshots.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent and judge model. | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for the agent and judge model. | `https://api.openai.com/v1` |

The agent model is configured by the `-model` flag. The metric configuration in `data/` expands `${OPENAI_API_KEY}`, `${OPENAI_BASE_URL}` at load time and configures the judge model.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json`. | `./data` |
| `-output-dir` | Directory where evaluation results are written. | `./output` |
| `-model` | Model identifier used by the agent. | `gpt-5.2` |
| `-eval-set` | Evaluation set identifier to execute. | `template-trace-basic` |

## Run

```bash
cd examples/evaluation/llm/templatetrace
go run . \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "template-trace-basic"
```

The evaluation call enables execution trace recording with `evaluation.WithRunOptions(agent.WithExecutionTraceEnabled(true))`, so template bindings can read the current invocation trace step snapshots.

## Data Layout

```text
data/
└── template-trace-eval-app/
    ├── template-trace-basic.evalset.json
    └── template-trace-basic.metrics.json
```

The metric uses:

- `evaluatorName: "llm_judge_template"`
- `template.prompt`
- `template.variableBindings`
- `template.responseScorerName: "single_score"`
- `actual.traceStepInput` with `source.selector.nodeID: "template-trace-agent/weather_lookup"`
- `actual.traceStepOutput` with `source.selector.nodeID: "template-trace-agent/weather_lookup"`

## Output

Results are written under `./output/template-trace-eval-app`. The console prints a short summary of overall and per-case outcomes.
