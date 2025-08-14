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

// Package graphagent provides a graph-based agent implementation.
package graphagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var defaultChannelBufferSize = 256

// Option is a function that configures a GraphAgent.
type Option func(*Options)

// WithDescription sets the description of the agent.
func WithDescription(description string) Option {
	return func(opts *Options) {
		opts.Description = description
	}
}

// WithTools sets the list of tools available to the agent.
func WithTools(tools []tool.Tool) Option {
	return func(opts *Options) {
		opts.Tools = tools
	}
}

// WithAgentCallbacks sets the agent callbacks.
func WithAgentCallbacks(callbacks *agent.Callbacks) Option {
	return func(opts *Options) {
		opts.AgentCallbacks = callbacks
	}
}

// WithModelCallbacks sets the model callbacks.
func WithModelCallbacks(callbacks *model.Callbacks) Option {
	return func(opts *Options) {
		opts.ModelCallbacks = callbacks
	}
}

// WithToolCallbacks sets the tool callbacks.
func WithToolCallbacks(callbacks *tool.Callbacks) Option {
	return func(opts *Options) {
		opts.ToolCallbacks = callbacks
	}
}

// WithInitialState sets the initial state for graph execution.
func WithInitialState(state graph.State) Option {
	return func(opts *Options) {
		opts.InitialState = state
	}
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) Option {
	return func(opts *Options) {
		opts.ChannelBufferSize = size
	}
}

// Options contains configuration options for creating a GraphAgent.
type Options struct {
	// Description is a description of the agent.
	Description string
	// Tools is the list of tools available to the agent.
	Tools []tool.Tool
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.Callbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.Callbacks
	// ToolCallbacks contains callbacks for tool operations.
	ToolCallbacks *tool.Callbacks
	// InitialState is the initial state for graph execution.
	InitialState graph.State
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
}

// GraphAgent is an agent that executes a graph.
type GraphAgent struct {
	name              string
	description       string
	graph             *graph.Graph
	executor          *graph.Executor
	tools             []tool.Tool
	agentCallbacks    *agent.Callbacks
	modelCallbacks    *model.Callbacks
	toolCallbacks     *tool.Callbacks
	initialState      graph.State
	channelBufferSize int
}

// New creates a new GraphAgent with the given graph and options.
func New(name string, g *graph.Graph, opts ...Option) (*GraphAgent, error) {
	// set default channel buffer size.
	var options Options = Options{ChannelBufferSize: defaultChannelBufferSize}

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	executor, err := graph.NewExecutor(g,
		graph.WithChannelBufferSize(options.ChannelBufferSize))
	if err != nil {
		return nil, fmt.Errorf("failed to create graph executor: %w", err)
	}

	return &GraphAgent{
		name:              name,
		description:       options.Description,
		graph:             g,
		executor:          executor,
		tools:             options.Tools,
		agentCallbacks:    options.AgentCallbacks,
		modelCallbacks:    options.ModelCallbacks,
		toolCallbacks:     options.ToolCallbacks,
		initialState:      options.InitialState,
		channelBufferSize: options.ChannelBufferSize,
	}, nil
}

// Run executes the graph with the provided invocation.
func (ga *GraphAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Prepare initial state.
	var initialState graph.State

	if ga.initialState != nil {
		// Clone the base initial state to avoid modifying the original.
		initialState = ga.initialState.Clone()
	} else {
		initialState = make(graph.State)
	}

	// Merge runtime state from RunOptions if provided.
	if invocation.RunOptions.RuntimeState != nil {
		for key, value := range invocation.RunOptions.RuntimeState {
			initialState[key] = value
		}
	}

	// Add invocation message to state.
	if invocation.Message.Content != "" {
		initialState[graph.StateKeyUserInput] = invocation.Message.Content
	}
	// Add session context if available.
	if invocation.Session != nil {
		initialState[graph.StateKeySession] = invocation.Session
	}
	// Set agent callbacks if available.
	if invocation.AgentCallbacks == nil && ga.agentCallbacks != nil {
		invocation.AgentCallbacks = ga.agentCallbacks
	}
	// Set model callbacks if available.
	if invocation.ModelCallbacks == nil && ga.modelCallbacks != nil {
		invocation.ModelCallbacks = ga.modelCallbacks
	}
	// Set tool callbacks if available.
	if invocation.ToolCallbacks == nil && ga.toolCallbacks != nil {
		invocation.ToolCallbacks = ga.toolCallbacks
	}
	// Execute the graph.
	if invocation.AgentCallbacks != nil {
		customResponse, err := invocation.AgentCallbacks.RunBeforeAgent(ctx, invocation)
		if err != nil {
			return nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		if customResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			// Create an event from the custom response.
			customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
			eventChan <- customEvent
			close(eventChan)
			return eventChan, nil
		}
	}
	eventChan, err := ga.executor.Execute(ctx, initialState, invocation)
	if err != nil {
		return nil, err
	}
	if invocation.AgentCallbacks != nil {
		return ga.wrapEventChannel(ctx, invocation, eventChan), nil
	}
	return eventChan, nil
}

// Tools returns the list of tools that this agent has access to.
func (ga *GraphAgent) Tools() []tool.Tool {
	return ga.tools
}

// Info returns the basic information about this agent.
func (ga *GraphAgent) Info() agent.Info {
	return agent.Info{
		Name:        ga.name,
		Description: ga.description,
	}
}

// SubAgents returns the list of sub-agents available to this agent.
func (ga *GraphAgent) SubAgents() []agent.Agent {
	// GraphAgent does not support sub-agents.
	return nil
}

// FindSubAgent finds a sub-agent by name.
func (ga *GraphAgent) FindSubAgent(name string) agent.Agent {
	// GraphAgent does not support sub-agents.
	return nil
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (ga *GraphAgent) wrapEventChannel(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, ga.channelBufferSize)
	go func() {
		defer close(wrappedChan)
		// Forward all events from the original channel
		for evt := range originalChan {
			select {
			case wrappedChan <- evt:
			case <-ctx.Done():
				return
			}
		}
		// After all events are processed, run after agent callbacks
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
			case wrappedChan <- errorEvent:
			case <-ctx.Done():
				return
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
			case wrappedChan <- customEvent:
			case <-ctx.Done():
				return
			}
		}
	}()
	return wrappedChan
}
