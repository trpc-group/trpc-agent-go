# Hallucination (LLM) Evaluation Example

This example adds a hallucination evaluator to `trpc-agent-go`.
It borrows the sentence-level grounding idea from ADK's `hallucinations_v1`, while keeping the existing LLM evaluator stack in Go.
The evaluator first segments the final answer into claims, then checks each claim against the captured grounding context, and reports a sentence-level pass rate with the `llm_hallucinations` metric.

## Scenario

The demo agent does not use a knowledge base.
Instead, it must call a local `product_catalog_lookup` tool that returns fictional catalog data.
The hallucination evaluator then verifies whether each sentence in the final answer is grounded in the captured tool call and tool output.
The judge is executed through a local `judge runner`, not a `judge model` entry in the metric file.
When `-force-hallucination` is enabled, the candidate side switches to a scripted agent that emits the same tool trace but returns intentionally wrong claims, which is useful for validating failure behavior.

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the agent model (required) | `` |
| `OPENAI_BASE_URL` | Optional custom endpoint for the agent model | `https://api.openai.com/v1` |

The example uses the same OpenAI-compatible environment and the same `-model` value for both the candidate agent and the judge runner.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model identifier used by the agent and judge runner | `gpt-5.4` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-force-hallucination` | Force the candidate side to return a grounded-but-wrong answer | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `hallucination-basic` |

## Run

```bash
cd examples/evaluation/llm/hallucination
OPENAI_API_KEY=sk-... \
go run . \
  -model "gpt-5.4" \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "hallucination-basic"
```

The agent answers a fictional product catalog question by calling the local tool, and the judge runner verifies whether each sentence is grounded in the captured tool interaction.

To validate a failing case, run:

```bash
cd examples/evaluation/llm/hallucination
OPENAI_API_KEY=sk-... \
go run . \
  -model "gpt-5.4" \
  -force-hallucination \
  -data-dir "./data" \
  -output-dir "./output" \
  -eval-set "hallucination-basic"
```

In that mode, the candidate emits a valid tool call and tool result, then replies with intentionally incorrect facts, so the hallucination metric should fail.

## Data Layout

```text
data/
└── hallucination-eval-app/
    ├── hallucination-basic.evalset.json     # EvalSet with one tool-grounded QA case
    └── hallucination-basic.metrics.json     # llm_hallucinations metric using judge runner
```

## Output

Results are written under `./output/hallucination-eval-app`, mirroring the eval set structure.
The console prints the overall status and per-case hallucination score.
