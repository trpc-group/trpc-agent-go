// Package parallelagent provides a parallel agent implementation.
package parallelagent

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

const defaultChannelBufferSize = 256

// ParallelAgent is an agent that runs its sub-agents in parallel in isolated manner.
// This approach is beneficial for scenarios requiring multiple perspectives or
// attempts on a single task, such as:
// - Running different algorithms simultaneously.
// - Generating multiple responses for review by a subsequent evaluation agent.
type ParallelAgent struct {
	name              string
	subAgents         []agent.Agent
	tools             []tool.UnaryTool
	channelBufferSize int
}

// Options contains configuration options for creating a ParallelAgent.
type Options struct {
	// Name is the name of the agent.
	Name string
	// SubAgents is the list of sub-agents to run in parallel.
	SubAgents []agent.Agent
	// Tools is the list of tools available to the agent.
	Tools []tool.UnaryTool
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
}

// New creates a new ParallelAgent with the given options.
func New(opts Options) *ParallelAgent {
	// Set default channel buffer size if not specified.
	channelBufferSize := opts.ChannelBufferSize
	if channelBufferSize <= 0 {
		channelBufferSize = defaultChannelBufferSize
	}

	return &ParallelAgent{
		name:              opts.Name,
		subAgents:         opts.SubAgents,
		tools:             opts.Tools,
		channelBufferSize: channelBufferSize,
	}
}

// createBranchInvocationForSubAgent creates an isolated branch invocation for each sub-agent.
// This ensures parallel execution doesn't interfere with each other.
func (a *ParallelAgent) createBranchInvocationForSubAgent(
	subAgent agent.Agent,
	baseInvocation *agent.Invocation,
) *agent.Invocation {
	// Create a copy of the invocation.
	branchInvocation := *baseInvocation
	branchInvocation.Agent = subAgent

	// Create unique invocation ID for this branch.
	branchSuffix := a.name + "." + branchInvocation.AgentName
	branchInvocation.InvocationID = baseInvocation.InvocationID + "." + branchSuffix

	return &branchInvocation
}

// Run implements the agent.Agent interface.
// It executes sub-agents in parallel and merges their event streams.
func (a *ParallelAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	go func() {
		defer close(eventChan)

		// Set agent name if not already set.
		if invocation.AgentName == "" {
			invocation.AgentName = a.name
		}

		// Create context that can be cancelled to stop all sub-agents.
		subCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Start all sub-agents in parallel.
		var wg sync.WaitGroup
		eventChans := make([]<-chan *event.Event, len(a.subAgents))

		for i, subAgent := range a.subAgents {
			wg.Add(1)
			go func(idx int, sa agent.Agent) {
				defer wg.Done()

				// Create branch invocation for this sub-agent.
				branchInvocation := a.createBranchInvocationForSubAgent(sa, invocation)

				// Run the sub-agent.
				subEventChan, err := sa.Run(subCtx, branchInvocation)
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
					case <-subCtx.Done():
					}
					return
				}

				eventChans[idx] = subEventChan
			}(i, subAgent)
		}

		// Wait for all sub-agents to start.
		wg.Wait()

		// Merge events from all sub-agents.
		a.mergeEventStreams(subCtx, eventChans, eventChan)
	}()

	return eventChan, nil
}

// mergeEventStreams merges multiple event channels into a single output channel.
// This implementation processes events as they arrive from different sub-agents.
func (a *ParallelAgent) mergeEventStreams(
	ctx context.Context,
	eventChans []<-chan *event.Event,
	outputChan chan<- *event.Event,
) {
	var wg sync.WaitGroup

	// Start a goroutine for each input channel.
	for _, ch := range eventChans {
		if ch == nil {
			continue
		}

		wg.Add(1)
		go func(inputChan <-chan *event.Event) {
			defer wg.Done()
			for {
				select {
				case evt, ok := <-inputChan:
					if !ok {
						return // Channel closed.
					}
					select {
					case outputChan <- evt:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}

	// Wait for all goroutines to finish.
	wg.Wait()
}

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *ParallelAgent) Tools() []tool.UnaryTool {
	return a.tools
}
