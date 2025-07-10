# Model Example

This example demonstrates how to use the OpenAI-like model implementation with environment variable configuration.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Environment Variables

The example supports the following environment variables:

| Variable | Description | Default Value |
|----------|-------------|---------------|
| `OPENAI_API_KEY` | API key for the model service (required, automatically read by OpenAI SDK) | `` |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK) | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code. The SDK handles this automatically when creating the client.

## Command Line Arguments

| Argument | Description | Default Value |
|----------|-------------|---------------|
| `-model` | Name of the model to use | `gpt-4o-mini` |

## Running the Example

### Using default values:

```bash
cd examples/model
go run main.go
```

### Using custom model:

```bash
cd examples/model
go run main.go -model gpt-4
```

### Using custom environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"

cd examples/model
go run main.go -model gpt-4o-mini
```

### Using environment variables inline:

```bash
cd examples/model
OPENAI_API_KEY="your-api-key" OPENAI_BASE_URL="https://api.openai.com/v1" go run main.go -model gpt-4o-mini
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
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create a new model instance
// The OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment variables.
llm := openai.New(modelName, openai.Options{
    ChannelBufferSize: 512, // Optional: configure buffer size
})

// Use the model
request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("You are a helpful assistant."),
        model.NewUserMessage("Hello!"),
    },
    GenerationConfig: model.GenerationConfig{
        Stream: false,
    },
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