# Model Switching Example

This example demonstrates how to use the dynamic model switching functionality in trpc-agent-go, allowing you to switch between different LLM models during a conversation.

## Prerequisites

Make sure you have Go installed and the project dependencies are available.

## Overview

The example shows how to create an LLM agent with multiple models and dynamically switch between them during execution. This is useful for scenarios where you want to use different models for different types of tasks or when you need to fallback to alternative models.

## Key Features

1. **Multi-Model Support**: Create an agent with multiple models
2. **Dynamic Switching**: Switch between models during runtime
3. **Model Information**: Display detailed information about available models
4. **Active Model Tracking**: Monitor which model is currently active
5. **Comprehensive Examples**: Various switching scenarios and use cases

## Environment Variables

The example supports the following environment variables:

| Variable          | Description                                                                | Default Value               |
| ----------------- | -------------------------------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`  | API key for the model service (required, automatically read by OpenAI SDK) | ``                          |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint (automatically read by OpenAI SDK)     | `https://api.openai.com/v1` |

**Note**: `OPENAI_API_KEY` and `OPENAI_BASE_URL` are automatically read by the OpenAI SDK. You don't need to manually read these environment variables in your code.

## Command Line Arguments

| Argument | Description          | Default Value |
| -------- | -------------------- | ------------- |
| `-model` | Default model to use | `gpt-4o-mini` |

## How It Works

The model switching implementation works by:

1. **Multi-Model Setup**: Create an agent with multiple models using `WithModels`
2. **Model Manager**: Internal manager handles model registration and switching
3. **Dynamic Switching**: Switch between models using `SwitchModel` method
4. **Active Model Tracking**: Always know which model is currently active
5. **Seamless Execution**: Continue conversation with different models

## Running the Example

### Using default values:

```bash
cd examples/model/switching
go run main.go
```

### Using custom default model:

```bash
cd examples/model/switching
go run main.go -model gpt-4o
```

### Using custom environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"

cd examples/model/switching
go run main.go -model gpt-4o-mini
```

## Example Output

The example will run several demonstrations:

1. **🔧 Initial Setup**: Shows agent creation with multiple models
2. **📋 Model Information**: Lists all available models and their details
3. **🔄 Model Switching**: Demonstrates switching between different models
4. **💬 Conversation Examples**: Shows conversations with different models
5. **📊 Performance Comparison**: Compares responses from different models

## Package Usage

The example demonstrates the model switching functionality:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create multiple models
defaultModel := openai.New("gpt-4o-mini")
model1 := openai.New("gpt-4o")
model2 := openai.New("gpt-3.5-turbo")

// Create agent with multiple models
agent := llmagent.New("multi-model-agent",
    llmagent.WithModels(defaultModel, model1, model2))

// Switch to a different model
err := agent.SwitchModel("gpt-4o")

// Get current active model
activeModel := agent.ActiveModel()

// List all available models
allModels := agent.Models()
```

## Model Switching Examples

### Basic Model Switching

```go
// Create agent with multiple models
agent := llmagent.New("agent",
    llmagent.WithModels(
        openai.New("gpt-4o-mini"),  // default
        openai.New("gpt-4o"),       // additional
        openai.New("gpt-3.5-turbo") // additional
    ))

// Switch to a specific model
err := agent.SwitchModel("gpt-4o")
if err != nil {
    log.Printf("Failed to switch model: %v", err)
}

// Verify the switch
currentModel := agent.ActiveModel()
fmt.Printf("Current model: %s\n", currentModel.Info().Name)
```

### Model Fallback Strategy

```go
// Try primary model first
err := agent.SwitchModel("gpt-4o")
if err != nil {
    // Fallback to secondary model
    agent.SwitchModel("gpt-4o-mini")
}

// Continue with available model
response := agent.Run(ctx, request)
```

## Advanced Features

### Model Information Display

```go
// Get detailed information about all models
models := agent.Models()
for _, m := range models {
    info := m.Info()
    fmt.Printf("Model: %s\n", info.Name)
    fmt.Println("---")
}
```

### Active Model Monitoring

```go
// Always know which model is active
activeModel := agent.ActiveModel()
fmt.Printf("Active model: %s\n", activeModel.Info().Name)

// Check if specific model is available
availableModels := agent.Models()
modelNames := make([]string, len(availableModels))
for i, m := range availableModels {
    modelNames[i] = m.Info().Name
}
fmt.Printf("Available models: %v\n", modelNames)
```

## Error Handling

The example includes comprehensive error handling for:

- Model switching failures
- Invalid model names
- Model availability issues
- API errors during switching
- Fallback strategies

## Security Notes

- Never commit API keys to version control
- Use environment variables or secure configuration management
- The default API key in the example is for demonstration only

## Important Notes

- **Model Registration**: All models must be registered during agent creation
- **Switching Validation**: Model names must match exactly (case-sensitive)
- **State Persistence**: Model selection persists until changed
- **Fallback Support**: Always have a default model available
- **Performance**: Switching is fast and doesn't affect ongoing operations

## Benefits

1. **Flexibility**: Use different models for different tasks
2. **Cost Optimization**: Switch to cheaper models for simple tasks
3. **Performance**: Use faster models for time-sensitive operations
4. **Quality**: Use more capable models for complex tasks
5. **Reliability**: Fallback to alternative models if needed
6. **Seamless Experience**: Switch models without interrupting conversation

## Use Cases

- **Content Creation**: Use creative models for writing, efficient models for summaries
- **Code Generation**: Use specialized models for different programming languages
- **Customer Support**: Use fast models for simple queries, advanced models for complex issues
- **Research**: Use different models for different types of analysis
- **Fallback Strategy**: Automatically switch to alternative models on errors
