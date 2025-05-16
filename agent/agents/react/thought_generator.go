// Package react defines the interfaces and core components for ReAct agents.
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

// ThoughtFormat specifies the format for reasoning traces.
type ThoughtFormat string

const (
	// ThoughtFormatFree indicates free-form reasoning.
	ThoughtFormatFree ThoughtFormat = "free"

	// ThoughtFormatStructured indicates structured reasoning (e.g., plan-oriented).
	ThoughtFormatStructured ThoughtFormat = "structured"
)

// LLMThoughtGenerator generates thoughts using an LLM.
type LLMThoughtGenerator struct {
	model     model.Model
	prompting ThoughtPromptStrategy
	format    ThoughtFormat
}

// ThoughtPromptStrategy represents a strategy for prompting thought generation.
type ThoughtPromptStrategy interface {
	// BuildThoughtPrompt builds a prompt for thought generation.
	BuildThoughtPrompt(msg *message.Message, history []*Cycle, tools []tool.Tool, format ThoughtFormat) string
}

// DefaultThoughtPromptStrategy is the default strategy for thought prompting.
type DefaultThoughtPromptStrategy struct {
}

// NewDefaultThoughtPromptStrategy creates a new default thought prompt strategy.
func NewDefaultThoughtPromptStrategy() *DefaultThoughtPromptStrategy {
	return &DefaultThoughtPromptStrategy{}
}

// BuildThoughtPrompt builds a prompt for thought generation.
func (s *DefaultThoughtPromptStrategy) BuildThoughtPrompt(msg *message.Message, history []*Cycle, tools []tool.Tool, format ThoughtFormat) string {
	var prompt strings.Builder

	prompt.WriteString("Think through the following request step by step to determine the best action to take. " +
		"Use the available tools to help you solve the problem. You have the following tools:\n\n")

	// List available tools
	schemaProcessor := tool.NewSchemaProcessor(tools)
	for _, t := range tools {
		prompt.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))

		// Add example parameters if available
		params, err := schemaProcessor.GetParametersForTool(t.Name())
		if err == nil && len(params) > 0 {
			prompt.WriteString("  Parameters:\n")
			for _, param := range params {
				required := ""
				if param.Required {
					required = " (required)"
				}
				prompt.WriteString(fmt.Sprintf("  - %s: %s%s\n", param.Name, param.Description, required))
			}
		}
	}

	prompt.WriteString("\nThought process format: ")
	if format == ThoughtFormatStructured {
		prompt.WriteString("Start with a detailed analysis, then formulate a plan, and finally present your reasoning in a step-by-step manner.\n")
	} else {
		prompt.WriteString("Think through the problem step by step, considering the available information and tools that could help you solve it.\n")
	}

	// Add user query
	prompt.WriteString("\nUser query: ")
	prompt.WriteString(msg.Content)
	prompt.WriteString("\n")

	// PROMINENTLY include the previous cycle observations
	if len(history) > 0 {
		prompt.WriteString("\n=== PREVIOUS ACTIONS AND OBSERVATIONS ===\n")
		prompt.WriteString("Remember to carefully review these past actions and particularly any error messages to avoid repeating mistakes.\n\n")
		
		for i, cycle := range history {
			if cycle.Action != nil {
				// Format the tool input nicely
				inputJSON, _ := json.MarshalIndent(cycle.Action.ToolInput, "", "  ")
				
				prompt.WriteString(fmt.Sprintf("Step %d:\n", i+1))
				prompt.WriteString(fmt.Sprintf("Tool Used: %s\n", cycle.Action.ToolName))
				prompt.WriteString(fmt.Sprintf("Input Parameters: %s\n", string(inputJSON)))
				
				// Extract and format observation content clearly
				if cycle.Observation != nil {
					if cycle.Observation.IsError {
						if errMsg, ok := cycle.Observation.ToolOutput["error"]; ok {
							prompt.WriteString(fmt.Sprintf("RESULT - ERROR: %v\n", errMsg))
							
							// Provide extra guidance on common error types
							errString := fmt.Sprintf("%v", errMsg)
							if strings.Contains(strings.ToLower(errString), "required") && 
							   strings.Contains(strings.ToLower(errString), "location") {
								prompt.WriteString("→ Hint: You must use exact parameter names. For weather lookups, use 'location' parameter with city name.\n")
							} else if strings.Contains(strings.ToLower(errString), "not found") {
								prompt.WriteString("→ Hint: Check that you're using the correct tool name and parameters.\n")
							}
						} else {
							prompt.WriteString("RESULT - ERROR: An error occurred with this tool call.\n")
						}
					} else {
						if output, ok := cycle.Observation.ToolOutput["output"]; ok {
							prompt.WriteString(fmt.Sprintf("RESULT - SUCCESS: %v\n", output))
						} else {
							prompt.WriteString("RESULT - SUCCESS: Tool executed but returned no specific output.\n")
						}
					}
				} else {
					prompt.WriteString("RESULT: No observation recorded for this action.\n")
				}
				prompt.WriteString("\n")
			}
		}
		
		prompt.WriteString("=== END OF PREVIOUS ACTIONS AND OBSERVATIONS ===\n\n")
		
		// Add specific guidance based on history analysis
		if hasRepeatedErrors(history) {
			prompt.WriteString("IMPORTANT: You've made similar errors multiple times. Please carefully check parameter names and values.\n")
			
			// If we can identify a specific parameter name error, add specific guidance
			if paramNameError := identifyParameterNameError(history); paramNameError != "" {
				prompt.WriteString(paramNameError + "\n")
			}
		}
	}

	prompt.WriteString("\nNow, think step by step about how to respond to the user's query, making effective use of the available tools.\n")
	prompt.WriteString("If you've already gathered all necessary information, consider providing a Final Answer.\n")

	return prompt.String()
}

// Helper function to detect if the agent is repeating the same errors
func hasRepeatedErrors(history []*Cycle) bool {
	if len(history) < 2 {
		return false
	}
	
	errorCount := 0
	for _, cycle := range history {
		if cycle.Observation != nil && cycle.Observation.IsError {
			errorCount++
		}
	}
	
	// If more than half of the cycles have errors, consider it a pattern
	return errorCount >= len(history)/2
}

// Helper function to identify specific parameter name errors
func identifyParameterNameError(history []*Cycle) string {
	for i := len(history) - 1; i >= 0; i-- {
		cycle := history[i]
		if cycle.Observation != nil && cycle.Observation.IsError && cycle.Action != nil {
			if errMsg, ok := cycle.Observation.ToolOutput["error"].(string); ok {
				errLower := strings.ToLower(errMsg)
				
				// Check for common parameter name errors
				if strings.Contains(errLower, "location") && strings.Contains(errLower, "required") {
					// Check if they're using wrong parameter names
					if _, hasCity := cycle.Action.ToolInput["city"]; hasCity {
						return "Note: Use 'location' parameter instead of 'city' for weather lookups."
					} else if _, hasCityName := cycle.Action.ToolInput["city_name"]; hasCityName {
						return "Note: Use 'location' parameter instead of 'city_name' for weather lookups."
					}
				}
			}
		}
	}
	return ""
}

// formatToolInput formats tool input as JSON for display.
func formatToolInput(input map[string]interface{}) (string, error) {
	if len(input) == 0 {
		return "{}", nil
	}

	// Simple formatting for small inputs
	parts := make([]string, 0, len(input))
	for k, v := range input {
		parts = append(parts, fmt.Sprintf(`"%s": %v`, k, formatValue(v)))
	}

	return "{" + strings.Join(parts, ", ") + "}", nil
}

// formatValue formats a value for display in JSON.
func formatValue(v interface{}) string {
	if v == nil {
		return "null"
	}

	switch val := v.(type) {
	case string:
		return fmt.Sprintf(`"%s"`, val)
	case bool, int, int64, float64, float32:
		return fmt.Sprintf("%v", val)
	default:
		// For complex types, just use %v
		return fmt.Sprintf("%v", val)
	}
}

// NewLLMThoughtGenerator creates a new LLM-based thought generator.
func NewLLMThoughtGenerator(model model.Model, strategy ThoughtPromptStrategy, format ThoughtFormat) *LLMThoughtGenerator {
	return &LLMThoughtGenerator{
		model:     model,
		prompting: strategy,
		format:    format,
	}
}

// Generate generates a thought using an LLM.
func (g *LLMThoughtGenerator) Generate(ctx context.Context, messages []*message.Message, history []*Cycle) (*Thought, error) {
	if g.model == nil {
		return nil, fmt.Errorf("model is required for thought generation")
	}

	// Find the last user message if any
	var msg *message.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			msg = messages[i]
			break
		}
	}
	if msg == nil && len(messages) > 0 {
		msg = messages[len(messages)-1]
	}
	if msg == nil {
		return nil, fmt.Errorf("no message found for thought generation")
	}

	// Get the tools from the first cycle if available, or use empty slice
	var tools []tool.Tool
	// We'll use the tools inferred from elsewhere instead
	
	// Find the previous action from the last cycle, if available
	var previousAction *Action
	if len(history) > 0 {
		lastCycle := history[len(history)-1]
		if lastCycle != nil && lastCycle.Action != nil {
			previousAction = lastCycle.Action
		}
	}

	// Build the prompt for thought generation
	promptText := ""
	if g.prompting != nil {
		promptText = g.prompting.BuildThoughtPrompt(msg, history, tools, g.format)
	} else {
		promptText = fmt.Sprintf("Think about how to respond to this: %s", msg.Content)
	}

	// Create a system message with instructions
	sysMsg := message.NewSystemMessage(promptText)

	// Generate the thought using the model
	opts := model.DefaultOptions()
	// We're not asking for tool calls in thought generation phase - this is handled by ActionSelector
	opts.EnableToolCalls = false
	response, err := g.model.GenerateWithMessages(ctx, []*message.Message{sysMsg}, opts)
	if err != nil {
		return nil, fmt.Errorf("thought generation failed: %w", err)
	}

	// Extract the text from the response
	thoughtText := ""
	if len(response.Messages) > 0 && response.Messages[0].Content != "" {
		thoughtText = response.Messages[0].Content
	} else if response.Text != "" {
		thoughtText = response.Text
	} else if len(response.ToolCalls) > 0 {
		// Handle cases where model returns empty content but has tool calls
		// This is common with OpenAI-compatible models
		var toolCallsText strings.Builder
		toolCallsText.WriteString("I need to use a tool to answer this question. ")
		
		for i, toolCall := range response.ToolCalls {
			if i > 0 {
				toolCallsText.WriteString("\n\n")
			}
			toolCallsText.WriteString(fmt.Sprintf("I'll use the %s tool", toolCall.Function.Name))
			
			if toolCall.Function.Arguments != "" && toolCall.Function.Arguments != "{}" {
				toolCallsText.WriteString(fmt.Sprintf(" with these parameters: %s", toolCall.Function.Arguments))
			}
			toolCallsText.WriteString(".")
		}
		
		thoughtText = toolCallsText.String()
	} else {
		return nil, fmt.Errorf("model returned empty response for thought generation")
	}

	// Create a structured Thought object
	thought := &Thought{
		ID:             fmt.Sprintf("thought-%d", time.Now().UnixNano()),
		Content:        thoughtText,
		Type:           "reasoning",
		Timestamp:      time.Now().Unix(),
		PreviousAction: previousAction,
	}

	// For structured thoughts, parse the plan state if present
	if g.format == ThoughtFormatStructured {
		// PlanState parsing logic would go here
		// For example, extracting goals, tasks, or structured information from the thought
	}

	return thought, nil
}

// RuleBasedThoughtGenerator generates thoughts using predefined rules.
type RuleBasedThoughtGenerator struct {
	templates map[string]string
	fallback  string
}

// NewRuleBasedThoughtGenerator creates a new rule-based thought generator.
func NewRuleBasedThoughtGenerator(templates map[string]string, fallback string) *RuleBasedThoughtGenerator {
	return &RuleBasedThoughtGenerator{
		templates: templates,
		fallback:  fallback,
	}
}

// Generate generates a thought using predefined rules.
func (g *RuleBasedThoughtGenerator) Generate(ctx context.Context, messages []*message.Message, history []*Cycle) (*Thought, error) {
	// Find the last user message if any
	var msg *message.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			msg = messages[i]
			break
		}
	}
	if msg == nil && len(messages) > 0 {
		msg = messages[len(messages)-1]
	}
	if msg == nil {
		return nil, fmt.Errorf("no message found for thought generation")
	}

	content := msg.Content
	
	// Find the previous action from the last cycle, if available
	var previousAction *Action
	if len(history) > 0 {
		lastCycle := history[len(history)-1]
		if lastCycle != nil && lastCycle.Action != nil {
			previousAction = lastCycle.Action
		}
	}

	// Select the template based on keywords
	template := g.fallback
	for keyword, tmpl := range g.templates {
		if strings.Contains(strings.ToLower(content), strings.ToLower(keyword)) {
			template = tmpl
			break
		}
	}

	// Replace placeholders in the template
	thoughtText := strings.ReplaceAll(template, "{{input}}", content)

	// Create the thought
	thought := &Thought{
		ID:             fmt.Sprintf("thought-%d", time.Now().UnixNano()),
		Content:        thoughtText,
		Type:           "rule_based",
		Timestamp:      time.Now().Unix(),
		PreviousAction: previousAction,
	}

	return thought, nil
}
