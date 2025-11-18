//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model provides interfaces for working with LLMs.
package model

import (
	"context"
)

// BeforeModelCallback is called before the model is invoked. It can mutate the request.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be returned to user and model call will be skipped.
// - error: if not nil, model call will be stopped with this error.
// Deprecated: Use BeforeModelCallbackStructured instead for better type safety and context passing.
type BeforeModelCallback = func(ctx context.Context, req *Request) (*Response, error)

// AfterModelCallback is called after the model is invoked.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be used instead of the actual model response.
// - error: if not nil, this error will be returned.
// Deprecated: Use AfterModelCallbackStructured instead for better type safety and context passing.
type AfterModelCallback = func(ctx context.Context, req *Request, rsp *Response, modelErr error) (*Response, error)

// BeforeModelArgs contains all parameters for before model callback.
type BeforeModelArgs struct {
	// Request is the request about to be sent to the model (can be modified).
	Request *Request
}

// BeforeModelResult contains the return value for before model callback.
type BeforeModelResult struct {
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResponse if not nil, will skip model call and return this response.
	CustomResponse *Response
}

// BeforeModelCallbackStructured is called before the model is invoked.
// Returns (result, error).
// - result: contains optional custom response and context for subsequent operations.
//   - CustomResponse: if not nil, this response will be returned to user and model call will be skipped.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//
// - error: if not nil, model call will be stopped with this error.
type BeforeModelCallbackStructured = func(
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
	// Context if not nil, will be used by the framework for subsequent operations.
	Context context.Context
	// CustomResponse if not nil, will replace the original response.
	CustomResponse *Response
}

// AfterModelCallbackStructured is called after the model is invoked.
// Returns (result, error).
// - result: contains optional custom response and context for subsequent operations.
//   - CustomResponse: if not nil, this response will be used instead of the actual model response.
//   - Context: if not nil, will be used by the framework for subsequent operations.
//
// - error: if not nil, this error will be returned.
type AfterModelCallbackStructured = func(
	ctx context.Context,
	args *AfterModelArgs,
) (*AfterModelResult, error)

// Callbacks holds callbacks for model operations.
// Internally stores the new structured callback types.
type Callbacks struct {
	// BeforeModel is a list of callbacks called before the model is invoked.
	BeforeModel []BeforeModelCallbackStructured
	// AfterModel is a list of callbacks called after the model is invoked.
	AfterModel []AfterModelCallbackStructured
	// continueOnError controls whether to continue executing callbacks when an error occurs.
	// Default: false (stop on first error)
	continueOnError bool
	// continueOnResponse controls whether to continue executing callbacks when a CustomResponse is returned.
	// Default: false (stop on first CustomResponse)
	continueOnResponse bool
}

// CallbacksOption configures Callbacks behavior.
type CallbacksOption func(*Callbacks)

// WithContinueOnError sets whether to continue executing callbacks when an error occurs.
func WithContinueOnError(continueOnError bool) CallbacksOption {
	return func(c *Callbacks) {
		c.continueOnError = continueOnError
	}
}

// WithContinueOnResponse sets whether to continue executing callbacks when a CustomResponse is returned.
func WithContinueOnResponse(continueOnResponse bool) CallbacksOption {
	return func(c *Callbacks) {
		c.continueOnResponse = continueOnResponse
	}
}

// NewCallbacks creates a new Callbacks instance for model.
func NewCallbacks(opts ...CallbacksOption) *Callbacks {
	c := &Callbacks{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// RegisterBeforeModel registers a before model callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterBeforeModel(cb any) *Callbacks {
	switch callback := cb.(type) {
	case BeforeModelCallbackStructured:
		c.BeforeModel = append(c.BeforeModel, callback)
	case BeforeModelCallback:
		wrapped := func(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
			// Call old signature
			resp, err := callback(ctx, args.Request)
			if err != nil {
				return nil, err
			}
			if resp != nil {
				return &BeforeModelResult{CustomResponse: resp}, nil
			}
			return &BeforeModelResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.BeforeModel = append(c.BeforeModel, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// RegisterAfterModel registers an after model callback.
// Supports both old and new callback function signatures.
// Old signatures are automatically wrapped into new signatures.
func (c *Callbacks) RegisterAfterModel(cb any) *Callbacks {
	switch callback := cb.(type) {
	case AfterModelCallbackStructured:
		c.AfterModel = append(c.AfterModel, callback)
	case AfterModelCallback:
		wrapped := func(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
			// Call old signature
			resp, err := callback(ctx, args.Request, args.Response, args.Error)
			if err != nil {
				return nil, err
			}
			if resp != nil {
				return &AfterModelResult{CustomResponse: resp}, nil
			}
			return &AfterModelResult{}, nil // Return empty result to indicate callback was executed.
		}
		c.AfterModel = append(c.AfterModel, wrapped)
	default:
		panic("unsupported callback type")
	}
	return c
}

// handleCallbackError processes callback error and returns whether to continue.
func (c *Callbacks) handleCallbackError(err error, firstErr *error) (shouldStop bool) {
	if err == nil {
		return false
	}
	if !c.continueOnError {
		return true
	}
	if *firstErr == nil {
		*firstErr = err
	}
	return false
}

// processBeforeModelResult processes before model callback result and updates context.
// Returns whether to stop execution immediately.
func (c *Callbacks) processBeforeModelResult(
	result *BeforeModelResult,
	ctx *context.Context,
	lastResult **BeforeModelResult,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	if result.Context != nil {
		*ctx = result.Context
	}
	if result.CustomResponse != nil {
		*lastResult = result
		if !c.continueOnResponse {
			return true
		}
	} else {
		*lastResult = result
	}
	return false
}

// finalizeBeforeModelResult determines the final return value for before model callbacks.
func (c *Callbacks) finalizeBeforeModelResult(
	lastResult *BeforeModelResult,
	firstErr error,
) (*BeforeModelResult, error) {
	if lastResult != nil && lastResult.CustomResponse != nil {
		if c.continueOnError && firstErr != nil {
			return lastResult, firstErr
		}
		return lastResult, nil
	}
	if c.continueOnError && firstErr != nil {
		return lastResult, firstErr
	}
	if lastResult != nil && lastResult.Context == nil && lastResult.CustomResponse == nil {
		return nil, nil
	}
	return lastResult, nil
}

// RunBeforeModel runs all before model callbacks in order.
// This method uses the new structured callback interface.
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunBeforeModel(ctx context.Context, args *BeforeModelArgs) (*BeforeModelResult, error) {
	var lastResult *BeforeModelResult
	var firstErr error

	for _, cb := range c.BeforeModel {
		result, err := cb(ctx, args)

		if c.handleCallbackError(err, &firstErr) {
			return nil, err
		}

		if c.processBeforeModelResult(result, &ctx, &lastResult) {
			if c.continueOnError && firstErr != nil {
				return result, firstErr
			}
			return result, nil
		}
	}

	return c.finalizeBeforeModelResult(lastResult, firstErr)
}

// processAfterModelResult processes after model callback result and updates context.
// Returns whether to stop execution immediately.
func (c *Callbacks) processAfterModelResult(
	result *AfterModelResult,
	ctx *context.Context,
	lastResult **AfterModelResult,
) (shouldStop bool) {
	if result == nil {
		return false
	}
	if result.Context != nil {
		*ctx = result.Context
	}
	if result.CustomResponse != nil {
		*lastResult = result
		if !c.continueOnResponse {
			return true
		}
	} else {
		*lastResult = result
	}
	return false
}

// finalizeAfterModelResult determines the final return value for after model callbacks.
func (c *Callbacks) finalizeAfterModelResult(
	lastResult *AfterModelResult,
	firstErr error,
) (*AfterModelResult, error) {
	if lastResult != nil && lastResult.CustomResponse != nil {
		if c.continueOnError && firstErr != nil {
			return lastResult, firstErr
		}
		return lastResult, nil
	}
	if c.continueOnError && firstErr != nil {
		return lastResult, firstErr
	}
	if lastResult != nil && lastResult.Context == nil && lastResult.CustomResponse == nil {
		return nil, nil
	}
	return lastResult, nil
}

// RunAfterModel runs all after model callbacks in order.
// This method uses the new structured callback interface.
// If a callback returns a non-nil Context in the result, it will be used for subsequent callbacks.
func (c *Callbacks) RunAfterModel(ctx context.Context, args *AfterModelArgs) (*AfterModelResult, error) {
	var lastResult *AfterModelResult
	var firstErr error

	for _, cb := range c.AfterModel {
		result, err := cb(ctx, args)

		if c.handleCallbackError(err, &firstErr) {
			return nil, err
		}

		if c.processAfterModelResult(result, &ctx, &lastResult) {
			if c.continueOnError && firstErr != nil {
				return result, firstErr
			}
			return result, nil
		}
	}

	return c.finalizeAfterModelResult(lastResult, firstErr)
}
