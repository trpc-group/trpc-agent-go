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
	// MaxEstimatedCost is the optional maximum estimated cost; zero disables it.
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
	// deterministic under the supplied runtime configuration.
	Deterministic bool `json:"deterministic,omitempty"`
}

// PromptIterConfiguration is the effective execution policy retained by the
// Engine and copied into the audit report.
type PromptIterConfiguration struct {
	NumRuns                  int  `json:"numRuns"`
	TraceUsageCoversAllCalls bool `json:"traceUsageCoversAllCalls,omitempty"`
	// RetainAuditEvidence records whether the Engine retained repeated raw
	// evaluation observations for this run. False is emitted explicitly.
	RetainAuditEvidence bool `json:"retainAuditEvidence"`
	// EvaluateFinalCandidateTrain records whether the terminal candidate was
	// evaluated on training sets. False is emitted explicitly.
	EvaluateFinalCandidateTrain          bool     `json:"evaluateFinalCandidateTrain"`
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
	// IncludeRawContent persists user inputs, responses, trace snapshots, and
	// tool payloads after analysis. Enable it only for trusted or synthetic data.
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
	RunID                 int               `json:"runId"`
	FinalResponse         string            `json:"finalResponse,omitempty"`
	ExpectedFinalResponse string            `json:"expectedFinalResponse,omitempty"`
	Route                 string            `json:"route,omitempty"`
	ExpectedRoute         string            `json:"expectedRoute,omitempty"`
	Trace                 []TraceStep       `json:"trace,omitempty"`
	Error                 string            `json:"error,omitempty"`
	Tools                 []ToolObservation `json:"tools,omitempty"`
	ExpectedTools         []ToolObservation `json:"expectedTools,omitempty"`
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

// CostEstimate describes a priced resource amount and its provenance.
type CostEstimate struct {
	EstimatedCost float64 `json:"estimatedCost"`
	CostKnown     bool    `json:"costKnown"`
	PricingSource string  `json:"pricingSource,omitempty"`
}

// CostBreakdown allocates a total estimate across baseline and rounds.
type CostBreakdown struct {
	CostEstimate
	BaselineEstimatedCost float64         `json:"baselineEstimatedCost,omitempty"`
	RoundEstimatedCosts   map[int]float64 `json:"roundEstimatedCosts,omitempty"`
}

// UsageSupplement contains the resource facts that PromptIter cannot measure.
// Callers cannot declare model calls, tokens, or telemetry completeness.
type UsageSupplement struct {
	PromptIterLatency time.Duration `json:"promptIterLatency"`
	CostBreakdown
}

// UsageSummary stores aggregate resource consumption for audit and gates.
type UsageSummary struct {
	Calls        int   `json:"calls"`
	InputTokens  int64 `json:"inputTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty"`
	TotalTokens  int64 `json:"totalTokens"`
	CostEstimate
	PromptIterLatency time.Duration `json:"promptIterLatency"`
	// Complete means the summary covers every model-bearing optimization stage,
	// not only Evaluation execution traces.
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

// Evidence identifies the observation supporting a failure attribution.
type Evidence struct {
	Source string `json:"source"`
	Path   string `json:"path"`
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
	Phase       AttributionPhase `json:"phase,omitempty"`
	CandidateID string           `json:"candidateId,omitempty"`
	EvalSetID   string           `json:"evalSetId"`
	CaseID      string           `json:"caseId"`
	Category    FailureCategory  `json:"category"`
	Reason      string           `json:"reason"`
	Evidence    []Evidence       `json:"evidence"`
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
	Type  RuleValueType `json:"type"`
	Value string        `json:"value"`
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
	Rule      string    `json:"rule"`
	Passed    bool      `json:"passed"`
	Observed  RuleValue `json:"observed"`
	Threshold RuleValue `json:"threshold"`
	Reason    string    `json:"reason,omitempty"`
}

// GateInput contains the evidence needed for one candidate decision.
type GateInput struct {
	Spec                    *RunSpec
	PromptIterAccepted      bool
	PromptIterReason        string
	CandidateProfileValid   bool
	CandidateProfileReason  string
	CandidateProfileChanged bool
	CandidateValidation     *EvaluationSnapshot
	TrainDelta              *DeltaReport
	ValidationDelta         *DeltaReport
	TotalUsage              UsageSummary
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
	RoundUsage           UsageSummary        `json:"roundUsage"`
	CumulativeUsage      UsageSummary        `json:"cumulativeUsage"`
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
	// Attribute classifies one failed or execution-error training case. The
	// context controls the call; case must be non-nil and is read-only. A
	// successful result must be non-nil, identify the same case, and contain a
	// category, reason, and at least one evidence item.
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
