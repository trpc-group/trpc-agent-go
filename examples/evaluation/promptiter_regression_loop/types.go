//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "time"

type EvalSet struct {
	ID    string     `json:"eval_set_id"`
	Cases []EvalCase `json:"cases"`
}

type EvalCase struct {
	ID                string              `json:"id"`
	Input             string              `json:"input"`
	ExpectedResponse  string              `json:"expected_response"`
	ExpectedToolCalls []ToolCall          `json:"expected_tool_calls,omitempty"`
	ExpectedRoute     string              `json:"expected_route,omitempty"`
	RetrievalRequired *bool               `json:"retrieval_required,omitempty"`
	Critical          bool                `json:"critical,omitempty"`
	Runs              map[string]RunTrace `json:"runs"`
}

type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type RunTrace struct {
	FinalResponse string     `json:"final_response"`
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`
	Route         string     `json:"route,omitempty"`
	FormatValid   bool       `json:"format_valid"`
	RetrievalHit  bool       `json:"retrieval_hit"`
	Cost          float64    `json:"cost"`
	LatencyMS     int64      `json:"latency_ms"`
	Error         string     `json:"error,omitempty"`
}

type MetricsConfig struct {
	PassThreshold  float64 `json:"pass_threshold"`
	ResponseWeight float64 `json:"response_weight"`
	ToolWeight     float64 `json:"tool_weight"`
	FormatWeight   float64 `json:"format_weight"`
}

type LoopConfig struct {
	Seed       int64             `json:"seed"`
	Engine     EngineConfig      `json:"engine"`
	Gate       GateConfig        `json:"gate"`
	Candidates []CandidateConfig `json:"candidates"`
}

type EngineConfig struct {
	Type  string `json:"type"`
	Model string `json:"model"`
}

type CandidateConfig struct {
	ID         string `json:"id"`
	PromptFile string `json:"prompt_file"`
}

type GateConfig struct {
	MinValidationGain float64  `json:"min_validation_gain"`
	NoNewHardFails    bool     `json:"no_new_hard_fails"`
	CriticalCaseIDs   []string `json:"critical_case_ids"`
	MaxCostIncrease   *float64 `json:"max_cost_increase,omitempty"`
	MaxToolCalls      *int     `json:"max_tool_calls,omitempty"`
}

type Attribution string

const (
	AttributionFinalResponse Attribution = "final_response_mismatch"
	AttributionToolCall      Attribution = "tool_call_error"
	AttributionToolArgs      Attribution = "tool_argument_error"
	AttributionRoute         Attribution = "route_error"
	AttributionFormat        Attribution = "format_error"
	AttributionKnowledge     Attribution = "knowledge_retrieval_insufficient"
	AttributionRuntime       Attribution = "runtime_error"
	AttributionUnknown       Attribution = "unknown"
)

type CaseResult struct {
	CaseID        string      `json:"case_id"`
	Critical      bool        `json:"critical"`
	Score         float64     `json:"score"`
	Passed        bool        `json:"passed"`
	Attribution   Attribution `json:"attribution,omitempty"`
	Reason        string      `json:"reason,omitempty"`
	FinalResponse string      `json:"final_response"`
	ToolCalls     []ToolCall  `json:"tool_calls,omitempty"`
	Trace         RunTrace    `json:"trace"`
}

type EvaluationResult struct {
	EvalSetID    string       `json:"eval_set_id"`
	PromptID     string       `json:"prompt_id"`
	OverallScore float64      `json:"overall_score"`
	Passed       int          `json:"passed"`
	Failed       int          `json:"failed"`
	TotalCost    float64      `json:"total_cost"`
	ToolCalls    int          `json:"tool_calls"`
	LatencyMS    int64        `json:"latency_ms"`
	Cases        []CaseResult `json:"cases"`
}

type CaseDelta struct {
	CaseID         string  `json:"case_id"`
	BaselineScore  float64 `json:"baseline_score"`
	CandidateScore float64 `json:"candidate_score"`
	ScoreDelta     float64 `json:"score_delta"`
	NewlyPassed    bool    `json:"newly_passed"`
	NewlyFailed    bool    `json:"newly_failed"`
	Critical       bool    `json:"critical"`
}

type DeltaSummary struct {
	ScoreDelta    float64     `json:"score_delta"`
	NewlyPassed   int         `json:"newly_passed"`
	NewlyFailed   int         `json:"newly_failed"`
	Improved      int         `json:"improved"`
	Regressed     int         `json:"regressed"`
	CaseSetErrors []string    `json:"case_set_errors,omitempty"`
	Cases         []CaseDelta `json:"cases"`
}

type GateDecision struct {
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons"`
}

type RoundAudit struct {
	Round       int              `json:"round"`
	CandidateID string           `json:"candidate_id"`
	Prompt      string           `json:"prompt"`
	Train       EvaluationResult `json:"train"`
	Validation  EvaluationResult `json:"validation"`
	Delta       DeltaSummary     `json:"delta"`
	Gate        GateDecision     `json:"gate"`
	DurationMS  int64            `json:"duration_ms"`
}

type OptimizationReport struct {
	StartedAt          time.Time           `json:"started_at"`
	DurationMS         int64               `json:"duration_ms"`
	Seed               int64               `json:"seed"`
	Engine             EngineConfig        `json:"engine"`
	Metrics            MetricsConfig       `json:"metrics"`
	Gate               GateConfig          `json:"gate"`
	BaselinePrompt     string              `json:"baseline_prompt"`
	BaselineTrain      EvaluationResult    `json:"baseline_train"`
	BaselineValidation EvaluationResult    `json:"baseline_validation"`
	AttributionCounts  map[Attribution]int `json:"attribution_counts"`
	Rounds             []RoundAudit        `json:"rounds"`
	SelectedCandidate  string              `json:"selected_candidate,omitempty"`
	SelectedPrompt     string              `json:"selected_prompt,omitempty"`
	Accepted           bool                `json:"accepted"`
	DecisionReasons    []string            `json:"decision_reasons"`
}
