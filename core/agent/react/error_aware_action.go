package react

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// ErrorAwareActionSelector selects actions with built-in error recovery strategies.
type ErrorAwareActionSelector struct {
	model           model.Model
	prompting       ErrorAwarePromptStrategy
	recoveryOptions map[string][]string
}

// ErrorAwarePromptStrategy represents a strategy for prompting action selection
// with error awareness.
type ErrorAwarePromptStrategy interface {
	// BuildActionPrompt builds a prompt for action selection with error context.
	BuildActionPrompt(thought *Thought, tools []tool.Tool, planState *PlanState, lastError *ErrorInfo) string
}

// ErrorAwarePromptingStrategy is an implementation of ErrorAwarePromptStrategy
// that provides error context and recovery suggestions.
type ErrorAwarePromptingStrategy struct {
	recoveryOptions        map[string][]string
	includeRecoveryOptions bool
}

// NewErrorAwarePromptingStrategy creates a new error-aware prompting strategy.
func NewErrorAwarePromptingStrategy(
	recoveryOptions map[string][]string,
	includeRecoveryOptions bool,
) *ErrorAwarePromptingStrategy {
	if recoveryOptions == nil {
		// Default recovery options for common error types
		recoveryOptions = map[string][]string{
			"permission_denied": {
				"Check if you have the necessary permissions",
				"Try a different approach that doesn't require elevated permissions",
				"Look for alternative data sources",
			},
			"not_found": {
				"Verify that the resource exists",
				"Try different search criteria",
				"Check for typos in resource identifiers",
				"Look for alternative resources",
			},
			"rate_limit": {
				"Wait and retry after a delay",
				"Reduce the frequency of requests",
				"Try a different API or data source",
			},
			"invalid_input": {
				"Review and correct the input parameters",
				"Check input format and structure",
				"Verify that the input meets all requirements",
			},
			"tool_execution": {
				"Try a different tool that provides similar functionality",
				"Break down the operation into smaller steps",
				"Check if the tool requires specific formatting or parameters",
			},
			"network": {
				"Check network connectivity",
				"Wait and retry the operation",
				"Try an alternative endpoint or service",
			},
			"default": {
				"Consider alternative approaches to achieve the same goal",
				"Break down the problem into smaller steps",
				"Try using a different tool",
				"Check if you're missing any context or information",
			},
		}
	}

	return &ErrorAwarePromptingStrategy{
		recoveryOptions:        recoveryOptions,
		includeRecoveryOptions: includeRecoveryOptions,
	}
}

// BuildActionPrompt builds a prompt for action selection with error awareness.
func (s *ErrorAwarePromptingStrategy) BuildActionPrompt(
	thought *Thought,
	tools []tool.Tool,
	planState *PlanState,
	lastError *ErrorInfo,
) string {
	var prompt strings.Builder

	prompt.WriteString("Based on your thought, select an action to take. Available tools:\n")
	for _, t := range tools {
		prompt.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))

		// Include parameter information
		params := t.Parameters()
		if len(params) > 0 {
			paramsJSON, err := json.MarshalIndent(params, "  ", "  ")
			if err == nil {
				prompt.WriteString(fmt.Sprintf("  Parameters: %s\n", string(paramsJSON)))
			}
		}
	}

	prompt.WriteString("\nYour thought was:\n")
	prompt.WriteString(thought.Content)

	// Include plan state information if available
	if planState != nil && planState.HasPlan {
		prompt.WriteString("\n\nYou are currently working on a plan. Current step: ")
		if planState.CurrentStepIndex < len(planState.CurrentPlan) {
			prompt.WriteString(planState.CurrentPlan[planState.CurrentStepIndex])
		}
	}

	// Include error context and recovery suggestions if there was an error
	if lastError != nil {
		prompt.WriteString("\n\nYou encountered an error in your previous action:\n")
		prompt.WriteString(fmt.Sprintf("Error message: %s\n", lastError.Message))

		if s.includeRecoveryOptions {
			// Determine error type and provide relevant recovery suggestions
			errorType := "default"
			messageLower := strings.ToLower(lastError.Message)

			// Try to infer error type from message
			if strings.Contains(messageLower, "permission") || strings.Contains(messageLower, "access denied") {
				errorType = "permission_denied"
			} else if strings.Contains(messageLower, "not found") || strings.Contains(messageLower, "doesn't exist") {
				errorType = "not_found"
			} else if strings.Contains(messageLower, "rate limit") || strings.Contains(messageLower, "too many requests") {
				errorType = "rate_limit"
			} else if strings.Contains(messageLower, "invalid") || strings.Contains(messageLower, "wrong format") {
				errorType = "invalid_input"
			} else if strings.Contains(messageLower, "network") || strings.Contains(messageLower, "connection") {
				errorType = "network"
			} else if lastError.Source == "tool_execution" {
				errorType = "tool_execution"
			}

			// Get recovery suggestions
			suggestions, exists := s.recoveryOptions[errorType]
			if !exists {
				suggestions = s.recoveryOptions["default"]
			}

			if len(suggestions) > 0 {
				prompt.WriteString("\nConsider these recovery strategies:\n")
				for i, suggestion := range suggestions {
					prompt.WriteString(fmt.Sprintf("%d. %s\n", i+1, suggestion))
				}
			}
		}

		prompt.WriteString("\nChoose an action that helps recover from this error or takes a different approach.\n")
	}

	prompt.WriteString("\n\nSelect one of the available tools and provide the appropriate input parameters. Respond in JSON format with 'tool_name' and 'tool_input' fields. Example:\n")
	prompt.WriteString("{\n  \"tool_name\": \"example_tool\",\n  \"tool_input\": {\n    \"param1\": \"value1\",\n    \"param2\": 42\n  }\n}")

	return prompt.String()
}

// NewErrorAwareActionSelector creates a new error-aware action selector.
func NewErrorAwareActionSelector(
	model model.Model,
	prompting ErrorAwarePromptStrategy,
	recoveryOptions map[string][]string,
) *ErrorAwareActionSelector {
	if recoveryOptions == nil {
		recoveryOptions = make(map[string][]string)
	}

	return &ErrorAwareActionSelector{
		model:           model,
		prompting:       prompting,
		recoveryOptions: recoveryOptions,
	}
}

// Select selects an action based on the thought with error awareness.
func (s *ErrorAwareActionSelector) Select(
	ctx context.Context,
	thought *Thought,
	tools []tool.Tool,
) (*Action, error) {
	if s.model == nil {
		return nil, fmt.Errorf("model is required for ErrorAwareActionSelector")
	}

	if thought == nil {
		return nil, fmt.Errorf("thought cannot be nil")
	}

	if len(tools) == 0 {
		return nil, fmt.Errorf("at least one tool is required")
	}

	// Extract plan state and last error from context
	ctxVal := ctx.Value("planState")
	var planState *PlanState
	if ctxVal != nil {
		if ps, ok := ctxVal.(*PlanState); ok {
			planState = ps
		}
	}

	var lastError *ErrorInfo
	if planState != nil {
		lastError = planState.LastError
	}

	// Build the prompt for action selection
	promptText := ""
	if s.prompting != nil {
		promptText = s.prompting.BuildActionPrompt(thought, tools, planState, lastError)
	} else {
		// Fallback to a simple default prompt if no strategy is provided
		promptText = fmt.Sprintf("Based on your thought: \"%s\", select an action to take.", thought.Content)
	}

	// Create a system message with the prompt
	promptMsg := message.NewSystemMessage(promptText)

	// Create a user message with the thought content
	thoughtMsg := message.NewUserMessage(thought.Content)

	// Generate the action using the model
	opts := model.DefaultOptions()
	response, err := s.model.GenerateWithMessages(ctx, []*message.Message{promptMsg, thoughtMsg}, opts)
	if err != nil {
		return nil, fmt.Errorf("action selection failed: %w", err)
	}

	actionText := ""
	if len(response.Messages) > 0 {
		actionText = response.Messages[0].Content
	} else if response.Text != "" {
		actionText = response.Text
	} else {
		return nil, fmt.Errorf("model returned empty response")
	}

	// Extract JSON from the response
	actionJSON := extractJSON(actionText)
	if actionJSON == "" {
		return nil, fmt.Errorf("no valid JSON found in model response")
	}

	// Parse the JSON
	var actionData struct {
		ToolName  string                 `json:"tool_name"`
		ToolInput map[string]interface{} `json:"tool_input"`
	}
	if err := json.Unmarshal([]byte(actionJSON), &actionData); err != nil {
		return nil, fmt.Errorf("failed to parse action JSON: %w", err)
	}

	// Validate the tool name
	var validTool bool
	for _, t := range tools {
		if t.Name() == actionData.ToolName {
			validTool = true
			break
		}
	}
	if !validTool {
		return nil, fmt.Errorf("selected tool '%s' is not available", actionData.ToolName)
	}

	// Create the action
	action := &Action{
		ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
		ThoughtID: thought.ID,
		ToolName:  actionData.ToolName,
		ToolInput: actionData.ToolInput,
		Timestamp: time.Now().Unix(),
	}

	return action, nil
}
