# Model Example

This example demonstrates how to use the OpenAI-like model implementation with environment variable configuration.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required) | `` |
| `MODEL_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |
| `MODEL_NAME` | Name of the model to use | `gpt-4o-mini` |

## Running the Example

### Using default values:

```bash
cd examples/model
go run main.go
```

### Using custom environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export MODEL_BASE_URL="https://api.openai.com/v1"
export MODEL_NAME="gpt-4o-mini"

cd examples/model
go run main.go
```

### Using environment variables inline:

```bash
cd examples/model
OPENAI_API_KEY="your-api-key" MODEL_BASE_URL="https://api.openai.com/v1" MODEL_NAME="gpt-4o-mini" go run main.go
```

## Example Output

The example will run four different demonstrations:

1. **Non-streaming Example**: Shows basic model usage without streaming
2. **Streaming Example**: Shows streaming response handling
3. **Advanced Example**: Shows usage with custom parameters
4. **Parameter Testing Example**: Tests different parameter combinations

Each example will display the model responses along with metadata like token usage and finish reasons.

## Package Usage

The example demonstrates the new package structure:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/core/model"
    "trpc.group/trpc-go/trpc-agent-go/core/model/openailike"
)

// Create a new model instance
llm := openailike.New(modelName, openailike.Options{
    APIKey:  apiKey,
    BaseURL: baseURL,
})

// Use the model
request := &model.Request{
    Model: modelName,
    Messages: []model.Message{
        model.NewSystemMessage("You are a helpful assistant."),
        model.NewUserMessage("Hello!"),
    },
    Stream: false,
}

responseChan, err := llm.GenerateContent(ctx, request)
```

## Error Handling

The example includes comprehensive error handling for:

- Missing API keys
- API errors
- Network timeouts
- Invalid responses

## Security Notes

- Never commit API keys to version control
- Use environment variables or secure configuration management
- The default API key in the example is for demonstration only 