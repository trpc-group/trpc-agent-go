// Package agent provides the core agent functionality.
package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

// BeforeAgentCallback is called before the agent runs.
// Returns (customResponse, skip, error).
// - customResponse: if not nil, this response will be returned to user and agent execution will be skipped.
// - skip: if true, agent execution will be skipped.
// - error: if not nil, agent execution will be stopped with this error.
type BeforeAgentCallback func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error)

// AfterAgentCallback is called after the agent runs.
// Returns (customResponse, override, error).
// - customResponse: if not nil and override is true, this response will be used instead of the actual agent response.
// - override: if true, the customResponse will be used.
// - error: if not nil, this error will be returned.
type AfterAgentCallback func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error)

// AgentCallbacks holds callbacks for agent operations.
type AgentCallbacks struct {
	BeforeAgent []BeforeAgentCallback
	AfterAgent  []AfterAgentCallback
}

// NewAgentCallbacks creates a new AgentCallbacks instance.
func NewAgentCallbacks() *AgentCallbacks {
	return &AgentCallbacks{}
}

// RegisterBeforeAgent registers a before agent callback.
func (c *AgentCallbacks) RegisterBeforeAgent(cb BeforeAgentCallback) {
	c.BeforeAgent = append(c.BeforeAgent, cb)
}

// RegisterAfterAgent registers an after agent callback.
func (c *AgentCallbacks) RegisterAfterAgent(cb AfterAgentCallback) {
	c.AfterAgent = append(c.AfterAgent, cb)
}

// RunBeforeAgent runs all before agent callbacks in order.
// Returns (customResponse, skip, error).
// If any callback returns a custom response or skip=true, stop and return.
func (c *AgentCallbacks) RunBeforeAgent(
	ctx context.Context,
	invocation *Invocation,
) (*model.Response, bool, error) {
	for _, cb := range c.BeforeAgent {
		customResponse, skip, err := cb(ctx, invocation)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil || skip {
			return customResponse, skip, nil
		}
	}
	return nil, false, nil
}

// RunAfterAgent runs all after agent callbacks in order.
// Returns (customResponse, override, error).
// If any callback returns a custom response with override=true, stop and return.
func (c *AgentCallbacks) RunAfterAgent(
	ctx context.Context,
	invocation *Invocation,
	runErr error,
) (*model.Response, bool, error) {
	for _, cb := range c.AfterAgent {
		customResponse, override, err := cb(ctx, invocation, runErr)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil && override {
			return customResponse, true, nil
		}
	}
	return nil, false, nil
}
