//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package metric provides evaluation metrics.
package metric

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalMetric represents a metric used to evaluate a particular aspect of an eval case
type EvalMetric struct {
	// MetricName identifies the metric
	MetricName string `json:"metric_name"`
	// Threshold value for this metric
	Threshold float64 `json:"threshold"`
	// JudgeModelOptions for metrics that use LLM-as-Judge
	JudgeModelOptions *JudgeModelOptions `json:"judge_model_options,omitempty"`
	// Config contains metric-specific configuration
	Config map[string]interface{} `json:"config,omitempty"`
}

// JudgeModelOptions contains options for LLM-as-Judge evaluation
type JudgeModelOptions struct {
	// JudgeModel name of the model to use
	JudgeModel string `json:"judge_model"`
	// Temperature for the judge model
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens for the judge model response
	MaxTokens *int `json:"max_tokens,omitempty"`
	// NumSamples number of times to sample the model
	NumSamples *int `json:"num_samples,omitempty"`
	// CustomPrompt custom prompt template
	CustomPrompt string `json:"custom_prompt,omitempty"`
}

// EvalMetricResult represents the result of a single metric evaluation.
type EvalMetricResult struct {
	// MetricName identifies the metric.
	MetricName string `json:"metric_name"`
	// Score obtained for this metric.
	Score float64 `json:"score,omitempty"`
	// Status of this metric evaluation.
	Status status.EvalStatus `json:"status"`
	// Threshold that was used.
	Threshold float64 `json:"threshold"`
	// Details contains additional metric-specific information.
	Details map[string]interface{} `json:"details,omitempty"`
}

// EvalMetricResultPerInvocation represents metric results for a single invocation.
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation `json:"actual_invocation"`
	ExpectedInvocation *evalset.Invocation `json:"expected_invocation"`
	// MetricResults contains results for each metric for this invocation.
	MetricResults []*EvalMetricResult `json:"metric_results"`
}
