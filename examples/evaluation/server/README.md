# Online Evaluation Server Example

This example exposes the existing evaluation workflow as an HTTP API service so web pages or other systems can trigger evaluation runs remotely instead of executing them only from the CLI.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Configuration Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-addr` | Listen address for the evaluation server | `:8080` |
| `-base-path` | Base path exposed by the evaluation server | `/evaluation` |
| `-model` | Model identifier used by the calculator agent | `deepseek-v4-flash` |
| `-streaming` | Enable streaming responses from the agent | `false` |
| `-data-dir` | Directory containing `.evalset.json` and `.metrics.json` files | `./data` |
| `-output-dir` | Directory where evaluation results are written | `./output` |

## Data Files

`-data-dir` must contain an app-specific subdirectory whose name matches the example app name `math-eval-app`. The server example now includes ready-to-use sample files:

- `data/math-eval-app/math-basic.evalset.json`
- `data/math-eval-app/math-basic.metrics.json`

The expected layout is:

```text
examples/evaluation/server/data/
`-- math-eval-app/
    |-- math-basic.evalset.json
    `-- math-basic.metrics.json
```

## Run

```bash
cd trpc-agent-go/examples/evaluation/server
go run . \
  -addr ":8080" \
  -base-path "/evaluation" \
  -model "deepseek-v4-flash" \
  -data-dir "./data" \
  -output-dir "./output"
```

The server exposes the following endpoints:

- `GET /evaluation/sets`
- `GET /evaluation/sets/{setId}`
- `POST /evaluation/runs`
- `GET /evaluation/results`
- `GET /evaluation/results/{resultId}`

## Example Requests

List available evaluation sets:

```bash
curl "http://127.0.0.1:8080/evaluation/sets"
```

Run an evaluation set online:

```bash
curl -X POST "http://127.0.0.1:8080/evaluation/runs" \
  -H "Content-Type: application/json" \
  -d '{"setId":"math-basic","numRuns":1}'
```

The response body contains the `evaluationResult` returned by the agent evaluator.

Fetch result details:

```bash
curl "http://127.0.0.1:8080/evaluation/results"
curl "http://127.0.0.1:8080/evaluation/results/<resultId>"
```

This pattern is intended for UI integration where users pick an eval set, enter a server address such as `http://10.12.156.151:8080/evaluation/runs`, and trigger the run from a web page.
