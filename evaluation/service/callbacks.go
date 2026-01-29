//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

// NamedCallback binds a callback function with a component name.
type NamedCallback[T any] struct {
	// Name is the component name for the callback.
	Name string
	// Callback is the callback function.
	Callback T
}

// BeforeInferenceSetCallback is called before an inference run starts for an eval set.
type BeforeInferenceSetCallback func(context.Context, *BeforeInferenceSetArgs) (*BeforeInferenceSetResult, error)

// AfterInferenceSetCallback is called after an inference run finishes for an eval set.
type AfterInferenceSetCallback func(context.Context, *AfterInferenceSetArgs) (*AfterInferenceSetResult, error)

// BeforeInferenceCaseCallback is called before an inference run starts for an eval case.
type BeforeInferenceCaseCallback func(context.Context, *BeforeInferenceCaseArgs) (*BeforeInferenceCaseResult, error)

// AfterInferenceCaseCallback is called after an inference run finishes for an eval case.
type AfterInferenceCaseCallback func(context.Context, *AfterInferenceCaseArgs) (*AfterInferenceCaseResult, error)

// BeforeEvaluateSetCallback is called before an evaluation run starts for an eval set.
type BeforeEvaluateSetCallback func(context.Context, *BeforeEvaluateSetArgs) (*BeforeEvaluateSetResult, error)

// AfterEvaluateSetCallback is called after an evaluation run finishes for an eval set.
type AfterEvaluateSetCallback func(context.Context, *AfterEvaluateSetArgs) (*AfterEvaluateSetResult, error)

// BeforeEvaluateCaseCallback is called before an evaluation run starts for an eval case.
type BeforeEvaluateCaseCallback func(context.Context, *BeforeEvaluateCaseArgs) (*BeforeEvaluateCaseResult, error)

// AfterEvaluateCaseCallback is called after an evaluation run finishes for an eval case.
type AfterEvaluateCaseCallback func(context.Context, *AfterEvaluateCaseArgs) (*AfterEvaluateCaseResult, error)

// Callback groups optional callbacks for evaluation points.
type Callback struct {
	// BeforeInferenceSet is called before an inference run starts for an eval set.
	BeforeInferenceSet BeforeInferenceSetCallback
	// AfterInferenceSet is called after an inference run finishes for an eval set.
	AfterInferenceSet AfterInferenceSetCallback
	// BeforeInferenceCase is called before an inference run starts for an eval case.
	BeforeInferenceCase BeforeInferenceCaseCallback
	// AfterInferenceCase is called after an inference run finishes for an eval case.
	AfterInferenceCase AfterInferenceCaseCallback
	// BeforeEvaluateSet is called before an evaluation run starts for an eval set.
	BeforeEvaluateSet BeforeEvaluateSetCallback
	// AfterEvaluateSet is called after an evaluation run finishes for an eval set.
	AfterEvaluateSet AfterEvaluateSetCallback
	// BeforeEvaluateCase is called before an evaluation run starts for an eval case.
	BeforeEvaluateCase BeforeEvaluateCaseCallback
	// AfterEvaluateCase is called after an evaluation run finishes for an eval case.
	AfterEvaluateCase AfterEvaluateCaseCallback
}

// Callbacks stores all registered callbacks for evaluation lifecycle points.
type Callbacks struct {
	// BeforeInferenceSet contains callbacks called before inference starts for an eval set.
	BeforeInferenceSet []NamedCallback[BeforeInferenceSetCallback]
	// AfterInferenceSet contains callbacks called after inference finishes for an eval set.
	AfterInferenceSet []NamedCallback[AfterInferenceSetCallback]
	// BeforeInferenceCase contains callbacks called before inference starts for an eval case.
	BeforeInferenceCase []NamedCallback[BeforeInferenceCaseCallback]
	// AfterInferenceCase contains callbacks called after inference finishes for an eval case.
	AfterInferenceCase []NamedCallback[AfterInferenceCaseCallback]
	// BeforeEvaluateSet contains callbacks called before evaluation starts for an eval set.
	BeforeEvaluateSet []NamedCallback[BeforeEvaluateSetCallback]
	// AfterEvaluateSet contains callbacks called after evaluation finishes for an eval set.
	AfterEvaluateSet []NamedCallback[AfterEvaluateSetCallback]
	// BeforeEvaluateCase contains callbacks called before evaluation starts for an eval case.
	BeforeEvaluateCase []NamedCallback[BeforeEvaluateCaseCallback]
	// AfterEvaluateCase contains callbacks called after evaluation finishes for an eval case.
	AfterEvaluateCase []NamedCallback[AfterEvaluateCaseCallback]
}

// CallbacksOption configures Callbacks behavior.
type CallbacksOption func(*Callbacks)

// NewCallbacks creates a new Callbacks instance for evaluation callbacks.
func NewCallbacks(opts ...CallbacksOption) *Callbacks {
	c := &Callbacks{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Register adds a callback component with the provided name.
func (c *Callbacks) Register(name string, callback *Callback) *Callbacks {
	if callback == nil {
		return c
	}
	if callback.BeforeInferenceSet != nil {
		c.BeforeInferenceSet = append(c.BeforeInferenceSet, NamedCallback[BeforeInferenceSetCallback]{Name: name, Callback: callback.BeforeInferenceSet})
	}
	if callback.AfterInferenceSet != nil {
		c.AfterInferenceSet = append(c.AfterInferenceSet, NamedCallback[AfterInferenceSetCallback]{Name: name, Callback: callback.AfterInferenceSet})
	}
	if callback.BeforeInferenceCase != nil {
		c.BeforeInferenceCase = append(c.BeforeInferenceCase, NamedCallback[BeforeInferenceCaseCallback]{Name: name, Callback: callback.BeforeInferenceCase})
	}
	if callback.AfterInferenceCase != nil {
		c.AfterInferenceCase = append(c.AfterInferenceCase, NamedCallback[AfterInferenceCaseCallback]{Name: name, Callback: callback.AfterInferenceCase})
	}
	if callback.BeforeEvaluateSet != nil {
		c.BeforeEvaluateSet = append(c.BeforeEvaluateSet, NamedCallback[BeforeEvaluateSetCallback]{Name: name, Callback: callback.BeforeEvaluateSet})
	}
	if callback.AfterEvaluateSet != nil {
		c.AfterEvaluateSet = append(c.AfterEvaluateSet, NamedCallback[AfterEvaluateSetCallback]{Name: name, Callback: callback.AfterEvaluateSet})
	}
	if callback.BeforeEvaluateCase != nil {
		c.BeforeEvaluateCase = append(c.BeforeEvaluateCase, NamedCallback[BeforeEvaluateCaseCallback]{Name: name, Callback: callback.BeforeEvaluateCase})
	}
	if callback.AfterEvaluateCase != nil {
		c.AfterEvaluateCase = append(c.AfterEvaluateCase, NamedCallback[AfterEvaluateCaseCallback]{Name: name, Callback: callback.AfterEvaluateCase})
	}
	return c
}

// RegisterBeforeInferenceSet registers a before inference set callback with the provided name.
func (c *Callbacks) RegisterBeforeInferenceSet(name string, fn BeforeInferenceSetCallback) *Callbacks {
	return c.Register(name, &Callback{BeforeInferenceSet: fn})
}

// RegisterAfterInferenceSet registers an after inference set callback with the provided name.
func (c *Callbacks) RegisterAfterInferenceSet(name string, fn AfterInferenceSetCallback) *Callbacks {
	return c.Register(name, &Callback{AfterInferenceSet: fn})
}

// RegisterBeforeInferenceCase registers a before inference case callback with the provided name.
func (c *Callbacks) RegisterBeforeInferenceCase(name string, fn BeforeInferenceCaseCallback) *Callbacks {
	return c.Register(name, &Callback{BeforeInferenceCase: fn})
}

// RegisterAfterInferenceCase registers an after inference case callback with the provided name.
func (c *Callbacks) RegisterAfterInferenceCase(name string, fn AfterInferenceCaseCallback) *Callbacks {
	return c.Register(name, &Callback{AfterInferenceCase: fn})
}

// RegisterBeforeEvaluateSet registers a before evaluate set callback with the provided name.
func (c *Callbacks) RegisterBeforeEvaluateSet(name string, fn BeforeEvaluateSetCallback) *Callbacks {
	return c.Register(name, &Callback{BeforeEvaluateSet: fn})
}

// RegisterAfterEvaluateSet registers an after evaluate set callback with the provided name.
func (c *Callbacks) RegisterAfterEvaluateSet(name string, fn AfterEvaluateSetCallback) *Callbacks {
	return c.Register(name, &Callback{AfterEvaluateSet: fn})
}

// RegisterBeforeEvaluateCase registers a before evaluate case callback with the provided name.
func (c *Callbacks) RegisterBeforeEvaluateCase(name string, fn BeforeEvaluateCaseCallback) *Callbacks {
	return c.Register(name, &Callback{BeforeEvaluateCase: fn})
}

// RegisterAfterEvaluateCase registers an after evaluate case callback with the provided name.
func (c *Callbacks) RegisterAfterEvaluateCase(name string, fn AfterEvaluateCaseCallback) *Callbacks {
	return c.Register(name, &Callback{AfterEvaluateCase: fn})
}

// BeforeInferenceSetArgs contains parameters for before inference set callbacks.
type BeforeInferenceSetArgs struct {
	// Request is the request about to be used for inference and can be modified.
	Request *InferenceRequest
}

// BeforeInferenceSetResult contains the return value for before inference set callbacks.
type BeforeInferenceSetResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}

// AfterInferenceSetArgs contains parameters for after inference set callbacks.
type AfterInferenceSetArgs struct {
	// Request is the final request used for this inference stage.
	Request *InferenceRequest
	// Results contains inference results and may be partial or nil on error.
	Results []*InferenceResult
	// Error is the error occurred during inference and may be nil.
	Error error
	// StartTime records when the inference set stage started.
	StartTime time.Time
}

// AfterInferenceSetResult contains the return value for after inference set callbacks.
type AfterInferenceSetResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}

// BeforeInferenceCaseArgs contains parameters for before inference case callbacks.
type BeforeInferenceCaseArgs struct {
	Request    *InferenceRequest
	EvalCaseID string
	SessionID  string
}

// BeforeInferenceCaseResult contains the return value for before inference case callbacks.
type BeforeInferenceCaseResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}

// AfterInferenceCaseArgs contains parameters for after inference case callbacks.
type AfterInferenceCaseArgs struct {
	// Request is the final request used for this inference stage.
	Request *InferenceRequest
	// Result contains the inference result for the case.
	Result *InferenceResult
	// Error is the error occurred during inference and may be nil.
	Error error
	// StartTime records when the inference case stage started.
	StartTime time.Time
}

// AfterInferenceCaseResult contains the return value for after inference case callbacks.
type AfterInferenceCaseResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}

// BeforeEvaluateSetArgs contains parameters for before evaluation set callbacks.
type BeforeEvaluateSetArgs struct {
	// Request is the request about to be used for evaluation and can be modified.
	Request *EvaluateRequest
}

// BeforeEvaluateSetResult contains the return value for before evaluation set callbacks.
type BeforeEvaluateSetResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}

// AfterEvaluateSetArgs contains parameters for after evaluation set callbacks.
type AfterEvaluateSetArgs struct {
	// Request is the final request used for this evaluation stage.
	Request *EvaluateRequest
	// Result contains the eval set run result and may be nil on error.
	Result *EvalSetRunResult
	// Error is the error occurred during evaluation and may be nil.
	Error error
	// StartTime records when the evaluate set stage started.
	StartTime time.Time
}

// AfterEvaluateSetResult contains the return value for after evaluation set callbacks.
type AfterEvaluateSetResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}

// BeforeEvaluateCaseArgs contains parameters for before evaluation case callbacks.
type BeforeEvaluateCaseArgs struct {
	Request    *EvaluateRequest
	EvalCaseID string
}

// BeforeEvaluateCaseResult contains the return value for before evaluation case callbacks.
type BeforeEvaluateCaseResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}

// AfterEvaluateCaseArgs contains parameters for after evaluation case callbacks.
type AfterEvaluateCaseArgs struct {
	// Request is the final request used for this evaluation stage.
	Request *EvaluateRequest
	// InferenceResult is the inference output for the eval case.
	InferenceResult *InferenceResult
	// Result contains the eval case result.
	Result *evalresult.EvalCaseResult
	// Error is the error occurred during evaluation and may be nil.
	Error error
	// StartTime records when the evaluate case stage started.
	StartTime time.Time
}

// AfterEvaluateCaseResult contains the return value for after evaluation case callbacks.
type AfterEvaluateCaseResult struct {
	// Context if not nil will be used by the framework for subsequent operations.
	Context context.Context
}
