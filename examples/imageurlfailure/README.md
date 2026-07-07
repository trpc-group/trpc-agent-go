# Image URL Failure Continuation Demo

This example sends a URL-backed image in the first turn, then continues in the
same session. If the model service cannot fetch or decode the image URL, the
framework records that historical image part location in session state. Later
turns replace only that failed image part with a text placeholder before
sending the model request, so the session is not repeatedly blocked by the same
historical image input.

The framework does not download, validate, proxy, or store the image. It only
records model-side failures and changes later model-facing request views. It
does not retry the failed first turn.

## Run

```bash
cd examples/imageurlfailure
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="your-api-key"
export MODEL_NAME="your-model"
go run .
```

By default the first turn attaches `https://example.invalid/unavailable.png`.
You can use another URL or pass provider settings as flags:

```bash
go run . \
  -base-url "https://your-openai-compatible-endpoint/v1" \
  -api-key "your-api-key" \
  -model "your-model" \
  -image-url "not-a-url"
```

The example enables `llmagent.WithImageURLFailureContinuation(true)` explicitly.
The feature is disabled by default for normal LLM agents.
