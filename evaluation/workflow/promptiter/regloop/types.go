//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regloop turns one PromptIter engine RunResult into a structured
// Evaluation + Optimization audit: failure attribution, baseline-vs-candidate
// per-case delta, a configurable release gate, and a machine- and human-readable
// optimization report. All functions are pure over the engine result types, so
// they need no model or API key and are unit-testable with hand-built fixtures.
package regloop

// DeltaKind classifies how one metric moved from baseline to candidate.
type DeltaKind string

const (
	// DeltaNewlyPassed marks a metric that failed at baseline and passes for the candidate.
	DeltaNewlyPassed DeltaKind = "NewlyPassed"
	// DeltaNewlyFailed marks a metric that passed at baseline and fails for the candidate.
	DeltaNewlyFailed DeltaKind = "NewlyFailed"
	// DeltaScoreUp marks a metric whose score improved without a status flip.
	DeltaScoreUp DeltaKind = "ScoreUp"
	// DeltaScoreDown marks a metric whose score regressed without a status flip.
	DeltaScoreDown DeltaKind = "ScoreDown"
	// DeltaUnchanged marks a metric with no meaningful score or status change.
	DeltaUnchanged DeltaKind = "Unchanged"
)

// FailureCategory groups one failed metric by its likely root cause.
type FailureCategory string

const (
	// CategoryResponseMismatch marks a final-response / ROUGE style content mismatch.
	CategoryResponseMismatch FailureCategory = "responseMismatch"
	// CategoryFormatError marks a format / structure compliance failure.
	CategoryFormatError FailureCategory = "formatError"
	// CategoryToolError marks a tool-trajectory failure (wrong or missing tool call).
	CategoryToolError FailureCategory = "toolError"
	// CategoryToolArgError marks a tool call with wrong arguments.
	CategoryToolArgError FailureCategory = "toolArgError"
	// CategoryRouteError marks a routing / wrong-agent failure.
	CategoryRouteError FailureCategory = "routeError"
	// CategoryKnowledgeRecall marks insufficient knowledge recall / grounding.
	CategoryKnowledgeRecall FailureCategory = "knowledgeRecall"
	// CategoryOther is the fallback for unclassified failures.
	CategoryOther FailureCategory = "other"
)

// Report is the full optimization audit, serialized to optimization_report.json.
type Report struct {
	App                string            `json:"app"`
	Mode               string            `json:"mode"`
	Status             string            `json:"status"`
	Baseline           PhaseScore        `json:"baseline"`
	Candidate          CandidateScore    `json:"candidate"`
	Delta              DeltaReport       `json:"delta"`
	FailureAttribution AttributionReport `json:"failureAttribution"`
	Gate               GateResult        `json:"gate"`
	Cost               CostReport        `json:"cost"`
	Rounds             []RoundReport     `json:"rounds"`
	Config             map[string]any    `json:"config,omitempty"`
}

// PhaseScore captures scores for one evaluation phase (baseline or candidate).
type PhaseScore struct {
	OverallScore float64        `json:"overallScore"`
	EvalSets     []EvalSetScore `json:"evalSets"`
}

// EvalSetScore captures one evaluation set's aggregate and per-case scores.
type EvalSetScore struct {
	EvalSetID    string      `json:"evalSetId"`
	OverallScore float64     `json:"overallScore"`
	Cases        []CaseScore `json:"cases"`
}

// CaseScore captures one case's metric scores.
type CaseScore struct {
	EvalCaseID string        `json:"evalCaseId"`
	Metrics    []MetricScore `json:"metrics"`
}

// MetricScore captures one metric measurement.
type MetricScore struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason,omitempty"`
}

// CandidateScore summarizes the accepted candidate.
type CandidateScore struct {
	OverallScore    float64 `json:"overallScore"`
	ProfileAccepted bool    `json:"profileAccepted"`
	AcceptedRound   int     `json:"acceptedRound,omitempty"`
}

// DeltaReport holds per-case deltas and their summary counts.
type DeltaReport struct {
	CaseDeltas []CaseDelta  `json:"caseDeltas"`
	Summary    DeltaSummary `json:"summary"`
}

// CaseDelta describes one metric's movement from baseline to candidate.
type CaseDelta struct {
	EvalSetID       string    `json:"evalSetId"`
	EvalCaseID      string    `json:"evalCaseId"`
	MetricName      string    `json:"metricName"`
	BaselineScore   float64   `json:"baselineScore"`
	CandidateScore  float64   `json:"candidateScore"`
	BaselineStatus  string    `json:"baselineStatus"`
	CandidateStatus string    `json:"candidateStatus"`
	Kind            DeltaKind `json:"kind"`
}

// DeltaSummary counts case deltas by kind.
type DeltaSummary struct {
	NewlyPassed int `json:"newlyPassed"`
	NewlyFailed int `json:"newlyFailed"`
	ScoreUp     int `json:"scoreUp"`
	ScoreDown   int `json:"scoreDown"`
	Unchanged   int `json:"unchanged"`
}

// AttributionReport summarizes baseline failures by category and severity.
type AttributionReport struct {
	Baseline   map[FailureCategory]int `json:"baseline"`
	BySeverity map[string]int          `json:"bySeverity"`
	Details    []FailureDetail         `json:"details,omitempty"`
}

// FailureDetail is one classified failed metric.
type FailureDetail struct {
	EvalSetID  string          `json:"evalSetId"`
	EvalCaseID string          `json:"evalCaseId"`
	MetricName string          `json:"metricName"`
	Category   FailureCategory `json:"category"`
	Reason     string          `json:"reason,omitempty"`
}

// GateResult is the release decision and its reasons.
type GateResult struct {
	Released bool     `json:"released"`
	Reasons  []string `json:"reasons"`
}

// CostReport summarizes run cost. Values are estimated from observable counts
// because the engine result carries no token accounting.
type CostReport struct {
	Rounds       int    `json:"rounds"`
	TeacherCalls int    `json:"teacherCalls"`
	Estimated    bool   `json:"estimated"`
	Note         string `json:"note,omitempty"`
}

// RoundReport summarizes one optimization round.
type RoundReport struct {
	Round           int     `json:"round"`
	ValidationScore float64 `json:"validationScore"`
	Accepted        bool    `json:"accepted"`
	ScoreDelta      float64 `json:"scoreDelta"`
	Reason          string  `json:"reason,omitempty"`
}
