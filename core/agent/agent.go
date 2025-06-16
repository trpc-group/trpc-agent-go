// Package agent provides the core agent functionality.
package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
)

// Agent is the interface that all agents must implement.
// This is a complete redesign with Content-first approach.
type Agent interface {
	// Core identification
	Name() string
	Description() string

	// Content-based execution (no message.Message dependency)
	Process(ctx context.Context, content *event.Content) (*event.Content, error)
	ProcessAsync(ctx context.Context, content *event.Content) (<-chan *event.Event, error)
}

// BaseAgentConfig is the configuration for a base agent.
type BaseAgentConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// BaseAgent is a base implementation of the Agent interface.
// This provides the foundation for all agent types.
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

// Process is the base implementation that should be overridden by concrete agents.
// It simply echoes back the input content with a prefix.
func (a *BaseAgent) Process(ctx context.Context, content *event.Content) (*event.Content, error) {
	// Base implementation creates a simple text response
	inputText := content.GetText()
	responseText := "BaseAgent processed: " + inputText

	return event.NewTextContent(responseText), nil
}

// ProcessAsync implements asynchronous content processing.
// It processes the content and emits events with the result.
func (a *BaseAgent) ProcessAsync(ctx context.Context, content *event.Content) (<-chan *event.Event, error) {
	// Create a channel for events
	eventCh := make(chan *event.Event, 10)

	// Run in a goroutine
	go func() {
		defer close(eventCh)

		// Process the content
		response, err := a.Process(ctx, content)

		// If error occurred, send error event with metadata
		if err != nil {
			errorContent := event.NewTextContent("Error: " + err.Error())
			errorEvent := event.NewEvent(a.Name(), errorContent)
			errorEvent.SetMetadata("error", err.Error())
			eventCh <- errorEvent
			return
		}

		// Send response event
		responseEvent := event.NewEvent(a.Name(), response)
		eventCh <- responseEvent
	}()

	return eventCh, nil
}
