//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// Config describes one reproducible prompt optimization run.
type Config struct {
	AppName           string            `json:"app_name"`
	Mode              string            `json:"mode"`
	Seed              int64             `json:"seed"`
	PromptSource      string            `json:"prompt_source"`
	TrainEvalSet      string            `json:"train_evalset"`
	ValidationEvalSet string            `json:"validation_evalset"`
	Metrics           string            `json:"metrics"`
	OutputDir         string            `json:"output_dir"`
	TargetSurfaceID   string            `json:"target_surface_id"`
	MaxRounds         int               `json:"max_rounds"`
	FakeEngine        FakeEngineConfig  `json:"fake_engine"`
	LLM               LLMConfig         `json:"llm"`
	Gate              GateConfig        `json:"gate"`
	Candidates        []CandidateConfig `json:"candidates"`
}

// LLMConfig configures the real OpenAI-compatible PromptIter runtime.
type LLMConfig struct {
	CandidateModel string  `json:"candidate_model"`
	JudgeModel     string  `json:"judge_model"`
	WorkerModel    string  `json:"worker_model"`
	Temperature    float64 `json:"temperature"`
	MaxTokens      int     `json:"max_tokens"`
	DebugIO        bool    `json:"debug_io"`
}

// FakeEngineConfig captures deterministic runner metadata for auditability.
type FakeEngineConfig struct {
	Name        string `json:"name"`
	Model       string `json:"model"`
	TraceMode   bool   `json:"trace_mode"`
	Determinism string `json:"determinism"`
}

// CandidateConfig describes one PromptIter-style candidate patch.
type CandidateConfig struct {
	ID           string `json:"id"`
	Description  string `json:"description"`
	AppendPrompt string `json:"append_prompt"`
}

// GateConfig controls candidate acceptance.
type GateConfig struct {
	MinValidationGain        float64  `json:"min_validation_gain"`
	MaxNewHardFails          int      `json:"max_new_hard_fails"`
	RejectCriticalRegression bool     `json:"reject_critical_regression"`
	MaxCalls                 int      `json:"max_calls"`
	MaxEstimatedUSD          float64  `json:"max_estimated_usd"`
	CriticalCaseIDs          []string `json:"critical_case_ids"`
}

// LoadedInput contains parsed pipeline inputs.
type LoadedInput struct {
	Config            Config
	ConfigDir         string
	BaselinePrompt    string
	TrainEvalSet      EvalSetInput
	ValidationEvalSet EvalSetInput
	Metrics           []MetricInput
}

// EvalSetInput mirrors the evaluation evalset JSON shape used by examples.
type EvalSetInput struct {
	EvalSetID string     `json:"evalSetId,omitempty"`
	Name      string     `json:"name,omitempty"`
	Cases     []EvalCase `json:"evalCases,omitempty"`
}

// EvalCase is the subset of evalset.EvalCase used by this deterministic loop.
type EvalCase struct {
	EvalID       string       `json:"evalId,omitempty"`
	Critical     bool         `json:"critical,omitempty"`
	Conversation []Invocation `json:"conversation,omitempty"`
	SessionInput SessionInput `json:"sessionInput,omitempty"`
}

// SessionInput carries app and user identity for audit output.
type SessionInput struct {
	AppName string `json:"appName,omitempty"`
	UserID  string `json:"userId,omitempty"`
}

// Invocation is the subset of evalset.Invocation needed for local scoring.
type Invocation struct {
	InvocationID  string     `json:"invocationId,omitempty"`
	UserContent   *Message   `json:"userContent,omitempty"`
	FinalResponse *Message   `json:"finalResponse,omitempty"`
	Tools         []ToolCall `json:"tools,omitempty"`
}

// Message stores a conversational message.
type Message struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ToolCall stores an expected or actual tool call.
type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments any    `json:"arguments,omitempty"`
	Result    any    `json:"result,omitempty"`
}

// MetricInput is the subset of metric.EvalMetric needed by the example.
type MetricInput struct {
	MetricName string  `json:"metricName,omitempty"`
	Threshold  float64 `json:"threshold,omitempty"`
}

// EvaluationRun captures one train or validation evaluation pass.
type EvaluationRun struct {
	Name         string       `json:"name"`
	EvalSetID    string       `json:"eval_set_id"`
	OverallScore float64      `json:"overall_score"`
	Passed       int          `json:"passed"`
	Failed       int          `json:"failed"`
	Cases        []CaseResult `json:"cases"`
	LatencyMs    int64        `json:"latency_ms"`
	Cost         CostSummary  `json:"cost"`
}

// CaseResult captures per-case scoring and trace details.
type CaseResult struct {
	EvalSetID      string               `json:"eval_set_id"`
	CaseID         string               `json:"case_id"`
	Critical       bool                 `json:"critical"`
	Score          float64              `json:"score"`
	Status         status.EvalStatus    `json:"status"`
	Metrics        []MetricResult       `json:"metrics"`
	FailureReasons []FailureAttribution `json:"failure_reasons,omitempty"`
	Expected       Invocation           `json:"expected"`
	Actual         Invocation           `json:"actual"`
	Trace          TraceSummary         `json:"trace"`
	LatencyMs      int64                `json:"latency_ms"`
}

// MetricResult captures one metric score.
type MetricResult struct {
	MetricName string            `json:"metric_name"`
	Score      float64           `json:"score"`
	Threshold  float64           `json:"threshold"`
	Status     status.EvalStatus `json:"status"`
	Reason     string            `json:"reason,omitempty"`
}

// TraceSummary is a compact, deterministic trace-mode artifact.
type TraceSummary struct {
	Mode           string     `json:"mode"`
	Route          string     `json:"route"`
	ToolTrajectory []ToolCall `json:"tool_trajectory,omitempty"`
	Signals        []string   `json:"signals,omitempty"`
}

// FailureAttribution explains why a failed metric failed.
type FailureAttribution struct {
	Category   string `json:"category"`
	MetricName string `json:"metric_name,omitempty"`
	Evidence   string `json:"evidence"`
}

// DeltaSummary compares candidate validation results against baseline.
type DeltaSummary struct {
	BaselineScore     float64     `json:"baseline_score"`
	CandidateScore    float64     `json:"candidate_score"`
	ScoreDelta        float64     `json:"score_delta"`
	NewlyPassed       int         `json:"newly_passed"`
	NewlyFailed       int         `json:"newly_failed"`
	Improved          int         `json:"improved"`
	Regressed         int         `json:"regressed"`
	CriticalRegressed int         `json:"critical_regressed"`
	Cases             []CaseDelta `json:"cases"`
}

// CaseDelta captures per-case candidate movement.
type CaseDelta struct {
	CaseID          string               `json:"case_id"`
	Critical        bool                 `json:"critical"`
	BaselineScore   float64              `json:"baseline_score"`
	CandidateScore  float64              `json:"candidate_score"`
	ScoreDelta      float64              `json:"score_delta"`
	BaselineStatus  status.EvalStatus    `json:"baseline_status"`
	CandidateStatus status.EvalStatus    `json:"candidate_status"`
	Transition      string               `json:"transition"`
	FailureReasons  []FailureAttribution `json:"failure_reasons,omitempty"`
}

// GateDecision records accept/reject state and all contributing reasons.
type GateDecision struct {
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons"`
}

// CostSummary provides deterministic budget accounting.
type CostSummary struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalCalls       int     `json:"total_calls"`
	EstimatedUSD     float64 `json:"estimated_usd"`
}

// LatencySummary records wall-clock timing.
type LatencySummary struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMs int64     `json:"duration_ms"`
}

// FailureSummary aggregates failure attribution categories.
type FailureSummary struct {
	Train      map[string]int `json:"train"`
	Validation map[string]int `json:"validation"`
}

// CandidateSummary records the selected candidate prompt and validation result.
type CandidateSummary struct {
	ID                   string        `json:"id"`
	Description          string        `json:"description"`
	Prompt               string        `json:"prompt"`
	TrainEvaluation      EvaluationRun `json:"train_evaluation"`
	ValidationEvaluation EvaluationRun `json:"validation_evaluation"`
}

// RoundAudit stores PromptIter-style round artifacts.
type RoundAudit struct {
	Round         int                   `json:"round"`
	CandidateID   string                `json:"candidate_id"`
	Losses        []promptiter.CaseLoss `json:"losses"`
	Patches       *promptiter.PatchSet  `json:"patches"`
	OutputProfile *promptiter.Profile   `json:"output_profile"`
	Delta         DeltaSummary          `json:"delta"`
	Gate          GateDecision          `json:"gate"`
	Cost          CostSummary           `json:"cost"`
	LatencyMs     int64                 `json:"latency_ms"`
}

// OptimizationReport is written as optimization_report.json.
type OptimizationReport struct {
	RunID              string           `json:"run_id"`
	AppName            string           `json:"app_name"`
	Mode               string           `json:"mode"`
	DataSource         string           `json:"data_source"`
	Seed               int64            `json:"seed"`
	TargetSurfaceID    string           `json:"target_surface_id"`
	PromptSource       string           `json:"prompt_source"`
	FakeEngine         FakeEngineConfig `json:"fake_engine"`
	BaselinePrompt     string           `json:"baseline_prompt"`
	BaselineTrain      EvaluationRun    `json:"baseline_train"`
	BaselineValidation EvaluationRun    `json:"baseline_validation"`
	Candidate          CandidateSummary `json:"candidate"`
	Delta              DeltaSummary     `json:"delta"`
	Gate               GateDecision     `json:"gate"`
	FailureAttribution FailureSummary   `json:"failure_attribution"`
	Cost               CostSummary      `json:"cost"`
	Latency            LatencySummary   `json:"latency"`
	Rounds             []RoundAudit     `json:"rounds"`
}
