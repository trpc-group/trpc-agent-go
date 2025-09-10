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
)

// EvaluationService defines the main interface for evaluation operations
// This interface aligns with ADK Python's BaseEvalService
type EvaluationService interface {
	// PerformInference performs inference for eval cases and returns results as they become available
	// Equivalent to Python's perform_inference method that returns AsyncGenerator[InferenceResult, None]
	PerformInference(ctx context.Context, request *InferenceRequest) (<-chan *InferenceResult, error)

	// Evaluate evaluates inference results and returns eval case results as they become available
	// Equivalent to Python's evaluate method that returns AsyncGenerator[EvalCaseResult, None]
	Evaluate(ctx context.Context, request *EvaluateRequest) (<-chan *evalresult.EvalCaseResult, error)
}

// InferenceConfig represents configuration for agent inference
type InferenceConfig struct {
	// AgentConfig contains agent-specific configuration
	AgentConfig map[string]interface{} `json:"agent_config" yaml:"agent_config"`

	// MaxTokens for response generation
	MaxTokens int `json:"max_tokens" yaml:"max_tokens"`

	// Temperature for response generation
	Temperature float64 `json:"temperature" yaml:"temperature"`
}

// InferenceRequest represents a request for agent inference
type InferenceRequest struct {
	// AppName is the name of the app
	AppName string `json:"app_name"`

	// EvalSetID is the ID of the eval set
	EvalSetID string `json:"eval_set_id"`

	// EvalCaseIDs are the IDs of eval cases to process (optional)
	EvalCaseIDs []string `json:"eval_case_ids,omitempty"`

	// InferenceConfig is the configuration for inference
	InferenceConfig InferenceConfig `json:"inference_config"`
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
    Inferences []evalset.Invocation `json:"inferences,omitempty"`

	// SessionID is the ID of the inference session
	SessionID string `json:"session_id,omitempty"`

	// Status is the status of the inference
	Status InferenceStatus `json:"status"`

	// ErrorMessage contains error details if inference failed
	ErrorMessage string `json:"error_message,omitempty"`
}

// EvaluateRequest represents a request for evaluation
type EvaluateRequest struct {
	// InferenceResults are the results to be evaluated
	InferenceResults []InferenceResult `json:"inference_results"`

	// EvaluateConfig is the configuration for evaluation
	EvaluateConfig EvaluateConfig `json:"evaluate_config"`
}

// InferenceStatus represents the status of inference
type InferenceStatus int

const (
	InferenceStatusUnknown InferenceStatus = iota
	InferenceStatusSuccess
	InferenceStatusFailure
)

func (s InferenceStatus) String() string {
	switch s {
	case InferenceStatusSuccess:
		return "success"
	case InferenceStatusFailure:
		return "failure"
	default:
		return "unknown"
	}
}

// EvaluateConfig represents configuration for evaluation
type EvaluateConfig struct {
	// Metrics to evaluate
	Metrics []metric.EvalMetric `json:"metrics" yaml:"metrics"`

	// InferenceConfig for running the agent
	InferenceConfig InferenceConfig `json:"inference_config" yaml:"inference_config"`

	// ConcurrencyConfig for parallel processing
	ConcurrencyConfig ConcurrencyConfig `json:"concurrency_config" yaml:"concurrency_config"`
}

// ConcurrencyConfig controls parallel execution
type ConcurrencyConfig struct {
	// MaxInferenceConcurrency for agent inference
	MaxInferenceConcurrency int `json:"max_inference_concurrency" yaml:"max_inference_concurrency"`

	// MaxEvalConcurrency for evaluation
	MaxEvalConcurrency int `json:"max_eval_concurrency" yaml:"max_eval_concurrency"`
}
