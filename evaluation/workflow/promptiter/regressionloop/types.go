// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type AttributionCategory string

const (
	AttributionRouteError         AttributionCategory = "route_error"
	AttributionToolCallError      AttributionCategory = "tool_call_error"
	AttributionToolArgumentError  AttributionCategory = "tool_argument_error"
	AttributionFormatError        AttributionCategory = "format_error"
	AttributionKnowledgeRecallGap AttributionCategory = "knowledge_recall_gap"
	AttributionResponseMismatch   AttributionCategory = "response_mismatch"
)

type DeltaType string

const (
	DeltaNewlyPassed DeltaType = "newlyPassed"
	DeltaNewlyFailed DeltaType = "newlyFailed"
	DeltaScoreUp     DeltaType = "scoreUp"
	DeltaScoreDown   DeltaType = "scoreDown"
	DeltaUnchanged   DeltaType = "unchanged"
	DeltaMissing     DeltaType = "missing"
)

type GateRuleType string

const (
	GateRuleValidationGainThreshold     GateRuleType = "validation_gain_threshold"
	GateRuleNewHardFailLimit            GateRuleType = "new_hard_fail_limit"
	GateRuleCriticalRegressionDetection GateRuleType = "critical_regression_detection"
	GateRuleProtectedCases              GateRuleType = "protected_cases"
	GateRuleResourceBudget              GateRuleType = "resource_budget"
	GateRuleCostValidation              GateRuleType = "cost_validation"
	GateRuleLatencyBudget               GateRuleType = "latency_budget"
)

type GateResultType string

const (
	GateResultAccept GateResultType = "accept"
	GateResultReject GateResultType = "reject"
)

type InvocationEvidence struct {
	ActualToolName    string                 `json:"actualToolName,omitempty"`
	ExpectedToolName  string                 `json:"expectedToolName,omitempty"`
	ActualArguments   map[string]interface{} `json:"actualArguments,omitempty"`
	ExpectedArguments map[string]interface{} `json:"expectedArguments,omitempty"`
	ToolCallPresent   bool                   `json:"toolCallPresent"`
	ExpectedToolCall  bool                   `json:"expectedToolCall"`
}

type AttributionResult struct {
	EvalCaseID       string                  `json:"evalCaseId"`
	MetricName       string                  `json:"metricName"`
	Category         AttributionCategory     `json:"category"`
	Reason           string                  `json:"reason"`
	Evidence         *InvocationEvidence     `json:"evidence,omitempty"`
	DerivedFrom      []string                `json:"derivedFrom,omitempty"`
	LossHintSeverity promptiter.LossSeverity `json:"lossHintSeverity,omitempty"`
}

type CaseDelta struct {
	EvalCaseID        string             `json:"evalCaseId"`
	EvalSetID         string             `json:"evalSetId"`
	BaselineScore     float64            `json:"baselineScore"`
	CandidateScore    float64            `json:"candidateScore"`
	ScoreDelta        float64            `json:"scoreDelta"`
	BaselinePassed    bool               `json:"baselinePassed"`
	CandidatePassed   bool               `json:"candidatePassed"`
	DeltaType         DeltaType          `json:"deltaType"`
	AttributionResult *AttributionResult `json:"attributionResult,omitempty"`
}

type GateRuleResult struct {
	RuleType    GateRuleType `json:"ruleType"`
	Passed      bool         `json:"passed"`
	Reason      string       `json:"reason"`
	Threshold   float64      `json:"threshold,omitempty"`
	ActualValue float64      `json:"actualValue,omitempty"`
}

type GateDecision struct {
	Result            GateResultType   `json:"result"`
	Stage             string           `json:"stage"`
	RuleResults       []GateRuleResult `json:"ruleResults"`
	RejectionReasons  []string         `json:"rejectionReasons,omitempty"`
	AcceptanceReasons []string         `json:"acceptanceReasons,omitempty"`
	ScoreDelta        float64          `json:"scoreDelta"`
	BaselineScore     float64          `json:"baselineScore"`
	CandidateScore    float64          `json:"candidateScore"`
}

type AttributionRule struct {
	Name     string              `json:"name"`
	Category AttributionCategory `json:"category"`
	Patterns []string            `json:"patterns"`
	Priority int                 `json:"priority"`
}

type GateConfig struct {
	MinValidationGain   float64  `json:"minValidationGain"`
	AllowNewHardFail    bool     `json:"allowNewHardFail"`
	MaxNewHardFailCount int      `json:"maxNewHardFailCount"`
	MaxRegressedCases   int      `json:"maxRegressedCases"`
	ProtectedCaseIDs    []string `json:"protectedCaseIds"`
	CriticalCaseIDs     []string `json:"criticalCaseIds"`
	MaxCost             float64  `json:"maxCost"`
	MaxCalls            int      `json:"maxCalls"`
	MaxLatencyMS        int      `json:"maxLatencyMs"`
}

type OptimizationConfig struct {
	MaxRounds        int      `json:"maxRounds"`
	TargetSurfaceIDs []string `json:"targetSurfaceIds"`
	MinScoreGain     float64  `json:"minScoreGain"`
	CaseParallelism  int      `json:"caseParallelism"`
}

type OutputConfig struct {
	OutputDir           string `json:"outputDir"`
	SaveAuditTrail      bool   `json:"saveAuditTrail"`
	SaveCandidatePrompt bool   `json:"saveCandidatePrompt"`
}

type Config struct {
	TrainEvalSetPath      string             `json:"trainEvalSetPath"`
	ValidationEvalSetPath string             `json:"validationEvalSetPath"`
	MetricsPath           string             `json:"metricsPath"`
	BaselinePromptPath    string             `json:"baselinePromptPath"`
	PromptiterConfigPath  string             `json:"promptiterConfigPath"`
	Seed                  int64              `json:"seed"`
	Mode                  string             `json:"mode"`
	Gate                  GateConfig         `json:"gate"`
	Optimization          OptimizationConfig `json:"optimization"`
	Output                OutputConfig       `json:"output"`
	AttributionRules      []AttributionRule  `json:"attributionRules,omitempty"`
}

type RunMeta struct {
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	DurationMS int64     `json:"durationMs"`
	Mode       string    `json:"mode"`
	Seed       int64     `json:"seed"`
	ConfigHash string    `json:"configHash"`
}

type CandidateInfo struct {
	Round           int                 `json:"round"`
	Profile         *promptiter.Profile `json:"profile"`
	ValidationScore float64             `json:"validationScore"`
	Accepted        bool                `json:"accepted"`
}

type OptimizationReport struct {
	RunMeta             RunMeta         `json:"runMeta"`
	BaselineTrainScore  float64         `json:"baselineTrainScore"`
	BaselineValScore    float64         `json:"baselineValScore"`
	CandidateTrainScore float64         `json:"candidateTrainScore"`
	CandidateValScore   float64         `json:"candidateValScore"`
	ScoreDeltaTrain     float64         `json:"scoreDeltaTrain"`
	ScoreDeltaVal       float64         `json:"scoreDeltaVal"`
	GateDecision        GateDecision    `json:"gateDecision"`
	CaseDeltas          []CaseDelta     `json:"caseDeltas"`
	AttributionSummary  map[string]int  `json:"attributionSummary"`
	Candidates          []CandidateInfo `json:"candidates"`
	TotalCost           float64         `json:"totalCost"`
	TotalCalls          int             `json:"totalCalls"`
	TotalLatencyMS      int64           `json:"totalLatencyMs"`
}

type PipelineContext struct {
	Config         *Config
	BaselineTrain  *engine.EvaluationResult
	BaselineVal    *engine.EvaluationResult
	CandidateTrain *engine.EvaluationResult
	CandidateVal   *engine.EvaluationResult
	Attributions   []AttributionResult
	CaseDeltas     []CaseDelta
	GateDecision   *GateDecision
	Candidates     []CandidateInfo
	TotalCost      float64
	TotalCalls     int
	TotalLatencyMS int64
}
