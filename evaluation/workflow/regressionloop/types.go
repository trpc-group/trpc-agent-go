//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package regressionloop implements an evaluation-optimization regression pipeline
// that wraps the PromptIter engine to add failure attribution, enhanced acceptance
// gates, overfitting detection, and audit report generation.
package regressionloop

// FailureCategory classifies why an evaluation case failed.
type FailureCategory string

const (
	// FailureFinalResponseMismatch means the final response did not match expectation.
	FailureFinalResponseMismatch FailureCategory = "final_response_mismatch"

	// FailureToolCallError means the agent called the wrong tool or missed a required call.
	FailureToolCallError FailureCategory = "tool_call_error"

	// FailureToolArgumentError means the agent called the right tool with wrong arguments.
	FailureToolArgumentError FailureCategory = "tool_argument_error"

	// FailureRouteError means the agent dispatched to the wrong sub-agent or route.
	FailureRouteError FailureCategory = "route_error"

	// FailureFormatError means the output had formatting problems (JSON, XML, length).
	FailureFormatError FailureCategory = "format_error"

	// FailureKnowledgeRecall means the agent lacked required knowledge or context.
	FailureKnowledgeRecall FailureCategory = "knowledge_recall"

	// FailureHallucination means the agent fabricated unsupported information.
	FailureHallucination FailureCategory = "hallucination"

	// FailureQualityBelowThreshold means the overall quality score was below the metric threshold.
	FailureQualityBelowThreshold FailureCategory = "quality_below_threshold"

	// FailureUnknown is the fallback when no rule matched.
	FailureUnknown FailureCategory = "unknown"
)

// FailureAttribution records the classification result for one failed metric.
type FailureAttribution struct {
	// EvalSetID identifies the evaluation set.
	EvalSetID string `json:"eval_set_id"`
	// EvalCaseID identifies the specific case that failed.
	EvalCaseID string `json:"eval_case_id"`
	// MetricName identifies which metric failed.
	MetricName string `json:"metric_name"`
	// Category is the classified failure reason.
	Category FailureCategory `json:"category"`
	// Reason is the original metric failure reason.
	Reason string `json:"reason"`
	// Score is the metric score that failed the threshold.
	Score float64 `json:"score"`
	// Explanation describes why this category was chosen.
	Explanation string `json:"explanation"`
}

// CaseDelta records per-case comparison between baseline and candidate.
type CaseDelta struct {
	// EvalSetID identifies the evaluation set.
	EvalSetID string `json:"eval_set_id"`
	// EvalCaseID identifies the case.
	EvalCaseID string `json:"eval_case_id"`
	// BaselineScore is the baseline metric score for this case.
	BaselineScore float64 `json:"baseline_score"`
	// CandidateScore is the candidate metric score for this case.
	CandidateScore float64 `json:"candidate_score"`
	// ScoreDelta is candidate minus baseline.
	ScoreDelta float64 `json:"score_delta"`
	// BaselineStatus is pass/fail for baseline.
	BaselineStatus string `json:"baseline_status"`
	// CandidateStatus is pass/fail for candidate.
	CandidateStatus string `json:"candidate_status"`
	// IsNewPass means baseline failed but candidate passed.
	IsNewPass bool `json:"is_new_pass"`
	// IsNewFailure means baseline passed but candidate failed.
	IsNewFailure bool `json:"is_new_failure"`
	// IsRegression means both failed but score decreased.
	IsRegression bool `json:"is_regression"`
	// IsImprovement means both passed but score increased.
	IsImprovement bool `json:"is_improvement"`
}

// GateDecision records the result of the enhanced acceptance gate.
type GateDecision struct {
	// Accepted is true if all gate rules passed.
	Accepted bool `json:"accepted"`
	// Reasons lists each rule evaluation result.
	Reasons []GateRuleResult `json:"reasons"`
	// OverfittingDetected flags train-improve-but-validation-degrade.
	OverfittingDetected bool `json:"overfitting_detected"`
	// Summary is a human-readable decision explanation.
	Summary string `json:"summary"`
}

// GateRuleResult records one gate rule evaluation.
type GateRuleResult struct {
	// Rule identifies the gate rule that was evaluated.
	Rule string `json:"rule"`
	// Passed indicates whether the rule passed.
	Passed bool `json:"passed"`
	// Detail provides rule-specific context.
	Detail string `json:"detail"`
}
