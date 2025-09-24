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
)

// EvalCaseResult represents the evaluation result for a single eval case.
type EvalCaseResult struct {
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"eval_set_id"`
	// EvalID identifies the eval case.
	EvalID string `json:"eval_id"`
	// FinalEvalStatus is the final eval status for this eval case.
	FinalEvalStatus EvalStatus `json:"final_eval_status"`
	// OverallEvalMetricResults contains overall result for each metric for the entire eval case.
	OverallEvalMetricResults []*EvalMetricResult `json:"overall_eval_metric_results"`
	// EvalMetricResultPerInvocation contains result for each metric on a per invocation basis.
	EvalMetricResultPerInvocation []*EvalMetricResultPerInvocation `json:"eval_metric_result_per_invocation"`
	// SessionID is the session id of the session generated as result of inferencing stage of the eval.
	SessionID string `json:"session_id"`
	// UserID is the user id used during inferencing stage of the eval.
	UserID string `json:"user_id,omitempty"`
}

// EvalSetResult represents the evaluation result for an entire eval set.
type EvalSetResult struct {
	// EvalSetResultID uniquely identifies this result.
	EvalSetResultID string `json:"eval_set_result_id"`
	// EvalSetResultName is the name of this result.
	EvalSetResultName string `json:"eval_set_result_name,omitempty"`
	// EvalSetID identifies the eval set.
	EvalSetID string `json:"eval_set_id"`
	// EvalCaseResults contains results for each eval case.
	EvalCaseResults []EvalCaseResult `json:"eval_case_results"`
	// CreationTimestamp when this result was created.
	CreationTimestamp evalset.EpochTime `json:"creation_timestamp"`
}

// Manager defines the interface for managing evaluation results.
type Manager interface {
	// Save stores an evaluation result.
	Save(ctx context.Context, result *EvalSetResult) error
	// Get retrieves an evaluation result by evalSetResultID.
	Get(ctx context.Context, evalSetResultID string) (*EvalSetResult, error)
	// List returns all available evaluation results.
	List(ctx context.Context) ([]*EvalSetResult, error)
}
