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
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// defaultNumRuns is the default number of runs.
const defaultNumRuns = 1

// options holds the configuration options for the evaluation.
type options struct {
	evalSetManager                    evalset.Manager
	evalResultManager                 evalresult.Manager
	metricManager                     metric.Manager
	registry                          registry.Registry
	metricRegistry                    metricregistry.Registry
	evalService                       service.Service
	expectedRunner                    runner.Runner
	userSimulator                     usersimulation.Simulator
	callbacks                         *service.Callbacks
	judgeRunner                       runner.Runner
	numRuns                           int
	evalCaseIDs                       []string
	numRunsParallelEnabled            *bool
	evalCaseParallelism               *int
	evalCaseParallelInferenceEnabled  *bool
	evalCaseParallelEvaluationEnabled *bool
	runDetailsEnabled                 bool
	runDetailsCollector               *runDetailsCollector
	runOptions                        []agent.RunOption
}

// newOptions creates a new options with the default values.
func newOptions(opt ...Option) *options {
	// Initialize options with default values.
	opts := &options{
		numRuns:           defaultNumRuns,
		evalSetManager:    evalsetinmemory.New(),
		evalResultManager: evalresultinmemory.New(),
		metricManager:     metricinmemory.New(),
		registry:          registry.New(),
		metricRegistry:    metricregistry.New(),
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

// WithMetricRegistry sets the metric runtime registry.
func WithMetricRegistry(r metricregistry.Registry) Option {
	return func(o *options) {
		o.metricRegistry = r
	}
}

// WithEvaluationService sets the evaluation service.
func WithEvaluationService(s service.Service) Option {
	return func(o *options) {
		o.evalService = s
	}
}

// WithUserSimulator sets the simulator used for conversation scenarios.
func WithUserSimulator(sim usersimulation.Simulator) Option {
	return func(o *options) {
		o.userSimulator = sim
	}
}

// WithCallbacks sets evaluation callbacks for evaluation service.
func WithCallbacks(c *service.Callbacks) Option {
	return func(o *options) {
		o.callbacks = c
	}
}

// WithJudgeRunner injects a judge runner for all LLM judge evaluators.
func WithJudgeRunner(judge runner.Runner) Option {
	return func(o *options) {
		o.judgeRunner = judge
	}
}

// WithExpectedRunner sets the runner used to generate dynamic expected outputs.
func WithExpectedRunner(r runner.Runner) Option {
	return func(o *options) {
		o.expectedRunner = r
	}
}

// WithNumRuns sets the number of runs.
func WithNumRuns(numRuns int) Option {
	return func(o *options) {
		o.numRuns = numRuns
	}
}

// WithEvalCaseIDs limits evaluation to the specified eval case IDs.
func WithEvalCaseIDs(evalCaseIDs ...string) Option {
	return func(o *options) {
		o.evalCaseIDs = append([]string(nil), evalCaseIDs...)
	}
}

// WithNumRunsParallelEnabled enables or disables parallel execution across evaluation runs.
func WithNumRunsParallelEnabled(enabled bool) Option {
	return func(o *options) {
		o.numRunsParallelEnabled = &enabled
	}
}

// WithEvalCaseParallelism sets the maximum number of eval cases processed in parallel.
func WithEvalCaseParallelism(parallelism int) Option {
	return func(o *options) {
		o.evalCaseParallelism = &parallelism
	}
}

// WithEvalCaseParallelInferenceEnabled enables or disables parallel inference across eval cases.
func WithEvalCaseParallelInferenceEnabled(enabled bool) Option {
	return func(o *options) {
		o.evalCaseParallelInferenceEnabled = &enabled
	}
}

// WithEvalCaseParallelEvaluationEnabled enables or disables parallel evaluation across eval cases.
func WithEvalCaseParallelEvaluationEnabled(enabled bool) Option {
	return func(o *options) {
		o.evalCaseParallelEvaluationEnabled = &enabled
	}
}

// WithRunDetailsEnabled enables or disables per-run inference details in evaluation results.
func WithRunDetailsEnabled(enabled bool) Option {
	return func(o *options) {
		o.runDetailsEnabled = enabled
	}
}

// WithRunOptions appends agent.RunOption values that will be applied to every runner.Run call during inference.
func WithRunOptions(opt ...agent.RunOption) Option {
	return func(o *options) {
		o.runOptions = append(o.runOptions, opt...)
	}
}

func (o *options) validate(requireEvalService bool) error {
	if o == nil {
		return errors.New("options is nil")
	}
	if o.numRuns <= 0 {
		return errors.New("num runs must be greater than 0")
	}
	parallelInferenceEnabled := o.evalCaseParallelInferenceEnabled != nil && *o.evalCaseParallelInferenceEnabled
	parallelEvaluationEnabled := o.evalCaseParallelEvaluationEnabled != nil && *o.evalCaseParallelEvaluationEnabled
	if (parallelInferenceEnabled || parallelEvaluationEnabled) && o.evalCaseParallelism != nil && *o.evalCaseParallelism <= 0 {
		return errors.New("eval case parallelism must be greater than 0")
	}
	if o.evalSetManager == nil {
		return errors.New("eval set manager is nil")
	}
	if o.metricManager == nil {
		return errors.New("metric manager is nil")
	}
	if o.evalResultManager == nil {
		return errors.New("eval result manager is nil")
	}
	if o.registry == nil {
		return errors.New("registry is nil")
	}
	if o.metricRegistry == nil {
		return errors.New("metric registry is nil")
	}
	if requireEvalService && o.evalService == nil {
		return errors.New("eval service is nil")
	}
	return nil
}
