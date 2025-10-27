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

	"trpc.group/trpc-go/trpc-agent-go/callback"
)

// BeforeModelCallback is called before the model is invoked. It can mutate the request.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be returned to user and model call will be skipped.
// - error: if not nil, model call will be stopped with this error.
type BeforeModelCallback func(ctx context.Context, req *Request) (*Response, error)

// AfterModelCallback is called after the model is invoked.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be used instead of the actual model response.
// - error: if not nil, this error will be returned.
type AfterModelCallback func(ctx context.Context, req *Request, rsp *Response, modelErr error) (*Response, error)

// Callbacks holds callbacks for model operations.
type Callbacks struct {
	// BeforeModel is a list of callbacks that are called before the model is invoked.
	BeforeModel []BeforeModelCallback
	// AfterModel is a list of callbacks that are called after the model is invoked.
	AfterModel []AfterModelCallback
}

// NewCallbacks creates a new Callbacks instance for model.
func NewCallbacks() *Callbacks {
	return &Callbacks{}
}

// RegisterBeforeModel registers a before model callback.
func (c *Callbacks) RegisterBeforeModel(cb BeforeModelCallback) *Callbacks {
	c.BeforeModel = append(c.BeforeModel, cb)
	return c
}

// RegisterAfterModel registers an after model callback.
func (c *Callbacks) RegisterAfterModel(cb AfterModelCallback) *Callbacks {
	c.AfterModel = append(c.AfterModel, cb)
	return c
}

// RunBeforeModel runs all before model callbacks in order.
// Returns (customResponse, error).
// If any callback returns a custom response, stop and return.
func (c *Callbacks) RunBeforeModel(ctx context.Context, req *Request) (*Response, error) {
	for _, cb := range c.BeforeModel {
		customResponse, err := cb(ctx, req)
		if err != nil {
			return nil, err
		}
		if customResponse != nil {
			return customResponse, nil
		}
	}
	return nil, nil
}

// RunAfterModel runs all after model callbacks in order.
// Returns (customResponse, error).
// If any callback returns a custom response, stop and return.
func (c *Callbacks) RunAfterModel(
	ctx context.Context, req *Request, rsp *Response, modelErr error,
) (*Response, error) {
	for _, cb := range c.AfterModel {
		customResponse, err := cb(ctx, req, rsp, modelErr)
		if err != nil {
			return nil, err
		}
		if customResponse != nil {
			return customResponse, nil
		}
	}
	return nil, nil
}

// contextKey is the type for model callback context key.
type contextKey struct{}

// CallbackMessage returns the callback message from context.
// Returns nil if not found.
func CallbackMessage(ctx context.Context) callback.Message {
	if msg, ok := ctx.Value(contextKey{}).(callback.Message); ok {
		return msg
	}
	return nil
}

// WithCallbackMessage injects a callback message into context.
// This should be called before running model callbacks.
func WithCallbackMessage(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, callback.NewMessage())
}
