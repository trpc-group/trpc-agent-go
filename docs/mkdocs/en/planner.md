# Planner Usage Guide

Planner is a component for implementing planning capabilities for Agents. It allows an Agent to formulate a plan before executing tasks, thereby improving execution efficiency and accuracy.

The framework provides two Planner implementations, each suited for different types of models:

- BuiltinPlanner: Suitable for models that support native reasoning/thinking.
- ReActPlanner: Suitable for models that do not support native reasoning, guiding the model to output in a fixed, labeled format to provide a structured reasoning process.

## Planner Interface

The Planner interface defines the methods that all planners must implement:

```go
type Planner interface {
    // BuildPlanningInstruction applies necessary configurations to the LLM request and constructs the system instruction to be attached for planning.
    // Return an empty string if no instruction is needed.
    BuildPlanningInstruction(
        ctx context.Context,
        invocation *agent.Invocation,
        llmRequest *model.Request,
    ) string

    // ProcessPlanningResponse processes the LLM's planning response and returns the processed response.
    // Return nil if no processing is needed.
    ProcessPlanningResponse(
        ctx context.Context,
        invocation *agent.Invocation,
        response *model.Response,
    ) *model.Response
}
```

Planner workflow:

1. Request processing phase: Before the LLM request is sent, the Planner adds planning instructions or configurations via `BuildPlanningInstruction`.
2. Response processing phase: The Planner processes the LLM response and organizes the content structure via `ProcessPlanningResponse`.

## Enable and Capture Thinking (without Planner)

You can enable and read a model's internal reasoning ("thinking") even without using a Planner. 
Configure GenerationConfig to request thinking, and then capture ReasoningContent from responses.

### Enable via GenerationConfig
```go
genConfig := model.GenerationConfig{
    Stream:      true,  // streaming or non-streaming
    MaxTokens:   2000,
    Temperature: 0.7,
}

// Enable thinking (if provider/model supports it).
thinkingEnabled := true
thinkingTokens := 2048
genConfig.ThinkingEnabled = &thinkingEnabled
genConfig.ThinkingTokens  = &thinkingTokens
```

Attach genConfig to your Agent when constructing it.

### Capture reasoning from responses
- Streaming: read `choice.Delta.ReasoningContent` (reasoning) and `choice.Delta.Content` (visible text).
- Non-streaming: read `choice.Message.ReasoningContent` and `choice.Message.Content`.

```go
eventChan, err := runner.Run(ctx, userID, sessionID, model.NewUserMessage("Question"))
if err != nil { /* handle */ }

for e := range eventChan {
    if e.Error != nil {
        fmt.Printf("Error: %s\n", e.Error.Message)
        continue
    }
    if len(e.Response.Choices) == 0 {
        continue
    }
    ch := e.Response.Choices[0]

    // Dim-print reasoning if present.
    if genConfig.Stream {
        if rc := ch.Delta.ReasoningContent; rc != "" {
            fmt.Printf("\x1b[2m%s\x1b[0m", rc)
        }
        if content := ch.Delta.Content; content != "" {
            fmt.Print(content)
        }
    } else {
        if rc := ch.Message.ReasoningContent; rc != "" {
            fmt.Printf("\x1b[2m%s\x1b[0m\n", rc)
        }
        if content := ch.Message.Content; content != "" {
            fmt.Println(content)
        }
    }

    if e.IsFinalResponse() {
        fmt.Println()
        break
    }
}
```

Notes:
- The visibility of reasoning depends on the model and provider; enabling related parameters expresses intent and does not guarantee it will be returned.
- In streaming mode, you may insert a blank line between reasoning and normal content to improve the reading experience.
- Session history may aggregate segmented reasoning into the final message to aid later review and traceability.

## BuiltinPlanner

BuiltinPlanner is suitable for models that support native reasoning. It does not generate explicit planning instructions, but instead configures the model to use its internal reasoning mechanisms to implement planning.

Model configuration:

```go
type Options struct {
    // ReasoningEffort limits the reasoning effort of the reasoning model.
    // Supported values: "low", "medium", "high".
    // Only effective for OpenAI o-series models.
    ReasoningEffort *string
    // ThinkingEnabled enables thinking mode for models that support it.
    // Only effective for Claude and Gemini models via OpenAI API.
    ThinkingEnabled *bool
    // ThinkingTokens controls the length of thinking.
    // Only effective for Claude and Gemini models via OpenAI API.
    ThinkingTokens *int
}
```

Implementation details for BuiltinPlanner:

- `BuildPlanningInstruction`: Injects reasoning parameters into the LLM request. Since the model supports native thinking, no planning tags are required, so it returns an empty string.
- `ProcessPlanningResponse`: Since the model's response already contains the planning process, it directly returns nil.

Example:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/planner/builtin"
)

// Create model instance.
modelInstance := openai.New("gpt-4o-mini")

// Create BuiltinPlanner.
reasoningEffort := "high"
planner := builtin.New(builtin.Options{
    ReasoningEffort: &reasoningEffort,
})

// Create LLMAgent and configure Planner.
llmAgent := llmagent.New(
    "demo-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A helpful AI assistant with built-in planning"),
    llmagent.WithInstruction("Be helpful and think through problems carefully"),
    llmagent.WithPlanner(planner), // Configure Planner.
)
```

## ReActPlanner

ReActPlanner is suitable for models that do not support native reasoning. It guides the LLM to follow a specific format and uses specific tags to structure planning, reasoning, actions, and the final answer, thus enabling a structured reasoning process.

ReActPlanner uses the following specific tags to organize response content:

1. Planning phase (`/*PLANNING*/`): Create a clear plan to answer the user's question.
2. Reasoning phase (`/*REASONING*/`): Provide reasoning between tool executions.
3. Action phase (`/*ACTION*/`): Execute tools according to the plan.
4. Replanning (`/*REPLANNING*/`): Revise the plan as needed based on results.
5. Final answer (`/*FINAL_ANSWER*/`): Provide a comprehensive answer.

Implementation details for ReActPlanner:

- `BuildPlanningInstruction`: Returns a comprehensive instruction that includes high-level guidance, planning requirements, reasoning requirements, etc., guiding the model to output using labeled format.
- `ProcessPlanningResponse`: Filters tool calls with empty names. If the content contains the `/*FINAL_ANSWER*/` tag, only the final answer part is retained; otherwise, the original content is returned, separating planning content from the final answer.

Usage example:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/planner/react"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Create model instance.
modelInstance := openai.New("gpt-4o-mini")

// Create tool.
searchTool := function.NewFunctionTool(
    searchFunction,
    function.WithName("search"),
    function.WithDescription("Search for information on a given topic"),
)

// Create ReActPlanner.
planner := react.New()

// Create LLMAgent and configure Planner.
llmAgent := llmagent.New(
    "react-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An AI assistant that uses structured planning"),
    llmagent.WithInstruction("Follow a structured approach to solve problems"),
    llmagent.WithPlanner(planner), // Configure Planner.
    llmagent.WithTools([]tool.Tool{searchTool}), // Configure tools.
)
```

For complete code examples, please refer to [examples/react](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/react).

## Custom Planner

In addition to the two Planner implementations provided by the framework, you can also create a custom Planner by implementing the `Planner` interface to meet specific needs:

```go
type customPlanner struct {
    // Custom configuration.
}

func (p *customPlanner) BuildPlanningInstruction(
    ctx context.Context,
    invocation *agent.Invocation,
    llmRequest *model.Request,
) string {
    // Return custom planning instruction.
    return "Your custom planning instruction"
}

func (p *customPlanner) ProcessPlanningResponse(
    ctx context.Context,
    invocation *agent.Invocation,
    response *model.Response,
) *model.Response {
    // Process response.
    return response
}

// Create LLMAgent and configure custom Planner.
llmAgent := llmagent.New(
    "react-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An AI assistant that uses structured planning"),
    llmagent.WithInstruction("Follow a structured approach to solve problems"),
    llmagent.WithPlanner(&customPlanner{}),      // Configure Planner.
    llmagent.WithTools([]tool.Tool{searchTool}), // Configure tools.
)
```
