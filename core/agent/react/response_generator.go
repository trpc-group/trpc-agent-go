package react

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// LLMResponseGenerator generates responses using an LLM.
type LLMResponseGenerator struct {
	model     model.Model
	prompting ResponsePromptStrategy
}

// ResponsePromptStrategy represents a strategy for prompting response generation.
type ResponsePromptStrategy interface {
	// BuildResponsePrompt builds a prompt for response generation.
	BuildResponsePrompt(userQuery string, history []*message.Message, cycles []*Cycle) string
}

// DefaultResponsePromptStrategy is the default strategy for response prompting.
type DefaultResponsePromptStrategy struct {
	includeFullThoughtProcess bool
}

// NewDefaultResponsePromptStrategy creates a new default response prompt strategy.
func NewDefaultResponsePromptStrategy(includeFullThoughtProcess bool) *DefaultResponsePromptStrategy {
	return &DefaultResponsePromptStrategy{
		includeFullThoughtProcess: includeFullThoughtProcess,
	}
}

// BuildResponsePrompt builds a prompt for response generation.
func (s *DefaultResponsePromptStrategy) BuildResponsePrompt(userQuery string, history []*message.Message, cycles []*Cycle) string {
	var prompt strings.Builder

	prompt.WriteString("Generate a final response to the user based on the reasoning process and information gathered. The response should be clear, concise, and directly address the user's query.\n\n")

	prompt.WriteString("User query: ")
	prompt.WriteString(userQuery)
	prompt.WriteString("\n\n")

	if s.includeFullThoughtProcess && len(cycles) > 0 {
		prompt.WriteString("Reasoning process:\n")
		for i, cycle := range cycles {
			prompt.WriteString(fmt.Sprintf("Step %d:\n", i+1))

			if cycle.Thought != nil {
				prompt.WriteString(fmt.Sprintf("- Thought: %s\n", cycle.Thought.Content))
			}
			if cycle.Actions != nil {
				for i, action := range cycle.Actions {
					observation := cycle.Observations[i]
					prompt.WriteString(fmt.Sprintf("- Action: %s\n", action.ToolName))
					prompt.WriteString(fmt.Sprintf("- Action Input: %v\n", action.ToolInput))
					prompt.WriteString(fmt.Sprintf("- Observation: %v\n", observation.ToolOutput))
				}
			}
			prompt.WriteString("\n")
		}
	} else if len(cycles) > 0 {
		// Include just the final thought if we don't want the full process
		lastCycle := cycles[len(cycles)-1]
		if lastCycle.Thought != nil {
			prompt.WriteString("Final thought: ")
			prompt.WriteString(lastCycle.Thought.Content)
			prompt.WriteString("\n\n")
		}
	}
	prompt.WriteString(`Based on this information, provide a direct and helpful response to the user's query. 
The response should be in a conversational tone and should not mention the thought process, actions, or 
observations explicitly unless necessary for explanation.
`)
	return prompt.String()
}

// NewLLMResponseGenerator creates a new LLM-based response generator.
func NewLLMResponseGenerator(model model.Model, prompting ResponsePromptStrategy) *LLMResponseGenerator {
	return &LLMResponseGenerator{
		model:     model,
		prompting: prompting,
	}
}

// Generate generates a response based on the cycles.
func (g *LLMResponseGenerator) Generate(ctx context.Context, userQuery string, history []*message.Message, cycles []*Cycle) (*message.Message, error) {
	if g.model == nil {
		return nil, fmt.Errorf("model is required for LLMResponseGenerator")
	}

	// Build the prompt for response generation
	promptText := ""
	if g.prompting != nil {
		promptText = g.prompting.BuildResponsePrompt(userQuery, history, cycles)
	} else {
		// Fallback to a simple default prompt if no strategy is provided
		promptText = fmt.Sprintf("Based on the information gathered, provide a response to the user's query: \"%s\"", userQuery)
	}

	// Create a system message with the prompt
	promptMsg := message.NewSystemMessage(promptText)

	// Create model input with just the prompt to avoid confusing the model with the full history again
	modelInput := []*message.Message{promptMsg}

	// Optionally, include the most recent user message
	var latestUserMsg *message.Message
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == message.RoleUser {
			latestUserMsg = history[i]
			break
		}
	}
	if latestUserMsg != nil {
		modelInput = append(modelInput, latestUserMsg)
	}

	// Generate the response using the model
	opts := model.DefaultOptions()
	// Disable tool calls for response generation since we want a text response
	opts.EnableToolCalls = false
	response, err := g.model.GenerateWithMessages(ctx, modelInput, opts)
	if err != nil {
		return nil, fmt.Errorf("response generation failed: %w", err)
	}

	responseContent := ""
	if len(response.Messages) > 0 {
		responseContent = response.Messages[0].Content
	} else if response.Text != "" {
		responseContent = response.Text
	} else {
		return nil, fmt.Errorf("model returned empty response")
	}

	// Create the response message
	responseMsg := message.NewAssistantMessage(responseContent)
	responseMsg.ID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
	responseMsg.CreatedAt = time.Now()

	return responseMsg, nil
}

// DirectResponseGenerator generates responses directly from the final thought.
type DirectResponseGenerator struct {
	// extractFinalAnswer determines if we should extract just the final answer part
	extractFinalAnswer bool
}

// NewDirectResponseGenerator creates a new direct response generator.
func NewDirectResponseGenerator(extractFinalAnswer bool) *DirectResponseGenerator {
	return &DirectResponseGenerator{
		extractFinalAnswer: extractFinalAnswer,
	}
}

// Generate generates a response directly from the final thought without LLM processing.
func (g *DirectResponseGenerator) Generate(ctx context.Context, userQuery string, history []*message.Message, cycles []*Cycle) (*message.Message, error) {
	if len(cycles) == 0 {
		return message.NewAssistantMessage("I don't have enough information to provide a response."), nil
	}

	// Get the last thought
	lastCycle := cycles[len(cycles)-1]
	if lastCycle.Thought == nil {
		return message.NewAssistantMessage("I processed your request but encountered an issue while formulating the response."), nil
	}

	responseContent := lastCycle.Thought.Content

	// If we should extract just the final answer part
	if g.extractFinalAnswer {
		if idx := strings.Index(responseContent, "Final Answer:"); idx != -1 {
			responseContent = strings.TrimSpace(responseContent[idx+13:]) // 13 is length of "Final Answer:"
		}
	}

	// Create the response message
	responseMsg := message.NewAssistantMessage(responseContent)
	responseMsg.ID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
	responseMsg.CreatedAt = time.Now()

	return responseMsg, nil
}
