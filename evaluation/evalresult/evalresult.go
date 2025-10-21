//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evalresult provides evaluation result for evaluation set.
package evalresult

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalSetResult represents the evaluation result for an entire eval set.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvalSetResult struct {
	// EvalSetResultID uniquely identifies this result.
	EvalSetResultID string `json:"evalSetResultId"`
	// EvalSetResultName is the name of this result.
	EvalSetResultName string `json:"evalSetResultName,omitempty"`
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"evalSetId"`
	// EvalCaseResults contains results for each eval case.
	EvalCaseResults []*EvalCaseResult `json:"evalCaseResults"`
	// CreationTimestamp when this result was created.
	CreationTimestamp *evalset.EpochTime `json:"creationTimestamp"`
}

// EvalCaseResult represents the result of a single evaluation case.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvalCaseResult struct {
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"evalSetId"`
	// EvalID identifies the eval case.
	EvalID string `json:"evalId"`
	// FinalEvalStatus is the final eval status for this eval case.
	FinalEvalStatus status.EvalStatus `json:"finalEvalStatus"`
	// OverallEvalMetricResults contains overall result for each metric for the entire eval case.
	OverallEvalMetricResults []*EvalMetricResult `json:"overallEvalMetricResults"`
	// EvalMetricResultPerInvocation contains result for each metric on a per invocation basis.
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation `json:"evalMetricResultPerInvocation"`
	// SessionID is the session id of the session generated as result of inferencing stage of the eval.
	SessionID string `json:"sessionId"`
	// UserID is the user id used during inferencing stage of the eval.
	UserID string `json:"userId,omitempty"`
}

// EvalMetricResult represents the result of a single metric evaluation.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvalMetricResult struct {
	// MetricName identifies the metric.
	MetricName string `json:"metricName"`
	// Score obtained for this metric.
	Score float64 `json:"score,omitempty"`
	// Status of this metric evaluation.
	Status status.EvalStatus `json:"status"`
	// Threshold that was used.
	Threshold float64 `json:"threshold"`
	// Details contains additional metric-specific information.
	Details map[string]any `json:"details,omitempty"`
}

// EvalMetricResultPerInvocation represents metric results for a single invocation.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation `json:"actualInvocation"`
	ExpectedInvocation *evalset.Invocation `json:"expectedInvocation"`
	// MetricResults contains results for each metric for this invocation.
	MetricResults []*EvalMetricResult `json:"metricResults"`
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
