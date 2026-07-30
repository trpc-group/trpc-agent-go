//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package optimization defines offline skill optimization contracts and a
// built-in pure-Go GEPA implementation.
package optimization

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	defaultMaxIterations   = 10
	defaultMaxMetricCalls  = 1000
	defaultReflectionBatch = 3
	minimumPromotionCases  = 10
)

// Case is one immutable evaluation example. Evaluators may interpret Input,
// Expected, and Metadata according to their task domain. Sampled feedback
// Input and Expected values cross the reflection-model boundary. ID remains
// local and is replaced by an experiment-local ordinal before reflection. All
// fields are persisted when filesystem experiment recording is enabled.
type Case struct {
	ID       string            `json:"id"`
	Input    string            `json:"input"`
	Expected string            `json:"expected,omitempty"`
	Critical bool              `json:"critical,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Evaluation is an evaluator's result for one Case. Score must be finite and
// normalized to [0, 1]. Output, Feedback, and Trace cross the reflection-model
// boundary for sampled feedback cases. The optimizer applies best-effort
// credential redaction, but callers must remove domain-sensitive data from
// these fields and from Case Input and Expected. Score is the scalar metric
// used for search. Objectives are reported for analysis but do not participate
// in candidate selection.
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
// honoring it. Returning an error aborts the experiment; task-level failures
// should instead be expressed through Score, Feedback, and Trace.
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
// requires at least 10 cases in each dataset split. ParentRevisionID identifies
// the active revision evaluated by Seed and becomes the submission's optimistic
// concurrency token.
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
// whether it should be promoted. Algorithm identifies the implementation that
// produced it; NewGEPA reports "gepa". Internal candidates and search
// bookkeeping intentionally remain private.
type Result struct {
	Algorithm           string               `json:"algorithm"`
	ExperimentID        string               `json:"experiment_id"`
	Spec                *evolution.SkillSpec `json:"spec"`
	BaselineValidation  Summary              `json:"baseline_validation"`
	CandidateValidation Summary              `json:"candidate_validation"`
	BaselineHoldout     Summary              `json:"baseline_holdout"`
	CandidateHoldout    Summary              `json:"candidate_holdout"`
	CandidateCount      int                  `json:"candidate_count"`
	MetricCalls         int                  `json:"metric_calls"`
	StopReason          string               `json:"stop_reason"`
	PromotionEligible   bool                 `json:"promotion_eligible"`
	PromotionReason     string               `json:"promotion_reason"`
	SubmissionReason    string               `json:"submission_reason,omitempty"`
	Revision            *evolution.Revision  `json:"revision,omitempty"`
}

// Optimizer runs one isolated skill optimization experiment. Implementations
// must not mutate Request or retain its mutable fields after Optimize returns.
// A non-nil Result may accompany an error after search selected a candidate;
// its fields describe the phases that completed before the failure.
type Optimizer interface {
	Optimize(context.Context, Request) (*Result, error)
}

// Option configures the GEPA optimizer created by NewGEPA.
type Option func(*options)

type options struct {
	engine engineOptions
	gepa   gepaOptions
}

func defaultOptions() options {
	return options{
		engine: engineOptions{
			maxMetricCalls: defaultMaxMetricCalls,
			randomSeed:     1,
		},
		gepa: gepaOptions{
			maxIterations:       defaultMaxIterations,
			reflectionBatchSize: defaultReflectionBatch,
		},
	}
}

// WithMaxIterations limits reflective mutation attempts.
func WithMaxIterations(n int) Option {
	return func(o *options) { o.gepa.maxIterations = n }
}

// WithMaxMetricCalls limits the total number of evaluated cases, including
// validation and holdout comparisons. It does not count model requests or
// tokens. A non-positive value disables the cap.
func WithMaxMetricCalls(n int) Option {
	return func(o *options) { o.engine.maxMetricCalls = n }
}

// WithReflectionBatchSize sets the feedback minibatch size.
func WithReflectionBatchSize(n int) Option {
	return func(o *options) { o.gepa.reflectionBatchSize = n }
}

// WithRandomSeed makes optimizer sampling reproducible and passes stable seeds
// to paired evaluator calls. Evaluators must use the seed for deterministic
// execution when their task environment supports it.
func WithRandomSeed(seed int64) Option {
	return func(o *options) { o.engine.randomSeed = seed }
}

// WithTimeLimit bounds a complete optimization run. A non-positive duration
// leaves deadline control to the caller's context.
func WithTimeLimit(limit time.Duration) Option {
	return func(o *options) { o.engine.timeLimit = limit }
}

// WithStoreDir enables a node-local filesystem experiment record under dir.
// Records can contain dataset and evaluator data; experiment directories and
// files use private permissions on permission-aware filesystems. Each
// experiment has one writer, and this option does not provide distributed
// coordination or remote durability.
func WithStoreDir(dir string) Option {
	return func(o *options) { o.engine.storeDir = dir }
}

// WithRevisionSubmitter enables optional submission into a revision
// governance lifecycle. The optimizer borrows submitter and does not manage
// its lifecycle.
func WithRevisionSubmitter(submitter evolution.RevisionSubmitter) Option {
	return func(o *options) { o.engine.revisionSubmitter = submitter }
}

// WithMinimumHoldoutImprovement sets the required absolute paired holdout
// score delta before submission. It does not affect search or final candidate
// selection. The default is zero (no regression).
func WithMinimumHoldoutImprovement(delta float64) Option {
	return func(o *options) { o.engine.minimumHoldoutImprovement = delta }
}
