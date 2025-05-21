// Package agent provides specialized agent implementations.
package agent

import (
	"context"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// ParallelAgentConfig contains configuration for a parallel agent.
type ParallelAgentConfig struct {
	// Name of the agent.
	Name string `json:"name"`

	// Description of the agent.
	Description string `json:"description"`

	// Agents to run in parallel.
	Agents []Agent `json:"agents"`
}

// ParallelAgent executes multiple agents concurrently.
type ParallelAgent struct {
	*BaseAgent
	agents []Agent
}

// NewParallelAgent creates a new parallel agent.
func NewParallelAgent(config ParallelAgentConfig) (*ParallelAgent, error) {
	if len(config.Agents) == 0 {
		return nil, fmt.Errorf("at least one agent is required for parallel agent")
	}

	// Create base agent config
	baseConfig := BaseAgentConfig{
		Name:        config.Name,
		Description: config.Description,
	}

	return &ParallelAgent{
		BaseAgent: NewBaseAgent(baseConfig),
		agents:    config.Agents,
	}, nil
}

// Run executes all agents in parallel and collects their responses.
func (a *ParallelAgent) Run(ctx context.Context, msg *message.Message) (*message.Message, error) {
	var wg sync.WaitGroup
	responses := make([]*message.Message, len(a.agents))
	errors := make([]error, len(a.agents))

	// Execute all agents in parallel
	for i, ag := range a.agents {
		wg.Add(1)
		go func(index int, agent Agent) {
			defer wg.Done()
			resp, err := agent.Run(ctx, msg)
			responses[index] = resp
			errors[index] = err
		}(i, ag)
	}

	// Wait for all agents to complete
	wg.Wait()

	// Check for errors
	var errorMsgs []string
	for i, err := range errors {
		if err != nil {
			agentName := "unknown"
			if a.agents[i] != nil {
				agentName = a.agents[i].Name()
			}
			errorMsgs = append(errorMsgs, fmt.Sprintf("%s: %v", agentName, err))
		}
	}

	// If all agents failed, return an error
	if len(errorMsgs) == len(a.agents) {
		return nil, fmt.Errorf("all parallel agents failed: %v", errorMsgs)
	}

	// Combine responses into a single message
	var combinedContent string
	for i, resp := range responses {
		if resp != nil {
			agentName := a.agents[i].Name()
			combinedContent += fmt.Sprintf("Agent [%s]: %s\n\n", agentName, resp.Content)
		}
	}

	if combinedContent == "" {
		return nil, fmt.Errorf("no valid responses from parallel agents")
	}

	return message.NewAssistantMessage(combinedContent), nil
}

// RunAsync executes all agents asynchronously and streams their events.
func (a *ParallelAgent) RunAsync(ctx context.Context, msg *message.Message) (<-chan *event.Event, error) {
	// Create channel for events
	eventCh := make(chan *event.Event, 10*len(a.agents))

	// Start all agents in parallel
	var wg sync.WaitGroup

	// Emit agent start event
	go func() {
		eventCh <- event.NewAgentStartEvent(a.Name(), 0)
	}()

	// Process in a goroutine
	go func() {
		defer close(eventCh)
		defer func() {
			// Emit agent end event when all agents are done
			eventCh <- event.NewAgentEndEvent(a.Name(), 0)
		}()

		subEventChs := make([]<-chan *event.Event, 0, len(a.agents))

		// Start all agents
		for _, ag := range a.agents {
			agentEventCh, err := ag.RunAsync(ctx, msg)
			if err != nil {
				eventCh <- event.NewErrorEvent(err, 500)
				continue
			}

			subEventChs = append(subEventChs, agentEventCh)
		}

		// Fan-in all event channels to the main channel
		for _, ch := range subEventChs {
			wg.Add(1)
			go func(subEventCh <-chan *event.Event) {
				defer wg.Done()
				for evt := range subEventCh {
					// Forward all events
					select {
					case <-ctx.Done():
						return
					case eventCh <- evt:
						// Event forwarded
					}
				}
			}(ch)
		}

		// Wait for all event channels to be closed
		wg.Wait()
	}()

	return eventCh, nil
}
