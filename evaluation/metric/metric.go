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
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type EvalMetric struct {
	// MetricName identifies the metric.
	MetricName string `json:"metricName"`
	// Threshold value for this metric.
	Threshold float64 `json:"threshold"`
	// JudgeModelOptions for metrics that use LLM-as-Judge.
	JudgeModelOptions *JudgeModelOptions `json:"judgeModelOptions,omitempty"`
	// Config contains metric-specific configuration.
	Config map[string]any `json:"config,omitempty"`
}

// JudgeModelOptions contains options for LLM-as-Judge evaluation.
// It mirrors the schema used by ADK Web, with field names in camel-case to align with the JSON format.
type JudgeModelOptions struct {
	// JudgeModel is the name of the model to use.
	JudgeModel string `json:"judgeModel"`
	// Temperature is the temperature for the judge model.
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens is the maximum number of tokens for the judge model response.
	MaxTokens *int `json:"maxTokens,omitempty"`
	// NumSamples is the number of times to sample the model.
	NumSamples *int `json:"numSamples,omitempty"`
	// CustomPrompt is the custom prompt template.
	CustomPrompt string `json:"customPrompt,omitempty"`
}

// Manager defines the interface for managing evaluation metrics.
type Manager interface {
	// List returns all metric names identified by the given app name and eval set ID.
	List(ctx context.Context, appName, evalSetID string) ([]string, error)
	// Save stores the given metrics identified by the given app name and eval set ID.
	Save(ctx context.Context, appName, evalSetID string, metrics []*EvalMetric) error
	// Get gets a metric identified by the given app name, eval set ID and metric name.
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
}
