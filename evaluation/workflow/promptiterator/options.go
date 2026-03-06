//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiterator

import (
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const defaultMaxOptimizationRounds = 3

type options struct {
	appName               string
	maxOptimizationRounds int
	candidateRunner       runner.Runner
	expectedRunner        runner.Runner
	judgeRunner           runner.Runner
	evalSetManager        evalset.Manager
	metricManager         metric.Manager
	registry              registry.Registry
	issueExtractor        issue.IssueExtractor
	aggregator            aggregator.Aggregator
	optimizer             optimizer.Optimizer
	runOptions            []agent.RunOption
}

// Option configures the prompt iteration workflow.
type Option func(*options)

func newOptions(opt ...Option) *options {
	opts := &options{
		evalSetManager:        evalsetinmemory.New(),
		metricManager:         metricinmemory.New(),
		registry:              registry.New(),
		maxOptimizationRounds: defaultMaxOptimizationRounds,
		issueExtractor:        issue.DefaultExtractor,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

func (o *options) validate(requireCandidateRunner bool) error {
	if strings.TrimSpace(o.appName) == "" {
		return errors.New("app name is empty")
	}
	if o.maxOptimizationRounds <= 0 {
		return fmt.Errorf("max optimization rounds must be greater than 0: %d", o.maxOptimizationRounds)
	}
	if requireCandidateRunner && o.candidateRunner == nil {
		return errors.New("candidate runner is nil")
	}
	if o.evalSetManager == nil {
		return errors.New("eval set manager is nil")
	}
	if o.metricManager == nil {
		return errors.New("metric manager is nil")
	}
	if o.registry == nil {
		return errors.New("registry is nil")
	}
	if o.issueExtractor == nil {
		return errors.New("issue extractor is nil")
	}
	if o.aggregator == nil {
		return errors.New("aggregator is nil")
	}
	if o.optimizer == nil {
		return errors.New("optimizer is nil")
	}
	return nil
}

// WithMaxOptimizationRounds sets the maximum number of optimization rounds.
func WithMaxOptimizationRounds(rounds int) Option {
	return func(o *options) {
		o.maxOptimizationRounds = rounds
	}
}

// WithExpectedRunner sets the runner used when eval cases enable expected runner execution.
func WithExpectedRunner(r runner.Runner) Option {
	return func(o *options) {
		o.expectedRunner = r
	}
}

// WithJudgeRunner sets the runner used for LLM judge evaluators.
func WithJudgeRunner(r runner.Runner) Option {
	return func(o *options) {
		o.judgeRunner = r
	}
}

// WithEvalSetManager sets the eval set manager.
func WithEvalSetManager(m evalset.Manager) Option {
	return func(o *options) {
		o.evalSetManager = m
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

// WithIssueExtractor sets the issue extraction function used by the workflow.
func WithIssueExtractor(extractor issue.IssueExtractor) Option {
	return func(o *options) {
		o.issueExtractor = extractor
	}
}

// WithAggregator sets the gradient aggregator implementation.
func WithAggregator(a aggregator.Aggregator) Option {
	return func(o *options) {
		o.aggregator = a
	}
}

// WithOptimizer sets the prompt optimizer implementation.
func WithOptimizer(opt optimizer.Optimizer) Option {
	return func(o *options) {
		o.optimizer = opt
	}
}

// WithRunOptions appends agent run options applied to every candidate inference.
func WithRunOptions(runOptions ...agent.RunOption) Option {
	return func(o *options) {
		o.runOptions = append(o.runOptions, runOptions...)
	}
}
