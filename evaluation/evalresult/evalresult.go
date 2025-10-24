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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalSetResult represents the evaluation result for an entire eval set.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type EvalSetResult struct {
	// EvalSetResultID uniquely identifies this result.
	EvalSetResultID string `json:"eval_set_result_id,omitempty"`
	// EvalSetResultName is the name of this result.
	EvalSetResultName string `json:"eval_set_result_name,omitempty"`
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"eval_set_id,omitempty"`
	// EvalCaseResults contains results for each eval case.
	EvalCaseResults []*EvalCaseResult `json:"eval_case_results,omitempty"`
	// CreationTimestamp when this result was created.
	CreationTimestamp *epochtime.EpochTime `json:"creation_timestamp,omitempty"`
}

// EvalCaseResult represents the result of a single evaluation case.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type EvalCaseResult struct {
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"eval_set_id,omitempty"`
	// EvalID identifies the eval case.
	EvalID string `json:"eval_id,omitempty"`
	// FinalEvalStatus is the final eval status for this eval case.
	FinalEvalStatus status.EvalStatus `json:"final_eval_status,omitempty"`
	// OverallEvalMetricResults contains overall result for each metric for the entire eval case.
	OverallEvalMetricResults []*EvalMetricResult `json:"overall_eval_metric_results,omitempty"`
	// EvalMetricResultPerInvocation contains result for each metric on a per invocation basis.
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation `json:"eval_metric_result_per_invocation,omitempty"`
	// SessionID is the session id of the session generated as result of inferencing stage of the eval.
	SessionID string `json:"session_id,omitempty"`
	// UserID is the user id used during inferencing stage of the eval.
	UserID string `json:"user_id,omitempty"`
}

// EvalMetricResult represents the result of a single metric evaluation.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type EvalMetricResult struct {
	// MetricName identifies the metric.
	MetricName string `json:"metric_name,omitempty"`
	// Score obtained for this metric.
	Score float64 `json:"score,omitempty"`
	// EvalStatus of this metric evaluation.
	EvalStatus status.EvalStatus `json:"eval_status,omitempty"`
	// Threshold that was used.
	Threshold float64 `json:"threshold,omitempty"`
	// Details contains additional metric-specific information.
	Details map[string]any `json:"details,omitempty"`
}

// EvalMetricResultPerInvocation represents metric results for a single invocation.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type EvalMetricResultPerInvocation struct {
	// ActualInvocation is the actual invocation, captured from agent run.
	ActualInvocation *evalset.Invocation `json:"actual_invocation,omitempty"`
	// ExpectedInvocation is the expected invocation.
	ExpectedInvocation *evalset.Invocation `json:"expected_invocation,omitempty"`
	// EvalMetricResults contains results for each metric for this invocation.
	EvalMetricResults []*EvalMetricResult `json:"eval_metric_results,omitempty"`
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
