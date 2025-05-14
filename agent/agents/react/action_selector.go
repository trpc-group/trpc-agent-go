package react

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// LLMActionSelector selects actions using an LLM.
type LLMActionSelector struct {
	model     model.Model
	prompting ActionPromptStrategy
}

// ActionPromptStrategy represents a strategy for prompting action selection.
type ActionPromptStrategy interface {
	// BuildActionPrompt builds a prompt for action selection.
	BuildActionPrompt(thought *Thought, tools []tool.Tool) string
}

// DefaultActionPromptStrategy is the default strategy for action prompting.
type DefaultActionPromptStrategy struct {
}

// NewDefaultActionPromptStrategy creates a new default action prompt strategy.
func NewDefaultActionPromptStrategy() *DefaultActionPromptStrategy {
	return &DefaultActionPromptStrategy{}
}

// BuildActionPrompt builds a prompt for action selection.
func (s *DefaultActionPromptStrategy) BuildActionPrompt(thought *Thought, tools []tool.Tool) string {
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

	prompt.WriteString("\n\nYou can respond in one of these two formats:\n")

	// JSON Format example
	prompt.WriteString("\n1. JSON Format (preferred):\n")
	prompt.WriteString("{\n  \"tool_name\": \"example_tool\",\n  \"tool_input\": {\n    \"param1\": \"value1\",\n    \"param2\": 42\n  }\n}\n")

	// ReAct Format example
	prompt.WriteString("\n2. ReAct Format:\n")
	prompt.WriteString("Action: example_tool\n")
	prompt.WriteString("Action Input: param1=value1, param2=42\n")

	// Additional examples for tools that have single parameters
	prompt.WriteString("\nExamples for tools with single parameters:\n")
	prompt.WriteString("- For location-based tools:\n")
	prompt.WriteString("  Action: mcp_weather_lookup\n")
	prompt.WriteString("  Action Input: New York\n")

	prompt.WriteString("- For calculator:\n")
	prompt.WriteString("  Action: calculator\n")
	prompt.WriteString("  Action Input: a=5, b=3, operation=add\n")

	return prompt.String()
}

// NewLLMActionSelector creates a new LLM-based action selector.
func NewLLMActionSelector(model model.Model, prompting ActionPromptStrategy) *LLMActionSelector {
	return &LLMActionSelector{
		model:     model,
		prompting: prompting,
	}
}

// Select selects an action based on the thought.
func (s *LLMActionSelector) Select(ctx context.Context, thought *Thought, tools []tool.Tool) (*Action, error) {
	if s.model == nil {
		return nil, fmt.Errorf("model is required for LLMActionSelector")
	}

	if thought == nil {
		return nil, fmt.Errorf("thought cannot be nil")
	}

	if len(tools) == 0 {
		return nil, fmt.Errorf("at least one tool is required")
	}

	// Build the prompt for action selection
	promptText := ""
	if s.prompting != nil {
		promptText = s.prompting.BuildActionPrompt(thought, tools)
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
	opts.EnableToolCalls = true // Ensure tool calls are enabled
	response, err := s.model.GenerateWithMessages(ctx, []*message.Message{promptMsg, thoughtMsg}, opts)
	if err != nil {
		return nil, fmt.Errorf("action selection failed: %w", err)
	}

	// First check if the model returned a tool call directly
	if len(response.ToolCalls) > 0 {
		// Use the first tool call
		toolCall := response.ToolCalls[0]

		// Parse the arguments
		var toolInput map[string]interface{}
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &toolInput); err != nil {
			return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
		}

		// Validate the tool name
		var validTool bool
		for _, t := range tools {
			if t.Name() == toolCall.Function.Name {
				validTool = true
				break
			}
		}
		if !validTool {
			return nil, fmt.Errorf("selected tool '%s' is not available", toolCall.Function.Name)
		}

		// Create the action
		action := &Action{
			ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
			ThoughtID: thought.ID,
			ToolName:  toolCall.Function.Name,
			ToolInput: toolInput,
			Timestamp: time.Now().Unix(),
		}

		return action, nil
	}

	// Fallback to parsing from text if no tool calls were returned
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
		// Try to parse using ReAct format (Action: X, Action Input: Y)
		// Find the matching tool to pass along for parameter checking
		var matchingTool tool.Tool
		for _, t := range tools {
			if strings.Contains(strings.ToLower(actionText), strings.ToLower(t.Name())) {
				matchingTool = t
				break
			}
		}

		reactJSON, err := parseReActFormat(actionText, matchingTool, tools)
		if err != nil {
			return nil, fmt.Errorf("failed to parse action from response: %w", err)
		}

		if reactJSON != "" {
			actionJSON = reactJSON
		} else {
			return nil, fmt.Errorf("no valid JSON or ReAct format found in model response")
		}
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

// RuleBasedActionSelector selects actions using predefined rules.
type RuleBasedActionSelector struct {
	rules    map[string]string
	fallback string
}

// NewRuleBasedActionSelector creates a new rule-based action selector.
func NewRuleBasedActionSelector(rules map[string]string, fallback string) *RuleBasedActionSelector {
	return &RuleBasedActionSelector{
		rules:    rules,
		fallback: fallback,
	}
}

// Select selects an action based on the thought using predefined rules.
func (s *RuleBasedActionSelector) Select(ctx context.Context, thought *Thought, tools []tool.Tool) (*Action, error) {
	if thought == nil {
		return nil, fmt.Errorf("thought cannot be nil")
	}

	// Check thought content against rules
	selectedTool := s.fallback
	selectedInput := make(map[string]interface{})

	for pattern, toolName := range s.rules {
		if strings.Contains(strings.ToLower(thought.Content), strings.ToLower(pattern)) {
			selectedTool = toolName
			break
		}
	}

	// Validate the tool name
	var validTool bool
	for _, t := range tools {
		if t.Name() == selectedTool {
			validTool = true
			break
		}
	}
	if !validTool {
		return nil, fmt.Errorf("selected tool '%s' is not available", selectedTool)
	}

	// Create a simple input based on the thought
	selectedInput["query"] = thought.Content

	// Create the action
	action := &Action{
		ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
		ThoughtID: thought.ID,
		ToolName:  selectedTool,
		ToolInput: selectedInput,
		Timestamp: time.Now().Unix(),
	}

	return action, nil
}

// extractJSON extracts JSON from a string that might contain other text.
func extractJSON(text string) string {
	// Find the first '{' and the last '}'
	start := strings.Index(text, "{")
	if start == -1 {
		return ""
	}

	end := strings.LastIndex(text, "}")
	if end == -1 || end < start {
		return ""
	}

	potentialJSON := text[start : end+1]

	// Validate that it's valid JSON
	var js interface{}
	if err := json.Unmarshal([]byte(potentialJSON), &js); err != nil {
		return ""
	}

	return potentialJSON
}

// parseReActFormat parses ReAct format output (Action: X, Action Input: Y)
// and returns a structured JSON representation of it.
func parseReActFormat(text string, matchingTool tool.Tool, allTools []tool.Tool) (string, error) {
	// Extract the action name
	actionPrefix := "Action:"
	actionInputPrefix := "Action Input:"

	actionStartIdx := strings.Index(text, actionPrefix)
	if actionStartIdx == -1 {
		return "", fmt.Errorf("no action found in text")
	}

	// Find the start of the action name (after "Action:")
	actionStartIdx += len(actionPrefix)

	// Find the end of the action (either end of line or start of Action Input)
	actionEndIdx := strings.Index(text[actionStartIdx:], "\n")
	if actionEndIdx == -1 {
		actionEndIdx = len(text[actionStartIdx:])
	} else {
		actionEndIdx += actionStartIdx
	}

	// Trim the action name
	actionName := strings.TrimSpace(text[actionStartIdx:actionEndIdx])

	// If we don't have a matching tool yet, find one by name
	if matchingTool == nil {
		for _, t := range allTools {
			if t.Name() == actionName {
				matchingTool = t
				break
			}
		}
	}

	// Extract the action input
	actionInputStartIdx := strings.Index(text, actionInputPrefix)
	if actionInputStartIdx == -1 {
		// No action input found, return an empty object as input
		jsonResult := fmt.Sprintf(`{"tool_name": "%s", "tool_input": {}}`, actionName)
		return jsonResult, nil
	}

	// Find the start of the action input (after "Action Input:")
	actionInputStartIdx += len(actionInputPrefix)

	// The rest of the text is the action input
	actionInput := strings.TrimSpace(text[actionInputStartIdx:])

	// Create a schema processor for parameter handling
	schemaProcessor := tool.NewSchemaProcessor(allTools)

	// If the action input is in JSON format, try to use it directly
	if strings.HasPrefix(actionInput, "{") && strings.HasSuffix(actionInput, "}") {
		var parsedInput map[string]interface{}
		if err := json.Unmarshal([]byte(actionInput), &parsedInput); err == nil {
			// If we have a schema, verify and convert types
			if matchingTool != nil {
				convertedInput, err := tool.ConvertArgumentsToCorrectTypes(parsedInput, matchingTool.Parameters())
				if err == nil {
					parsedInput = convertedInput
				}
			}

			// Create the final result
			inputJSON, _ := json.Marshal(parsedInput)
			jsonResult := fmt.Sprintf(`{"tool_name": "%s", "tool_input": %s}`, actionName, string(inputJSON))
			return jsonResult, nil
		}
	}

	// Try to parse as structured text (key-value pairs)
	parsedArgs, err := tool.ParseStructuredArgument(actionInput)
	if err == nil && len(parsedArgs) > 0 {
		// If we have a schema, verify and convert types
		if matchingTool != nil {
			convertedInput, err := tool.ConvertArgumentsToCorrectTypes(parsedArgs, matchingTool.Parameters())
			if err == nil {
				parsedArgs = convertedInput
			}
		}

		// Create the final result
		inputJSON, _ := json.Marshal(parsedArgs)
		jsonResult := fmt.Sprintf(`{"tool_name": "%s", "tool_input": %s}`, actionName, string(inputJSON))
		return jsonResult, nil
	}

	// If we couldn't parse it as structured, handle single parameter case
	var inputObj map[string]interface{}

	// Try to extract primary parameter name from tool schema
	var paramName string
	if matchingTool != nil {
		// Use the schema processor to find the primary parameter
		primaryParam, err := schemaProcessor.FindPrimaryParameterName(matchingTool.Name())
		if err == nil {
			paramName = primaryParam
		}
	} else {
		// Try to infer from other tools by name
		for _, t := range allTools {
			if t.Name() == actionName {
				primaryParam, err := schemaProcessor.FindPrimaryParameterName(t.Name())
				if err == nil {
					paramName = primaryParam
					break
				}
			}
		}
	}

	// If we still don't have a parameter name, use a fallback inference mechanism
	if paramName == "" {
		// Common default parameter names based on partial tool name
		if strings.Contains(actionName, "weather") {
			paramName = "location"
		} else if strings.Contains(actionName, "search") {
			paramName = "query"
		} else if strings.Contains(actionName, "translate") {
			paramName = "text"
		} else if strings.Contains(actionName, "calculator") {
			paramName = "expression"
		} else if strings.Contains(actionName, "convert") {
			paramName = "value"
		} else if strings.Contains(actionName, "analyze") {
			paramName = "data"
		} else {
			// If we can't infer a parameter name, use a generic one
			paramName = "input"
		}
	}

	// Create the input object with the primary parameter
	inputObj = map[string]interface{}{
		paramName: actionInput,
	}

	// If we have a schema, convert the input to the correct type
	if matchingTool != nil {
		convertedInput, err := tool.ConvertArgumentsToCorrectTypes(inputObj, matchingTool.Parameters())
		if err == nil {
			inputObj = convertedInput
		}
	}

	// Convert the input object to JSON
	inputJSON, err := json.Marshal(inputObj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal action input: %w", err)
	}

	jsonResult := fmt.Sprintf(`{"tool_name": "%s", "tool_input": %s}`, actionName, string(inputJSON))
	return jsonResult, nil
}
