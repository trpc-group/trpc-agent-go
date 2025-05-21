// Package agent provides the core agent functionality.
package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// Agent is the interface that all agents must implement.
type Agent interface {
	// Name returns the name of the agent.
	Name() string

	// Description returns the description of the agent.
	Description() string

	// Run processes the given message and returns a response.
	Run(ctx context.Context, msg *message.Message) (*message.Message, error)

	// RunAsync processes the given message asynchronously and sends events through the channel.
	RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error)
}

// BaseAgentConfig is the configuration for a base agent.
type BaseAgentConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// BaseAgent is a base implementation of the Agent interface.
type BaseAgent struct {
	name        string
	description string
}

// NewBaseAgent creates a new base agent.
func NewBaseAgent(config BaseAgentConfig) *BaseAgent {
	return &BaseAgent{
		name:        config.Name,
		description: config.Description,
	}
}

// Name returns the name of the agent.
func (a *BaseAgent) Name() string {
	return a.name
}

// Description returns the description of the agent.
func (a *BaseAgent) Description() string {
	return a.description
}

// Run is the base implementation that should be overridden by concrete agents.
func (a *BaseAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	// Base implementation just echoes the input message
	return message.NewAssistantMessage("BaseAgent implementation: " + msg.Content), nil
}

// RunAsync implements asynchronous message processing.
func (a *BaseAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	// Create a channel for events
	eventCh := make(chan *event.Event, 10)

	// Run in a goroutine
	go func() {
		defer close(eventCh)

		// Process the message
		response, err := a.Run(ctx, msg)

		// If error occurred, send error event
		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		// Send message event
		eventCh <- event.NewMessageEvent(response)
	}()

	return eventCh, nil
}
