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
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// CurrentSchemaVersion is the report schema emitted by this package.
const CurrentSchemaVersion = "1"

// CostCurrencyUSD is the canonical ISO 4217 currency used by estimated-cost
// evidence and BudgetPolicy.MaxEstimatedCost.
const CostCurrencyUSD = "USD"

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
	// RunID identifies this audit and its report bundle. It must satisfy
	// ValidateRunID and be unique within the selected artifact store.
	RunID string `json:"runId"`
	// TargetSurfaceID identifies the only profile surface a candidate may
	// change. It must be non-empty and present in the audited profiles.
	TargetSurfaceID string `json:"targetSurfaceId"`
	// MetricPolicies defines every metric considered by delta and gate
	// evaluation. It must contain at least one valid policy.
	MetricPolicies map[string]MetricPolicy `json:"metricPolicies"`
	// CriticalCaseIDs identifies validation cases that must not regress. Each
	// ID must be unique and present in baseline validation evidence.
	CriticalCaseIDs []string `json:"criticalCaseIds,omitempty"`
	// Gate defines the deterministic release policy applied to each candidate.
	Gate GatePolicy `json:"gate"`
	// Budget defines optional resource limits. A zero limit disables the
	// corresponding limit unless its field documentation states otherwise.
	Budget BudgetPolicy `json:"budget"`
	// Runtime declares reproducibility evidence for the audited execution.
	Runtime RuntimePolicy `json:"runtime"`
	// Audit controls redaction and retention of report content.
	Audit AuditPolicy `json:"audit,omitempty"`
	// InputFingerprint is the lowercase SHA-256 digest of the audited inputs.
	// It must use the format accepted by ValidateInputFingerprint so it can be
	// persisted and rendered safely.
	InputFingerprint string `json:"inputFingerprint"`
	// Metadata contains optional caller annotations. Values are copied and
	// sanitized before they are retained in a report.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// MetricPolicy configures weighting, floors, and hard-failure semantics.
type MetricPolicy struct {
	// Weight is a finite positive contribution to weighted score deltas.
	Weight float64 `json:"weight"`
	// Floor is the optional minimum validation metric score. Zero disables the
	// floor rule for this metric.
	Floor float64 `json:"floor,omitempty"`
	// HardFail marks a newly failing metric as a mandatory gate rejection.
	HardFail bool `json:"hardFail,omitempty"`
}

// GatePolicy configures deterministic validation acceptance rules.
type GatePolicy struct {
	// MinValidationGain is the minimum weighted validation-score delta.
	MinValidationGain float64 `json:"minValidationGain"`
	// MaxCaseRegression is the largest permitted per-metric validation decline.
	MaxCaseRegression float64 `json:"maxCaseRegression"`
	// MaxGeneralizationGap is the optional maximum train-minus-validation
	// weighted gain. Zero disables this overfitting rule.
	MaxGeneralizationGap float64 `json:"maxGeneralizationGap,omitempty"`
	// RejectAnyNewFail rejects a candidate that introduces any validation failure.
	RejectAnyNewFail bool `json:"rejectAnyNewFail"`
	// RequirePromptIterAcceptance makes PromptIter's round acceptance a
	// mandatory release rule instead of advisory evidence.
	RequirePromptIterAcceptance bool `json:"requirePromptIterAcceptance,omitempty"`
	// MaxScoreStdDev is the optional maximum validation-score standard deviation.
	// Zero disables the stability gate. A positive value requires Runtime.NumRuns
	// to be at least two so the standard deviation has sufficient evidence.
	MaxScoreStdDev float64 `json:"maxScoreStdDev,omitempty"`
}

// BudgetPolicy limits aggregate model calls, tokens, cost, and wall time.
type BudgetPolicy struct {
	// MaxCalls is the optional maximum number of model calls; zero disables it.
	MaxCalls int `json:"maxCalls,omitempty"`
	// MaxTokens is the optional maximum aggregate token count; zero disables it.
	MaxTokens int64 `json:"maxTokens,omitempty"`
	// MaxEstimatedCost is the optional maximum estimated cost in USD; zero
	// disables it. Cost evidence must declare CostCurrencyUSD when this budget
	// or RequireKnownCost is enabled.
	MaxEstimatedCost float64 `json:"maxEstimatedCost,omitempty"`
	// MaxPromptIterLatency is the optional upper bound on complete PromptIter
	// latency; zero disables it.
	MaxPromptIterLatency time.Duration `json:"maxPromptIterLatency,omitempty"`
	// RequireKnownCost rejects evidence whose cost provenance is unknown.
	RequireKnownCost bool `json:"requireKnownCost,omitempty"`
}

// RuntimePolicy records caller-owned reproducibility declarations that are not
// part of the PromptIter Engine configuration itself.
type RuntimePolicy struct {
	// Seed is the caller-declared seed used for reproducibility evidence.
	Seed int64 `json:"seed"`
	// SeedApplied is true only when the seed was actually passed to every
	// stochastic model or optimizer component covered by the audit.
	SeedApplied bool `json:"seedApplied"`
	// NumRuns is the positive count of complete evaluation runs per case.
	NumRuns int `json:"numRuns"`
	// Deterministic declares that all audited execution components were
	// deterministic under the supplied runtime configuration. False is emitted
	// explicitly so an audit distinguishes a nondeterministic run from omitted
	// reproducibility evidence.
	Deterministic bool `json:"deterministic"`
}

// PromptIterConfiguration is the effective execution policy retained by the
// Engine and copied into the audit report.
type PromptIterConfiguration struct {
	// NumRuns is the effective Engine evaluation count per case. It is required
	// to match RuntimePolicy.NumRuns, which is the audit declaration.
	NumRuns int `json:"numRuns"`
	// TraceUsageCoversAllCalls reports whether Engine trace telemetry covers all
	// model calls. False means a resource-budget gate must fail closed.
	TraceUsageCoversAllCalls bool `json:"traceUsageCoversAllCalls,omitempty"`
	// RetainAuditEvidence records whether the Engine retained repeated raw
	// evaluation observations for this run. False is emitted explicitly.
	RetainAuditEvidence bool `json:"retainAuditEvidence"`
	// EvaluateFinalCandidateTrain records whether the terminal candidate was
	// evaluated on training sets. False is emitted explicitly.
	EvaluateFinalCandidateTrain bool `json:"evaluateFinalCandidateTrain"`
	// EvalCaseParallelism caps concurrent case evaluations; zero uses Engine's
	// sequential/default execution. Its enabled flag controls whether it applies.
	EvalCaseParallelism int `json:"evalCaseParallelism,omitempty"`
	// EvalCaseParallelInferenceEnabled enables parallel inference inside a case.
	EvalCaseParallelInferenceEnabled bool `json:"evalCaseParallelInferenceEnabled,omitempty"`
	// EvalCaseParallelEvaluationEnabled enables parallel evaluator work per case.
	EvalCaseParallelEvaluationEnabled bool `json:"evalCaseParallelEvaluationEnabled,omitempty"`
	// BackwardCaseParallelismEnabled enables the retained backward parallelism.
	BackwardCaseParallelismEnabled bool `json:"backwardCaseParallelismEnabled,omitempty"`
	// BackwardCaseParallelism is the configured backward case concurrency; zero
	// uses Engine's default when backward parallelism is enabled.
	BackwardCaseParallelism int `json:"backwardCaseParallelism,omitempty"`
	// AggregationSurfaceParallelismEnabled enables aggregation surface parallelism.
	AggregationSurfaceParallelismEnabled bool `json:"aggregationSurfaceParallelismEnabled,omitempty"`
	// AggregationSurfaceParallelism is the configured aggregation concurrency;
	// zero uses Engine's default when aggregation parallelism is enabled.
	AggregationSurfaceParallelism int `json:"aggregationSurfaceParallelism,omitempty"`
	// OptimizerSurfaceParallelismEnabled enables optimizer surface parallelism.
	OptimizerSurfaceParallelismEnabled bool `json:"optimizerSurfaceParallelismEnabled,omitempty"`
	// OptimizerSurfaceParallelism is the configured optimizer concurrency; zero
	// uses Engine's default when optimizer parallelism is enabled.
	OptimizerSurfaceParallelism int `json:"optimizerSurfaceParallelism,omitempty"`
	// MinScoreGain is Engine's own finite acceptance threshold; zero permits
	// non-decreasing candidates and is independent of GatePolicy.MinValidationGain.
	MinScoreGain float64 `json:"minScoreGain"`
	// MaxRounds is Engine's positive maximum optimization-round count.
	MaxRounds int `json:"maxRounds"`
	// MaxRoundsWithoutAcceptance stops Engine after this many consecutive
	// rejected rounds; zero leaves that Engine stop condition disabled.
	MaxRoundsWithoutAcceptance int `json:"maxRoundsWithoutAcceptance,omitempty"`
	// TargetScore is the optional finite Engine stopping score; nil disables it.
	TargetScore *float64 `json:"targetScore,omitempty"`
	// TargetSurfaceIDs is the Engine target list. Regression requires exactly one
	// non-empty value matching RunSpec.TargetSurfaceID.
	TargetSurfaceIDs []string `json:"targetSurfaceIds"`
}

// AuditPolicy controls how much raw execution content is retained.
type AuditPolicy struct {
	// IncludeRawContent persists user inputs, responses, trace snapshots, and
	// tool payloads after analysis. Enable it only for trusted or synthetic data.
	IncludeRawContent bool `json:"includeRawContent,omitempty"`
	// MaxContentBytes limits each retained raw text field. Zero uses a safe
	// default.
	MaxContentBytes int `json:"maxContentBytes,omitempty"`
}

// EvaluationSnapshot is a normalized, case-level evaluation result.
type EvaluationSnapshot struct {
	// EvalSetID identifies the source evaluation set.
	EvalSetID string `json:"evalSetId"`
	// ProfileHash identifies the evaluated profile content.
	ProfileHash string `json:"profileHash"`
	// OverallScore is the aggregate score reported by the evaluator.
	OverallScore float64 `json:"overallScore"`
	// Complete reports whether every expected case and metric was observed.
	Complete bool `json:"complete"`
	// Cases is the owned normalized case evidence for this snapshot.
	Cases []CaseResult `json:"cases"`
	// ScoreStdDev is the repeated-run score deviation when available.
	ScoreStdDev float64 `json:"scoreStdDev,omitempty"`
}

// CaseResult stores observed runs and aggregate metric outcomes for one case.
type CaseResult struct {
	// EvalSetID identifies the source evaluation set.
	EvalSetID string `json:"evalSetId"`
	// CaseID identifies this case within EvalSetID.
	CaseID string `json:"caseId"`
	// Input is retained only when AuditPolicy.IncludeRawContent is enabled.
	Input string `json:"input,omitempty"`
	// Passed is the aggregate case outcome.
	Passed bool `json:"passed"`
	// Critical marks a case that must not regress under GatePolicy.
	Critical bool `json:"critical,omitempty"`
	// Metrics contains the normalized metric evidence for this case.
	Metrics []MetricResult `json:"metrics"`
	// Runs contains optional per-run observations used for audit and stability.
	Runs []Observation `json:"runs,omitempty"`
}

// Observation stores the final response, route, tools, trace, and error for one run.
type Observation struct {
	// RunID identifies the repeated evaluation execution.
	RunID int `json:"runId"`
	// FinalResponse is the observed final response when raw retention is enabled.
	FinalResponse string `json:"finalResponse,omitempty"`
	// ExpectedFinalResponse is the expected response used for attribution.
	ExpectedFinalResponse string `json:"expectedFinalResponse,omitempty"`
	// Route is the observed route or agent selection.
	Route string `json:"route,omitempty"`
	// ExpectedRoute is the route expected by the evaluation case.
	ExpectedRoute string `json:"expectedRoute,omitempty"`
	// Trace contains the stable trace subset when available.
	Trace []TraceStep `json:"trace,omitempty"`
	// Error records an execution error for this run.
	Error string `json:"error,omitempty"`
	// Tools contains the observed tool trajectory.
	Tools []ToolObservation `json:"tools,omitempty"`
	// ExpectedTools contains the expected tool trajectory.
	ExpectedTools []ToolObservation `json:"expectedTools,omitempty"`
}

// TraceStep stores the stable audit subset of one execution trace step.
type TraceStep struct {
	// StepID identifies the trace step.
	StepID string `json:"stepId"`
	// NodeID identifies the producing graph node when available.
	NodeID string `json:"nodeId,omitempty"`
	// Branch identifies the executed branch when available.
	Branch string `json:"branch,omitempty"`
	// AppliedSurfaceIDs lists prompt surfaces active for this step.
	AppliedSurfaceIDs []string `json:"appliedSurfaceIds,omitempty"`
	// Input is retained only when raw audit content is enabled.
	Input string `json:"input,omitempty"`
	// Output is retained only when raw audit content is enabled.
	Output string `json:"output,omitempty"`
	// Error records a trace-step failure.
	Error string `json:"error,omitempty"`
}

// MetricResult stores one metric score and its explanatory evidence.
type MetricResult struct {
	// Name identifies the configured metric.
	Name string `json:"name"`
	// Score is the observed metric score.
	Score float64 `json:"score"`
	// Threshold is the metric pass threshold supplied by the evaluator.
	Threshold float64 `json:"threshold"`
	// Passed reports whether Score meets Threshold.
	Passed bool `json:"passed"`
	// Reason explains a failed or otherwise notable metric result.
	Reason string `json:"reason,omitempty"`
	// Rubrics contains optional structured judge evidence.
	Rubrics []RubricResult `json:"rubrics,omitempty"`
}

// RubricResult stores one structured judge rubric result.
type RubricResult struct {
	// ID identifies the rubric.
	ID string `json:"id"`
	// Score is the rubric score.
	Score float64 `json:"score"`
	// Reason explains the rubric score.
	Reason string `json:"reason,omitempty"`
}

// ToolObservation stores a tool call and its observable result.
type ToolObservation struct {
	// Name identifies the invoked or expected tool.
	Name string `json:"name"`
	// Arguments is the serialized tool argument payload when retained.
	Arguments string `json:"arguments,omitempty"`
	// Result is the serialized tool result payload when retained.
	Result string `json:"result,omitempty"`
	// Error records a tool execution error.
	Error string `json:"error,omitempty"`
}

// CostEstimate describes a priced resource amount and its provenance. Known
// costs use the canonical CostCurrencyUSD unit when a cost policy is enabled.
type CostEstimate struct {
	// EstimatedCost is the non-negative aggregate price in Currency when
	// CostKnown is true.
	EstimatedCost float64 `json:"estimatedCost"`
	// CostKnown reports whether the amount and its provenance are complete.
	CostKnown bool `json:"costKnown"`
	// Currency is CostCurrencyUSD for known evidence used by a cost policy; it
	// is empty when the amount is unknown or no unit is available.
	Currency string `json:"currency,omitempty"`
	// PricingSource identifies the table or provider used to derive the amount.
	PricingSource string `json:"pricingSource,omitempty"`
}

// CostBreakdown allocates a total estimate across baseline and rounds.
type CostBreakdown struct {
	// CostEstimate is the aggregate cost and provenance.
	CostEstimate
	// BaselineEstimatedCost is the baseline portion of EstimatedCost.
	BaselineEstimatedCost float64 `json:"baselineEstimatedCost,omitempty"`
	// RoundEstimatedCosts maps every optimization round to its cost portion.
	RoundEstimatedCosts map[int]float64 `json:"roundEstimatedCosts,omitempty"`
}

// UsageSupplement contains the resource facts that PromptIter cannot measure.
// Callers cannot declare model calls, tokens, or telemetry completeness.
type UsageSupplement struct {
	// PromptIterLatency is the measured complete Engine.Run interval.
	PromptIterLatency time.Duration `json:"promptIterLatency"`
	// CostBreakdown supplies the cost facts not measured by Engine telemetry.
	CostBreakdown
}

// UsageSummary stores aggregate resource consumption for audit and gates.
type UsageSummary struct {
	// Calls is the aggregate model-call count.
	Calls int `json:"calls"`
	// InputTokens is the aggregate prompt-token count when available.
	InputTokens int64 `json:"inputTokens,omitempty"`
	// OutputTokens is the aggregate completion-token count when available.
	OutputTokens int64 `json:"outputTokens,omitempty"`
	// TotalTokens is the aggregate token count used by budget gates.
	TotalTokens int64 `json:"totalTokens"`
	// CostEstimate is the aggregate priced usage and provenance.
	CostEstimate
	// PromptIterLatency is the complete Engine.Run latency.
	PromptIterLatency time.Duration `json:"promptIterLatency"`
	// Complete means the summary covers every model-bearing optimization stage,
	// not only Evaluation execution traces.
	// Complete reports whether telemetry covers every model-bearing stage.
	Complete bool `json:"complete"`
	// TelemetrySource identifies the authoritative usage producer.
	TelemetrySource string `json:"telemetrySource"`
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

func validFailureCategory(category FailureCategory) bool {
	switch category {
	case FailureInferenceError, FailureFinalResponseMismatch, FailureToolSelection,
		FailureToolArgument, FailureToolResultHandling, FailureRoute, FailureFormat,
		FailureKnowledgeRecall, FailureSafetyPolicy, FailureUnknown:
		return true
	default:
		return false
	}
}

func sanitizedFailureCategory(category FailureCategory) FailureCategory {
	if validFailureCategory(category) {
		return category
	}
	return FailureUnknown
}

// Evidence identifies the observation supporting a failure attribution.
type Evidence struct {
	// Source identifies the evidence producer.
	Source string `json:"source"`
	// Path identifies the stable location within that producer.
	Path string `json:"path"`
	// Reason explains how the evidence supports the attribution.
	Reason string `json:"reason"`
}

// AttributionPhase identifies the evaluation snapshot that produced a failure.
type AttributionPhase string

const (
	// AttributionBaselineTrain identifies baseline training evidence.
	AttributionBaselineTrain AttributionPhase = "baseline_train"
	// AttributionBaselineValidation identifies baseline validation evidence.
	AttributionBaselineValidation AttributionPhase = "baseline_validation"
	// AttributionCandidateTrain identifies candidate training evidence.
	AttributionCandidateTrain AttributionPhase = "candidate_train"
	// AttributionCandidateValidation identifies candidate validation evidence.
	AttributionCandidateValidation AttributionPhase = "candidate_validation"
)

// AttributionResult explains the primary failure of one training case.
type AttributionResult struct {
	// Phase identifies the evaluation stage that produced this attribution.
	Phase AttributionPhase `json:"phase,omitempty"`
	// CandidateID identifies the candidate for candidate-phase evidence.
	CandidateID string `json:"candidateId,omitempty"`
	// EvalSetID identifies the source evaluation set.
	EvalSetID string `json:"evalSetId"`
	// CaseID identifies the attributed case.
	CaseID string `json:"caseId"`
	// Category is one declared, stable FailureCategory.
	Category FailureCategory `json:"category"`
	// Reason is the primary human-readable explanation.
	Reason string `json:"reason"`
	// Evidence contains at least one supporting item for analyzer output.
	Evidence []Evidence `json:"evidence"`
}

// Candidate is one concrete profile produced by a PromptIter round.
type Candidate struct {
	// ID uniquely identifies this candidate within the run.
	ID string `json:"id"`
	// Round is the one-based PromptIter round that created this candidate.
	Round int `json:"round"`
	// Profile is an owned candidate profile snapshot.
	Profile *promptiter.Profile `json:"profile"`
	// ProfileHash is the canonical hash of Profile.
	ProfileHash string `json:"profileHash"`
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
	// MetricName identifies the compared metric.
	MetricName string `json:"metricName"`
	// Kind classifies the baseline-to-candidate change.
	Kind ChangeKind `json:"kind"`
	// BaselineScore is the baseline metric score.
	BaselineScore float64 `json:"baselineScore"`
	// CandidateScore is the candidate metric score.
	CandidateScore float64 `json:"candidateScore"`
	// BaselinePassed is the baseline metric pass state.
	BaselinePassed bool `json:"baselinePassed"`
	// CandidatePassed is the candidate metric pass state.
	CandidatePassed bool `json:"candidatePassed"`
	// HardFail reports whether the configured metric treats a new failure as hard.
	HardFail bool `json:"hardFail,omitempty"`
}

// CaseDelta stores all metric changes for one evaluation case.
type CaseDelta struct {
	// EvalSetID identifies the source evaluation set.
	EvalSetID string `json:"evalSetId"`
	// CaseID identifies the compared case.
	CaseID string `json:"caseId"`
	// Kind summarizes the case-level change.
	Kind ChangeKind `json:"kind"`
	// Critical reports whether this is a configured critical case.
	Critical bool `json:"critical,omitempty"`
	// BaselinePassed is the baseline case pass state.
	BaselinePassed bool `json:"baselinePassed"`
	// CandidatePassed is the candidate case pass state.
	CandidatePassed bool `json:"candidatePassed"`
	// Metrics contains the per-metric change evidence.
	Metrics []MetricDelta `json:"metrics"`
}

// DeltaReport summarizes baseline-to-candidate changes for one set.
type DeltaReport struct {
	// BaselineScore is the baseline aggregate score.
	BaselineScore float64 `json:"baselineScore"`
	// CandidateScore is the candidate aggregate score.
	CandidateScore float64 `json:"candidateScore"`
	// WeightedScoreDelta is the policy-weighted candidate-minus-baseline score.
	WeightedScoreDelta float64 `json:"weightedScoreDelta"`
	// Complete reports whether the compared evidence is complete.
	Complete bool `json:"complete"`
	// NewPasses is the count of newly passing cases.
	NewPasses int `json:"newPasses"`
	// NewFailures is the count of newly failing cases.
	NewFailures int `json:"newFailures"`
	// NewHardFailures is the count of configured hard metric failures.
	NewHardFailures int `json:"newHardFailures"`
	// CriticalRegressions is the count of critical cases that regressed.
	CriticalRegressions int `json:"criticalRegressions"`
	// Cases contains the complete deterministic case-level comparison.
	Cases []CaseDelta `json:"cases"`
}

// RuleValueType identifies the scalar encoded by a RuleValue.
//
// The type tag keeps report JSON stable. RuleValue is encoded as one JSON
// string in the form "type|value", while Type tells consumers how to interpret
// that canonical scalar.
type RuleValueType string

const (
	// RuleValueBoolean encodes a boolean value using strconv.FormatBool.
	RuleValueBoolean RuleValueType = "boolean"
	// RuleValueInteger encodes a base-10 signed integer.
	RuleValueInteger RuleValueType = "integer"
	// RuleValueNumber encodes a finite floating-point number.
	RuleValueNumber RuleValueType = "number"
	// RuleValueText encodes arbitrary sanitized text.
	RuleValueText RuleValueType = "text"
	// RuleValueDuration encodes a time.Duration using time.Duration.String.
	RuleValueDuration RuleValueType = "duration"
)

// RuleValue is a tagged, canonical scalar used in GateRuleResult evidence.
//
// Construct values with the helper functions below. Consumers must use Type
// rather than guessing from Value; this gives JSON reports one stable string
// representation for every gate rule without inflating report artifacts.
type RuleValue struct {
	// Type identifies the canonical scalar encoding of Value.
	Type RuleValueType `json:"type"`
	// Value is the canonical text form validated for Type.
	Value string `json:"value"`
}

// BooleanRuleValue returns a canonical boolean gate value.
func BooleanRuleValue(value bool) RuleValue {
	return RuleValue{Type: RuleValueBoolean, Value: strconv.FormatBool(value)}
}

// IntegerRuleValue returns a canonical signed-integer gate value.
func IntegerRuleValue(value int64) RuleValue {
	return RuleValue{Type: RuleValueInteger, Value: strconv.FormatInt(value, 10)}
}

// NumberRuleValue returns a canonical finite floating-point gate value.
// Non-finite inputs produce an invalid RuleValue and are rejected by Analyzer.
func NumberRuleValue(value float64) RuleValue {
	return RuleValue{Type: RuleValueNumber, Value: strconv.FormatFloat(value, 'g', -1, 64)}
}

// TextRuleValue returns a textual gate value. Report generation sanitizes its
// Value according to the audit policy before it crosses a persistence boundary.
func TextRuleValue(value string) RuleValue {
	return RuleValue{Type: RuleValueText, Value: value}
}

// DurationRuleValue returns a canonical duration gate value.
func DurationRuleValue(value time.Duration) RuleValue {
	return RuleValue{Type: RuleValueDuration, Value: value.String()}
}

// String returns RuleValue's canonical scalar representation for display.
func (v RuleValue) String() string {
	return v.Value
}

// MarshalJSON encodes a RuleValue as the stable tagged scalar "type|value".
func (v RuleValue) MarshalJSON() ([]byte, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(string(v.Type) + "|" + v.Value)
}

// UnmarshalJSON decodes and validates the stable tagged scalar "type|value".
func (v *RuleValue) UnmarshalJSON(data []byte) error {
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return fmt.Errorf("decode rule value: %w", err)
	}
	separator := strings.IndexByte(encoded, '|')
	if separator <= 0 {
		return fmt.Errorf("invalid tagged rule value %q", encoded)
	}
	decoded := RuleValue{
		Type:  RuleValueType(encoded[:separator]),
		Value: encoded[separator+1:],
	}
	if err := decoded.validate(); err != nil {
		return err
	}
	*v = decoded
	return nil
}

func (v RuleValue) validate() error {
	switch v.Type {
	case RuleValueBoolean:
		value, err := strconv.ParseBool(v.Value)
		if err != nil || strconv.FormatBool(value) != v.Value {
			return fmt.Errorf("invalid boolean value %q", v.Value)
		}
	case RuleValueInteger:
		value, err := strconv.ParseInt(v.Value, 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != v.Value {
			return fmt.Errorf("invalid integer value %q", v.Value)
		}
	case RuleValueNumber:
		value, err := strconv.ParseFloat(v.Value, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) ||
			strconv.FormatFloat(value, 'g', -1, 64) != v.Value {
			return fmt.Errorf("invalid finite number value %q", v.Value)
		}
	case RuleValueText:
		// Text is intentionally unconstrained; it is redacted before persistence.
	case RuleValueDuration:
		value, err := time.ParseDuration(v.Value)
		if err != nil || value.String() != v.Value {
			return fmt.Errorf("invalid duration value %q", v.Value)
		}
	default:
		return fmt.Errorf("unknown rule value type %q", v.Type)
	}
	return nil
}

// GateRuleResult records one deterministic gate rule outcome. Observed and
// Threshold always use RuleValue's tagged-string JSON schema, never arbitrary
// JSON values.
type GateRuleResult struct {
	// Rule is the stable rule identifier.
	Rule string `json:"rule"`
	// Passed reports whether this rule passed.
	Passed bool `json:"passed"`
	// Observed is the canonical observed scalar.
	Observed RuleValue `json:"observed"`
	// Threshold is the canonical required scalar.
	Threshold RuleValue `json:"threshold"`
	// Reason explains a failed rule.
	Reason string `json:"reason,omitempty"`
}

// GateInput contains the evidence needed for one candidate decision.
type GateInput struct {
	// Spec is the immutable audit policy being evaluated.
	Spec *RunSpec
	// PromptIterAccepted is the Engine acceptance outcome.
	PromptIterAccepted bool
	// PromptIterReason explains a rejected Engine outcome.
	PromptIterReason string
	// CandidateProfileValid reports whether only the target surface changed.
	CandidateProfileValid bool
	// CandidateProfileReason explains an invalid profile scope.
	CandidateProfileReason string
	// CandidateProfileChanged reports an effective profile change. The separate
	// CandidateProfileValid field verifies that a change is limited to the configured target surface.
	CandidateProfileChanged bool
	// CandidateValidation is the normalized candidate validation evidence.
	CandidateValidation *EvaluationSnapshot
	// TrainDelta is optional candidate training comparison evidence.
	TrainDelta *DeltaReport
	// ValidationDelta is the required candidate validation comparison.
	ValidationDelta *DeltaReport
	// TotalUsage is the candidate cumulative resource evidence.
	TotalUsage UsageSummary
}

// GateDecision is the accepted, rejected, or inconclusive gate result.
type GateDecision struct {
	// Decision is the final accepted, rejected, or inconclusive outcome.
	Decision Decision `json:"decision"`
	// Rules is the deterministic evidence for every applied rule.
	Rules []GateRuleResult `json:"rules"`
	// Reasons explains non-accepted decisions.
	Reasons []string `json:"reasons,omitempty"`
	// Warnings records advisory non-gating evidence.
	Warnings []string `json:"warnings,omitempty"`
}

// CandidateResult stores one PromptIter round and its independent audit evidence.
type CandidateResult struct {
	// Candidate identifies the audited PromptIter output.
	Candidate Candidate `json:"candidate"`
	// PromptIterAccepted is the Engine round acceptance outcome.
	PromptIterAccepted bool `json:"promptIterAccepted"`
	// PromptIterReason explains the Engine outcome when available.
	PromptIterReason string `json:"promptIterReason,omitempty"`
	// ProfileChanged reports an effective profile change. The gate separately
	// verifies that the change is limited to the configured target surface.
	ProfileChanged bool `json:"profileChanged"`
	// PromptIterShouldStop reports whether Engine stopped after this round.
	PromptIterShouldStop bool `json:"promptIterShouldStop,omitempty"`
	// PromptIterStopReason explains a stopping round.
	PromptIterStopReason string `json:"promptIterStopReason,omitempty"`
	// Train is optional candidate training evidence.
	Train *EvaluationSnapshot `json:"train,omitempty"`
	// Validation is the required candidate validation evidence.
	Validation *EvaluationSnapshot `json:"validation"`
	// TrainDelta is optional candidate training comparison evidence.
	TrainDelta *DeltaReport `json:"trainDelta,omitempty"`
	// ValidationDelta is the required validation comparison evidence.
	ValidationDelta *DeltaReport `json:"validationDelta"`
	// RoundUsage is resource use attributable to this round alone.
	RoundUsage UsageSummary `json:"roundUsage"`
	// CumulativeUsage is resource use through this candidate round.
	CumulativeUsage UsageSummary `json:"cumulativeUsage"`
	// Gate is the independent Regression release decision.
	Gate *GateDecision `json:"gate"`
}

// RunResult is the complete machine-readable optimization audit record.
type RunResult struct {
	// SchemaVersion identifies the JSON report schema.
	SchemaVersion string `json:"schemaVersion"`
	// RunID identifies this immutable audit run.
	RunID string `json:"runId"`
	// Status is the terminal audit lifecycle status.
	Status RunStatus `json:"status"`
	// StartedAt is the audit start timestamp in UTC.
	StartedAt time.Time `json:"startedAt"`
	// EndedAt is the audit end timestamp in UTC.
	EndedAt time.Time `json:"endedAt"`
	// Spec is the owned, persisted audit specification.
	Spec *RunSpec `json:"spec,omitempty"`
	// PromptIter is the effective Engine configuration.
	PromptIter *PromptIterConfiguration `json:"promptIter,omitempty"`
	// BaselineProfile is the owned baseline profile snapshot.
	BaselineProfile *promptiter.Profile `json:"baselineProfile,omitempty"`
	// BaselineTrain is normalized baseline training evidence.
	BaselineTrain *EvaluationSnapshot `json:"baselineTrain,omitempty"`
	// BaselineValidation is normalized baseline validation evidence.
	BaselineValidation *EvaluationSnapshot `json:"baselineValidation,omitempty"`
	// Attributions contains explainable failed-case classifications.
	Attributions []AttributionResult `json:"attributions,omitempty"`
	// AttributionCounts summarizes Attributions by stable category.
	AttributionCounts map[FailureCategory]int `json:"attributionCounts,omitempty"`
	// Candidates contains each independently gated candidate.
	Candidates []CandidateResult `json:"candidates,omitempty"`
	// SelectedCandidateID identifies the sole publishable candidate, if any.
	SelectedCandidateID string `json:"selectedCandidateId,omitempty"`
	// Decision is the aggregate Regression release decision.
	Decision Decision `json:"decision"`
	// Usage is complete run-level resource usage.
	Usage UsageSummary `json:"usage"`
	// ErrorMessage is the sanitized terminal failure or cancellation reason.
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// Attributor classifies failed or execution-error training and validation cases.
type Attributor interface {
	// Attribute classifies one failed or execution-error case from a training or
	// validation snapshot. The context controls the call; case must be non-nil
	// and is read-only. A successful result must be non-nil, identify the same
	// case, use a declared FailureCategory, and contain a reason and at least
	// one evidence item.
	Attribute(context.Context, *CaseResult) (*AttributionResult, error)
}

// DeltaEngine computes stable case- and metric-level changes.
type DeltaEngine interface {
	// Compare computes a deterministic baseline-to-candidate report. Both
	// snapshots and metricPolicies are read-only and must be valid; a successful
	// result must be non-nil and describe compatible evidence. Valid but incomplete
	// evidence is returned with DeltaReport.Complete false so Gate can fail closed.
	Compare(*EvaluationSnapshot, *EvaluationSnapshot, map[string]MetricPolicy) (*DeltaReport, error)
}

// Gate applies deterministic acceptance policy to one candidate.
type Gate interface {
	// Decide applies release policy to one candidate. input is read-only and
	// must be non-nil. A successful result must be non-nil and satisfy the
	// GateDecision evidence contract, including canonical RuleValue fields.
	Decide(*GateInput) (*GateDecision, error)
}
