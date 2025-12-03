//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chainagent provides a sequential agent implementation.
package chainagent

import "trpc.group/trpc-go/trpc-agent-go/agent"

const defaultChannelBufferSize = 256

// Option configures ChainAgent settings using the functional options pattern.
// This type is exported to allow external packages to create custom options.
type Option func(*Options)

// Options contains all configuration options for ChainAgent.
// This struct is exported to allow external packages to inspect or modify options.
type Options struct {
	subAgents         []agent.Agent
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

var (
	defaultOptions = Options{
		channelBufferSize: defaultChannelBufferSize,
	}
)

// WithSubAgents sets the sub-agents that will be executed in sequence.
// The agents will run one after another, with each agent's output potentially
// influencing the next agent's execution.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(o *Options) { o.subAgents = subAgents }
}

// WithChannelBufferSize sets the buffer size for the event channel.
// This controls how many events can be buffered before blocking.
// Default is 256 if not specified.
func WithChannelBufferSize(size int) Option {
	return func(o *Options) {
		if size < 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}

// WithAgentCallbacks attaches lifecycle callbacks to the chain agent.
// These callbacks allow custom logic to be executed before and after
// the chain agent runs.
func WithAgentCallbacks(cb *agent.Callbacks) Option {
	return func(o *Options) { o.agentCallbacks = cb }
}
