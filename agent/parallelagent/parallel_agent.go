//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
	agentCallbacks    *agent.AgentCallbacks
}

// option configures ParallelAgent.
type option func(*options)

type options struct {
	subAgents         []agent.Agent
	tools             []tool.Tool
	channelBufferSize int
	agentCallbacks    *agent.AgentCallbacks
}

// WithSubAgents sets the parallel sub-agents.
func WithSubAgents(sub []agent.Agent) option {
	return func(o *options) { o.subAgents = sub }
}

// WithTools registers tools.
func WithTools(tools []tool.Tool) option {
	return func(o *options) { o.tools = tools }
}

// WithChannelBufferSize sets buffer size.
func WithChannelBufferSize(size int) option {
	return func(o *options) { o.channelBufferSize = size }
}

// WithAgentCallbacks attaches callbacks.
func WithAgentCallbacks(cb *agent.AgentCallbacks) option {
	return func(o *options) { o.agentCallbacks = cb }
}

// New instantiates a ParallelAgent using functional options.
func New(name string, opts ...option) *ParallelAgent {
	cfg := options{channelBufferSize: defaultChannelBufferSize}
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

// createBranchInvocationForSubAgent creates an isolated branch invocation for each sub-agent.
// This ensures parallel execution doesn't interfere with each other.
func (a *ParallelAgent) createBranchInvocationForSubAgent(
	subAgent agent.Agent,
	baseInvocation *agent.Invocation,
) *agent.Invocation {
	// Create a copy of the invocation.
	branchInvocation := *baseInvocation
	branchInvocation.Agent = subAgent
	branchInvocation.AgentName = subAgent.Info().Name

	// Create unique invocation ID for this branch.
	branchSuffix := a.name + "." + branchInvocation.AgentName
	branchInvocation.InvocationID = baseInvocation.InvocationID + "." + branchSuffix

	// Set branch identifier for hierarchical event filtering.
	branchInvocation.Branch = branchInvocation.InvocationID

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

		// Set agent callbacks if available.
		if invocation.AgentCallbacks == nil && a.agentCallbacks != nil {
			invocation.AgentCallbacks = a.agentCallbacks
		}

		// Run before agent callbacks if they exist.
		if invocation.AgentCallbacks != nil {
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
				return
			}
			if customResponse != nil {
				// Create an event from the custom response and then close.
				customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
				select {
				case eventChan <- customEvent:
				case <-ctx.Done():
				}
				return
			}
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

		// Run after agent callbacks if they exist.
		if invocation.AgentCallbacks != nil {
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
				customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
				select {
				case eventChan <- customEvent:
				case <-ctx.Done():
				}
			}
		}
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
