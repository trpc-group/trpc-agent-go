// Package filter provides the core filter functionality.
package filter

import (
	"context"
)

// InterceptionPoint represents all possible interception points for filters.
type InterceptionPoint string

const (
	// Triggered before LLM is invoked.
	PreLLMInvoke InterceptionPoint = "llm.pre_invoke"
	// Triggered after LLM is invoked.
	PostLLMInvoke InterceptionPoint = "llm.post_invoke"

	// Triggered before Tool is invoked.
	PreToolInvoke InterceptionPoint = "tool.pre_invoke"
	// Triggered after Tool is invoked.
	PostToolInvoke InterceptionPoint = "tool.post_invoke"

	// Triggered before Agent is invoked.
	PreAgentInvoke InterceptionPoint = "agent.pre_invoke"
	// Triggered after Agent is invoked.
	PostAgentInvoke InterceptionPoint = "agent.post_invoke"

	// Triggered before Agent executes.
	PreAgentExecute InterceptionPoint = "agent.pre_execute"
	// Triggered after Agent executes.
	PostAgentExecute InterceptionPoint = "agent.post_execute"

	// Triggered during Agent's streaming invoke, for each generated chunk.
	AgentStreamInvoke InterceptionPoint = "agent.stream_invoke"
	// Triggered during Agent's streaming execute, for each received chunk.
	AgentStreamExecute InterceptionPoint = "agent.stream_execute"
)

// AgentContext is the context for agent execution.
// You can extend this interface to include more fields as needed.
type AgentContext interface {
	context.Context
	// Add more methods if needed.
}

// Filter defines the interface for all filters.
type Filter interface {
	// Type returns the list of interception points supported by this filter.
	// Except for stream points, other points should appear in pre/post pairs.
	// Registration will check that the filter only executes on supported points.
	Type() []InterceptionPoint

	// PreInvoke is called before the target method is executed.
	// Return true to continue the filter chain and core logic.
	// Return false to interrupt the chain; subsequent filters and core logic will not be executed.
	PreInvoke(ctx AgentContext, point InterceptionPoint) (bool, error)

	// PostInvoke is called after the target method is executed (regardless of success or failure).
	// Return true if the post-processing logic succeeded.
	// Return false if the post-processing logic encountered a problem (mainly for logging or status marking).
	// This return value does not directly stop the execution of other post_invoke filters.
	PostInvoke(ctx AgentContext, point InterceptionPoint) (bool, error)
}
