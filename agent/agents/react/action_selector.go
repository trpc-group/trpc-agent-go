package react

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
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
	prompt.WriteString("\n\nYou must respond in the following format or output Finished if you have no more actions to take:\n")
	prompt.WriteString("1. JSON Format (preferred):\n")
	prompt.WriteString("{\n   \"tool_name\": \"example_tool\",\n  \"tool_input\": {\n    \"param1\": \"value1\",\n    \"param2\": 42\n  }\n}\n")
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
	// If thought has a suggested action, try to use it
	if thought.SuggestedAction != nil {
		// Validate the tool name
		var matchingTool tool.Tool
		for _, t := range tools {
			if t.Name() == thought.SuggestedAction.ToolName {
				matchingTool = t
				break
			}
		}

		if matchingTool == nil {
			log.Warnf("Suggested tool '%s' not found in available tools", thought.SuggestedAction.ToolName)
		} else {
			// Use the suggested action directly
			action := &Action{
				ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
				ThoughtID: thought.ID,
				ToolName:  thought.SuggestedAction.ToolName,
				ToolInput: thought.SuggestedAction.ToolInput,
				Timestamp: time.Now().Unix(),
			}
			log.Infof("Using suggested action from thought: %s", action.ToolName)
			return action, nil
		}
	}

	// Build prompt using the prompting strategy
	prompt := s.prompting.BuildActionPrompt(thought, tools)

	// Create model message
	messages := []*message.Message{
		message.NewUserMessage(prompt),
	}

	// Setup model options
	opts := model.GenerationOptions{
		Temperature:      0.0,
		MaxTokens:        250,
		FrequencyPenalty: 0.0,
		PresencePenalty:  0.0,
		TopP:             1.0,
		EnableToolCalls:  s.model.SupportsToolCalls(),
	}

	// Find if there's a matching tool mentioned in the thought
	var matchingTool tool.Tool
	for _, t := range tools {
		// Check if thought mentions the tool name
		if strings.Contains(strings.ToLower(thought.Content), strings.ToLower(t.Name())) {
			matchingTool = t
			log.Debugf("Found matching tool in thought: %s", t.Name())
			break
		}
	}

	log.Debugf("Sending action selection prompt to model %s with EnableToolCalls=%v",
		s.model.Name(), opts.EnableToolCalls)
	response, err := s.model.GenerateWithMessages(ctx, messages, opts)
	if err != nil {
		log.Errorf("Failed to generate action selection: %v", err)
		return nil, fmt.Errorf("failed to generate action selection: %w", err)
	}

	log.Debugf("Got response from model with %d tool calls", len(response.ToolCalls))

	// Check if the model indicated it's finished and wants to provide a final response
	if response.Text != "" {
		respLower := strings.ToLower(response.Text)
		if strings.Contains(respLower, "finished") ||
			strings.Contains(respLower, "no more actions") ||
			strings.Contains(respLower, "final answer") {
			log.Infof("LLM indicated it's finished with actions and wants to provide a summary")

			// Generate a summary response from the model
			summaryPrompt := fmt.Sprintf(
				"Based on the information gathered, please provide a concise final answer to the user's query. User query was related to: %s",
				thought.Content)

			summaryMessages := []*message.Message{
				message.NewUserMessage(summaryPrompt),
			}

			summaryOpts := model.GenerationOptions{
				Temperature:      0.0,
				MaxTokens:        500,
				FrequencyPenalty: 0.0,
				PresencePenalty:  0.0,
				TopP:             1.0,
				EnableToolCalls:  false, // No tool calls needed for summary
			}

			// Get the summary from the model
			summaryResponse, summaryErr := s.model.GenerateWithMessages(ctx, summaryMessages, summaryOpts)
			if summaryErr != nil {
				log.Warnf("Failed to generate summary, using thought content: %v", summaryErr)
				// Create a special "finished" action that will be recognized by the agent
				action := &Action{
					ID:        fmt.Sprintf("final-answer-%d", time.Now().UnixNano()),
					ThoughtID: thought.ID,
					ToolName:  "final_answer",
					ToolInput: map[string]interface{}{
						"content": thought.Content,
					},
					Timestamp: time.Now().Unix(),
				}
				return action, nil
			}

			summaryText := ""
			if len(summaryResponse.Messages) > 0 {
				summaryText = summaryResponse.Messages[0].Content
			} else if summaryResponse.Text != "" {
				summaryText = summaryResponse.Text
			} else {
				summaryText = thought.Content
			}

			// Create a special "finished" action that will be recognized by the agent
			action := &Action{
				ID:        fmt.Sprintf("final-answer-%d", time.Now().UnixNano()),
				ThoughtID: thought.ID,
				ToolName:  "final_answer",
				ToolInput: map[string]interface{}{
					"content": summaryText,
				},
				Timestamp: time.Now().Unix(),
			}
			return action, nil
		}
	}

	// Extract the tool call from the response
	// If we have a tool call, use it directly
	// Otherwise try to parse from the text
	if len(response.ToolCalls) > 0 {
		// Use the first tool call
		toolCall := response.ToolCalls[0]
		log.Debugf("Found tool call: %s(%s)", toolCall.Function.Name, toolCall.Function.Arguments)

		// Find the matching tool
		var matchingTool tool.Tool
		for _, t := range tools {
			if t.Name() == toolCall.Function.Name {
				matchingTool = t
				log.Debugf("Found matching tool: %s", t.Name())
				break
			}
		}

		if matchingTool == nil {
			log.Warnf("No matching tool found for '%s'", toolCall.Function.Name)
			return nil, fmt.Errorf("tool '%s' not found", toolCall.Function.Name)
		}

		// Parse the arguments
		var toolInput map[string]interface{}
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &toolInput); err != nil {
			log.Errorf("Failed to parse tool arguments: %v", err)
			return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
		}

		// Try to parse direct string input first (for OpenAI-compatible models)
		if len(toolInput) == 1 {
			// Create a synthetic structure with tool_name and tool_input
			syntheticInput := map[string]interface{}{
				"tool_name":  toolCall.Function.Name,
				"tool_input": toolCall.Function.Arguments, // Use the raw arguments string
			}
			if action, success := parseFormatWithDirectToolInput(syntheticInput, tools); success {
				// Set the thought ID
				action.ThoughtID = thought.ID
				return action, nil
			}
		}

		// Validate and convert parameters
		validatedInput, err := tool.ValidateParameters(toolInput, matchingTool)
		if err != nil {
			log.Warnf("Parameter validation failed: %v", err)
			return nil, fmt.Errorf("parameter validation for tool '%s' failed: %w",
				matchingTool.Name(), err)
		}

		log.Debugf("Successfully validated parameters for tool '%s'", matchingTool.Name())
		toolInput = validatedInput

		// Create the action
		action := &Action{
			ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
			ThoughtID: thought.ID,
			ToolName:  matchingTool.Name(),
			ToolInput: toolInput,
			Timestamp: time.Now().Unix(),
		}

		log.Debugf("Selected action from tool call: %s with input: %v", action.ToolName, action.ToolInput)
		return action, nil
	}

	// No direct tool calls, try to parse from text content
	log.Debugf("No tool calls found in model response, falling back to text parsing")
	if response.Text == "" && len(response.Messages) > 0 {
		response.Text = response.Messages[0].Content
		log.Debugf("Using content from first message: length=%d", len(response.Text))
	}
	if response.Text == "" {
		return nil, fmt.Errorf("model returned empty response")
	}

	// Try to parse the action from the text response
	action, err := parseActionFromText(thought.ID, response.Text, matchingTool, tools)
	if err != nil {
		log.Errorf("Failed to parse action from response: %v", err)
		return nil, fmt.Errorf("failed to parse action from response: %w", err)
	}

	log.Debugf("Successfully parsed action from text: %s with input: %v", action.ToolName, action.ToolInput)
	return action, nil
}

// parseActionFromText contains the logic for parsing actions from text responses
func parseActionFromText(thoughtID string, actionText string, matchingTool tool.Tool, tools []tool.Tool) (*Action, error) {
	log.Debugf("Parsing action from text response. Length: %d, First 100 chars: %s",
		len(actionText), actionText[:min(100, len(actionText))])

	// Look for tool name references first to identify the most likely tool
	if matchingTool == nil {
		log.Debugf("No matching tool provided, searching for tool references in text")
		for _, t := range tools {
			if strings.Contains(strings.ToLower(actionText), strings.ToLower(t.Name())) {
				matchingTool = t
				log.Debugf("Found matching tool reference in text: %s", t.Name())
				break
			}
		}
	}

	// Look for special markdown JSON code blocks like ```json { ... } ```
	jsonBlockRegex := regexp.MustCompile("```json\\s*([\\s\\S]*?)\\s*```")
	if matches := jsonBlockRegex.FindStringSubmatch(actionText); len(matches) > 1 {
		blockContent := strings.TrimSpace(matches[1])
		log.Debugf("Found JSON code block: %s", blockContent)

		// If we have a matching tool, try to create an action directly
		if matchingTool != nil {
			var toolInput map[string]interface{}
			if err := json.Unmarshal([]byte(blockContent), &toolInput); err == nil {
				log.Debugf("Successfully parsed JSON block as tool input: %v", toolInput)

				// Convert arguments to correct types if possible
				convertedInput, err := tool.ConvertArgumentsToCorrectTypes(
					toolInput,
					matchingTool.Parameters(),
				)
				if err == nil {
					toolInput = convertedInput
				} else {
					log.Debugf("Failed to convert arguments types: %v", err)
				}

				// Create the action
				action := &Action{
					ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
					ThoughtID: thoughtID,
					ToolName:  matchingTool.Name(),
					ToolInput: toolInput,
					Timestamp: time.Now().Unix(),
				}

				log.Debugf("Created action from JSON block: %s with input: %v",
					action.ToolName, action.ToolInput)
				return action, nil
			} else {
				log.Debugf("Failed to parse JSON block: %v", err)
			}
		}
	}

	// Special handling for format: "Action: Use the X tool to..."
	actionPrefix := "Action:"
	if actionIdx := strings.Index(actionText, actionPrefix); actionIdx != -1 {
		log.Debugf("Found 'Action:' marker at index %d", actionIdx)

		// Extract text after "Action:"
		actionPart := actionText[actionIdx+len(actionPrefix):]
		endOfLine := strings.Index(actionPart, "\n")
		if endOfLine != -1 {
			actionPart = actionPart[:endOfLine]
		}
		actionPart = strings.TrimSpace(actionPart)
		log.Debugf("Action text: %s", actionPart)

		// Try to extract tool name from common formats
		toolNamePatterns := []string{
			"use the `(.*?)` tool",
			"use the (.*?) tool",
			"use (.*?) to",
			"using the (.*?) tool",
			"using (.*?) to",
			"tool: (.*?)$",
			"the (.*?) function",
		}

		extractedToolName := ""
		for _, pattern := range toolNamePatterns {
			re := regexp.MustCompile("(?i)" + pattern)
			if matches := re.FindStringSubmatch(actionPart); len(matches) > 1 {
				extractedToolName = strings.TrimSpace(matches[1])
				log.Debugf("Extracted tool name '%s' using pattern '%s'", extractedToolName, pattern)
				break
			}
		}

		// If we extracted a tool name, find the matching tool
		if extractedToolName != "" {
			for _, t := range tools {
				if strings.Contains(strings.ToLower(extractedToolName), strings.ToLower(t.Name())) ||
					strings.Contains(strings.ToLower(t.Name()), strings.ToLower(extractedToolName)) {
					matchingTool = t
					log.Debugf("Found matching tool: %s", t.Name())
					break
				}
			}
		}

		// Now look for JSON after the action marker
		jsonStart := strings.Index(actionText[actionIdx:], "{")
		if jsonStart != -1 {
			jsonStart += actionIdx
			jsonEnd := findMatchingCloseBrace(actionText, jsonStart)
			if jsonEnd != -1 && jsonEnd > jsonStart {
				jsonContent := actionText[jsonStart : jsonEnd+1]
				log.Debugf("Found JSON after action marker: %s", jsonContent)

				var toolInput map[string]interface{}
				if err := json.Unmarshal([]byte(jsonContent), &toolInput); err == nil {
					log.Debugf("Successfully parsed JSON as tool input: %v", toolInput)

					// If we have a matching tool, create the action
					if matchingTool != nil {
						// Convert arguments to correct types
						convertedInput, err := tool.ConvertArgumentsToCorrectTypes(
							toolInput,
							matchingTool.Parameters(),
						)
						if err == nil {
							toolInput = convertedInput
						} else {
							log.Debugf("Failed to convert arguments: %v", err)
						}

						// Create the action
						action := &Action{
							ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
							ThoughtID: thoughtID,
							ToolName:  matchingTool.Name(),
							ToolInput: toolInput,
							Timestamp: time.Now().Unix(),
						}

						log.Debugf("Created action from text with JSON after action marker: %s with input: %v",
							action.ToolName, action.ToolInput)
						return action, nil
					}
				} else {
					log.Debugf("Failed to parse JSON after action marker: %v", err)
				}
			}
		}
	}

	// First look for JSON objects in the response which might contain tools
	jsonStartIdx := strings.Index(actionText, "{")
	jsonEndIdx := strings.LastIndex(actionText, "}")

	log.Debugf("Parsing action from text: JSON bounds found at indices %d to %d", jsonStartIdx, jsonEndIdx)

	if jsonStartIdx != -1 && jsonEndIdx != -1 && jsonEndIdx > jsonStartIdx {
		potentialJSON := actionText[jsonStartIdx : jsonEndIdx+1]
		log.Debugf("Found potential JSON in response: %s", potentialJSON)

		// Enhanced JSON extraction - try to clean and parse the JSON
		potentialJSON = cleanActionJSON(potentialJSON)

		// Try to parse as a valid action with tool_name and tool_input
		var actionData struct {
			ToolName  string                 `json:"tool_name"`
			ToolInput map[string]interface{} `json:"tool_input"`
		}

		if err := json.Unmarshal([]byte(potentialJSON), &actionData); err == nil {
			if actionData.ToolName != "" {
				// Validate the tool name
				var validTool bool
				for _, t := range tools {
					if t.Name() == actionData.ToolName {
						validTool = true
						matchingTool = t
						break
					}
				}

				if validTool {
					log.Debugf("Successfully parsed tool_name/tool_input JSON format: tool=%s, input=%v",
						actionData.ToolName, actionData.ToolInput)

					// Convert arguments to correct types if possible
					if matchingTool != nil && actionData.ToolInput != nil {
						convertedInput, err := tool.ConvertArgumentsToCorrectTypes(
							actionData.ToolInput,
							matchingTool.Parameters(),
						)
						if err == nil {
							actionData.ToolInput = convertedInput
						} else {
							log.Debugf("Failed to convert arguments: %v", err)
						}
					}

					// Create the action
					action := &Action{
						ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
						ThoughtID: thoughtID,
						ToolName:  actionData.ToolName,
						ToolInput: actionData.ToolInput,
						Timestamp: time.Now().Unix(),
					}

					return action, nil
				} else {
					log.Debugf("Found tool_name '%s' in JSON but it's not a valid tool", actionData.ToolName)
				}
			}
		} else {
			log.Debugf("Failed to parse potential JSON as tool_name/tool_input format: %v", err)
		}

		// Enhanced JSON extraction - try alternative formats
		// Try for single parameter function call format: {"toolName": paramValue}
		if matchingTool != nil {
			var singleParamValue interface{}
			if err := json.Unmarshal([]byte(potentialJSON), &singleParamValue); err == nil {
				if mapValue, ok := singleParamValue.(map[string]interface{}); ok {
					// Check if it contains exactly the tool name as a key
					if toolValue, ok := mapValue[matchingTool.Name()]; ok && len(mapValue) == 1 {
						// Create a tool input with the primary parameter
						primaryParam := inferToolPrimaryParameter(matchingTool.Name(), matchingTool)
						toolInput := map[string]interface{}{
							primaryParam: toolValue,
						}

						// Convert types if needed
						convertedInput, err := tool.ConvertArgumentsToCorrectTypes(
							toolInput,
							matchingTool.Parameters(),
						)
						if err == nil {
							toolInput = convertedInput
						}

						// Create the action
						action := &Action{
							ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
							ThoughtID: thoughtID,
							ToolName:  matchingTool.Name(),
							ToolInput: toolInput,
							Timestamp: time.Now().Unix(),
						}
						return action, nil
					}
				}
			}
		}

		// If we have a matching tool already, try to parse the potentialJSON as direct parameters
		if matchingTool != nil {
			var toolInput map[string]interface{}
			if err := json.Unmarshal([]byte(potentialJSON), &toolInput); err == nil {
				// Check if it might be in format {"name": "toolName", "arguments": {...}}
				if name, hasName := toolInput["name"].(string); hasName {
					if name == matchingTool.Name() {
						if args, hasArgs := toolInput["arguments"].(map[string]interface{}); hasArgs {
							toolInput = args
							log.Debugf("Found name/arguments JSON format for tool: %s", name)
						} else if argsStr, hasArgsStr := toolInput["arguments"].(string); hasArgsStr {
							// Try to parse arguments as JSON
							var parsedArgs map[string]interface{}
							if err := json.Unmarshal([]byte(argsStr), &parsedArgs); err == nil {
								toolInput = parsedArgs
								log.Debugf("Found name/arguments(string) JSON format for tool: %s", name)
							} else {
								log.Debugf("Failed to parse arguments string as JSON: %v", err)
							}
						}
					}
				}

				// Convert arguments to correct types
				convertedInput, err := tool.ConvertArgumentsToCorrectTypes(
					toolInput,
					matchingTool.Parameters(),
				)
				if err == nil {
					toolInput = convertedInput
				} else {
					log.Debugf("Failed to convert tool inputs: %v", err)
				}

				// Create the action
				action := &Action{
					ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
					ThoughtID: thoughtID,
					ToolName:  matchingTool.Name(),
					ToolInput: toolInput,
					Timestamp: time.Now().Unix(),
				}

				log.Debugf("Created action using matching tool %s with direct parameter JSON", matchingTool.Name())
				return action, nil
			} else {
				log.Debugf("Failed to parse JSON as direct parameters: %v", err)
			}
		}
	}

	// Try using other JSON extraction method
	actionJSON := extractJSON(actionText)
	if actionJSON == "" {
		// Try to parse using ReAct format (Action: X, Action Input: Y)
		log.Debugf("No valid JSON found, attempting to parse ReAct format")
		reactJSON, err := parseReActFormat(actionText, matchingTool, tools)
		if err != nil {
			log.Warnf("Failed to parse ReAct format: %v", err)
			return nil, fmt.Errorf("failed to parse action from response: %w", err)
		}

		if reactJSON != "" {
			actionJSON = reactJSON
			log.Debugf("Successfully parsed ReAct format into JSON: %s", actionJSON)
		} else {
			log.Warnf("No valid JSON or ReAct format found in model response")
			return nil, fmt.Errorf("no valid JSON or ReAct format found in model response")
		}
	}

	// Parse the JSON
	var actionData struct {
		ToolName  string                 `json:"tool_name"`
		ToolInput map[string]interface{} `json:"tool_input"`
	}

	if err := json.Unmarshal([]byte(actionJSON), &actionData); err != nil {
		log.Errorf("Failed to parse action JSON: %v", err)
		return nil, fmt.Errorf("failed to parse action JSON: %w", err)
	}

	// Validate the tool name
	var validTool bool
	for _, t := range tools {
		if t.Name() == actionData.ToolName {
			validTool = true
			matchingTool = t
			break
		}
	}
	if !validTool {
		log.Errorf("Selected tool '%s' is not available", actionData.ToolName)
		return nil, fmt.Errorf("selected tool '%s' is not available", actionData.ToolName)
	}

	// Convert arguments to correct types if possible
	if matchingTool != nil && actionData.ToolInput != nil {
		convertedInput, err := tool.ConvertArgumentsToCorrectTypes(
			actionData.ToolInput,
			matchingTool.Parameters(),
		)
		if err == nil {
			actionData.ToolInput = convertedInput
		} else {
			log.Debugf("Failed to convert tool inputs: %v", err)
		}
	}

	// Create the action
	action := &Action{
		ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
		ThoughtID: thoughtID,
		ToolName:  actionData.ToolName,
		ToolInput: actionData.ToolInput,
		Timestamp: time.Now().Unix(),
	}

	log.Debugf("Successfully created action from JSON: %s with input: %v",
		action.ToolName, action.ToolInput)
	return action, nil
}

// findMatchingCloseBrace finds the matching closing brace for an opening brace
func findMatchingCloseBrace(text string, openIdx int) int {
	if openIdx >= len(text) || text[openIdx] != '{' {
		return -1
	}

	depth := 1
	for i := openIdx + 1; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseReActFormat parses ReAct format output (Action: X, Action Input: Y)
// and returns a structured JSON representation of it.
func parseReActFormat(text string, matchingTool tool.Tool, tools []tool.Tool) (string, error) {
	actionRegex := regexp.MustCompile(`(?i)Action:?\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:\n|$)`)
	actionInputRegex := regexp.MustCompile(`(?i)Action\s*Input:?\s*(.+?)(?:\n\n|\n\s*$|$)`)

	// Extract the action name
	actionMatches := actionRegex.FindStringSubmatch(text)
	if len(actionMatches) < 2 {
		return "", fmt.Errorf("no action found in text")
	}
	actionName := strings.TrimSpace(actionMatches[1])

	// Look up the tool
	var selectedTool tool.Tool
	if matchingTool != nil && strings.EqualFold(matchingTool.Name(), actionName) {
		selectedTool = matchingTool
	} else {
		// Find the matching tool in the set
		for _, t := range tools {
			if strings.EqualFold(t.Name(), actionName) {
				selectedTool = t
				break
			}
		}
	}

	if selectedTool == nil {
		return "", fmt.Errorf("tool '%s' not found", actionName)
	}

	// Extract the action input
	actionInputMatches := actionInputRegex.FindStringSubmatch(text)
	var actionInput string
	if len(actionInputMatches) >= 2 {
		actionInput = strings.TrimSpace(actionInputMatches[1])
	}

	// Enhanced logic for handling single string inputs
	if actionInput != "" {
		// Check if it has key=value structure (traditional parameters)
		if strings.Contains(actionInput, "=") {
			// Handle parameters in key=value format
			return parseReActKeyValueParams(actionName, actionInput)
		} else {
			// Check if the tool accepts a primary string parameter
			primaryParam := inferToolPrimaryParameter(actionName, selectedTool)
			if primaryParam != "" {
				// For simple string inputs, use the inferred parameter name
				params := map[string]interface{}{
					"tool_name":  actionName,
					"tool_input": actionInput,
				}
				paramsJSON, err := json.Marshal(params)
				if err != nil {
					return "", fmt.Errorf("failed to marshal parameters: %w", err)
				}
				return string(paramsJSON), nil
			}
		}
	}

	// Handle empty action input or fallback
	params := map[string]interface{}{
		"tool_name":  actionName,
		"tool_input": map[string]interface{}{},
	}

	// Parse key=value pairs if present
	if actionInput != "" {
		paramMap := parseReActInputParams(actionInput)
		if len(paramMap) > 0 {
			params["tool_input"] = paramMap
		}
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("failed to marshal parameters: %w", err)
	}

	return string(paramsJSON), nil
}

// Helper functions for parameter parsing
func parseReActKeyValueParams(actionName string, input string) (string, error) {
	paramMap := parseReActInputParams(input)
	params := map[string]interface{}{
		"tool_name":  actionName,
		"tool_input": paramMap,
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("failed to marshal parameters: %w", err)
	}

	return string(paramsJSON), nil
}

func parseReActInputParams(input string) map[string]interface{} {
	paramMap := make(map[string]interface{})

	// Split on commas but respect quotes
	parts := splitRespectingQuotes(input, ',')

	for _, part := range parts {
		// Check for key=value format
		keyValue := splitRespectingQuotes(part, '=')
		if len(keyValue) >= 2 {
			key := strings.TrimSpace(keyValue[0])
			value := strings.TrimSpace(strings.Join(keyValue[1:], "="))

			// Remove surrounding quotes if present
			if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
				(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
				value = value[1 : len(value)-1]
			}

			// Try to convert to specific types
			if converted, err := convertStringToType(value); err == nil {
				paramMap[key] = converted
			} else {
				paramMap[key] = value
			}
		} else {
			// If it's not in key=value format, store with a default parameter name
			if part = strings.TrimSpace(part); part != "" {
				paramMap["value"] = part
			}
		}
	}

	return paramMap
}

// convertStringToType tries to convert a string to a more specific type
func convertStringToType(s string) (interface{}, error) {
	// Try boolean
	if s == "true" {
		return true, nil
	} else if s == "false" {
		return false, nil
	}

	// Try number (integer first, then float)
	if i, err := strconv.Atoi(s); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}

	// Just return the string if no conversion is possible
	return s, fmt.Errorf("no conversion possible")
}

// splitRespectingQuotes splits a string by a delimiter but respects quotes
func splitRespectingQuotes(s string, delimiter rune) []string {
	var result []string
	var current strings.Builder
	inQuotes := false
	quoteChar := rune(0)

	for _, char := range s {
		if (char == '"' || char == '\'') && (quoteChar == 0 || quoteChar == char) {
			inQuotes = !inQuotes
			if inQuotes {
				quoteChar = char
			} else {
				quoteChar = 0
			}
			current.WriteRune(char)
		} else if char == delimiter && !inQuotes {
			result = append(result, current.String())
			current.Reset()
		} else {
			current.WriteRune(char)
		}
	}

	if current.Len() > 0 {
		result = append(result, current.String())
	}

	return result
}

// inferToolPrimaryParameter attempts to infer the primary parameter for a tool
func inferToolPrimaryParameter(toolName string, matchingTool tool.Tool) string {
	// First try to get the parameter name from the tool definition
	if matchingTool != nil {
		if def := matchingTool.GetDefinition(); def != nil {
			// First check for any required parameters
			required := def.RequiredParameters()
			if len(required) > 0 {
				return required[0] // Use first required parameter
			}

			// If no required parameters, try the first string parameter
			for name, prop := range def.Parameters {
				if prop.Type == "string" {
					return name // Prefer string parameters
				}
			}

			// If no string parameter, use the first parameter
			for name := range def.Parameters {
				return name // Use first parameter found
			}
		}
	}

	// Default to a generic parameter name
	return "input"
}

// cleanActionJSON cleans up any JSON that may have been corrupted by the model
func cleanActionJSON(s string) string {
	// Replace line breaks and extra whitespace
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")

	// Fix common formatting issues
	s = strings.ReplaceAll(s, "' ", "'")
	s = strings.ReplaceAll(s, " '", "'")
	s = strings.ReplaceAll(s, "\"", "\"")

	// Handle escaped quotes within already quoted strings
	s = strings.ReplaceAll(s, "\\\"", "\"")

	// Ensure proper JSON object structure
	if !strings.HasPrefix(strings.TrimSpace(s), "{") {
		s = "{" + s
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "}") {
		s = s + "}"
	}

	return s
}

// parseFormatWithDirectToolInput handles parsing format where tool_input might be a direct string
// instead of a structured object, which is common in some OpenAI-compatible models.
func parseFormatWithDirectToolInput(toolData map[string]interface{}, tools []tool.Tool) (*Action, bool) {
	toolName, hasToolName := toolData["tool_name"].(string)
	if !hasToolName {
		return nil, false
	}

	// Find the matching tool
	var matchingTool tool.Tool
	for _, t := range tools {
		if t.Name() == toolName {
			matchingTool = t
			break
		}
	}

	if matchingTool == nil {
		log.Warnf("Tool '%s' not found in available tools", toolName)
		return nil, false
	}

	// Initialize tool input map
	toolInput := make(map[string]interface{})

	// Case 1: tool_input is a direct string that needs parsing
	if toolInputStr, ok := toolData["tool_input"].(string); ok {
		log.Debugf("Found direct string tool_input: %s", toolInputStr)

		// Try to parse as JSON first
		if err := json.Unmarshal([]byte(toolInputStr), &toolInput); err != nil {
			log.Debugf("Direct tool_input is not valid JSON: %v", err)

			// Get parameter info from tool definition
			var primaryParam string

			// Try to get the parameter name from the tool definition
			if def := matchingTool.GetDefinition(); def != nil {
				// First check for any required parameters
				required := def.RequiredParameters()
				if len(required) > 0 {
					primaryParam = required[0] // Use first required parameter
				} else {
					// If no required parameters, use first parameter found
					for name := range def.Parameters {
						primaryParam = name
						break
					}
				}
			}

			// If we couldn't get a parameter from the definition, use our inference function
			if primaryParam == "" {
				primaryParam = inferToolPrimaryParameter(toolName, matchingTool)
			}

			// Create parameter map with the direct string value
			toolInput = map[string]interface{}{
				primaryParam: toolInputStr,
			}
			log.Debugf("Mapped direct string value to parameter '%s'", primaryParam)
		}
	} else if directInput, ok := toolData["tool_input"].(map[string]interface{}); ok {
		// Case 2: tool_input is already a map
		toolInput = directInput
		log.Debugf("Found map-type tool_input: %v", toolInput)
	} else if len(toolData) > 2 {
		// Case 3: Extra parameters are included at the top level
		for k, v := range toolData {
			// Skip the tool_name and empty tool_input
			if k != "tool_name" && k != "tool_input" {
				toolInput[k] = v
			}
		}
		log.Debugf("Extracted parameters from top-level keys: %v", toolInput)
	} else if toolData["tool_input"] == nil {
		// Case 4: tool_input is present but null - this happens with some models
		// Try to extract any parameters at top level
		for k, v := range toolData {
			if k != "tool_name" && k != "tool_input" {
				toolInput[k] = v
			}
		}
		log.Debugf("Found null tool_input, extracted any top-level params: %v", toolInput)
	} else {
		// Handle empty or missing tool_input by looking for a default parameter
		log.Debugf("No valid tool_input found, checking for default parameter")

		// Try to create an empty parameter set based on tool definition
		if def := matchingTool.GetDefinition(); def != nil {
			// If tool has parameters with defaults, use those
			for name, prop := range def.Parameters {
				if prop.Default != nil {
					toolInput[name] = prop.Default
					log.Debugf("Using default value for parameter '%s': %v", name, prop.Default)
				}
			}
		}
	}

	// Validate and convert parameters to the correct types
	validatedInput, err := tool.ValidateParameters(toolInput, matchingTool)
	if err != nil {
		log.Warnf("Parameter validation failed: %v", err)
		// Continue with original parameters rather than failing
		validatedInput = toolInput
	} else {
		log.Debugf("Successfully validated parameters")
	}

	// Create the action
	action := &Action{
		ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
		ToolName:  matchingTool.Name(),
		ToolInput: validatedInput,
		Timestamp: time.Now().Unix(),
	}

	log.Debugf("Successfully parsed action using direct tool input: %s with input: %v",
		action.ToolName, action.ToolInput)
	return action, true
}

// inferActionFromThought provides a mechanism to infer an action from the thought
func inferActionFromThought(thought *Thought, tools []tool.Tool) *Action {
	// No thought to work with
	if thought == nil || thought.Content == "" {
		return nil
	}

	// Scan through the thought for mentions of tools and action indicators
	thoughtText := strings.ToLower(thought.Content)

	// Common patterns that suggest tool usage
	toolMentionPatterns := []string{
		"I will use the `(.*?)` tool",
		"use the `(.*?)` tool",
		"use the (.*?) tool",
		"using the (.*?) tool",
		"I will use (.*?) to",
		"use (.*?) to",
		"using (.*?) to",
		"call (.*?) with",
		"call the (.*?) tool",
	}

	// Try to find a tool mention
	mentionedTools := make(map[string]int)

	// Count tool mentions
	for _, toolName := range scanForToolNames(thoughtText, tools, toolMentionPatterns) {
		mentionedTools[toolName]++
	}

	// If we found mentioned tools, use the most mentioned one
	if len(mentionedTools) > 0 {
		// Find the most mentioned tool
		var bestToolName string
		var maxMentions int
		for toolName, mentions := range mentionedTools {
			if mentions > maxMentions {
				bestToolName = toolName
				maxMentions = mentions
			}
		}

		// Find the actual tool
		var selectedTool tool.Tool
		for _, t := range tools {
			if strings.EqualFold(t.Name(), bestToolName) {
				selectedTool = t
				break
			}
		}

		if selectedTool != nil {
			// Try to extract parameter values for the selected tool
			params := inferParametersFromThought(thoughtText, selectedTool)

			// Create action
			return &Action{
				ID:        fmt.Sprintf("inferred-action-%d", time.Now().UnixNano()),
				ThoughtID: thought.ID,
				ToolName:  selectedTool.Name(),
				ToolInput: params,
				Timestamp: time.Now().Unix(),
			}
		}
	}

	// Fallback to any explicitly mentioned tools
	for _, t := range tools {
		if strings.Contains(thoughtText, strings.ToLower(t.Name())) {
			// Tool is mentioned, try to extract parameters
			params := inferParametersFromThought(thoughtText, t)

			return &Action{
				ID:        fmt.Sprintf("inferred-action-%d", time.Now().UnixNano()),
				ThoughtID: thought.ID,
				ToolName:  t.Name(),
				ToolInput: params,
				Timestamp: time.Now().Unix(),
			}
		}
	}

	return nil
}

// scanForToolNames searches for tool name mentions in text using provided patterns
func scanForToolNames(text string, tools []tool.Tool, patterns []string) []string {
	var result []string

	// Check each pattern
	for _, pattern := range patterns {
		re := regexp.MustCompile("(?i)" + pattern)
		matches := re.FindAllStringSubmatch(text, -1)

		for _, match := range matches {
			if len(match) > 1 {
				extractedName := strings.TrimSpace(match[1])

				// Check against known tools
				for _, t := range tools {
					toolName := strings.ToLower(t.Name())
					extractedLower := strings.ToLower(extractedName)

					// Check for exact match or partial match
					if toolName == extractedLower ||
						strings.Contains(toolName, extractedLower) ||
						strings.Contains(extractedLower, toolName) {
						result = append(result, t.Name())
						break
					}
				}
			}
		}
	}

	return result
}

// inferParametersFromThought tries to extract parameter values from the thought text
func inferParametersFromThought(text string, t tool.Tool) map[string]interface{} {
	params := make(map[string]interface{})

	// Get parameter schema
	schema := t.Parameters()
	if schema == nil {
		return params
	}

	// Extract properties
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return params
	}

	// For each parameter, try to find values in the text
	for paramName, prop := range properties {
		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}

		// Get description and type
		description, _ := propMap["description"].(string)
		paramType, _ := propMap["type"].(string)

		// For location-related parameters, regardless of tool type
		if isLocationParameter(paramName, description) {
			// Look for common city names in the text
			cityPattern := `(?i)(in|for|at|about) (?:the )?(?:city of )?["']?([A-Z][a-z]+(?:\s[A-Z][a-z]+)*)["']?`
			re := regexp.MustCompile(cityPattern)
			matches := re.FindStringSubmatch(text)

			if len(matches) > 2 {
				params[paramName] = matches[2]
			}
		}

		// For numerical parameters
		if paramType == "number" || paramType == "integer" {
			// Look for numbers related to this parameter
			numberPattern := fmt.Sprintf(`(?i)%s[^\d]*?(\d+(?:\.\d+)?)`, paramName)
			re := regexp.MustCompile(numberPattern)
			matches := re.FindStringSubmatch(text)

			if len(matches) > 1 {
				// Try to convert to number
				if val, err := strconv.ParseFloat(matches[1], 64); err == nil {
					if paramType == "integer" {
						params[paramName] = int(val)
					} else {
						params[paramName] = val
					}
				}
			}
		}
	}

	return params
}

// extractJSON extracts JSON from a string that might contain other text.
func extractJSON(text string) string {
	log.Debugf("Attempting to extract JSON from text")

	// First check for markdown code blocks with JSON
	jsonBlockPattern := "```json\\s*([\\s\\S]*?)\\s*```"
	re := regexp.MustCompile(jsonBlockPattern)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		jsonContent := strings.TrimSpace(matches[1])
		log.Debugf("Found JSON in markdown code block: %s", jsonContent)

		// Validate that it's valid JSON
		var js interface{}
		if err := json.Unmarshal([]byte(jsonContent), &js); err == nil {
			return jsonContent
		} else {
			log.Debugf("Extracted content is not valid JSON: %v", err)
		}
	}

	// Find the first '{' and the last '}'
	start := strings.Index(text, "{")
	if start == -1 {
		log.Debugf("No opening brace found in text")
		return ""
	}

	end := strings.LastIndex(text, "}")
	if end == -1 || end < start {
		log.Debugf("No closing brace found after opening brace")
		return ""
	}

	potentialJSON := text[start : end+1]
	log.Debugf("Found potential JSON between indexes %d and %d", start, end)

	// Validate that it's valid JSON
	var js interface{}
	if err := json.Unmarshal([]byte(potentialJSON), &js); err != nil {
		log.Debugf("Potential JSON is not valid: %v", err)

		// Try cleaning it
		cleanedJSON := cleanActionJSON(potentialJSON)
		if err := json.Unmarshal([]byte(cleanedJSON), &js); err != nil {
			log.Debugf("Cleaned JSON is still not valid: %v", err)
			return ""
		}
		log.Debugf("Cleaned JSON is valid: %s", cleanedJSON)
		return cleanedJSON
	}

	log.Debugf("Extracted valid JSON: %s", potentialJSON)
	return potentialJSON
}

// isLocationParameter checks if a parameter appears to be location-related
func isLocationParameter(paramName string, description string) bool {
	// Check parameter name for location-related terms
	locationTerms := []string{"location", "place", "city", "address", "destination", "where", "country"}

	paramNameLower := strings.ToLower(paramName)
	descriptionLower := strings.ToLower(description)

	// Check if parameter name contains any location terms
	for _, term := range locationTerms {
		if strings.Contains(paramNameLower, term) {
			return true
		}
	}

	// Check if description contains any location terms
	for _, term := range locationTerms {
		if strings.Contains(descriptionLower, term) {
			return true
		}
	}

	return false
}
