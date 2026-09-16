# tRPC-Agent Remote Evaluation Example

This example shows how to evaluate a remote agent served through the tRPC-Agent HTTP protocol.

The Go evaluation client uses `runner/trpcagent` to call a candidate server, requests an execution trace for each run, and scores the result with a local judge runner. The metric file only describes what to evaluate; the judge runner is configured in `main.go` with `model/openai`.

The same client can evaluate one of three candidate server implementations:

- `servers/trpcagentgo`: Go `llmagent` exposed with `server/trpcagent`.
- `servers/adk`: Python ADK agent exposed with the same wire protocol.
- `servers/langgraph`: Python LangGraph agent exposed with the same wire protocol.

All implementations use a real OpenAI-compatible LLM and the same travel-advice scenario. There are no mocked tools or mocked model responses.

## What Is Evaluated

The eval set contains one user turn asking for Shanghai travel advice. The expected answer checks for four facts: sunny weather at 26C, downtown festival traffic disruption, museum ticket availability with 25 seats left, and practical booking or routing advice.

The metric file contains two LLM judge metrics:

- `llm_final_response` compares the candidate final answer with the reference answer.
- `trace_io_template` uses `llm_judge_template` and binds only `actual.traceStepInput` and `actual.traceStepOutput` from the selected trace step.

The template metric selects the root agent step with `nodeID: "trpcagent-travel-agent"`.

## Layout

```text
examples/evaluation/trpcagent/
├── main.go
├── data/
│   └── trpcagent-travel-agent/
│       ├── travel-advice-basic.evalset.json
│       └── travel-advice-basic.metrics.json
└── servers/
    ├── trpcagentgo/
    ├── adk/
    └── langgraph/
```

## Requirements

Set the standard OpenAI-compatible environment variables before running the Go server or the evaluation client:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
```

`OPENAI_BASE_URL` is optional when the provider uses the default OpenAI endpoint. The Go server and the judge runner both use `gpt-5.2` by default.

For Python servers, create a virtual environment in the selected server directory and install its `requirements.txt`.

## Start a Candidate Server

Run one server at a time. Each implementation listens on `http://127.0.0.1:8081/trpc-agent/v1/apps/trpcagent-travel-agent` by default.

### Go Reference Server

```bash
cd examples/evaluation/trpcagent/servers/trpcagentgo
go run . -model "gpt-5.2"
```

### ADK Server

```bash
cd examples/evaluation/trpcagent/servers/adk
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export OPENAI_API_BASE="${OPENAI_BASE_URL}"
export ADK_MODEL="openai/gpt-5.2"
python server.py
```

### LangGraph Server

```bash
cd examples/evaluation/trpcagent/servers/langgraph
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
export LANGGRAPH_MODEL="gpt-5.2"
python server.py
```

## Run the Evaluation Client

In another terminal:

```bash
cd examples/evaluation/trpcagent
go run . -target "http://127.0.0.1:8081"
```

The client loads eval sets and metrics from `./data`, writes results to `./output`, and injects a local judge runner into all LLM judge metrics with `evaluation.WithJudgeRunner(...)`.

## Execution Trace

The client enables `agent.WithExecutionTraceEnabled(true)` for the remote run. Evaluation results include the trace under each case run detail.

The Go server produces native tRPC-Agent traces. ADK and LangGraph servers return protocol-compatible traces with the same non-empty fields used by this example: root input, root output, step input, step output, applied surfaces, status, and token usage when the underlying framework reports it.

Trace snapshot contents can differ by implementation. For example, the Go server records model message and response JSON, while the Python adapters use simpler text snapshots.

## Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-target` | Remote tRPC-Agent service target | `http://localhost:8081` |
| `-base-path` | Remote service base path | `/trpc-agent/v1/apps` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |
| `-eval-set` | Evaluation set ID to execute | `travel-advice-basic` |
