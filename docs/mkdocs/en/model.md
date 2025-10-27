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

### 2. Batch Processing (Batch API)

Batch API is an asynchronous batch processing technique for efficiently handling large volumes of requests. This feature is particularly suitable for scenarios requiring large-scale data processing, significantly reducing costs and improving processing efficiency.

#### Core Features

- **Asynchronous Processing**: Batch requests are processed asynchronously without waiting for immediate responses
- **Cost Optimization**: Typically more cost-effective than individual requests
- **Flexible Input**: Supports both inline requests and file-based input
- **Complete Management**: Provides full operations including create, retrieve, cancel, and list
- **Result Parsing**: Automatically downloads and parses batch processing results

#### Quick Start

**Creating a Batch Job**:

```go
import (
    openaisdk "github.com/openai/openai-go"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create model instance.
llm := openai.New("gpt-4o-mini")

// Prepare batch requests.
requests := []*openai.BatchRequestInput{
    {
        CustomID: "request-1",
        Method:   "POST",
        URL:      string(openaisdk.BatchNewParamsEndpointV1ChatCompletions),
        Body: openai.BatchRequest{
            Messages: []model.Message{
                model.NewSystemMessage("You are a helpful assistant."),
                model.NewUserMessage("Hello"),
            },
        },
    },
    {
        CustomID: "request-2",
        Method:   "POST",
        URL:      string(openaisdk.BatchNewParamsEndpointV1ChatCompletions),
        Body: openai.BatchRequest{
            Messages: []model.Message{
                model.NewSystemMessage("You are a helpful assistant."),
                model.NewUserMessage("Introduce Go language"),
            },
        },
    },
}

// Create batch job.
batch, err := llm.CreateBatch(ctx, requests,
    openai.WithBatchCreateCompletionWindow("24h"),
)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Batch job created: %s\n", batch.ID)
```

#### Batch Operations

**Retrieving Batch Status**:

```go
// Get batch details.
batch, err := llm.RetrieveBatch(ctx, batchID)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Status: %s\n", batch.Status)
fmt.Printf("Total requests: %d\n", batch.RequestCounts.Total)
fmt.Printf("Completed: %d\n", batch.RequestCounts.Completed)
fmt.Printf("Failed: %d\n", batch.RequestCounts.Failed)
```

**Downloading and Parsing Results**:

```go
// Download output file.
if batch.OutputFileID != "" {
    text, err := llm.DownloadFileContent(ctx, batch.OutputFileID)
    if err != nil {
        log.Fatal(err)
    }

    // Parse batch output.
    entries, err := llm.ParseBatchOutput(text)
    if err != nil {
        log.Fatal(err)
    }

    // Process each result.
    for _, entry := range entries {
        fmt.Printf("[%s] Status code: %d\n", entry.CustomID, entry.Response.StatusCode)
        if len(entry.Response.Body.Choices) > 0 {
            content := entry.Response.Body.Choices[0].Message.Content
            fmt.Printf("Content: %s\n", content)
        }
        if entry.Error != nil {
            fmt.Printf("Error: %s\n", entry.Error.Message)
        }
    }
}
```

**Canceling a Batch Job**:

```go
// Cancel an in-progress batch.
batch, err := llm.CancelBatch(ctx, batchID)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Batch job canceled: %s\n", batch.ID)
```

**Listing Batch Jobs**:

```go
// List batch jobs (with pagination support).
page, err := llm.ListBatches(ctx, "", 10)
if err != nil {
    log.Fatal(err)
}

for _, batch := range page.Data {
    fmt.Printf("ID: %s, Status: %s\n", batch.ID, batch.Status)
}
```

#### Configuration Options

**Global Configuration**:

```go
// Configure batch default parameters when creating model.
llm := openai.New("gpt-4o-mini",
    openai.WithBatchCompletionWindow("24h"),
    openai.WithBatchMetadata(map[string]string{
        "project": "my-project",
        "env":     "production",
    }),
    openai.WithBatchBaseURL("https://custom-batch-api.com"),
)
```

**Request-level Configuration**:

```go
// Override default configuration when creating batch.
batch, err := llm.CreateBatch(ctx, requests,
    openai.WithBatchCreateCompletionWindow("48h"),
    openai.WithBatchCreateMetadata(map[string]string{
        "priority": "high",
    }),
)
```

#### How It Works

Batch API execution flow:

```
1. Prepare batch requests (BatchRequestInput list)
2. Validate request format and CustomID uniqueness
3. Generate JSONL format input file
4. Upload input file to server
5. Create batch job
6. Process requests asynchronously
7. Download output file and parse results
```

Key design:

- **CustomID Uniqueness**: Each request must have a unique CustomID for matching input/output
- **JSONL Format**: Batch processing uses JSONL (JSON Lines) format for storing requests and responses
- **Asynchronous Processing**: Batch jobs execute asynchronously in the background without blocking main flow
- **Completion Window**: Configurable completion time window for batch processing (e.g., 24h)

#### Use Cases

- **Large-scale Data Processing**: Processing thousands or tens of thousands of requests
- **Offline Analysis**: Non-real-time data analysis and processing tasks
- **Cost Optimization**: Batch processing is typically more economical than individual requests
- **Scheduled Tasks**: Regularly executed batch processing jobs

#### Usage Example

For a complete interactive example, see [examples/model/batch](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/batch).

### 3. Retry Mechanism

The retry mechanism is an automatic error recovery technique that automatically retries failed requests. This feature is provided by the underlying OpenAI SDK, with the framework passing retry parameters to the SDK through configuration options.

#### Core Features

- **Automatic Retry**: SDK automatically handles retryable errors
- **Smart Backoff**: Follows API's `Retry-After` headers or uses exponential backoff
- **Configurable**: Supports custom maximum retry count and timeout duration
- **Zero Maintenance**: No custom retry logic needed, handled by mature SDK

#### Quick Start

**Basic Configuration**:

```go
import (
    "time"
    openaiopt "github.com/openai/openai-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create model instance with retry configuration.
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(3),
        openaiopt.WithRequestTimeout(30*time.Second),
    ),
)
```

#### Retryable Errors

The OpenAI SDK automatically retries the following errors:

- **408 Request Timeout**: Request timeout
- **409 Conflict**: Conflict error
- **429 Too Many Requests**: Rate limiting
- **500+ Server Errors**: Internal server errors (5xx)
- **Network Connection Errors**: No response or connection failure

**Note**: SDK default maximum retry count is 2.

#### Retry Strategies

**Standard Retry**:

```go
// Standard configuration suitable for most scenarios.
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(3),
        openaiopt.WithRequestTimeout(30*time.Second),
    ),
)
```

**Rate Limiting Optimization**:

```go
// Optimized configuration for rate limiting scenarios.
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(5),  // More retry attempts.
        openaiopt.WithRequestTimeout(60*time.Second),  // Longer timeout.
    ),
)
```

**Fast Fail**:

```go
// For scenarios requiring quick failure.
llm := openai.New("gpt-4o-mini",
    openai.WithOpenAIOptions(
        openaiopt.WithMaxRetries(1),  // Minimal retries.
        openaiopt.WithRequestTimeout(10*time.Second),  // Short timeout.
    ),
)
```

#### How It Works

Retry mechanism execution flow:

```
1. Send request to LLM API
2. If request fails and error is retryable:
   a. Check if maximum retry count is reached
   b. Calculate wait time based on Retry-After header or exponential backoff
   c. Wait and resend request
3. If request succeeds or error is not retryable, return result
```

Key design:

- **SDK-level Implementation**: Retry logic is completely handled by OpenAI SDK
- **Configuration Pass-through**: Framework passes configuration via `WithOpenAIOptions`
- **Smart Backoff**: Prioritizes using `Retry-After` header returned by API
- **Transparent Handling**: Transparent to application layer, no additional code needed

#### Use Cases

- **Production Environment**: Improve service reliability and fault tolerance
- **Rate Limiting**: Automatically handle 429 errors
- **Network Instability**: Handle temporary network failures
- **Server Errors**: Handle temporary server-side issues

#### Important Notes

- **No Framework Retry**: Framework itself does not implement retry logic
- **Client-level Retry**: All retry is handled by OpenAI client
- **Configuration Pass-through**: Use `WithOpenAIOptions` to configure retry behavior
- **Automatic Handling**: Rate limiting (429) is automatically handled without additional code

#### Usage Example

For a complete interactive example, see [examples/model/retry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/retry).

### 4. Model Switching

Model switching allows dynamically changing the LLM model used by an Agent at runtime, accomplished simply with the `SetModel` method.

#### Basic Usage

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create Agent.
agent := llmagent.New("my-agent",
    llmagent.WithModel(openai.New("gpt-4o-mini")),
)

// Switch to another model.
agent.SetModel(openai.New("gpt-4o"))
```

#### Use Cases

```go
// Select model based on task complexity.
if isComplexTask {
    agent.SetModel(openai.New("gpt-4o"))  // Use powerful model.
} else {
    agent.SetModel(openai.New("gpt-4o-mini"))  // Use fast model.
}
```

#### Important Notes

- **Immediate Effect**: After calling `SetModel`, the next request immediately uses the new model
- **Session Persistence**: Switching models does not clear session history
- **Independent Configuration**: Each model retains its own configuration (temperature, max tokens, etc.)

#### Usage Example

For a complete interactive example, see [examples/model/switch](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/switch).

### 5. Token Tailoring

Token Tailoring is an intelligent message management technique that automatically trims messages when they exceed the model's context window limit, ensuring requests can be successfully sent to the LLM API. This feature is particularly useful for long conversation scenarios, maintaining key context while keeping the message list within the model's token constraints.

#### Core Features

- **Dual-mode Configuration**: Supports automatic and advanced modes
- **Intelligent Preservation**: Automatically preserves system messages and the last conversation turn
- **Multiple Strategies**: Provides MiddleOut, HeadOut, and TailOut trimming strategies
- **Efficient Algorithm**: Uses prefix sum and binary search with O(n) time complexity
- **Real-time Statistics**: Displays message and token counts before and after tailoring

#### Quick Start

**Automatic Mode (Recommended)**:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Simply enable token tailoring, other parameters are auto-configured
model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
)
```

Automatic mode will:

- Automatically detect the model's context window size
- Calculate optimal `maxInputTokens` (subtracting protocol overhead and output reserve)
- Use default `SimpleTokenCounter` and `MiddleOutStrategy`

**Advanced Mode**:

```go
// Custom token limit and strategy
model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),               // Required: enable token tailoring
    openai.WithMaxInputTokens(10000),                    // Custom token limit
    openai.WithTokenCounter(customCounter),              // Optional: custom counter
    openai.WithTailoringStrategy(customStrategy),        // Optional: custom strategy
)
```

#### Tailoring Strategies

The framework provides three built-in strategies for different scenarios:

**MiddleOutStrategy (Default)**:

Removes messages from the middle, preserving head and tail:

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

counter := model.NewSimpleTokenCounter()
strategy := model.NewMiddleOutStrategy(counter)

model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithMaxInputTokens(10000),
    openai.WithTailoringStrategy(strategy),
)
```

- **Use Case**: Scenarios requiring both initial and recent context
- **Preserves**: System message + early messages + recent messages + last turn

**HeadOutStrategy**:

Removes messages from the head, prioritizing recent messages:

```go
strategy := model.NewHeadOutStrategy(counter)

model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithMaxInputTokens(10000),
    openai.WithTailoringStrategy(strategy),
)
```

- **Use Case**: Chat applications where recent context is more important
- **Preserves**: System message + recent messages + last turn

**TailOutStrategy**:

Removes messages from the tail, prioritizing early messages:

```go
strategy := model.NewTailOutStrategy(counter)

model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithMaxInputTokens(10000),
    openai.WithTailoringStrategy(strategy),
)
```

- **Use Case**: RAG applications where initial instructions and context are more important
- **Preserves**: System message + early messages + last turn

#### Token Counters

**SimpleTokenCounter (Default)**:

Fast estimation based on character count:

```go
counter := model.NewSimpleTokenCounter()
```

- **Pros**: Fast, no external dependencies, suitable for most scenarios
- **Cons**: Slightly less accurate than tiktoken

**TikToken Counter (Optional)**:

Accurate counting using OpenAI's official tokenizer:

```go
import "trpc.group/trpc-go/trpc-agent-go/model/tiktoken"

tkCounter, err := tiktoken.New("gpt-4o")
if err != nil {
    // Handle error
}

model := openai.New("gpt-4o-mini",
    openai.WithEnableTokenTailoring(true),
    openai.WithTokenCounter(tkCounter),
)
```

- **Pros**: Accurately matches OpenAI API token counting
- **Cons**: Requires additional dependency, slightly lower performance

#### How It Works

Token Tailoring execution flow:

```
1. Check if token tailoring is enabled via WithEnableTokenTailoring(true)
2. Calculate total tokens for current messages
3. If exceeds limit:
   a. Mark messages that must be preserved (system message + last turn)
   b. Apply selected strategy to trim middle messages
   c. Ensure result is within token limit
4. Return trimmed message list
```

**Important**: Token tailoring is only activated when `WithEnableTokenTailoring(true)` is set. The `WithMaxInputTokens()` option only sets the token limit but does not enable tailoring by itself.

Key design principles:

- **Immutable Original**: Original message list remains unchanged
- **Smart Preservation**: Automatically preserves system message and last complete user-assistant pair
- **Efficient Algorithm**: Uses prefix sum (O(n)) + binary search (O(log n))

#### Model Context Registration

For custom models not recognized by the framework, you can register their context window size to enable automatic mode:

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

// Register a single model
model.RegisterModelContextWindow("my-custom-model", 8192)

// Batch register multiple models
model.RegisterModelContextWindows(map[string]int{
    "my-model-1": 4096,
    "my-model-2": 16384,
    "my-model-3": 32768,
})

// Then use automatic mode
m := openai.New("my-custom-model",
    openai.WithEnableTokenTailoring(true), // Auto-detect context window
)
```

**Use Cases**:

- Using privately deployed or custom models
- Overriding framework built-in context window configurations
- Adapting to newly released model versions

#### Usage Example

For a complete interactive example, see [examples/tailor](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/tailor).
