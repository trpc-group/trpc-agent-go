//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// DeltaKind classifies one baseline-to-candidate score change.
type DeltaKind string

const (
	// DeltaNewPass means a failing baseline item now passes.
	DeltaNewPass DeltaKind = "new_pass"
	// DeltaNewFail means a passing baseline item now fails.
	DeltaNewFail DeltaKind = "new_fail"
	// DeltaImproved means score increased without changing pass state.
	DeltaImproved DeltaKind = "improved"
	// DeltaDeclined means score decreased without changing pass state.
	DeltaDeclined DeltaKind = "declined"
	// DeltaUnchanged means score and pass state did not change.
	DeltaUnchanged DeltaKind = "unchanged"
)

// AttributionCategory is a stable failure taxonomy.
type AttributionCategory string

const (
	// CategoryFinalResponseMismatch identifies response-content failures.
	CategoryFinalResponseMismatch AttributionCategory = "final_response_mismatch"
	// CategoryToolCallError identifies wrong or failed tool calls.
	CategoryToolCallError AttributionCategory = "tool_call_error"
	// CategoryToolParameterError identifies invalid tool arguments.
	CategoryToolParameterError AttributionCategory = "tool_parameter_error"
	// CategoryRouteError identifies wrong agent or route selection.
	CategoryRouteError AttributionCategory = "route_error"
	// CategoryFormatError identifies structured output or format failures.
	CategoryFormatError AttributionCategory = "format_error"
	// CategoryKnowledgeRecall identifies insufficient retrieval or knowledge recall.
	CategoryKnowledgeRecall AttributionCategory = "knowledge_recall_insufficient"
	// CategoryExecutionError identifies trace or runtime failures.
	CategoryExecutionError AttributionCategory = "execution_error"
	// CategoryMetricFailure is the explainable fallback category.
	CategoryMetricFailure AttributionCategory = "metric_failure"
)

// MetricKind describes criterion evidence used for attribution.
type MetricKind string

const (
	// MetricKindUnknown indicates no typed criterion metadata.
	MetricKindUnknown MetricKind = "unknown"
	// MetricKindFinalResponse indicates plain final-response comparison.
	MetricKindFinalResponse MetricKind = "final_response"
	// MetricKindRouge indicates ROUGE comparison.
	MetricKindRouge MetricKind = "rouge"
	// MetricKindJSON indicates JSON comparison.
	MetricKindJSON MetricKind = "json"
	// MetricKindXML indicates XML comparison.
	MetricKindXML MetricKind = "xml"
	// MetricKindToolTrajectory indicates tool-trajectory comparison.
	MetricKindToolTrajectory MetricKind = "tool_trajectory"
	// MetricKindKnowledge indicates a knowledge or recall rubric.
	MetricKindKnowledge MetricKind = "knowledge"
	// MetricKindRoute indicates a route or handoff rubric.
	MetricKindRoute MetricKind = "route"
)

// RunStatus describes whether the full optimization run completed.
type RunStatus string

const (
	// RunStatusRunning means the pipeline has not finalized yet.
	RunStatusRunning RunStatus = "running"
	// RunStatusCompleted means every configured attempt completed.
	RunStatusCompleted RunStatus = "completed"
	// RunStatusFailed means the pipeline stopped before all attempts completed.
	RunStatusFailed RunStatus = "failed"
)

// UsageSummary records reliable inference and stage-call usage.
type UsageSummary struct {
	MonetaryCostAvailable bool          `json:"monetaryCostAvailable"`
	MonetaryCost          float64       `json:"monetaryCost"`
	PromptTokens          int           `json:"promptTokens"`
	CompletionTokens      int           `json:"completionTokens"`
	TotalTokens           int           `json:"totalTokens"`
	ModelCalls            int           `json:"modelCalls"`
	ToolCalls             int           `json:"toolCalls"`
	Duration              time.Duration `json:"durationNanos"`
}

// TraceStep is a compact auditable execution step.
type TraceStep struct {
	StepID   string `json:"stepId"`
	NodeID   string `json:"nodeId"`
	NodeType string `json:"nodeType"`
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

// TraceSummary is a compact auditable execution trace.
type TraceSummary struct {
	Status string       `json:"status"`
	Output string       `json:"output,omitempty"`
	Steps  []TraceStep  `json:"steps"`
	Usage  UsageSummary `json:"-"`
}

// MetricResult is one normalized metric result.
type MetricResult struct {
	Name   string            `json:"name"`
	Score  float64           `json:"score"`
	Status status.EvalStatus `json:"status"`
	Reason string            `json:"reason,omitempty"`
}

// CaseResult is one normalized case result.
type CaseResult struct {
	EvalSetID string         `json:"evalSetId"`
	CaseID    string         `json:"caseId"`
	Score     float64        `json:"score"`
	Passed    bool           `json:"passed"`
	Metrics   []MetricResult `json:"metrics"`
	Trace     TraceSummary   `json:"trace"`
}

// EvaluationResult is a stable report-facing evaluation result.
type EvaluationResult struct {
	OverallScore float64      `json:"overallScore"`
	Cases        []CaseResult `json:"cases"`
	Usage        UsageSummary `json:"usage"`
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

// CaseDelta compares one case and its metrics.
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

// DeltaSummary stores aggregate and per-case changes.
type DeltaSummary struct {
	ScoreDelta float64           `json:"scoreDelta"`
	Counts     map[DeltaKind]int `json:"counts"`
	Cases      []CaseDelta       `json:"cases"`
}

// DeltaOverview stores aggregate change without repeating per-case details.
type DeltaOverview struct {
	ScoreDelta float64           `json:"scoreDelta"`
	Counts     map[DeltaKind]int `json:"counts"`
}

// AttributionEvidence explains why a category was selected.
type AttributionEvidence struct {
	Source string `json:"source"`
	StepID string `json:"stepId,omitempty"`
	Reason string `json:"reason"`
}

// Attribution records one failed metric classification.
type Attribution struct {
	EvalSetID string                `json:"evalSetId"`
	CaseID    string                `json:"caseId"`
	Metric    string                `json:"metric"`
	Category  AttributionCategory   `json:"category"`
	Evidence  []AttributionEvidence `json:"evidence"`
}

// AttributionSummary aggregates failure categories.
type AttributionSummary struct {
	TotalFailures      int                             `json:"totalFailures"`
	AttributedFailures int                             `json:"attributedFailures"`
	FallbackFailures   int                             `json:"fallbackFailures"`
	CategoryCounts     map[AttributionCategory]int     `json:"categoryCounts"`
	CategoryPercent    map[AttributionCategory]float64 `json:"categoryPercent"`
}

// AttributionResult contains detailed and aggregate attribution.
type AttributionResult struct {
	Items   []Attribution      `json:"items"`
	Summary AttributionSummary `json:"summary"`
}

// GateConfig controls candidate acceptance.
type GateConfig struct {
	MinValidationScoreGain  float64  `json:"minValidationScoreGain"`
	RejectNewFailures       bool     `json:"rejectNewFailures"`
	CriticalCaseIDs         []string `json:"criticalCaseIds"`
	MaxCriticalScoreDrop    float64  `json:"maxCriticalScoreDrop"`
	MaxValidationTokens     int      `json:"maxValidationTokens"`
	MaxValidationModelCalls int      `json:"maxValidationModelCalls"`
	MaxValidationToolCalls  int      `json:"maxValidationToolCalls"`
}

// GateDecision records all acceptance reasons.
type GateDecision struct {
	Accepted   bool     `json:"accepted"`
	ScoreDelta float64  `json:"scoreDelta"`
	Reasons    []string `json:"reasons"`
}

// GateInput carries the three evaluation states used by the gate.
type GateInput struct {
	OriginalBaseline *EvaluationResult
	AcceptedBaseline *EvaluationResult
	Candidate        *EvaluationResult
}

// PromptRecord stores one optimized prompt artifact.
type PromptRecord struct {
	SurfaceID string `json:"surfaceId"`
	Text      string `json:"text"`
}

// PatchRecord stores one PromptIter patch proposal.
type PatchRecord struct {
	SurfaceID string `json:"surfaceId"`
	Text      string `json:"text,omitempty"`
	Reason    string `json:"reason"`
}

// RoundReport stores one complete optimization attempt.
type RoundReport struct {
	Attempt                int               `json:"attempt"`
	InputPrompt            PromptRecord      `json:"inputPrompt"`
	CandidatePrompt        PromptRecord      `json:"candidatePrompt"`
	Patches                []PatchRecord     `json:"patches"`
	Train                  *EvaluationResult `json:"train,omitempty"`
	Validation             *EvaluationResult `json:"validation,omitempty"`
	Delta                  *DeltaSummary     `json:"delta,omitempty"`
	Attribution            AttributionResult `json:"attribution"`
	RegressionGateDecision GateDecision      `json:"regressionGateDecision"`
	Usage                  UsageSummary      `json:"costLatency"`
	Duration               time.Duration     `json:"durationNanos"`
}

// RunMetadata records reproducibility inputs.
type RunMetadata struct {
	Seed         int64         `json:"seed"`
	Mode         string        `json:"mode"`
	Model        string        `json:"model"`
	Status       RunStatus     `json:"status"`
	Error        string        `json:"error,omitempty"`
	StartedAt    time.Time     `json:"startedAt"`
	Duration     time.Duration `json:"durationNanos"`
	ConfigPath   string        `json:"configPath"`
	ConfigSHA256 string        `json:"configSha256"`
	FakeEngine   string        `json:"fakeEngine"`
}

// Report is the versioned optimization audit artifact.
type Report struct {
	SchemaVersion       string            `json:"schemaVersion"`
	Run                 RunMetadata       `json:"run"`
	BaselineTrain       *EvaluationResult `json:"baselineTrain"`
	BaselineValidation  *EvaluationResult `json:"baselineValidation"`
	BaselineAttribution AttributionResult `json:"baselineAttribution"`
	Rounds              []RoundReport     `json:"rounds"`
	Candidate           *PromptRecord     `json:"candidate,omitempty"`
	Delta               *DeltaOverview    `json:"delta,omitempty"`
	WritebackProfile    *PromptRecord     `json:"writebackProfile,omitempty"`
	ShouldWriteBack     bool              `json:"shouldWriteBack"`
	Decision            GateDecision      `json:"gateDecision"`
	Usage               UsageSummary      `json:"costLatency"`
}
