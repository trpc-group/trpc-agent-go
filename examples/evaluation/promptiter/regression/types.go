//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "time"

const (
	metricFinalResponse  = "final_response_avg_score"
	metricToolTrajectory = "tool_trajectory_avg_score"
)

type failureCategory string

const (
	failureFinalResponse   failureCategory = "final_response_mismatch"
	failureToolCall        failureCategory = "tool_call_error"
	failureToolArgument    failureCategory = "tool_argument_error"
	failureRoute           failureCategory = "route_error"
	failureFormat          failureCategory = "format_error"
	failureKnowledgeRecall failureCategory = "knowledge_recall_insufficient"
	failureUnknown         failureCategory = "unknown_failure"
)

type failureAttribution struct {
	Category failureCategory `json:"category"`
	Evidence string          `json:"evidence"`
}

type metricEvaluation struct {
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	Threshold float64 `json:"threshold"`
	Passed    bool    `json:"passed"`
	Reason    string  `json:"reason,omitempty"`
}

type toolAudit struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
	Result    any    `json:"result,omitempty"`
}

type traceStepAudit struct {
	StepID            string   `json:"stepId,omitempty"`
	NodeID            string   `json:"nodeId,omitempty"`
	NodeType          string   `json:"nodeType,omitempty"`
	AppliedSurfaceIDs []string `json:"appliedSurfaceIds,omitempty"`
	Error             string   `json:"error,omitempty"`
}

type traceAudit struct {
	Status string           `json:"status,omitempty"`
	Steps  []traceStepAudit `json:"steps,omitempty"`
}

type costSummary struct {
	ModelCalls       int     `json:"modelCalls"`
	ToolCalls        int     `json:"toolCalls"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	TotalTokens      int     `json:"totalTokens"`
	EstimatedCostUSD float64 `json:"estimatedCostUsd"`
}

type caseEvaluation struct {
	CaseID                 string               `json:"caseId"`
	Score                  float64              `json:"score"`
	Passed                 bool                 `json:"passed"`
	FinalResponse          string               `json:"finalResponse,omitempty"`
	ExpectedResponse       string               `json:"expectedResponse,omitempty"`
	ToolTrajectory         []toolAudit          `json:"toolTrajectory,omitempty"`
	ExpectedToolTrajectory []toolAudit          `json:"expectedToolTrajectory,omitempty"`
	Metrics                []metricEvaluation   `json:"metrics"`
	FailureAttributions    []failureAttribution `json:"failureAttributions,omitempty"`
	Trace                  traceAudit           `json:"trace"`
	Cost                   costSummary          `json:"cost"`
	LatencyMillis          int64                `json:"latencyMillis"`
}

type evaluationSummary struct {
	EvalSetID     string           `json:"evalSetId"`
	Score         float64          `json:"score"`
	PassedCases   int              `json:"passedCases"`
	FailedCases   int              `json:"failedCases"`
	Cases         []caseEvaluation `json:"cases"`
	Cost          costSummary      `json:"cost"`
	LatencyMillis int64            `json:"latencyMillis"`
}

type caseDeltaClass string

const (
	caseNewlyPassed caseDeltaClass = "newly_passed"
	caseNewlyFailed caseDeltaClass = "newly_failed"
	caseImproved    caseDeltaClass = "improved"
	caseRegressed   caseDeltaClass = "regressed"
	caseUnchanged   caseDeltaClass = "unchanged"
)

type caseDelta struct {
	CaseID          string         `json:"caseId"`
	BaselineScore   float64        `json:"baselineScore"`
	CandidateScore  float64        `json:"candidateScore"`
	ScoreDelta      float64        `json:"scoreDelta"`
	BaselinePassed  bool           `json:"baselinePassed"`
	CandidatePassed bool           `json:"candidatePassed"`
	Class           caseDeltaClass `json:"class"`
}

type evaluationDelta struct {
	ScoreDelta  float64     `json:"scoreDelta"`
	NewlyPassed int         `json:"newlyPassed"`
	NewlyFailed int         `json:"newlyFailed"`
	Improved    int         `json:"improved"`
	Regressed   int         `json:"regressed"`
	Unchanged   int         `json:"unchanged"`
	Cases       []caseDelta `json:"cases"`
}

type gateConfig struct {
	MinValidationScoreGain float64  `json:"minValidationScoreGain"`
	AllowNewHardFails      bool     `json:"allowNewHardFails"`
	CriticalCaseIDs        []string `json:"criticalCaseIds"`
	MaxCriticalScoreDrop   float64  `json:"maxCriticalScoreDrop"`
	MaxEstimatedCostUSD    float64  `json:"maxEstimatedCostUsd"`
	MaxToolCalls           int      `json:"maxToolCalls"`
}

type gateCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type gateDecision struct {
	Accepted bool        `json:"accepted"`
	Reasons  []string    `json:"reasons"`
	Checks   []gateCheck `json:"checks"`
}

type promptEvaluation struct {
	Round       int               `json:"round,omitempty"`
	CandidateID string            `json:"candidateId,omitempty"`
	Prompt      string            `json:"prompt"`
	Train       evaluationSummary `json:"train"`
	Validation  evaluationSummary `json:"validation"`
}

type roundAudit struct {
	Round          int               `json:"round"`
	CandidateID    string            `json:"candidateId"`
	Prompt         string            `json:"prompt"`
	PatchReason    string            `json:"patchReason"`
	Train          evaluationSummary `json:"train"`
	Validation     evaluationSummary `json:"validation"`
	Delta          evaluationDelta   `json:"delta"`
	Decision       gateDecision      `json:"decision"`
	DurationMillis int64             `json:"durationMillis"`
}

type attributionSummary struct {
	Baseline  map[failureCategory]int `json:"baseline"`
	Candidate map[failureCategory]int `json:"candidate"`
}

type costLatencySummary struct {
	Baseline           costSummary `json:"baseline"`
	Candidates         costSummary `json:"candidates"`
	Total              costSummary `json:"total"`
	TotalLatencyMillis int64       `json:"totalLatencyMillis"`
}

type inputAudit struct {
	TrainEvalSet      string `json:"trainEvalSet"`
	ValidationEvalSet string `json:"validationEvalSet"`
	Metrics           string `json:"metrics"`
	PromptSource      string `json:"promptSource"`
	Config            string `json:"config"`
}

type runtimeAudit struct {
	Seed           int64           `json:"seed"`
	Engine         string          `json:"engine"`
	Model          fakeModelConfig `json:"model"`
	StartedAt      time.Time       `json:"startedAt"`
	DurationMillis int64           `json:"durationMillis"`
}

type configurationAudit struct {
	TargetSurfaceID string     `json:"targetSurfaceId"`
	MaxRounds       int        `json:"maxRounds"`
	Gate            gateConfig `json:"gate"`
}

type optimizationReport struct {
	SchemaVersion      string             `json:"schemaVersion"`
	RunID              string             `json:"runId"`
	Inputs             inputAudit         `json:"inputs"`
	Configuration      configurationAudit `json:"configuration"`
	Runtime            runtimeAudit       `json:"runtime"`
	Baseline           promptEvaluation   `json:"baseline"`
	Candidate          promptEvaluation   `json:"candidate"`
	Delta              evaluationDelta    `json:"delta"`
	GateDecision       gateDecision       `json:"gateDecision"`
	FailureAttribution attributionSummary `json:"failureAttribution"`
	CostLatency        costLatencySummary `json:"costLatency"`
	Rounds             []roundAudit       `json:"rounds"`
}

type attributionInput struct {
	metrics          []metricEvaluation
	actualResponse   string
	expectedResponse string
	actualTools      []toolAudit
	expectedTools    []toolAudit
	trace            traceAudit
}
