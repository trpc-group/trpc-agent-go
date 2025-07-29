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
type BeforeAgentCallback func(ctx context.Context, invocation *Invocation) (*model.Response, error)

// AfterAgentCallback is called after the agent runs.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be used instead of the actual agent response.
// - error: if not nil, this error will be returned.
type AfterAgentCallback func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, error)

// Callbacks holds callbacks for agent operations.
type Callbacks struct {
	// BeforeAgent is a list of callbacks that are called before the agent runs.
	BeforeAgent []BeforeAgentCallback
	// AfterAgent is a list of callbacks that are called after the agent runs.
	AfterAgent []AfterAgentCallback
}

// NewCallbacks creates a new Callbacks instance for agent.
func NewCallbacks() *Callbacks {
	return &Callbacks{}
}

// RegisterBeforeAgent registers a before agent callback.
func (c *Callbacks) RegisterBeforeAgent(cb BeforeAgentCallback) {
	c.BeforeAgent = append(c.BeforeAgent, cb)
}

// RegisterAfterAgent registers an after agent callback.
func (c *Callbacks) RegisterAfterAgent(cb AfterAgentCallback) {
	c.AfterAgent = append(c.AfterAgent, cb)
}

// RunBeforeAgent runs all before agent callbacks in order.
// Returns (customResponse, error).
// If any callback returns a custom response, stop and return.
func (c *Callbacks) RunBeforeAgent(
	ctx context.Context,
	invocation *Invocation,
) (*model.Response, error) {
	for _, cb := range c.BeforeAgent {
		customResponse, err := cb(ctx, invocation)
		if err != nil {
			return nil, err
		}
		if customResponse != nil {
			return customResponse, nil
		}
	}
	return nil, nil
}

// RunAfterAgent runs all after agent callbacks in order.
// Returns (customResponse, error).
// If any callback returns a custom response, stop and return.
func (c *Callbacks) RunAfterAgent(
	ctx context.Context,
	invocation *Invocation,
	runErr error,
) (*model.Response, error) {
	for _, cb := range c.AfterAgent {
		customResponse, err := cb(ctx, invocation, runErr)
		if err != nil {
			return nil, err
		}
		if customResponse != nil {
			return customResponse, nil
		}
	}
	return nil, nil
}
