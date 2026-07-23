//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regression implements an auditable evaluation and PromptIter
// regression loop. It complements PromptIter's aggregate-score acceptance with
// per-case attribution, validation gates, cost budgets, and report generation.
package regression

import (
	"errors"
	"fmt"
	"math"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// FailureCategory is a stable, machine-readable failure attribution label.
type FailureCategory string

const (
	// FailureFinalResponseMismatch means the final response did not satisfy the reference or rubric.
	FailureFinalResponseMismatch FailureCategory = "final_response_mismatch"
	// FailureToolCallError means a required tool was missing, extra, or the wrong tool was selected.
	FailureToolCallError FailureCategory = "tool_call_error"
	// FailureToolParameterError means a correct tool was called with incorrect arguments.
	FailureToolParameterError FailureCategory = "tool_parameter_error"
	// FailureRouteError means the execution selected an incorrect route or sub-agent.
	FailureRouteError FailureCategory = "route_error"
	// FailureFormatError means the response violated a required structured-output format.
	FailureFormatError FailureCategory = "format_error"
	// FailureKnowledgeRetrievalInsufficient means required evidence was not retrieved.
	FailureKnowledgeRetrievalInsufficient FailureCategory = "knowledge_retrieval_insufficient"
)

// TraceStep is a compact, serializable trace signal used by the offline runner
// and written to the audit report.
type TraceStep struct {
	StepID       string         `json:"stepId"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name,omitempty"`
	Status       string         `json:"status,omitempty"`
	ElapsedMS    *int64         `json:"elapsedMs,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	Output       map[string]any `json:"output,omitempty"`
	Message      string         `json:"message,omitempty"`
	RubricScore  *float64       `json:"rubricScore,omitempty"`
	RubricReason string         `json:"rubricReason,omitempty"`
	Usage        *Usage         `json:"usage,omitempty"`
}

// Usage captures deterministic or provider-reported execution consumption.
type Usage struct {
	ModelCalls   int     `json:"modelCalls"`
	ToolCalls    int     `json:"toolCalls"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd"`
	LatencyMS    int64   `json:"latencyMs"`
}

// UsageDelta is a signed candidate-minus-baseline consumption difference.
type UsageDelta struct {
	ModelCalls   int     `json:"modelCalls"`
	ToolCalls    int     `json:"toolCalls"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd"`
	LatencyMS    int64   `json:"latencyMs"`
}

// AddChecked returns the element-wise sum and rejects invalid values or integer overflow.
func (u Usage) AddChecked(other Usage) (Usage, error) {
	if err := validateUsage(u); err != nil {
		return Usage{}, fmt.Errorf("left usage: %w", err)
	}
	if err := validateUsage(other); err != nil {
		return Usage{}, fmt.Errorf("right usage: %w", err)
	}
	modelCalls, err := checkedAddInt(u.ModelCalls, other.ModelCalls)
	if err != nil {
		return Usage{}, fmt.Errorf("model calls: %w", err)
	}
	toolCalls, err := checkedAddInt(u.ToolCalls, other.ToolCalls)
	if err != nil {
		return Usage{}, fmt.Errorf("tool calls: %w", err)
	}
	inputTokens, err := checkedAddInt(u.InputTokens, other.InputTokens)
	if err != nil {
		return Usage{}, fmt.Errorf("input tokens: %w", err)
	}
	outputTokens, err := checkedAddInt(u.OutputTokens, other.OutputTokens)
	if err != nil {
		return Usage{}, fmt.Errorf("output tokens: %w", err)
	}
	latency, err := checkedAddInt64(u.LatencyMS, other.LatencyMS)
	if err != nil {
		return Usage{}, fmt.Errorf("latency: %w", err)
	}
	cost := u.CostUSD + other.CostUSD
	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		return Usage{}, errors.New("cost overflow")
	}
	return Usage{
		ModelCalls:   modelCalls,
		ToolCalls:    toolCalls,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostUSD:      cost,
		LatencyMS:    latency,
	}, nil
}

// Expectations holds evaluation signals that are not represented by the
// standard evalset.Invocation schema.
type Expectations struct {
	Route                 string   `json:"route,omitempty"`
	ResponseFormat        string   `json:"responseFormat,omitempty"`
	RequiredFacts         []string `json:"requiredFacts,omitempty"`
	MinRetrievedDocuments int      `json:"minRetrievedDocuments,omitempty"`
}

// FakeOutput is one deterministic model/trace-mode output variant. Its
// FinalResponse and Tools fields intentionally mirror evalset.Invocation.
type FakeOutput struct {
	Response             string          `json:"response,omitempty"`
	Tools                []*evalset.Tool `json:"tools,omitempty"`
	Route                string          `json:"route,omitempty"`
	RetrievedFacts       []string        `json:"retrievedFacts,omitempty"`
	RetrievedDocuments   int             `json:"retrievedDocuments,omitempty"`
	StructuredValid      *bool           `json:"structuredValid,omitempty"`
	RubricScore          *float64        `json:"rubricScore,omitempty"`
	RubricReason         string          `json:"rubricReason,omitempty"`
	Error                string          `json:"error,omitempty"`
	PromptSemanticSHA256 string          `json:"promptSemanticSha256,omitempty"`
	Trace                []TraceStep     `json:"trace,omitempty"`
	Usage                Usage           `json:"usage"`
}

// EvalCase is a standard-evalset-compatible case plus deterministic fake
// outputs. Conversation is the expected reference transcript.
type EvalCase struct {
	EvalID                string                        `json:"evalId"`
	EvalMode              evalset.EvalMode              `json:"evalMode,omitempty"`
	ExpectedRunnerEnabled bool                          `json:"expectedRunnerEnabled,omitempty"`
	ContextMessages       []*model.Message              `json:"contextMessages,omitempty"`
	Conversation          []*evalset.Invocation         `json:"conversation"`
	ConversationScenario  *evalset.ConversationScenario `json:"conversationScenario,omitempty"`
	ActualConversation    []*evalset.Invocation         `json:"actualConversation,omitempty"`
	SessionInput          *evalset.SessionInput         `json:"sessionInput,omitempty"`
	Rubrics               []*evalset.EvalCaseRubric     `json:"rubrics,omitempty"`
	CreationTimestamp     *epochtime.EpochTime          `json:"creationTimestamp,omitempty"`
	Critical              bool                          `json:"critical,omitempty"`
	Tags                  []string                      `json:"tags,omitempty"`
	Expectations          Expectations                  `json:"expectations,omitempty"`
	FakeResponses         map[string]FakeOutput         `json:"fakeResponses"`
}

// EvalSet is the on-disk train or validation evaluation set.
type EvalSet struct {
	EvalSetID         string               `json:"evalSetId"`
	Name              string               `json:"name,omitempty"`
	Description       string               `json:"description,omitempty"`
	CreationTimestamp *epochtime.EpochTime `json:"creationTimestamp,omitempty"`
	PassThreshold     *float64             `json:"passThreshold,omitempty"`
	EvalCases         []EvalCase           `json:"evalCases"`
}

// MetricConfig configures a local deterministic metric. MetricName uses the
// same names as the Evaluation Service where possible.
type MetricConfig struct {
	MetricName string  `json:"metricName"`
	Threshold  float64 `json:"threshold"`
	Weight     float64 `json:"weight"`
	HardFail   bool    `json:"hardFail,omitempty"`
}

// MetricResult is one scored metric with its pass status and evidence.
type MetricResult struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Threshold  float64 `json:"threshold"`
	Weight     float64 `json:"weight"`
	Passed     bool    `json:"passed"`
	HardFail   bool    `json:"hardFail"`
	Reason     string  `json:"reason"`
}

// Attribution explains one categorized failure and the evidence used.
type Attribution struct {
	Category   FailureCategory `json:"category"`
	Confidence float64         `json:"confidence"`
	Evidence   string          `json:"evidence"`
	Signals    []string        `json:"signals"`
}

// CaseResult stores all case-level signals required for delta and attribution.
type CaseResult struct {
	CaseID                 string          `json:"caseId"`
	Critical               bool            `json:"critical"`
	Score                  float64         `json:"score"`
	Passed                 bool            `json:"passed"`
	HardFail               bool            `json:"hardFail"`
	Error                  string          `json:"error,omitempty"`
	ResponseVariantID      string          `json:"responseVariantId"`
	ResponsePromptSHA256   string          `json:"responsePromptSemanticSha256,omitempty"`
	UsedFallback           bool            `json:"usedFallback"`
	MetricResults          []MetricResult  `json:"metricResults"`
	ExpectedResponse       string          `json:"expectedResponse,omitempty"`
	FinalResponse          string          `json:"finalResponse,omitempty"`
	ExpectedToolTrajectory []*evalset.Tool `json:"expectedToolTrajectory,omitempty"`
	ToolTrajectory         []*evalset.Tool `json:"toolTrajectory,omitempty"`
	ExpectedRoute          string          `json:"expectedRoute,omitempty"`
	Route                  string          `json:"route,omitempty"`
	ResponseFormat         string          `json:"responseFormat,omitempty"`
	StructuredValid        bool            `json:"structuredValid"`
	RequiredFacts          []string        `json:"requiredFacts,omitempty"`
	RetrievedFacts         []string        `json:"retrievedFacts,omitempty"`
	MinRetrievedDocuments  int             `json:"minRetrievedDocuments,omitempty"`
	RetrievedDocuments     int             `json:"retrievedDocuments,omitempty"`
	RubricReason           string          `json:"rubricReason,omitempty"`
	Trace                  []TraceStep     `json:"trace,omitempty"`
	PrimaryFailure         *Attribution    `json:"primaryFailure,omitempty"`
	FailureAttributions    []Attribution   `json:"failureAttributions,omitempty"`
	Usage                  Usage           `json:"usage"`
}

// EvaluationSummary aggregates a single prompt's results on one split.
type EvaluationSummary struct {
	EvalSetID        string                  `json:"evalSetId"`
	VariantID        string                  `json:"variantId"`
	PassThreshold    float64                 `json:"passThreshold"`
	OverallScore     float64                 `json:"overallScore"`
	PassedCases      int                     `json:"passedCases"`
	FailedCases      int                     `json:"failedCases"`
	HardFailedCases  int                     `json:"hardFailedCases"`
	Cases            []CaseResult            `json:"cases"`
	AttributionStats map[FailureCategory]int `json:"attributionStats"`
	Usage            Usage                   `json:"usage"`
}

// DeltaOutcome is the primary status transition for a case.
type DeltaOutcome string

const (
	DeltaNewPass          DeltaOutcome = "new_pass"
	DeltaNewFailure       DeltaOutcome = "new_failure"
	DeltaImproved         DeltaOutcome = "improved"
	DeltaRegressed        DeltaOutcome = "regressed"
	DeltaUnchangedPass    DeltaOutcome = "unchanged_pass"
	DeltaUnchangedFailure DeltaOutcome = "unchanged_failure"
	DeltaMissingCandidate DeltaOutcome = "missing_candidate"
	DeltaUnexpectedCase   DeltaOutcome = "unexpected_candidate_case"
)

// MetricDelta captures the score change for one metric.
type MetricDelta struct {
	MetricName     string  `json:"metricName"`
	BaselineScore  float64 `json:"baselineScore"`
	CandidateScore float64 `json:"candidateScore"`
	ScoreDelta     float64 `json:"scoreDelta"`
}

// CaseDelta compares baseline and candidate for one case.
type CaseDelta struct {
	CaseID            string        `json:"caseId"`
	BaselinePresent   bool          `json:"baselinePresent"`
	CandidatePresent  bool          `json:"candidatePresent"`
	BaselineScore     float64       `json:"baselineScore"`
	CandidateScore    float64       `json:"candidateScore"`
	ScoreDelta        float64       `json:"scoreDelta"`
	BaselinePassed    bool          `json:"baselinePassed"`
	CandidatePassed   bool          `json:"candidatePassed"`
	BaselineHardFail  bool          `json:"baselineHardFail"`
	CandidateHardFail bool          `json:"candidateHardFail"`
	Critical          bool          `json:"critical"`
	Outcome           DeltaOutcome  `json:"outcome"`
	BecamePassed      bool          `json:"becamePassed"`
	BecameFailed      bool          `json:"becameFailed"`
	ScoreImproved     bool          `json:"scoreImproved"`
	ScoreRegressed    bool          `json:"scoreRegressed"`
	NewHardFail       bool          `json:"newHardFail"`
	MetricDeltas      []MetricDelta `json:"metricDeltas"`
}

// DeltaSummary contains validation or training case-by-case regression data.
type DeltaSummary struct {
	BaselineScore     float64     `json:"baselineScore"`
	CandidateScore    float64     `json:"candidateScore"`
	ScoreDelta        float64     `json:"scoreDelta"`
	NewPasses         int         `json:"newPasses"`
	NewFailures       int         `json:"newFailures"`
	ScoreImprovements int         `json:"scoreImprovements"`
	ScoreRegressions  int         `json:"scoreRegressions"`
	NewHardFails      int         `json:"newHardFails"`
	Complete          bool        `json:"complete"`
	CoverageIssues    []string    `json:"coverageIssues,omitempty"`
	Cases             []CaseDelta `json:"cases"`
}

// GatePolicy configures candidate acceptance. Pointer budgets distinguish an
// omitted check from a zero-valued limit.
type GatePolicy struct {
	MinValidationScoreGain float64  `json:"minValidationScoreGain"`
	RejectNewHardFails     bool     `json:"rejectNewHardFails"`
	MaxNewFailures         *int     `json:"maxNewFailures,omitempty"`
	CriticalCaseIDs        []string `json:"criticalCaseIds,omitempty"`
	MaxCriticalScoreDrop   float64  `json:"maxCriticalScoreDrop"`
	MaxPerCaseScoreDrop    *float64 `json:"maxPerCaseScoreDrop,omitempty"`
	MaxCostUSD             *float64 `json:"maxCostUsd,omitempty"`
	MaxCostIncreaseRatio   *float64 `json:"maxCostIncreaseRatio,omitempty"`
	MaxModelCalls          *int     `json:"maxModelCalls,omitempty"`
	MaxTotalCalls          *int     `json:"maxTotalCalls,omitempty"`
	MaxLatencyMS           *int64   `json:"maxLatencyMs,omitempty"`
}

// GateCheck is an auditable predicate evaluated by the acceptance gate.
type GateCheck struct {
	Name       string  `json:"name"`
	Passed     bool    `json:"passed"`
	Actual     float64 `json:"actual"`
	Comparator string  `json:"comparator"`
	Limit      float64 `json:"limit"`
	Reason     string  `json:"reason"`
}

// GateDecision is the final accept/reject decision for one candidate.
type GateDecision struct {
	Accepted bool        `json:"accepted"`
	Checks   []GateCheck `json:"checks"`
	Reasons  []string    `json:"reasons"`
}

// GateInput contains the data required for a fail-closed acceptance decision.
type GateInput struct {
	Delta               *DeltaSummary
	BaselineValidation  *EvaluationSummary
	CandidateValidation *EvaluationSummary
	BaselineUsage       Usage
	CandidateUsage      Usage
	BaselinePromptHash  string
	CandidatePromptHash string
}

// SurfaceConfig identifies the PromptIter surface optimized by the offline adapter.
type SurfaceConfig struct {
	StructureID string `json:"structureId"`
	NodeID      string `json:"nodeId"`
	Type        string `json:"type"`
}

// CandidateConfig is a deterministic PromptIter patch proposal.
type CandidateConfig struct {
	ID                string            `json:"id"`
	AppendPrompt      string            `json:"appendPrompt"`
	Reason            string            `json:"reason"`
	AddressCategories []FailureCategory `json:"addressCategories,omitempty"`
}

// FakeEngineConfig is persisted in the report for reproducibility.
type FakeEngineConfig struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	FallbackVariant string `json:"fallbackVariant"`
}

// Config is the promptiter.json schema used by the deterministic loop.
type Config struct {
	SchemaVersion string            `json:"schemaVersion"`
	RunID         string            `json:"runId,omitempty"`
	Mode          string            `json:"mode"`
	Seed          int64             `json:"seed"`
	MaxRounds     int               `json:"maxRounds"`
	Surface       SurfaceConfig     `json:"surface"`
	Candidates    []CandidateConfig `json:"candidates"`
	Gate          GatePolicy        `json:"gate"`
	FakeEngine    FakeEngineConfig  `json:"fakeEngine"`
}

// Candidate is a concrete PromptIter profile and patch proposal.
type Candidate struct {
	ID         string               `json:"id"`
	Round      int                  `json:"round"`
	Prompt     string               `json:"prompt"`
	PromptHash string               `json:"promptHash"`
	SurfaceID  string               `json:"surfaceId"`
	Reason     string               `json:"reason"`
	PatchSet   *promptiter.PatchSet `json:"-"`
	Profile    *promptiter.Profile  `json:"-"`
}

// PromptSnapshot records prompt content and integrity hash for audit.
type PromptSnapshot struct {
	ID             string `json:"id"`
	Content        string `json:"content"`
	SHA256         string `json:"sha256"`
	SemanticSHA256 string `json:"semanticSha256"`
	SurfaceID      string `json:"surfaceId"`
	PatchReason    string `json:"patchReason,omitempty"`
}

// EvaluationPair keeps train and validation summaries together.
type EvaluationPair struct {
	Train      EvaluationSummary `json:"train"`
	Validation EvaluationSummary `json:"validation"`
}

// DeltaPair keeps train and validation deltas together.
type DeltaPair struct {
	Train      DeltaSummary `json:"train"`
	Validation DeltaSummary `json:"validation"`
}

// RoundReport is the complete audit record for one optimization round.
type RoundReport struct {
	Round        int            `json:"round"`
	Candidate    PromptSnapshot `json:"candidatePrompt"`
	Evaluation   EvaluationPair `json:"evaluation"`
	Delta        DeltaPair      `json:"delta"`
	GateDecision GateDecision   `json:"gateDecision"`
	Usage        Usage          `json:"usage"`
}

// CostLatencySummary compares execution consumption for the reported prompt.
type CostLatencySummary struct {
	Baseline  Usage      `json:"baseline"`
	Candidate Usage      `json:"candidate"`
	Delta     UsageDelta `json:"delta"`
	TotalRun  Usage      `json:"totalRun"`
}

// Report is the stable optimization_report.json schema.
type Report struct {
	SchemaVersion           string                  `json:"schemaVersion"`
	RunID                   string                  `json:"runId"`
	Mode                    string                  `json:"mode"`
	Seed                    int64                   `json:"seed"`
	StartedAt               time.Time               `json:"startedAt"`
	CompletedAt             time.Time               `json:"completedAt"`
	WallTimeMS              int64                   `json:"wallTimeMs"`
	ModelConfig             FakeEngineConfig        `json:"modelConfig"`
	BaselinePrompt          PromptSnapshot          `json:"baselinePrompt"`
	Baseline                EvaluationPair          `json:"baseline"`
	CandidatePrompt         PromptSnapshot          `json:"candidatePrompt"`
	Candidate               EvaluationPair          `json:"candidate"`
	Delta                   DeltaPair               `json:"delta"`
	GateDecision            GateDecision            `json:"gateDecision"`
	FailureAttributionStats map[FailureCategory]int `json:"failureAttributionStats"`
	CostLatencySummary      CostLatencySummary      `json:"costLatencySummary"`
	Rounds                  []RoundReport           `json:"rounds"`
}
