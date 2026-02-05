//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluation

import (
	"runtime"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

// defaultNumRuns is the default number of runs.
const defaultNumRuns = 1

// options holds the configuration options for the evaluation.
type options struct {
	evalSetManager                    evalset.Manager
	evalResultManager                 evalresult.Manager
	metricManager                     metric.Manager
	registry                          registry.Registry
	evalService                       service.Service
	callbacks                         *service.Callbacks
	numRuns                           int
	evalCaseParallelism               int
	evalCaseParallelInferenceEnabled  bool
	evalCaseParallelEvaluationEnabled bool
}

// newOptions creates a new options with the default values.
func newOptions(opt ...Option) *options {
	// Initialize options with default values.
	opts := &options{
		numRuns:                           defaultNumRuns,
		evalSetManager:                    evalsetinmemory.New(),
		evalResultManager:                 evalresultinmemory.New(),
		metricManager:                     metricinmemory.New(),
		registry:                          registry.New(),
		evalCaseParallelism:               runtime.GOMAXPROCS(0),
		evalCaseParallelInferenceEnabled:  false,
		evalCaseParallelEvaluationEnabled: false,
	}
	// Apply user options.
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option defines a function type for configuring the evaluation.
type Option func(*options)

// WithEvalSetManager sets the eval set manager.
func WithEvalSetManager(m evalset.Manager) Option {
	return func(o *options) {
		o.evalSetManager = m
	}
}

// WithEvalResultManager sets the eval result manager.
func WithEvalResultManager(m evalresult.Manager) Option {
	return func(o *options) {
		o.evalResultManager = m
	}
}

// WithMetricManager sets the metric manager.
func WithMetricManager(m metric.Manager) Option {
	return func(o *options) {
		o.metricManager = m
	}
}

// WithRegistry sets the evaluator registry.
func WithRegistry(r registry.Registry) Option {
	return func(o *options) {
		o.registry = r
	}
}

// WithEvaluationService sets the evaluation service.
func WithEvaluationService(s service.Service) Option {
	return func(o *options) {
		o.evalService = s
	}
}

// WithCallbacks sets evaluation callbacks for evaluation service.
func WithCallbacks(c *service.Callbacks) Option {
	return func(o *options) {
		o.callbacks = c
	}
}

// WithNumRuns sets the number of runs.
func WithNumRuns(numRuns int) Option {
	return func(o *options) {
		o.numRuns = numRuns
	}
}

// WithEvalCaseParallelism sets the maximum number of eval cases processed in parallel.
func WithEvalCaseParallelism(parallelism int) Option {
	return func(o *options) {
		o.evalCaseParallelism = parallelism
	}
}

// WithEvalCaseParallelInferenceEnabled enables or disables parallel inference across eval cases.
func WithEvalCaseParallelInferenceEnabled(enabled bool) Option {
	return func(o *options) {
		o.evalCaseParallelInferenceEnabled = enabled
	}
}

// WithEvalCaseParallelEvaluationEnabled enables or disables parallel evaluation across eval cases.
func WithEvalCaseParallelEvaluationEnabled(enabled bool) Option {
	return func(o *options) {
		o.evalCaseParallelEvaluationEnabled = enabled
	}
}
