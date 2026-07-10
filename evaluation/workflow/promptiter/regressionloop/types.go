//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regressionloop adds an auditable regression gate around PromptIter.
package regressionloop

import (
	"encoding/json"
	"time"
)

// Phase identifies one evaluation stage in the regression loop.
type Phase string

const (
	// PhaseBaselineTrain evaluates the source prompt on the training set.
	PhaseBaselineTrain Phase = "baseline_train"
	// PhaseBaselineValidation evaluates the source prompt on the validation set.
	PhaseBaselineValidation Phase = "baseline_validation"
	// PhaseCandidateValidation evaluates the final candidate prompt on the validation set.
	PhaseCandidateValidation Phase = "candidate_validation"
)

// FailureCategory identifies the most likely root cause for one failed case.
type FailureCategory string

const (
	// FailureFinalResponseMismatch means the final answer does not match expectations.
	FailureFinalResponseMismatch FailureCategory = "final_response_mismatch"
	// FailureToolCallError means the agent called a wrong, missing, or unexpected tool.
	FailureToolCallError FailureCategory = "tool_call_error"
	// FailureToolArgumentError means the tool was right but arguments or results diverged.
	FailureToolArgumentError FailureCategory = "tool_argument_error"
	// FailureRouteError means a router/sub-agent route was likely wrong.
	FailureRouteError FailureCategory = "route_error"
	// FailureFormatError means output shape or serialization failed.
	FailureFormatError FailureCategory = "format_error"
	// FailureKnowledgeRecallGap means required knowledge was missing or hallucinated.
	FailureKnowledgeRecallGap FailureCategory = "knowledge_recall_gap"
	// FailureRubricFailure means an LLM rubric rejected quality without a narrower category.
	FailureRubricFailure FailureCategory = "rubric_failure"
	// FailureInferenceError means inference or trace execution itself failed.
	FailureInferenceError FailureCategory = "inference_error"
	// FailureUnknown is a fallback for failures with insufficient signal.
	FailureUnknown FailureCategory = "unknown_failure"
)

// CaseAttribution stores one explainable failure attribution.
type CaseAttribution struct {
	EvalSetID           string            `json:"evalSetId"`
	EvalCaseID          string            `json:"evalCaseId"`
	MetricName          string            `json:"metricName"`
	Category            FailureCategory   `json:"category"`
	SecondaryCategories []FailureCategory `json:"secondaryCategories,omitempty"`
	Severity            string            `json:"severity,omitempty"`
	Method              string            `json:"method,omitempty"`
	Confidence          float64           `json:"confidence,omitempty"`
	Reason              string            `json:"reason"`
	Evidence            []string          `json:"evidence,omitempty"`
}

// AttributionSummary aggregates failure categories across a report.
type AttributionSummary struct {
	Total               int                          `json:"total"`
	ByCategory          map[FailureCategory]int      `json:"byCategory"`
	BySecondaryCategory map[FailureCategory]int      `json:"bySecondaryCategory,omitempty"`
	ByMetric            map[string]int               `json:"byMetric"`
	ByCase              map[string][]FailureCategory `json:"byCase"`
}

// DeltaKind classifies one baseline-to-candidate movement.
type DeltaKind string

const (
	// DeltaNewlyPassed means a previously failing metric now passes.
	DeltaNewlyPassed DeltaKind = "newly_passed"
	// DeltaNewlyFailed means a previously passing metric now fails.
	DeltaNewlyFailed DeltaKind = "newly_failed"
	// DeltaScoreUp means pass/fail did not change but score improved.
	DeltaScoreUp DeltaKind = "score_up"
	// DeltaScoreDown means pass/fail did not change but score declined.
	DeltaScoreDown DeltaKind = "score_down"
	// DeltaUnchanged means score and status are effectively unchanged.
	DeltaUnchanged DeltaKind = "unchanged"
)

// CaseDelta stores one metric-level movement for a case.
type CaseDelta struct {
	EvalSetID       string    `json:"evalSetId"`
	EvalCaseID      string    `json:"evalCaseId"`
	MetricName      string    `json:"metricName"`
	BaselineScore   float64   `json:"baselineScore"`
	CandidateScore  float64   `json:"candidateScore"`
	BaselineStatus  string    `json:"baselineStatus"`
	CandidateStatus string    `json:"candidateStatus"`
	ScoreDelta      float64   `json:"scoreDelta"`
	Kind            DeltaKind `json:"kind"`
	Critical        bool      `json:"critical,omitempty"`
}

// DeltaSummary stores movement counters.
type DeltaSummary struct {
	NewlyPassed int `json:"newlyPassed"`
	NewlyFailed int `json:"newlyFailed"`
	ScoreUp     int `json:"scoreUp"`
	ScoreDown   int `json:"scoreDown"`
	Unchanged   int `json:"unchanged"`
}

// DeltaReport stores all case deltas for one validation comparison.
type DeltaReport struct {
	OverallScoreDelta float64      `json:"overallScoreDelta"`
	Summary           DeltaSummary `json:"summary"`
	Cases             []CaseDelta  `json:"cases"`
}

// GateConfig controls the production release decision.
type GateConfig struct {
	MinValidationScoreGain float64  `json:"minValidationScoreGain"`
	AllowNewHardFail       bool     `json:"allowNewHardFail"`
	RejectAnyScoreDown     bool     `json:"rejectAnyScoreDown,omitempty"`
	CriticalCaseIDs        []string `json:"criticalCaseIds,omitempty"`
	HardFailMetricNames    []string `json:"hardFailMetricNames,omitempty"`
	MaxModelCalls          int      `json:"maxModelCalls,omitempty"`
	MaxCost                float64  `json:"maxCost,omitempty"`
	MaxLatency             Duration `json:"maxLatency,omitempty"`
	RequireEngineAccepted  bool     `json:"requireEngineAccepted"`
}

// GateDecision stores the final release gate outcome.
type GateDecision struct {
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons"`
}

// Duration wraps time.Duration with JSON text representation.
type Duration struct {
	time.Duration
}

const (
	// CostSourceModelCallEstimate marks the built-in model-call estimate.
	CostSourceModelCallEstimate = "model_call_estimate"
	// CostSourceProvider marks cost reported by a caller-supplied CostProvider.
	CostSourceProvider = "cost_provider"
)

// CostSummary captures execution budget and cost provenance.
type CostSummary struct {
	ModelCalls int     `json:"modelCalls"`
	Tokens     int     `json:"tokens,omitempty"`
	Amount     float64 `json:"amount,omitempty"`
	Currency   string  `json:"currency,omitempty"`
	Estimated  bool    `json:"estimated,omitempty"`
	Source     string  `json:"source,omitempty"`
}

// RunMetadata stores audit metadata.
type RunMetadata struct {
	AppName          string                     `json:"appName"`
	StartedAt        time.Time                  `json:"startedAt"`
	FinishedAt       time.Time                  `json:"finishedAt"`
	Duration         Duration                   `json:"duration"`
	Seed             int64                      `json:"seed"`
	PromptSource     string                     `json:"promptSource"`
	MetricsPath      string                     `json:"metricsPath"`
	MetricNames      []string                   `json:"metricNames,omitempty"`
	TrainEvalSetID   string                     `json:"trainEvalSetId"`
	ValidationSetID  string                     `json:"validationEvalSetId"`
	Scenario         string                     `json:"scenario,omitempty"`
	TargetSurfaces   []string                   `json:"targetSurfaces,omitempty"`
	ModelConfig      map[string]string          `json:"modelConfig,omitempty"`
	FakeConfig       map[string]string          `json:"fakeConfig,omitempty"`
	AttributionHints map[string]FailureCategory `json:"attributionHints,omitempty"`
}

// MetricDefinition is the subset of metrics.json needed by this loop.
type MetricDefinition struct {
	MetricName      string                     `json:"metricName"`
	EvaluatorName   string                     `json:"evaluatorName,omitempty"`
	Threshold       float64                    `json:"threshold,omitempty"`
	FailureCategory FailureCategory            `json:"failureCategory,omitempty"`
	Criterion       map[string]json.RawMessage `json:"criterion,omitempty"`
}

// AttributionConfig controls deterministic failure attribution.
type AttributionConfig struct {
	MetricCategoryHints map[string]FailureCategory `json:"metricCategoryHints,omitempty"`
}

// EvaluationReport is the JSON-friendly form of a PromptIter evaluation result.
type EvaluationReport struct {
	OverallScore float64         `json:"overallScore"`
	EvalSets     []EvalSetReport `json:"evalSets,omitempty"`
}

// EvalSetReport stores one eval set in a JSON-friendly report shape.
type EvalSetReport struct {
	EvalSetID    string       `json:"evalSetId"`
	OverallScore float64      `json:"overallScore"`
	Cases        []CaseReport `json:"cases,omitempty"`
}

// CaseReport stores one evaluated case in a JSON-friendly report shape.
type CaseReport struct {
	EvalSetID  string         `json:"evalSetId"`
	EvalCaseID string         `json:"evalCaseId"`
	SessionID  string         `json:"sessionId,omitempty"`
	Trace      *TraceReport   `json:"trace,omitempty"`
	Metrics    []MetricReport `json:"metrics,omitempty"`
}

// MetricReport stores one metric result in a JSON-friendly report shape.
type MetricReport struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason,omitempty"`
}

// TraceReport stores the trace evidence needed by optimization audit reports.
type TraceReport struct {
	RootAgentName    string       `json:"rootAgentName,omitempty"`
	RootInvocationID string       `json:"rootInvocationId,omitempty"`
	SessionID        string       `json:"sessionId,omitempty"`
	Status           string       `json:"status,omitempty"`
	Steps            []StepReport `json:"steps,omitempty"`
}

// StepReport stores one execution-trace step in camelCase JSON.
type StepReport struct {
	StepID             string          `json:"stepId,omitempty"`
	InvocationID       string          `json:"invocationId,omitempty"`
	ParentInvocationID string          `json:"parentInvocationId,omitempty"`
	AgentName          string          `json:"agentName,omitempty"`
	Branch             string          `json:"branch,omitempty"`
	NodeID             string          `json:"nodeId,omitempty"`
	PredecessorStepIDs []string        `json:"predecessorStepIds,omitempty"`
	AppliedSurfaceIDs  []string        `json:"appliedSurfaceIds,omitempty"`
	Input              *SnapshotReport `json:"input,omitempty"`
	Output             *SnapshotReport `json:"output,omitempty"`
	Error              string          `json:"error,omitempty"`
}

// SnapshotReport stores a text snapshot in camelCase JSON.
type SnapshotReport struct {
	Text string `json:"text,omitempty"`
}

// RoundAudit stores a compact PromptIter round audit.
type RoundAudit struct {
	Round           int               `json:"round"`
	TrainScore      float64           `json:"trainScore"`
	ValidationScore float64           `json:"validationScore"`
	Accepted        bool              `json:"accepted"`
	Reason          string            `json:"reason,omitempty"`
	Patches         []PatchAudit      `json:"patches,omitempty"`
	Delta           *DeltaReport      `json:"delta,omitempty"`
	Validation      *EvaluationReport `json:"validation,omitempty"`
}

// PatchAudit stores one prompt patch for audit.
type PatchAudit struct {
	SurfaceID string `json:"surfaceId"`
	Value     string `json:"value,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// OptimizationReport is the JSON report written by the loop.
type OptimizationReport struct {
	Metadata                           RunMetadata        `json:"metadata"`
	BaselineTrain                      *EvaluationReport  `json:"baselineTrain,omitempty"`
	BaselineValidation                 *EvaluationReport  `json:"baselineValidation,omitempty"`
	AcceptedValidation                 *EvaluationReport  `json:"acceptedValidation,omitempty"`
	CandidateValidation                *EvaluationReport  `json:"candidateValidation,omitempty"`
	Rounds                             []RoundAudit       `json:"rounds,omitempty"`
	Delta                              DeltaReport        `json:"delta"`
	GateDecision                       GateDecision       `json:"gateDecision"`
	BaselineFailureAttributions        []CaseAttribution  `json:"baselineFailureAttributions,omitempty"`
	BaselineFailureAttributionSummary  AttributionSummary `json:"baselineFailureAttributionSummary"`
	CandidateFailureAttributions       []CaseAttribution  `json:"candidateFailureAttributions,omitempty"`
	CandidateFailureAttributionSummary AttributionSummary `json:"candidateFailureAttributionSummary"`
	FailureAttributions                []CaseAttribution  `json:"failureAttributions,omitempty"`
	FailureAttributionSummary          AttributionSummary `json:"failureAttributionSummary"`
	Cost                               CostSummary        `json:"cost"`
	Latency                            Duration           `json:"latency"`
	CandidatePrompt                    string             `json:"candidatePrompt,omitempty"`
}
