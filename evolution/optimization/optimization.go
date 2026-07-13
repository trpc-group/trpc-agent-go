//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package optimization evolves managed skill specifications with reflective
// mutations and instance-level Pareto candidate selection.
package optimization

import (
	"context"
	"errors"
	"math"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	defaultMaxIterations   = 10
	defaultMaxMetricCalls  = 1000
	defaultReflectionBatch = 3
	minimumPromotionCases  = 10
)

// Case is one immutable evaluation example. Evaluators may interpret Input,
// Expected, and Metadata according to their task domain.
type Case struct {
	ID       string            `json:"id"`
	Input    string            `json:"input"`
	Expected string            `json:"expected,omitempty"`
	Critical bool              `json:"critical,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Evaluation is an evaluator's result for one Case. Score must be finite and
// normalized to [0, 1]. Feedback and Trace are consumed by the reflection
// model; callers should redact secrets before returning them. Score is the
// scalar metric used for search. Objectives are reported for analysis but do
// not participate in candidate selection.
type Evaluation struct {
	CaseID     string             `json:"case_id"`
	Score      float64            `json:"score"`
	Output     string             `json:"output,omitempty"`
	Feedback   string             `json:"feedback,omitempty"`
	Trace      string             `json:"trace,omitempty"`
	Objectives map[string]float64 `json:"objectives,omitempty"`
}

// Evaluator executes a candidate against a batch of cases. It must return
// exactly one Evaluation for every input case. seed is stable for paired
// parent/child comparisons, but determinism depends on the implementation
// honoring it.
type Evaluator interface {
	Evaluate(
		ctx context.Context,
		candidate *evolution.SkillSpec,
		cases []Case,
		seed int64,
	) ([]Evaluation, error)
}

// Dataset separates information used for reflection, candidate selection,
// and final verification. Case IDs must be unique across all three splits.
type Dataset struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Feedback   []Case `json:"feedback"`
	Validation []Case `json:"validation"`
	Holdout    []Case `json:"holdout,omitempty"`
}

// Request starts one isolated optimization experiment. Submit asks the
// optimizer to send a successful holdout result to the configured evolution
// service; submitted revisions are held for human approval. Submission
// requires at least 10 cases in each dataset split.
type Request struct {
	Seed             *evolution.SkillSpec
	Dataset          Dataset
	Scope            skill.SkillScope
	ParentRevisionID string
	Submit           bool
}

// Summary is an aggregate view of one dataset evaluation.
type Summary struct {
	Score      float64            `json:"score"`
	Cases      int                `json:"cases"`
	Objectives map[string]float64 `json:"objectives,omitempty"`
}

// Result contains the selected candidate and the evidence needed to decide
// whether it should be promoted. Internal candidates and Pareto bookkeeping
// intentionally remain private.
type Result struct {
	ExperimentID        string               `json:"experiment_id"`
	Spec                *evolution.SkillSpec `json:"spec"`
	BaselineValidation  Summary              `json:"baseline_validation"`
	CandidateValidation Summary              `json:"candidate_validation"`
	BaselineHoldout     Summary              `json:"baseline_holdout"`
	CandidateHoldout    Summary              `json:"candidate_holdout"`
	CandidateCount      int                  `json:"candidate_count"`
	MetricCalls         int                  `json:"metric_calls"`
	StopReason          string               `json:"stop_reason"`
	SubmissionReason    string               `json:"submission_reason,omitempty"`
	Revision            *evolution.Revision  `json:"revision,omitempty"`
}

// Option configures an Optimizer.
type Option func(*options)

type options struct {
	maxIterations             int
	maxMetricCalls            int
	reflectionBatchSize       int
	randomSeed                int64
	timeLimit                 time.Duration
	storeDir                  string
	evolutionService          evolution.Service
	minimumHoldoutImprovement float64
}

func defaultOptions() options {
	return options{
		maxIterations:       defaultMaxIterations,
		maxMetricCalls:      defaultMaxMetricCalls,
		reflectionBatchSize: defaultReflectionBatch,
		randomSeed:          1,
	}
}

// WithMaxIterations limits reflective mutation attempts.
func WithMaxIterations(n int) Option {
	return func(o *options) { o.maxIterations = n }
}

// WithMaxMetricCalls limits the total number of evaluated cases, including
// validation and holdout comparisons. It does not count model requests or
// tokens. A non-positive value disables the cap.
func WithMaxMetricCalls(n int) Option {
	return func(o *options) { o.maxMetricCalls = n }
}

// WithReflectionBatchSize sets the feedback minibatch size.
func WithReflectionBatchSize(n int) Option {
	return func(o *options) { o.reflectionBatchSize = n }
}

// WithRandomSeed makes optimizer sampling reproducible and passes stable seeds
// to paired evaluator calls. Evaluators must use the seed for deterministic
// execution when their task environment supports it.
func WithRandomSeed(seed int64) Option {
	return func(o *options) { o.randomSeed = seed }
}

// WithTimeLimit bounds a complete optimization run. A non-positive duration
// leaves deadline control to the caller's context.
func WithTimeLimit(limit time.Duration) Option {
	return func(o *options) { o.timeLimit = limit }
}

// WithStoreDir enables a filesystem experiment record under dir.
func WithStoreDir(dir string) Option {
	return func(o *options) { o.storeDir = dir }
}

// WithEvolutionService enables optional submission into the existing
// revision, gate, approval, publish, and rollback lifecycle.
func WithEvolutionService(svc evolution.Service) Option {
	return func(o *options) { o.evolutionService = svc }
}

// WithMinimumHoldoutImprovement sets the required absolute paired holdout
// score delta before submission. It does not affect search or final candidate
// selection. The default is zero (no regression).
func WithMinimumHoldoutImprovement(delta float64) Option {
	return func(o *options) { o.minimumHoldoutImprovement = delta }
}

// Optimizer is a pure-Go reflective skill optimizer. Its configuration is
// immutable after construction, and per-run search state is isolated.
type Optimizer struct {
	reflector reflector
	evaluator Evaluator
	opts      options
}

// New creates an Optimizer backed by reflectionModel and evaluator.
func New(
	reflectionModel model.Model,
	evaluator Evaluator,
	opts ...Option,
) (*Optimizer, error) {
	if reflectionModel == nil {
		return nil, errors.New("evolution optimization: nil reflection model")
	}
	if evaluator == nil {
		return nil, errors.New("evolution optimization: nil evaluator")
	}
	o := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.maxIterations < 0 {
		return nil, errors.New("evolution optimization: max iterations must not be negative")
	}
	if o.reflectionBatchSize <= 0 {
		return nil, errors.New("evolution optimization: reflection batch size must be positive")
	}
	if math.IsNaN(o.minimumHoldoutImprovement) ||
		o.minimumHoldoutImprovement < 0 || o.minimumHoldoutImprovement > 1 {
		return nil, errors.New("evolution optimization: holdout improvement must be between 0 and 1")
	}
	return &Optimizer{
		reflector: newLLMReflector(reflectionModel),
		evaluator: evaluator,
		opts:      o,
	}, nil
}
