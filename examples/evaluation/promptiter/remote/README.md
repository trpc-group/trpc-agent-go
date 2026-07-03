# PromptIter Remote Inference Example

This example runs PromptIter asynchronously while executing the candidate agent through a remote `server/trpcagent` HTTP service.

PromptIter uses two remote endpoints:

- `GET /trpc-agent/v1/apps/promptiter-sports-recap-agent/structure` to read the optimizable structure.
- `POST /trpc-agent/v1/apps/promptiter-sports-recap-agent/runs` to run eval cases with profile overrides.

The target surfaces are the five instruction surfaces of the multinode sports recap candidate.

## Run

The `servers/trpc-agent-go` directory provides the Go reference server. The `servers/adk-python` and `servers/langgraph` directories show how non-Go agents can participate by implementing the same `server/trpcagent` HTTP service.

The Go server reuses the multinode sports recap candidate. The Python services expose the same stage instruction surfaces and run the same headline/highlights/stats-angle, writer, and editor flow through their own frameworks.

Start one candidate service first, then run:

```bash
cd examples/evaluation/promptiter/remote
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . -candidate-target "http://127.0.0.1:8081"
```

The default data directory is `./data`, and results are written to `./output`.

## Example Results

The following runs use the sports recap data, the `deepseek-v3.2` candidate model, and the default PromptIter settings. The Go remote driver owns the iteration settings, so all candidate services use the same max-rounds, parallelism, and stop policy. Scores are model-dependent and should be treated as illustrative.

### tRPC-Agent-Go

| Step | Train score | Validation score | Accepted | Delta | Stop reason |
| --- | --- | --- | --- | --- | --- |
| Baseline | - | `0.55` | - | - | - |
| Round 1 | `0.53` | `0.72` | yes | `+0.17` | continue optimization |
| Round 2 | `0.73` | `0.86` | yes | `+0.14` | continue optimization |
| Round 3 | `0.81` | `0.88` | yes | `+0.02` | continue optimization |
| Round 4 | `0.89` | `0.84` | no | `-0.03` | max rounds reached |

The final accepted validation score was `0.88`, up from the baseline score of `0.55`. The accepted profile contained instruction overrides for all five target stage surfaces.

### ADK Python

| Step | Train score | Validation score | Accepted | Delta | Stop reason |
| --- | --- | --- | --- | --- | --- |
| Baseline | - | `0.61` | - | - | - |
| Round 1 | `0.69` | `0.70` | yes | `+0.09` | continue optimization |
| Round 2 | `0.67` | `0.91` | yes | `+0.20` | continue optimization |
| Round 3 | `0.69` | `0.83` | no | `-0.08` | continue optimization |
| Round 4 | `0.80` | `0.86` | no | `-0.05` | max rounds reached |

The final accepted validation score was `0.91`, up from the baseline score of `0.61`.

### LangGraph

| Step | Train score | Validation score | Accepted | Delta | Stop reason |
| --- | --- | --- | --- | --- | --- |
| Baseline | - | `0.48` | - | - | - |
| Round 1 | `0.70` | `0.64` | yes | `+0.16` | continue optimization |
| Round 2 | `0.64` | `0.69` | yes | `+0.05` | continue optimization |
| Round 3 | `0.66` | `0.81` | yes | `+0.12` | continue optimization |
| Round 4 | `0.77` | `0.84` | yes | `+0.03` | max rounds reached |

The final accepted validation score was `0.84`, up from the baseline score of `0.48`.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-candidate-target` | Remote candidate service target | `http://localhost:8081` |
| `-candidate-base-path` | Remote candidate service base path | `/trpc-agent/v1/apps` |
| `-data-dir` | Directory containing evalset and metric files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-judge-model` | Judge model used by evaluation metrics | `gpt-5.2` |
| `-worker-model` | PromptIter backwarder, aggregator, and optimizer model | `gpt-5.2` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `4` |
| `-poll-interval` | Polling interval used to wait for asynchronous run completion | `1s` |

Other parallelism and stop-policy flags use the same names as the `syncrun` example.
