# UserSimulation Evaluation Example

This example demonstrates dynamic user simulation with `conversationScenario` and the default `usersimulation.New(...)` implementation. The EvalSet does not provide a static multi-turn `conversation`; instead, the simulator generates the next user utterance during inference based on the scenario plan and the candidate agent’s latest reply.

The sample uses the `llm_rubric_response` metric with `evaluation.WithJudgeRunner(...)`. The metric file contains only rubric definitions; the judge responses are produced at runtime by a dedicated judge runner rather than a `judgeModel` entry in JSON.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the candidate, simulator, and judge agents (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for the candidate, simulator, and judge agents | `https://api.openai.com/v1` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the candidate, simulator, and judge agents | `gpt-5.4` |
| `-streaming` | Enable streaming responses from the candidate agent | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `business-trip-scenario` |

## Run

```bash
cd examples/evaluation/usersimulation
OPENAI_API_KEY=sk-... \
go run . \
  -model "gpt-5.4" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "business-trip-scenario"
```

The example creates one candidate runner, one simulator runner, and one judge runner. The simulator is wrapped by `usersimulation.New(...)` without extra overrides, so `stopSignal` and `maxAllowedInvocations` come directly from the EvalSet file. The simulator is injected through `evaluation.WithUserSimulator(...)`, and the rubric judge is injected through `evaluation.WithJudgeRunner(...)`.

## Data Layout

```text
data/
└── usersimulation-app/
    ├── business-trip-scenario.evalset.json    # EvalSet with one conversationScenario case
    └── business-trip-scenario.metrics.json    # llm_rubric_response metric with rubric definitions only
```

## Output

Results are written under `./output/usersimulation-app`. The console prints a short summary of overall status and per-case scores.
