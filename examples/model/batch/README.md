# Model Batch Example

This example demonstrates how to use the OpenAI Batch API with the OpenAI-like model implementation in trpc-agent-go framework.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Overview

The example shows how to use the OpenAI Batch API for processing multiple requests asynchronously. It supports creating, retrieving, canceling, and listing batch jobs, with flexible input methods for batch requests.

## Key Features

1. **Batch Operations**: Create, retrieve, cancel, and list batch jobs
2. **Flexible Input**: Support both inline requests and file-based requests
3. **Environment Variable Support**: Automatically reads `OPENAI_API_KEY` and `OPENAI_BASE_URL`
4. **Command Line Configuration**: Use flags to configure batch operations
5. **Comprehensive Error Handling**: Robust error handling for various scenarios
6. **Detailed Output**: Rich information display for batch status and results

## Environment Variables

The example supports the following environment variables:

| Variable          | Description                                                                | Default Value               |
| ----------------- | -------------------------------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required, automatically read by OpenAI SDK) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK)     | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code. The SDK handles this automatically when creating the client.

## Command Line Arguments

| Argument    | Description                                  | Default Value |
| ----------- | -------------------------------------------- | ------------- |
| `-action`   | Action to perform: create\|get\|cancel\|list | `list`        |
| `-model`    | Name of the model to use                     | `gpt-4o-mini` |
| `-requests` | Inline requests specification                | ``            |
| `-file`     | Path to requests specification file          | ``            |
| `-id`       | Batch ID for get/cancel operations           | ``            |
| `-after`    | Pagination cursor for listing batches        | ``            |
| `-limit`    | Max number of batches to list (1-100)        | `5`           |

## How It Works

The batch API works by:

1. **Request Preparation**: Parse user-specified requests into batch format
2. **File Upload**: Upload batch input file to OpenAI/backend
3. **Batch Creation**: Create batch job with uploaded file
4. **Status Monitoring**: Track batch progress and completion
5. **Result Retrieval**: Download and parse batch results

## Request Specification Format

The example supports a simple text format for specifying batch requests:

```
role: message || role: message /// role: message || role: message
```

- **Separators**: `///` separates different requests, `||` separates messages within a request
- **Message Format**: `role: content` where role can be `system`, `user`, or `assistant`
- **Examples**:
  ```
  system: You are a helpful assistant. || user: Hello /// system: You are a helpful assistant. || user: How are you?
  ```

## Running the Example

### List existing batches (default action):

```bash
cd examples/model/batch
go run main.go
```

### Create a new batch with inline requests:

```bash
cd examples/model/batch
go run main.go -action create -requests "system: You are a helpful assistant. || user: Hello /// system: You are a helpful assistant. || user: How are you?"
```

### Create a batch from a file:

```bash
cd examples/model/batch
# Create a requests.txt file with your requests
echo "system: You are a helpful assistant. || user: Hello /// system: You are a helpful assistant. || user: How are you?" > requests.txt
go run main.go -action create -file requests.txt
```

### Get batch details:

```bash
cd examples/model/batch
go run main.go -action get -id batch_abc123
```

### Cancel a batch:

```bash
cd examples/model/batch
go run main.go -action cancel -id batch_abc123
```

### List batches with pagination:

```bash
cd examples/model/batch
go run main.go -action list -limit 10 -after batch_abc123
```

### Using custom environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"

cd examples/model/batch
go run main.go -action create -requests "user: Hello"
```

## Example Output

### Batch Creation

```
🚀 Using configuration:
   📝 Model Name: gpt-4o-mini
   🎛️  Action: create
   🔑 OpenAI SDK reads OPENAI_API_KEY and OPENAI_BASE_URL from env

🆕 Batch created.
   🆔 ID: batch_abc123
   🔗 Endpoint: /v1/chat/completions
   🕐 Created: 2025-01-27T10:30:00Z
   🧭 Status: validating
   📥 Input File: file_abc123
   📊 Requests: total=2 ok=0 fail=0
🎉 Done.
```

### Batch Listing

```
📃 Listing up to 5 batches (after=).
 1. id=batch_abc123 status=completed created=2025-01-27T10:30:00Z requests(total=2,ok=2,fail=0)
     📤 Output: file_def456
     ⏰ Window: 24h
     ✅ Completed: 2025-01-27T10:35:00Z

 2. id=batch_xyz789 status=validating created=2025-01-27T10:40:00Z requests(total=1,ok=0,fail=0)
     ⏰ Window: 24h
🎉 Done.
```

## Package Usage

The example demonstrates the batch API usage:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create a new model instance
llm := openai.New("gpt-4o-mini")

// Create batch requests
requests := []*openai.BatchRequestInput{
    {
        CustomID: "1",
        Method:   "POST",
        URL:      "/v1/chat/completions",
        Body: openai.BatchRequest{
            Messages: []model.Message{
                model.NewSystemMessage("You are a helpful assistant."),
                model.NewUserMessage("Hello"),
            },
        },
    },
}

// Create batch
batch, err := llm.CreateBatch(ctx, requests,
    openai.WithBatchCreateCompletionWindow("24h"),
)
```

## Batch Operations

### Create Batch

Creates a new batch job with the specified requests:

```go
batch, err := llm.CreateBatch(ctx, requests, opts...)
```

### Retrieve Batch

Gets detailed information about a specific batch:

```go
batch, err := llm.RetrieveBatch(ctx, batchID)
```

### Cancel Batch

Cancels a running batch job:

```go
batch, err := llm.CancelBatch(ctx, batchID)
```

### List Batches

Lists batches with pagination support:

```go
page, err := llm.ListBatches(ctx, after, limit)
```

## Error Handling

The example includes comprehensive error handling for:

- Missing API keys
- Invalid request specifications
- File upload failures
- Batch creation errors
- Network timeouts
- Invalid batch IDs

## Security Notes

- Never commit API keys to version control
- Use environment variables or secure configuration management
- The default API key in the example is for demonstration only

## Important Notes

- **Asynchronous Processing**: Batch jobs are processed asynchronously
- **File-based Input**: Batch requests are uploaded as JSONL files
- **Completion Windows**: Set appropriate completion windows for your use case
- **Request Limits**: Be aware of backend-specific request limits
- **Environment Variables**: API key and base URL are automatically read from environment

## Benefits

1. **Efficient Processing**: Process multiple requests in a single batch
2. **Cost Optimization**: Often more cost-effective than individual requests
3. **Flexible Input**: Support both inline and file-based request specifications
4. **Comprehensive Monitoring**: Track batch progress and retrieve detailed results
5. **Error Handling**: Robust error handling for production use
6. **Professional Structure**: Clean, maintainable code structure

## Backend Compatibility

The example works with:

- **OpenAI API**: Official OpenAI batch endpoints
- **Venus Platform**: OpenAI-compatible batch API
- **Other Backends**: Any OpenAI-compatible service via `OPENAI_BASE_URL`

## Troubleshooting

### Common Issues

1. **File Upload Errors**: Ensure proper file format and size limits
2. **Batch Creation Failures**: Check request format and backend compatibility
3. **Timeout Issues**: Adjust completion window and timeout settings
4. **Authentication Errors**: Verify API key and base URL configuration

### Debug Information

The example provides detailed output including:

- Batch status and progress
- Request counts and completion status
- Error details and file IDs
- Timestamps for various stages
