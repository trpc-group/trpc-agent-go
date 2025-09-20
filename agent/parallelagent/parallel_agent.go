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
	"runtime/debug"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
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
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

// WithSubAgents sets the sub-agents that will be executed in parallel.
// All agents will start simultaneously and their events will be merged
// into a single output stream.
func WithSubAgents(sub []agent.Agent) Option {
	return func(o *Options) { o.subAgents = sub }
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
	// Create unique invocation ID for this branch.
	eventFilterKey := baseInvocation.GetEventFilterKey()
	if eventFilterKey == "" {
		eventFilterKey = a.name + agent.EventFilterKeyDelimiter + subAgent.Info().Name
	} else {
		eventFilterKey += agent.EventFilterKeyDelimiter + subAgent.Info().Name
	}

	branchInvocation := baseInvocation.Clone(
		agent.WithInvocationAgent(subAgent),
		agent.WithInvocationEventFilterKey(eventFilterKey),
	)

	return branchInvocation
}

// setupInvocation prepares the invocation for execution.
func (a *ParallelAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name
	invocation.Agent = a
	invocation.AgentName = a.name
}

// handleBeforeAgentCallbacks handles pre-execution callbacks.
func (a *ParallelAgent) handleBeforeAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) bool {
	if a.agentCallbacks == nil {
		return false
	}

	customResponse, err := a.agentCallbacks.RunBeforeAgent(ctx, invocation)
	var evt *event.Event

	if err != nil {
		// Send error event.
		evt = event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
	} else if customResponse != nil {
		// Create an event from the custom response and then close.
		evt = event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
	}

	if evt == nil {
		return false // Continue execution
	}

	agent.EmitEvent(ctx, invocation, eventChan, evt)
	return true
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
			// Recover from panics in sub-agent execution to prevent
			// the whole service from crashing.
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					log.Errorf("Sub-agent execution panic for %s (index: %d, parent: %s): %v\n%s",
						sa.Info().Name, idx, invocation.AgentName, r, string(stack))
					// Send error event for the panic.
					errorEvent := event.NewErrorEvent(
						invocation.InvocationID,
						invocation.AgentName,
						model.ErrorTypeFlowError,
						fmt.Sprintf("sub-agent %s panic: %v", sa.Info().Name, r),
					)
					agent.EmitEvent(ctx, invocation, eventChan, errorEvent)
				}
			}()

			// Create branch invocation for this sub-agent.
			branchInvocation := a.createBranchInvocation(sa, invocation)

			// Reset invocation information in context
			branchAgentCtx := agent.NewInvocationContext(ctx, branchInvocation)

			// Run the sub-agent.
			subEventChan, err := sa.Run(branchAgentCtx, branchInvocation)
			if err != nil {
				// Send error event.
				agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					model.ErrorTypeFlowError,
					err.Error(),
				))
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
	if a.agentCallbacks == nil {
		return
	}

	customResponse, err := a.agentCallbacks.RunAfterAgent(ctx, invocation, nil)
	var evt *event.Event
	if err != nil {
		// Send error event.
		evt = event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
	} else if customResponse != nil {
		// Create an event from the custom response.
		evt = event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
	}

	agent.EmitEvent(ctx, invocation, eventChan, evt)
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
			// Recover from potential panics during event merging.
			defer func() {
				if r := recover(); r != nil {
					// Log the panic but don't propagate error events here since
					// we're already in the event merging phase.
					log.Errorf("Event merging panic in parallel agent %s: %v", a.name, r)
				}
			}()
			for evt := range inputChan {
				if err := event.EmitEvent(ctx, outputChan, evt); err != nil {
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
	return []tool.Tool{}
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
