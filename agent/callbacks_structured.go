//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// BeforeAgentArgs contains all parameters for before agent callback.
type BeforeAgentArgs struct {
	// Invocation is the invocation context.
	Invocation *Invocation
}

// BeforeAgentResult contains the return value for before agent callback.
type BeforeAgentResult struct {
	// CustomResponse if not nil, will skip agent execution and return this
	// response.
	CustomResponse *model.Response
}

// BeforeAgentCallbackStructured is the before agent callback (structured version).
// Parameters:
// - ctx: context.Context (use InvocationFromContext to get invocation)
// - args: callback arguments
// Returns (result, error).
//   - result: if not nil and CustomResponse is set, agent execution will be
//     skipped.
//   - error: if not nil, agent execution will be stopped with this error.
type BeforeAgentCallbackStructured func(
	ctx context.Context,
	args *BeforeAgentArgs,
) (*BeforeAgentResult, error)

// AfterAgentArgs contains all parameters for after agent callback.
type AfterAgentArgs struct {
	// Invocation is the invocation context.
	Invocation *Invocation
	// Error is the error occurred during agent execution (may be nil).
	Error error
}

// AfterAgentResult contains the return value for after agent callback.
type AfterAgentResult struct {
	// CustomResponse if not nil, will replace the original response.
	CustomResponse *model.Response
}

// AfterAgentCallbackStructured is the after agent callback (structured version).
// Parameters:
// - ctx: context.Context (use InvocationFromContext to get invocation)
// - args: callback arguments
// Returns (result, error).
// - result: if not nil and CustomResponse is set, this response will be used.
// - error: if not nil, this error will be returned.
type AfterAgentCallbackStructured func(
	ctx context.Context,
	args *AfterAgentArgs,
) (*AfterAgentResult, error)

// CallbacksStructured holds structured callbacks for agent operations.
type CallbacksStructured struct {
	// BeforeAgent is a list of callbacks called before the agent runs.
	BeforeAgent []BeforeAgentCallbackStructured
	// AfterAgent is a list of callbacks called after the agent runs.
	AfterAgent []AfterAgentCallbackStructured
}

// NewCallbacksStructured creates a new CallbacksStructured instance for agent.
func NewCallbacksStructured() *CallbacksStructured {
	return &CallbacksStructured{}
}

// RegisterBeforeAgent registers a before agent callback.
func (c *CallbacksStructured) RegisterBeforeAgent(
	cb BeforeAgentCallbackStructured,
) *CallbacksStructured {
	c.BeforeAgent = append(c.BeforeAgent, cb)
	return c
}

// RegisterAfterAgent registers an after agent callback.
func (c *CallbacksStructured) RegisterAfterAgent(
	cb AfterAgentCallbackStructured,
) *CallbacksStructured {
	c.AfterAgent = append(c.AfterAgent, cb)
	return c
}

// RunBeforeAgent runs all before agent callbacks in order.
// Returns (result, error).
// If any callback returns a result with CustomResponse, stop and return.
func (c *CallbacksStructured) RunBeforeAgent(
	ctx context.Context,
	invocation *Invocation,
) (*BeforeAgentResult, error) {
	args := &BeforeAgentArgs{Invocation: invocation}
	for _, cb := range c.BeforeAgent {
		result, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if result != nil && result.CustomResponse != nil {
			return result, nil
		}
	}
	return nil, nil
}

// RunAfterAgent runs all after agent callbacks in order.
// Returns (result, error).
// If any callback returns a result with CustomResponse, stop and return.
func (c *CallbacksStructured) RunAfterAgent(
	ctx context.Context,
	invocation *Invocation,
	runErr error,
) (*AfterAgentResult, error) {
	args := &AfterAgentArgs{
		Invocation: invocation,
		Error:      runErr,
	}
	for _, cb := range c.AfterAgent {
		result, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if result != nil && result.CustomResponse != nil {
			return result, nil
		}
	}
	return nil, nil
}
