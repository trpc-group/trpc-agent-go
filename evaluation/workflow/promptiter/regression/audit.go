//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import "time"

// Usage records auditable evaluation and model resource counters.
type Usage struct {
	EvaluationCaseRuns  int  `json:"evaluationCaseRuns"`
	ModelCalls          int  `json:"modelCalls"`
	ToolCalls           int  `json:"toolCalls"`
	InputTokens         int  `json:"inputTokens"`
	OutputTokens        int  `json:"outputTokens"`
	Retries             int  `json:"retries"`
	TokenUsageAvailable bool `json:"tokenUsageAvailable"`
}

// Timing records wall-clock boundaries for the complete pipeline.
type Timing struct {
	StartedAt       time.Time `json:"startedAt"`
	FinishedAt      time.Time `json:"finishedAt"`
	DurationSeconds float64   `json:"durationSeconds"`
}

// EstimatedCost records a best-effort cost estimate and its source.
type EstimatedCost struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	Source   string  `json:"source"`
}

// ResourceSnapshot records resources consumed by one profile evaluation.
type ResourceSnapshot struct {
	Usage          Usage         `json:"usage"`
	LatencySeconds float64       `json:"latencySeconds"`
	EstimatedCost  EstimatedCost `json:"estimatedCost"`
}

// ResourceDelta records candidate resource changes against a comparison profile.
type ResourceDelta struct {
	EvaluationCaseRuns  int     `json:"evaluationCaseRuns"`
	ModelCalls          int     `json:"modelCalls"`
	ToolCalls           int     `json:"toolCalls"`
	LatencySeconds      float64 `json:"latencySeconds"`
	EstimatedCostAmount float64 `json:"estimatedCostAmount"`
}

// ResourceComparison records both sides and the delta used by a release check.
type ResourceComparison struct {
	LastReleased ResourceSnapshot `json:"lastReleased"`
	Candidate    ResourceSnapshot `json:"candidate"`
	Delta        ResourceDelta    `json:"delta"`
}

// EvaluationResourceComparison separates train and validation measurements.
type EvaluationResourceComparison struct {
	Train      ResourceComparison `json:"train"`
	Validation ResourceComparison `json:"validation"`
}

// ModelConfig identifies the model or deterministic engine used by a run.
type ModelConfig struct {
	Mode   string         `json:"mode"`
	Name   string         `json:"name"`
	Config map[string]any `json:"config,omitempty"`
}
