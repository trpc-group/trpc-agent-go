//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
type BeforeModelCallback func(ctx context.Context, req *Request) (*Response, error)

// AfterModelCallback is called after the model is invoked.
// Returns (customResponse, error).
// - customResponse: if not nil, this response will be used instead of the actual model response.
// - error: if not nil, this error will be returned.
type AfterModelCallback func(ctx context.Context, rsp *Response, modelErr error) (*Response, error)

// ModelCallbacks holds callbacks for model operations.
type ModelCallbacks struct {
	BeforeModel []BeforeModelCallback
	AfterModel  []AfterModelCallback
}

// NewModelCallbacks creates a new ModelCallbacks instance.
func NewModelCallbacks() *ModelCallbacks {
	return &ModelCallbacks{}
}

// RegisterBeforeModel registers a before model callback.
func (c *ModelCallbacks) RegisterBeforeModel(cb BeforeModelCallback) {
	c.BeforeModel = append(c.BeforeModel, cb)
}

// RegisterAfterModel registers an after model callback.
func (c *ModelCallbacks) RegisterAfterModel(cb AfterModelCallback) {
	c.AfterModel = append(c.AfterModel, cb)
}

// RunBeforeModel runs all before model callbacks in order.
// Returns (customResponse, error).
// If any callback returns a custom response, stop and return.
func (c *ModelCallbacks) RunBeforeModel(ctx context.Context, req *Request) (*Response, error) {
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
func (c *ModelCallbacks) RunAfterModel(ctx context.Context, rsp *Response, modelErr error) (*Response, error) {
	for _, cb := range c.AfterModel {
		customResponse, err := cb(ctx, rsp, modelErr)
		if err != nil {
			return nil, err
		}
		if customResponse != nil {
			return customResponse, nil
		}
	}
	return nil, nil
}
