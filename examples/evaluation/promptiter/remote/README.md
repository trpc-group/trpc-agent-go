# PromptIter Remote Inference Example

This example runs PromptIter locally while executing the candidate agent through a remote `server/trpcagent` HTTP service.

PromptIter uses two remote endpoints:

- `GET /trpc-agent/v1/apps/promptiter-nba-commentary-candidate/structure` to read the optimizable structure.
- `POST /trpc-agent/v1/apps/promptiter-nba-commentary-candidate/runs` to run eval cases with profile overrides.

The target surface is:

```text
candidate#instruction
```

## Run With the Go Candidate Server

Terminal 1:

```bash
cd examples/evaluation/promptiter/remote
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . \
  -mode serve-go-candidate \
  -model "deepseek-v3.2"
```

Terminal 2:

```bash
cd examples/evaluation/promptiter/remote
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . \
  -mode run \
  -candidate-target "http://127.0.0.1:8081" \
  -judge-model "gpt-5.2" \
  -worker-model "gpt-5.2"
```

The default data directory reuses `../syncrun/data`, and results are written to `./output`.

## Run With Python Candidate Servers

The `servers/adk-python` and `servers/langgraph` directories show how non-Go agents can participate by implementing the same `server/trpcagent` HTTP service.

They are intentionally small text-only adapters for this PromptIter example: they expose `candidate#instruction`, apply profile overrides for that surface, and return a trace. The Go candidate server is the reference implementation for the full protocol behavior.

Start one Python service first, then run:

```bash
cd examples/evaluation/promptiter/remote
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
go run . -mode run -candidate-target "http://127.0.0.1:8081"
```

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-mode` | `run` or `serve-go-candidate` | `run` |
| `-addr` | Listen address for `serve-go-candidate` | `:8081` |
| `-server-base-path` | Base path exposed by the Go candidate server | `/trpc-agent/v1/apps` |
| `-candidate-target` | Remote candidate service target | `http://localhost:8081` |
| `-candidate-base-path` | Remote candidate service base path | `/trpc-agent/v1/apps` |
| `-data-dir` | Directory containing evalset and metric files | `../syncrun/data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-judge-model` | Judge model used by evaluation metrics | `gpt-5.2` |
| `-worker-model` | PromptIter backwarder, aggregator, and optimizer model | `gpt-5.2` |
| `-max-rounds` | Maximum PromptIter optimization rounds | `4` |

Other parallelism and stop-policy flags match the `syncrun` example.
