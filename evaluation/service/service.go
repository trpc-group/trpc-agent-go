//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package service provides evaluate service.
package service

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Service defines the interface that an evaluation service must satisfy.
// It covers two phases: inference to capture agent responses, and evaluation to
// score those responses with the configured metrics.
type Service interface {
	// Inference runs the agent for the requested eval cases and returns the inference results for each case.
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error)
	// Evaluate runs the evaluation on the inference results and returns the persisted eval set result.
	Evaluate(ctx context.Context, request *EvaluateRequest) (*evalresult.EvalSetResult, error)
}

// InferenceRequest represents a request for running the agent inference on an eval set.
type InferenceRequest struct {
	// AppName is the name of the app.
	AppName string `json:"appName,omitempty"`
	// EvalSetID is the ID of the eval set.
	EvalSetID string `json:"evalSetId,omitempty"`
	// EvalCaseIDs are the IDs of eval cases to process.
	// If not specified, all eval cases in the eval set will be processed.
	EvalCaseIDs []string `json:"evalCaseIds,omitempty"`
}

// InferenceResult contains the inference results for a single eval case.
type InferenceResult struct {
	// AppName is the name of the app.
	AppName string `json:"appName,omitempty"`
	// EvalSetID is the ID of the eval set.
	EvalSetID string `json:"evalSetId,omitempty"`
	// EvalCaseID is the ID of the eval case.
	EvalCaseID string `json:"evalCaseId,omitempty"`
	// Inferences are the inference results.
	Inferences []*evalset.Invocation `json:"inferences,omitempty"`
	// SessionID is the ID of the inference session.
	SessionID string `json:"sessionId,omitempty"`
	// Status is the status of the inference.
	Status status.EvalStatus `json:"status,omitempty"`
	// ErrorMessage contains the error message if inference failed.
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// EvaluateRequest represents a request for running the evaluation on the inference results.
type EvaluateRequest struct {
	// AppName is the name of the app.
	AppName string `json:"appName,omitempty"`
	// EvalSetID is the ID of the eval set.
	EvalSetID string `json:"evalSetId,omitempty"`
	// InferenceResults are the inference results to be evaluated.
	InferenceResults []*InferenceResult `json:"inferenceResults,omitempty"`
	// EvaluateConfig contains the evaluation configuration used during evaluation.
	EvaluateConfig *EvaluateConfig `json:"evaluateConfig,omitempty"`
}

// EvaluateConfig contains evaluation configuration used during evaluation.
type EvaluateConfig struct {
	// EvalMetrics contains the metrics to be evaluated.
	EvalMetrics []*metric.EvalMetric `json:"evalMetrics,omitempty"`
}
