// Package chainagent provides a sequential agent implementation.
package chainagent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

const defaultChannelBufferSize = 256

// ChainAgent is an agent that runs its sub-agents in sequence.
type ChainAgent struct {
	name              string
	subAgents         []agent.Agent
	tools             []tool.Tool
	channelBufferSize int
}

// Options contains configuration options for creating a ChainAgent.
type Options struct {
	// Name is the name of the agent.
	Name string
	// SubAgents is the list of sub-agents to run in sequence.
	SubAgents []agent.Agent
	// Tools is the list of tools available to the agent.
	Tools []tool.Tool
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
}

// New creates a new ChainAgent with the given options.
func New(opts Options) *ChainAgent {
	// Set default channel buffer size if not specified.
	channelBufferSize := opts.ChannelBufferSize
	if channelBufferSize <= 0 {
		channelBufferSize = defaultChannelBufferSize
	}

	return &ChainAgent{
		name:              opts.Name,
		subAgents:         opts.SubAgents,
		tools:             opts.Tools,
		channelBufferSize: channelBufferSize,
	}
}

// Run implements the agent.Agent interface.
// It executes sub-agents in sequence, passing events through as they are generated.
func (a *ChainAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	go func() {
		defer close(eventChan)

		// Set agent name if not already set.
		if invocation.AgentName == "" {
			invocation.AgentName = a.name
		}

		// Run each sub-agent in sequence.
		for _, subAgent := range a.subAgents {
			// Create a new invocation for the sub-agent.
			subInvocation := *invocation
			subInvocation.Agent = subAgent

			// Run the sub-agent.
			subEventChan, err := subAgent.Run(ctx, &subInvocation)
			if err != nil {
				// Send error event.
				errorEvent := event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					model.ErrorTypeFlowError,
					err.Error(),
				)
				select {
				case eventChan <- errorEvent:
				case <-ctx.Done():
				}
				return
			}

			// Forward all events from the sub-agent.
			for subEvent := range subEventChan {
				select {
				case eventChan <- subEvent:
				case <-ctx.Done():
					return
				}
			}

			// Check if context was cancelled.
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()

	return eventChan, nil
}

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *ChainAgent) Tools() []tool.Tool {
	return a.tools
}
