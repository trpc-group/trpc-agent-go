# Model Example

This example demonstrates how to use the OpenAI-like model implementation with environment variable configuration.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Overview

The example shows how to use the OpenAI-like model implementation with automatic environment variable reading and flexible command line configuration.

## Key Features

1. **Environment Variable Support**: Automatically reads `OPENAI_API_KEY` and `OPENAI_BASE_URL`
2. **Command Line Configuration**: Use flags to configure model parameters
3. **Multiple Examples**: Non-streaming, streaming, advanced parameters, and parameter testing
4. **Comprehensive Error Handling**: Robust error handling for various scenarios

## Environment Variables

The example supports the following environment variables:

| Variable          | Description                                                                | Default Value               |
| ----------------- | -------------------------------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required, automatically read by OpenAI SDK) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK)     | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code. The SDK handles this automatically when creating the client.

## Command Line Arguments

| Argument | Description              | Default Value |
| -------- | ------------------------ | ------------- |
| `-model` | Name of the model to use | `gpt-4o-mini` |

## How It Works

The model implementation works by:

1. **Automatic Configuration**: SDK automatically reads environment variables
2. **Model Creation**: Creates OpenAI client with specified model
3. **Request Processing**: Handles both streaming and non-streaming requests
4. **Response Handling**: Processes responses with comprehensive metadata

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

1. **🔄 Non-streaming Example**: Shows basic model usage without streaming
2. **🌊 Streaming Example**: Shows streaming response handling
3. **⚡ Advanced Example**: Shows usage with custom parameters
4. **🧪 Parameter Testing Example**: Tests different parameter combinations

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
llm := openai.New(modelName)

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

## Model Configuration Examples

### Basic Usage

```go
// Simple model creation with default settings
llm := openai.New("gpt-4o-mini")

// Create basic request
request := &model.Request{
    Messages: []model.Message{
        model.NewUserMessage("Hello, how are you?"),
    },
    GenerationConfig: model.GenerationConfig{
        Stream: false,
    },
}
```

### Advanced Configuration

```go
// Model with custom parameters
llm := openai.New("gpt-4")

// Advanced request with custom parameters
temperature := 0.7
maxTokens := 1000
request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("You are an expert programmer."),
        model.NewUserMessage("Explain Go channels."),
    },
    GenerationConfig: model.GenerationConfig{
        Temperature: &temperature,
        MaxTokens:   &maxTokens,
        Stream:      false,
    },
}
```

### Streaming Configuration

```go
// Streaming request
llm := openai.New("gpt-4o-mini")

request := &model.Request{
    Messages: []model.Message{
        model.NewUserMessage("Write a story."),
    },
    GenerationConfig: model.GenerationConfig{
        Stream: true,
    },
}
```

## Error Handling

The example includes comprehensive error handling for:

- Missing API keys
- API errors
- Network timeouts
- Invalid responses
- Streaming errors

## Security Notes

- Never commit API keys to version control
- Use environment variables or secure configuration management
- The default API key in the example is for demonstration only

## Important Notes

- **Automatic Environment Reading**: API key and base URL are automatically read from environment
- **Flexible Model Selection**: Use command line flags to select different models
- **Streaming Support**: Both streaming and non-streaming modes are supported
- **Parameter Flexibility**: Easy configuration of temperature, max tokens, and other parameters
- **Comprehensive Metadata**: Full response metadata including token usage and timing

## Benefits

1. **Easy Setup**: Automatic environment variable reading
2. **Flexible Configuration**: Command line flags for quick model selection
3. **Multiple Modes**: Support for both streaming and non-streaming
4. **Rich Examples**: Comprehensive examples covering various use cases
5. **Error Resilience**: Robust error handling for production use
6. **Professional Structure**: Clean, maintainable code structure
