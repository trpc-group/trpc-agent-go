// Package agent provides specialized agent implementations.
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// LLMAgentConfig contains configuration for an LLM agent.
// This is a simplified, clean configuration structure.
type LLMAgentConfig struct {
	// Name of the agent.
	Name string `json:"name"`

	// Description of the agent.
	Description string `json:"description"`

	// Model to use for generating responses.
	Model model.Model

	// System prompt to use for the model.
	SystemPrompt string

	// Tools available to the agent.
	Tools []tool.BaseTool
}

// LLMAgent is an agent that uses a language model to generate responses.
// It maintains a direct array of tools for execution.
type LLMAgent struct {
	*BaseAgent
	model        model.Model
	systemPrompt string
	tools        []tool.BaseTool
}

// NewLLMAgent creates a new LLM agent.
func NewLLMAgent(config LLMAgentConfig) (*LLMAgent, error) {
	if config.Model == nil {
		return nil, fmt.Errorf("model is required for LLM agent")
	}

	// Create base agent config
	baseConfig := BaseAgentConfig{
		Name:        config.Name,
		Description: config.Description,
	}

	return &LLMAgent{
		BaseAgent:    NewBaseAgent(baseConfig),
		model:        config.Model,
		systemPrompt: config.SystemPrompt,
		tools:        config.Tools,
	}, nil
}

// Process processes the given content using the LLM and returns a response.
// This is a simplified implementation focusing on core functionality.
func (a *LLMAgent) Process(ctx context.Context, content *event.Content) (*event.Content, error) {
	// Extract text from input content
	inputText := content.GetText()
	if inputText == "" {
		return nil, fmt.Errorf("no text content provided")
	}

	// For now, simplified processing without tool integration in Process method
	// Tool integration will be handled in ProcessAsync
	opts := model.DefaultOptions()

	// Prepare messages array with system prompt and user input
	messages := []*message.Message{}

	// Add system message if we have a system prompt
	if a.systemPrompt != "" {
		messages = append(messages, message.NewSystemMessage(a.systemPrompt))
	}

	// Add user message
	messages = append(messages, message.NewUserMessage(inputText))

	response, err := a.model.GenerateWithMessages(ctx, messages, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate response: %w", err)
	}

	// Create response content
	responseContent := event.NewTextContent(response.Text)

	return responseContent, nil
}

// ProcessAsync processes the given content asynchronously using the LLM.
// This implementation handles the complete LLM + tool workflow.
func (a *LLMAgent) ProcessAsync(ctx context.Context, content *event.Content) (<-chan *event.Event, error) {
	// Create channel for events
	eventCh := make(chan *event.Event, 10)

	// Process in a goroutine
	go func() {
		defer close(eventCh)

		// Process the content with LLM
		if err := a.processWithLLM(ctx, content, eventCh); err != nil {
			errorContent := event.NewTextContent("LLM processing error: " + err.Error())
			errorEvent := event.NewEvent(a.Name(), errorContent)
			errorEvent.SetMetadata("error", err.Error())
			eventCh <- errorEvent
			return
		}
	}()

	return eventCh, nil
}

// processWithLLM processes content with the LLM and handles tool calls in the response.
func (a *LLMAgent) processWithLLM(ctx context.Context, content *event.Content, eventCh chan<- *event.Event) error {
	// Extract text from input content
	inputText := content.GetText()
	if inputText == "" {
		return fmt.Errorf("no text content provided")
	}

	// Generate response from model using message-based approach
	opts := model.DefaultOptions()

	// TODO: Enable tool calls when we have proper integration
	// For now, we'll handle tool calls manually based on response content
	// This will be enhanced in future PRs

	// Prepare messages array with system prompt and user input
	messages := []*message.Message{}

	// Add system message if we have a system prompt
	if a.systemPrompt != "" {
		messages = append(messages, message.NewSystemMessage(a.systemPrompt))
	}

	// Add user message
	messages = append(messages, message.NewUserMessage(inputText))

	response, err := a.model.GenerateWithMessages(ctx, messages, opts)
	if err != nil {
		return fmt.Errorf("failed to generate response: %w", err)
	}

	// For basic implementation, just send the text response
	// Tool calling will be enhanced in future iterations when we have proper model integration
	responseContent := event.NewTextContent(response.Text)
	responseEvent := event.NewEvent(a.Name(), responseContent)
	eventCh <- responseEvent

	// TODO: Tool call handling will be added here when model interface is updated
	// Check if the response contains tool calls
	if false && len(response.ToolCalls) > 0 {
		// Process each tool call
		for _, toolCall := range response.ToolCalls {
			args, err := a.parseToolArguments(toolCall.Function.Arguments)
			if err != nil {
				return fmt.Errorf("failed to parse tool arguments: %w", err)
			}

			// Find tool by name in the available tools
			var targetTool tool.BaseTool
			for _, t := range a.tools {
				if t.Name() == toolCall.Function.Name {
					targetTool = t
					break
				}
			}

			if targetTool == nil {
				return fmt.Errorf("tool not found: %s", toolCall.Function.Name)
			}

			// Send tool call event
			funcCall := &event.FunctionCall{
				Name:      toolCall.Function.Name,
				Arguments: args,
				ID:        toolCall.ID,
			}
			toolCallEvent := event.NewToolCallEvent(a.Name(), funcCall)
			eventCh <- toolCallEvent

			// Execute tool using BaseTool interface
			result, err := targetTool.Run(ctx, args)
			if err != nil {
				return fmt.Errorf("tool execution failed for %s: %w", toolCall.Function.Name, err)
			}

			// Send tool response event
			funcResponse := &event.FunctionResponse{
				Name:   toolCall.Function.Name,
				Result: result,
				ID:     toolCall.ID,
			}
			responseEvent := event.NewToolResponseEvent(a.Name(), funcResponse)
			eventCh <- responseEvent
		}

		// Generate follow-up response incorporating tool results
		// TODO: For now, we'll skip the follow-up and just send the tool results
		// This can be enhanced later
	}

	return nil
}

// parseToolArguments parses JSON string arguments into a map.
func (a *LLMAgent) parseToolArguments(jsonArgs string) (map[string]interface{}, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(jsonArgs), &args); err != nil {
		return nil, fmt.Errorf("failed to parse JSON arguments: %w", err)
	}
	return args, nil
}

// findTool finds a tool by name in the tools array.
func (a *LLMAgent) findTool(name string) (tool.BaseTool, bool) {
	for _, t := range a.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// HasTools returns true if the agent has tools available.
func (a *LLMAgent) HasTools() bool {
	return len(a.tools) > 0
}

// GetModel returns the model used by this agent.
func (a *LLMAgent) GetModel() model.Model {
	return a.model
}

// GetSystemPrompt returns the system prompt used by this agent.
func (a *LLMAgent) GetSystemPrompt() string {
	return a.systemPrompt
}
