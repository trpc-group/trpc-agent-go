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

// New creates a new ParallelAgent with the given name and options.
// ParallelAgent executes all its sub-agents simultaneously and merges
// their event streams into a single output channel.
func New(name string, opts ...Option) *ParallelAgent {
	cfg := defaultOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
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
// Returns the updated context and whether execution should stop early.
func (a *ParallelAgent) handleBeforeAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) (context.Context, bool) {
	if a.agentCallbacks == nil {
		return ctx, false
	}

	result, err := a.agentCallbacks.RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
		Invocation: invocation,
	})
	// Use the context from result if provided.
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	var evt *event.Event

	if err != nil {
		// Send error event.
		evt = event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
	} else if result != nil && result.CustomResponse != nil {
		// Create an event from the custom response and then close.
		evt = event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, result.CustomResponse)
	}

	if evt == nil {
		return ctx, false // Continue execution
	}

	agent.EmitEvent(ctx, invocation, eventChan, evt)
	return ctx, true
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
		runCtx := agent.CloneContext(ctx)
		go func(ctx context.Context, idx int, sa agent.Agent) {
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
		}(runCtx, i, subAgent)
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
	fullRespEvent *event.Event,
) {
	if a.agentCallbacks == nil {
		return
	}

	result, err := a.agentCallbacks.RunAfterAgent(ctx, &agent.AfterAgentArgs{
		Invocation:        invocation,
		Error:             nil,
		FullResponseEvent: fullRespEvent,
	})
	// Use the context from result if provided.
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	var evt *event.Event
	if err != nil {
		// Send error event.
		evt = event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
	} else if result != nil && result.CustomResponse != nil {
		// Create an event from the custom response.
		evt = event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, result.CustomResponse)
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

	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)
		a.executeParallelRun(ctx, invocation, eventChan)
	}(runCtx)

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
	var shouldReturn bool
	ctx, shouldReturn = a.handleBeforeAgentCallbacks(ctx, invocation, eventChan)
	if shouldReturn {
		return
	}

	// Start sub-agents.
	eventChans := a.startSubAgents(ctx, invocation, eventChan)

	// Merge events from all sub-agents and collect full response event.
	var fullRespEvent *event.Event
	a.mergeEventStreams(ctx, eventChans, eventChan, &fullRespEvent)

	// Handle after agent callbacks.
	a.handleAfterAgentCallbacks(ctx, invocation, eventChan, fullRespEvent)
}

// mergeEventStreams merges multiple event channels into a single output channel.
// This implementation processes events as they arrive from different sub-agents.
func (a *ParallelAgent) mergeEventStreams(
	ctx context.Context,
	eventChans []<-chan *event.Event,
	outputChan chan<- *event.Event,
	fullRespEvent **event.Event,
) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Start a goroutine for each input channel.
	for _, ch := range eventChans {
		if ch == nil {
			continue
		}

		runCtx := agent.CloneContext(ctx)
		wg.Add(1)
		go func(ctx context.Context, inputChan <-chan *event.Event) {
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
				if evt != nil && evt.Response != nil && !evt.Response.IsPartial {
					mu.Lock()
					*fullRespEvent = evt
					mu.Unlock()
				}
				if err := event.EmitEvent(ctx, outputChan, evt); err != nil {
					return
				}
			}
		}(runCtx, ch)
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
