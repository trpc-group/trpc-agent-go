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

import "trpc.group/trpc-go/trpc-agent-go/agent"

const defaultChannelBufferSize = 256

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

var (
	defaultOptions = Options{channelBufferSize: defaultChannelBufferSize}
)

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
	return func(o *Options) {
		if size < 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}

// WithAgentCallbacks attaches lifecycle callbacks to the parallel agent.
// These callbacks allow custom logic to be executed before and after
// the parallel agent runs.
func WithAgentCallbacks(cb *agent.Callbacks) Option {
	return func(o *Options) { o.agentCallbacks = cb }
}
