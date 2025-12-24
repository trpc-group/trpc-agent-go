//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evalresult provides evaluation result for evaluation.
package evalresult

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalSetResult represents the evaluation result for an entire eval set.
type EvalSetResult struct {
	// EvalSetResultID uniquely identifies this result.
	EvalSetResultID string `json:"evalSetResultId,omitempty"`
	// EvalSetResultName is the name of this result.
	EvalSetResultName string `json:"evalSetResultName,omitempty"`
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"evalSetId,omitempty"`
	// EvalCaseResults contains results for each eval case.
	EvalCaseResults []*EvalCaseResult `json:"evalCaseResults,omitempty"`
	// CreationTimestamp when this result was created.
	CreationTimestamp *epochtime.EpochTime `json:"creationTimestamp,omitempty"`
}

// EvalCaseResult represents the result of a single evaluation case.
type EvalCaseResult struct {
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"evalSetId,omitempty"`
	// EvalID identifies the eval case.
	EvalID string `json:"evalId,omitempty"`
	// FinalEvalStatus is the final eval status for this eval case.
	FinalEvalStatus status.EvalStatus `json:"finalEvalStatus,omitempty"`
	// OverallEvalMetricResults contains overall result for each metric for the entire eval case.
	OverallEvalMetricResults []*EvalMetricResult `json:"overallEvalMetricResults,omitempty"`
	// EvalMetricResultPerInvocation contains result for each metric on a per invocation basis.
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation `json:"evalMetricResultPerInvocation,omitempty"`
	// SessionID is the session id of the session generated as result of inferencing stage of the eval.
	SessionID string `json:"sessionId,omitempty"`
	// UserID is the user id used during inferencing stage of the eval.
	UserID string `json:"userId,omitempty"`
}

// EvalMetricResult represents the result of a single metric evaluation.
type EvalMetricResult struct {
	// MetricName identifies the metric.
	MetricName string `json:"metricName,omitempty"`
	// Score obtained for this metric.
	Score float64 `json:"score,omitempty"`
	// EvalStatus of this metric evaluation.
	EvalStatus status.EvalStatus `json:"evalStatus,omitempty"`
	// Threshold that was used.
	Threshold float64 `json:"threshold,omitempty"`
	// Criterion contains the criterion used for this metric evaluation.
	Criterion *criterion.Criterion `json:"criterion,omitempty"`
	// Details contains additional metric-specific information.
	Details *EvalMetricResultDetails `json:"details,omitempty"`
}

// EvalMetricResultDetails contains additional metric-specific information.
type EvalMetricResultDetails struct {
	// Reason is the reason for the metric evaluation result.
	Reason string `json:"reason,omitempty"`
	// Score is the score for the metric evaluation result.
	Score float64 `json:"score,omitempty"`
}

// EvalMetricResultPerInvocation represents metric results for a single invocation.
type EvalMetricResultPerInvocation struct {
	// ActualInvocation is the actual invocation, captured from agent run.
	ActualInvocation *evalset.Invocation `json:"actualInvocation,omitempty"`
	// ExpectedInvocation is the expected invocation.
	ExpectedInvocation *evalset.Invocation `json:"expectedInvocation,omitempty"`
	// EvalMetricResults contains results for each metric for this invocation.
	EvalMetricResults []*EvalMetricResult `json:"evalMetricResults,omitempty"`
}

// Manager defines the interface for managing evaluation results.
type Manager interface {
	// Save stores an evaluation result.
	Save(ctx context.Context, appName string, evalSetResult *EvalSetResult) (string, error)
	// Get retrieves an evaluation result by evalSetResultID.
	Get(ctx context.Context, appName, evalSetResultID string) (*EvalSetResult, error)
	// List returns all available evaluation results.
	List(ctx context.Context, appName string) ([]string, error)
}
