# Model Module

## Overview

The Model module is the large language model abstraction layer of the tRPC-Agent-Go framework, providing a unified LLM interface design that currently supports OpenAI-compatible API calls. Through standardized interface design, developers can flexibly switch between different model providers, achieving seamless model integration and invocation. This module has been verified to be compatible with most OpenAI-like interfaces both inside and outside the company.

The Model module has the following core features:

- **Unified Interface Abstraction**: Provides standardized `Model` interface, shielding differences between model providers
- **Streaming Response Support**: Native support for streaming output, enabling real-time interactive experience
- **Multimodal Capabilities**: Supports text, image, audio, and other multimodal content processing
- **Complete Error Handling**: Provides dual-layer error handling mechanism, distinguishing between system errors and API errors
- **Extensible Configuration**: Supports rich custom configuration options to meet different scenario requirements

## Quick Start

### Using Model in Agent

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

func main() {
    // 1. Create model instance.
    modelInstance := openai.New("deepseek-chat",
        openai.WithExtraFields(map[string]interface{}{
            "tool_choice": "auto", // Automatically select tools.
        }),
    )

    // 2. Configure generation parameters.
    genConfig := model.GenerationConfig{
        MaxTokens:   intPtr(2000),
        Temperature: floatPtr(0.7),
        Stream:      true, // Enable streaming output.
    }

    // 3. Create Agent and integrate model.
    agent := llmagent.New(
        "chat-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("A helpful assistant"),
        llmagent.WithInstruction("You are an intelligent assistant, use tools when needed."),
        llmagent.WithGenerationConfig(genConfig),
        llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
    )

    // 4. Create Runner and run.
    r := runner.NewRunner("app-name", agent)
    eventChan, err := r.Run(ctx, userID, sessionID, model.NewUserMessage("Hello"))
    if err != nil {
        log.Fatal(err)
    }

    // 5. Handle response events.
    for event := range eventChan {
        // Handle streaming responses, tool calls, etc.
    }
}
```

Example code is located at [examples/runner](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)

### Usage Methods and Platform Integration Guide

The Model module supports multiple usage methods and platform integration. The following are common usage scenarios based on Runner examples:

#### Quick Start

```bash
# Basic usage: Configure through environment variables, run directly.
cd examples/runner
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"
go run main.go -model deepseek-chat
```

#### Platform Integration Configuration

All platform integration methods follow the same pattern, only requiring configuration of different environment variables or direct setting in code:

**Environment Variable Method** (Recommended):

```bash
export OPENAI_BASE_URL="Platform API address"
export OPENAI_API_KEY="API key"
```

**Code Method**:

```go
model := openai.New("Model name",
    openai.WithBaseURL("Platform API address"),
    openai.WithAPIKey("API key"),
)
```

#### Supported Platforms and Their Configuration

The following are configuration examples for each platform, divided into environment variable configuration and code configuration methods:

**Environment Variable Configuration**

The runner example supports specifying model names through command line parameters (-model), which is actually passing the model name when calling `openai.New()`.

```bash
# OpenAI platform.
export OPENAI_API_KEY="sk-..."
cd examples/runner
go run main.go -model gpt-4o-mini

# OpenAI API compatible.
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"
cd examples/runner
go run main.go -model deepseek-chat
```

**Code Configuration Method**

Configuration method when directly using Model in your own code:

```go
model := openai.New("deepseek-chat",
    openai.WithBaseURL("https://api.deepseek.com/v1"),
    openai.WithAPIKey("your-api-key"),
)

// Other platform configurations are similar, only need to modify model name, BaseURL and APIKey, no additional fields needed.
```

## Core Interface Design

### Model Interface

```go
// Model is the interface that all language models must implement.
type Model interface {
    // Generate content, supports streaming response.
    GenerateContent(ctx context.Context, request *Request) (<-chan *Response, error)

    // Return basic model information.
    Info() Info
}

// Model information structure.
type Info struct {
    Name string // Model name.
}
```

### Request Structure

```go
// Request represents the request sent to the model.
type Request struct {
    // Message list, containing system instructions, user input and assistant replies.
    Messages []Message `json:"messages"`

    // Generation configuration (inlined into request).
    GenerationConfig `json:",inline"`

    // Tool list.
    Tools map[string]tool.Tool `json:"-"`
}

// GenerationConfig contains generation parameter configuration.
type GenerationConfig struct {
    // Whether to use streaming response.
    Stream bool `json:"stream"`

    // Temperature parameter (0.0-2.0).
    Temperature *float64 `json:"temperature,omitempty"`

    // Maximum generation token count.
    MaxTokens *int `json:"max_tokens,omitempty"`

    // Top-P sampling parameter.
    TopP *float64 `json:"top_p,omitempty"`

    // Stop generation markers.
    Stop []string `json:"stop,omitempty"`

    // Frequency penalty.
    FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`

    // Presence penalty.
    PresencePenalty *float64 `json:"presence_penalty,omitempty"`

    // Reasoning effort level ("low", "medium", "high").
    ReasoningEffort *string `json:"reasoning_effort,omitempty"`

    // Whether to enable thinking mode.
    ThinkingEnabled *bool `json:"-"`

    // Maximum token count for thinking mode.
    ThinkingTokens *int `json:"-"`
}
```

### Response Structure

```go
// Response represents the response returned by the model.
type Response struct {
    // OpenAI compatible fields.
    ID                string   `json:"id,omitempty"`
    Object            string   `json:"object,omitempty"`
    Created           int64    `json:"created,omitempty"`
    Model             string   `json:"model,omitempty"`
    SystemFingerprint *string  `json:"system_fingerprint,omitempty"`
    Choices           []Choice `json:"choices,omitempty"`
    Usage             *Usage   `json:"usage,omitempty"`

    // Error information.
    Error *ResponseError `json:"error,omitempty"`

    // Internal fields.
    Timestamp time.Time `json:"-"`
    Done      bool      `json:"-"`
    IsPartial bool      `json:"-"`
}

// ResponseError represents API-level errors.
type ResponseError struct {
    Message string    `json:"message"`
    Type    ErrorType `json:"type"`
    Param   string    `json:"param,omitempty"`
    Code    string    `json:"code,omitempty"`
}
```

### Direct Model Usage

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func main() {
    // Create model instance.
    llm := openai.New("deepseek-chat")

    // Build request.
    temperature := 0.7
    maxTokens := 1000

    request := &model.Request{
        Messages: []model.Message{
            model.NewSystemMessage("You are a professional AI assistant."),
            model.NewUserMessage("Introduce Go language's concurrency features."),
        },
        GenerationConfig: model.GenerationConfig{
            Temperature: &temperature,
            MaxTokens:   &maxTokens,
            Stream:      false,
        },
    }

    // Call model.
    ctx := context.Background()
    responseChan, err := llm.GenerateContent(ctx, request)
    if err != nil {
        fmt.Printf("System error: %v\n", err)
        return
    }

    // Handle response.
    for response := range responseChan {
        if response.Error != nil {
            fmt.Printf("API error: %s\n", response.Error.Message)
            return
        }

        if len(response.Choices) > 0 {
            fmt.Printf("Reply: %s\n", response.Choices[0].Message.Content)
        }

        if response.Done {
            break
        }
    }
}
```

### Streaming Output

```go
// Streaming request configuration.
request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("You are a creative story teller."),
        model.NewUserMessage("Write a short story about a robot learning to paint."),
    },
    GenerationConfig: model.GenerationConfig{
        Stream: true,  // Enable streaming output.
    },
}

// Handle streaming response.
responseChan, err := llm.GenerateContent(ctx, request)
if err != nil {
    return err
}

for response := range responseChan {
    if response.Error != nil {
        fmt.Printf("Error: %s", response.Error.Message)
        return
    }

    if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
        fmt.Print(response.Choices[0].Delta.Content)
    }

    if response.Done {
        break
    }
}
```

### Advanced Parameter Configuration

```go
// Use advanced generation parameters.
temperature := 0.3
maxTokens := 2000
topP := 0.9
presencePenalty := 0.2
frequencyPenalty := 0.5
reasoningEffort := "high"

request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("You are a professional technical documentation writer."),
        model.NewUserMessage("Explain the advantages and disadvantages of microservice architecture."),
    },
    GenerationConfig: model.GenerationConfig{
        Temperature:      &temperature,
        MaxTokens:        &maxTokens,
        TopP:             &topP,
        PresencePenalty:  &presencePenalty,
        FrequencyPenalty: &frequencyPenalty,
        ReasoningEffort:  &reasoningEffort,
        Stream:           true,
    },
}
```

### Multimodal Content

```go
// Read image file.
imageData, _ := os.ReadFile("image.jpg")

// Create multimodal message.
request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("You are an image analysis expert."),
        {
            Role: model.RoleUser,
            ContentParts: []model.ContentPart{
                {
                    Type: model.ContentTypeText,
                    Text: stringPtr("What's in this image?"),
                },
                {
                    Type: model.ContentTypeImage,
                    Image: &model.Image{
                        Data:   imageData,
                        Format: "jpeg",
                    },
                },
            },
        },
    },
}
```

## Advanced Features

### 1. Callback Functions

```go
// Set pre-request callback function.
model := openai.New("deepseek-chat",
    openai.WithChatRequestCallback(func(ctx context.Context, req *openai.ChatCompletionNewParams) {
        // Called before request is sent.
        log.Printf("Sending request: model=%s, message count=%d", req.Model, len(req.Messages))
    }),

    // Set response callback function (non-streaming).
    openai.WithChatResponseCallback(func(ctx context.Context,
        req *openai.ChatCompletionNewParams,
        resp *openai.ChatCompletion) {
        // Called when complete response is received.
        log.Printf("Received response: ID=%s, tokens used=%d",
            resp.ID, resp.Usage.TotalTokens)
    }),

    // Set streaming response callback function.
    openai.WithChatChunkCallback(func(ctx context.Context,
        req *openai.ChatCompletionNewParams,
        chunk *openai.ChatCompletionChunk) {
        // Called when each streaming response chunk is received.
        log.Printf("Received streaming chunk: ID=%s", chunk.ID)
    }),

    // Set streaming completion callback function.
    openai.WithChatStreamCompleteCallback(func(ctx context.Context,
        req *openai.ChatCompletionNewParams,
        acc *openai.ChatCompletionAccumulator,
        streamErr error) {
        // Called when streaming is completely finished (success or error).
        if streamErr != nil {
            log.Printf("Streaming failed: %v", streamErr)
        } else {
            log.Printf("Streaming completed: reason=%s", 
                acc.Choices[0].FinishReason)
        }
    }),
)
```
