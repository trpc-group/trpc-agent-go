//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package bestofn provides evaluation-backed runner options for best-of-N candidate selection.
package bestofn

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evaluatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const defaultAttempts = 2

// SelectionMode controls how best-of-N chooses the winning candidate.
type SelectionMode string

const (
	// SelectionModePointwise scores each candidate independently and selects the best score.
	SelectionModePointwise SelectionMode = "pointwise"
	// SelectionModePairwise selects the candidate with the most pairwise wins.
	// Evaluators should score above 0.5 when the actual candidate is preferred.
	SelectionModePairwise SelectionMode = "pairwise"
)

// Option configures the best-of-N runner option.
type Option func(*options)

type options struct {
	attempts                int
	metrics                 []*metric.EvalMetric
	selectionMode           SelectionMode
	contextMessages         []*model.Message
	evalSetManager          evalset.Manager
	judgeRunner             runner.Runner
	judgeRunnerNumSamples   *int
	parallelAttempts        bool
	attemptParallelism      int
	registry                evaluatorregistry.Registry
	metricRegistry          metricregistry.Registry
	requirePassingCandidate bool
}

// NewRunnerOption creates an evaluation-backed best-of-N runner option.
// Selection is bypassed for runs whose effects cannot be safely replayed,
// including execution traces and graph checkpoint resumes.
func NewRunnerOption(opts ...Option) (runner.Option, error) {
	o := newOptions(opts...)
	if err := o.validate(); err != nil {
		return nil, err
	}
	selector := newEvaluationSelector(o)
	return runner.WithCandidateSelector(
		selector,
		runner.WithCandidateAttempts(o.attempts),
		runner.WithCandidateAttemptParallelEnabled(o.parallelAttempts),
		runner.WithCandidateAttemptParallelism(o.attemptParallelism),
	), nil
}

// WithAttempts sets how many candidate attempts are generated per runner turn.
// Values less than one are invalid. A value of one runs a single candidate attempt.
func WithAttempts(attempts int) Option {
	return func(o *options) {
		o.attempts = attempts
	}
}

// WithAttemptParallelEnabled enables parallel best-of-N candidate attempts.
func WithAttemptParallelEnabled(enabled bool) Option {
	return func(o *options) {
		o.parallelAttempts = enabled
	}
}

// WithAttemptParallelism sets the maximum parallel best-of-N candidate attempts.
// Values less than or equal to zero use runtime.GOMAXPROCS when parallel attempts are enabled.
func WithAttemptParallelism(parallelism int) Option {
	return func(o *options) {
		o.attemptParallelism = parallelism
	}
}

// WithEvalMetrics sets the evaluation metrics used to score candidates.
func WithEvalMetrics(metrics ...*metric.EvalMetric) Option {
	return func(o *options) {
		o.metrics = append(o.metrics, metrics...)
	}
}

// WithSelectionMode sets how candidate evaluation results are aggregated.
func WithSelectionMode(mode SelectionMode) Option {
	return func(o *options) {
		o.selectionMode = mode
	}
}

// WithContextMessages sets context messages attached to each candidate eval case.
func WithContextMessages(messages ...*model.Message) Option {
	return func(o *options) {
		o.contextMessages = append(o.contextMessages, messages...)
	}
}

// WithEvalSetManager sets the eval set manager used during candidate evaluation.
// Passing nil keeps the default in-memory manager.
func WithEvalSetManager(manager evalset.Manager) Option {
	return func(o *options) {
		o.evalSetManager = manager
	}
}

// WithJudgeRunner sets the runner used by LLM-backed evaluation metrics.
func WithJudgeRunner(r runner.Runner) Option {
	return func(o *options) {
		o.judgeRunner = r
	}
}

// WithJudgeRunnerNumSamples sets how many judge samples are collected.
func WithJudgeRunnerNumSamples(n int) Option {
	return func(o *options) {
		o.judgeRunnerNumSamples = &n
	}
}

// WithRegistry sets the evaluator registry used by the evaluation backend.
func WithRegistry(r evaluatorregistry.Registry) Option {
	return func(o *options) {
		o.registry = r
	}
}

// WithMetricRegistry sets the metric extension registry used by the evaluation backend.
func WithMetricRegistry(r metricregistry.Registry) Option {
	return func(o *options) {
		o.metricRegistry = r
	}
}

// WithRequirePassingCandidate requires the selected winner to pass evaluation.
func WithRequirePassingCandidate(enabled bool) Option {
	return func(o *options) {
		o.requirePassingCandidate = enabled
	}
}

func newOptions(opts ...Option) *options {
	o := &options{
		attempts:       defaultAttempts,
		selectionMode:  SelectionModePointwise,
		registry:       evaluatorregistry.New(),
		metricRegistry: metricregistry.New(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

func (o *options) validate() error {
	if o.attempts < 1 {
		return errors.New("bestofn: attempts must be greater than 0")
	}
	if len(o.metrics) == 0 {
		return errors.New("bestofn: eval metrics are empty")
	}
	switch o.selectionMode {
	case SelectionModePointwise, SelectionModePairwise:
	default:
		return fmt.Errorf("bestofn: unsupported selection mode %q", o.selectionMode)
	}
	if o.selectionMode == SelectionModePairwise && o.requirePassingCandidate {
		return errors.New("bestofn: require passing candidate is not supported in pairwise selection mode")
	}
	if o.registry == nil {
		return errors.New("bestofn: registry is nil")
	}
	if o.metricRegistry == nil {
		return errors.New("bestofn: metric registry is nil")
	}
	if o.judgeRunnerNumSamples != nil && *o.judgeRunnerNumSamples <= 0 {
		return errors.New("bestofn: judge runner num samples must be greater than 0")
	}
	if o.judgeRunnerNumSamples != nil && o.judgeRunner == nil {
		return errors.New("bestofn: judge runner is required when judge runner num samples is set")
	}
	if o.judgeRunnerNumSamples != nil && !hasLLMJudgeMetric(o.metrics) {
		return errors.New("bestofn: LLM judge metric is required when judge runner num samples is set")
	}
	return nil
}

func hasLLMJudgeMetric(metrics []*metric.EvalMetric) bool {
	for _, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.Criterion != nil && evalMetric.Criterion.LLMJudge != nil {
			return true
		}
	}
	return false
}
