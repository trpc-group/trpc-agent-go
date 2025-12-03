//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package cycleagent provides a looping agent implementation.
package cycleagent

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const defaultChannelBufferSize = 256

// EscalationFunc is a callback function that determines if an event should
// trigger escalation (stop the cycle). Return true to stop the cycle.
type EscalationFunc func(*event.Event) bool

// Option configures CycleAgent settings using the functional options pattern.
// This type is exported to allow external packages to create custom options.
type Option func(*Options)

// Options contains all configuration options for CycleAgent.
// This struct is exported to allow external packages to inspect or modify options.
type Options struct {
	subAgents         []agent.Agent
	maxIterations     *int
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
	escalationFunc    EscalationFunc
}

var (
	defaultOptions = Options{channelBufferSize: defaultChannelBufferSize}
)

// WithSubAgents sets the sub-agents that will be executed in a loop.
// The agents will run repeatedly until an escalation condition is met
// or the maximum number of iterations is reached.
func WithSubAgents(sub []agent.Agent) Option {
	return func(o *Options) { o.subAgents = sub }
}

// WithMaxIterations sets the maximum number of loop iterations.
// If not set, the loop will continue until an escalation condition is met.
// This prevents infinite loops in case escalation detection fails.
func WithMaxIterations(max int) Option {
	return func(o *Options) { o.maxIterations = &max }
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

// WithAgentCallbacks attaches lifecycle callbacks to the cycle agent.
// These callbacks allow custom logic to be executed before and after
// the cycle agent runs.
func WithAgentCallbacks(cb *agent.Callbacks) Option {
	return func(o *Options) { o.agentCallbacks = cb }
}

// WithEscalationFunc sets a custom function to detect escalation conditions.
// This function determines when the loop should stop based on events.
// If not set, a default escalation detection is used.
func WithEscalationFunc(f EscalationFunc) Option {
	return func(o *Options) { o.escalationFunc = f }
}
