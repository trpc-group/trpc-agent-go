//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package pipeline implements the Evaluation + Optimization closed-loop pipeline,
// covering baseline evaluation, failure attribution, PromptIter-based prompt
// optimization, candidate validation, configurable acceptance gates, and audit
// persistence.
//
// The pipeline integrates with the real trpc-agent-go evaluation framework and
// the evaluation/workflow/promptiter package. In fake_deterministic mode it
// produces deterministic outcomes using the same type contracts so the pipeline
// runs end-to-end without API keys. In trace_mode and real mode it delegates to
// the real evaluation.AgentEvaluator and promptiter engine.
package pipeline

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// FailureCategory classifies the root cause of a failed eval case.
type FailureCategory string

const (
	// FinalResponseMismatch indicates the agent's final answer does not match the expected output.
	FinalResponseMismatch FailureCategory = "final_response_mismatch"
	// ToolCallError indicates the agent called a tool that returned an error or failed to execute.
	ToolCallError FailureCategory = "tool_call_error"
	// ToolArgumentError indicates the agent supplied incorrect arguments to a tool.
	ToolArgumentError FailureCategory = "tool_argument_error"
	// RouteError indicates the agent routed to the wrong sub-agent, skill, or workflow.
	RouteError FailureCategory = "route_error"
	// FormatError indicates the output format (JSON, markdown, etc.) is malformed.
	FormatError FailureCategory = "format_error"
	// KnowledgeRecallInsufficient indicates the agent did not recall required knowledge.
	KnowledgeRecallInsufficient FailureCategory = "knowledge_recall_insufficient"
	// UnknownFailure indicates a failure that could not be classified.
	UnknownFailure FailureCategory = "unknown"
)

// FailureAttribution stores the classified failure reason and evidence for one case metric.
type FailureAttribution struct {
	// EvalCaseID identifies the eval case this attribution belongs to.
	EvalCaseID string `json:"evalCaseId"`
	// MetricName identifies the failing metric.
	MetricName string `json:"metricName"`
	// Category is the classified failure root cause.
	Category FailureCategory `json:"category"`
	// Severity is the promptiter.LossSeverity level (P0=blocker..P3=minor).
	// It uses the real promptiter.LossSeverity type so attributions can be
	// directly converted to promptiter.TerminalLoss for the backwarder stage.
	Severity promptiter.LossSeverity `json:"severity,omitempty"`
	// Reason is the human-readable explanation for this classification.
	Reason string `json:"reason"`
	// Evidence captures key signals extracted from trace, response, or trajectory.
	Evidence []string `json:"evidence,omitempty"`
}

// PromptTarget identifies a specific prompt surface being optimized.
type PromptTarget struct {
	// SurfaceID is a stable identifier for this prompt target (e.g. "system_prompt", "router_instruction").
	SurfaceID string `json:"surfaceId"`
	// Description briefly explains what this prompt controls.
	Description string `json:"description"`
	// BaselineText stores the baseline prompt content before optimization.
	BaselineText string `json:"baselineText"`
}

// PromptCandidate stores a candidate optimization for one or more prompt targets.
type PromptCandidate struct {
	// CandidateID uniquely identifies this candidate proposal.
	CandidateID string `json:"candidateId"`
	// Round is the optimization round index that produced this candidate.
	Round int `json:"round"`
	// GeneratedBy indicates the optimization source (e.g. "promptiter_v1" or "fake_deterministic").
	GeneratedBy string `json:"generatedBy"`
	// Patches maps SurfaceID -> replacement prompt text.
	Patches map[string]string `json:"patches"`
	// Rationale briefly explains why the optimizer proposed these changes.
	Rationale string `json:"rationale,omitempty"`
	// PatchSet stores the real evaluation/workflow/promptiter.PatchSet produced
	// by the optimizer. This is the native promptiter representation and is kept
	// in sync with Patches. In fake_deterministic mode it is still populated so
	// downstream consumers can rely on the same type contract.
	PatchSet *promptiter.PatchSet `json:"-"`
	// Profile stores the real promptiter.Profile that this candidate represents.
	// It is built from Patches via BuildProfile and can be fed directly to the
	// promptiter engine or evaluation service.
	Profile *promptiter.Profile `json:"-"`
}

// CaseMetric stores a single metric value for one eval case.
type CaseMetric struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Threshold  float64 `json:"threshold"`
	Passed     bool    `json:"passed"`
	// EvalStatus stores the real evaluation/status.EvalStatus for this metric.
	// It mirrors Passed (Passed => status.EvalStatusPassed) and enables direct
	// interoperability with the evaluation framework's result types.
	EvalStatus status.EvalStatus `json:"evalStatus,omitempty"`
	Reason     string            `json:"reason,omitempty"`
}

// CaseEval stores per-case evaluation output.
type CaseEval struct {
	EvalCaseID     string       `json:"evalCaseId"`
	EvalSetID      string       `json:"evalSetId"`
	OverallPassed  bool         `json:"overallPassed"`
	Metrics        []CaseMetric `json:"metrics"`
	SessionID      string       `json:"sessionId,omitempty"`
	FinalResponse  string       `json:"finalResponse,omitempty"`
	ToolTrajectory []ToolStep   `json:"toolTrajectory,omitempty"`
	TraceID        string       `json:"traceId,omitempty"`
}

// ToolStep describes a single tool invocation used during failure attribution.
type ToolStep struct {
	ToolName string         `json:"toolName"`
	Args     map[string]any `json:"args,omitempty"`
	Error    string         `json:"error,omitempty"`
	Output   string         `json:"output,omitempty"`
}

// EvalSummary is the aggregate view of an evaluation run (train or validation).
type EvalSummary struct {
	// EvalSetID is the eval set that was scored.
	EvalSetID string `json:"evalSetId"`
	// TotalCases is the number of cases evaluated.
	TotalCases int `json:"totalCases"`
	// PassedCases is the count of cases that passed all metrics.
	PassedCases int `json:"passedCases"`
	// FailedCases is the count of cases that failed at least one metric.
	FailedCases int `json:"failedCases"`
	// OverallScore is the overall aggregated metric score (mean across all metrics).
	OverallScore float64 `json:"overallScore"`
	// PerCase stores detailed case-level results.
	PerCase []CaseEval `json:"perCase"`
	// Attribution stores failure attributions for all failing metrics.
	Attribution []FailureAttribution `json:"attribution,omitempty"`
}

// CaseDelta captures the per-case change between baseline and candidate evaluation.
type CaseDelta struct {
	EvalCaseID       string   `json:"evalCaseId"`
	BaselinePassed   bool     `json:"baselinePassed"`
	CandidatePassed  bool     `json:"candidatePassed"`
	BaselineScore    float64  `json:"baselineScore"`
	CandidateScore   float64  `json:"candidateScore"`
	ScoreDelta       float64  `json:"scoreDelta"`
	IsHardFailNew    bool     `json:"isHardFailNew"`
	IsKeyCaseDegrade bool     `json:"isKeyCaseDegrade"`
	Labels           []string `json:"labels,omitempty"`
}

// AcceptanceGateConfig defines the configurable gates applied before accepting a candidate.
type AcceptanceGateConfig struct {
	// MinValidationScoreGain is the minimum overall validation score increase required.
	MinValidationScoreGain float64 `json:"minValidationScoreGain"`
	// AllowNewHardFail controls whether a candidate that turns a passing case into failing is acceptable.
	AllowNewHardFail bool `json:"allowNewHardFail"`
	// KeyCaseIDs lists eval case IDs that must not degrade (score must not fall).
	KeyCaseIDs []string `json:"keyCaseIds,omitempty"`
	// MaxCostBudget is the maximum token/unit cost allowed per candidate (0 means no budget check).
	MaxCostBudget float64 `json:"maxCostBudget,omitempty"`
}

// AcceptanceDecision is the gate result.
type AcceptanceDecision struct {
	// Accepted is true if all gates passed.
	Accepted bool `json:"accepted"`
	// ScoreDelta is the overall validation score delta (candidate - baseline).
	ScoreDelta float64 `json:"scoreDelta"`
	// Reasons lists human-readable decisions for each gate.
	Reasons []string `json:"reasons"`
	// PerCaseDelta stores per-case deltas.
	PerCaseDelta []CaseDelta `json:"perCaseDelta"`
	// HardFailNewCount counts newly-introduced hard fails (pass -> fail).
	HardFailNewCount int `json:"hardFailNewCount"`
	// KeyCaseDegradeCount counts key cases that regressed.
	KeyCaseDegradeCount int `json:"keyCaseDegradeCount"`
	// Cost within or over budget (relevant when MaxCostBudget > 0).
	Cost float64 `json:"cost,omitempty"`
}

// CostEstimate captures runtime cost and timing for a round.
type CostEstimate struct {
	InferenceTokens   int           `json:"inferenceTokens,omitempty"`
	JudgeTokens       int           `json:"judgeTokens,omitempty"`
	OptimizerTokens   int           `json:"optimizerTokens,omitempty"`
	TotalTokens       int           `json:"totalTokens,omitempty"`
	EstimatedCostUSD  float64       `json:"estimatedCostUsd,omitempty"`
	WallClockDuration time.Duration `json:"wallClockDurationNs"`
}

// RoundRecord is the audit record for one optimization round.
type RoundRecord struct {
	Round         int                  `json:"round"`
	Timestamp     time.Time            `json:"timestamp"`
	RandomSeed    int64                `json:"randomSeed"`
	TrainSummary  *EvalSummary         `json:"trainSummary,omitempty"`
	ValBaseline   *EvalSummary         `json:"valBaseline,omitempty"`
	Attributions  []FailureAttribution `json:"attributions,omitempty"`
	Candidate     *PromptCandidate     `json:"candidate,omitempty"`
	ValCandidate  *EvalSummary         `json:"valCandidate,omitempty"`
	PerCaseDelta  []CaseDelta          `json:"perCaseDelta,omitempty"`
	Acceptance    *AcceptanceDecision  `json:"acceptance"`
	Cost          CostEstimate         `json:"cost"`
	PromptsBefore map[string]string    `json:"promptsBefore"`
	PromptsAfter  map[string]string    `json:"promptsAfter,omitempty"`
}

// OptimizationReport is the final JSON/MD audit report persisted to disk.
type OptimizationReport struct {
	// AppName identifies the agent target.
	AppName string `json:"appName"`
	// PipelineVersion identifies the pipeline version.
	PipelineVersion string `json:"pipelineVersion"`
	// Mode records the runner mode: "fake_deterministic", "trace_mode", or "real".
	Mode string `json:"mode"`
	// StartedAt and FinishedAt capture the timing window.
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	// RandomSeed is the top-level seed used for deterministic runs.
	RandomSeed int64 `json:"randomSeed"`
	// PromptTargets lists the surfaces that were candidates for optimization.
	PromptTargets []PromptTarget `json:"promptTargets"`
	// GateConfig is the acceptance gate configuration used.
	GateConfig AcceptanceGateConfig `json:"gateConfig"`
	// BaselineTrain and BaselineVal capture baseline evaluation summaries.
	BaselineTrain *EvalSummary `json:"baselineTrain"`
	BaselineVal   *EvalSummary `json:"baselineVal"`
	// Rounds stores the per-round audit history.
	Rounds []RoundRecord `json:"rounds"`
	// FinalAccepted indicates whether at least one round was accepted.
	FinalAccepted bool `json:"finalAccepted"`
	// FinalValidationScore is the last accepted validation score (or baseline).
	FinalValidationScore float64 `json:"finalValidationScore"`
	// BestValidationScore and BestRound track the best score observed.
	BestValidationScore float64 `json:"bestValidationScore"`
	BestRound           int     `json:"bestRound"`
	// PromptsFinal stores the final accepted prompt values per surface.
	PromptsFinal map[string]string `json:"promptsFinal"`
	// TotalCost aggregates cost across all rounds.
	TotalCost CostEstimate `json:"totalCost"`
	// Notes stores free-form annotations.
	Notes []string `json:"notes,omitempty"`
}

// Mode enumerates supported runner modes for the pipeline.
type Mode string

const (
	// ModeFakeDeterministic uses pre-programmed deterministic outcomes (no LLM calls).
	ModeFakeDeterministic Mode = "fake_deterministic"
	// ModeTraceMode uses the trpc-agent trace mode to replay traces.
	ModeTraceMode Mode = "trace_mode"
	// ModeReal uses live model API calls (requires API keys, not the default).
	ModeReal Mode = "real"
)

// Config bundles user-facing pipeline configuration.
type Config struct {
	AppName          string
	Mode             Mode
	DataDir          string
	OutputDir        string
	TrainSetID       string
	ValSetID         string
	MaxRounds        int
	GateConfig       AcceptanceGateConfig
	RandomSeed       int64
	TargetSurfaceIDs []string
	PromptsBaseline  map[string]string
}
