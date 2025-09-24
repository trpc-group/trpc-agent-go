//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package service provides service for evaluation.
package service

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// Service defines the main interface for evaluation operations.
// This interface aligns with ADK Python's BaseEvalService.
type Service interface {
	// PerformInference performs inference for eval cases and returns results as they become available
	// Equivalent to Python's perform_inference method that returns AsyncGenerator[InferenceResult, None]
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error)

	// Evaluate evaluates inference results and returns eval case results as they become available
	// Equivalent to Python's evaluate method that returns AsyncGenerator[EvalCaseResult, None]
	Evaluate(ctx context.Context, request *EvaluateRequest) ([]*evalresult.EvalCaseResult, error)
}

// InferenceRequest represents a request for agent inference
type InferenceRequest struct {
	AppName string `json:"app_name"`
	// EvalSetID is the ID of the eval set
	EvalSetID string `json:"eval_set_id"`
	// EvalCaseIDs are the IDs of eval cases to process (optional)
	EvalCaseIDs []string `json:"eval_case_ids,omitempty"`
}

// InferenceResult represents the result of agent inference
type InferenceResult struct {
	// AppName is the name of the app
	AppName string `json:"app_name"`
	// EvalSetID is the ID of the eval set
	EvalSetID string `json:"eval_set_id"`
	// EvalCaseID is the ID of the eval case
	EvalCaseID string `json:"eval_case_id"`
	// Inferences are the generated invocations
	// Using a concrete type avoids ambiguity and simplifies downstream evaluators.
	Inferences []*evalset.Invocation `json:"inferences,omitempty"`
	// SessionID is the ID of the inference session
	SessionID string `json:"session_id,omitempty"`
	// Status is the status of the inference
	Status evalresult.EvalStatus `json:"status"`
	// ErrorMessage contains error details if inference failed
	ErrorMessage string `json:"error_message,omitempty"`
}

// EvaluateRequest represents a request for evaluation.
type EvaluateRequest struct {
	// InferenceResults are the results to be evaluated
	InferenceResults []*InferenceResult `json:"inference_results"`
	EvaluateConfig   *EvaluateConfig
}

type EvaluateConfig struct {
	EvalMertrics []*evalset.EvalMetric
}
