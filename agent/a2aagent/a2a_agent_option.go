//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"strings"

	"trpc.group/trpc-go/trpc-a2a-go/server"
)

// Option configures the A2AAgent
type Option func(*A2AAgent)

// WithName sets the name of agent
func WithName(name string) Option {
	return func(a *A2AAgent) {
		a.name = name
	}
}

// WithDescription sets the agent description
func WithDescription(description string) Option {
	return func(a *A2AAgent) {
		a.description = description
	}
}

// WithAgentCardURL set the agent card URL
func WithAgentCardURL(url string) Option {
	return func(a *A2AAgent) {
		a.agentURL = strings.TrimSpace(url)
	}
}

// WithAgentCard set the agent card
func WithAgentCard(agentCard *server.AgentCard) Option {
	return func(a *A2AAgent) {
		a.agentCard = agentCard
	}
}

// WithForceNonStreaming forces the agent to use non-streaming mode
// even if the remote agent supports streaming
func WithForceNonStreaming(force bool) Option {
	return func(a *A2AAgent) {
		a.forceNonStreaming = force
	}
}

// WithCustomEventConverter adds a custom A2A event converter to the A2AAgent.
func WithCustomEventConverter(converter A2AEventConverter) Option {
	return func(a *A2AAgent) {
		a.eventConverter = converter
	}
}

// WithCustomA2AConverter adds a custom A2A message converter to the A2AAgent.
// This converter will be used to convert invocations to A2A protocol messages.
func WithCustomA2AConverter(converter InvocationA2AConverter) Option {
	return func(a *A2AAgent) {
		a.a2aMessageConverter = converter
	}
}
