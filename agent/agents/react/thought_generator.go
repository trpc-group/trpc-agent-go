package react

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// LLMThoughtGenerator generates thoughts using an LLM.
type LLMThoughtGenerator struct {
	model       model.Model
	prompting   ThoughtPromptStrategy
	thoughtType string
}

// ThoughtPromptStrategy represents a strategy for prompting thought generation.
type ThoughtPromptStrategy interface {
	// BuildThoughtPrompt builds a prompt for thought generation.
	BuildThoughtPrompt(history []*message.Message, previousCycles []*Cycle) string
}

// DefaultThoughtPromptStrategy is the default strategy for thought prompting.
type DefaultThoughtPromptStrategy struct {
	tools []tool.Tool
}

// NewDefaultThoughtPromptStrategy creates a new default thought prompt strategy.
func NewDefaultThoughtPromptStrategy(tools []tool.Tool) *DefaultThoughtPromptStrategy {
	return &DefaultThoughtPromptStrategy{
		tools: tools,
	}
}

// BuildThoughtPrompt builds a prompt for thought generation.
func (s *DefaultThoughtPromptStrategy) BuildThoughtPrompt(history []*message.Message, previousCycles []*Cycle) string {
	var prompt strings.Builder

	// Include tool information
	prompt.WriteString("You are a ReAct agent that thinks step by step. Available tools:\n")
	for _, t := range s.tools {
		prompt.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))
		// Could add parameter information here if needed
	}

	prompt.WriteString("\nYou must analyze the problem and think about how to solve it. Generate a thought that explains your reasoning.\n")
	prompt.WriteString("If you have enough information to provide a final answer, include 'Final Answer:' in your thought, followed by the answer.\n")

	if len(previousCycles) > 0 {
		prompt.WriteString("\nPrevious reasoning steps:\n")
		for i, cycle := range previousCycles {
			prompt.WriteString(fmt.Sprintf("Step %d:\n", i+1))
			prompt.WriteString(fmt.Sprintf("- Thought: %s\n", cycle.Thought.Content))
			if cycle.Action != nil {
				prompt.WriteString(fmt.Sprintf("- Action: %s\n", cycle.Action.ToolName))
				prompt.WriteString(fmt.Sprintf("- Action Input: %v\n", cycle.Action.ToolInput))
			}
			if cycle.Observation != nil {
				if cycle.Observation.IsError {
					promptVal, ok := cycle.Observation.ToolOutput["error"]
					if ok {
						prompt.WriteString(fmt.Sprintf("- Observation: Error - %v\n", promptVal))
					} else {
						prompt.WriteString("- Observation: An error occurred.\n")
					}
				} else {
					promptVal, ok := cycle.Observation.ToolOutput["output"]
					if ok {
						prompt.WriteString(fmt.Sprintf("- Observation: %v\n", promptVal))
					} else {
						prompt.WriteString("- Observation: Tool execution was successful.\n")
					}
				}
			}
			prompt.WriteString("\n")
		}
	}

	prompt.WriteString("\nNow, what is your next thought?\n")
	return prompt.String()
}

// NewLLMThoughtGenerator creates a new LLM-based thought generator.
func NewLLMThoughtGenerator(model model.Model, prompting ThoughtPromptStrategy) *LLMThoughtGenerator {
	return &LLMThoughtGenerator{
		model:       model,
		prompting:   prompting,
		thoughtType: "reasoning",
	}
}

// Generate generates a thought based on the conversation history.
func (g *LLMThoughtGenerator) Generate(ctx context.Context, history []*message.Message, previousCycles []*Cycle) (*Thought, error) {
	if g.model == nil {
		return nil, fmt.Errorf("model is required for LLMThoughtGenerator")
	}

	// Build the prompt for thought generation
	promptText := ""
	if g.prompting != nil {
		promptText = g.prompting.BuildThoughtPrompt(history, previousCycles)
	} else {
		// Fallback to a simple default prompt if no strategy is provided
		promptText = "Given the conversation history, think step by step about how to proceed. If you have enough information to provide a final answer, include 'Final Answer:' in your response."
	}

	// Create a system message with the prompt
	promptMsg := message.NewSystemMessage(promptText)

	// Combine prompt with history for the model input
	modelInput := make([]*message.Message, 0, len(history)+1)
	modelInput = append(modelInput, promptMsg)
	modelInput = append(modelInput, history...)

	// Generate the thought using the model
	opts := model.DefaultOptions()
	// Disable tool calls for thought generation since we want a text response
	opts.EnableToolCalls = false
	response, err := g.model.GenerateWithMessages(ctx, modelInput, opts)
	if err != nil {
		return nil, fmt.Errorf("thought generation failed: %w", err)
	}

	thoughtContent := ""
	if len(response.Messages) > 0 {
		thoughtContent = response.Messages[0].Content
	} else if response.Text != "" {
		thoughtContent = response.Text
	} else {
		return nil, fmt.Errorf("model returned empty response")
	}

	// Create the thought
	thought := &Thought{
		ID:        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
		Content:   thoughtContent,
		Timestamp: time.Now().Unix(),
	}

	return thought, nil
}

// TemplateThoughtGenerator generates thoughts using templates.
type TemplateThoughtGenerator struct {
	templates map[string]string
}

// NewTemplateThoughtGenerator creates a new template-based thought generator.
func NewTemplateThoughtGenerator(templates map[string]string) *TemplateThoughtGenerator {
	return &TemplateThoughtGenerator{
		templates: templates,
	}
}

// Generate generates a thought using a template.
// This is primarily useful for testing or simple scenarios.
func (g *TemplateThoughtGenerator) Generate(ctx context.Context, history []*message.Message, previousCycles []*Cycle) (*Thought, error) {
	templateKey := "default"

	// Determine which template to use based on context
	latestUserMsg := findLatestUserMessage(history)
	if latestUserMsg != nil {
		for key := range g.templates {
			if strings.Contains(strings.ToLower(latestUserMsg.Content), strings.ToLower(key)) {
				templateKey = key
				break
			}
		}
	}

	// Get the template or use default
	template, exists := g.templates[templateKey]
	if !exists {
		template, exists = g.templates["default"]
		if !exists {
			return nil, fmt.Errorf("no default template found")
		}
	}

	// Create the thought
	thought := &Thought{
		ID:        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
		Content:   template,
		Timestamp: time.Now().Unix(),
	}

	return thought, nil
}

// findLatestUserMessage finds the most recent user message in the history.
func findLatestUserMessage(history []*message.Message) *message.Message {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == message.RoleUser {
			return history[i]
		}
	}
	return nil
}
