//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
)

// BeforeModelArgs contains all parameters for before model callback.
type BeforeModelArgs struct {
	// Request is the request about to be sent to the model (can be modified).
	Request *Request
}

// BeforeModelResult contains the return value for before model callback.
type BeforeModelResult struct {
	// CustomResponse if not nil, will skip model call and return this response.
	CustomResponse *Response
}

// BeforeModelCallbackStructured is the before model callback (structured version).
// Parameters:
// - ctx: context.Context (use agent.InvocationFromContext to get invocation)
// - args: callback arguments
// Returns (result, error).
// - result: if not nil and CustomResponse is set, model call will be skipped.
// - error: if not nil, model call will be stopped with this error.
type BeforeModelCallbackStructured func(
	ctx context.Context,
	args *BeforeModelArgs,
) (*BeforeModelResult, error)

// AfterModelArgs contains all parameters for after model callback.
type AfterModelArgs struct {
	// Request is the original request sent to the model.
	Request *Request
	// Response is the response returned by the model (may be nil).
	Response *Response
	// Error is the error occurred during model call (may be nil).
	Error error
}

// AfterModelResult contains the return value for after model callback.
type AfterModelResult struct {
	// CustomResponse if not nil, will replace the original response.
	CustomResponse *Response
}

// AfterModelCallbackStructured is the after model callback (structured version).
// Parameters:
// - ctx: context.Context (use agent.InvocationFromContext to get invocation)
// - args: callback arguments
// Returns (result, error).
// - result: if not nil and CustomResponse is set, this response will be used.
// - error: if not nil, this error will be returned.
type AfterModelCallbackStructured func(
	ctx context.Context,
	args *AfterModelArgs,
) (*AfterModelResult, error)

// CallbacksStructured holds structured callbacks for model operations.
type CallbacksStructured struct {
	// BeforeModel is a list of callbacks called before the model is invoked.
	BeforeModel []BeforeModelCallbackStructured
	// AfterModel is a list of callbacks called after the model is invoked.
	AfterModel []AfterModelCallbackStructured
}

// NewCallbacksStructured creates a new CallbacksStructured instance for model.
func NewCallbacksStructured() *CallbacksStructured {
	return &CallbacksStructured{}
}

// RegisterBeforeModel registers a before model callback.
func (c *CallbacksStructured) RegisterBeforeModel(
	cb BeforeModelCallbackStructured,
) *CallbacksStructured {
	c.BeforeModel = append(c.BeforeModel, cb)
	return c
}

// RegisterAfterModel registers an after model callback.
func (c *CallbacksStructured) RegisterAfterModel(
	cb AfterModelCallbackStructured,
) *CallbacksStructured {
	c.AfterModel = append(c.AfterModel, cb)
	return c
}

// RunBeforeModel runs all before model callbacks in order.
// Returns (result, error).
// If any callback returns a result with CustomResponse, stop and return.
func (c *CallbacksStructured) RunBeforeModel(
	ctx context.Context,
	req *Request,
) (*BeforeModelResult, error) {
	args := &BeforeModelArgs{Request: req}
	for _, cb := range c.BeforeModel {
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

// RunAfterModel runs all after model callbacks in order.
// Returns (result, error).
// If any callback returns a result with CustomResponse, stop and return.
func (c *CallbacksStructured) RunAfterModel(
	ctx context.Context,
	req *Request,
	rsp *Response,
	modelErr error,
) (*AfterModelResult, error) {
	args := &AfterModelArgs{
		Request:  req,
		Response: rsp,
		Error:    modelErr,
	}
	for _, cb := range c.AfterModel {
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
