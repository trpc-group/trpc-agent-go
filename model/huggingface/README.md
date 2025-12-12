# HuggingFace Model Integration

This directory contains the HuggingFace model implementation for trpc-agent-go, providing seamless integration with HuggingFace's Inference API.

## Features

- ✅ **Native API Support**: Direct integration with HuggingFace Inference API
- ✅ **Streaming Support**: Real-time streaming responses
- ✅ **Callback Mechanisms**: Monitor requests, responses, and streaming chunks
- ✅ **Flexible Configuration**: Customizable API endpoints, headers, and parameters
- ✅ **Error Handling**: Comprehensive error handling and reporting
- 🚧 **tRPC Client Support**: Coming soon
- 🚧 **Token Tailoring**: Coming soon

## Installation

```bash
go get trpc.group/trpc-go/trpc-agent-go
```

## Quick Start

### Basic Chat

```go
package main

import (
    "context"
    "fmt"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/huggingface"
)

func main() {
    // Create model instance
    m, err := huggingface.New(
        "meta-llama/Llama-2-7b-chat-hf",
        huggingface.WithAPIKey("your-api-key"),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Create request
    request := &model.Request{
        Messages: []model.Message{
            {
                Role:    model.RoleUser,
                Content: "Hello, how are you?",
            },
        },
    }

    // Generate response
    ctx := context.Background()
    responseChan, err := m.GenerateContent(ctx, request)
    if err != nil {
        log.Fatal(err)
    }

    // Read response
    for resp := range responseChan {
        if resp.Error != nil {
            log.Printf("Error: %s", resp.Error.Message)
            continue
        }
        if len(resp.Choices) > 0 {
            fmt.Println(resp.Choices[0].Message.Content)
        }
    }
}
```

### Streaming Chat

```go
m, err := huggingface.New(
    "meta-llama/Llama-2-7b-chat-hf",
    huggingface.WithAPIKey("your-api-key"),
    huggingface.WithChatChunkCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest, chunk *huggingface.ChatCompletionChunk) {
        if len(chunk.Choices) > 0 {
            fmt.Print(chunk.Choices[0].Delta.Content)
        }
    }),
)

request := &model.Request{
    Messages: []model.Message{
        {Role: model.RoleUser, Content: "Tell me a story"},
    },
    Stream: true, // Enable streaming
}

responseChan, _ := m.GenerateContent(context.Background(), request)
for range responseChan {
    // Chunks are handled by callback
}
```

## Configuration Options

### API Configuration

```go
huggingface.WithAPIKey("your-api-key")           // Set API key
huggingface.WithBaseURL("https://custom.api")    // Custom API endpoint
huggingface.WithHTTPClient(customClient)         // Custom HTTP client
```

### Callbacks

```go
// Request callback - called before sending request
huggingface.WithChatRequestCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest) {
    fmt.Println("Sending request...")
})

// Response callback - called after receiving non-streaming response
huggingface.WithChatResponseCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest, resp *huggingface.ChatCompletionResponse) {
    fmt.Printf("Received response: %s\n", resp.ID)
})

// Chunk callback - called for each streaming chunk
huggingface.WithChatChunkCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest, chunk *huggingface.ChatCompletionChunk) {
    fmt.Print(chunk.Choices[0].Delta.Content)
})

// Stream complete callback - called when streaming finishes
huggingface.WithChatStreamCompleteCallback(func(ctx context.Context, req *huggingface.ChatCompletionRequest, err error) {
    if err != nil {
        fmt.Printf("Stream error: %v\n", err)
    } else {
        fmt.Println("Stream completed")
    }
})
```

### Advanced Options

```go
// Extra HTTP headers
huggingface.WithExtraHeaders(map[string]string{
    "X-Custom-Header": "value",
})

// Extra request fields
huggingface.WithExtraFields(map[string]any{
    "custom_field": "value",
})

// Channel buffer size
huggingface.WithChannelBufferSize(512)
```

## Supported Models

You can use any model available on HuggingFace Inference API. Popular choices include:

- **Llama 2**: `meta-llama/Llama-2-7b-chat-hf`, `meta-llama/Llama-2-13b-chat-hf`
- **Mistral**: `mistralai/Mistral-7B-Instruct-v0.2`
- **Zephyr**: `HuggingFaceH4/zephyr-7b-beta`
- **CodeLlama**: `codellama/CodeLlama-7b-Instruct-hf`

## API Key Setup

You can provide your HuggingFace API key in two ways:

1. **Via Option**:
   ```go
   huggingface.WithAPIKey("your-api-key")
   ```

2. **Via Environment Variable**:
   ```bash
   export HUGGINGFACE_API_KEY="your-api-key"
   ```

Get your API key from: https://huggingface.co/settings/tokens

## Examples

See the `examples/huggingface/` directory for complete examples:

- `basic_chat.go` - Simple chat example
- `streaming_chat.go` - Streaming chat with callbacks

## Error Handling

The implementation provides comprehensive error handling:

```go
responseChan, err := m.GenerateContent(ctx, request)
if err != nil {
    // Handle request creation error
    log.Fatal(err)
}

for resp := range responseChan {
    if resp.Error != nil {
        // Handle response error
        log.Printf("Error: %s", resp.Error.Message)
        continue
    }
    // Process successful response
}
```

## Roadmap

- [ ] tRPC client support for internal services
- [ ] Token tailoring for context window management
- [ ] Multi-modal support (images, audio)
- [ ] Function calling support
- [ ] Batch request support

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

## License

Apache License 2.0
