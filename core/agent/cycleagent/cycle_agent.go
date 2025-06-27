// Package cycleagent provides a looping agent implementation.
package cycleagent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

const defaultChannelBufferSize = 256

// CycleAgent is an agent that runs its sub-agents in a loop.
// When a sub-agent generates an event with escalation or max_iterations are
// reached, the cycle agent will stop.
type CycleAgent struct {
	name              string
	subAgents         []agent.Agent
	tools             []tool.UnaryTool
	maxIterations     *int // Optional maximum number of iterations
	channelBufferSize int
}

// Options contains configuration options for creating a CycleAgent.
type Options struct {
	// Name is the name of the agent.
	Name string
	// SubAgents is the list of sub-agents to run in a loop.
	SubAgents []agent.Agent
	// Tools is the list of tools available to the agent.
	Tools []tool.UnaryTool
	// MaxIterations is the maximum number of iterations to run the loop agent.
	// If not set, the loop agent will run indefinitely until a sub-agent escalates.
	MaxIterations *int
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
}

// New creates a new CycleAgent with the given options.
func New(opts Options) *CycleAgent {
	// Set default channel buffer size if not specified.
	channelBufferSize := opts.ChannelBufferSize
	if channelBufferSize <= 0 {
		channelBufferSize = defaultChannelBufferSize
	}

	return &CycleAgent{
		name:              opts.Name,
		subAgents:         opts.SubAgents,
		tools:             opts.Tools,
		maxIterations:     opts.MaxIterations,
		channelBufferSize: channelBufferSize,
	}
}

// shouldEscalate checks if an event indicates escalation.
// In the Go implementation, we consider an event to indicate escalation
// if it's an error event or if it's marked as done with an error.
func (a *CycleAgent) shouldEscalate(evt *event.Event) bool {
	if evt == nil {
		return false
	}

	// Check for explicit error events.
	if evt.Error != nil {
		return true
	}

	// Check for done events that might indicate completion or escalation.
	// TODO: add more sophisticated escalation logic.
	return evt.Done && evt.Object == model.ObjectTypeError
}

// Run implements the agent.Agent interface.
// It executes sub-agents in a loop until escalation or max iterations.
func (a *CycleAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	go func() {
		defer close(eventChan)

		// Set agent name if not already set.
		if invocation.AgentName == "" {
			invocation.AgentName = a.name
		}

		timesLooped := 0

		// Main loop: continue until max iterations or escalation.
		for a.maxIterations == nil || timesLooped < *a.maxIterations {
			// Check if context was cancelled.
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Run each sub-agent in sequence within the loop.
			shouldBreak := false
			for _, subAgent := range a.subAgents {
				// Create a new invocation for the sub-agent.
				subInvocation := *invocation
				subInvocation.Agent = subAgent

				// Run the sub-agent.
				subEventChan, err := subAgent.Run(ctx, &subInvocation)
				if err != nil {
					// Send error event and escalate.
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

				// Forward events from the sub-agent and check for escalation.
				for subEvent := range subEventChan {
					select {
					case eventChan <- subEvent:
					case <-ctx.Done():
						return
					}

					// Check if this event indicates escalation.
					if a.shouldEscalate(subEvent) {
						shouldBreak = true
						break
					}
				}

				// If escalation was detected, break out of the sub-agent loop.
				if shouldBreak {
					break
				}

				// Check if context was cancelled.
				select {
				case <-ctx.Done():
					return
				default:
				}
			}

			// If escalation was detected, break out of the main loop.
			if shouldBreak {
				break
			}

			timesLooped++
		}
	}()

	return eventChan, nil
}

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *CycleAgent) Tools() []tool.UnaryTool {
	return a.tools
}
