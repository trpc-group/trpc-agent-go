## Planner User Guide

`Planner` is the component that enables planning capabilities for an `Agent`. It allows an `Agent` to formulate a plan before executing tasks, improving both efficiency and accuracy.

The framework provides two `Planner` implementations for different model types:

- `BuiltinPlanner`: for models that natively support internal reasoning/thinking
- `ReActPlanner`: for models without native thinking, guiding them to output in a tagged, structured format

### Planner Interface

The `Planner` interface defines the methods that all planners must implement:

```go
type Planner interface {
    // BuildPlanningInstruction applies necessary configuration to the LLM request
    // and returns the system instruction to attach for planning.
    // Return an empty string if no instruction is needed.
    BuildPlanningInstruction(
        ctx context.Context,
        invocation *agent.Invocation,
        llmRequest *model.Request,
    ) string

    // ProcessPlanningResponse processes the LLM planning response and returns the processed response.
    // Return nil if no processing is needed.
    ProcessPlanningResponse(
        ctx context.Context,
        invocation *agent.Invocation,
        response *model.Response,
    ) *model.Response
}
```

Planner workflow:

1. Request phase: before sending the LLM request, `BuildPlanningInstruction` adds planning instructions or applies configuration
2. Response phase: `ProcessPlanningResponse` organizes the LLM response content and structure

### BuiltinPlanner

`BuiltinPlanner` targets models that support native thinking. It does not generate explicit planning instructions. Instead, it configures the model to leverage its internal reasoning mechanism to achieve planning.

Model configuration:

```go
type Options struct {
    // ReasoningEffort constrains the reasoning intensity of reasoning-enabled models.
    // Supported values: "low", "medium", "high".
    // Only effective for OpenAI o-series models.
    ReasoningEffort *string
    // ThinkingEnabled enables thinking mode for models that support it.
    // Only effective for Claude and Gemini models via OpenAI-compatible API.
    ThinkingEnabled *bool
    // ThinkingTokens controls the length of the thinking process.
    // Only effective for Claude and Gemini models via OpenAI-compatible API.
    ThinkingTokens *int
}
```

Implementation details:

- `BuildPlanningInstruction`: applies thinking parameters to the LLM request. Since the model supports native reasoning, no planning tags are needed, so return an empty string
- `ProcessPlanningResponse`: returns nil because the model's response already includes its planning process

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

// Create LLMAgent and configure the Planner.
llmAgent := llmagent.New(
    "demo-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("A helpful AI assistant with built-in planning"),
    llmagent.WithInstruction("Be helpful and think through problems carefully"),
    llmagent.WithPlanner(planner), // Configure Planner.
)
```

### ReActPlanner

`ReActPlanner` targets models that do not support native thinking. It guides the LLM to follow a specific, tagged format to structure planning, reasoning, actions, and the final answer, thereby achieving a structured thinking process.

ReActPlanner uses the following tags to organize response content:

1. Planning phase (`/*PLANNING*/`): create a clear plan to answer the user's question
2. Reasoning phase (`/*REASONING*/`): provide reasoning between tool executions
3. Action phase (`/*ACTION*/`): execute tools based on the plan
4. Re-planning (`/*REPLANNING*/`): revise the plan based on results when needed
5. Final answer (`/*FINAL_ANSWER*/`): provide the synthesized answer

Implementation details:

- `BuildPlanningInstruction`: returns comprehensive instructions containing high-level guidance, planning requirements, and reasoning requirements, prompting the model to output in the tagged format
- `ProcessPlanningResponse`: filters out tool calls with empty names; if the content contains the `/*FINAL_ANSWER*/` tag, keep only the final answer section; otherwise, return the original content, separating planning content from the final answer

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

// Create LLMAgent and configure the Planner.
llmAgent := llmagent.New(
    "react-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An AI assistant that uses structured planning"),
    llmagent.WithInstruction("Follow a structured approach to solve problems"),
    llmagent.WithPlanner(planner), // Configure Planner.
    llmagent.WithTools([]tool.Tool{searchTool}), // Configure tools.
)
```

See the full example at [examples/react](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/react).

### Custom Planner

Besides the two built-in `Planner` implementations, you can implement the `Planner` interface to create a custom `Planner` for specific needs:

```go
type customPlanner struct {
    // Custom configuration.
}

func (p *customPlanner) BuildPlanningInstruction(
    ctx context.Context,
    invocation *agent.Invocation,
    llmRequest *model.Request,
) string {
    // Return your custom planning instruction.
    return "your custom planning instruction"
}

func (p *customPlanner) ProcessPlanningResponse(
    ctx context.Context,
    invocation *agent.Invocation,
    response *model.Response,
) *model.Response {
    // Process the response.
    return response
}

// Create LLMAgent and configure the custom Planner.
llmAgent := llmagent.New(
    "react-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("An AI assistant that uses structured planning"),
    llmagent.WithInstruction("Follow a structured approach to solve problems"),
    llmagent.WithPlanner(&customPlanner{}),      // Configure Planner.
    llmagent.WithTools([]tool.Tool{searchTool}), // Configure tools.
)
```
