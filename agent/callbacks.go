//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent provides the core agent functionality.
package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ErrorTypeAgentCallbackError is used for errors from agent callbacks (before/after hooks).
const ErrorTypeAgentCallbackError = "agent_callback_error"

// BeforeAgentCallback is called before the agent runs.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be returned to user and agent execution will be skipped.
// - error: if not nil, agent execution will be stopped with this error.
// Deprecated: Use BeforeAgentCallbackStructured instead for better type safety and context passing.
type BeforeAgentCallback = func(ctx context.Context, invocation *Invocation) (*model.Response, error)

// AfterAgentCallback is called after the agent runs.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be used instead of the actual agent response.
// - error: if not nil, this error will be returned.
// Deprecated: Use AfterAgentCallbackStructured instead for better type safety and context passing.
type AfterAgentCallback = func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error)

// BeforeAgentArgs contains all parameters for before agent callback.
type BeforeAgentArgs struct {
	// Invocation is the invocation context.
	Invocation *Invocation
}

// BeforeAgentResult contains the return value for before agent callback.
type BeforeAgentResult struct {
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResponse if not nil, will skip agent execution and return this response.
	CustomResponse *model.Response
}

// BeforeAgentCallbackStructured is called before the agent runs.
// Returns (result, error).
// - result: contains optional custom response and context for subsequent operations.
//   - CustomResponse: if not nil, this response will be returned to user and agent execution will be skipped.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//
// - error: if not nil, agent execution will be stopped with this error.
type BeforeAgentCallbackStructured = func(
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
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResponse if not nil, will replace the original response.
	CustomResponse *model.Response
}

// AfterAgentCallbackStructured is called after the agent runs.
// Returns (result, error).
// - result: contains optional custom response and context for subsequent operations.
//   - CustomResponse: if not nil, this response will be used instead of the actual agent response.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//
// - error: if not nil, this error will be returned.
type AfterAgentCallbackStructured = func(
	ctx context.Context,
	args *AfterAgentArgs,
) (*AfterAgentResult, error)

// Callbacks holds callbacks for agent operations.
// Internally stores the new structured callback types.
type Callbacks struct {
	// BeforeAgent is a list of callbacks called before the agent runs.
	BeforeAgent []BeforeAgentCallbackStructured
	// AfterAgent is a list of callbacks called after the agent runs.
	AfterAgent []AfterAgentCallbackStructured
}

// NewCallbacks creates a new Callbacks instance for agent.
func NewCallbacks() *Callbacks {
	return &Callbacks{}
}

// RegisterBeforeAgent registers a before agent callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterBeforeAgent(cb any) *Callbacks {
	switch callback := cb.(type) {
	case BeforeAgentCallbackStructured:
		c.BeforeAgent = append(c.BeforeAgent, callback)
	case BeforeAgentCallback:
		wrapped := func(ctx context.Context, args *BeforeAgentArgs) (*BeforeAgentResult, error) {
			// Call old signature
			resp, err := callback(ctx, args.Invocation)
			if err != nil {
				return nil, err
			}
			if resp != nil {
				return &BeforeAgentResult{CustomResponse: resp}, nil
			}
			return &BeforeAgentResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.BeforeAgent = append(c.BeforeAgent, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// RegisterAfterAgent registers an after agent callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterAfterAgent(cb any) *Callbacks {
	switch callback := cb.(type) {
	case AfterAgentCallbackStructured:
		c.AfterAgent = append(c.AfterAgent, callback)
	case AfterAgentCallback:
		wrapped := func(ctx context.Context, args *AfterAgentArgs) (*AfterAgentResult, error) {
			// Call old signature
			resp, err := callback(ctx, args.Invocation, args.Error)
			if err != nil {
				return nil, err
			}
			if resp != nil {
				return &AfterAgentResult{CustomResponse: resp}, nil
			}
			return &AfterAgentResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.AfterAgent = append(c.AfterAgent, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// RunBeforeAgent runs all before agent callbacks in order.
// This method uses the new structured callback interface.
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunBeforeAgent(
	ctx context.Context,
	args *BeforeAgentArgs,
) (*BeforeAgentResult, error) {
	var lastResult *BeforeAgentResult
	for _, cb := range c.BeforeAgent {
		result, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if result != nil {
			// Use the context from result if provided for subsequent callbacks.
			if result.Context != nil {
				ctx = result.Context
			}
			lastResult = result
			if result.CustomResponse != nil {
				return result, nil
			}
		}
	}
	// Return nil if lastResult is empty (no Context and no CustomResponse).
	if lastResult != nil && lastResult.Context == nil && lastResult.CustomResponse == nil {
		return nil, nil
	}
	return lastResult, nil
}

// RunAfterAgent runs all after agent callbacks in order.
// This method uses the new structured callback interface.
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunAfterAgent(
	ctx context.Context,
	args *AfterAgentArgs,
) (*AfterAgentResult, error) {
	var lastResult *AfterAgentResult
	for _, cb := range c.AfterAgent {
		result, err := cb(ctx, args)
		if err != nil {
			return nil, err
		}
		if result != nil {
			// Use the context from result if provided for subsequent callbacks.
			if result.Context != nil {
				ctx = result.Context
			}
			lastResult = result
			if result.CustomResponse != nil {
				return result, nil
			}
		}
	}
	// Return nil if lastResult is empty (no Context and no CustomResponse).
	if lastResult != nil && lastResult.Context == nil && lastResult.CustomResponse == nil {
		return nil, nil
	}
	return lastResult, nil
}
