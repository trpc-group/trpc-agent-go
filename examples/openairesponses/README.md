# OpenAI Responses examples

Minimal Agent + Runner examples that use the nested `model/openai/responses` module instead of Chat Completions `model/openai`.

These examples talk to the official OpenAI Responses API (`api.openai.com`). They do not contain Taiji / HY3 URLs.

Existing Chat Completions `examples/runner` is unchanged.

## Setup

```bash
export OPENAI_API_KEY="sk-..."
# optional
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_MODEL="gpt-5"

cd examples/openairesponses
go run ./chat -streaming=true
go run ./tools -streaming=true   # try: calculate 123 * 456
```

Flags: `-model`, `-api-key`, `-base-url`, `-streaming`.

The constructor is the scheme-5 diff:

```go
modelInstance := openairesponses.New(
    modelName,
    openairesponses.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
    openairesponses.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
    openairesponses.WithStore(false),
)
```
