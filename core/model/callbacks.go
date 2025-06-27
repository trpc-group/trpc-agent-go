// Package model provides interfaces for working with LLMs.
package model

import (
	"context"
)

// BeforeModelCallback is called before the model is invoked. It can mutate the request.
// Returns (customResponse, skip, error).
// - customResponse: if not nil, this response will be returned to user and model call will be skipped.
// - skip: if true, model call will be skipped.
// - error: if not nil, model call will be stopped with this error.
type BeforeModelCallback func(ctx context.Context, req *Request) (*Response, bool, error)

// AfterModelCallback is called after the model is invoked.
// Returns (customResponse, override, error).
// - customResponse: if not nil and override is true, this response will be used instead of the actual model response.
// - override: if true, the customResponse will be used.
// - error: if not nil, this error will be returned.
type AfterModelCallback func(ctx context.Context, rsp *Response, modelErr error) (*Response, bool, error)

// ModelCallbacks holds callbacks for model operations.
type ModelCallbacks struct {
	BeforeModel []BeforeModelCallback
	AfterModel  []AfterModelCallback
}

// NewModelCallbacks creates a new ModelCallbacks instance.
func NewModelCallbacks() *ModelCallbacks {
	return &ModelCallbacks{}
}

// AddBeforeModel adds a before model callback.
func (c *ModelCallbacks) AddBeforeModel(cb BeforeModelCallback) {
	c.BeforeModel = append(c.BeforeModel, cb)
}

// AddAfterModel adds an after model callback.
func (c *ModelCallbacks) AddAfterModel(cb AfterModelCallback) {
	c.AfterModel = append(c.AfterModel, cb)
}

// RunBeforeModel runs all before model callbacks in order.
// Returns (customResponse, skip, error).
// If any callback returns a custom response or skip=true, stop and return.
func (c *ModelCallbacks) RunBeforeModel(ctx context.Context, req *Request) (*Response, bool, error) {
	for _, cb := range c.BeforeModel {
		customResponse, skip, err := cb(ctx, req)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil || skip {
			return customResponse, skip, nil
		}
	}
	return nil, false, nil
}

// RunAfterModel runs all after model callbacks in order.
// Returns (customResponse, override, error).
// If any callback returns a custom response with override=true, stop and return.
func (c *ModelCallbacks) RunAfterModel(ctx context.Context, rsp *Response, modelErr error) (*Response, bool, error) {
	for _, cb := range c.AfterModel {
		customResponse, override, err := cb(ctx, rsp, modelErr)
		if err != nil {
			return nil, false, err
		}
		if customResponse != nil && override {
			return customResponse, true, nil
		}
	}
	return nil, false, nil
}
