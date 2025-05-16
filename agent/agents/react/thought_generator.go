// Package react defines the interfaces and core components for ReAct agents.
package react

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
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
	prompt.WriteString("Think through the following request step by step to determine the best action to take " +
		"or output 'Final Answer: your answer here' if you have gathered all necessary information. ")

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
		prompt.WriteString("\n=== PREVIOUS THOUGHTS AND ACTIONS, OBSERVATIONS ===\n")
		prompt.WriteString("Remember to carefully review these past actions and particularly any error messages to avoid repeating mistakes.\n\n")

		for i, cycle := range history {
			if cycle.Action != nil {
				// Format the tool input nicely
				inputJSON, _ := json.MarshalIndent(cycle.Action.ToolInput, "", "  ")

				prompt.WriteString(fmt.Sprintf("Step %d:\n", i+1))

				// Add previous thought if available
				if cycle.Thought != nil {
					prompt.WriteString(fmt.Sprintf("Previous Thought: %s\n", cycle.Thought.Content))
				}

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
								prompt.WriteString("→ Hint: You must use exact parameter names.\n")
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

		prompt.WriteString("=== END OF PREVIOUS THOUGHTS AND ACTIONS, OBSERVATIONS ===\n\n")

		// Add specific guidance based on history analysis
		if hasRepeatedErrors(history) {
			prompt.WriteString("IMPORTANT: You've made similar errors multiple times. Please carefully check parameter names and values.\n")
		}
	}
	prompt.WriteString("\nNow, think step by step about how to respond to the user's query, making effective use of the available tools or " +
		"output 'Final Answer: your answer here' if you have gathered all necessary information.\n")
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

// NewLLMThoughtGenerator creates a new LLM-based thought generator.
func NewLLMThoughtGenerator(model model.Model, strategy ThoughtPromptStrategy, format ThoughtFormat) *LLMThoughtGenerator {
	return &LLMThoughtGenerator{
		model:     model,
		prompting: strategy,
		format:    format,
	}
}

// Generate generates a thought using an LLM.
func (g *LLMThoughtGenerator) Generate(
	ctx context.Context,
	messages []*message.Message,
	history []*Cycle,
	tools []tool.Tool,
) (*Thought, error) {
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
	log.Debugf("Thought prompt: %s", promptText)

	// Create a system message with instructions
	userMsg := message.NewUserMessage(promptText)

	// Generate the thought using the model
	opts := model.DefaultOptions()
	// Enable tool calls so the model can directly output structured tool calls if appropriate
	opts.EnableToolCalls = g.model.SupportsToolCalls()
	response, err := g.model.GenerateWithMessages(ctx, []*message.Message{userMsg}, opts)
	if err != nil {
		return nil, fmt.Errorf("thought generation failed: %w", err)
	}

	// Create a structured Thought object
	thought := &Thought{
		ID:             fmt.Sprintf("thought-%d", time.Now().UnixNano()),
		Content:        response.Text,
		Type:           "reasoning",
		Timestamp:      time.Now().Unix(),
		PreviousAction: previousAction,
	}

	log.Infof("Thought: %s, tool calls: %v", thought.Content, response.ToolCalls)

	// If the model provided structured tool calls, attach them to the thought
	if len(response.ToolCalls) > 0 {
		toolCall := response.ToolCalls[0] // Use the first tool call

		// Parse arguments if possible
		var toolInput map[string]interface{}
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &toolInput); err == nil {
			thought.SuggestedAction = &Action{
				ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
				ThoughtID: thought.ID,
				ToolName:  toolCall.Function.Name,
				ToolInput: toolInput,
				Timestamp: time.Now().Unix(),
			}
		}
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
