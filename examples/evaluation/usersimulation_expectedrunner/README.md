# UserSimulation With ExpectedRunner Evaluation Example

This example demonstrates how to use `conversationScenario`, `usersimulation.New(...)`, `evaluation.WithExpectedRunner(...)`, and `evaluation.WithJudgeRunner(...)` together. The user turns are generated dynamically from a simulated conversation plan, the expected runner drives the conversation transcript with `driver=expected`, and the judge runner uses `llm_rubric_critic` to compare the candidate reply against the expected reply with rubric-based checks.

The example uses real LLM agents for the candidate runner, expected runner, simulator runner, and judge runner. It requires model access through the standard OpenAI-compatible environment variables. In practice, the expected runner often benefits from a stronger model or a higher reasoning effort than the candidate runner, so the example exposes separate flags for those settings.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the candidate, expected, simulator, and judge agents (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for the candidate, expected, simulator, and judge agents | `https://api.openai.com/v1` |

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the candidate agent | `gpt-5.4` |
| `-expected-model` | Model identifier used by the expected runner. Defaults to `-model` | `` |
| `-simulator-model` | Model identifier used by the simulator runner. Defaults to `-model` | `` |
| `-judge-model` | Model identifier used by the judge runner. Defaults to `-expected-model` when set, otherwise `-model` | `` |
| `-expected-reasoning-effort` | Reasoning effort used by the expected runner. Pass an empty string to disable the override | `medium` |
| `-judge-reasoning-effort` | Reasoning effort used by the judge runner. Pass an empty string to disable the override | `medium` |
| `-streaming` | Enable streaming responses from the candidate agent | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `business-trip-expected-runner` |

## Run

```bash
cd examples/evaluation/usersimulation_expectedrunner
OPENAI_API_KEY=sk-... \
go run . \
  -model "gpt-5.4" \
  -expected-model "gpt-5.4" \
  -judge-model "gpt-5.4" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "business-trip-expected-runner"
```

The example creates one candidate runner, one expected runner, one simulator runner, and one judge runner. The EvalSet enables `expectedRunnerEnabled` and sets `conversationScenario.driver` to `expected`, so the framework first lets the expected runner drive the scenario transcript, then replays the same user-input sequence against the candidate runner. The generated `ExpectedInferences` are persisted into the EvalResult file as `expectedInvocation.finalResponse`. The simulator is wrapped by `usersimulation.New(...)` without extra overrides, so `stopSignal` and `maxAllowedInvocations` come directly from the EvalSet file.

## Data Layout

```text
data/
└── usersimulation_expectedrunner_app/
    ├── business-trip-expected-runner.evalset.json    # EvalSet with conversationScenario, driver=expected, and expectedRunnerEnabled
    └── business-trip-expected-runner.metrics.json    # llm_rubric_critic metric judged by the judge runner
```

## Output

Results are written under `./output/usersimulation_expectedrunner_app`. The EvalResult file includes both `actualInvocation` and `expectedInvocation`, and each expected invocation should contain a non-empty `finalResponse`.
