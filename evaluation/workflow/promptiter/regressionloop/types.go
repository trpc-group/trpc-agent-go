//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regressionloop provides an auditable evaluation and prompt optimization loop.
package regressionloop

import "time"

// PromptTargetType identifies the prompt surface type under optimization.
type PromptTargetType string

const (
	// PromptTargetSystemPrompt targets system-level behavior text.
	PromptTargetSystemPrompt PromptTargetType = "system_prompt"
	// PromptTargetAgentInstruction targets an agent instruction surface.
	PromptTargetAgentInstruction PromptTargetType = "agent_instruction"
	// PromptTargetSkillDescription targets a skill description surface.
	PromptTargetSkillDescription PromptTargetType = "skill_description"
	// PromptTargetRouterPrompt targets routing instructions.
	PromptTargetRouterPrompt PromptTargetType = "router_prompt"
)

// RunnerMode identifies how evaluations and candidates are produced.
type RunnerMode string

const (
	// RunnerModeFake uses deterministic fake outcomes.
	RunnerModeFake RunnerMode = "fake"
	// RunnerModeTrace uses recorded trace outcomes.
	RunnerModeTrace RunnerMode = "trace"
	// RunnerModeModel uses live model execution.
	RunnerModeModel RunnerMode = "model"
)

// Phase identifies a pipeline evaluation phase.
type Phase string

const (
	// PhaseBaselineTrain evaluates the baseline prompt on the train set.
	PhaseBaselineTrain Phase = "baseline_train"
	// PhaseBaselineValidation evaluates the baseline prompt on the validation set.
	PhaseBaselineValidation Phase = "baseline_validation"
	// PhaseCandidateTrain evaluates a candidate prompt on the train set.
	PhaseCandidateTrain Phase = "candidate_train"
	// PhaseCandidateValidation evaluates a candidate prompt on the validation set.
	PhaseCandidateValidation Phase = "candidate_validation"
)

// AttributionCategory identifies a deterministic failure reason class.
type AttributionCategory string

const (
	// AttributionFinalResponseMismatch indicates the final answer mismatched the reference.
	AttributionFinalResponseMismatch AttributionCategory = "final_response_mismatch"
	// AttributionToolSelectionError indicates the wrong tool was called or a required tool was omitted.
	AttributionToolSelectionError AttributionCategory = "tool_selection_error"
	// AttributionToolArgumentError indicates a tool was called with incorrect arguments.
	AttributionToolArgumentError AttributionCategory = "tool_argument_error"
	// AttributionRoutingError indicates the request was routed to the wrong node/agent/skill.
	AttributionRoutingError AttributionCategory = "routing_error"
	// AttributionFormatError indicates malformed or non-compliant output format.
	AttributionFormatError AttributionCategory = "format_error"
	// AttributionKnowledgeRecallInsufficient indicates missing or stale knowledge/context.
	AttributionKnowledgeRecallInsufficient AttributionCategory = "knowledge_recall_insufficient"
	// AttributionMetricThresholdMiss indicates no specific class was found beyond metric failure.
	AttributionMetricThresholdMiss AttributionCategory = "metric_threshold_miss"
	// AttributionUnknown indicates incomplete evidence.
	AttributionUnknown AttributionCategory = "unknown"
)

// Transition identifies one validation case's baseline-to-candidate movement.
type Transition string

const (
	// TransitionNewlyPassed indicates a failed baseline became passing.
	TransitionNewlyPassed Transition = "newly_passed"
	// TransitionNewlyFailed indicates a passing baseline became failing.
	TransitionNewlyFailed Transition = "newly_failed"
	// TransitionImproved indicates score improved without pass/fail transition.
	TransitionImproved Transition = "improved"
	// TransitionRegressed indicates score regressed without pass/fail transition.
	TransitionRegressed Transition = "regressed"
	// TransitionUnchanged indicates no score or status change.
	TransitionUnchanged Transition = "unchanged"
	// TransitionMissing indicates either baseline or candidate case is missing.
	TransitionMissing Transition = "missing"
)

// PromptSource describes the prompt surface under optimization.
type PromptSource struct {
	ID           string           `json:"id"`
	Path         string           `json:"path"`
	TargetType   PromptTargetType `json:"targetType"`
	SurfaceID    string           `json:"surfaceId,omitempty"`
	BaselineText string           `json:"baselineText,omitempty"`
}

// EvalSetRef identifies one evaluation set input.
type EvalSetRef struct {
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	CaseIDs []string `json:"caseIds,omitempty"`
}

// MetricsRef identifies the metric configuration input.
type MetricsRef struct {
	Path         string `json:"path"`
	MetricFileID string `json:"metricFileId,omitempty"`
}

// PromptIterConfig configures candidate generation.
type PromptIterConfig struct {
	MaxRounds                  int      `json:"maxRounds"`
	TargetSurfaceIDs           []string `json:"targetSurfaceIds"`
	MinScoreGain               float64  `json:"minScoreGain,omitempty"`
	MaxRoundsWithoutAcceptance int      `json:"maxRoundsWithoutAcceptance,omitempty"`
	TargetScore                *float64 `json:"targetScore,omitempty"`
}

// RunnerConfig stores runner identity and deterministic settings.
type RunnerConfig struct {
	Mode           RunnerMode `json:"mode"`
	FixturePath    string     `json:"fixturePath,omitempty"`
	ModelName      string     `json:"modelName,omitempty"`
	JudgeModelName string     `json:"judgeModelName,omitempty"`
	Deterministic  bool       `json:"deterministic,omitempty"`
}

// OutputConfig configures report output paths.
type OutputConfig struct {
	Dir            string `json:"dir"`
	JSONReport     string `json:"jsonReport"`
	MarkdownReport string `json:"markdownReport"`
}

// GatePolicy configures final candidate acceptance.
type GatePolicy struct {
	MinValidationScoreGain  float64  `json:"minValidationScoreGain"`
	AllowNewHardFails       bool     `json:"allowNewHardFails"`
	BlockCriticalRegression bool     `json:"blockCriticalRegression"`
	CriticalCaseIDs         []string `json:"criticalCaseIds,omitempty"`
	MaxCost                 float64  `json:"maxCost,omitempty"`
	MaxCalls                int      `json:"maxCalls,omitempty"`
	MaxLatencyMS            int64    `json:"maxLatencyMs,omitempty"`
}

// Config is the top-level loop configuration.
type Config struct {
	AppName           string           `json:"appName"`
	Seed              int64            `json:"seed"`
	PromptSource      PromptSource     `json:"promptSource"`
	TrainEvalSet      EvalSetRef       `json:"trainEvalSet"`
	ValidationEvalSet EvalSetRef       `json:"validationEvalSet"`
	Metrics           MetricsRef       `json:"metrics"`
	PromptIter        PromptIterConfig `json:"promptiter"`
	Gate              GatePolicy       `json:"gate"`
	Runner            RunnerConfig     `json:"runner"`
	Output            OutputConfig     `json:"output"`
}

// MetricResult stores one metric measurement for an evaluated case.
type MetricResult struct {
	Name     string  `json:"name"`
	Score    float64 `json:"score"`
	Passed   bool    `json:"passed"`
	HardFail bool    `json:"hardFail,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}

// ToolCall stores a compact trajectory entry.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
}

// CaseResult stores output, scoring, and evidence for one evaluated case.
type CaseResult struct {
	EvalSetID              string         `json:"evalSetId,omitempty"`
	EvalID                 string         `json:"evalId"`
	Critical               bool           `json:"critical,omitempty"`
	Score                  float64        `json:"score"`
	Passed                 bool           `json:"passed"`
	HardFail               bool           `json:"hardFail"`
	MetricResults          []MetricResult `json:"metricResults,omitempty"`
	FinalResponse          string         `json:"finalResponse,omitempty"`
	ExpectedResponse       string         `json:"expectedResponse,omitempty"`
	ToolTrajectory         []ToolCall     `json:"toolTrajectory,omitempty"`
	ExpectedToolTrajectory []ToolCall     `json:"expectedToolTrajectory,omitempty"`
	TraceSummary           string         `json:"traceSummary,omitempty"`
	RubricReason           string         `json:"rubricReason,omitempty"`
	StructuredOutputStatus string         `json:"structuredOutputStatus,omitempty"`
	FailureReasons         []string       `json:"failureReasons,omitempty"`
	Attributions           []Attribution  `json:"attributions"`
}

// Attribution stores one failure attribution.
type Attribution struct {
	Category   AttributionCategory `json:"category"`
	Confidence float64             `json:"confidence,omitempty"`
	Evidence   string              `json:"evidence"`
	MetricName string              `json:"metricName,omitempty"`
	Source     string              `json:"source,omitempty"`
}

// EvaluationSummary stores set-level and case-level evaluation results.
type EvaluationSummary struct {
	EvalSetID string         `json:"evalSetId"`
	Score     float64        `json:"score"`
	Status    string         `json:"status"`
	Cases     []CaseResult   `json:"cases"`
	Cost      CostSummary    `json:"cost,omitempty"`
	Latency   LatencySummary `json:"latency,omitempty"`
}

// EvaluationPair stores train and validation evaluation summaries.
type EvaluationPair struct {
	Train      EvaluationSummary `json:"train"`
	Validation EvaluationSummary `json:"validation"`
}

// Candidate stores a prompt candidate generated by optimization.
type Candidate struct {
	Round  int    `json:"round"`
	Prompt string `json:"prompt"`
	Reason string `json:"reason,omitempty"`
}

// CandidateRound stores full round audit state.
type CandidateRound struct {
	Round        int               `json:"round"`
	Prompt       string            `json:"prompt"`
	Reason       string            `json:"reason,omitempty"`
	Train        EvaluationSummary `json:"train"`
	Validation   EvaluationSummary `json:"validation"`
	Delta        []CaseDelta       `json:"delta"`
	GateDecision GateDecision      `json:"gateDecision"`
	Cost         CostSummary       `json:"cost,omitempty"`
	Latency      LatencySummary    `json:"latency,omitempty"`
}

// CaseDelta stores one validation case comparison.
type CaseDelta struct {
	EvalSetID          string     `json:"evalSetId,omitempty"`
	EvalID             string     `json:"evalId"`
	BaselineScore      float64    `json:"baselineScore"`
	CandidateScore     float64    `json:"candidateScore"`
	ScoreDelta         float64    `json:"scoreDelta"`
	BaselinePassed     bool       `json:"baselinePassed,omitempty"`
	CandidatePassed    bool       `json:"candidatePassed,omitempty"`
	Transition         Transition `json:"transition"`
	NewHardFail        bool       `json:"newHardFail"`
	CriticalRegression bool       `json:"criticalRegression"`
	AttributionChange  string     `json:"attributionChange,omitempty"`
}

// DeltaSummary summarizes validation deltas.
type DeltaSummary struct {
	NewlyPassed         int `json:"newlyPassed"`
	NewlyFailed         int `json:"newlyFailed"`
	Improved            int `json:"improved"`
	Regressed           int `json:"regressed"`
	Unchanged           int `json:"unchanged"`
	NewHardFails        int `json:"newHardFails"`
	CriticalRegressions int `json:"criticalRegressions"`
}

// DeltaReport stores report-level validation delta.
type DeltaReport struct {
	Summary DeltaSummary `json:"summary"`
	Cases   []CaseDelta  `json:"cases"`
}

// GateDecision stores final gate outcome.
type GateDecision struct {
	Accepted    bool     `json:"accepted"`
	ScoreDelta  float64  `json:"scoreDelta"`
	PassedRules []string `json:"passedRules,omitempty"`
	FailedRules []string `json:"failedRules,omitempty"`
	Reasons     []string `json:"reasons"`
}

// CostSummary stores deterministic or real cost counters.
type CostSummary struct {
	Calls         int     `json:"calls"`
	EstimatedCost float64 `json:"estimatedCost"`
}

// LatencySummary stores latency totals.
type LatencySummary struct {
	TotalMS int64 `json:"totalMs"`
}

// RunMetadata stores report run metadata.
type RunMetadata struct {
	AppName    string       `json:"appName"`
	StartedAt  string       `json:"startedAt"`
	DurationMS int64        `json:"durationMs"`
	Seed       int64        `json:"seed"`
	Runner     RunnerConfig `json:"runner"`
}

// Report is the machine-readable audit artifact.
type Report struct {
	Run                     RunMetadata      `json:"run"`
	Baseline                EvaluationPair   `json:"baseline"`
	Candidates              []CandidateRound `json:"candidates"`
	SelectedCandidate       *CandidateRound  `json:"selectedCandidate"`
	Delta                   DeltaReport      `json:"delta"`
	GateDecision            GateDecision     `json:"gateDecision"`
	FailureAttributionStats map[string]int   `json:"failureAttributionStats"`
	CostSummary             CostSummary      `json:"costSummary"`
	LatencySummary          LatencySummary   `json:"latencySummary"`
	Artifacts               []string         `json:"artifacts,omitempty"`
}

// RunResult stores in-memory outputs from one pipeline run.
type RunResult struct {
	Report       *Report
	JSONPath     string
	MarkdownPath string
	StartedAt    time.Time
}
