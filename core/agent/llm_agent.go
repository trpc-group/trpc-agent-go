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
	Tools []tool.Tool
}

// LLMAgent is an agent that uses a language model to generate responses.
// This is a complete rewrite with Content-first approach.
type LLMAgent struct {
	*BaseAgent
	model        model.Model
	systemPrompt string
	toolSet      *tool.ToolSet
}

// NewLLMAgent creates a new LLM agent.
func NewLLMAgent(config LLMAgentConfig) (*LLMAgent, error) {
	if config.Model == nil {
		return nil, fmt.Errorf("model is required for LLM agent")
	}

	// Create tool set if tools are provided
	var toolSet *tool.ToolSet
	if len(config.Tools) > 0 {
		toolSet = tool.NewToolSet()
		for _, t := range config.Tools {
			if err := toolSet.Add(t); err != nil {
				return nil, fmt.Errorf("failed to add tool: %w", err)
			}
		}
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
		toolSet:      toolSet,
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

	// Generate response from model using message-based approach
	// Use GenerateWithMessages for better compatibility with modern APIs
	opts := model.DefaultOptions()

	// Enable tool calls if we have tools
	if a.toolSet != nil && a.toolSet.Size() > 0 {
		opts.EnableToolCalls = true
		// Set tool definitions in the model
		toolDefinitions := a.toolSet.GetToolDefinitions()
		a.model.SetTools(toolDefinitions)
	}

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

		// Handle potential tool calls in the input
		if content.HasFunctionCalls() {
			// Process tool calls first
			if err := a.processFunctionCalls(ctx, content, eventCh); err != nil {
				errorContent := event.NewTextContent("Tool execution error: " + err.Error())
				errorEvent := event.NewEvent(a.Name(), errorContent)
				errorEvent.SetMetadata("error", err.Error())
				eventCh <- errorEvent
				return
			}
		}

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

	// Enable tool calls if we have tools
	if a.toolSet != nil && a.toolSet.Size() > 0 {
		opts.EnableToolCalls = true
		// Set tool definitions in the model
		toolDefinitions := a.toolSet.GetToolDefinitions()
		a.model.SetTools(toolDefinitions)
	}

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

	// Check if the response contains tool calls
	if len(response.ToolCalls) > 0 {
		// Process each tool call
		for _, toolCall := range response.ToolCalls {
			// Convert model.ToolCall to event.FunctionCall
			funcCall := &event.FunctionCall{
				Name: toolCall.Function.Name,
				ID:   toolCall.ID,
			}

			// Parse arguments JSON string to map
			args, err := a.parseToolArguments(toolCall.Function.Arguments)
			if err != nil {
				return fmt.Errorf("failed to parse tool arguments: %w", err)
			}
			funcCall.Arguments = args

			// Send tool call event
			toolCallEvent := event.NewToolCallEvent(a.Name(), funcCall)
			eventCh <- toolCallEvent

			// Execute the tool if we have it
			if a.toolSet != nil {
				tool, exists := a.toolSet.Get(toolCall.Function.Name)
				if exists {
					result, err := tool.Execute(ctx, args)
					if err != nil {
						return fmt.Errorf("tool execution failed for %s: %w", toolCall.Function.Name, err)
					}

					// Create function response
					funcResponse := &event.FunctionResponse{
						Name:   toolCall.Function.Name,
						Result: result.Output,
						ID:     toolCall.ID,
					}

					// Send tool response event
					responseEvent := event.NewToolResponseEvent(a.Name(), funcResponse)
					eventCh <- responseEvent

					// Generate follow-up response incorporating tool result
					followUpMessages := append(messages, message.NewAssistantMessage(""))
					followUpMessages = append(followUpMessages, message.NewUserMessage(fmt.Sprintf("Tool %s returned: %s. Please provide a response incorporating this result.", toolCall.Function.Name, result.Output)))

					finalResponse, err := a.model.GenerateWithMessages(ctx, followUpMessages, model.DefaultOptions())
					if err != nil {
						return fmt.Errorf("failed to generate follow-up response: %w", err)
					}

					// Send final response
					finalContent := event.NewTextContent(finalResponse.Text)
					finalEvent := event.NewEvent(a.Name(), finalContent)
					eventCh <- finalEvent
				} else {
					return fmt.Errorf("tool not found: %s", toolCall.Function.Name)
				}
			}
		}
	} else {
		// No tool calls, just send the text response
		responseContent := event.NewTextContent(response.Text)
		responseEvent := event.NewEvent(a.Name(), responseContent)
		eventCh <- responseEvent
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

// processFunctionCalls processes function calls in the content.
func (a *LLMAgent) processFunctionCalls(ctx context.Context, content *event.Content, eventCh chan<- *event.Event) error {
	if a.toolSet == nil {
		return fmt.Errorf("no tools available for function calls")
	}

	functionCalls := content.GetFunctionCalls()
	for _, call := range functionCalls {
		// Get the tool
		tool, exists := a.toolSet.Get(call.Name)
		if !exists {
			return fmt.Errorf("tool not found: %s", call.Name)
		}

		// Execute the tool
		result, err := tool.Execute(ctx, call.Arguments)
		if err != nil {
			return fmt.Errorf("tool execution failed for %s: %w", call.Name, err)
		}

		// Create function response
		response := &event.FunctionResponse{
			Name:   call.Name,
			Result: result.Output,
			ID:     call.ID,
		}

		// Send tool response event
		responseEvent := event.NewToolResponseEvent(a.Name(), response)
		eventCh <- responseEvent
	}

	return nil
}

// GetModel returns the model used by this agent.
func (a *LLMAgent) GetModel() model.Model {
	return a.model
}

// GetSystemPrompt returns the system prompt.
func (a *LLMAgent) GetSystemPrompt() string {
	return a.systemPrompt
}

// GetToolSet returns the tool set.
func (a *LLMAgent) GetToolSet() *tool.ToolSet {
	return a.toolSet
}

// HasTools returns true if the agent has tools available.
func (a *LLMAgent) HasTools() bool {
	return a.toolSet != nil && a.toolSet.Size() > 0
}
