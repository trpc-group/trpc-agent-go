//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package parallelagent provides a parallel agent implementation.
package parallelagent

import (
	"context"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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
	tools             []tool.Tool
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

// Option configures ParallelAgent settings using the functional options pattern.
// This type is exported to allow external packages to create custom options.
type Option func(*Options)

// Options contains all configuration options for ParallelAgent.
// This struct is exported to allow external packages to inspect or modify options.
type Options struct {
	subAgents         []agent.Agent
	tools             []tool.Tool
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

// WithSubAgents sets the sub-agents that will be executed in parallel.
// All agents will start simultaneously and their events will be merged
// into a single output stream.
func WithSubAgents(sub []agent.Agent) Option {
	return func(o *Options) { o.subAgents = sub }
}

// WithTools registers tools available to the parallel agent.
// These tools can be used by any sub-agent during parallel execution.
func WithTools(tools []tool.Tool) Option {
	return func(o *Options) { o.tools = tools }
}

// WithChannelBufferSize sets the buffer size for the event channel.
// This controls how many events can be buffered before blocking.
// Default is 256 if not specified.
func WithChannelBufferSize(size int) Option {
	return func(o *Options) { o.channelBufferSize = size }
}

// WithAgentCallbacks attaches lifecycle callbacks to the parallel agent.
// These callbacks allow custom logic to be executed before and after
// the parallel agent runs.
func WithAgentCallbacks(cb *agent.Callbacks) Option {
	return func(o *Options) { o.agentCallbacks = cb }
}

// New creates a new ParallelAgent with the given name and options.
// ParallelAgent executes all its sub-agents simultaneously and merges
// their event streams into a single output channel.
func New(name string, opts ...Option) *ParallelAgent {
	cfg := Options{channelBufferSize: defaultChannelBufferSize}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.channelBufferSize <= 0 {
		cfg.channelBufferSize = defaultChannelBufferSize
	}
	return &ParallelAgent{
		name:              name,
		subAgents:         cfg.subAgents,
		tools:             cfg.tools,
		channelBufferSize: cfg.channelBufferSize,
		agentCallbacks:    cfg.agentCallbacks,
	}
}

// createBranchInvocation creates an isolated branch invocation for each sub-agent.
// This ensures parallel execution doesn't interfere with each other.
func (a *ParallelAgent) createBranchInvocation(
	subAgent agent.Agent,
	baseInvocation *agent.Invocation,
) *agent.Invocation {
	branchInvocation := baseInvocation.CreateBranchInvocation(subAgent)

	// Create unique invocation ID for this branch.
	branchSuffix := a.name + "." + subAgent.Info().Name
	branchInvocation.InvocationID = baseInvocation.InvocationID + "." + branchSuffix

	// Set branch identifier for hierarchical event filtering.
	branchInvocation.Branch = branchInvocation.InvocationID

	return branchInvocation
}

// setupInvocation prepares the invocation for execution.
func (a *ParallelAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name
	invocation.Agent = a
	invocation.AgentName = a.name

	// Set agent callbacks if available.
	invocation.AgentCallbacks = a.agentCallbacks
}

// handleBeforeAgentCallbacks handles pre-execution callbacks.
func (a *ParallelAgent) handleBeforeAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) bool {
	if invocation.AgentCallbacks == nil {
		return false
	}

	customResponse, err := invocation.AgentCallbacks.RunBeforeAgent(ctx, invocation)
	if err != nil {
		// Send error event.
		errorEvent := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
		select {
		case eventChan <- errorEvent:
		case <-ctx.Done():
		}
		return true // Indicates early return
	}
	if customResponse != nil {
		// Create an event from the custom response and then close.
		customEvent := event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			customResponse,
		)
		select {
		case eventChan <- customEvent:
		case <-ctx.Done():
		}
		return true // Indicates early return
	}
	return false // Continue execution
}

// startSubAgents starts all sub-agents in parallel and returns their event channels.
func (a *ParallelAgent) startSubAgents(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) []<-chan *event.Event {
	// Start all sub-agents in parallel.
	var wg sync.WaitGroup
	eventChans := make([]<-chan *event.Event, len(a.subAgents))

	for i, subAgent := range a.subAgents {
		wg.Add(1)
		go func(idx int, sa agent.Agent) {
			defer wg.Done()

			// Create branch invocation for this sub-agent.
			branchInvocation := a.createBranchInvocation(sa, invocation)

			// Reset invocation information in context
			branchAgentCtx := agent.NewInvocationContext(ctx, branchInvocation)

			// Run the sub-agent.
			subEventChan, err := sa.Run(branchAgentCtx, branchInvocation)
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

			eventChans[idx] = subEventChan
		}(i, subAgent)
	}

	// Wait for all sub-agents to start.
	wg.Wait()
	return eventChans
}

// handleAfterAgentCallbacks handles post-execution callbacks.
func (a *ParallelAgent) handleAfterAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	if invocation.AgentCallbacks == nil {
		return
	}

	customResponse, err := invocation.AgentCallbacks.RunAfterAgent(ctx, invocation, nil)
	if err != nil {
		// Send error event.
		errorEvent := event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
		select {
		case eventChan <- errorEvent:
		case <-ctx.Done():
		}
		return
	}
	if customResponse != nil {
		// Create an event from the custom response.
		customEvent := event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			customResponse,
		)
		select {
		case eventChan <- customEvent:
		case <-ctx.Done():
		}
	}
}

// Run implements the agent.Agent interface.
// It executes sub-agents in parallel and merges their event streams.
func (a *ParallelAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	go func() {
		defer close(eventChan)
		a.executeParallelRun(ctx, invocation, eventChan)
	}()

	return eventChan, nil
}

// executeParallelRun handles the main execution logic for parallel agent.
func (a *ParallelAgent) executeParallelRun(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	// Setup invocation.
	a.setupInvocation(invocation)

	// Handle before agent callbacks.
	if a.handleBeforeAgentCallbacks(ctx, invocation, eventChan) {
		return
	}

	// Start sub-agents.
	eventChans := a.startSubAgents(ctx, invocation, eventChan)

	// Merge events from all sub-agents.
	a.mergeEventStreams(ctx, eventChans, eventChan)

	// Handle after agent callbacks.
	a.handleAfterAgentCallbacks(ctx, invocation, eventChan)
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
func (a *ParallelAgent) Tools() []tool.Tool {
	return a.tools
}

// Info implements the agent.Agent interface.
// It returns the basic information about this agent.
func (a *ParallelAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: fmt.Sprintf("Parallel agent that runs %d sub-agents concurrently", len(a.subAgents)),
	}
}

// SubAgents implements the agent.Agent interface.
// It returns the list of sub-agents available to this agent.
func (a *ParallelAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

// FindSubAgent implements the agent.Agent interface.
// It finds a sub-agent by name and returns nil if not found.
func (a *ParallelAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}
