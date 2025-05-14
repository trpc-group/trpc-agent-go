// Package agents provides specialized agent implementations.
package agents

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	// ErrModelRequired is returned when an LLM agent is initialized without a model.
	ErrModelRequired = errors.New("model is required for LLM agent")
)

// LLMAgentConfig contains configuration for an LLM agent.
type LLMAgentConfig struct {
	// Name of the agent.
	Name string `json:"name"`

	// Description of the agent.
	Description string `json:"description"`

	// Model to use for generating responses.
	Model model.Model

	// Memory system to use for storing conversation history.
	Memory memory.Memory

	// MaxHistoryMessages is the maximum number of messages to include in context.
	MaxHistoryMessages int

	// System prompt to use for the model.
	SystemPrompt string

	// Tools available to the agent.
	Tools []tool.Tool

	// EnableStreaming determines if the agent should stream responses.
	EnableStreaming bool
}

// LLMAgent is an agent that uses a language model to generate responses.
type LLMAgent struct {
	*agent.BaseAgent
	model           model.Model
	memory          memory.Memory
	maxHistory      int
	systemPrompt    string
	tools           []tool.Tool
	toolSet         *tool.ToolSet
	enableStreaming bool
}

// NewLLMAgent creates a new LLM agent.
func NewLLMAgent(config LLMAgentConfig) (*LLMAgent, error) {
	if config.Model == nil {
		return nil, ErrModelRequired
	}

	// Create default memory if none provided
	if config.Memory == nil {
		config.Memory = memory.NewBaseMemory()
	}

	// Set default max history if not specified
	if config.MaxHistoryMessages <= 0 {
		config.MaxHistoryMessages = 10
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

		// If model supports tool calls, register the tools with the model
		if tcModel, ok := config.Model.(model.ToolCallSupportingModel); ok {
			toolDefs := make([]model.ToolDefinition, 0, len(config.Tools))
			for _, t := range config.Tools {
				toolDefs = append(toolDefs, model.ToolDefinition{
					Name:        t.Name(),
					Description: t.Description(),
					Parameters:  t.Parameters(),
				})
			}
			if err := tcModel.SetTools(toolDefs); err != nil {
				return nil, fmt.Errorf("failed to set tools on model: %w", err)
			}
		}
	}

	// Create base agent config
	baseConfig := agent.BaseAgentConfig{
		Name:        config.Name,
		Description: config.Description,
	}

	return &LLMAgent{
		BaseAgent:       agent.NewBaseAgent(baseConfig),
		model:           config.Model,
		memory:          config.Memory,
		maxHistory:      config.MaxHistoryMessages,
		systemPrompt:    config.SystemPrompt,
		tools:           config.Tools,
		toolSet:         toolSet,
		enableStreaming: config.EnableStreaming,
	}, nil
}

// Run processes the given message using the LLM and returns a response.
func (a *LLMAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	// Store the incoming message in memory
	if err := a.memory.Store(ctx, msg); err != nil {
		return nil, fmt.Errorf("failed to store message: %w", err)
	}

	// Retrieve conversation history
	history, err := a.getConversationHistory(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve conversation history: %w", err)
	}

	// Add system message if set
	if a.systemPrompt != "" {
		// Insert system message at the beginning
		sysMsg := message.NewSystemMessage(a.systemPrompt)
		history = append([]*message.Message{sysMsg}, history...)
	}

	// Generate response from model
	opts := model.DefaultOptions()
	opts.EnableToolCalls = a.toolSet != nil && len(a.tools) > 0

	response, err := a.model.GenerateWithMessages(ctx, history, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate response: %w", err)
	}

	// Process response
	if len(response.Messages) > 0 {
		assistantMsg := response.Messages[0]

		// Store the model's response in memory
		if err := a.memory.Store(ctx, assistantMsg); err != nil {
			return nil, fmt.Errorf("failed to store response: %w", err)
		}

		return assistantMsg, nil
	}

	// If no messages in response but we have text, create a message
	if response.Text != "" {
		assistantMsg := message.NewAssistantMessage(response.Text)

		// Store the model's response in memory
		if err := a.memory.Store(ctx, assistantMsg); err != nil {
			return nil, fmt.Errorf("failed to store response: %w", err)
		}

		return assistantMsg, nil
	}

	// Empty response
	assistantMsg := message.NewAssistantMessage("No response generated")
	return assistantMsg, nil
}

// RunAsync processes the given message asynchronously using the LLM and streaming.
func (a *LLMAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	// Create channel for events
	eventCh := make(chan *event.Event, 10)

	// If streaming is not enabled or model doesn't support it, fall back to default implementation
	if !a.enableStreaming {
		return a.BaseAgent.RunAsync(ctx, msg)
	}

	streamingModel, ok := a.model.(model.StreamingModel)
	if !ok {
		// Fall back to non-streaming implementation
		return a.BaseAgent.RunAsync(ctx, msg)
	}

	// Process in a goroutine
	go func() {
		defer close(eventCh)

		// Store the incoming message in memory
		if err := a.memory.Store(ctx, msg); err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		// Retrieve conversation history
		history, err := a.getConversationHistory(ctx)
		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		// Add system message if set
		if a.systemPrompt != "" {
			// Insert system message at the beginning
			sysMsg := message.NewSystemMessage(a.systemPrompt)
			history = append([]*message.Message{sysMsg}, history...)
		}

		// Generate streaming response from model
		opts := model.DefaultOptions()
		opts.EnableToolCalls = a.toolSet != nil && len(a.tools) > 0

		responseCh, err := streamingModel.GenerateStreamWithMessages(ctx, history, opts)
		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		// Accumulate the full response
		var fullResponse *message.Message
		var responseText string

		// Process streaming responses
		for response := range responseCh {
			if len(response.Messages) > 0 {
				// If we get a complete message, use it
				chunk := response.Messages[0]

				// Send stream event
				eventCh <- event.NewStreamEvent(chunk.Content)

				// Update full response
				if fullResponse == nil {
					fullResponse = message.NewAssistantMessage(chunk.Content)
				} else {
					fullResponse.Content += chunk.Content
				}
			} else if response.Text != "" {
				// If we get text, accumulate it
				responseText += response.Text

				// Send stream event
				eventCh <- event.NewStreamEvent(response.Text)
			}
		}

		// If we don't have a full response but accumulated text, create a message
		if fullResponse == nil && responseText != "" {
			fullResponse = message.NewAssistantMessage(responseText)
		}

		// If we have a response, store it and send completion event
		if fullResponse != nil {
			// Store the complete response in memory
			if err := a.memory.Store(ctx, fullResponse); err != nil {
				eventCh <- event.NewErrorEvent(err, 500)
				return
			}

			// Send completion event
			eventCh <- event.NewMessageEvent(fullResponse)
		} else {
			// No response generated
			emptyMsg := message.NewAssistantMessage("No response generated")
			eventCh <- event.NewMessageEvent(emptyMsg)
		}
	}()

	return eventCh, nil
}

// getConversationHistory retrieves recent conversation history from memory.
func (a *LLMAgent) getConversationHistory(ctx context.Context) ([]*message.Message, error) {
	// Retrieve all messages from memory
	messages, err := a.memory.Retrieve(ctx)
	if err != nil {
		return nil, err
	}

	// Limit to max history if needed
	if len(messages) > a.maxHistory {
		messages = messages[len(messages)-a.maxHistory:]
	}

	return messages, nil
}

// GetModel returns the model used by this agent.
func (a *LLMAgent) GetModel() model.Model {
	return a.model
}

// GetMemory returns the memory used by this agent.
func (a *LLMAgent) GetMemory() memory.Memory {
	return a.memory
}

// GetSystemPrompt returns the system prompt used by this agent.
func (a *LLMAgent) GetSystemPrompt() string {
	return a.systemPrompt
}

// GetToolSet returns the tool set used by this agent.
func (a *LLMAgent) GetToolSet() *tool.ToolSet {
	return a.toolSet
}
