# Debug + Evaluation Server Example

This sample pairs the trpc-agent-go debug server with the evaluation pipeline.
It lets you collect real conversations through ADK Web, convert them into eval
sets, persist metric definitions, and run scoring jobs directly from the UI.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OPENAI_API_KEY` | API key for the OpenAI-compatible backend (required) | `` |
| `OPENAI_BASE_URL` | Base URL for the LLM endpoint | `https://api.openai.com/v1` |

## Command Line Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-model` | Model identifier used by the demo agent | `deepseek-chat` |
| `-addr` | HTTP listen address | `:8080` |
| `-app` | Application name exposed to ADK Web | `assistant` |
| `-data-dir` | Directory containing eval set JSON + metric configs | `./data` |
| `-output-dir` | Directory where eval results are persisted | `./output` |

## Run the Server

```bash
cd examples/evaluation/debug
OPENAI_API_KEY=sk-your-key \
go run . \
  -model deepseek-chat \
  -addr 127.0.0.1:8080 \
  -app assistant \
  -data-dir ./data \
  -output-dir ./output
```

The process keeps running until interrupted. Sessions themselves remain
in-memory, but eval sets, metric definitions, and eval results live under the
directories specified by `-data-dir` and `-output-dir`.

## Use with ADK Web

1. Clone [ADK Web](https://github.com/google/adk-web), install dependencies, and
   start it pointing to `http://127.0.0.1:8080`.
2. Create a chat session in the UI under the `-app` name and talk to the agent.
   Events are streamed and stored verbatim.
3. Convert a completed session into an eval case via the UI (or the REST API).
   The server stores the resulting `*.evalset.json` and `*.metrics.json` inside
   `-data-dir`.
4. Run evaluations from the Eval tab or call `/apps/{app}/eval-sets/{id}/run`.
   Metrics come from the stored configs; overriding them in the request also
   persists the new values.
5. Download the produced eval results either from the UI or under
   `-output-dir/{app}`.

## Data Layout

```
data/
└── assistant/
    ├── math-demo.evalset.json
    └── math-demo.metrics.json

output/
└── assistant/
    └── assistant_math-demo_<uuid>.evalset_result.json
```
