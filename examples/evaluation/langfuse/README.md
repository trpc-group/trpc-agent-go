# Langfuse Remote Experiment Example

This example shows how to mount Langfuse remote experiment support onto the official `server/evaluation` HTTP server with a real OpenAI-compatible LLM agent and a real judge runner.

The flow is:

1. Langfuse triggers a remote dataset run webhook.
2. The official `server/evaluation/langfuse` handler fetches the dataset from Langfuse.
3. The example-specific case builder converts dataset items into `EvalCase` definitions.
4. The handler loads metrics using the Langfuse dataset ID as the eval set ID.
5. `trpc-agent-go` executes inference and evaluation locally with a real LLM agent, a real judge runner, and local tools.
6. The handler writes traces, dataset run items, case-level scores, and run-level aggregate scores back to Langfuse.

The example keeps the Langfuse protocol handling inside the official server package and only leaves dataset-item parsing plus the real demo agent setup in the example itself.

If you need a product-level research document on Langfuse evaluation capabilities, see [`LANGFUSE_EVALUATION_FRONTEND_GUIDE.md`](./LANGFUSE_EVALUATION_FRONTEND_GUIDE.md).

## Default Endpoint

The example runs on top of the evaluation server. The Langfuse webhook URL is:

```text
http://127.0.0.1:8088/evaluation/langfuse/remote-experiment
```

## Dataset Item Format

This example uses one fixed dataset schema.

`input`:

```json
{
  "question": "What is 2 + 3? Use the calculator tool."
}
```

`expectedOutput`:

```json
{
  "answer": "5"
}
```

`metadata.expectedTools` is optional, but it should be provided when the dataset is evaluated with `tool_trajectory_avg_score`.

```json
{
  "expectedTools": [
    {
      "name": "calculator",
      "arguments": {
        "operation": "add",
        "a": 2,
        "b": 3
      },
      "result": {
        "operation": "add",
        "a": 2,
        "b": 3,
        "result": 5
      }
    }
  ]
}
```

The example starts a real `llmagent` with one local tool:

- `calculator`

The bundled [`sample.metrics.json`](./data/langfuse-remote-eval-app/sample.metrics.json) uses:

- `tool_trajectory_avg_score`
- `llm_rubric_response`

The rubric response metric is evaluated through the runtime judge runner configured in `main.go`. The metric file itself does not embed model configuration.

The example uses local file managers for evaluation assets and results.

- Eval sets and metrics are stored under `data-dir`.
- Eval results are stored under `output-dir`.
- Metrics are loaded from `data-dir/langfuse-remote-eval-app/<dataset-id>.metrics.json`.

Before triggering a remote run, prepare the metric file for the target Langfuse dataset ID. This example expects one dataset to correspond to one metric set.

Use [`sample.metrics.json`](./data/langfuse-remote-eval-app/sample.metrics.json) as the starting point, then copy it to the dataset-specific path. For example:

```bash
cp ./data/langfuse-remote-eval-app/sample.metrics.json ./data/langfuse-remote-eval-app/<dataset-id>.metrics.json
```

## Remote Payload Format

Langfuse sends dataset metadata together with an optional payload. This example accepts either:

- a plain string treated as `runName`
- a JSON object such as:

```json
{
  "runName": "remote-demo-20260410",
  "runDescription": "Remote experiment example with a real agent and judge runner.",
  "userId": "payload-user",
  "traceTags": ["payload-tag-a", "payload-tag-b"]
}
```

If `userId` or `traceTags` are missing from the payload, the handler falls back to its configured defaults.

## Required Environment Variables

| Variable | Description |
| --- | --- |
| `OPENAI_API_KEY` | API key used by the example agent model and the judge runner |
| `LANGFUSE_HOST` | Host:port used by Langfuse telemetry export |
| `LANGFUSE_PUBLIC_KEY` | Langfuse public API key used by telemetry and the remote experiment handler |
| `LANGFUSE_SECRET_KEY` | Langfuse secret API key used by telemetry and the remote experiment handler |

## Optional Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `LANGFUSE_INSECURE` | Use plain HTTP when Langfuse is served without TLS | `false` |
| `OPENAI_BASE_URL` | Optional custom endpoint used by the example agent model and the judge runner | `https://api.openai.com/v1` |

## Optional Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-addr` | Address to bind the evaluation server to | `:8088` |
| `-model` | Model identifier used by the example agent | `gpt-4.1-mini` |
| `-streaming` | Enable streaming responses from the example agent | `false` |
| `-data-dir` | Directory containing local eval set and metric files | `./data` |
| `-output-dir` | Directory where local eval results will be stored | `./output` |

## Run The Example Server

```bash
cd trpc-agent-go/examples/evaluation/langfuse
LANGFUSE_HOST=127.0.0.1:3000 \
LANGFUSE_INSECURE=true \
LANGFUSE_PUBLIC_KEY=pk-lf-... \
LANGFUSE_SECRET_KEY=sk-lf-... \
OPENAI_API_KEY=sk-... \
go run .
```

## Configure Langfuse UI

1. Open the target dataset in Langfuse.
2. Open the remote dataset run trigger configuration.
3. Set the webhook URL to the example endpoint.
4. Paste the example payload shown above as default config.
5. Trigger a remote dataset run from the UI.

If Langfuse runs inside Docker and this example runs on the host machine, the URL usually needs to use an address reachable from the Langfuse web container.

## Trigger The Webhook Without UI

```bash
curl -X POST http://127.0.0.1:8088/evaluation/langfuse/remote-experiment \
  -H 'Content-Type: application/json' \
  -d '{
    "projectId": "your-project-id",
    "datasetId": "your-dataset-id",
    "datasetName": "remote-eval-demo",
    "payload": {
      "runName": "remote-demo-20260410",
      "runDescription": "Remote experiment example with a real agent and judge runner.",
      "userId": "payload-user",
      "traceTags": ["payload-tag-a", "payload-tag-b"]
    }
  }'
```

## What You Will See In Langfuse

- A remote dataset run created under the selected dataset.
- One trace per dataset item.
- Case-level scores on each trace.
- Run-level aggregate scores on the dataset run.
- Tool observations inside the trace after Langfuse OTEL ingestion finishes.
- Score comments populated from evaluator reasons and aggregate pass-rate summaries.

## Local Files

After a run finishes, the example will also persist evaluation assets locally.

- Eval set and metric files are written under `data-dir`.
- Eval result files are written under `output-dir`.
