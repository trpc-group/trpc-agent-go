// Package agent provides specialized agent implementations.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/memory"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
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
	*BaseAgent
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
	}

	// Create base agent config
	baseConfig := BaseAgentConfig{
		Name:        config.Name,
		Description: config.Description,
	}

	return &LLMAgent{
		BaseAgent:       NewBaseAgent(baseConfig),
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
	if response != nil && len(response.Messages) > 0 {
		assistantMsg := response.Messages[0]

		// Store the model's response in memory
		if err := a.memory.Store(ctx, assistantMsg); err != nil {
			return nil, fmt.Errorf("failed to store response: %w", err)
		}

		return assistantMsg, nil
	}

	// If no messages in response but we have text, create a message
	if response != nil && response.Text != "" {
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
			log.Errorf("Failed to get streaming response from model: %v", err)
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		log.Debugf("Stream started from model, waiting for responses...")

		// Accumulate the full response
		var fullResponse *message.Message
		var responseText string
		var sequence int = 0 // Add sequence counter

		// Process streaming responses
		for response := range responseCh {
			if response != nil && len(response.ToolCalls) > 0 {
				// Handle tool calls
				for _, toolCall := range response.ToolCalls {
					// Create and send a tool call event
					toolCallEvent := event.NewStreamToolCallEvent(
						toolCall.Function.Name,
						toolCall.Function.Arguments,
						toolCall.ID,
					)
					eventCh <- toolCallEvent

					// If we have a toolset, try to execute the tool
					if a.toolSet != nil {
						tool, found := a.toolSet.Get(toolCall.Function.Name)
						if found {
							// Parse the arguments
							var params map[string]interface{}
							if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err == nil {
								// Execute the tool and send the result
								result, err := tool.Execute(ctx, params)
								toolResultEvent := event.NewStreamToolResultEvent(
									toolCall.Function.Name,
									result,
									err,
								)
								eventCh <- toolResultEvent
							}
						}
					}
				}
			} else if response != nil && len(response.Messages) > 0 {
				// If we get a complete message, use it
				chunk := response.Messages[0]

				// Send stream chunk event directly instead of stream event
				eventCh <- event.NewStreamChunkEvent(chunk.Content, sequence)
				sequence++ // Increment sequence

				// Update full response
				if fullResponse == nil {
					fullResponse = chunk
				} else {
					fullResponse.Content += chunk.Content
				}
			} else if response != nil && response.Text != "" {
				// If we get text, accumulate it
				responseText += response.Text

				// Send stream chunk event directly instead of stream event
				eventCh <- event.NewStreamChunkEvent(response.Text, sequence)
				sequence++ // Increment sequence
			}
		}

		// If we have a full response, store it and send a message event
		if fullResponse != nil {
			if err := a.memory.Store(ctx, fullResponse); err != nil {
				eventCh <- event.NewErrorEvent(fmt.Errorf("failed to store response: %w", err), 500)
				return
			}
			eventCh <- event.NewMessageEvent(fullResponse)
		} else if responseText != "" {
			// If we only accumulated text, create a message
			assistantMsg := message.NewAssistantMessage(responseText)
			if err := a.memory.Store(ctx, assistantMsg); err != nil {
				eventCh <- event.NewErrorEvent(fmt.Errorf("failed to store response: %w", err), 500)
				return
			}
			eventCh <- event.NewMessageEvent(assistantMsg)
		} else {
			// Empty response
			assistantMsg := message.NewAssistantMessage("No response generated")
			eventCh <- event.NewMessageEvent(assistantMsg)
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
