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

import "context"

// EvalMetric represents a metric used to evaluate a particular aspect of an eval case.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type EvalMetric struct {
	// MetricName identifies the metric.
	MetricName string `json:"metric_name,omitempty"`
	// Threshold value for this metric.
	Threshold float64 `json:"threshold,omitempty"`
	// JudgeModelOptions for metrics that use LLM-as-Judge.
	JudgeModelOptions *JudgeModelOptions `json:"judge_model_options,omitempty"`
	// Config contains metric-specific configuration.
	Config map[string]any `json:"config,omitempty"`
}

// JudgeModelOptions contains options for LLM-as-Judge evaluation.
// It mirrors the schema used by ADK Web, with field names in snake_case to align with the JSON format.
type JudgeModelOptions struct {
	// JudgeModel is the name of the model to use.
	JudgeModel string `json:"judge_model,omitempty"`
	// Temperature is the temperature for the judge model.
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens is the maximum number of tokens for the judge model response.
	MaxTokens *int `json:"max_tokens,omitempty"`
	// NumSamples is the number of times to sample the model.
	NumSamples *int `json:"num_samples,omitempty"`
	// CustomPrompt is the custom prompt template.
	CustomPrompt string `json:"custom_prompt,omitempty"`
}

// Manager defines the interface for managing evaluation metrics.
type Manager interface {
	// List returns all metric names identified by the given app name and eval set ID.
	List(ctx context.Context, appName, evalSetID string) ([]string, error)
	// Get gets a metric identified by the given app name, eval set ID and metric name.
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
	// Add adds a metric to EvalSet identified by evalSetID.
	Add(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Delete deletes the metric from EvalSet identified by evalSetID and metricName.
	Delete(ctx context.Context, appName, evalSetID, metricName string) error
	// Update updates the metric identified by evalSetID and metric.MetricName.
	Update(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
}
