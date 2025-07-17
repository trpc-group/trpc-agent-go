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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

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

// WithSubAgents sets the list of sub-agents available to the agent.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(opts *Options) {
		opts.SubAgents = subAgents
	}
}

// WithAgentCallbacks sets the agent callbacks.
func WithAgentCallbacks(callbacks *agent.AgentCallbacks) Option {
	return func(opts *Options) {
		opts.AgentCallbacks = callbacks
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
	// SubAgents is the list of sub-agents available to the agent.
	SubAgents []agent.Agent
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.AgentCallbacks
	// InitialState is the initial state for graph execution.
	InitialState graph.State
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
}

// GraphAgent is an agent that executes a graph.
type GraphAgent struct {
	name           string
	description    string
	graph          *graph.Graph
	executor       *graph.Executor
	tools          []tool.Tool
	subAgents      []agent.Agent
	agentCallbacks *agent.AgentCallbacks
	initialState   graph.State
}

// New creates a new GraphAgent with the given graph and options.
func New(name string, g *graph.Graph, opts ...Option) (*GraphAgent, error) {
	var options Options

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	// Set default channel buffer size if not specified.
	if options.ChannelBufferSize == 0 {
		options.ChannelBufferSize = 256
	}

	executor, err := graph.NewExecutor(g,
		graph.WithChannelBufferSize(options.ChannelBufferSize))
	if err != nil {
		return nil, fmt.Errorf("failed to create graph executor: %w", err)
	}

	return &GraphAgent{
		name:           name,
		description:    options.Description,
		graph:          g,
		executor:       executor,
		tools:          options.Tools,
		subAgents:      options.SubAgents,
		agentCallbacks: options.AgentCallbacks,
		initialState:   options.InitialState,
	}, nil
}

// Run executes the graph with the provided invocation.
func (ga *GraphAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Prepare initial state
	initialState := ga.initialState
	if initialState == nil {
		initialState = make(graph.State)
	}
	// Add invocation message to state
	if invocation.Message.Content != "" {
		initialState[graph.StateKeyUserInput] = invocation.Message.Content
	}
	// Add session context if available
	if invocation.Session != nil {
		initialState[graph.StateKeySession] = invocation.Session
	}
	// Execute the graph
	return ga.executor.Execute(ctx, initialState, invocation.InvocationID)
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
	return ga.subAgents
}

// FindSubAgent finds a sub-agent by name.
func (ga *GraphAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range ga.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}
