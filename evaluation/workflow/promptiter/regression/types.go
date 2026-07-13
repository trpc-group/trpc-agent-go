//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regression audits PromptIter runs with deterministic attribution,
// validation deltas, release gates, and reproducible reports.
package regression

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// CurrentSchemaVersion is the report schema emitted by this package.
const CurrentSchemaVersion = "1"

// RunStatus describes the lifecycle result of an audit run.
type RunStatus string

const (
	// RunStatusRunning means the audit has started but has not finished.
	RunStatusRunning RunStatus = "running"
	// RunStatusSucceeded means every audit stage completed.
	RunStatusSucceeded RunStatus = "succeeded"
	// RunStatusFailed means a non-cancellation error stopped the audit.
	RunStatusFailed RunStatus = "failed"
	// RunStatusCanceled means the context was canceled or timed out.
	RunStatusCanceled RunStatus = "canceled"
)

// Decision is the release recommendation for a candidate or complete run.
type Decision string

const (
	// DecisionAccepted means all mandatory gate rules passed.
	DecisionAccepted Decision = "accepted"
	// DecisionRejected means at least one deterministic gate rule failed.
	DecisionRejected Decision = "rejected"
	// DecisionInconclusive means required evidence was unavailable.
	DecisionInconclusive Decision = "inconclusive"
)

// RunSpec contains caller-controlled audit and release policy inputs.
type RunSpec struct {
	RunID            string                  `json:"runId"`
	TargetSurfaceID  string                  `json:"targetSurfaceId"`
	MetricPolicies   map[string]MetricPolicy `json:"metricPolicies"`
	CriticalCaseIDs  []string                `json:"criticalCaseIds,omitempty"`
	Gate             GatePolicy              `json:"gate"`
	Budget           BudgetPolicy            `json:"budget"`
	Runtime          RuntimePolicy           `json:"runtime"`
	Audit            AuditPolicy             `json:"audit,omitempty"`
	InputFingerprint string                  `json:"inputFingerprint"`
	Metadata         map[string]string       `json:"metadata,omitempty"`
}

// MetricPolicy configures weighting, floors, and hard-failure semantics.
type MetricPolicy struct {
	Weight   float64 `json:"weight"`
	Floor    float64 `json:"floor,omitempty"`
	HardFail bool    `json:"hardFail,omitempty"`
}

// GatePolicy configures deterministic validation acceptance rules.
type GatePolicy struct {
	MinValidationGain    float64 `json:"minValidationGain"`
	MaxCaseRegression    float64 `json:"maxCaseRegression"`
	MaxGeneralizationGap float64 `json:"maxGeneralizationGap,omitempty"`
	RejectAnyNewFail     bool    `json:"rejectAnyNewFail"`
	// RequireCompleteResults is retained for configuration compatibility.
	// Release decisions always require complete case and metric evidence.
	RequireCompleteResults bool    `json:"requireCompleteResults"`
	MaxScoreStdDev         float64 `json:"maxScoreStdDev,omitempty"`
}

// BudgetPolicy limits aggregate model calls, tokens, cost, and wall time.
type BudgetPolicy struct {
	MaxCalls         int           `json:"maxCalls,omitempty"`
	MaxTokens        int64         `json:"maxTokens,omitempty"`
	MaxEstimatedCost float64       `json:"maxEstimatedCost,omitempty"`
	MaxWallTime      time.Duration `json:"maxWallTime,omitempty"`
	RequireKnownCost bool          `json:"requireKnownCost,omitempty"`
}

// RuntimePolicy records caller-owned reproducibility declarations that are not
// part of the PromptIter Engine configuration itself.
type RuntimePolicy struct {
	Seed int64 `json:"seed"`
	// SeedApplied is true only when the seed was actually passed to every
	// stochastic model or optimizer component covered by the audit.
	SeedApplied   bool `json:"seedApplied"`
	NumRuns       int  `json:"numRuns"`
	Deterministic bool `json:"deterministic,omitempty"`
}

// PromptIterConfiguration is the effective execution policy retained by the
// Engine and copied into the audit report.
type PromptIterConfiguration struct {
	NumRuns                              int      `json:"numRuns"`
	TraceUsageCoversAllCalls             bool     `json:"traceUsageCoversAllCalls,omitempty"`
	EvalCaseParallelism                  int      `json:"evalCaseParallelism,omitempty"`
	EvalCaseParallelInferenceEnabled     bool     `json:"evalCaseParallelInferenceEnabled,omitempty"`
	EvalCaseParallelEvaluationEnabled    bool     `json:"evalCaseParallelEvaluationEnabled,omitempty"`
	BackwardCaseParallelismEnabled       bool     `json:"backwardCaseParallelismEnabled,omitempty"`
	BackwardCaseParallelism              int      `json:"backwardCaseParallelism,omitempty"`
	AggregationSurfaceParallelismEnabled bool     `json:"aggregationSurfaceParallelismEnabled,omitempty"`
	AggregationSurfaceParallelism        int      `json:"aggregationSurfaceParallelism,omitempty"`
	OptimizerSurfaceParallelismEnabled   bool     `json:"optimizerSurfaceParallelismEnabled,omitempty"`
	OptimizerSurfaceParallelism          int      `json:"optimizerSurfaceParallelism,omitempty"`
	MinScoreGain                         float64  `json:"minScoreGain"`
	MaxRounds                            int      `json:"maxRounds"`
	MaxRoundsWithoutAcceptance           int      `json:"maxRoundsWithoutAcceptance,omitempty"`
	TargetScore                          *float64 `json:"targetScore,omitempty"`
	TargetSurfaceIDs                     []string `json:"targetSurfaceIds"`
}

// AuditPolicy controls how much raw execution content is retained.
type AuditPolicy struct {
	// IncludeRawContent retains user inputs, responses, trace snapshots, and
	// tool payloads. It should only be enabled for trusted or synthetic data.
	IncludeRawContent bool `json:"includeRawContent,omitempty"`
	// MaxContentBytes limits each retained raw text field. Zero uses a safe
	// default.
	MaxContentBytes int `json:"maxContentBytes,omitempty"`
}

// EvaluationSnapshot is a normalized, case-level evaluation result.
type EvaluationSnapshot struct {
	EvalSetID    string       `json:"evalSetId"`
	ProfileHash  string       `json:"profileHash"`
	OverallScore float64      `json:"overallScore"`
	Complete     bool         `json:"complete"`
	Cases        []CaseResult `json:"cases"`
	ScoreStdDev  float64      `json:"scoreStdDev,omitempty"`
}

// CaseResult stores observed runs and aggregate metric outcomes for one case.
type CaseResult struct {
	EvalSetID string         `json:"evalSetId"`
	CaseID    string         `json:"caseId"`
	Input     string         `json:"input,omitempty"`
	Passed    bool           `json:"passed"`
	Critical  bool           `json:"critical,omitempty"`
	Metrics   []MetricResult `json:"metrics"`
	Runs      []Observation  `json:"runs,omitempty"`
}

// Observation stores the final response, route, tools, trace, and error for one run.
type Observation struct {
	RunID         int               `json:"runId"`
	FinalResponse string            `json:"finalResponse,omitempty"`
	Route         string            `json:"route,omitempty"`
	Trace         []TraceStep       `json:"trace,omitempty"`
	Error         string            `json:"error,omitempty"`
	Tools         []ToolObservation `json:"tools,omitempty"`
}

// TraceStep stores the stable audit subset of one execution trace step.
type TraceStep struct {
	StepID            string   `json:"stepId"`
	NodeID            string   `json:"nodeId,omitempty"`
	Branch            string   `json:"branch,omitempty"`
	AppliedSurfaceIDs []string `json:"appliedSurfaceIds,omitempty"`
	Input             string   `json:"input,omitempty"`
	Output            string   `json:"output,omitempty"`
	Error             string   `json:"error,omitempty"`
}

// MetricResult stores one metric score and its explanatory evidence.
type MetricResult struct {
	Name      string         `json:"name"`
	Score     float64        `json:"score"`
	Threshold float64        `json:"threshold"`
	Passed    bool           `json:"passed"`
	Reason    string         `json:"reason,omitempty"`
	Rubrics   []RubricResult `json:"rubrics,omitempty"`
}

// RubricResult stores one structured judge rubric result.
type RubricResult struct {
	ID     string  `json:"id"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason,omitempty"`
}

// ToolObservation stores a tool call and its observable result.
type ToolObservation struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

// UsageSummary stores aggregate resource consumption for audit and gates.
type UsageSummary struct {
	Calls         int           `json:"calls"`
	InputTokens   int64         `json:"inputTokens,omitempty"`
	OutputTokens  int64         `json:"outputTokens,omitempty"`
	TotalTokens   int64         `json:"totalTokens"`
	EstimatedCost float64       `json:"estimatedCost"`
	CostKnown     bool          `json:"costKnown"`
	Latency       time.Duration `json:"latency"`
	// Complete means the summary covers every model-bearing optimization stage,
	// not only Evaluation execution traces.
	Complete bool `json:"complete"`
	// Source identifies the component that produced the usage summary.
	Source string `json:"source,omitempty"`
}

// FailureCategory is a stable, machine-readable failure class.
type FailureCategory string

const (
	// FailureInferenceError indicates execution failed before quality evaluation.
	FailureInferenceError FailureCategory = "inference_error"
	// FailureFinalResponseMismatch indicates the final answer was incorrect.
	FailureFinalResponseMismatch FailureCategory = "final_response_mismatch"
	// FailureToolSelection indicates the wrong tool was selected.
	FailureToolSelection FailureCategory = "tool_selection_error"
	// FailureToolArgument indicates a tool received incorrect arguments.
	FailureToolArgument FailureCategory = "tool_argument_error"
	// FailureToolResultHandling indicates a tool error or result was mishandled.
	FailureToolResultHandling FailureCategory = "tool_result_handling_error"
	// FailureRoute indicates the wrong agent or route was selected.
	FailureRoute FailureCategory = "route_error"
	// FailureFormat indicates a structured-output contract was violated.
	FailureFormat FailureCategory = "format_error"
	// FailureKnowledgeRecall indicates required facts were not recalled.
	FailureKnowledgeRecall FailureCategory = "knowledge_recall_error"
	// FailureSafetyPolicy indicates a safety requirement was violated.
	FailureSafetyPolicy FailureCategory = "safety_policy_error"
	// FailureUnknown indicates the available evidence was insufficient.
	FailureUnknown FailureCategory = "unknown"
)

// Evidence identifies the observation supporting a failure attribution.
type Evidence struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// AttributionResult explains the primary failure of one training case.
type AttributionResult struct {
	EvalSetID string          `json:"evalSetId"`
	CaseID    string          `json:"caseId"`
	Category  FailureCategory `json:"category"`
	Reason    string          `json:"reason"`
	Evidence  []Evidence      `json:"evidence"`
}

// Candidate is one concrete profile produced by a PromptIter round.
type Candidate struct {
	ID          string              `json:"id"`
	Round       int                 `json:"round"`
	Profile     *promptiter.Profile `json:"profile"`
	ProfileHash string              `json:"profileHash"`
}

// ChangeKind classifies a baseline-to-candidate change.
type ChangeKind string

const (
	// ChangeNewPass means a previously failing item now passes.
	ChangeNewPass ChangeKind = "new_pass"
	// ChangeNewFail means a previously passing item now fails.
	ChangeNewFail ChangeKind = "new_fail"
	// ChangeImproved means the score increased without a pass-state transition.
	ChangeImproved ChangeKind = "improved"
	// ChangeRegressed means the score decreased without a pass-state transition.
	ChangeRegressed ChangeKind = "regressed"
	// ChangeUnchanged means the score and pass state are equivalent.
	ChangeUnchanged ChangeKind = "unchanged"
	// ChangeMissing means a baseline item is absent from candidate evidence.
	ChangeMissing ChangeKind = "missing"
	// ChangeExtra means candidate evidence contains an unexpected item.
	ChangeExtra ChangeKind = "extra"
)

// MetricDelta stores the change for one metric on one case.
type MetricDelta struct {
	MetricName      string     `json:"metricName"`
	Kind            ChangeKind `json:"kind"`
	BaselineScore   float64    `json:"baselineScore"`
	CandidateScore  float64    `json:"candidateScore"`
	BaselinePassed  bool       `json:"baselinePassed"`
	CandidatePassed bool       `json:"candidatePassed"`
	HardFail        bool       `json:"hardFail,omitempty"`
}

// CaseDelta stores all metric changes for one evaluation case.
type CaseDelta struct {
	EvalSetID       string        `json:"evalSetId"`
	CaseID          string        `json:"caseId"`
	Kind            ChangeKind    `json:"kind"`
	Critical        bool          `json:"critical,omitempty"`
	BaselinePassed  bool          `json:"baselinePassed"`
	CandidatePassed bool          `json:"candidatePassed"`
	Metrics         []MetricDelta `json:"metrics"`
}

// DeltaReport summarizes baseline-to-candidate changes for one set.
type DeltaReport struct {
	BaselineScore       float64     `json:"baselineScore"`
	CandidateScore      float64     `json:"candidateScore"`
	WeightedScoreDelta  float64     `json:"weightedScoreDelta"`
	Complete            bool        `json:"complete"`
	NewPasses           int         `json:"newPasses"`
	NewFailures         int         `json:"newFailures"`
	NewHardFailures     int         `json:"newHardFailures"`
	CriticalRegressions int         `json:"criticalRegressions"`
	Cases               []CaseDelta `json:"cases"`
}

// GateRuleResult records one deterministic gate rule outcome.
type GateRuleResult struct {
	Rule      string `json:"rule"`
	Passed    bool   `json:"passed"`
	Observed  any    `json:"observed"`
	Threshold any    `json:"threshold"`
	Reason    string `json:"reason,omitempty"`
}

// GateInput contains the evidence needed for one candidate decision.
type GateInput struct {
	Spec                   *RunSpec
	PromptIterAccepted     bool
	PromptIterReason       string
	CandidateProfileValid  bool
	CandidateProfileReason string
	CandidateValidation    *EvaluationSnapshot
	TrainDelta             *DeltaReport
	ValidationDelta        *DeltaReport
	TotalUsage             UsageSummary
}

// GateDecision is the accepted, rejected, or inconclusive gate result.
type GateDecision struct {
	Decision Decision         `json:"decision"`
	Rules    []GateRuleResult `json:"rules"`
	Reasons  []string         `json:"reasons,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
}

// CandidateResult stores one PromptIter round and its independent audit evidence.
type CandidateResult struct {
	Candidate            Candidate           `json:"candidate"`
	PromptIterAccepted   bool                `json:"promptIterAccepted"`
	PromptIterReason     string              `json:"promptIterReason,omitempty"`
	ProfileChanged       bool                `json:"profileChanged"`
	PromptIterShouldStop bool                `json:"promptIterShouldStop,omitempty"`
	PromptIterStopReason string              `json:"promptIterStopReason,omitempty"`
	Train                *EvaluationSnapshot `json:"train,omitempty"`
	Validation           *EvaluationSnapshot `json:"validation"`
	TrainDelta           *DeltaReport        `json:"trainDelta,omitempty"`
	ValidationDelta      *DeltaReport        `json:"validationDelta"`
	Gate                 *GateDecision       `json:"gate"`
}

// RunResult is the complete machine-readable optimization audit record.
type RunResult struct {
	SchemaVersion       string                   `json:"schemaVersion"`
	RunID               string                   `json:"runId"`
	Status              RunStatus                `json:"status"`
	StartedAt           time.Time                `json:"startedAt"`
	EndedAt             time.Time                `json:"endedAt"`
	Spec                *RunSpec                 `json:"spec,omitempty"`
	PromptIter          *PromptIterConfiguration `json:"promptIter,omitempty"`
	BaselineProfile     *promptiter.Profile      `json:"baselineProfile,omitempty"`
	BaselineTrain       *EvaluationSnapshot      `json:"baselineTrain,omitempty"`
	BaselineValidation  *EvaluationSnapshot      `json:"baselineValidation,omitempty"`
	Attributions        []AttributionResult      `json:"attributions,omitempty"`
	AttributionCounts   map[FailureCategory]int  `json:"attributionCounts,omitempty"`
	Candidates          []CandidateResult        `json:"candidates,omitempty"`
	SelectedCandidateID string                   `json:"selectedCandidateId,omitempty"`
	Decision            Decision                 `json:"decision"`
	Usage               UsageSummary             `json:"usage"`
	ErrorMessage        string                   `json:"errorMessage,omitempty"`
}

// Attributor classifies a failed training case.
type Attributor interface {
	Attribute(context.Context, *CaseResult) (*AttributionResult, error)
}

// DeltaEngine computes stable case- and metric-level changes.
type DeltaEngine interface {
	Compare(*EvaluationSnapshot, *EvaluationSnapshot, map[string]MetricPolicy) (*DeltaReport, error)
}

// Gate applies deterministic acceptance policy to one candidate.
type Gate interface {
	Decide(*GateInput) (*GateDecision, error)
}
