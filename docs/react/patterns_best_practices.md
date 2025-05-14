# ReAct Patterns and Best Practices

This guide provides comprehensive patterns and best practices for implementing effective ReAct agents using the trpc-agent-go framework.

## Table of Contents

1. [Introduction to ReAct](#introduction-to-react)
2. [Core ReAct Patterns](#core-react-patterns)
3. [Tool Design Patterns](#tool-design-patterns)
4. [Memory Utilization Patterns](#memory-utilization-patterns)
5. [Error Handling Patterns](#error-handling-patterns)
6. [Integration Patterns](#integration-patterns)
7. [Best Practices](#best-practices)

## Introduction to ReAct

ReAct (Reasoning and Acting) is a framework that combines reasoning traces with action steps, enabling agents to:

- Think through complex problems step by step
- Take actions to gather necessary information
- Observe results and refine their approach
- Provide transparent reasoning traces

The cycle follows a **Thought-Action-Observation** pattern that repeats until reaching a final answer.

### When to Use ReAct

ReAct is particularly effective for:

- Multi-step problem solving that requires both reasoning and information gathering
- Tasks where the agent needs to change approaches based on observations
- Scenarios where reasoning transparency is important
- Complex planning and execution that requires adaptation

## Core ReAct Patterns

### 1. Thought Expansion Pattern

The **Thought Expansion** pattern involves encouraging the agent to explore multiple possibilities before taking action.

```go
// When configuring your ReAct agent's system prompt:
systemPrompt := `When reasoning, consider multiple angles:
1. What information do I have?
2. What information do I need?
3. What assumptions am I making?
4. What alternative approaches exist?
5. What are the potential errors or challenges?

Only after exploring these angles, decide on the best action to take.`
```

### 2. Step-by-Step Reasoning Pattern

The **Step-by-Step Reasoning** pattern helps the agent break down complex problems into manageable steps.

```go
// When configuring your ReAct agent's system prompt:
systemPrompt := `When solving complex problems:
1. Break down the problem into smaller steps
2. Solve each step sequentially
3. Verify each step before moving to the next
4. Combine the results to solve the overall problem`
```

### 3. Action Validation Pattern

The **Action Validation** pattern helps ensure the agent selects and executes appropriate actions.

```go
// Example implementation of a validator in your agent logic:
func validateAction(action *react.Action, availableTools []tool.Tool) error {
    // Verify the tool exists
    toolExists := false
    for _, t := range availableTools {
        if t.Name() == action.ToolName {
            toolExists = true
            break
        }
    }
    if !toolExists {
        return fmt.Errorf("tool '%s' does not exist", action.ToolName)
    }
    
    // Verify required parameters
    // ... additional validation logic
    
    return nil
}
```

### 4. Observation Reflection Pattern

The **Observation Reflection** pattern ensures the agent properly processes and learns from observations.

```go
// In your system prompt:
systemPrompt += `After receiving an observation:
1. Analyze what the observation tells you
2. Consider whether it confirms or contradicts your expectations
3. Determine if the observation is sufficient or if you need more information
4. Update your understanding based on this new information`
```

## Tool Design Patterns

### 1. Purpose-Focused Tool Pattern

Design tools with a clear, single purpose that the agent can easily understand.

```go
// Good example: Calculator tool focused only on calculations
type CalculatorTool struct {
    name        string
    description string
}

func NewCalculatorTool() *CalculatorTool {
    return &CalculatorTool{
        name:        "calculator",
        description: "Perform mathematical calculations",
    }
}
```

### 2. Parameter Clarity Pattern

Design tool parameters with clear names, descriptions, and constraints.

```go
func (t *SearchTool) Parameters() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "query": map[string]interface{}{
                "type":        "string",
                "description": "The search query to look for",
                "required":    true,
            },
            "max_results": map[string]interface{}{
                "type":        "integer",
                "description": "Maximum number of results to return (1-10)",
                "minimum":     1,
                "maximum":     10,
                "default":     3,
                "required":    false,
            },
        },
        "required": []string{"query"},
    }
}
```

### 3. Observation Formatting Pattern

Format tool observations to be easily understood by the agent.

```go
func (t *SearchTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
    // ... execution logic
    
    // Format results for easy agent interpretation
    var result strings.Builder
    result.WriteString(fmt.Sprintf("Found %d results for query: '%s'\n\n", len(searchResults), query))
    
    for i, r := range searchResults {
        result.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
        result.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
        result.WriteString(fmt.Sprintf("   URL: %s\n\n", r.URL))
    }
    
    return &tool.Result{
        Output: result.String(),
    }, nil
}
```

## Memory Utilization Patterns

### 1. Context Persistence Pattern

Store important information in working memory for future reference.

```go
// Store important information from a conversation
func storeUserInformation(ctx context.Context, memory react.ReactWorkingMemory, 
                         name, profession, company string) error {
    item := react.WorkingMemoryItem{
        Type:    "user_info",
        ID:      "user_profile",
        Content: fmt.Sprintf("User %s works as a %s at %s", name, profession, company),
        Metadata: map[string]interface{}{
            "name":       name,
            "profession": profession,
            "company":    company,
        },
    }
    return memory.StoreItem(ctx, &item)
}
```

### 2. Progressive Knowledge Building Pattern

Build on existing knowledge by referencing and extending what's already in memory.

```go
// In your system prompt:
systemPrompt += `When learning new information:
1. Check if it relates to information you already know
2. Connect new information with existing knowledge
3. Update your understanding when new information contradicts what you knew
4. Store important facts, entities, and relationships in working memory`
```

### 3. Entity Tracking Pattern

Track entities and their relationships across multiple interactions.

```go
// Track project information
func trackProject(ctx context.Context, memory react.ReactWorkingMemory,
                 projectID, name, status string, team []string) error {
    projectItem := react.WorkingMemoryItem{
        Type:    "project",
        ID:      projectID,
        Content: fmt.Sprintf("Project %s is currently %s", name, status),
        Metadata: map[string]interface{}{
            "name":   name,
            "status": status,
            "team":   team,
        },
    }
    return memory.StoreItem(ctx, &projectItem)
}
```

## Error Handling Patterns

### 1. Graceful Degradation Pattern

Design agents to gracefully handle errors and continue functioning.

```go
// In your action executor:
func executeAction(ctx context.Context, agent *react.ReActAgent, action *react.Action) (*react.Observation, error) {
    observation, err := agent.ExecuteTool(ctx, action.ToolName, action.ToolInput)
    if err != nil {
        // Create an error observation rather than failing completely
        return react.NewErrorObservation(
            fmt.Errorf("tool execution failed: %w", err),
            action.ToolName,
        ), nil
    }
    return observation, nil
}
```

### 2. Retry with Adaptation Pattern

Adapt and retry when actions fail rather than giving up.

```go
// In your system prompt:
systemPrompt += `When you encounter an error:
1. Analyze what went wrong
2. Consider alternative approaches
3. Try again with a modified approach
4. If still unsuccessful after 2-3 attempts, acknowledge the limitation and explain what you tried`
```

### 3. Verification Pattern

Verify important facts and calculations to reduce errors.

```go
// In your system prompt:
systemPrompt += `For critical calculations or important facts:
1. Double-check your work
2. Consider if the result makes sense
3. Use a different method to verify if possible
4. State both your answer and how you verified it`
```

## Integration Patterns

### 1. Pipeline Pattern

Combine ReAct agents with other agent types in a pipeline.

```go
func createAgentPipeline(ctx context.Context) (*flow.SequentialFlow, error) {
    // Create the research agent
    researchAgent, err := createReactResearchAgent()
    if err != nil {
        return nil, err
    }
    
    // Create the writing agent
    writingAgent, err := createLLMWritingAgent()
    if err != nil {
        return nil, err
    }
    
    // Create a sequential flow
    pipeline := flow.NewSequentialFlow(
        "research_writing_pipeline",
        []agent.Agent{researchAgent, writingAgent},
    )
    
    return pipeline, nil
}
```

### 2. Expert Delegation Pattern

Design specialized ReAct agents for different domains and delegate tasks to them.

```go
// Define expert agents for different domains
mathExpert := createReactMathExpert()
researchExpert := createReactResearchExpert()
writingExpert := createReactWritingExpert()

// Create a router function to delegate tasks
func routeToExpert(query string) agent.Agent {
    if containsMathProblem(query) {
        return mathExpert
    } else if isResearchQuery(query) {
        return researchExpert
    } else {
        return writingExpert
    }
}
```

## Best Practices

### System Prompt Engineering

1. **Be explicit about the reasoning process** - Guide the agent on how to structure thoughts
2. **Define clear criteria for final answers** - Help the agent understand when to stop
3. **Provide examples** - Include example conversations showing proper reasoning
4. **Define the scope** - Clearly state what the agent should and shouldn't try to do

### Tool Design

1. **Single responsibility** - Each tool should do one thing well
2. **Clear descriptions** - Make it obvious what each tool does
3. **Explicit parameters** - Define parameters with clear descriptions and types
4. **Informative observations** - Return observations that aid reasoning
5. **Error messages** - Return helpful error messages that guide the agent

### Memory Management

1. **Be selective** - Store only important information
2. **Use metadata** - Add metadata to help with retrieval
3. **Structure consistently** - Use consistent patterns for storing related items
4. **Clean up** - Remove outdated or irrelevant information

### Testing and Debugging

1. **Test simple cases first** - Start with basic scenarios before complex ones
2. **Isolate components** - Test tools individually before integrating
3. **Trace reasoning paths** - Log thought processes to understand agent decisions
4. **Compare with expectations** - Verify outputs match expected results

### Performance Optimization

1. **Limit thinking iterations** - Set reasonable max iterations
2. **Cache tool results** - Cache observations for repeated actions
3. **Use efficient prompts** - Keep prompts concise but informative
4. **Prioritize important context** - Keep the most relevant context if size is limited

## Conclusion

Effective ReAct agents combine thoughtful design patterns with consistent best practices. By following the patterns and practices outlined in this guide, you can create robust, reliable agents that excel at complex reasoning tasks.

For more specific examples, see the [ReAct Examples](../../examples/react) directory, particularly the [Complex Reasoning Examples](../../examples/react/complex_reasoning). 