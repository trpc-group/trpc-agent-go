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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Service defines the contract that an evaluation service must satisfy.
// It covers two phases: inference to capture agent responses, and evaluation to
// score those responses with the configured metrics.
type Service interface {
	// Inference executes the agent for the requested eval cases and returns the
	// recorded invocations for each case.
	Inference(ctx context.Context, request *InferenceRequest) ([]*InferenceResult, error)

	// Evaluate scores the previously captured invocations and returns evaluation
	// results for each case.
	Evaluate(ctx context.Context, request *EvaluateRequest) ([]*evalresult.EvalCaseResult, error)
}

// InferenceRequest represents a request for running agent inference on an eval set.
type InferenceRequest struct {
	// AppName is the name of the app.
	AppName string `json:"app_name"`
	// EvalSetID is the ID of the eval set.
	EvalSetID string `json:"eval_set_id"`
	// EvalCaseIDs are the IDs of eval cases to process.
	// If not specified, all eval cases in the eval set will be processed.
	EvalCaseIDs []string `json:"eval_case_ids,omitempty"`
}

// InferenceResult contains the agent-generated invocations for a single eval case.
type InferenceResult struct {
	// AppName is the name of the app.
	AppName string `json:"app_name"`
	// EvalSetID is the ID of the eval set.
	EvalSetID string `json:"eval_set_id"`
	// EvalCaseID is the ID of the eval case.
	EvalCaseID string `json:"eval_case_id"`
	// Inferences are the generated invocations.
	Inferences []*evalset.Invocation `json:"inferences,omitempty"`
	// SessionID is the ID of the inference session.
	SessionID string `json:"session_id,omitempty"`
	// Status is the status of the inference.
	Status status.EvalStatus `json:"status"`
	// ErrorMessage contains error details if inference failed.
	ErrorMessage string `json:"error_message,omitempty"`
}

// EvaluateRequest represents a request for evaluating the inference results.
type EvaluateRequest struct {
	// InferenceResults are the results to be evaluated.
	InferenceResults []*InferenceResult `json:"inference_results"`
	// EvaluateConfig contains the metric configuration used during evaluation.
	EvaluateConfig *EvaluateConfig `json:"evaluate_config"`
}

// EvaluateConfig contains metric configuration used during evaluation.
type EvaluateConfig struct {
	// EvalMertrics contains the metrics to be evaluated.
	EvalMertrics []*metric.EvalMetric `json:"eval_mertrics"`
}
