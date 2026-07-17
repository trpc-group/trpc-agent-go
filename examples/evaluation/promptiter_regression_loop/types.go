//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

// FailureCategory identifies the most likely source of a failed evaluation case.
type FailureCategory string

const (
	// FailureCategoryModel means the model failed despite sufficient instructions and context.
	FailureCategoryModel FailureCategory = "model"
	// FailureCategoryPrompt means the prompt was ambiguous, incomplete, or not followed.
	FailureCategoryPrompt FailureCategory = "prompt"
	// FailureCategoryAgentTool means an agent orchestration step or tool invocation failed.
	FailureCategoryAgentTool FailureCategory = "agent_tool"
	// FailureCategoryEnvironment means an external dependency or runtime environment failed.
	FailureCategoryEnvironment FailureCategory = "environment"
	// FailureCategoryFormat means the response did not satisfy the required output format.
	FailureCategoryFormat FailureCategory = "format"
	// FailureCategoryKnowledge means required knowledge or retrieved context was missing.
	FailureCategoryKnowledge FailureCategory = "knowledge"
	// FailureCategoryUnknown means the available evidence cannot support a more specific cause.
	FailureCategoryUnknown FailureCategory = "unknown"
)

// TraceStep is a compact, provider-independent execution trace used for attribution.
type TraceStep struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// AttributionInput contains explicit signals and trace evidence for failure attribution.
// Explicit signals take precedence over keyword inference from Error and Output.
type AttributionInput struct {
	Passed             bool        `json:"passed"`
	Error              string      `json:"error,omitempty"`
	Output             string      `json:"output,omitempty"`
	Trace              []TraceStep `json:"trace,omitempty"`
	EnvironmentFailure bool        `json:"environment_failure,omitempty"`
	ToolFailure        bool        `json:"tool_failure,omitempty"`
	FormatFailure      bool        `json:"format_failure,omitempty"`
	KnowledgeMissing   bool        `json:"knowledge_missing,omitempty"`
	PromptMismatch     bool        `json:"prompt_mismatch,omitempty"`
	ModelFailure       bool        `json:"model_failure,omitempty"`
}

// AttributionResult records a deterministic category together with human-readable evidence.
type AttributionResult struct {
	Category    FailureCategory `json:"category"`
	Confidence  float64         `json:"confidence"`
	Explanation string          `json:"explanation"`
	Evidence    []string        `json:"evidence,omitempty"`
}

// Usage records model consumption. Tokens is InputTokens plus OutputTokens.
type Usage struct {
	Calls        int     `json:"calls"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostCNY      float64 `json:"cost_cny"`
}

// Tokens returns the total number of input and output tokens.
func (u Usage) Tokens() int {
	return u.InputTokens + u.OutputTokens
}

// Add returns the component-wise sum of two usage values.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		Calls:        u.Calls + other.Calls,
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		CostCNY:      u.CostCNY + other.CostCNY,
	}
}

// CaseRun is one independent run of an evaluation case.
type CaseRun struct {
	Score         float64           `json:"score"`
	Passed        bool              `json:"passed"`
	HardFailure   bool              `json:"hard_failure,omitempty"`
	Output        string            `json:"output,omitempty"`
	Error         string            `json:"error,omitempty"`
	Trace         []TraceStep       `json:"trace,omitempty"`
	LatencyMillis int64             `json:"latency_millis"`
	Usage         Usage             `json:"usage"`
	Attribution   AttributionResult `json:"attribution"`
}

// CaseEvaluation groups repeated independent runs for one evaluation case.
type CaseEvaluation struct {
	ID       string    `json:"id"`
	Critical bool      `json:"critical,omitempty"`
	Runs     []CaseRun `json:"runs"`
}

// CaseDelta compares aggregate baseline and candidate behavior for one case.
type CaseDelta struct {
	ID                  string  `json:"id"`
	Critical            bool    `json:"critical,omitempty"`
	BaselineMeanScore   float64 `json:"baseline_mean_score"`
	CandidateMeanScore  float64 `json:"candidate_mean_score"`
	ScoreDelta          float64 `json:"score_delta"`
	BaselinePassRate    float64 `json:"baseline_pass_rate"`
	CandidatePassRate   float64 `json:"candidate_pass_rate"`
	BaselinePassPowerK  bool    `json:"baseline_pass_power_k"`
	CandidatePassPowerK bool    `json:"candidate_pass_power_k"`
	NewHardFailure      bool    `json:"new_hard_failure,omitempty"`
	CriticalRegression  bool    `json:"critical_regression,omitempty"`
}

// Comparison is a validation-only comparison between baseline and candidate prompts.
type Comparison struct {
	PassK                   int         `json:"pass_k"`
	Deltas                  []CaseDelta `json:"deltas"`
	BaselineMeanScore       float64     `json:"baseline_mean_score"`
	CandidateMeanScore      float64     `json:"candidate_mean_score"`
	MeanScoreGain           float64     `json:"mean_score_gain"`
	BaselinePassPowerKRate  float64     `json:"baseline_pass_power_k_rate"`
	CandidatePassPowerKRate float64     `json:"candidate_pass_power_k_rate"`
	Usage                   Usage       `json:"usage"`
}

// ConfidenceInterval is a two-sided confidence interval for the paired mean delta.
type ConfidenceInterval struct {
	Confidence float64 `json:"confidence"`
	Lower      float64 `json:"lower"`
	Upper      float64 `json:"upper"`
}

// GateConfig configures prompt regression acceptance checks. A zero budget disables that budget.
type GateConfig struct {
	MinScoreGain       float64 `json:"min_score_gain"`
	PassK              int     `json:"pass_k"`
	BootstrapSeed      int64   `json:"bootstrap_seed"`
	BootstrapResamples int     `json:"bootstrap_resamples"`
	MaxCalls           int     `json:"max_calls"`
	MaxTokens          int     `json:"max_tokens"`
	MaxCostCNY         float64 `json:"max_cost_cny"`
}

// GateCheck is one auditable acceptance predicate.
type GateCheck struct {
	Name      string  `json:"name"`
	Passed    bool    `json:"passed"`
	Observed  float64 `json:"observed"`
	Threshold float64 `json:"threshold"`
	Operator  string  `json:"operator"`
	Detail    string  `json:"detail"`
}

// GateResult is the final acceptance decision and every predicate behind it.
type GateResult struct {
	Accepted           bool               `json:"accepted"`
	ConfidenceInterval ConfidenceInterval `json:"confidence_interval"`
	Checks             []GateCheck        `json:"checks"`
	FailedChecks       []string           `json:"failed_checks,omitempty"`
}
