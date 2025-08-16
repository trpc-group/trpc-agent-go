# Model Retry Example

This example demonstrates how to use the model retry mechanism in trpc-agent-go framework.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Overview

The example shows how to configure retry behavior for OpenAI models using the `WithOpenAIOptions` function, which passes retry configuration to the underlying OpenAI client.

## Key Features

1. **Command Line Configuration**: Use flags to configure retry parameters
2. **Environment Variable Support**: Automatically reads `OPENAI_API_KEY` and `OPENAI_BASE_URL`
3. **Flexible Retry Settings**: Configurable max retries and timeout
4. **Multiple Examples**: Basic, advanced, streaming, and rate limiting scenarios

## Environment Variables

The example supports the following environment variables:

| Variable          | Description                                                                | Default Value               |
| ----------------- | -------------------------------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required, automatically read by OpenAI SDK) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK)     | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code. The SDK handles this automatically when creating the client.

## Command Line Arguments

| Argument   | Description               | Default Value |
| ---------- | ------------------------- | ------------- |
| `-model`   | Name of the model to use  | `gpt-4o-mini` |
| `-retries` | Maximum number of retries | `3`           |
| `-timeout` | Request timeout duration  | `30s`         |

## How It Works

The retry mechanism works by:

1. **Configuration**: Using `WithOpenAIOptions` to pass retry options
2. **Pass-through**: Framework passes options to underlying OpenAI client
3. **Automatic Retry**: OpenAI client handles retries based on HTTP status codes
4. **Smart Backoff**: Follows API's `Retry-After` headers or uses exponential backoff

## Retryable Errors

The OpenAI client automatically retries on:

- `408 Request Timeout`
- `409 Conflict`
- `429 Too Many Requests` (Rate Limiting)
- `5xx Server Errors`
- Network connection errors

## Running the Example

### Using default values:

```bash
cd examples/model/retry
go run main.go
```

### Using custom configuration:

```bash
cd examples/model/retry
go run main.go -model gpt-4 -retries 5 -timeout 60s
```

### Using custom environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"

cd examples/model/retry
go run main.go -model gpt-4o-mini -retries 3 -timeout 30s
```

### Using environment variables inline:

```bash
cd examples/model/retry
OPENAI_API_KEY="your-api-key" OPENAI_BASE_URL="https://api.openai.com/v1" go run main.go -model gpt-4o-mini
```

## Example Output

The example will run four different demonstrations:

1. **🔄 Basic Retry Example**: Shows basic model usage with retry configuration
2. **⚡ Advanced Retry Example**: Shows usage with custom parameters and retry settings
3. **🌊 Streaming with Retry Example**: Shows streaming response handling with retry
4. **🚦 Rate Limiting Retry Example**: Tests retry mechanism for rate limiting scenarios

Each example will display the model responses along with metadata like token usage and finish reasons.

## Package Usage

The example demonstrates the retry configuration:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    openaiopt "github.com/openai/openai-go/option"
)

// Create a new model instance with retry configuration
// The OpenAI SDK will automatically read OPENAI_API_KEY and OPENAI_BASE_URL from environment variables.
llm := openai.New("gpt-4",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(3),
        openaiopt.WithRequestTimeout(30*time.Second),
    ),
)

// Use the model
request := &model.Request{
    Messages: []model.Message{
        model.NewUserMessage("Hello, how are you?"),
    },
    GenerationConfig: model.GenerationConfig{
        Stream: false,
    },
}

responseChan, err := llm.GenerateContent(ctx, request)
```

## Retry Configuration Examples

### Basic Retry

```go
llm := openai.New("gpt-4",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(3),
        openaiopt.WithRequestTimeout(30*time.Second),
    ),
)
```

### Rate Limiting Optimization

```go
llm := openai.New("gpt-4",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(5),           // More retries for rate limiting
        openaiopt.WithRequestTimeout(30*time.Second),
    ),
)
```

## Error Handling

The example includes comprehensive error handling for:

- Missing API keys
- API errors
- Network timeouts
- Invalid responses
- Rate limiting scenarios

## Security Notes

- Never commit API keys to version control
- Use environment variables or secure configuration management
- The default API key in the example is for demonstration only

## Important Notes

- **No Framework Retry**: The framework itself doesn't implement retry logic
- **Client-level Retry**: All retry logic is handled by the OpenAI client
- **Configuration Pass-through**: Use `WithOpenAIOptions` to configure retry behavior
- **Automatic Handling**: Rate limiting (429) is automatically handled with smart backoff
- **Environment Variables**: API key and base URL are automatically read from environment

## Benefits

1. **Leverages Existing Logic**: Uses OpenAI client's mature retry mechanism
2. **Smart Backoff**: Follows API recommendations for retry timing
3. **Rate Limit Aware**: Automatically handles 429 responses
4. **Zero Maintenance**: No custom retry logic to maintain
5. **Consistent Behavior**: Same retry behavior as other OpenAI clients
6. **Easy Configuration**: Simple command line flags for common settings
