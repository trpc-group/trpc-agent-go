# Dify Agent Examples

This directory contains comprehensive examples demonstrating how to use the `difyagent` package to integrate with Dify workflows and chatflows.

## Prerequisites

1. **Dify Account**: You need access to a Dify instance (cloud or self-hosted)
2. **API Secret**: Get your API secret from your Dify application
3. **Go Environment**: Go 1.19 or later

## Environment Setup

Before running any examples, set up the required environment variables:

```bash
export DIFY_BASE_URL="https://api.dify.ai/v1"  # or your self-hosted URL
export DIFY_API_SECRET="your-dify-api-secret"
```

## Examples Overview

### 1. Basic Chat (`basic_chat/`)

**Purpose**: Demonstrates simple non-streaming chat interaction with Dify.

**Key Features**:
- Basic Dify agent setup
- Non-streaming responses
- Simple conversation flow
- Error handling

**Run**:
```bash
cd basic_chat
go run main.go
```

**Expected Output**:
```
🤖 Starting Dify Chat Example
==================================================

👤 User: Hello! Can you introduce yourself?
🤖 Assistant: Hello! I'm an AI assistant powered by Dify...

👤 User: What can you help me with?
🤖 Assistant: I can help you with various tasks...
```

### 2. Streaming Chat (`streaming_chat/`)

**Purpose**: Shows real-time streaming responses from Dify.

**Key Features**:
- Streaming response handling
- Custom streaming handler
- Real-time content display
- Response metrics (speed, chunks, etc.)

**Run**:
```bash
cd streaming_chat
go run main.go
```

**Expected Output**:
```
🚀 Starting Dify Streaming Chat Example
============================================================

👤 User: Please write a short story about a robot learning to paint
🤖 Assistant: Once upon a time, in a small workshop...
[content streams in real-time]

📊 Response Stats:
   • Duration: 2.3s
   • Chunks: 15
   • Characters: 287
   • Speed: 124.8 chars/sec
```

### 3. Advanced Usage (`advanced_usage/`)

**Purpose**: Demonstrates advanced features like custom converters and state management.

**Key Features**:
- Custom event converter with metadata
- Custom request converter with user preferences
- State transfer between sessions
- Dynamic conversation context
- User preference handling

**Run**:
```bash
cd advanced_usage
go run main.go
```

**Expected Output**:
```
🔧 Starting Dify Advanced Usage Example
============================================================

📋 Scenario 1: Expert user asking about quantum computing
👤 User: Explain quantum computing
⚙️  User Preferences:
   • user_language: en
   • response_tone: professional
   • expertise_level: expert
   • response_format: detailed
🤖 Assistant: [Dify:conv-123] Quantum computing is a revolutionary...

📊 Event Metadata:
   • dify_conversation_id: conv-123
   • dify_message_id: msg-456
   • custom_processed: true
```

## Configuration Options

### Basic Configuration

```go
difyAgent, err := difyagent.New(
    difyagent.WithBaseUrl("https://api.dify.ai/v1"),
    difyagent.WithName("my-assistant"),
    difyagent.WithDescription("My custom assistant"),
)
```

### Streaming Configuration

```go
difyAgent, err := difyagent.New(
    difyagent.WithBaseUrl(difyBaseURL),
    difyagent.WithEnableStreaming(true),
    difyagent.WithStreamingChannelBufSize(2048),
    difyagent.WithStreamingRespHandler(customHandler),
)
```

### Custom Client Configuration

The `WithGetDifyClientFunc` option allows you to customize the Dify client creation for each invocation. This is particularly useful when you need:

- **Dynamic API Keys**: Different API secrets per user or session
- **Custom Timeouts**: Varying timeout settings based on request type
- **Multi-tenant Setup**: Different Dify instances per organization
- **Advanced Authentication**: Custom headers or authentication methods
- **Load Balancing**: Dynamic endpoint selection

```go
difyAgent, err := difyagent.New(
    // Custom client function - called for each invocation
    difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
        // Access invocation context for dynamic configuration
        userID := invocation.Session.UserID
        
        // Example: Use different API keys per user
        apiSecret := getUserAPISecret(userID)
        
        // Example: Custom timeout based on user tier
        timeout := getUserTimeout(userID)
        
        return dify.NewClientWithConfig(&dify.ClientConfig{
            Host:             baseURL,     // Can be dynamic too
            DefaultAPISecret: apiSecret,   // Per-user API secret
            Timeout:          timeout,     // Custom timeout
            // Add custom headers if needed
            // Headers: map[string]string{"X-User-ID": userID},
        }), nil
    }),
)
```

**Note**: If `WithGetDifyClientFunc` is not provided, the agent will create a default client using the base configuration.

## Custom Converters

### Event Converter

Customize how Dify responses are converted to events:

```go
type CustomEventConverter struct{}

func (c *CustomEventConverter) ConvertToEvent(
    resp *dify.ChatMessageResponse,
    agentName string,
    invocation *agent.Invocation,
) *event.Event {
    // Custom conversion logic
    return event
}
```

### Request Converter

Customize how invocations are converted to Dify requests:

```go
type CustomRequestConverter struct{}

func (c *CustomRequestConverter) ConvertToDifyRequest(
    ctx context.Context,
    invocation *agent.Invocation,
    isStream bool,
) (*dify.ChatMessageRequest, error) {
    // Custom request building logic
    return request, nil
}
```

## State Management

Transfer session state to Dify inputs:

```go
difyAgent, err := difyagent.New(
    difyagent.WithTransferStateKey("user_language", "user_preferences"),
)

// Use with runtime state
events, err := runner.Run(
    ctx, userID, sessionID,
    model.NewUserMessage("Hello"),
    agent.WithRuntimeState(map[string]any{
        "user_language": "en",
        "user_preferences": "detailed_responses",
    }),
)
```

## Error Handling

### Common Errors

1. **Missing API Secret**:
   ```
   DIFY_API_SECRET environment variable is required
   ```
   Solution: Set the environment variable

2. **Connection Timeout**:
   ```
   context deadline exceeded
   ```
   Solution: Increase timeout or check network connectivity

3. **Invalid API Secret**:
   ```
   401 Unauthorized
   ```
   Solution: Verify your API secret is correct

### Error Handling in Code

```go
events, err := runner.Run(ctx, userID, sessionID, message)
if err != nil {
    log.Printf("Runner error: %v", err)
    return
}

for event := range events {
    if event.Error != nil {
        log.Printf("Event error: %s", event.Error.Message)
        continue
    }
    // Process successful event
}
```

## Best Practices

### 1. Resource Management

```go
// Use context with timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
```

### 2. Session Management

```go
// Use consistent session IDs for conversation continuity
sessionID := fmt.Sprintf("user-%s-session-%d", userID, time.Now().Unix())
```

### 3. Error Recovery

```go
// Implement retry logic for transient errors
for attempts := 0; attempts < 3; attempts++ {
    events, err := runner.Run(ctx, userID, sessionID, message)
    if err == nil {
        break
    }
    time.Sleep(time.Duration(attempts+1) * time.Second)
}
```

### 4. Streaming Optimization

```go
// Use appropriate buffer sizes for streaming
difyagent.WithStreamingChannelBufSize(2048)

// Handle streaming gracefully
streamingHandler := func(resp *model.Response) (string, error) {
    if len(resp.Choices) > 0 {
        content := resp.Choices[0].Delta.Content
        // Process content immediately
        fmt.Print(content)
        return content, nil
    }
    return "", nil
}
```

## Testing

Each example includes basic error handling and logging. For production use, consider:

1. **Unit Testing**: Test your custom converters
2. **Integration Testing**: Test with actual Dify endpoints
3. **Load Testing**: Test streaming performance
4. **Error Scenarios**: Test network failures, timeouts, etc.

## Troubleshooting

### Debug Mode

Enable debug logging to see detailed request/response information:

```go
// Add debug logging to your Dify client configuration
client := dify.NewClientWithConfig(&dify.ClientConfig{
    Host:             baseURL,
    DefaultAPISecret: apiSecret,
    Debug:           true,  // Enable debug mode
})
```

### Common Issues

1. **Slow Responses**: Check your Dify workflow complexity
2. **Memory Issues**: Adjust streaming buffer sizes
3. **Connection Issues**: Verify network and firewall settings
4. **Rate Limiting**: Implement proper retry logic with backoff

## Next Steps

After running these examples:

1. **Customize**: Modify the examples for your specific use case
2. **Integrate**: Incorporate into your application
3. **Scale**: Consider load balancing and caching strategies
4. **Monitor**: Add metrics and logging for production use

## Support

For issues specific to:
- **Dify Integration**: Check Dify documentation
- **trpc-agent-go**: Check the main repository documentation
- **Examples**: Create issues in the trpc-agent-go repository