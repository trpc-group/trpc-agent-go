//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package cycleagent provides a looping agent implementation.
package cycleagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultChannelBufferSize = 256

// EscalationFunc is a callback function that determines if an event should
// trigger escalation (stop the cycle). Return true to stop the cycle.
type EscalationFunc func(*event.Event) bool

// CycleAgent is an agent that runs its sub-agents in a loop.
// When a sub-agent generates an event with escalation or max_iterations are
// reached, the cycle agent will stop.
type CycleAgent struct {
	name              string
	subAgents         []agent.Agent
	tools             []tool.Tool
	maxIterations     *int // Optional maximum number of iterations
	channelBufferSize int
	agentCallbacks    *agent.AgentCallbacks
	escalationFunc    EscalationFunc // Injectable escalation logic
}

// option configures CycleAgent.
type option func(*options)

type options struct {
	subAgents         []agent.Agent
	tools             []tool.Tool
	maxIterations     *int
	channelBufferSize int
	agentCallbacks    *agent.AgentCallbacks
	escalationFunc    EscalationFunc
}

// WithSubAgents sets sub-agents for the loop.
func WithSubAgents(sub []agent.Agent) option {
	return func(o *options) { o.subAgents = sub }
}

// WithTools configures tools.
func WithTools(tools []tool.Tool) option {
	return func(o *options) { o.tools = tools }
}

// WithMaxIterations limits loop iterations.
func WithMaxIterations(max int) option {
	return func(o *options) { o.maxIterations = &max }
}

// WithChannelBufferSize sets event channel buffer.
func WithChannelBufferSize(size int) option {
	return func(o *options) { o.channelBufferSize = size }
}

// WithAgentCallbacks attaches callbacks.
func WithAgentCallbacks(cb *agent.AgentCallbacks) option {
	return func(o *options) { o.agentCallbacks = cb }
}

// WithEscalationFunc sets custom escalation detection.
func WithEscalationFunc(f EscalationFunc) option {
	return func(o *options) { o.escalationFunc = f }
}

// New instantiates a CycleAgent using functional options.
func New(name string, opts ...option) *CycleAgent {
	cfg := options{channelBufferSize: defaultChannelBufferSize}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.channelBufferSize <= 0 {
		cfg.channelBufferSize = defaultChannelBufferSize
	}
	return &CycleAgent{
		name:              name,
		subAgents:         cfg.subAgents,
		tools:             cfg.tools,
		maxIterations:     cfg.maxIterations,
		channelBufferSize: cfg.channelBufferSize,
		agentCallbacks:    cfg.agentCallbacks,
		escalationFunc:    cfg.escalationFunc,
	}
}

// createSubAgentInvocation creates a proper invocation for sub-agents with correct attribution.
// This ensures events from sub-agents have the correct Author field set.
func (a *CycleAgent) createSubAgentInvocation(
	subAgent agent.Agent,
	baseInvocation *agent.Invocation,
) *agent.Invocation {
	// Create a copy of the invocation - no shared state mutation.
	subInvocation := *baseInvocation

	// Update agent-specific fields for proper agent attribution.
	subInvocation.Agent = subAgent
	subInvocation.AgentName = subAgent.Info().Name
	subInvocation.TransferInfo = nil // Clear transfer info for sub-agents.

	// Set branch info for hierarchical event filtering.
	// Do not use the sub-agent name here, it will cause the sub-agent unable to see the
	// previous agent's conversation history.
	if baseInvocation.Branch != "" {
		subInvocation.Branch = baseInvocation.Branch
	} else {
		subInvocation.Branch = a.name
	}

	return &subInvocation
}

// shouldEscalate checks if an event indicates escalation using injectable logic.
func (a *CycleAgent) shouldEscalate(evt *event.Event) bool {
	if evt == nil {
		return false
	}

	// Only check escalation for meaningful events, not streaming chunks
	if !a.isEscalationCheckEvent(evt) {
		return false
	}

	// Use custom escalation function if provided.
	if a.escalationFunc != nil {
		return a.escalationFunc(evt)
	}

	// Default escalation logic: error events.
	if evt.Error != nil {
		return true
	}

	// Check for done events that might indicate completion or escalation.
	return evt.Done && evt.Object == model.ObjectTypeError
}

// isEscalationCheckEvent determines if an event should be checked for escalation.
// Only check meaningful completion events, not streaming chunks or preprocessing.
func (a *CycleAgent) isEscalationCheckEvent(evt *event.Event) bool {
	// Always check error events
	if evt.Error != nil {
		return true
	}

	// Check tool response events (these contain our quality assessment results)
	if evt.Object == model.ObjectTypeToolResponse {
		return true
	}

	// Check final completion events (not streaming chunks)
	if evt.Done && evt.Response != nil && evt.Object != "chat.completion.chunk" {
		return true
	}

	// Skip streaming chunks, preprocessing events, etc.
	return false
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
				// Create a proper invocation for the sub-agent with correct attribution.
				subInvocation := a.createSubAgentInvocation(subAgent, invocation)

				// Run the sub-agent.
				subEventChan, err := subAgent.Run(ctx, subInvocation)
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

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *CycleAgent) Tools() []tool.Tool {
	return a.tools
}

// Info implements the agent.Agent interface.
// It returns the basic information about this agent.
func (a *CycleAgent) Info() agent.Info {
	maxIterStr := "unlimited"
	if a.maxIterations != nil {
		maxIterStr = fmt.Sprintf("%d", *a.maxIterations)
	}
	return agent.Info{
		Name: a.name,
		Description: fmt.Sprintf(
			"Cycle agent that runs %d sub-agents in a loop (max iterations: %s)",
			len(a.subAgents), maxIterStr,
		),
	}
}

// SubAgents implements the agent.Agent interface.
// It returns the list of sub-agents available to this agent.
func (a *CycleAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

// FindSubAgent implements the agent.Agent interface.
// It finds a sub-agent by name and returns nil if not found.
func (a *CycleAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}
