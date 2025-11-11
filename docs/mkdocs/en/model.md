# Model Module

## Overview

The Model module is the large language model abstraction layer of the tRPC-Agent-Go framework, providing a unified LLM interface design that currently supports OpenAI-compatible and Anthropic-compatible API calls. Through standardized interface design, developers can flexibly switch between different model providers, achieving seamless model integration and invocation. This module has been verified to be compatible with most OpenAI-like interfaces both inside and outside the company.

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

## OpenAI Model

The OpenAI Model is used to interface with OpenAI and its compatible platforms. It supports streaming output, multimodal and advanced parameter configuration, and provides rich callback mechanisms, batch processing and retry capabilities. It also allows for flexible setting of custom HTTP headers.

### Configuration Method

#### Environment Variable Method

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.openai.com" # Optional configuration, default is this BASE URL
```

#### Code Method

```go
import "trpc.group/trpc-go/trpc-agent-go/model/openai"

m := openai.New(
    "gpt-4o",
    openai.WithAPIKey("your-api-key"),
    openai.WithBaseURL("https://api.openai.com"), // Optional configuration, default is this BASE URL
)
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

### Advanced Features

#### 1. Callback Functions

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

#### 2. Batch Processing (Batch API)

Batch API is an asynchronous batch processing technique for efficiently handling large volumes of requests. This feature is particularly suitable for scenarios requiring large-scale data processing, significantly reducing costs and improving processing efficiency.

##### Core Features

- **Asynchronous Processing**: Batch requests are processed asynchronously without waiting for immediate responses
- **Cost Optimization**: Typically more cost-effective than individual requests
- **Flexible Input**: Supports both inline requests and file-based input
- **Complete Management**: Provides full operations including create, retrieve, cancel, and list
- **Result Parsing**: Automatically downloads and parses batch processing results

##### Quick Start

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

##### Batch Operations

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

##### Configuration Options

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

##### How It Works

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

##### Use Cases

- **Large-scale Data Processing**: Processing thousands or tens of thousands of requests
- **Offline Analysis**: Non-real-time data analysis and processing tasks
- **Cost Optimization**: Batch processing is typically more economical than individual requests
- **Scheduled Tasks**: Regularly executed batch processing jobs

##### Usage Example

For a complete interactive example, see [examples/model/batch](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/batch).

#### 3. Retry Mechanism

The retry mechanism is an automatic error recovery technique that automatically retries failed requests. This feature is provided by the underlying OpenAI SDK, with the framework passing retry parameters to the SDK through configuration options.

##### Core Features

- **Automatic Retry**: SDK automatically handles retryable errors
- **Smart Backoff**: Follows API's `Retry-After` headers or uses exponential backoff
- **Configurable**: Supports custom maximum retry count and timeout duration
- **Zero Maintenance**: No custom retry logic needed, handled by mature SDK

##### Quick Start

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

##### Retryable Errors

The OpenAI SDK automatically retries the following errors:

- **408 Request Timeout**: Request timeout
- **409 Conflict**: Conflict error
- **429 Too Many Requests**: Rate limiting
- **500+ Server Errors**: Internal server errors (5xx)
- **Network Connection Errors**: No response or connection failure

**Note**: SDK default maximum retry count is 2.

##### Retry Strategies

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

##### How It Works

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

##### Use Cases

- **Production Environment**: Improve service reliability and fault tolerance
- **Rate Limiting**: Automatically handle 429 errors
- **Network Instability**: Handle temporary network failures
- **Server Errors**: Handle temporary server-side issues

##### Important Notes

- **No Framework Retry**: Framework itself does not implement retry logic
- **Client-level Retry**: All retry is handled by OpenAI client
- **Configuration Pass-through**: Use `WithOpenAIOptions` to configure retry behavior
- **Automatic Handling**: Rate limiting (429) is automatically handled without additional code

##### Usage Example

For a complete interactive example, see [examples/model/retry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/model/retry).

#### 4. Custom HTTP Headers

In some enterprise or proxy scenarios, the model provider requires
additional HTTP headers (for example, organization ID, tenant routing,
or custom authentication). The Model module supports setting headers in
two reliable ways that apply to all model requests, including
non-streaming, streaming, file upload, and batch APIs.

Recommended order:

- Global header via OpenAI RequestOption (simple, built-in)
- Custom `http.RoundTripper` (advanced, cross-cutting)

Both methods affect streaming too because the same client is used for
`New` and `NewStreaming` calls.

##### 1. Global headers using OpenAI RequestOption

Use `WithOpenAIOptions` with `openaiopt.WithHeader` or
`openaiopt.WithMiddleware` to inject headers for every request created
by the underlying OpenAI client.

```go
import (
    openaiopt "github.com/openai/openai-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

llm := openai.New("deepseek-chat",
    // If your provider needs extra headers
    openai.WithOpenAIOptions(
        openaiopt.WithHeader("X-Custom-Header", "custom-value"),
        openaiopt.WithHeader("X-Request-ID", "req-123"),
        // You can also set User-Agent or vendor-specific headers
        openaiopt.WithHeader("User-Agent", "trpc-agent-go/1.0"),
    ),
)
```

For complex logic, middleware lets you modify headers conditionally
(for example, by URL path or context values):

```go
llm := openai.New("deepseek-chat",
    openai.WithOpenAIOptions(
        openaiopt.WithMiddleware(
            func(r *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
                // Example: per-request header via context value
                if v := r.Context().Value("x-request-id"); v != nil {
                    if s, ok := v.(string); ok && s != "" {
                        r.Header.Set("X-Request-ID", s)
                    }
                }
                // Or only for chat completion endpoint
                if strings.Contains(r.URL.Path, "/chat/completions") {
                    r.Header.Set("X-Feature-Flag", "on")
                }
                return next(r)
            },
        ),
    ),
)
```

Notes for authentication variants:

- OpenAI style: keep `openai.WithAPIKey("sk-...")` which sets
  `Authorization: Bearer ...` under the hood.
- Azure/OpenAI‑compatible that use `api-key`: omit `WithAPIKey` and set
  `openaiopt.WithHeader("api-key", "<key>")` instead.

##### 2. Custom http.RoundTripper (advanced)

Inject headers across all requests at the HTTP layer by wrapping the
transport. This is useful when you also need custom proxy, TLS, or
metrics logic.

```go
type headerRoundTripper struct{ base http.RoundTripper }

func (rt headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    // Add or override headers
    req.Header.Set("X-Custom-Header", "custom-value")
    req.Header.Set("X-Trace-ID", "trace-xyz")
    return rt.base.RoundTrip(req)
}

llm := openai.New("deepseek-chat",
    openai.WithHTTPClientOptions(
        openai.WithHTTPClientTransport(headerRoundTripper{base: http.DefaultTransport}),
    ),
)
```

Per-request headers

- Agent/Runner passes `ctx` through to the model call; middleware can
  read values from `req.Context()` to inject per-invocation headers.
- Chat completion per-request base URL override is not exposed; create a
  second model with a different base URL or alter `r.URL` in middleware.

#### 5. Token Tailoring

Token Tailoring is an intelligent message management technique designed to automatically trim messages when they exceed the model's context window limits, ensuring requests can be successfully sent to the LLM API. This feature is particularly useful for long conversation scenarios, allowing you to keep the message list within the model's token limits while preserving key context.

**Automatic Mode (Recommended)**:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Enable token tailoring with automatic configuration
model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
)
```

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

**Token Calculation Formula**:

The framework automatically calculates "maxInputTokens" based on the model's context window:

```
safetyMargin = contextWindow × 10%
calculatedMax = contextWindow - 2048 (output reserve) - 512 (protocol overhead) - safetyMargin
ratioLimit = contextWindow × 100% (max input ratio)
maxInputTokens = max(min(calculatedMax, ratioLimit), 1024 (minimum))
```

For example, "gpt-4o" (contextWindow = 128000):

```
safetyMargin = 128000 × 0.10 = 12800 tokens
calculatedMax = 128000 - 2048 - 512 - 12800 = 112640 tokens
ratioLimit = 128000 × 1.0 = 128000 tokens
maxInputTokens = 112640 tokens (approximately 88% of context window)
```

**Default Budget Parameters**:

The framework uses the following default values for token allocation (**it is recommended to keep the defaults**):

- **Protocol Overhead (ProtocolOverheadTokens)**: 512 tokens - reserved for request/response formatting
- **Output Reserve (ReserveOutputTokens)**: 2048 tokens - reserved for output generation
- **Input Floor (InputTokensFloor)**: 1024 tokens - ensures proper model processing
- **Output Floor (OutputTokensFloor)**: 256 tokens - ensures meaningful responses
- **Safety Margin Ratio (SafetyMarginRatio)**: 10% - buffer for token counting inaccuracies
- **Max Input Ratio (MaxInputTokensRatio)**: 100% - maximum input ratio of context window

**Tailoring Strategy**:

The framework provides a default tailoring strategy that preserves messages according to the following priorities:

1. **System Messages**: Highest priority, always preserved
2. **Latest User Message**: Ensures the current conversation turn is complete
3. **Tool Call Related Messages**: Maintains tool call context integrity
4. **Historical Messages**: Retains as much conversation history as possible based on remaining space

**Custom Tailoring Strategy**:

You can implement the `TailoringStrategy` interface to customize the trimming logic:

```go
type CustomStrategy struct{}

func (s *CustomStrategy) Tailor(
    ctx context.Context,
    messages []model.Message,
    maxTokens int,
    counter tokencounter.Counter,
) ([]model.Message, error) {
    // Implement custom tailoring logic
    // e.g., keep only the most recent N conversation rounds
    return messages, nil
}

model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithTailoringStrategy(&CustomStrategy{}),
)
```

**Advanced Configuration (Custom Budget Parameters)**:

If the default token allocation strategy does not meet your needs, you can customize the budget parameters using `WithTokenTailoringConfig`. **Note: It is recommended to keep the default values unless you have specific requirements.**

```go
model := openai.New("deepseek-chat",
    openai.WithEnableTokenTailoring(true),
    openai.WithTokenTailoringConfig(&openai.TokenTailoringConfig{
        ProtocolOverheadTokens: 1024,   // Custom protocol overhead
        ReserveOutputTokens:    4096,   // Custom output reserve
        InputTokensFloor:       2048,   // Custom input floor
        OutputTokensFloor:      512,    // Custom output floor
        SafetyMarginRatio:      0.15,   // Custom safety margin (15%)
        MaxInputTokensRatio:    0.90,   // Custom max input ratio (90%)
    }),
)
```

For Anthropic models, you can use the same configuration:

```go
model := anthropic.New("claude-sonnet-4-0",
    anthropic.WithEnableTokenTailoring(true),
    anthropic.WithTokenTailoringConfig(&anthropic.TokenTailoringConfig{
        SafetyMarginRatio: 0.15,  // Increase safety margin to 15%
    }),
)
```

#### 6. Variant Optimization: Adapting to Platform-Specific Behaviors
The Variant mechanism is an important optimization in the Model module, used to handle platform-specific behavioral differences across OpenAI-compatible providers. By specifying different Variants, the framework can automatically adapt to API differences between platforms, especially for file upload, deletion, and processing logic.
##### 6.1. Supported Variant Types
The framework currently supports the following Variants:

**1. VariantOpenAI（default）**

- Standard OpenAI API-compatible behavior
- File upload path：`/openapi/v1/files`
- File purpose:`user_data`
- File deletion Http method:：`DELETE`

**2. VariantHunyuan（hunyuan）**

- Tencent Hunyuan platform-specific adaptation
- File upload path:：`/openapi/v1/files/uploads`
- File purpose：`file-extract`
- File deletion Http Method：`POST`

**3. VariantDeepSeek**

- DeepSeek platform adaptation
- Default BaseURL：`https://api.deepseek.com`
- API Key environment variable name：`DEEPSEEK_API_KEY`
- Other behaviors are consistent with standard OpenAI

##### 6.2. Usage

**Usage Example**：

```go
import "trpc.group/trpc-go/trpc-agent-go/model/openai"

// Use the Hunyuan platform
model := openai.New("hunyuan-model",
    openai.WithBaseURL("https://your-hunyuan-api.com"),
    openai.WithAPIKey("your-api-key"),
    openai.WithVariant(openai.VariantHunyuan), // Specify the Hunyuan variant
)

// Use the DeepSeek platform
model := openai.New("deepseek-chat",
    openai.WithBaseURL("https://api.deepseek.com/v1"),
    openai.WithAPIKey("your-api-key"),
    openai.WithVariant(openai.VariantDeepSeek), // Specify the DeepSeek variant
)
```
##### 6.3. Behavioral Differences of Variants Examples

**Message content handling differences**：

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

// For the Hunyuan platform, the file ID is placed in extraFields instead of content parts
message := model.Message{
    Role: model.RoleUser,
    ContentParts: []model.ContentPart{
        {
            Type: model.ContentTypeFile,
            File: &model.File{
                FileID: "file_123",
            },
        },
    },
}
```
**Environment variable auto-configuration**

For certain Variants, the framework supports reading configuration from environment variables automatically:

```bash
# DeepSeek
export DEEPSEEK_API_KEY="your-api-key"
# No need to call WithAPIKey explicitly; the framework reads it automatically
```

```go
import "trpc.group/trpc-go/trpc-agent-go/model"

// DeepSeek
model := openai.New("deepseek-chat",
    openai.WithVariant(openai.VariantDeepSeek), // Automatically reads DEEPSEEK_API_KEY
)
```
## Anthropic Model

Anthropic Model is used to interface with Claude models and compatible platforms, supporting streaming output, thought modes and tool calls, and providing a rich callback mechanism, while also allowing for flexible configuration of custom HTTP headers.

### Configuration Method

#### Environment Variable Method

```bash
export ANTHROPIC_API_KEY="your-api-key"
export ANTHROPIC_BASE_URL="https://api.anthropic.com" # Optional configuration, default is this BASE URL
```

#### Code Method

```go
import "trpc.group/trpc-go/trpc-agent-go/model/anthropic"

m := anthropic.New(
    "claude-sonnet-4-0",
    anthropic.WithAPIKey("your-api-key"),
    anthropic.WithBaseURL("https://api.anthropic.com"), // Optional configuration, default is this BASE URL
)
```

### Using the Model Directly

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

func main() {
	// Create model instance
	llm := anthropic.New("claude-sonnet-4-0")
	// Build request
	temperature := 0.7
	maxTokens := 1000
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a professional AI assistant."),
			model.NewUserMessage("Introduce the concurrency features of Go language."),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      false,
		},
	}
	// Call the model
	ctx := context.Background()
	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		fmt.Printf("System error: %v\n", err)
		return
	}
	// Handle response
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
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

func main() {
	// Create model instance
	llm := anthropic.New("claude-sonnet-4-0")
	// Streaming request configuration
	temperature := 0.7
	maxTokens := 1000
	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a creative story storyteller."),
			model.NewUserMessage("Write a short story about a robot learning to paint."),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      true,
		},
	}
	// Call the model
	ctx := context.Background()
	// Handle streaming response
	responseChan, err := llm.GenerateContent(ctx, request)
	if err != nil {
		fmt.Printf("System error: %v\n", err)
		return
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
}
```

### Advanced Parameter Configuration

```go
// Using advanced generation parameters
temperature := 0.3
maxTokens := 2000
topP := 0.9
thinking := true
thinkingTokens := 2048

request := &model.Request{
    Messages: []model.Message{
        model.NewSystemMessage("You are a professional technical documentation writer."),
        model.NewUserMessage("Explain the pros and cons of microservices architecture."),
    },
    GenerationConfig: model.GenerationConfig{
        Temperature:     &temperature,
        MaxTokens:       &maxTokens,
        TopP:            &topP,
        ThinkingEnabled: &thinking,
        ThinkingTokens:  &thinkingTokens,
        Stream:          true,
    },
}
```

### Advanced features

#### 1. Callback Functions

```go
import (
    anthropicsdk "github.com/anthropics/anthropic-sdk-go"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

model := anthropic.New(
    "claude-sonnet-4-0",
    anthropic.WithChatRequestCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams) {
        // Log the request before sending.
        log.Printf("sending request: model=%s, messages=%d.", req.Model, len(req.Messages))
    }),
    anthropic.WithChatResponseCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams, resp *anthropicsdk.Message) {
        // Log details of the non-streaming response.
        log.Printf("received response: id=%s, input_tokens=%d, output_tokens=%d.", resp.ID, resp.Usage.InputTokens, resp.Usage.OutputTokens)
    }),
    anthropic.WithChatChunkCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams, chunk *anthropicsdk.MessageStreamEventUnion) {
        // Log the type of the streaming event.
        log.Printf("stream event: %T.", chunk.AsAny())
    }),
    anthropic.WithChatStreamCompleteCallback(func(ctx context.Context, req *anthropicsdk.MessageNewParams, acc *anthropicsdk.Message, streamErr error) {
        // Log stream completion or error.
        if streamErr != nil {
            log.Printf("stream failed: %v.", streamErr)
            return
        }
        log.Printf("stream completed: finish_reason=%s, input_tokens=%d, output_tokens=%d.", acc.StopReason, acc.Usage.InputTokens, acc.Usage.OutputTokens)
    }),
)
```

#### 2. Custom HTTP Headers

In environments like gateways, proprietary platforms, or proxy setups, model API requests often require additional HTTP headers (e.g., organization/tenant identifiers, grayscale routing, custom authentication, etc.). The Model module provides two reliable ways to add headers for "all model requests," including standard requests, streaming, file uploads, batch processing, etc.

Recommended order:

* Use **Anthropic RequestOption** to set global headers (simple and intuitive)
* Use a custom `http.RoundTripper` injection (advanced, more cross-cutting capabilities)

Both methods affect streaming requests, as they use the same underlying client.

##### 1. Using Anthropic RequestOption to Set Global Headers

By using `WithAnthropicClientOptions` combined with `anthropicopt.WithHeader` or `anthropicopt.WithMiddleware`, you can inject headers into every request made by the underlying Anthropic client.

```go
import (
    anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

llm := anthropic.New("claude-sonnet-4-0",
    // If your platform requires additional headers
    anthropic.WithAnthropicClientOptions(
        anthropicopt.WithHeader("X-Custom-Header", "custom-value"),
        anthropicopt.WithHeader("X-Request-ID", "req-123"),
        // You can also set User-Agent or vendor-specific headers
        anthropicopt.WithHeader("User-Agent", "trpc-agent-go/1.0"),
    ),
)
```

If you need to set headers conditionally (e.g., only for certain paths or depending on context values), you can use middleware:

```go
import (
    anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

llm := anthropic.New("claude-sonnet-4-0",
    anthropic.WithAnthropicClientOptions(
        anthropicopt.WithMiddleware(
            func(r *http.Request, next anthropicopt.MiddlewareNext) (*http.Response, error) {
                // Example: Set "per-request" headers based on context value
                if v := r.Context().Value("x-request-id"); v != nil {
                    if s, ok := v.(string); ok && s != "" {
                        r.Header.Set("X-Request-ID", s)
                    }
                }
                // Or only for the "message completion" endpoint
                if strings.Contains(r.URL.Path, "v1/messages") {
                    r.Header.Set("X-Feature-Flag", "on")
                }
                return next(r)
            },
        ),
    ),
)
```

##### 2. Using Custom `http.RoundTripper`

For injecting headers at the HTTP transport layer, ideal for scenarios requiring proxying, TLS, custom monitoring, and other capabilities.

```go
import (
    anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

type headerRoundTripper struct{ base http.RoundTripper }

func (rt headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
    // Add or override headers
    req.Header.Set("X-Custom-Header", "custom-value")
    req.Header.Set("X-Trace-ID", "trace-xyz")
    return rt.base.RoundTrip(req)
}

llm := anthropic.New("claude-sonnet-4-0",
    anthropic.WithHTTPClientOptions(
        anthropic.WithHTTPClientTransport(headerRoundTripper{base: http.DefaultTransport}),
    ),
)
```

Regarding **"per-request" headers**:

* The Agent/Runner will propagate `ctx` to the model call; middleware can read the value from `req.Context()` to inject headers for "this call."
* For **message completion**, the current API doesn't expose per-call BaseURL overrides; if switching is needed, create a model using a different BaseURL or modify the `r.URL` in middleware.

#### 3. Token Tailoring

Anthropic models also support Token Tailoring functionality, designed to automatically trim messages when they exceed the model's context window limits, ensuring requests can be successfully sent to the LLM API.

**Automatic Mode (Recommended)**:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
)

// Enable token tailoring with automatic configuration
model := anthropic.New("claude-3-5-sonnet",
    anthropic.WithEnableTokenTailoring(true),
)
```

**Advanced Mode**:

```go
// Custom token limit and strategy
model := anthropic.New("claude-3-5-sonnet",
    anthropic.WithEnableTokenTailoring(true),               // Required: enable token tailoring
    anthropic.WithMaxInputTokens(10000),                    // Custom token limit
    anthropic.WithTokenCounter(customCounter),              // Optional: custom counter
    anthropic.WithTailoringStrategy(customStrategy),        // Optional: custom strategy
)
```

For detailed explanations of the token calculation formula, tailoring strategy, and custom strategy implementation, please refer to [Token Tailoring under OpenAI Model](#5-token-tailoring).

### 3. Provider

With the emergence of multiple large model providers, some have defined their own API specifications. Currently, the framework has integrated the APIs of OpenAI and Anthropic, and exposes them as models. Users can access different provider models through `openai.New` and `anthropic.New`.

However, there are differences in instantiation and configuration between providers, which often requires developers to modify a significant amount of code when switching between providers, increasing the cost of switching.

To solve this problem, the Provider offers a unified model instantiation entry point. Developers only need to specify the provider and model name, and other configuration options are managed through the unified `Option`, simplifying the complexity of switching between providers.

The Provider supports the following `Option`:

| Option                                                                                            | Description                                                             |
| ------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `WithAPIKey` / `WithBaseURL`                                                                      | Set the API Key and Base URL for the model                              |
| `WithHTTPClientName` / `WithHTTPClientTransport`                                                  | Configure HTTP client properties                                        |
| `WithChannelBufferSize`                                                                           | Adjust the response channel buffer size                                 |
| `WithCallbacks`                                                                                   | Configure OpenAI / Anthropic request, response, and streaming callbacks |
| `WithExtraFields`                                                                                 | Configure custom fields in the request body                             |
| `WithEnableTokenTailoring` / `WithMaxInputTokens`<br>`WithTokenCounter` / `WithTailoringStrategy` | Token trimming related parameters                                       |
| `WithOpenAI` / `WithAnthropic`                                                                    | Pass-through native options for the respective providers                |

#### Usage Example

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/provider"
)

providerName := "openai"        // provider supports openai and anthropic.
modelName := "deepseek-chat"

modelInstance, err := provider.Model(
    providerName,
    modelName,
    provider.WithAPIKey(c.apiKey),
    provider.WithBaseURL(c.baseURL),
    provider.WithChannelBufferSize(c.channelBufferSize),
    provider.WithEnableTokenTailoring(c.tokenTailoring),
    provider.WithMaxInputTokens(c.maxInputTokens),
)

agent := llmagent.New("chat-assistant", llmagent.WithModel(modelInstance))
```

Full code can be found in [examples/provider](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/provider).

#### Registering a Custom Provider

The framework supports registering custom providers to integrate other large model providers or custom model implementations.

Using `provider.Register`, you can define a method to create custom model instances based on the options.

```go
import "trpc.group/trpc-go/trpc-agent-go/model/provider"

provider.Register("custom-provider", func(opts *provider.Options) (model.Model, error) {
    return newCustomModel(opts.ModelName, WithAPIKey(opts.APIKey)), nil
})

customModel, err := provider.Model("custom-provider", "custom-model")
```
