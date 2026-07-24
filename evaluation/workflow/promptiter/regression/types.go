//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regression adds an auditable held-out regression and release layer
// around the native Evaluation and PromptIter workflows.
package regression

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

const (
	// SchemaVersion is the report and custom-configuration schema version.
	SchemaVersion = "1.0"
	// DefaultEpsilon is used by every score comparison unless configured otherwise.
	DefaultEpsilon = 1e-9
)

// FailureCategory is a stable, machine-readable failure attribution.
type FailureCategory string

const (
	FailureResponseMismatch  FailureCategory = "response_mismatch"
	FailureWrongTool         FailureCategory = "wrong_tool"
	FailureWrongArguments    FailureCategory = "wrong_arguments"
	FailureWrongRoute        FailureCategory = "wrong_route"
	FailureInvalidFormat     FailureCategory = "invalid_format"
	FailureKnowledgeRecall   FailureCategory = "knowledge_recall_failure"
	FailureInsufficient      FailureCategory = "insufficient_evidence"
	FailureAmbiguousEvidence FailureCategory = "ambiguous_evidence"
)

// FailureSeverity describes the release impact of one failed metric.
type FailureSeverity string

const (
	FailureSeverityP0 FailureSeverity = "P0"
	FailureSeverityP1 FailureSeverity = "P1"
	FailureSeverityP2 FailureSeverity = "P2"
	FailureSeverityP3 FailureSeverity = "P3"
)

// EvidenceSufficiency states whether attribution evidence supports a conclusion.
type EvidenceSufficiency string

const (
	EvidenceSufficient   EvidenceSufficiency = "sufficient"
	EvidencePartial      EvidenceSufficiency = "partial"
	EvidenceInsufficient EvidenceSufficiency = "insufficient"
	EvidenceAmbiguous    EvidenceSufficiency = "ambiguous"
)

// EvidenceReference points to bounded evidence retained in an evaluation snapshot.
type EvidenceReference struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

// FailureAttribution explains a failed metric using evidence from one exact snapshot.
type FailureAttribution struct {
	EvalSetID           string              `json:"evalSetId"`
	EvalCaseID          string              `json:"evalCaseId"`
	MetricName          string              `json:"metricName"`
	PrimaryCategory     FailureCategory     `json:"primaryCategory"`
	SecondaryCategories []FailureCategory   `json:"secondaryCategories,omitempty"`
	Reason              string              `json:"reason"`
	Evidence            []EvidenceReference `json:"evidence"`
	Severity            FailureSeverity     `json:"severity"`
	Confidence          float64             `json:"confidence"`
	EvidenceSufficiency EvidenceSufficiency `json:"evidenceSufficiency"`
	EvaluationRunID     string              `json:"evaluationRunId"`
	ProfileHash         string              `json:"profileHash"`
}

// AttributionInput is the exact case and metric evidence to classify.
type AttributionInput struct {
	Snapshot *EvaluationSnapshot
	Case     CaseResult
	Metric   MetricResult
}

// ScoreDirection defines whether a larger or smaller metric score is better.
type ScoreDirection string

const (
	ScoreHigherIsBetter ScoreDirection = "higher_is_better"
	ScoreLowerIsBetter  ScoreDirection = "lower_is_better"
)

// ToolCall is one observed or expected tool invocation in execution order.
type ToolCall struct {
	Sequence  int    `json:"sequence"`
	Name      string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
	Result    any    `json:"result,omitempty"`
}

// TraceStep is the bounded trace evidence retained for one execution step.
type TraceStep struct {
	StepID             string   `json:"stepId"`
	NodeID             string   `json:"nodeId,omitempty"`
	AgentName          string   `json:"agentName,omitempty"`
	Branch             string   `json:"branch,omitempty"`
	PredecessorStepIDs []string `json:"predecessorStepIds,omitempty"`
	AppliedSurfaceIDs  []string `json:"appliedSurfaceIds,omitempty"`
	Input              string   `json:"input,omitempty"`
	Output             string   `json:"output,omitempty"`
	Error              string   `json:"error,omitempty"`
}

// MetricResult retains native evaluator semantics for one case metric.
type MetricResult struct {
	MetricName   string         `json:"metricName"`
	Score        float64        `json:"score"`
	Status       string         `json:"status"`
	Passed       bool           `json:"passed"`
	Threshold    float64        `json:"threshold"`
	Direction    ScoreDirection `json:"direction"`
	Reason       string         `json:"reason,omitempty"`
	RubricScores []RubricScore  `json:"rubricScores,omitempty"`
}

// RubricScore is one native rubric score and explanation.
type RubricScore struct {
	ID     string  `json:"id"`
	Reason string  `json:"reason,omitempty"`
	Score  float64 `json:"score"`
}

// CaseResult contains scores, execution state, and attribution evidence.
type CaseResult struct {
	EvalSetID        string         `json:"evalSetId"`
	CaseID           string         `json:"caseId"`
	Status           string         `json:"status"`
	Passed           bool           `json:"passed"`
	HardFailure      bool           `json:"hardFailure,omitempty"`
	Critical         bool           `json:"critical,omitempty"`
	PrimaryMetric    string         `json:"primaryMetric"`
	Metrics          []MetricResult `json:"metrics"`
	FinalResponse    string         `json:"finalResponse,omitempty"`
	ExpectedResponse string         `json:"expectedResponse,omitempty"`
	StructuredOutput string         `json:"structuredOutput,omitempty"`
	ExpectStructured bool           `json:"expectStructured,omitempty"`
	ToolTrajectory   []ToolCall     `json:"toolTrajectory,omitempty"`
	ExpectedTools    []ToolCall     `json:"expectedTools,omitempty"`
	Route            string         `json:"route,omitempty"`
	ExpectedRoute    string         `json:"expectedRoute,omitempty"`
	ExpectedFacts    []string       `json:"expectedFacts,omitempty"`
	Trace            []TraceStep    `json:"trace,omitempty"`
	Error            string         `json:"error,omitempty"`
}

// EvaluationStatus separates completed quality results from runtime failures.
type EvaluationStatus string

const (
	EvaluationCompleted    EvaluationStatus = "completed"
	EvaluationNotEvaluable EvaluationStatus = "not_evaluable"
	EvaluationRunFailed    EvaluationStatus = "run_failed"
)

// EvaluationProvenance binds a snapshot to all comparison-relevant inputs.
type EvaluationProvenance struct {
	RunID               string `json:"runId"`
	ProfileHash         string `json:"profileHash"`
	EvalSetID           string `json:"evalSetId"`
	EvalSetHash         string `json:"evalSetHash"`
	MetricsHash         string `json:"metricsHash"`
	Split               string `json:"split"`
	Seed                int64  `json:"seed"`
	EvaluatorConfigHash string `json:"evaluatorConfigHash"`
	MetricPolicyHash    string `json:"metricPolicyHash"`
}

// ExpectedInventory is the authoritative case and metric inventory from inputs.
type ExpectedInventory struct {
	CaseIDs     []string `json:"caseIds"`
	MetricNames []string `json:"metricNames"`
}

// EvaluationSnapshot is a complete, provenance-bound evaluation result.
type EvaluationSnapshot struct {
	Status       EvaluationStatus     `json:"status"`
	Provenance   EvaluationProvenance `json:"provenance"`
	Inventory    ExpectedInventory    `json:"expectedInventory"`
	OverallScore float64              `json:"overallScore"`
	Passed       int                  `json:"passed"`
	Failed       int                  `json:"failed"`
	Cases        []CaseResult         `json:"cases"`
	Attributions []FailureAttribution `json:"attributions,omitempty"`
	Resources    ResourceUsage        `json:"resources"`
	LatencyMS    int64                `json:"latencyMs"`
	Error        string               `json:"error,omitempty"`
}

// ChangeKind is the primary mutually-exclusive case delta classification.
type ChangeKind string

const (
	ChangeNewlyPassing ChangeKind = "newly_passing"
	ChangeNewlyFailing ChangeKind = "newly_failing"
	ChangeImproved     ChangeKind = "improved"
	ChangeRegressed    ChangeKind = "regressed"
	ChangeUnchanged    ChangeKind = "unchanged"
)

// MetricDelta retains the complete before/after change for one metric.
type MetricDelta struct {
	MetricName   string         `json:"metricName"`
	BeforeScore  float64        `json:"beforeScore"`
	AfterScore   float64        `json:"afterScore"`
	Delta        float64        `json:"delta"`
	Direction    ScoreDirection `json:"direction"`
	BeforeStatus string         `json:"beforeStatus"`
	AfterStatus  string         `json:"afterStatus"`
}

// CaseDelta describes one case change between two compatible snapshots.
type CaseDelta struct {
	EvalSetID    string              `json:"evalSetId"`
	CaseID       string              `json:"caseId"`
	BeforeStatus string              `json:"beforeStatus"`
	AfterStatus  string              `json:"afterStatus"`
	BeforePassed bool                `json:"beforePassed"`
	AfterPassed  bool                `json:"afterPassed"`
	PrimaryKind  ChangeKind          `json:"primaryKind"`
	HardFailure  bool                `json:"hardFailure,omitempty"`
	Critical     bool                `json:"critical,omitempty"`
	Metrics      []MetricDelta       `json:"metrics"`
	Reason       string              `json:"reason"`
	Evidence     []EvidenceReference `json:"evidence,omitempty"`
}

// DeltaSummary aggregates compatible case-level changes.
type DeltaSummary struct {
	Comparison         string      `json:"comparison"`
	BeforeProfileHash  string      `json:"beforeProfileHash"`
	AfterProfileHash   string      `json:"afterProfileHash"`
	BeforeOverallScore float64     `json:"beforeOverallScore"`
	AfterOverallScore  float64     `json:"afterOverallScore"`
	ScoreDelta         float64     `json:"scoreDelta"`
	NewlyPassing       int         `json:"newlyPassing"`
	NewlyFailing       int         `json:"newlyFailing"`
	Improved           int         `json:"improved"`
	Regressed          int         `json:"regressed"`
	Unchanged          int         `json:"unchanged"`
	Cases              []CaseDelta `json:"cases"`
}

// DeltaSet contains every held-out comparison required for one candidate.
type DeltaSet struct {
	VsInitial      DeltaSummary `json:"vsInitial"`
	VsSearchParent DeltaSummary `json:"vsSearchParent"`
	VsReleased     DeltaSummary `json:"vsReleased"`
}

// Count records whether an integer resource measurement is available.
type Count struct {
	Available bool  `json:"available"`
	Value     int64 `json:"value"`
}

// Amount records whether a floating-point resource measurement is available.
type Amount struct {
	Available bool    `json:"available"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit,omitempty"`
}

// ResourceUsage records measured resources for one stage or cumulative run.
type ResourceUsage struct {
	ModelCalls   Count  `json:"modelCalls"`
	InputTokens  Count  `json:"inputTokens"`
	OutputTokens Count  `json:"outputTokens"`
	LatencyMS    Count  `json:"latencyMs"`
	MonetaryCost Amount `json:"monetaryCost"`
}

// ResourceEntry records one stage in the cumulative resource ledger.
type ResourceEntry struct {
	Stage       string        `json:"stage"`
	Round       int           `json:"round,omitempty"`
	Split       string        `json:"split,omitempty"`
	ProfileHash string        `json:"profileHash,omitempty"`
	Usage       ResourceUsage `json:"usage"`
	Failed      bool          `json:"failed,omitempty"`
}

// ResourceLedger contains per-stage and cumulative resource consumption.
type ResourceLedger struct {
	Entries    []ResourceEntry `json:"entries"`
	Cumulative ResourceUsage   `json:"cumulative"`
}

// DecisionStatus is the outcome of a search or release decision.
type DecisionStatus string

const (
	DecisionAccepted     DecisionStatus = "accepted"
	DecisionRejected     DecisionStatus = "rejected"
	DecisionNotEvaluable DecisionStatus = "not_evaluable"
)

// Decision keeps one decision semantic separate from all other decisions.
type Decision struct {
	Status     DecisionStatus `json:"status"`
	Reasons    []string       `json:"reasons"`
	ScoreDelta *float64       `json:"scoreDelta,omitempty"`
}

// PipelineStatus is the terminal status of the whole regression run.
type PipelineStatus string

const (
	PipelineSucceeded     PipelineStatus = "succeeded"
	PipelineRunFailed     PipelineStatus = "run_failed"
	PipelineBudgetStopped PipelineStatus = "budget_stopped"
)

// StopReason is a stable outer-loop termination reason.
type StopReason string

const (
	StopMaxRounds             StopReason = "max_rounds"
	StopBudgetExhausted       StopReason = "budget_exhausted"
	StopNoCandidate           StopReason = "no_candidate"
	StopNecessaryRunFailed    StopReason = "necessary_run_failed"
	StopRepeatedFingerprint   StopReason = "repeated_fingerprint"
	StopTrainingFailuresFixed StopReason = "training_failures_fixed"
)

// ProfileRole identifies one profile's lifecycle role.
type ProfileRole string

const (
	ProfileInitial   ProfileRole = "initial"
	ProfileSearch    ProfileRole = "search"
	ProfileReleased  ProfileRole = "released"
	ProfileCandidate ProfileRole = "candidate"
)

// ProfileRecord is an auditable prompt profile identity.
type ProfileRecord struct {
	Role            ProfileRole         `json:"role"`
	Hash            string              `json:"hash"`
	StructureID     string              `json:"structureId"`
	TargetSurfaceID string              `json:"targetSurfaceId"`
	Prompt          string              `json:"prompt"`
	Profile         *promptiter.Profile `json:"profile"`
	EvaluationRunID string              `json:"evaluationRunId,omitempty"`
}

// PatchRecord stores one PromptIter patch in report-friendly form.
type PatchRecord struct {
	SurfaceID string `json:"surfaceId"`
	Value     string `json:"value"`
	Reason    string `json:"reason"`
}

// ProfileState contains profiles and the snapshots required for future comparisons.
type ProfileState struct {
	Initial            ProfileRecord
	Search             ProfileRecord
	Released           ProfileRecord
	InitialValidation  *EvaluationSnapshot
	SearchTrain        *EvaluationSnapshot
	SearchValidation   *EvaluationSnapshot
	ReleasedValidation *EvaluationSnapshot
}

// StateTransition records how both profile pointers changed after one candidate.
type StateTransition struct {
	SearchBefore   string `json:"searchBefore"`
	SearchAfter    string `json:"searchAfter"`
	ReleasedBefore string `json:"releasedBefore"`
	ReleasedAfter  string `json:"releasedAfter"`
	SearchUpdated  bool   `json:"searchUpdated"`
	ReleaseUpdated bool   `json:"releaseUpdated"`
	Explanation    string `json:"explanation"`
}

// GatePolicy controls the held-out release decision.
type GatePolicy struct {
	PrimaryMetric           string                    `json:"primaryMetric"`
	MetricDirections        map[string]ScoreDirection `json:"metricDirections"`
	Epsilon                 float64                   `json:"epsilon"`
	MinValidationGain       float64                   `json:"minValidationGain"`
	NoNewHardFailures       bool                      `json:"noNewHardFailures"`
	NoCriticalRegressions   bool                      `json:"noCriticalRegressions"`
	MaxCumulativeModelCalls int64                     `json:"maxCumulativeModelCalls,omitempty"`
}

// PromptIterPolicy controls the native engine invocation and outer search loop.
type PromptIterPolicy struct {
	MaxOuterRounds             int      `json:"maxOuterRounds"`
	SearchMinScoreGain         float64  `json:"searchMinScoreGain"`
	InternalValidationStrategy string   `json:"internalValidationStrategy"`
	InternalValidationCaseIDs  []string `json:"internalValidationCaseIds,omitempty"`
	TargetSurfaceIDs           []string `json:"targetSurfaceIds"`
}

// PromptIterConfig is the strict on-disk PromptIter configuration.
type PromptIterConfig struct {
	SchemaVersion string           `json:"schemaVersion"`
	Seed          int64            `json:"seed"`
	Policy        PromptIterPolicy `json:"policy"`
}

// OutputConfig names the two report artifacts.
type OutputConfig struct {
	JSON     string `json:"json"`
	Markdown string `json:"markdown"`
}

// RegressionConfig is the strict on-disk release and reporting configuration.
type RegressionConfig struct {
	SchemaVersion      string       `json:"schemaVersion"`
	ReportID           string       `json:"reportId"`
	GeneratedAt        time.Time    `json:"generatedAt"`
	Gate               GatePolicy   `json:"gate"`
	CriticalCaseIDs    []string     `json:"criticalCaseIds,omitempty"`
	HardFailureCaseIDs []string     `json:"hardFailureCaseIds,omitempty"`
	EvidenceLimit      int          `json:"evidenceLimit"`
	Output             OutputConfig `json:"output"`
}

// InputFiles identifies every required source artifact.
type InputFiles struct {
	TrainEvalSet      string
	ValidationEvalSet string
	Metrics           string
	BaselinePrompt    string
	PromptIterConfig  string
	RegressionConfig  string
}

// DatasetSpec is the authoritative input inventory for one split.
type DatasetSpec struct {
	EvalSetID             string            `json:"evalSetId"`
	EvalSetHash           string            `json:"evalSetHash"`
	MetricsHash           string            `json:"metricsHash"`
	CaseIDs               []string          `json:"caseIds"`
	MetricNames           []string          `json:"metricNames"`
	NormalizedInputHashes map[string]string `json:"normalizedInputHashes"`
}

// RuntimeConfig makes fake/model execution reproducible and auditable.
type RuntimeConfig struct {
	Engine     string         `json:"engine"`
	Seed       int64          `json:"seed"`
	Model      map[string]any `json:"model"`
	Evaluator  map[string]any `json:"evaluator"`
	FakeEngine map[string]any `json:"fakeEngine,omitempty"`
}

// RunConfig contains resolved in-memory inputs for one pipeline run.
type RunConfig struct {
	ReportID            string
	RunID               string
	GeneratedAt         time.Time
	Seed                int64
	InitialProfile      *promptiter.Profile
	Train               DatasetSpec
	Validation          DatasetSpec
	PromptIter          PromptIterPolicy
	Gate                GatePolicy
	Output              OutputConfig
	InputHashes         map[string]string
	EvaluatorConfigHash string
	MetricPolicyHash    string
	Runtime             RuntimeConfig
	CriticalCaseIDs     []string
	HardFailureCaseIDs  []string
	EvidenceLimit       int
	sourceConfigHash    string
	executionNonce      string
}

// ResolvedConfig is the serializable configuration saved in the report.
type ResolvedConfig struct {
	Seed               int64            `json:"seed"`
	Train              DatasetSpec      `json:"train"`
	Validation         DatasetSpec      `json:"validation"`
	PromptIter         PromptIterPolicy `json:"promptIter"`
	Gate               GatePolicy       `json:"gate"`
	Output             OutputConfig     `json:"output"`
	CriticalCaseIDs    []string         `json:"criticalCaseIds,omitempty"`
	HardFailureCaseIDs []string         `json:"hardFailureCaseIds,omitempty"`
	EvidenceLimit      int              `json:"evidenceLimit"`
}

// CandidateReport is the complete audit record for one PromptIter candidate.
type CandidateReport struct {
	Round              int                 `json:"round"`
	ID                 string              `json:"id"`
	Status             EvaluationStatus    `json:"status"`
	SearchParentHash   string              `json:"searchParentHash"`
	ReleasedParentHash string              `json:"releasedParentHash"`
	Profile            *ProfileRecord      `json:"profile,omitempty"`
	Patches            []PatchRecord       `json:"patches,omitempty"`
	OptimizationReason string              `json:"optimizationReason,omitempty"`
	PromptIterRunID    string              `json:"promptIterRunId,omitempty"`
	PromptIterStatus   string              `json:"promptIterStatus,omitempty"`
	Train              *EvaluationSnapshot `json:"train,omitempty"`
	Validation         *EvaluationSnapshot `json:"validation,omitempty"`
	Deltas             *DeltaSet           `json:"deltas,omitempty"`
	SearchDecision     Decision            `json:"searchDecision"`
	ReleaseDecision    Decision            `json:"releaseDecision"`
	Transition         StateTransition     `json:"transition"`
	Resources          ResourceLedger      `json:"resources"`
	Errors             []string            `json:"errors,omitempty"`
}

// ArtifactReferences records the canonical report output paths.
type ArtifactReferences struct {
	JSON     string `json:"json"`
	Markdown string `json:"markdown"`
}

// Report is the common in-memory model rendered to JSON and Markdown.
type Report struct {
	SchemaVersion      string              `json:"schemaVersion"`
	ReportID           string              `json:"reportId"`
	RunID              string              `json:"runId"`
	GeneratedAt        time.Time           `json:"generatedAt"`
	Status             PipelineStatus      `json:"status"`
	StopReason         StopReason          `json:"stopReason"`
	ResolvedConfig     ResolvedConfig      `json:"resolvedConfig"`
	InputHashes        map[string]string   `json:"inputHashes"`
	Runtime            RuntimeConfig       `json:"runtime"`
	InitialProfile     ProfileRecord       `json:"initialProfile"`
	SearchProfile      ProfileRecord       `json:"searchProfile"`
	ReleasedProfile    ProfileRecord       `json:"releasedProfile"`
	BaselineTrain      *EvaluationSnapshot `json:"baselineTrain,omitempty"`
	BaselineValidation *EvaluationSnapshot `json:"baselineValidation,omitempty"`
	Candidates         []CandidateReport   `json:"candidates"`
	FinalDecision      Decision            `json:"finalDecision"`
	Resources          ResourceLedger      `json:"resources"`
	Errors             []string            `json:"errors,omitempty"`
	Artifacts          ArtifactReferences  `json:"artifacts"`
}
