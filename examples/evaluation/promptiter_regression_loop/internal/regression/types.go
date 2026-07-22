// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regression contains the report-facing contracts for the PromptIter
// regression-loop example. It deliberately stays internal until the contract
// has proved stable enough for a public framework package.
package regression

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// DeltaKind classifies a baseline-to-candidate change.
type DeltaKind string

const (
	// DeltaNewPass means a previously failing item now passes.
	DeltaNewPass DeltaKind = "new_pass"
	// DeltaNewFail means a previously passing item now fails.
	DeltaNewFail DeltaKind = "new_fail"
	// DeltaImproved means the score increased without changing pass status.
	DeltaImproved DeltaKind = "improved"
	// DeltaDeclined means the score decreased without changing pass status.
	DeltaDeclined DeltaKind = "declined"
	// DeltaUnchanged means neither score nor pass status materially changed.
	DeltaUnchanged DeltaKind = "unchanged"
)

// FailureCategory is the stable taxonomy used by the default attribution rules.
type FailureCategory string

const (
	// FailureFinalResponse identifies a mismatch in the final response.
	FailureFinalResponse FailureCategory = "final_response_mismatch"
	// FailureToolCall identifies missing or incorrect tool selection.
	FailureToolCall FailureCategory = "tool_call_error"
	// FailureToolArgument identifies invalid tool arguments.
	FailureToolArgument FailureCategory = "tool_argument_error"
	// FailureRoute identifies an incorrect route or agent handoff.
	FailureRoute FailureCategory = "route_error"
	// FailureFormat identifies invalid structured output or formatting.
	FailureFormat FailureCategory = "format_error"
	// FailureKnowledge identifies insufficient retrieval or grounding.
	FailureKnowledge FailureCategory = "knowledge_recall_insufficient"
	// FailureExecution identifies a runner or trace execution failure.
	FailureExecution FailureCategory = "execution_error"
	// FailureMetric identifies a failed metric without stronger evidence.
	FailureMetric FailureCategory = "metric_failure"
)

// Usage records usage that can be measured from an evaluation trace.
type Usage struct {
	PromptTokens     int           `json:"promptTokens"`
	CompletionTokens int           `json:"completionTokens"`
	TotalTokens      int           `json:"totalTokens"`
	ModelCalls       int           `json:"modelCalls"`
	ToolCalls        int           `json:"toolCalls"`
	Duration         time.Duration `json:"durationNanos"`
	Measured         bool          `json:"measured"`
}

// TraceStep is a compact, report-safe execution step.
type TraceStep struct {
	StepID   string `json:"stepId"`
	NodeType string `json:"nodeType,omitempty"`
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Trace is the compact execution evidence used for attribution and auditing.
type Trace struct {
	Status string      `json:"status"`
	Output string      `json:"output,omitempty"`
	Steps  []TraceStep `json:"steps,omitempty"`
	Usage  Usage       `json:"usage"`
}

// MetricResult is one normalized metric result.
type MetricResult struct {
	Name      string            `json:"name"`
	Score     float64           `json:"score"`
	Threshold float64           `json:"threshold,omitempty"`
	Status    status.EvalStatus `json:"status"`
	Reason    string            `json:"reason,omitempty"`
}

// CaseResult is one normalized evaluation case.
type CaseResult struct {
	EvalSetID    string         `json:"evalSetId"`
	CaseID       string         `json:"caseId"`
	Score        float64        `json:"score"`
	Passed       bool           `json:"passed"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Metrics      []MetricResult `json:"metrics"`
	Trace        Trace          `json:"trace"`
}

// EvaluationResult is the stable report-facing evaluation result.
type EvaluationResult struct {
	EvalSetID     string            `json:"evalSetId"`
	OverallScore  float64           `json:"overallScore"`
	OverallStatus status.EvalStatus `json:"overallStatus"`
	ExecutionTime time.Duration     `json:"executionTimeNanos"`
	Cases         []CaseResult      `json:"cases"`
	Usage         Usage             `json:"usage"`
}

// Attribution explains one failed metric.
type Attribution struct {
	EvalSetID string          `json:"evalSetId"`
	CaseID    string          `json:"caseId"`
	Metric    string          `json:"metric"`
	Category  FailureCategory `json:"category"`
	Reason    string          `json:"reason"`
	Evidence  []string        `json:"evidence,omitempty"`
}

// AttributionSummary aggregates failure categories.
type AttributionSummary struct {
	TotalFailures      int                     `json:"totalFailures"`
	AttributedFailures int                     `json:"attributedFailures"`
	CategoryCounts     map[FailureCategory]int `json:"categoryCounts"`
}

// AttributionResult contains detailed and aggregate attribution.
type AttributionResult struct {
	Items   []Attribution      `json:"items"`
	Summary AttributionSummary `json:"summary"`
}

// MetricDelta compares one metric.
type MetricDelta struct {
	Name            string            `json:"name"`
	BaselineScore   float64           `json:"baselineScore"`
	CandidateScore  float64           `json:"candidateScore"`
	ScoreDelta      float64           `json:"scoreDelta"`
	BaselineStatus  status.EvalStatus `json:"baselineStatus"`
	CandidateStatus status.EvalStatus `json:"candidateStatus"`
	Kind            DeltaKind         `json:"kind"`
}

// CaseDelta compares one case and all of its metrics.
type CaseDelta struct {
	EvalSetID       string        `json:"evalSetId"`
	CaseID          string        `json:"caseId"`
	BaselineScore   float64       `json:"baselineScore"`
	CandidateScore  float64       `json:"candidateScore"`
	ScoreDelta      float64       `json:"scoreDelta"`
	BaselinePassed  bool          `json:"baselinePassed"`
	CandidatePassed bool          `json:"candidatePassed"`
	Kind            DeltaKind     `json:"kind"`
	Metrics         []MetricDelta `json:"metrics"`
}

// DeltaSummary stores deterministic, ordered case-level changes.
type DeltaSummary struct {
	ScoreDelta float64           `json:"scoreDelta"`
	Counts     map[DeltaKind]int `json:"counts"`
	Cases      []CaseDelta       `json:"cases"`
}

// GatePolicy controls whether a candidate may be released.
type GatePolicy struct {
	MinValidationScoreGain    float64  `json:"minValidationScoreGain"`
	RejectNewFailures         bool     `json:"rejectNewFailures"`
	RejectCriticalRegressions bool     `json:"rejectCriticalRegressions"`
	CriticalCaseIDs           []string `json:"criticalCaseIds"`
	MaxCriticalScoreDrop      float64  `json:"maxCriticalScoreDrop"`
	MaxValidationTokens       int      `json:"maxValidationTokens"`
	MaxValidationModelCalls   int      `json:"maxValidationModelCalls"`
	MaxValidationToolCalls    int      `json:"maxValidationToolCalls"`
}

// GateDecision records both detected regressions and enforced reasons.
type GateDecision struct {
	Accepted            bool     `json:"accepted"`
	ScoreDelta          float64  `json:"scoreDelta"`
	Reasons             []string `json:"reasons"`
	NewFailures         []string `json:"newFailures"`
	CriticalRegressions []string `json:"criticalRegressions"`
}

// PromptRecord identifies the candidate surface that was evaluated.
type PromptRecord struct {
	SurfaceID string `json:"surfaceId"`
	Text      string `json:"text"`
}

// PatchRecord records one PromptIter patch proposal.
type PatchRecord struct {
	SurfaceID string `json:"surfaceId"`
	Text      string `json:"text,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// RoundReport stores all reportable evidence for one optimization attempt.
type RoundReport struct {
	Attempt            int               `json:"attempt"`
	InputPrompt        PromptRecord      `json:"inputPrompt"`
	CandidatePrompt    PromptRecord      `json:"candidatePrompt"`
	PromptIterAccepted bool              `json:"promptIterAccepted"`
	Train              *EvaluationResult `json:"train"`
	Validation         *EvaluationResult `json:"validation"`
	// Delta compares the candidate with the validation result accepted before this round.
	Delta *DeltaSummary `json:"delta"`
	// BaselineDelta compares the candidate with the immutable original baseline.
	BaselineDelta *DeltaSummary     `json:"baselineDelta"`
	Attribution   AttributionResult `json:"attribution"`
	Gate          GateDecision      `json:"gate"`
	Patches       []PatchRecord     `json:"patches"`
	Usage         Usage             `json:"usage"`
	Duration      time.Duration     `json:"durationNanos"`
}

// RunMetadata records reproducibility and lifecycle data.
type RunMetadata struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	Mode         string        `json:"mode"`
	Seed         int64         `json:"seed"`
	Model        string        `json:"model"`
	ConfigPath   string        `json:"configPath"`
	ConfigSHA256 string        `json:"configSha256"`
	StartedAt    time.Time     `json:"startedAt"`
	Duration     time.Duration `json:"durationNanos"`
	Error        string        `json:"error,omitempty"`
}

// Report is the versioned optimization audit artifact.
type Report struct {
	SchemaVersion       string            `json:"schemaVersion"`
	Run                 RunMetadata       `json:"run"`
	BaselineTrain       *EvaluationResult `json:"baselineTrain"`
	BaselineValidation  *EvaluationResult `json:"baselineValidation"`
	BaselineAttribution AttributionResult `json:"baselineAttribution"`
	Rounds              []RoundReport     `json:"rounds"`
	SelectedAttempt     int               `json:"selectedAttempt"`
	SelectedCandidate   *PromptRecord     `json:"selectedCandidate,omitempty"`
	ShouldWriteBack     bool              `json:"shouldWriteBack"`
	Decision            GateDecision      `json:"decision"`
	Usage               Usage             `json:"usage"`
}
